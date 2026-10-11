package main

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func testDevelopmentSignup(t *testing.T, dsn string, owner, db *sql.DB, app *api, public testClient, existingBusiness string) {
	public.t, public.Token = t, ""
	ctx := context.Background()
	s, err := openWhatsAppService(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var devices []*store.Device
	var devicesMu sync.Mutex
	defer func() {
		s.cancel()
		s.wg.Wait()
		for _, device := range devices {
			if device.ID != nil {
				if err := device.Delete(ctx); err != nil {
					t.Error(err)
				}
			}
		}
		s.close()
	}()
	previous, previousSignup := app.whatsapp, app.developmentSignup
	app.whatsapp = s
	defer func() { app.whatsapp, app.developmentSignup = previous, previousSignup }()
	clients := make(chan *fakePairingClient, 16)
	s.newClient = func(device *store.Device) pairingClient {
		devicesMu.Lock()
		devices = append(devices, device)
		devicesMu.Unlock()
		f := &fakePairingClient{device: device, qr: make(chan whatsmeow.QRChannelItem, 8), phone: make(chan string, 1)}
		clients <- f
		return f
	}
	reset := func() { app.limiter = loginLimiter{windows: make(map[string]loginWindow)} }
	start := func(phone string) (string, actor, string) {
		reset()
		status, obj, _ := public.request("POST", "/api/auth/whatsapp/start", map[string]string{"phone_number": phone, "device_name": "Signup phone"}, "")
		expectStatus(t, 202, status, obj)
		if len(obj) != 3 {
			t.Fatal("start exposed identity or changed the contract")
		}
		token := field(t, obj, "attempt_token")
		hash, _ := secretHash(token)
		var user, business, account sql.NullString
		if err := owner.QueryRow(`SELECT COALESCE(signup_user_id,user_id)::text,COALESCE(signup_business_id,business_id)::text,
            COALESCE(signup_account_id,whatsapp_account_id)::text FROM ipo_pos.login_attempts WHERE token_hash=$1`, hash).
			Scan(&user, &business, &account); err != nil {
			t.Fatal(err)
		}
		return token, actor{UserID: user.String, BusinessID: business.String}, account.String
	}
	exchange := func(token string) (int, map[string]any) {
		status, obj, _ := public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": token}, "")
		return status, obj
	}
	noRecords := func(user actor, account string) {
		t.Helper()
		var count int
		if err := owner.QueryRow(`SELECT (SELECT count(*) FROM ipo_pos.users WHERE id=$1)
            +(SELECT count(*) FROM ipo_pos.businesses WHERE id=$2)
            +(SELECT count(*) FROM ipo_pos.business_memberships WHERE user_id=$1)
            +(SELECT count(*) FROM ipo_pos.whatsapp_accounts WHERE id=$3)`, user.UserID, user.BusinessID, account).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unapproved signup created records: count=%d err=%v", count, err)
		}
	}
	pair := func(f *fakePairingClient, number string) {
		jid := types.NewJID(number, types.DefaultUserServer)
		jid.Device = 9
		f.device.ID = &jid
		f.device.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
		if err := f.device.Save(ctx); err != nil {
			t.Fatal(err)
		}
		f.handler(&events.PairSuccess{ID: jid})
		f.qr <- whatsmeow.QRChannelSuccess
		f.handler(&events.ManualLoginReconnect{})
	}
	// The default/production path retains its generic pending response for unknown numbers.
	production, _, _ := start("+919000003001")
	status, obj := exchange(production)
	expectStatus(t, 401, status, obj)
	if len(clients) != 0 {
		t.Fatal("production enrolled an unknown number")
	}
	app.developmentSignup, s.developmentSignup = true, true
	attempt, user, account := start("+919000003002")
	awaitConnection(t, s, user.BusinessID, account, "awaiting_phone")
	fake := <-clients
	if requested := <-fake.phone; requested != "919000003002" && requested != "+919000003002" {
		t.Fatal("wrong signup pairing number")
	}
	noRecords(user, account)
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/status", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "code") != "ABCD-EFGH" {
		t.Fatal("signup did not receive its private linking code")
	}
	status, obj = exchange(attempt)
	expectStatus(t, 401, status, obj)
	duplicate, _, _ := start("+919000003002")
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/status", map[string]string{"attempt_token": duplicate}, "")
	expectStatus(t, 200, status, obj)
	if _, leaked := obj["code"]; leaked || len(clients) != 0 {
		t.Fatal("overlapping signup inherited a proof or started another worker")
	}
	pair(fake, "919000003002")
	awaitConnection(t, s, user.BusinessID, account, "connected")
	var name, role string
	var users, businesses int
	if err := owner.QueryRow(`SELECT u.name,m.role,(SELECT count(*) FROM ipo_pos.users WHERE phone_number=u.phone_number),
        (SELECT count(*) FROM ipo_pos.business_memberships WHERE user_id=u.id)
        FROM ipo_pos.users u JOIN ipo_pos.business_memberships m ON m.user_id=u.id WHERE u.id=$1`, user.UserID).Scan(&name, &role, &users, &businesses); err != nil || name != "+919000003002" || role != "admin" || users != 1 || businesses != 1 {
		t.Fatalf("incorrect signup identity: %v", err)
	}
	if user.BusinessID == existingBusiness {
		t.Fatal("signup joined an existing business")
	}
	// Redeeming the verified proof concurrently still creates exactly one session.
	reset()
	var wg sync.WaitGroup
	outcomes := make(chan map[string]any, 2)
	for range 2 {
		wg.Go(func() {
			status, obj := exchange(attempt)
			if status == 200 {
				outcomes <- obj
			} else if status != 401 {
				t.Errorf("unexpected signup exchange: %d %v", status, obj)
			}
		})
	}
	wg.Wait()
	if len(outcomes) != 1 {
		t.Fatal("signup proof was not single-use")
	}
	obj = <-outcomes
	if field(t, obj, "user_id") != user.UserID || field(t, obj, "whatsapp_account_id") != account {
		t.Fatal("signup exchange changed identity")
	}
	signedIn := public
	signedIn.Token = field(t, obj, "access_token")
	refresh := field(t, obj, "refresh_token")
	status, obj, _ = signedIn.request("GET", "/api/businesses/"+user.BusinessID+"/staff", nil, "")
	expectStatus(t, 200, status, obj)
	status, obj, _ = signedIn.request("GET", "/api/businesses/"+existingBusiness+"/staff", nil, "")
	expectStatus(t, 403, status, obj)
	status, obj, _ = public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refresh}, "")
	expectStatus(t, 200, status, obj)
	status, obj = exchange(duplicate)
	expectStatus(t, 401, status, obj)
	if _, err := db.Exec(`UPDATE ipo_pos.login_attempts SET signup_user_id=gen_random_uuid()`); err == nil {
		t.Fatal("runtime can forge signup identities")
	}
	// Revoke and return through the existing login path without creating another business.
	fake.handler(&events.LoggedOut{})
	awaitConnection(t, s, user.BusinessID, account, "needs_relink")
	attempt, returned, returnedAccount := start("+919000003002")
	if returned != user || returnedAccount != account {
		t.Fatal("returning login did not reuse the signup identity")
	}
	awaitConnection(t, s, user.BusinessID, account, "awaiting_phone")
	fake = <-clients
	pair(fake, "919000003002")
	awaitConnection(t, s, user.BusinessID, account, "connected")
	status, obj = exchange(attempt)
	expectStatus(t, 200, status, obj)
	// Wrong-number approval, expired proofs and production-mode workers cannot provision.
	for i, want := range []string{"invalid_identity", "binding_failed", "binding_failed", "binding_failed"} {
		phone := []string{"+919000003003", "+919000003004", "+919000003005", "+919000003007"}[i]
		token, reserved, pendingAccount := start(phone)
		awaitConnection(t, s, reserved.BusinessID, pendingAccount, "awaiting_phone")
		f := <-clients
		approved := phone[1:]
		if i == 0 {
			approved = "919000009999"
		}
		if i == 1 {
			hash, _ := secretHash(token)
			if _, err := owner.Exec(`UPDATE ipo_pos.login_attempts SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, hash); err != nil {
				t.Fatal(err)
			}
		}
		if i == 2 {
			s.developmentSignup = false
		}
		if i == 3 {
			// Administrative provisioning racing approval must roll back the reserved signup, not join that user.
			if _, err := owner.Exec(`INSERT INTO ipo_pos.users(name,phone_number) VALUES('Provisioned during pairing',$1)`, phone); err != nil {
				t.Fatal(err)
			}
		}
		pair(f, approved)
		awaitConnection(t, s, reserved.BusinessID, pendingAccount, want)
		s.developmentSignup = true
		noRecords(reserved, pendingAccount)
		status, obj = exchange(token)
		expectStatus(t, 401, status, obj)
	}
	// Existing disabled users/memberships and archived businesses are never bypassed by signup.
	for _, mutation := range []string{
		`UPDATE ipo_pos.users SET disabled_at=now() WHERE id=$1`,
		`UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE user_id=$1`,
		`UPDATE ipo_pos.businesses SET archived_at=now() WHERE id=(SELECT business_id FROM ipo_pos.business_memberships WHERE user_id=$1 LIMIT 1)`,
	} {
		if _, err := owner.Exec(`UPDATE ipo_pos.users SET disabled_at=NULL WHERE id=$1`, user.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(`UPDATE ipo_pos.business_memberships SET disabled_at=NULL WHERE user_id=$1`, user.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(`DELETE FROM ipo_pos.login_attempts WHERE phone_number='+919000003002'`); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(mutation, user.UserID); err != nil {
			t.Fatal(err)
		}
		token, _, _ := start("+919000003002")
		status, obj = exchange(token)
		expectStatus(t, 401, status, obj)
		if len(clients) != 0 {
			t.Fatal("development signup bypassed an existing account restriction")
		}
	}
	// Requests still reject caller-selected business/role and invalid international phones.
	for _, input := range []map[string]string{
		{"phone_number": "123", "device_name": "Phone"},
		{"phone_number": "+919000003006", "device_name": "Phone", "role": "admin"},
		{"phone_number": "+919000003006", "device_name": "Phone", "business_id": existingBusiness},
	} {
		reset()
		status, obj, _ = public.request("POST", "/api/auth/whatsapp/start", input, "")
		expectStatus(t, 400, status, obj)
	}
	// Per-number limits still apply to new numbers, including generic overlapping attempts.
	for i := range 4 {
		reset()
		status, obj, _ = public.request("POST", "/api/auth/whatsapp/start", map[string]string{"phone_number": "+919000003008", "device_name": "Rate limit phone"}, "")
		want := 202
		if i == 3 {
			want = 429
		}
		expectStatus(t, want, status, obj)
	}
}

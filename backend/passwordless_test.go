package main

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func testPasswordless(t *testing.T, dsn string, owner, db *sql.DB, app *api, admin, other testClient, business string) {
	admin.t, other.t = t, t
	ctx := context.Background()
	s, err := openWhatsAppService(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	previous := app.whatsapp
	app.whatsapp = s
	defer func() { app.whatsapp = previous }()
	clients := make(chan *fakePairingClient, 8)
	s.newClient = func(device *store.Device) pairingClient {
		f := &fakePairingClient{device: device, qr: make(chan whatsmeow.QRChannelItem, 8), phone: make(chan string, 1)}
		clients <- f
		return f
	}
	status, obj, _ := admin.request("POST", "/api/businesses/"+business+"/staff", map[string]string{"name": "Passwordless fixture", "phone_number": "+919000002001"}, "")
	expectStatus(t, 201, status, obj)
	user := field(t, obj, "user_id")
	public := admin
	public.Token = ""
	reset := func() { app.limiter = loginLimiter{windows: make(map[string]loginWindow)} }
	reset()
	enabled := app.passwordLogin
	app.passwordLogin = false
	status, obj, _ = public.request("POST", "/api/login", map[string]string{"phone_number": "+919000002001", "password": "irrelevant"}, "")
	expectStatus(t, 403, status, obj)
	app.passwordLogin = enabled
	start := func(phone string) string {
		status, obj, _ = public.request("POST", "/api/auth/whatsapp/start", map[string]string{"phone_number": phone, "device_name": "Original phone"}, "")
		expectStatus(t, 202, status, obj)
		if _, leaked := obj["user_id"]; leaked {
			t.Fatal("start leaked account identity")
		}
		return field(t, obj, "attempt_token")
	}
	unknown := start("+919000002999")
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/status", map[string]string{"attempt_token": unknown}, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "state") != "pending" || len(clients) != 0 {
		t.Fatal("unknown phone was enrolled")
	}
	attempt := start("+919000002001")
	var account string
	if err = owner.QueryRow(`SELECT id FROM ipo_pos.whatsapp_accounts WHERE linked_by_user_id=$1`, user).Scan(&account); err != nil {
		t.Fatal(err)
	}
	awaitConnection(t, s, business, account, "awaiting_phone")
	fake := <-clients
	if requested := <-fake.phone; requested != "919000002001" && requested != "+919000002001" {
		t.Fatal("wrong pairing number")
	}
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/status", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "code") != "ABCD-EFGH" {
		t.Fatal("attempt credential did not receive code")
	}
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 401, status, obj)
	concurrent := start("+919000002001")
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/status", map[string]string{"attempt_token": concurrent}, "")
	expectStatus(t, 200, status, obj)
	if _, leaked := obj["code"]; leaked {
		t.Fatal("another attempt inherited the pairing code")
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
	pair(fake, "919000002001")
	awaitConnection(t, s, business, account, "connected")
	if _, err = owner.Exec(`INSERT INTO ipo_pos.whatsapp_accounts(business_id,label,linked_by_user_id,device_jid,phone_number)
	VALUES($1,'Duplicate user',$2,'919000002001:10@s.whatsapp.net','+919000002002')`, business, user); err == nil {
		t.Fatal("second active user binding was accepted")
	}
	var adminID string
	if err = owner.QueryRow(`SELECT user_id FROM ipo_pos.business_memberships WHERE business_id=$1 AND role='admin' LIMIT 1`, business).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(`INSERT INTO ipo_pos.whatsapp_accounts(business_id,label,linked_by_user_id,device_jid,phone_number)
	VALUES($1,'Duplicate number',$2,'919000002001:11@s.whatsapp.net','+919000002001')`, business, adminID); err == nil {
		t.Fatal("second active number binding was accepted")
	}
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": concurrent}, "")
	expectStatus(t, 401, status, obj)
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 200, status, obj)
	original := public
	original.Token = field(t, obj, "access_token")
	refresh := field(t, obj, "refresh_token")
	originalID := field(t, obj, "session_id")
	if field(t, obj, "user_id") != user {
		t.Fatal("login recreated the user")
	}
	status, obj, _ = original.request("POST", "/api/businesses/"+business+"/whatsapp-accounts/"+account+"/connect", nil, "")
	expectStatus(t, 202, status, obj)
	if len(clients) != 0 {
		t.Fatal("connect replaced the passwordless worker")
	}
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 401, status, obj)
	status, obj, _ = original.request("GET", "/api/businesses/"+business+"/customers", nil, "")
	expectStatus(t, 200, status, obj)
	status, obj, _ = original.request("GET", "/api/sessions", nil, "")
	expectStatus(t, 200, status, obj)
	if len(obj["items"].([]any)) != 1 {
		t.Fatal("unexpected sessions")
	}
	if _, leaked := obj["token_hash"]; leaked {
		t.Fatal("session list leaked hashes")
	}

	reset()
	deviceToken := func() string {
		status, obj, _ = original.request("POST", "/api/auth/device-tokens", map[string]string{}, "")
		expectStatus(t, 201, status, obj)
		return field(t, obj, "device_token")
	}
	oldToken := deviceToken()
	transfer := deviceToken()
	exchangeDevice := func(token string) (int, map[string]any) {
		status, obj, _ := public.request("POST", "/api/auth/device-tokens/exchange", map[string]string{"device_token": token, "device_name": "Laptop"}, "")
		return status, obj
	}
	status, obj = exchangeDevice(oldToken)
	expectStatus(t, 401, status, obj)
	// Concurrent redemption must issue exactly one session.
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	var laptop testClient
	for range 3 {
		wg.Go(func() {
			status, obj := exchangeDevice(transfer)
			mu.Lock()
			defer mu.Unlock()
			if status == 200 {
				successes++
				laptop = public
				laptop.Token = field(t, obj, "access_token")
			} else if status != 401 {
				t.Errorf("unexpected redemption: %d %v", status, obj)
			}
		})
	}
	wg.Wait()
	if successes != 1 || len(clients) != 0 {
		t.Fatal("device exchange reused proof or paired another device")
	}
	status, obj, _ = original.request("GET", "/api/sessions", nil, "")
	expectStatus(t, 200, status, obj)
	if len(obj["items"].([]any)) != 2 {
		t.Fatal("devices do not have separate sessions")
	}
	status, obj, _ = laptop.request("GET", "/api/businesses/"+business+"/staff", nil, "")
	expectStatus(t, 403, status, obj)
	status, obj, _ = other.request("DELETE", "/api/sessions/"+originalID, nil, "")
	expectStatus(t, 204, status, obj)
	status, obj, _ = original.request("GET", "/api/me", nil, "")
	expectStatus(t, 200, status, obj)

	reset()
	// Refresh works after access expiry, rotates both credentials and retains device identity.
	originalHash, _ := secretHash(original.Token)
	if _, err = owner.Exec(`UPDATE ipo_pos.login_sessions SET expires_at=now()-interval '1 second',created_at=now()-interval '1 hour' WHERE token_hash=$1`, originalHash); err != nil {
		t.Fatal(err)
	}
	// Local legacy cleanup must not delete an expired access token that can still renew.
	status, obj, _ = public.request("POST", "/api/login", map[string]string{"phone_number": "+919000001001", "password": "fixture-password-12345"}, "")
	expectStatus(t, 200, status, obj)
	legacy := public
	legacy.Token = field(t, obj, "access_token")
	status, obj, _ = legacy.request("POST", "/api/auth/device-tokens", map[string]string{}, "")
	expectStatus(t, 403, status, obj)
	status, obj, _ = legacy.request("POST", "/api/logout", nil, "")
	expectStatus(t, 204, status, obj)
	status, obj, _ = original.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, obj)
	fresh, err := newAPI(db)
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(fresh.routes())
	defer restarted.Close()
	renewer := public
	renewer.URL = restarted.URL
	renewer.HTTP = restarted.Client()
	status, obj, _ = renewer.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refresh}, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "session_id") != originalID {
		t.Fatal("refresh changed device identity")
	}
	original.Token = field(t, obj, "access_token")
	refreshed := field(t, obj, "refresh_token")
	status, obj, _ = public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refresh}, "")
	expectStatus(t, 401, status, obj)
	status, obj, _ = laptop.request("POST", "/api/logout", nil, "")
	expectStatus(t, 204, status, obj)
	status, obj, _ = laptop.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, obj)
	status, obj, _ = original.request("GET", "/api/me", nil, "")
	expectStatus(t, 200, status, obj)
	fake.handler(&events.Disconnected{})
	awaitConnection(t, s, business, account, "reconnecting")
	status, obj, _ = original.request("GET", "/api/me", nil, "")
	expectStatus(t, 200, status, obj)
	fake.handler(&events.Connected{})
	awaitConnection(t, s, business, account, "connected")
	expired := deviceToken()
	hash, _ := secretHash(expired)
	if _, err = owner.Exec(`UPDATE ipo_pos.device_login_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, hash); err != nil {
		t.Fatal(err)
	}
	status, obj = exchangeDevice(expired)
	expectStatus(t, 401, status, obj)
	unused := deviceToken()
	// Hold the binding before concurrent authentication requests; revocation must
	// still be able to delete their source session/token without a lock inversion.
	locked, err := owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback()
	var lockedID string
	if err = locked.QueryRow(`SELECT id FROM ipo_pos.whatsapp_accounts WHERE id=$1 FOR UPDATE`, account).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	outcomes := make(chan int, 3)
	wg.Go(func() {
		code, _, _ := public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refreshed}, "")
		outcomes <- code
	})
	wg.Go(func() {
		code, _, _ := original.request("POST", "/api/auth/device-tokens", map[string]string{}, "")
		outcomes <- code
	})
	wg.Go(func() { code, _ := exchangeDevice(unused); outcomes <- code })
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err = owner.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FOR SHARE OF wa,u,m,b%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authentication did not reach binding locks")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = fake.device.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	fake.handler(&events.LoggedOut{})
	if _, err = locked.Exec(`UPDATE ipo_pos.whatsapp_accounts SET device_jid=NULL,unlinked_at=now() WHERE id=$1`, account); err != nil {
		t.Fatal("revocation deadlocked with authentication: " + err.Error())
	}
	if err = locked.Commit(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for range 3 {
		if code := <-outcomes; code != 401 {
			t.Fatalf("revoked concurrent request returned %d", code)
		}
	}
	s.mu.Lock()
	connection := s.accounts[business+"/"+account]
	s.mu.Unlock()
	select {
	case <-connection.done:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked worker did not stop")
	}
	status, obj, _ = original.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, obj)
	status, obj, _ = public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refreshed}, "")
	expectStatus(t, 401, status, obj)
	status, obj = exchangeDevice(unused)
	expectStatus(t, 401, status, obj)
	var retained int
	if err = owner.QueryRow(`SELECT count(*) FROM ipo_pos.users WHERE id=$1`, user).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("revocation deleted user")
	}

	reset()
	if _, err = owner.Exec(`DELETE FROM ipo_pos.login_attempts WHERE phone_number='+919000002001'`); err != nil {
		t.Fatal(err)
	}
	attempt = start("+919000002001")
	awaitConnection(t, s, business, account, "awaiting_phone")
	fake = <-clients
	pair(fake, "919000002001")
	awaitConnection(t, s, business, account, "connected")
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "user_id") != user || field(t, obj, "whatsapp_account_id") != account {
		t.Fatal("relink changed stable identity")
	}
	original.Token = field(t, obj, "access_token")
	status, obj, _ = public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refreshed}, "")
	expectStatus(t, 401, status, obj)
	transfer = deviceToken()
	status, obj = exchangeDevice(transfer)
	expectStatus(t, 200, status, obj)
	laptop.Token = field(t, obj, "access_token")
	status, obj, _ = original.request("POST", "/api/logout-all", nil, "")
	expectStatus(t, 204, status, obj)
	status, obj, _ = laptop.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, obj)
	// Restore a fixture-only session to exercise explicit remote unlink without new pairing.
	tx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err = tx.QueryRow(`SELECT auth_generation FROM ipo_pos.whatsapp_accounts WHERE id=$1`, account).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	session, err := issueLinkedSession(ctx, tx, user, business, account, generation, "Fixture", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	original.Token = session.AccessToken
	status, obj, _ = original.request("POST", "/api/businesses/"+business+"/whatsapp-accounts/"+account+"/disconnect", nil, "")
	expectStatus(t, 200, status, obj)
	awaitConnection(t, s, business, account, "needs_relink")
	status, obj, _ = original.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, obj)
	if _, err = db.Exec(`UPDATE ipo_pos.login_attempts SET state='verified'`); err == nil {
		t.Fatal("runtime can forge a login proof")
	}
	if _, err = db.Exec(`UPDATE ipo_pos.whatsapp_accounts SET auth_generation=123`); err == nil {
		t.Fatal("runtime can forge link generation")
	}
	if _, err = owner.Exec(`DELETE FROM ipo_pos.login_attempts WHERE expires_at<=now()`); err != nil {
		t.Fatal(err)
	}
	reset()
	attempt = start("+919000002001")
	awaitConnection(t, s, business, account, "awaiting_phone")
	fake = <-clients
	pair(fake, "919000002002")
	awaitConnection(t, s, business, account, "invalid_identity")
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 401, status, obj)
	if _, err = owner.Exec(`UPDATE ipo_pos.login_attempts SET expires_at=now()-interval '1 second' WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	attempt = start("+919000002001")
	awaitConnection(t, s, business, account, "awaiting_phone")
	fake = <-clients
	hash, _ = secretHash(attempt)
	if _, err = owner.Exec(`UPDATE ipo_pos.login_attempts SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, hash); err != nil {
		t.Fatal(err)
	}
	pair(fake, "919000002001")
	awaitConnection(t, s, business, account, "binding_failed")
	status, obj, _ = public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 401, status, obj)
}

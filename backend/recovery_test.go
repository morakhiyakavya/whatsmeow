package main

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func recoveryEvent(device *store.Device, message string) *events.Message {
	return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{
		Chat: device.GetJID().ToNonAD(), Sender: device.GetJID().ToNonAD(), IsFromMe: true},
		ID: "synthetic-approval", Timestamp: time.Now()}, Message: &waE2E.Message{Conversation: &message}}
}

func TestRecoveryMessageBoundary(t *testing.T) {
	jid := types.NewJID("919000002001", types.DefaultUserServer)
	jid.Device = 9
	lid := types.NewJID("12345", types.HiddenUserServer)
	device := &store.Device{ID: &jid, LID: lid}
	proof, _, _ := newSecret()
	message := recoveryPrefix + proof
	for _, test := range []struct {
		name   string
		change func(*events.Message)
		valid  bool
	}{
		{"primary self-chat", func(*events.Message) {}, true},
		{"primary LID self-chat", func(e *events.Message) { e.Info.Sender, e.Info.Chat = lid, lid }, true},
		{"other person", func(e *events.Message) { e.Info.IsFromMe = false }, false},
		{"wrong sender", func(e *events.Message) { e.Info.Sender.User = "919000002002" }, false},
		{"other chat", func(e *events.Message) { e.Info.Chat.User = "919000002002" }, false},
		{"linked device", func(e *events.Message) { e.Info.Sender.Device = 2 }, false},
		{"backend device", func(e *events.Message) { e.Info.Sender = jid }, false},
		{"group", func(e *events.Message) { e.Info.IsGroup = true }, false},
		{"history", func(e *events.Message) { e.SourceWebMsg = &waWeb.WebMessageInfo{} }, false},
		{"rerequest", func(e *events.Message) { e.UnavailableRequestID = "old" }, false},
		{"forwarded", func(e *events.Message) {
			forwarded := true
			e.Message = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &message, ContextInfo: &waE2E.ContextInfo{IsForwarded: &forwarded}}}
		}, false},
		{"edit", func(e *events.Message) { e.IsEdit = true }, false},
		{"edit metadata", func(e *events.Message) { e.Info.Edit = types.EditAttributeMessageEdit }, false},
		{"missing message", func(e *events.Message) { e.Message = nil }, false},
		{"missing ID", func(e *events.Message) { e.Info.ID = "" }, false},
		{"bad proof", func(e *events.Message) { text := recoveryPrefix + "guess"; e.Message.Conversation = &text }, false},
		{"quoted", func(e *events.Message) {
			e.Message = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &message, ContextInfo: &waE2E.ContextInfo{QuotedMessage: &waE2E.Message{}}}}
		}, false},
		{"extended text", func(e *events.Message) {
			e.Message = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &message}}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := recoveryEvent(device, message)
			test.change(event)
			if _, ok := recoveryMessageHash(device, event); ok != test.valid {
				t.Fatalf("accepted=%v", ok)
			}
		})
	}
}

func testRecovery(t *testing.T, owner, db *sql.DB, app *api, public testClient, s *whatsAppService, fake *fakePairingClient, user, business, account string) {
	t.Helper()
	ctx := context.Background()
	reset := func() { app.limiter = loginLimiter{windows: make(map[string]loginWindow)} }
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var snapshot string
		if err := owner.QueryRow(`SELECT jsonb_build_array(
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM ipo_pos.users t),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM ipo_pos.business_memberships t),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM ipo_pos.whatsapp_accounts t),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM ipo_pos.transactions t),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM ipo_pos.transaction_versions t),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM ipo_pos.manual_transaction_requests t),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM ipo_pos.transaction_confirmations t))::text`).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := snapshot()
	exec(`UPDATE ipo_pos.login_attempts SET created_at=now()-interval '2 minutes' WHERE phone_number='+919000002001'`)
	start := func(phone string) (string, string) {
		t.Helper()
		code, obj, headers := public.request("POST", "/api/auth/whatsapp/start", map[string]string{"phone_number": phone, "device_name": "Recovered Android"}, "")
		expectStatus(t, 202, code, obj)
		if headers.Get("Cache-Control") != "no-store" || len(obj) != 5 {
			t.Fatal("start exposes unexpected fields or permits caching")
		}
		return field(t, obj, "attempt_token"), field(t, obj, "recovery_message")
	}
	status := func(attempt string) map[string]any {
		t.Helper()
		code, obj, _ := public.request("POST", "/api/auth/whatsapp/status", map[string]string{"attempt_token": attempt}, "")
		expectStatus(t, 200, code, obj)
		return obj
	}
	exchange := func(attempt string) (int, map[string]any) {
		code, obj, _ := public.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
		return code, obj
	}
	var sessions int
	if err := db.QueryRow(`SELECT count(*) FROM ipo_pos.login_sessions WHERE user_id=$1`, user).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("recovery must begin with zero sessions")
	}
	reset()
	unknown, _ := start("+919000009999")
	attempt, message := start("+919000002001")
	for _, token := range []string{unknown, attempt} {
		obj := status(token)
		if field(t, obj, "state") != "pending" || field(t, obj, "next_step") != "send_recovery_message" || len(obj) != 3 {
			t.Fatal("pending recovery reveals an account or gives no next step")
		}
	}
	code, obj := exchange(attempt)
	expectStatus(t, 401, code, obj)
	code, obj = exchange(strings.TrimPrefix(message, recoveryPrefix))
	expectStatus(t, 401, code, obj)
	code, obj, _ = public.request("POST", "/api/logout-all", nil, "")
	expectStatus(t, 401, code, obj)
	code, obj, _ = public.request("POST", "/api/businesses/"+business+"/whatsapp-accounts/"+account+"/disconnect", nil, "")
	expectStatus(t, 401, code, obj)
	// Offline recovery stays bounded and cannot mint credentials or unlink anything.
	fake.handler(&events.Disconnected{})
	awaitConnection(t, s, business, account, "reconnecting")
	if field(t, status(attempt), "next_step") != "send_recovery_message" {
		t.Fatal("offline account leaked connectivity")
	}
	fake.handler(recoveryEvent(fake.device, message))
	// A lifecycle barrier ensures the disconnected worker has ignored the approval.
	fake.handler(&events.Connected{})
	awaitConnection(t, s, business, account, "connected")
	if field(t, status(attempt), "state") != "pending" {
		t.Fatal("offline worker approved recovery")
	}
	fresh, err := newAPI(db)
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(fresh.routes())
	defer restarted.Close()
	restartedClient := public
	restartedClient.URL, restartedClient.HTTP = restarted.URL, restarted.Client()
	code, obj, _ = restartedClient.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 401, code, obj)
	// Wrong-device and stale messages cannot approve the challenge.
	for _, mutate := range []func(*events.Message){
		func(e *events.Message) { e.Info.IsFromMe = false },
		func(e *events.Message) { e.Info.Timestamp = time.Now().Add(-time.Hour) },
		func(e *events.Message) { e.Info.Timestamp = time.Now().Add(time.Hour) },
		func(e *events.Message) { e.Info.Sender.Device = 9 },
	} {
		e := recoveryEvent(fake.device, message)
		mutate(e)
		if err := s.approveRecovery(ctx, fake.device, actor{UserID: user, BusinessID: business}, account, fake.device.GetJID().String(), e); err != nil {
			t.Fatal(err)
		}
		if field(t, status(attempt), "state") != "pending" {
			t.Fatal("unauthorized proof approved")
		}
	}
	reset()
	fake.handler(recoveryEvent(fake.device, message))
	deadline := time.Now().Add(5 * time.Second)
	for field(t, status(attempt), "state") != "verified" {
		if time.Now().After(deadline) {
			t.Fatal("live approval not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A new API instance with no WhatsApp worker can redeem the persisted proof.
	code, obj, _ = restartedClient.request("POST", "/api/auth/whatsapp/exchange", map[string]string{"attempt_token": attempt}, "")
	expectStatus(t, 200, code, obj)
	if field(t, obj, "user_id") != user || field(t, obj, "whatsapp_account_id") != account {
		t.Fatal("recovery changed identity")
	}
	first := public
	first.Token = field(t, obj, "access_token")
	code, obj, _ = first.request("GET", "/api/me", nil, "")
	expectStatus(t, 200, code, obj)
	reset()
	code, obj = exchange(attempt)
	expectStatus(t, 401, code, obj)
	exec(`UPDATE ipo_pos.login_attempts SET created_at=now()-interval '2 minutes' WHERE phone_number='+919000002001'`)
	// Expired approvals and replay of a previous message cannot verify a new attempt.
	expired, expiredMessage := start("+919000002001")
	expiredHash, _ := secretHash(expired)
	if err := s.approveRecovery(ctx, fake.device, actor{UserID: user, BusinessID: business}, account, fake.device.GetJID().String(), recoveryEvent(fake.device, expiredMessage)); err != nil {
		t.Fatal(err)
	}
	if field(t, status(expired), "state") != "verified" {
		t.Fatal("expiry fixture not approved")
	}
	exec(`UPDATE ipo_pos.login_attempts SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, expiredHash)
	fake.handler(recoveryEvent(fake.device, expiredMessage))
	code, obj = exchange(expired)
	expectStatus(t, 401, code, obj)
	firstAttempt, firstMessage := start("+919000002001")
	secondAttempt, secondMessage := start("+919000002001")
	_, _ = start("+919000002001")
	code, obj, headers := public.request("POST", "/api/auth/whatsapp/start", map[string]string{"phone_number": "+919000002001", "device_name": "Excess attempt"}, "")
	expectStatus(t, 429, code, obj)
	if headers.Get("Retry-After") != "60" {
		t.Fatal("missing retry limit")
	}
	fake.handler(recoveryEvent(fake.device, message))
	if field(t, status(firstAttempt), "state") != "pending" {
		t.Fatal("old approval verified a new attempt")
	}
	// Unverified requests preserve the active session and its push subscription.
	code, obj, _ = first.request("PUT", "/api/push-devices/20000000-0000-4000-8000-000000000001", map[string]string{"fcm_token": "recovery-fixture-token"}, "")
	expectStatus(t, 204, code, obj)
	code, obj = exchange(firstAttempt)
	expectStatus(t, 401, code, obj)
	var pushes int
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.push_devices WHERE user_id=$1`, user).Scan(&pushes); err != nil || pushes != 1 {
		t.Fatal("unverified recovery removed push registration")
	}
	for _, msg := range []string{firstMessage, secondMessage} {
		if err := s.approveRecovery(ctx, fake.device, actor{UserID: user, BusinessID: business}, account, fake.device.GetJID().String(), recoveryEvent(fake.device, msg)); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE ipo_pos.users SET disabled_at=now() WHERE id=$1`, user)
	reset()
	code, obj = exchange(firstAttempt)
	expectStatus(t, 401, code, obj)
	exec(`UPDATE ipo_pos.users SET disabled_at=NULL WHERE id=$1`, user)
	// Disablement clears push registrations independently; re-register before replacement.
	code, obj, _ = first.request("PUT", "/api/push-devices/20000000-0000-4000-8000-000000000001", map[string]string{"fcm_token": "recovery-fixture-token"}, "")
	expectStatus(t, 204, code, obj)
	reset()
	var wg sync.WaitGroup
	type outcome struct {
		code   int
		object map[string]any
	}
	results := make(chan outcome, 4)
	for _, token := range []string{firstAttempt, secondAttempt, firstAttempt, secondAttempt} {
		wg.Go(func() { code, object := exchange(token); results <- outcome{code, object} })
	}
	wg.Wait()
	close(results)
	winners := 0
	var recovered map[string]any
	for result := range results {
		if result.code == 200 {
			winners++
			recovered = result.object
		} else if result.code != 401 {
			t.Fatalf("concurrent exchange returned %d", result.code)
		}
	}
	if winners != 1 {
		t.Fatalf("recovery winners=%d", winners)
	}
	code, obj, _ = first.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, code, obj)
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.login_sessions WHERE user_id=$1`, user).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatal("replacement left multiple sessions")
	}
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.push_devices WHERE user_id=$1`, user).Scan(&pushes); err != nil || pushes != 0 {
		t.Fatal("replacement retained stale push registration")
	}
	if snapshot() != before {
		t.Fatal("recovery mutated business data, link or transaction retry identities")
	}
	var replacements int
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.auth_events WHERE user_id=$1 AND reason='recovery_replaced'`, user).Scan(&replacements); err != nil || replacements != 1 {
		t.Fatal("replacement reason not audited exactly once")
	}
	// Rejection details are persisted privately while responses remain generic.
	reset()
	refresh := field(t, recovered, "refresh_token")
	refreshHash, _ := secretHash(refresh)
	checkReason := func(want string) {
		t.Helper()
		code, obj, _ := public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refresh}, "")
		expectStatus(t, 401, code, obj)
		if field(t, obj["error"].(map[string]any), "code") != "invalid_token" {
			t.Fatal("private rejection reason leaked")
		}
		var reason string
		if err := owner.QueryRow(`SELECT reason FROM ipo_pos.auth_events WHERE user_id=$1 ORDER BY occurred_at DESC LIMIT 1`, user).Scan(&reason); err != nil || reason != want {
			t.Fatalf("reason=%s, want %s, err=%v", reason, want, err)
		}
	}
	exec(`UPDATE ipo_pos.users SET disabled_at=now() WHERE id=$1`, user)
	checkReason("user_disabled")
	exec(`UPDATE ipo_pos.users SET disabled_at=NULL WHERE id=$1`, user)
	exec(`UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE user_id=$1 AND business_id=$2`, user, business)
	checkReason("membership_disabled")
	exec(`UPDATE ipo_pos.business_memberships SET disabled_at=NULL WHERE user_id=$1 AND business_id=$2`, user, business)
	exec(`UPDATE ipo_pos.businesses SET archived_at=now() WHERE id=$1`, business)
	checkReason("business_archived")
	exec(`UPDATE ipo_pos.businesses SET archived_at=NULL WHERE id=$1`, business)
	exec(`UPDATE ipo_pos.login_sessions SET auth_generation=auth_generation+1 WHERE refresh_hash=$1`, refreshHash)
	checkReason("link_changed")
	exec(`UPDATE ipo_pos.login_sessions SET auth_generation=auth_generation-1 WHERE refresh_hash=$1`, refreshHash)
	code, obj, _ = public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": refresh}, "")
	expectStatus(t, 200, code, obj)
	var rotated int
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.auth_events WHERE user_id=$1 AND reason='refresh_rotated'`, user).Scan(&rotated); err != nil || rotated == 0 {
		t.Fatal("rotation not recorded")
	}
	old := refresh
	refresh = field(t, obj, "refresh_token")
	refreshHash, _ = secretHash(refresh)
	exec(`UPDATE ipo_pos.login_sessions SET created_at=now()-interval '1 day',refresh_expires_at=now()-interval '1 second' WHERE refresh_hash=$1`, refreshHash)
	checkReason("refresh_expired")
	code, obj, _ = public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": old}, "")
	expectStatus(t, 401, code, obj)
	var unknownCount int
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.auth_events WHERE user_id IS NULL AND reason='unknown_refresh'`).Scan(&unknownCount); err != nil || unknownCount == 0 {
		t.Fatal("unknown refresh not recorded")
	}
	if _, err := db.Exec(`UPDATE ipo_pos.login_attempts SET recovery_hash=NULL`); err == nil {
		t.Fatal("runtime can alter recovery proof")
	}
	if _, err := db.Exec(`DELETE FROM ipo_pos.auth_events`); err == nil {
		t.Fatal("runtime can erase diagnostics")
	}
	reset()
}

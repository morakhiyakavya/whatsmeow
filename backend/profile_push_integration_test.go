package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type pushSenderFunc func(context.Context, string, map[string]string) pushOutcome

func (f pushSenderFunc) send(ctx context.Context, token string, data map[string]string) pushOutcome {
	return f(ctx, token, data)
}

func TestProfilePushIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("use database/Test-Schema.ps1 -WithApiTests for isolated PostgreSQL tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role, database string
	if err = db.QueryRow(`SELECT current_user,current_database()`).Scan(&role, &database); err != nil || role != "schema_test" {
		t.Fatal("requires the disposable schema_test cluster")
	}
	grants, err := os.ReadFile("../database/api_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE ROLE ipo_push_test NOLOGIN;" + strings.NewReplacer(`:"api_role"`, `"ipo_push_test"`, `:"database_name"`, pgx.Identifier{database}.Sanitize()).Replace(string(grants))); err != nil {
		t.Fatal(err)
	}
	config, _ := pgx.ParseConfig(dsn)
	config.RuntimeParams["role"] = "ipo_push_test"
	runtime := stdlib.OpenDB(*config)
	defer runtime.Close()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var business, user, otherUser, account string
	if err = db.QueryRow(`INSERT INTO ipo_pos.businesses(name) VALUES('Push fixture') RETURNING id`).Scan(&business); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO ipo_pos.users(name,phone_number) VALUES('Push owner','+919000005001') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO ipo_pos.users(name,phone_number) VALUES('Push other','+919000005002') RETURNING id`).Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO ipo_pos.business_memberships(business_id,user_id,role) VALUES($1,$2,'admin'),($1,$3,'cashier')`, business, user, otherUser)
	const binding = "919000005001:1@s.whatsapp.net"
	if err = db.QueryRow(`INSERT INTO ipo_pos.whatsapp_accounts(business_id,label,linked_by_user_id,device_jid,phone_number)
		VALUES($1,'Push fixture',$2,$3,'+919000005001') RETURNING id`, business, user, binding).Scan(&account); err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(runtime)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.routes())
	defer server.Close()
	linkedSession := func() appSession {
		t.Helper()
		tx, err := runtime.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		result, err := issueLinkedSession(ctx, tx, user, business, account, 1, "Push phone", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	session := linkedSession()
	owner := testClient{t: t, URL: server.URL, HTTP: server.Client(), Token: session.AccessToken}
	otherToken, otherHash, _ := newSecret()
	exec(`INSERT INTO ipo_pos.login_sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, otherHash, otherUser)
	other := owner
	other.Token = otherToken
	public := owner
	public.Token = ""
	const installation = "10000000-0000-4000-8000-000000000001"
	const secondInstallation = "10000000-0000-4000-8000-000000000002"
	register := func(client testClient, id, token string) {
		t.Helper()
		status, object, headers := client.request("PUT", "/api/push-devices/"+id, map[string]string{"fcm_token": token}, "")
		expectStatus(t, 204, status, object)
		if len(object) != 0 || headers.Get("Cache-Control") != "no-store" {
			t.Fatal("registration leaked data or allowed caching")
		}
	}
	for _, test := range []struct {
		client testClient
		id     string
		body   map[string]string
		status int
	}{
		{public, installation, map[string]string{"fcm_token": "token"}, 401},
		{owner, "bad", map[string]string{"fcm_token": "token"}, 400},
		{owner, installation, map[string]string{}, 400},
		{owner, installation, map[string]string{"fcm_token": "bad token"}, 400},
		{owner, installation, map[string]string{"fcm_token": "token", "user_id": otherUser}, 400},
	} {
		status, object, _ := test.client.request("PUT", "/api/push-devices/"+test.id, test.body, "")
		expectStatus(t, test.status, status, object)
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	deviceGeneration := func(id string) string {
		t.Helper()
		var generation string
		if err := db.QueryRow(`SELECT generation FROM ipo_pos.push_devices WHERE installation_id=$1`, id).Scan(&generation); err != nil {
			t.Fatal(err)
		}
		return generation
	}
	register(owner, installation, "token-one")
	generation := deviceGeneration(installation)
	register(owner, strings.ToUpper(installation), "token-one")
	if deviceGeneration(installation) != generation {
		t.Fatal("idempotent registration changed its generation")
	}
	status, object, _ := public.request("POST", "/api/auth/refresh", map[string]string{"refresh_token": session.RefreshToken}, "")
	expectStatus(t, 200, status, object)
	owner.Token = field(t, object, "access_token")
	if field(t, object, "session_id") != session.SessionID {
		t.Fatal("refresh replaced stable session identity")
	}
	register(owner, installation, "token-one")
	if deviceGeneration(installation) != generation {
		t.Fatal("POS access-token rotation duplicated the registration")
	}
	register(owner, installation, "token-rotated")
	if deviceGeneration(installation) == generation || count(`SELECT count(*) FROM ipo_pos.push_devices WHERE fcm_token='token-one'`) != 0 {
		t.Fatal("FCM token rotation retained the old token")
	}
	register(other, installation, "token-rotated")
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE installation_id=$1 AND user_id=$2`, installation, user) != 0 {
		t.Fatal("installation remained subscribed to the previous user")
	}
	register(owner, installation, "token-owner")
	register(owner, secondInstallation, "token-second")
	const phone = "+919000005010"
	actor := actor{UserID: user, BusinessID: business}
	observe := func(state, id string, timestamp time.Time) string {
		t.Helper()
		revision, err := saveProfileObservation(ctx, db, actor, account, binding, phone, state, id, timestamp)
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	queueCount := func() int {
		return count(`SELECT count(*) FROM ipo_pos.profile_push_outbox WHERE business_id=$1`, business)
	}
	if observe("available", "photo-1", time.Time{}) != "1" || queueCount() != 0 {
		t.Fatal("initial lookup did not establish a revision without a notification")
	}
	if observe("available", "photo-1", time.Time{}) != "1" {
		t.Fatal("unchanged picture advanced revision")
	}
	timestamp := time.Now().UTC().Truncate(time.Second)
	if observe("changed", "photo-2", timestamp) != "2" || queueCount() != 2 {
		t.Fatal("event did not queue both installations")
	}
	if observe("changed", "photo-2", timestamp) != "2" || observe("not_set", "", timestamp.Add(-time.Second)) != "2" {
		t.Fatal("duplicate/stale event changed the revision")
	}
	if observe("available", "photo-2", time.Time{}) != "2" {
		t.Fatal("resolving the event advanced revision twice")
	}
	if observe("changed", "photo-2", timestamp.Add(time.Second)) != "2" || observe("available", "photo-2", time.Time{}) != "2" {
		t.Fatal("a repeat announcement of an already observed picture advanced revision")
	}
	if observe("hidden", "", time.Time{}) != "3" || observe("not_set", "", time.Time{}) != "4" || queueCount() != 2 {
		t.Fatal("hidden/unset transitions were not coalesced into the latest revision")
	}
	// A changed photo ID is detected even if the image URL stays the same (URLs are not stored).
	if observe("available", "photo-3", time.Time{}) != "5" {
		t.Fatal("picture replacement was not detected")
	}
	var mu sync.Mutex
	calls := map[string]int{}
	sender := pushSenderFunc(func(_ context.Context, token string, data map[string]string) pushOutcome {
		mu.Lock()
		defer mu.Unlock()
		calls[token]++
		if len(data) != 6 || data["type"] != "contact_profile_changed" || data["user_id"] != user || data["business_id"] != business || data["whatsapp_account_id"] != account || data["phone_number"] != phone || data["profile_revision"] != "5" {
			t.Error("FCM data leaked extra fields or has wrong account scope/revision")
		}
		if count(`SELECT count(*) FROM ipo_pos.contact_profile_revisions WHERE business_id=$1 AND profile_revision=5`, business) != 1 {
			t.Error("notification preceded the readable revision")
		}
		return pushOutcome{}
	})
	for range 2 {
		if found, err := deliverProfilePush(ctx, db, sender); err != nil || !found {
			t.Fatalf("delivery failed: found=%v err=%v", found, err)
		}
	}
	if calls["token-owner"] != 1 || calls["token-second"] != 1 || queueCount() != 0 {
		t.Fatal("authorised devices did not receive exactly one coalesced invalidation")
	}
	// Concurrent observations cannot lose increments or create duplicate outbox rows.
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			if _, err := saveProfileObservation(ctx, db, actor, account, binding, phone, "available", "concurrent-"+strconv.Itoa(i), time.Time{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if count(`SELECT count(*) FROM ipo_pos.contact_profile_revisions WHERE business_id=$1 AND profile_revision=13`, business) != 1 || queueCount() != 2 {
		t.Fatal("concurrent observations lost revisions or duplicated work")
	}
	// Runtime-role requests may register devices, but cannot forge revisions or notification jobs.
	for _, query := range []string{`UPDATE ipo_pos.contact_profile_revisions SET profile_revision=100`, `DELETE FROM ipo_pos.profile_push_outbox`, `INSERT INTO ipo_pos.profile_push_outbox DEFAULT VALUES`} {
		if _, err := runtime.Exec(query); err == nil {
			t.Fatal("runtime role can forge or erase notifications")
		}
	}
	exec(`DELETE FROM ipo_pos.profile_push_outbox WHERE business_id=$1`, business)
	// Retry scheduling survives process replacement, honours Retry-After and stops after five attempts.
	observe("available", "retry-photo", time.Time{})
	retrying := pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome {
		return pushOutcome{failed: true, retry: true, after: 3 * time.Minute}
	})
	if found, err := deliverProfilePush(ctx, db, retrying); err != nil || !found {
		t.Fatal("transient delivery did not run")
	}
	if count(`SELECT count(*) FROM ipo_pos.profile_push_outbox WHERE business_id=$1 AND attempts=1 AND next_attempt_at>now()+interval '170 seconds'`, business) != 1 {
		t.Fatal("retry was not persisted with Retry-After")
	}
	exec(`UPDATE ipo_pos.profile_push_outbox SET attempts=4,next_attempt_at=now() WHERE business_id=$1`, business)
	for range 2 {
		_, err := deliverProfilePush(ctx, db, retrying)
		if err != nil {
			t.Fatal(err)
		}
	}
	if queueCount() != 0 {
		t.Fatal("retry bound was exceeded")
	}
	// Terminal token rejection removes only the rejected generation, not a concurrent replacement.
	observe("available", "invalid-token-photo", time.Time{})
	replacing := pushSenderFunc(func(_ context.Context, token string, _ map[string]string) pushOutcome {
		if token == "token-owner" {
			register(owner, installation, "token-new")
		}
		return pushOutcome{failed: true, unregistered: true}
	})
	for range 2 {
		if _, err := deliverProfilePush(ctx, db, replacing); err != nil {
			t.Fatal(err)
		}
	}
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE installation_id=$1 AND fcm_token='token-new'`, installation) != 1 || count(`SELECT count(*) FROM ipo_pos.push_devices WHERE installation_id=$1`, secondInstallation) != 0 {
		t.Fatal("UNREGISTERED removed a replacement or retained the invalid token")
	}
	// Superseding a leased job while sending must preserve the newer revision.
	observe("available", "leased-photo", time.Time{})
	superseding := pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome {
		observe("available", "newer-photo", time.Time{})
		return pushOutcome{}
	})
	if _, err := deliverProfilePush(ctx, db, superseding); err != nil || queueCount() != 1 {
		t.Fatal("acknowledging old delivery discarded the newer invalidation")
	}
	// An expired lease is recoverable; no durable sending state wedges the queue.
	exec(`UPDATE ipo_pos.profile_push_outbox SET next_attempt_at=now()-interval '1 second',attempts=1 WHERE business_id=$1`, business)
	if _, err := deliverProfilePush(ctx, db, pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome { return pushOutcome{} })); err != nil || queueCount() != 0 {
		t.Fatal("expired lease was not recovered")
	}
	// Switching installation ownership invalidates previously queued jobs before send.
	observe("available", "switched-photo", time.Time{})
	register(other, installation, "token-other")
	never := pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome {
		t.Error("unauthorised or stale registration reached FCM")
		return pushOutcome{}
	})
	if _, err := deliverProfilePush(ctx, db, never); err != nil || queueCount() != 0 {
		t.Fatal("stale installation job was not discarded")
	}
	register(owner, installation, "token-owner-again")
	observe("available", "membership-photo", time.Time{})
	exec(`UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE business_id=$1 AND user_id=$2`, business, user)
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE user_id=$1`, user) != 0 || queueCount() != 0 {
		t.Fatal("disabled membership retained subscriptions")
	}
	exec(`UPDATE ipo_pos.business_memberships SET disabled_at=NULL WHERE business_id=$1 AND user_id=$2`, business, user)
	register(owner, installation, "token-enabled")
	observe("available", "expired-session-photo", time.Time{})
	exec(`UPDATE ipo_pos.login_sessions SET refresh_expires_at=now()-interval '30 minutes',created_at=now()-interval '1 hour',expires_at=now()-interval '1 second' WHERE id=$1`, session.SessionID)
	if _, err := deliverProfilePush(ctx, db, never); err != nil {
		t.Fatal(err)
	}
	// Explicit session revocation cascades to device registrations and their queued work.
	exec(`DELETE FROM ipo_pos.login_sessions WHERE id=$1`, session.SessionID)
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE user_id=$1`, user) != 0 {
		t.Fatal("session revocation retained registrations")
	}
	newSession := linkedSession()
	owner.Token = newSession.AccessToken
	register(owner, installation, "token-disable")
	exec(`UPDATE ipo_pos.users SET disabled_at=now() WHERE id=$1`, user)
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE user_id=$1`, user) != 0 {
		t.Fatal("disabled user retained registrations")
	}
	exec(`UPDATE ipo_pos.users SET disabled_at=NULL WHERE id=$1`, user)
	register(owner, installation, "token-logout")
	status, object, _ = owner.request("POST", "/api/logout", nil, "")
	expectStatus(t, 204, status, object)
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE user_id=$1`, user) != 0 {
		t.Fatal("logout retained registrations")
	}
	// Reusing a token on a new installation retires its old association.
	newSession = linkedSession()
	owner.Token = newSession.AccessToken
	register(owner, installation, "one-installation-only")
	register(owner, secondInstallation, "one-installation-only")
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE installation_id=$1`, installation) != 0 ||
		count(`SELECT count(*) FROM ipo_pos.push_devices WHERE fcm_token='one-installation-only'`) != 1 {
		t.Fatal("the same FCM token retained multiple subscriptions")
	}
	// Concurrent installs registering the same token resolve to one association.
	for _, id := range []string{installation, secondInstallation} {
		wg.Go(func() { register(owner, id, "concurrent-token") })
	}
	wg.Wait()
	if count(`SELECT count(*) FROM ipo_pos.push_devices WHERE fcm_token='concurrent-token'`) != 1 {
		t.Fatal("concurrent registration duplicated an FCM token")
	}
	// Neither schema identities nor contact scopes can be forged through raw constraints.
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO ipo_pos.push_devices(installation_id,fcm_token,user_id,session_id) VALUES(gen_random_uuid(),'forged-owner',$1,$2)`, []any{otherUser, newSession.SessionID}},
		{`UPDATE ipo_pos.contact_profile_revisions SET profile_revision=0 WHERE business_id=$1`, []any{business}},
		{`UPDATE ipo_pos.contact_profile_revisions SET phone_number='invalid' WHERE business_id=$1`, []any{business}},
		{`UPDATE ipo_pos.contact_profile_revisions SET business_id=gen_random_uuid() WHERE business_id=$1`, []any{business}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err == nil {
			t.Fatal("schema accepted a forged owner, revision or contact scope")
		}
	}
	exec(`DELETE FROM ipo_pos.push_devices WHERE user_id=$1`, user)
	register(owner, installation, "authorization-check")
	observe("available", "archived-business-photo", time.Time{})
	exec(`UPDATE ipo_pos.businesses SET archived_at=now() WHERE id=$1`, business)
	if _, err := deliverProfilePush(ctx, db, never); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE ipo_pos.businesses SET archived_at=NULL WHERE id=$1`, business)
	observe("available", "unlinked-photo", time.Time{})
	exec(`UPDATE ipo_pos.whatsapp_accounts SET device_jid=NULL,unlinked_at=now() WHERE business_id=$1 AND id=$2`, business, account)
	if queueCount() != 0 || count(`SELECT count(*) FROM ipo_pos.push_devices WHERE user_id=$1`, user) != 0 {
		t.Fatal("unlink did not revoke installations and pending notifications")
	}
	stop := startProfilePush(ctx, db, never)
	stop()
	if _, err := saveProfileObservation(ctx, db, actor, account, "wrong-binding", phone, "hidden", "", time.Time{}); !errors.Is(err, errProfileUnavailable) {
		t.Fatal("stale worker binding can change profile revisions")
	}
}

func testPictureEvents(t *testing.T, s *whatsAppService, client *fakePairingClient, owner testClient, path string) {
	t.Helper()
	const phone = "+919000006010"
	parts := strings.Split(path, "/")
	business, account := parts[3], parts[5]
	jid := types.NewJID(phone[1:], types.DefaultUserServer)
	lid := types.NewJID("888006010", types.HiddenUserServer)
	if err := client.device.LIDs.PutLIDMapping(context.Background(), lid, jid); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Truncate(time.Second)
	client.handler(&events.Picture{JID: lid, PictureID: "event-photo", Timestamp: timestamp})
	awaitRevision := func(revision int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			var got int
			err := s.db.QueryRow(`SELECT profile_revision FROM ipo_pos.contact_profile_revisions
				WHERE business_id=$1 AND whatsapp_account_id=$2 AND phone_number=$3`, business, account, phone).Scan(&got)
			if err == nil && got == revision {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("picture event did not persist revision %d", revision)
	}
	awaitRevision(1)
	client.handler(&events.Picture{JID: jid, Remove: true, Timestamp: timestamp.Add(time.Second)})
	awaitRevision(2)
	client.userInfo = func(context.Context, []types.JID) (map[types.JID]types.UserInfo, error) {
		return map[types.JID]types.UserInfo{jid: {}}, nil
	}
	client.pictureInfo = func(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
		return nil, whatsmeow.ErrProfilePictureNotSet
	}
	status, object, _ := owner.request("POST", path+"/contact-profile", map[string]string{"phone_number": phone}, "")
	expectStatus(t, 200, status, object)
	if object["profile_revision"] != "2" || object["picture_state"] != "not_set" {
		t.Fatal("profile endpoint did not expose the committed event revision")
	}
	client.pictureInfo = func(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
		return nil, errors.New("lookup unavailable")
	}
	status, object, _ = owner.request("POST", path+"/contact-profile", map[string]string{"phone_number": phone}, "")
	expectStatus(t, 502, status, object)
	awaitRevision(2)
	client.userInfo, client.pictureInfo = nil, nil
	client.handler(&events.PushName{JID: types.NewJID("unknown", types.HiddenUserServer), JIDAlt: jid, OldPushName: "Old", NewPushName: "New"})
	awaitRevision(3)
	client.handler(&events.BusinessName{JID: lid, OldBusinessName: "Old business", NewBusinessName: "New business"})
	awaitRevision(4)
	client.handler(&events.UserAbout{JID: jid, Status: "Private About", Timestamp: timestamp})
	awaitRevision(5)
	var pictureState string
	if err := s.db.QueryRow(`SELECT picture_state FROM ipo_pos.contact_profile_revisions
		WHERE business_id=$1 AND whatsapp_account_id=$2 AND phone_number=$3`, business, account, phone).Scan(&pictureState); err != nil || pictureState != "not_set" {
		t.Fatal("metadata invalidation changed the known photo state", err)
	}
	client.handler(&events.PushName{JID: jid, OldPushName: "Same", NewPushName: "Same"})
	client.handler(&events.UserAbout{JID: types.NewJID("group", types.GroupServer)})
	if phone, err := pictureEventPhone(context.Background(), client.device, types.NewJID("group", types.GroupServer)); err != nil || phone != "" {
		t.Fatal("group photo was treated as a contact")
	}
	if phone, err := pictureEventPhone(context.Background(), client.device, types.NewJID("unknown", types.HiddenUserServer)); err != nil || phone != "" {
		t.Fatal("unmapped LID was exposed as a phone number")
	}
}

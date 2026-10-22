package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type fakePairingClient struct {
	device      *store.Device
	handler     whatsmeow.EventHandler
	qr          chan whatsmeow.QRChannelItem
	phone       chan string
	sender      confirmationSender
	numberCheck func(context.Context, []string) ([]types.IsOnWhatsAppResponse, error)
	userInfo    func(context.Context, []types.JID) (map[types.JID]types.UserInfo, error)
	pictureInfo func(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error)
	connect     func(context.Context) error
}

func (f *fakePairingClient) GetUserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error) {
	if f.userInfo != nil {
		return f.userInfo(ctx, jids)
	}
	return nil, errors.New("unexpected profile lookup in pairing-only fixture")
}

func (f *fakePairingClient) GetProfilePictureInfo(ctx context.Context, jid types.JID, params *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
	if f.pictureInfo != nil {
		return f.pictureInfo(ctx, jid, params)
	}
	return nil, errors.New("unexpected picture lookup in pairing-only fixture")
}

func (f *fakePairingClient) IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	if f.numberCheck != nil {
		return f.numberCheck(ctx, phones)
	}
	return nil, errors.New("unexpected number check in pairing-only fixture")
}

func (f *fakePairingClient) AddEventHandler(h whatsmeow.EventHandler) uint32 { f.handler = h; return 1 }
func (f *fakePairingClient) Disconnect()                                     {}
func (f *fakePairingClient) Logout(ctx context.Context) error                { return f.device.Delete(ctx) }
func (f *fakePairingClient) GenerateMessageID() types.MessageID {
	if f.sender != nil {
		return f.sender.GenerateMessageID()
	}
	return whatsmeow.NewClient(f.device, nil).GenerateMessageID()
}
func (f *fakePairingClient) SendMessage(ctx context.Context, jid types.JID, msg *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	if f.sender != nil {
		return f.sender.SendMessage(ctx, jid, msg, extra...)
	}
	return whatsmeow.SendResponse{}, errors.New("unexpected send in pairing-only fixture")
}
func (f *fakePairingClient) GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return f.qr, nil
}
func (f *fakePairingClient) ConnectContext(ctx context.Context) error {
	if f.connect != nil {
		return f.connect(ctx)
	}
	if f.device.ID == nil {
		f.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "synthetic-qr", Timeout: time.Minute}
	} else {
		f.handler(&events.Connected{})
	}
	return nil
}
func (f *fakePairingClient) PairPhone(_ context.Context, phone string, _ bool, _ whatsmeow.PairClientType, _ string) (string, error) {
	f.phone <- phone
	return "ABCD-EFGH", nil
}

func TestConnectionCodeExpiry(t *testing.T) {
	c := &accountConnection{connectionState: connectionState{State: "awaiting_qr", Code: "secret", ExpiresAt: time.Now().Add(-time.Second)}}
	if c.state().Code != "" {
		t.Fatal("expired pairing secret was exposed")
	}
	c.update("connected", "", time.Time{})
	data, err := json.Marshal(c.state())
	if err != nil || strings.Contains(string(data), "secret") || strings.Contains(string(data), "expires_at") {
		t.Fatalf("connected state retained pairing data: %s, %v", data, err)
	}
}

func TestWhatsAppStorageErrorsHideCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, dsn := range []string{"postgres://user:private-secret@localhost:bad-port/db", "postgres://user:private-secret@127.0.0.1:1/db?sslmode=disable"} {
		service, err := openWhatsAppService(ctx, dsn)
		if service != nil || err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("storage error exposed credentials or accepted an invalid connection")
		}
	}
}

func awaitConnection(t *testing.T, s *whatsAppService, business, id, want string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		state, ok := s.state(business, id)
		if ok && state.State == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("connection state=%v, want %s", state, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func testWhatsAppPairing(t *testing.T, dsn string, db *sql.DB, app *api, admin, cashier, other testClient, businessID, cashierID string) {
	admin.t, cashier.t, other.t = t, t, t
	ctx := context.Background()
	s, err := openWhatsAppService(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	created := make(chan *fakePairingClient, 8)
	s.newClient = func(device *store.Device) pairingClient {
		client := &fakePairingClient{device: device, qr: make(chan whatsmeow.QRChannelItem, 8), phone: make(chan string, 1)}
		created <- client
		return client
	}
	app.whatsapp = s
	t.Cleanup(func() { app.whatsapp = nil })
	base := "/api/businesses/" + businessID + "/whatsapp-accounts"
	status, object, _ := cashier.request("POST", base, map[string]string{"label": "Pairing fixture"}, "")
	expectStatus(t, 201, status, object)
	id := field(t, object, "id")
	path := base + "/" + id
	status, object, _ = cashier.request("POST", path+"/check-number", map[string]string{"phone_number": "+919000001010"}, "")
	expectStatus(t, 409, status, object)
	status, object, _ = cashier.request("POST", path+"/contact-profile", map[string]string{"phone_number": "+919000001010"}, "")
	expectStatus(t, 409, status, object)
	for _, suffix := range []string{"/connection", "/qr", "/pair", "/connect", "/check-number", "/contact-profile"} {
		method := "GET"
		if suffix == "/pair" || suffix == "/connect" || suffix == "/check-number" || suffix == "/contact-profile" {
			method = "POST"
		}
		status, object, _ = admin.request(method, path+suffix, map[string]string{}, "")
		expectStatus(t, 403, status, object)
		status, object, _ = other.request(method, path+suffix, map[string]string{}, "")
		expectStatus(t, 403, status, object)
	}
	status, object, _ = cashier.request("POST", path+"/connect", nil, "")
	expectStatus(t, 409, status, object)
	for _, input := range []map[string]string{{"method": "bad"}, {"method": "phone", "phone_number": "919000001011"}, {"method": "qr", "phone_number": "+919000001011"}, {"method": "qr", "device_jid": "forged"}} {
		status, object, _ = cashier.request("POST", path+"/pair", input, "")
		expectStatus(t, 400, status, object)
	}
	status, object, _ = cashier.request("POST", path+"/pair", map[string]string{}, "")
	expectStatus(t, 202, status, object)
	awaitConnection(t, s, businessID, id, "awaiting_qr")
	fake := <-created
	status, object, _ = cashier.request("POST", path+"/pair", map[string]string{}, "")
	expectStatus(t, 202, status, object)
	if len(created) != 0 {
		t.Fatal("pairing retry created another client")
	}
	upperPath := "/api/businesses/" + strings.ToUpper(businessID) + "/whatsapp-accounts/" + strings.ToUpper(id)
	status, object, _ = cashier.request("POST", upperPath+"/pair", map[string]string{}, "")
	expectStatus(t, 202, status, object)
	if len(created) != 0 {
		t.Fatal("UUID case change bypassed active-client deduplication")
	}
	status, object, _ = cashier.request("GET", upperPath+"/connection", nil, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "code") != "synthetic-qr" {
		t.Fatal("UUID case change lost the connection state")
	}
	status, object, _ = cashier.request("GET", path+"/connection", nil, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "code") != "synthetic-qr" {
		t.Fatal("owner did not receive the QR data")
	}
	req, _ := http.NewRequest("GET", cashier.URL+path+"/qr", nil)
	req.Header.Set("Authorization", "Bearer "+cashier.Token)
	resp, err := cashier.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, pngErr := png.Decode(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" || pngErr != nil {
		t.Fatal("QR endpoint did not return an uncached PNG")
	}
	// A second process must fail before loading keys or opening a protocol client.
	second, err := openWhatsAppService(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	second.newClient = func(*store.Device) pairingClient { t.Error("lock failure opened a client"); return nil }
	_, err = second.start(actor{UserID: cashierID, BusinessID: businessID}, id, "qr", "")
	if err != nil {
		t.Fatal(err)
	}
	awaitConnection(t, second, businessID, id, "busy")
	second.close()

	jid := types.NewJID("999000000011", types.DefaultUserServer)
	jid.Device = 7
	fake.device.ID = &jid
	fake.device.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err = fake.device.Save(ctx); err != nil {
		t.Fatal(err)
	}
	fake.handler(&events.PairSuccess{ID: jid})
	fake.qr <- whatsmeow.QRChannelSuccess
	fake.handler(&events.ManualLoginReconnect{})
	awaitConnection(t, s, businessID, id, "connected")
	// A newly paired account must restore its saved keys after a storage outage.
	if _, err = s.db.Exec(`SELECT pg_terminate_backend(pid) FROM pg_locks
        WHERE locktype='advisory' AND granted AND objsubid=1
        AND classid::bigint=((hashtextextended($1,0)>>32)&4294967295)
        AND objid::bigint=(hashtextextended($1,0)&4294967295)`, "ipo-whatsapp/"+businessID+"/"+id); err != nil {
		t.Fatal(err)
	}
	select {
	case fake = <-created:
	case <-time.After(6 * time.Second):
		t.Fatal("paired account did not recover its lost database connection")
	}
	awaitConnection(t, s, businessID, id, "connected")
	testWhatsAppNumbers(t, db, cashier, other, s, fake, upperPath)
	testWhatsAppProfiles(t, cashier, other, s, fake, upperPath)
	testPictureEvents(t, s, fake, cashier, upperPath)
	status, object, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "pairing_state") != "paired" || field(t, object, "phone_number") != "+999000000011" || field(t, object, "id") != id {
		t.Fatal("verified pairing did not persist the stable mapping")
	}
	status, object, _ = cashier.request("GET", path+"/connection", nil, "")
	expectStatus(t, 200, status, object)
	if _, exposed := object["code"]; exposed {
		t.Fatal("paired account retained a pairing code")
	}
	status, object, _ = cashier.request("POST", path+"/connect", nil, "")
	expectStatus(t, 202, status, object)
	restored, err := openWhatsAppService(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.close)
	restored.newClient = s.newClient
	if err = restored.resume(); err != nil {
		t.Fatal(err)
	}
	awaitConnection(t, restored, businessID, id, "waiting_for_worker")
	if len(created) != 0 {
		t.Fatal("replacement worker opened a client before acquiring the locks")
	}
	if _, err = restored.start(actor{UserID: cashierID, BusinessID: businessID}, id, "", ""); err != nil {
		t.Fatal("waiting restore could not be reused", err)
	}
	cancelled, err := openWhatsAppService(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	cancelled.newClient = func(*store.Device) pairingClient { t.Error("waiting worker opened a client"); return nil }
	if err = cancelled.resume(); err != nil {
		cancelled.close()
		t.Fatal(err)
	}
	awaitConnection(t, cancelled, businessID, id, "waiting_for_worker")
	waiting, err := cancelled.start(actor{UserID: cashierID, BusinessID: businessID}, id, "", "")
	if err != nil {
		t.Fatal(err)
	}
	logoutResult := make(chan error, 1)
	select {
	case waiting.logout <- logoutResult:
	case <-time.After(5 * time.Second):
		t.Fatal("waiting worker did not accept disconnect")
	}
	select {
	case err := <-logoutResult:
		if err == nil {
			t.Fatal("waiting worker falsely confirmed remote unlink")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting worker did not answer disconnect")
	}
	awaitConnection(t, cancelled, businessID, id, "needs_relink")
	<-waiting.done
	if _, err = cancelled.start(actor{UserID: cashierID, BusinessID: businessID}, id, "", ""); err != nil {
		t.Fatal(err)
	}
	awaitConnection(t, cancelled, businessID, id, "waiting_for_worker")
	cancelled.close()
	awaitConnection(t, cancelled, businessID, id, "disconnected")
	s.close()
	awaitConnection(t, restored, businessID, id, "connected")
	resumed := <-created
	if resumed.device.GetJID() != jid {
		t.Fatal("restart loaded another device")
	}
	app.whatsapp = restored
	connection, err := restored.start(actor{UserID: cashierID, BusinessID: businessID}, id, "", "")
	if err != nil {
		t.Fatal(err)
	}
	resumed.handler(&events.StreamReplaced{})
	select {
	case <-connection.done:
	case <-time.After(5 * time.Second):
		t.Fatal("server rejection must stop automatic reconnects")
	}
	awaitConnection(t, restored, businessID, id, "connection_failed")
	if len(created) != 0 {
		t.Fatal("a rejected session reconnected automatically")
	}
	if _, err = restored.start(actor{UserID: cashierID, BusinessID: businessID}, id, "", ""); err != nil {
		t.Fatal(err)
	}
	awaitConnection(t, restored, businessID, id, "connected")
	resumed = <-created
	if err = resumed.device.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	resumed.handler(&events.LoggedOut{})
	awaitConnection(t, restored, businessID, id, "needs_relink")
	status, object, _ = cashier.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, object)
	app.limiter = loginLimiter{windows: make(map[string]loginWindow)}
	status, object, _ = cashier.request("POST", "/api/login", map[string]string{"phone_number": "+919000001003", "password": "cashier-password-12345"}, "")
	expectStatus(t, 200, status, object)
	cashier.Token = field(t, object, "access_token")
	status, object, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "pairing_state") != "unlinked" || field(t, object, "id") != id {
		t.Fatal("revocation lost the stable account identity")
	}
	status, object, _ = cashier.request("POST", path+"/pair", map[string]string{"method": "phone", "phone_number": "+999000000012"}, "")
	expectStatus(t, 202, status, object)
	awaitConnection(t, restored, businessID, id, "awaiting_phone")
	phoneClient := <-created
	if phone := <-phoneClient.phone; phone != "+999000000012" {
		t.Fatal("phone pairing used an incorrect number")
	}
	status, object, _ = cashier.request("GET", path+"/connection", nil, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "code") != "ABCD-EFGH" {
		t.Fatal("phone code was not returned to the owner")
	}
	phoneClient.qr <- whatsmeow.QRChannelTimeout
	awaitConnection(t, restored, businessID, id, "pairing_failed")
	status, object, _ = cashier.request("GET", path+"/connection", nil, "")
	expectStatus(t, 200, status, object)
	if _, exposed := object["code"]; exposed {
		t.Fatal("expired attempt retained its code")
	}
	// A stale mapping must require fresh pairing rather than adopt another session.
	if _, err = restored.db.Exec("UPDATE ipo_pos.whatsapp_accounts SET device_jid='999000000099:9@s.whatsapp.net',unlinked_at=NULL WHERE business_id=$1 AND id=$2", businessID, id); err != nil {
		t.Fatal(err)
	}
	status, object, _ = cashier.request("POST", path+"/connect", nil, "")
	expectStatus(t, 202, status, object)
	awaitConnection(t, restored, businessID, id, "needs_relink")
	status, object, _ = cashier.request("POST", "/api/login", map[string]string{"phone_number": "+919000001003", "password": "cashier-password-12345"}, "")
	expectStatus(t, 200, status, object)
	cashier.Token = field(t, object, "access_token")
	if len(created) != 0 {
		t.Fatal("missing credentials adopted an unrelated session")
	}
	if _, err = restored.db.Exec("UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE business_id=$1 AND user_id=$2", businessID, cashierID); err != nil {
		t.Fatal(err)
	}
	if err = restored.bind(ctx, actor{UserID: cashierID, BusinessID: businessID}, id, jid.String(), "+999000000011", nil); err == nil {
		t.Fatal("pairing persisted after the owner's membership was disabled")
	}
	if _, err = restored.db.Exec("UPDATE ipo_pos.business_memberships SET disabled_at=NULL WHERE business_id=$1 AND user_id=$2", businessID, cashierID); err != nil {
		t.Fatal(err)
	}
	// The ordinary API role still cannot claim a saved session directly.
	if _, err = db.Exec("UPDATE ipo_pos.whatsapp_accounts SET device_jid=$1 WHERE business_id=$2 AND id=$3", jid.String(), businessID, id); err == nil {
		t.Fatal("API role acquired protocol binding privileges")
	}
}

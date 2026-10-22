package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type fakeConfirmationSender struct {
	mu               sync.Mutex
	generated, calls int
	fail             bool
	IDs              []string
	phone, body      string
}

func (f *fakeConfirmationSender) GenerateMessageID() types.MessageID {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generated++
	return fmt.Sprintf("FIXTURE-%d", f.generated)
}
func (f *fakeConfirmationSender) SendMessage(_ context.Context, to types.JID, msg *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.IDs = append(f.IDs, extra[0].ID)
	f.phone, f.body = to.User, msg.GetConversation()
	if f.fail {
		return whatsmeow.SendResponse{}, errors.New("synthetic ambiguous send")
	}
	return whatsmeow.SendResponse{ID: extra[0].ID}, nil
}

func awaitConfirmation(t *testing.T, db *sql.DB, business, id, want string) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := db.QueryRow(`SELECT status FROM ipo_pos.transaction_confirmations WHERE business_id=$1 AND transaction_id=$2`, business, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("confirmation did not reach expected state: " + want)
}

func testConfirmationDelivery(t *testing.T, dsn string, db *sql.DB, cashier testClient, business, cashierID string, request map[string]any) {
	cashier.t = t
	ctx, capture := performanceContext(t)
	var account, id, jidText string
	if err := db.QueryRow(`SELECT q.whatsapp_account_id,q.transaction_id,q.sender_device_jid
        FROM ipo_pos.transaction_confirmations q JOIN ipo_pos.transactions t ON t.business_id=q.business_id AND t.id=q.transaction_id
        WHERE t.business_id=$1 AND t.idempotency_key='pos-confirmation'`, business).Scan(&account, &id, &jidText); err != nil {
		t.Fatal(err)
	}
	user := actor{UserID: cashierID, BusinessID: business}
	jid, err := types.ParseJID(jidText)
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeConfirmationSender{}
	open := func() *whatsAppService {
		s, err := openWhatsAppService(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.close)
		s.newClient = func(device *store.Device) pairingClient {
			if device.GetJID() != jid {
				t.Error("restored another account's credentials")
			}
			return &fakePairingClient{device: device, sender: sender, qr: make(chan whatsmeow.QRChannelItem, 8), phone: make(chan string, 1)}
		}
		return s
	}
	s := open()
	device := s.store.NewDevice()
	device.ID = &jid
	device.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err = device.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.start(user, account, "", ""); err != nil {
		t.Fatal(err)
	}
	awaitConfirmation(t, db, business, id, "sent")
	sender.mu.Lock()
	if sender.calls != 1 || sender.phone != "919000001010" || sender.body == "" || len(sender.IDs) != 1 {
		t.Error("worker sent the wrong receipt or more than once")
	}
	originalID := sender.IDs[0]
	sender.mu.Unlock()
	var persistedID string
	if err = db.QueryRow(`SELECT wa_message_id FROM ipo_pos.transaction_confirmations WHERE transaction_id=$1`, id).Scan(&persistedID); err != nil || originalID != persistedID {
		t.Fatal("wire message ID differs from the recorded ID")
	}
	s.close()
	base := "/api/businesses/" + business
	body := cloneMap(request)
	body["whatsapp_account_id"] = account
	queue := func(key string) string {
		status, obj, _ := cashier.request("POST", base+"/transactions", body, key)
		expectStatus(t, 201, status, obj)
		return field(t, obj, "id")
	}
	body["quantity"] = "-2.5"
	body["rate"] = "-250"
	restartID := queue("pending-after-restart")
	body["quantity"] = request["quantity"]
	body["rate"] = request["rate"]
	restored := open()
	if err = restored.resume(); err != nil {
		t.Fatal(err)
	}
	awaitConfirmation(t, db, business, restartID, "sent")
	sender.mu.Lock()
	if sender.body != "Fixture IPO ipo Sell 2.5 CE (20) at -250" {
		t.Error("sell delivery lost its direction or signed values")
	}
	sender.fail = true
	sender.mu.Unlock()
	uncertainID := queue("uncertain-send")
	awaitConfirmation(t, db, business, uncertainID, "uncertain")
	status, obj, _ := cashier.request("POST", base+"/transactions", body, "uncertain-send")
	expectStatus(t, 200, status, obj)
	if obj["whatsapp_confirmation"].(map[string]any)["status"] != "uncertain" {
		t.Fatal("POST replay reset an ambiguous send")
	}
	restored.close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = deliverConfirmation(ctx, conn, user, account, jidText, sender); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	calls := sender.calls
	sender.mu.Unlock()
	if calls != 3 {
		t.Fatal("uncertain or sent receipts were resent")
	}

	for _, kind := range []string{"cancelled", "edited", "recipient", "sender"} {
		queuedID := queue("blocked-" + kind)
		currentJID := jidText
		expected := "failed"
		switch kind {
		case "cancelled":
			status, obj, _ = cashier.request("POST", base+"/transactions/"+queuedID+"/cancel", map[string]any{"expected_version": 1, "change_reason": "Fixture cancellation"}, "")
			expectStatus(t, 200, status, obj)
			expected = "cancelled"
		case "edited":
			edit := cloneMap(request)
			edit["expected_version"] = 1
			edit["change_reason"] = "Fixture edit"
			edit["rate"] = "251"
			status, obj, _ = cashier.request("PUT", base+"/transactions/"+queuedID, edit, "")
			expectStatus(t, 200, status, obj)
		case "recipient":
			if _, err = db.Exec(`UPDATE ipo_pos.customers SET phone_number='+919000001011' WHERE business_id=$1 AND id=$2`, business, request["customer_id"]); err != nil {
				t.Fatal(err)
			}
		case "sender":
			expected = "cancelled"
			currentJID = "919000001003:56@s.whatsapp.net"
			if _, err = db.Exec(`UPDATE ipo_pos.whatsapp_accounts SET device_jid=$2 WHERE id=$1`, account, currentJID); err != nil {
				t.Fatal(err)
			}
		}
		if err = deliverConfirmation(ctx, conn, user, account, currentJID, sender); err != nil {
			t.Fatal(err)
		}
		awaitConfirmation(t, db, business, queuedID, expected)
		if _, err = db.Exec(`UPDATE ipo_pos.customers SET phone_number='+919000001010' WHERE business_id=$1 AND id=$2`, business, request["customer_id"]); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE ipo_pos.whatsapp_accounts SET device_jid=$2 WHERE id=$1`, account, jidText); err != nil {
			t.Fatal(err)
		}
		if kind == "sender" {
			status, obj, _ = cashier.request("POST", "/api/login", map[string]string{"phone_number": "+919000001003", "password": "cashier-password-12345"}, "")
			expectStatus(t, 200, status, obj)
			cashier.Token = field(t, obj, "access_token")
			var reason string
			if err = db.QueryRow(`SELECT error_code FROM ipo_pos.transaction_confirmations WHERE transaction_id=$1`, queuedID).Scan(&reason); err != nil || reason != "sender_unlinked" {
				t.Fatal("binding replacement did not stop old pending receipts")
			}
		}
	}
	interruptedID := queue("interrupted-send")
	if _, err = db.Exec(`UPDATE ipo_pos.transaction_confirmations SET status='sending',wa_message_id='INTERRUPTED',attempted_at=now() WHERE transaction_id=$1`, interruptedID); err != nil {
		t.Fatal(err)
	}
	// A restarted worker obtains the account lock before resolving its prior attempt.
	recovered := open()
	if _, err = recovered.start(user, account, "", ""); err != nil {
		t.Fatal(err)
	}
	awaitConfirmation(t, db, business, interruptedID, "uncertain")
	recovered.close()
	queue("disabled-member")
	if _, err = db.Exec(`UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE business_id=$1 AND user_id=$2`, business, cashierID); err != nil {
		t.Fatal(err)
	}
	if err = deliverConfirmation(ctx, conn, user, account, jidText, sender); err == nil {
		t.Fatal("disabled staff remained eligible to send")
	}
	if _, err = db.Exec(`UPDATE ipo_pos.business_memberships SET disabled_at=NULL WHERE business_id=$1 AND user_id=$2`, business, cashierID); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	calls = sender.calls
	sender.mu.Unlock()
	if calls != 3 {
		t.Fatal("a rejected or interrupted confirmation was sent")
	}
	cleanup := open()
	saved, err := cleanup.store.GetDevice(ctx, jid)
	if err != nil || saved == nil {
		t.Fatal("fixture device disappeared")
	}
	if err = saved.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE ipo_pos.whatsapp_accounts SET device_jid=NULL,unlinked_at=now() WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	cleanup.close()
	capture.Lock()
	defer capture.Unlock()
	traced := false
	for _, event := range capture.events {
		if event.Transaction != "confirmation.deliver" || event.Tags["job.key"] != performanceKey(id) {
			continue
		}
		sent, persisted := false, false
		for _, span := range event.Spans {
			sent = sent || span.Description == "confirmation.send"
			persisted = persisted || span.Description == "confirmation.persist"
		}
		traced = sent && persisted
	}
	if !traced {
		t.Fatal("real worker did not separate confirmation sending and persistence")
	}
}

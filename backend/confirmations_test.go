package main

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func testConfirmationAPI(t *testing.T, db, runtime *sql.DB, admin, cashier, other testClient, business, otherBusiness string, request map[string]any) {
	admin.t, cashier.t, other.t = t, t, t
	base := "/api/businesses/" + business
	create := func(c testClient, label string) string {
		status, obj, _ := c.request("POST", base+"/whatsapp-accounts", map[string]string{"label": label}, "")
		expectStatus(t, 201, status, obj)
		return field(t, obj, "id")
	}
	account := create(cashier, "POS fixture sender")
	wrongOwner := create(admin, "Other staff sender")
	body := cloneMap(request)
	body["whatsapp_account_id"] = account
	status, obj, _ := cashier.request("POST", base+"/transactions", body, "unpaired-confirmation")
	expectStatus(t, 409, status, obj)
	if _, err := db.Exec(`UPDATE ipo_pos.whatsapp_accounts SET device_jid='919000001003:55@s.whatsapp.net',phone_number='+919000001003' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	status, obj, _ = cashier.request("POST", base+"/transactions", body, "pos-confirmation")
	expectStatus(t, 201, status, obj)
	id := field(t, obj, "id")
	receipt := obj["whatsapp_confirmation"].(map[string]any)
	if field(t, receipt, "status") != "pending" || field(t, receipt, "recipient_phone_number") != "+919000001010" || receipt["sender_device_jid"] != nil {
		t.Fatal("confirmation recipient/status is incorrect or exposes a session binding")
	}
	if !strings.Contains(field(t, receipt, "body"), "Total: INR 625.00") || !strings.Contains(field(t, receipt, "body"), "CE (20.000000)") || !strings.Contains(field(t, receipt, "body"), id) {
		t.Fatal("receipt was not composed from the persisted sale")
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			status, obj, _ := cashier.request("POST", base+"/transactions", body, "pos-confirmation")
			expectStatus(t, 200, status, obj)
			if field(t, obj, "id") != id {
				t.Error("replay created another sale")
			}
		})
	}
	wg.Wait()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM ipo_pos.transaction_confirmations WHERE business_id=$1 AND transaction_id=$2`, business, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry duplicated confirmation intent")
	}
	for i, invalid := range []map[string]any{
		{"whatsapp_account_id": wrongOwner}, {"whatsapp_account_id": "invalid"},
		{"customer_id": "00000000-0000-0000-0000-000000000001"}, {"message_body": "Forged receipt"},
	} {
		changed := cloneMap(body)
		for key, value := range invalid {
			changed[key] = value
		}
		status, obj, _ = cashier.request("POST", base+"/transactions", changed, "bad-confirmation-"+strconv.Itoa(i))
		if invalid["whatsapp_account_id"] == wrongOwner {
			expectStatus(t, 403, status, obj)
		} else {
			expectStatus(t, 400, status, obj)
		}
	}
	changed := cloneMap(body)
	changed["whatsapp_account_id"] = wrongOwner
	status, obj, _ = cashier.request("POST", base+"/transactions", changed, "pos-confirmation")
	expectStatus(t, 409, status, obj)
	status, obj, _ = cashier.request("GET", base+"/transactions/"+id+"/confirmation", nil, "")
	expectStatus(t, 200, status, obj)
	status, obj, _ = other.request("GET", "/api/businesses/"+otherBusiness+"/transactions/"+id+"/confirmation", nil, "")
	expectStatus(t, 404, status, obj)
	status, obj, _ = cashier.request("POST", base+"/customers", map[string]string{"name": "No recipient phone"}, "")
	expectStatus(t, 201, status, obj)
	changed = cloneMap(body)
	changed["customer_id"] = field(t, obj, "id")
	status, obj, _ = cashier.request("POST", base+"/transactions", changed, "missing-recipient")
	expectStatus(t, 409, status, obj)
	if err := db.QueryRow(`SELECT count(*) FROM ipo_pos.transactions WHERE business_id=$1 AND idempotency_key IN ('missing-recipient','unpaired-confirmation')`, business).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed intent left a sale behind")
	}
	if _, err := runtime.Exec(`UPDATE ipo_pos.transaction_confirmations SET status='sent' WHERE business_id=$1`, business); err == nil {
		t.Fatal("runtime API role can forge delivery status")
	}
}

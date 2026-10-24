package main

import (
	"database/sql"
	"strconv"
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
	if field(t, receipt, "body") != "Fixture IPO ipo Buy 2.5 CE (20) at 250" {
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

func testConfirmationFormatting(t *testing.T, db *sql.DB, owner testClient, business string, request map[string]any) {
	owner.t = t
	base := "/api/businesses/" + business
	status, obj, _ := owner.request("POST", base+"/whatsapp-accounts", map[string]string{"label": "Message format fixture"}, "")
	expectStatus(t, 201, status, obj)
	account := field(t, obj, "id")
	if _, err := db.Exec(`UPDATE ipo_pos.whatsapp_accounts SET device_jid='919000001001:77@s.whatsapp.net',phone_number='+919000001001' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, form, category, quantity, rate, want string
		cepe                                       *string
	}{
		{name: "no category", form: "RETAIL", category: "none", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 RETAIL alloted at 250"},
		{name: "default category", form: "SHNI", quantity: "-10", rate: "250.500000", want: "Fixture IPO ipo Sell 10 SHNI alloted at 250.5"},
		{name: "retail 199 no category", form: "RETAIL_199", category: "none", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 RETAIL_199 alloted at 250"},
		{name: "BHNI no category", form: "BHNI", category: "none", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 BHNI alloted at 250"},
		{name: "shareholder no category", form: "SHAREHOLDER", category: "none", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 SHAREHOLDER alloted at 250"},
		{name: "shareholder subject 1", form: "SHAREHOLDER", category: "subject_1", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 SHAREHOLDER subject 1 at 250"},
		{name: "shareholder subject 2", form: "SHAREHOLDER", category: "subject_2", quantity: "-2.5", rate: "-250", want: "Fixture IPO ipo Sell 2.5 SHAREHOLDER subject 2 at -250"},
		{name: "subject 2 and zero rate", form: "BHNI", category: "subject_2", quantity: "1", rate: "0", want: "Fixture IPO ipo Buy 1 BHNI subject 2 at 0"},
		{name: "subject 1", form: "RETAIL", category: "subject_1", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 RETAIL subject 1 at 250"},
		{name: "negative rate", form: "SHARES", category: "subject_1", quantity: "0.000001", rate: "-0.123456", want: "Fixture IPO ipo Buy 0.000001 SHARES at -0.123456"},
		{name: "shares subject 2", form: "SHARES", category: "subject_2", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 SHARES at 250"},
		{name: "shares no category", form: "SHARES", category: "none", quantity: "10", rate: "250", want: "Fixture IPO ipo Buy 10 SHARES at 250"},
		{name: "CE subject 2", form: "CE", category: "subject_2", quantity: "10", rate: "250", cepe: new("20"), want: "Fixture IPO ipo Buy 10 CE (20) at 250"},
		{name: "CE no category", form: "CE", category: "none", quantity: "10", rate: "250", cepe: new("20"), want: "Fixture IPO ipo Buy 10 CE (20) at 250"},
		{name: "PE value", form: "PE", category: "subject_2", quantity: "-2.5", rate: "-250", cepe: new("-20.500000"), want: "Fixture IPO ipo Sell 2.5 PE (-20.5) at -250"},
		{name: "PE subject 1", form: "PE", category: "subject_1", quantity: "10", rate: "250", cepe: new("20"), want: "Fixture IPO ipo Buy 10 PE (20) at 250"},
		{name: "PE no category", form: "PE", category: "none", quantity: "10", rate: "250", cepe: new("20"), want: "Fixture IPO ipo Buy 10 PE (20) at 250"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := owner
			client.t = t
			body := cloneMap(request)
			body["whatsapp_account_id"] = account
			body["form_type_code"], body["category"], body["ce_pe_value"] = test.form, test.category, test.cepe
			body["quantity"], body["rate"] = test.quantity, test.rate
			status, obj, _ := client.request("POST", base+"/transactions", body, "confirmation-format-"+test.name)
			expectStatus(t, 201, status, obj)
			if got := field(t, obj["whatsapp_confirmation"].(map[string]any), "body"); got != test.want {
				t.Fatalf("body=%q, want %q", got, test.want)
			}
		})
	}
}

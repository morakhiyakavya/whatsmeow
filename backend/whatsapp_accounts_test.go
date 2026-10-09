package main

import (
	"database/sql"
	"strings"
	"testing"
)

// Uses the existing disposable-cluster fixture and restricted API role.
func testWhatsAppAccounts(t *testing.T, db, owner *sql.DB, admin, cashier, other testClient, businessID, otherBusiness, cashierID string) {
	admin.t, cashier.t, other.t = t, t, t
	base := "/api/businesses/" + businessID + "/whatsapp-accounts"
	otherBase := "/api/businesses/" + otherBusiness + "/whatsapp-accounts"
	status, object, _ := admin.request("GET", base, nil, "")
	expectStatus(t, 200, status, object)
	if len(object["items"].([]any)) != 0 || object["next_cursor"] != nil {
		t.Fatal("new business must have an empty account list")
	}
	status, object, _ = cashier.request("POST", base, map[string]string{"label": "  Cashier phone  "}, "")
	expectStatus(t, 201, status, object)
	id := field(t, object, "id")
	if !validUUID(id) || field(t, object, "business_id") != businessID || field(t, object, "linked_by_user_id") != cashierID || field(t, object, "label") != "Cashier phone" || field(t, object, "pairing_state") != "unpaired" || object["phone_number"] != nil {
		t.Fatalf("incorrect account identity/ownership: %v", object)
	}
	if _, exposed := object["device_jid"]; exposed {
		t.Fatal("account response exposed the protocol binding")
	}
	for _, label := range []string{"", " \t\n", strings.Repeat("x", 257)} {
		status, object, _ = cashier.request("POST", base, map[string]string{"label": label}, "")
		expectStatus(t, 400, status, object)
	}
	for _, key := range []string{"business_id", "linked_by_user_id", "device_jid", "phone_number", "id", "unlinked_at"} {
		status, object, _ = cashier.request("POST", base, map[string]string{"label": "Forged account", key: "caller-controlled"}, "")
		expectStatus(t, 400, status, object)
	}
	anonymous := cashier
	anonymous.Token = ""
	for _, c := range []testClient{anonymous, other} {
		want := 403
		if c.Token == "" {
			want = 401
		}
		for _, path := range []string{base, base + "/" + id} {
			status, object, _ = c.request("GET", path, nil, "")
			expectStatus(t, want, status, object)
		}
		status, object, _ = c.request("POST", base, map[string]string{"label": "Forbidden"}, "")
		expectStatus(t, want, status, object)
	}
	status, object, _ = other.request("GET", otherBase+"/"+id, nil, "")
	expectStatus(t, 404, status, object)
	status, object, _ = admin.request("GET", base+"/bad-id", nil, "")
	expectStatus(t, 400, status, object)
	status, object, _ = admin.request("GET", base+"/00000000-0000-0000-0000-000000000000", nil, "")
	expectStatus(t, 404, status, object)
	status, object, _ = other.request("POST", otherBase, map[string]string{"label": "Other business phone"}, "")
	expectStatus(t, 201, status, object)
	status, object, _ = admin.request("POST", base, map[string]string{"label": "Admin phone"}, "")
	expectStatus(t, 201, status, object)
	secondID := field(t, object, "id")
	status, object, _ = cashier.request("GET", base+"?limit=1", nil, "")
	expectStatus(t, 200, status, object)
	firstPage := field(t, object["items"].([]any)[0].(map[string]any), "id")
	next := field(t, object, "next_cursor")
	status, object, _ = cashier.request("GET", base+"?limit=1&cursor="+next, nil, "")
	expectStatus(t, 200, status, object)
	lastPage := field(t, object["items"].([]any)[0].(map[string]any), "id")
	if firstPage == lastPage || (firstPage != id && firstPage != secondID) || (lastPage != id && lastPage != secondID) || object["next_cursor"] != nil {
		t.Fatal("account pagination repeated, skipped or leaked a business account")
	}
	status, object, _ = cashier.request("GET", base+"?limit=101", nil, "")
	expectStatus(t, 400, status, object)

	if _, err := db.Exec("UPDATE ipo_pos.whatsapp_accounts SET device_jid='999000000001:7@s.whatsapp.net' WHERE business_id=$1 AND id=$2", businessID, id); err == nil {
		t.Fatal("API role can claim a protocol session")
	}
	if _, err := db.Exec("INSERT INTO ipo_pos.whatsapp_accounts(business_id,label,linked_by_user_id,device_jid) VALUES($1,'Forged',$2,'999000000001:7@s.whatsapp.net')", businessID, cashierID); err == nil {
		t.Fatal("API role can supply a session binding during registration")
	}
	if _, err := db.Exec("DELETE FROM ipo_pos.whatsapp_accounts WHERE business_id=$1 AND id=$2", businessID, id); err == nil {
		t.Fatal("API role can delete an account's stable identity")
	}
	// Synthetic owner writes simulate a future trusted pairing worker, without WhatsApp.
	if _, err := owner.Exec("UPDATE ipo_pos.whatsapp_accounts SET device_jid='999000000001:7@s.whatsapp.net',phone_number='+999000000001' WHERE business_id=$1 AND id=$2", businessID, id); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"paired", "unlinked"} {
		if state == "unlinked" {
			if _, err := owner.Exec("UPDATE ipo_pos.whatsapp_accounts SET device_jid=NULL,unlinked_at=now() WHERE business_id=$1 AND id=$2", businessID, id); err != nil {
				t.Fatal(err)
			}
		}
		status, object, _ = admin.request("GET", base+"/"+id, nil, "")
		expectStatus(t, 200, status, object)
		if field(t, object, "id") != id || field(t, object, "pairing_state") != state {
			t.Fatal("pairing state must preserve the application identity")
		}
		if _, exposed := object["device_jid"]; exposed {
			t.Fatal("account response exposed the protocol binding")
		}
	}
}

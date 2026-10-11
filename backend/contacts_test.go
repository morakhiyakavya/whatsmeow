package main

import (
	"database/sql"
	"net/url"
	"testing"
)

func testContactNames(t *testing.T, db *sql.DB, admin, cashier testClient, base, otherBusiness, customerID, cashierID string) {
	phone := "+919000001010"
	path := base + "/contacts?phone_number=" + url.QueryEscape(phone)
	upload := func(c testClient, number, name string) {
		status, obj, _ := c.request("POST", base+"/contacts", map[string]any{"contacts": []map[string]string{{"phone_number": number, "name": name}}}, "")
		expectStatus(t, 200, status, obj)
	}
	upload(admin, phone, "Customer in admin phone")
	upload(cashier, phone, "Customer in cashier phone")
	upload(cashier, phone, "Customer in cashier phone")
	status, obj, _ := cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, obj)
	if obj["source_count"] != float64(2) || obj["customer_count"] != float64(1) {
		t.Fatal("contact upload duplicated sources or customers")
	}
	suggested := obj["suggested_name"].(map[string]any)
	if field(t, suggested, "source") != "customer_record" || field(t, suggested, "customer_id") != customerID || field(t, suggested, "supplied_by_user_id") != cashierID {
		t.Fatal("manual customer name/provenance was overwritten by imported names")
	}
	var originalTime, repeatTime string
	if err := db.QueryRow(`SELECT updated_at::text FROM ipo_pos.contact_names WHERE phone_number=$1 AND supplied_by_user_id=$2`, phone, cashierID).Scan(&originalTime); err != nil {
		t.Fatal(err)
	}
	upload(cashier, phone, "Customer in cashier phone")
	if err := db.QueryRow(`SELECT updated_at::text FROM ipo_pos.contact_names WHERE phone_number=$1 AND supplied_by_user_id=$2`, phone, cashierID).Scan(&repeatTime); err != nil {
		t.Fatal(err)
	}
	if repeatTime != originalTime {
		t.Fatal("replay changed suggestion ordering")
	}
	for _, contacts := range [][]map[string]string{
		{},
		{{"phone_number": phone, "name": "Valid"}, {"phone_number": "123", "name": "Invalid"}},
		{{"phone_number": phone, "name": "One"}, {"phone_number": phone, "name": "Two"}},
		{{"phone_number": phone, "name": "Name", "supplied_by_user_id": cashierID}},
	} {
		status, obj, _ = admin.request("POST", base+"/contacts", map[string]any{"contacts": contacts}, "")
		expectStatus(t, 400, status, obj)
	}
	status, obj, _ = admin.request("GET", "/api/businesses/"+otherBusiness+"/contacts?phone_number="+url.QueryEscape(phone), nil, "")
	expectStatus(t, 403, status, obj)
	status, obj, _ = cashier.request("DELETE", path, nil, "")
	expectStatus(t, 204, status, obj)
	status, obj, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, obj)
	if obj["source_count"] != float64(1) || obj["name_sources"].([]any)[0].(map[string]any)["name"] != "Customer in admin phone" {
		t.Fatal("withdrawal removed another staff member's source")
	}
	unknown := "+919000001019"
	upload(admin, unknown, "Shared business contact")
	status, obj, _ = cashier.request("GET", base+"/contacts?phone_number="+url.QueryEscape(unknown), nil, "")
	expectStatus(t, 200, status, obj)
	if obj["customer_count"] != float64(0) || field(t, obj["suggested_name"].(map[string]any), "name") != "Shared business contact" {
		t.Fatal("another staff member could not resolve an unknown number")
	}
	status, obj, _ = cashier.request("GET", base+"/contacts?phone_number=%2B919000001099", nil, "")
	expectStatus(t, 200, status, obj)
	if obj["suggested_name"] != nil || obj["whatsapp_registered"] != nil || obj["whatsapp_checked_at"] != nil {
		t.Fatal("unknown number invented an identity")
	}
	status, obj, _ = cashier.request("GET", base+"/contacts?phone_number=123", nil, "")
	expectStatus(t, 400, status, obj)
}

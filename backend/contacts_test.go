package main

import (
	"database/sql"
	"net/url"
	"strings"
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
	display := obj["display_name"].(map[string]any)
	if field(t, display, "name") != "Customer in cashier phone" || display["is_own"] != true || field(t, display, "supplied_by_user_id") != cashierID {
		t.Fatal("personal display name did not take precedence over the customer suggestion")
	}
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
	if obj["source_count"] != float64(1) || obj["name_sources"].([]any)[0].(map[string]any)["name"] != "Customer in admin phone" || obj["display_name"].(map[string]any)["is_own"] != false {
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
	if obj["suggested_name"] != nil || obj["display_name"] != nil || obj["whatsapp_registered"] != nil || obj["whatsapp_checked_at"] != nil {
		t.Fatal("unknown number invented an identity")
	}
	status, obj, _ = cashier.request("GET", base+"/contacts?phone_number=123", nil, "")
	expectStatus(t, 400, status, obj)
	testContactAutocomplete(t, db, admin, cashier, base, otherBusiness, cashierID, upload)
}

func testContactAutocomplete(t *testing.T, db *sql.DB, admin, cashier testClient, base, otherBusiness, cashierID string, upload func(testClient, string, string)) {
	phone, shared := "+919000001080", "+919000001081"
	upload(cashier, phone, "Zenith IPO")
	upload(admin, phone, "Zenith Shah")
	upload(admin, shared, "Zebra Shared")
	if _, err := db.Exec(`INSERT INTO ipo_pos.contact_names(business_id,phone_number,supplied_by_user_id,name)
        SELECT business_id,$2,user_id,'Secret Zenith' FROM ipo_pos.business_memberships WHERE business_id=$1`, otherBusiness, "+919000001088"); err != nil {
		t.Fatal(err)
	}
	search := func(c testClient, query string) map[string]any {
		t.Helper()
		status, obj, headers := c.request("GET", base+"/contacts"+query, nil, "")
		expectStatus(t, 200, status, obj)
		if headers.Get("Cache-Control") != "no-store" {
			t.Fatal("autocomplete response could be cached")
		}
		return obj
	}
	for _, q := range []string{"Z", "Ze", "zEN", "  Zenith  ", "000001080"} {
		obj := search(cashier, "?q="+url.QueryEscape(q)+"&limit=20")
		items := obj["items"].([]any)
		want := 1
		if q == "Z" || q == "Ze" {
			want = 2
		}
		if len(items) != want || obj["next_after_phone"] != nil {
			t.Fatalf("incorrect dropdown for %q: %v", q, obj)
		}
		first := items[0].(map[string]any)
		if field(t, first, "phone_number") != phone || field(t, first, "name") != "Zenith IPO" || first["is_own"] != true || field(t, first, "supplied_by_user_id") != cashierID {
			t.Fatalf("newer colleague name displaced personal name: %v", first)
		}
	}
	obj := search(admin, "?q=Zenith")
	if field(t, obj["items"].([]any)[0].(map[string]any), "name") != "Zenith Shah" {
		t.Fatal("autocomplete was not personalized per requester")
	}
	obj = search(cashier, "?q=Zebra")
	if item := obj["items"].([]any)[0].(map[string]any); field(t, item, "name") != "Zebra Shared" || item["is_own"] != false {
		t.Fatal("colleague fallback was unavailable")
	}
	for _, q := range []string{"Shah", "Secret", "%", "_", "' OR true --", "does-not-exist"} {
		if items := search(cashier, "?q="+url.QueryEscape(q))["items"].([]any); len(items) != 0 {
			t.Fatalf("unexpected match or information leak for %q: %v", q, items)
		}
	}
	upload(cashier, phone, "Zenith Revised")
	obj = search(cashier, "?q=Revised")
	if len(obj["items"].([]any)) != 1 || len(search(cashier, "?q=IPO")["items"].([]any)) != 0 {
		t.Fatal("fresh search did not reflect a saved rename")
	}
	status, obj, _ := cashier.request("GET", base+"/contacts?phone_number="+url.QueryEscape(phone), nil, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj["display_name"].(map[string]any), "name") != "Zenith Revised" {
		t.Fatal("exact lookup and dropdown chose different personal names")
	}
	status, obj, _ = cashier.request("DELETE", base+"/contacts?phone_number="+url.QueryEscape(phone), nil, "")
	expectStatus(t, 204, status, obj)
	obj = search(cashier, "?q=Zenith")
	if item := obj["items"].([]any)[0].(map[string]any); field(t, item, "name") != "Zenith Shah" || item["is_own"] != false {
		t.Fatal("withdrawal did not reveal the remaining colleague name")
	}
	last, count := "", 0
	for {
		obj = search(cashier, "?limit=1&after_phone="+url.QueryEscape(last))
		items := obj["items"].([]any)
		if len(items) != 1 || count >= 4 {
			t.Fatalf("directory pagination duplicated or skipped contacts: %v", obj)
		}
		number := field(t, items[0].(map[string]any), "phone_number")
		if number <= last {
			t.Fatal("directory ordering repeated a number")
		}
		count++
		if obj["next_after_phone"] == nil {
			break
		}
		last = field(t, obj, "next_after_phone")
		if last != number {
			t.Fatal("page cursor was not the last returned number")
		}
	}
	if count != 4 {
		t.Fatalf("directory should contain four distinct business numbers, got %d", count)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=abc", "?after_phone=123", "?cursor=bad", "?q=" + strings.Repeat("x", 257), "?q=%00", "?q=%FF", "?phone_number="} {
		status, obj, _ = cashier.request("GET", base+"/contacts"+query, nil, "")
		expectStatus(t, 400, status, obj)
	}
	status, obj, _ = admin.request("GET", "/api/businesses/"+otherBusiness+"/contacts?q=Secret", nil, "")
	expectStatus(t, 403, status, obj)
	anonymous := admin
	anonymous.Token = ""
	status, obj, _ = anonymous.request("GET", base+"/contacts?q=Zenith", nil, "")
	expectStatus(t, 401, status, obj)
}

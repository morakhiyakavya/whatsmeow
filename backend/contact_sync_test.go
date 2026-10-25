package main

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

func TestContactDirectorySyncIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable schema_test database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err = db.QueryRow(`SELECT current_user`).Scan(&role); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test database")
	}
	config := configuration{BusinessName: "Directory sync fixture", AdminName: "Sync owner", AdminPhone: "+919000009221", AdminPassword: "sync-fixture-password"}
	if err = bootstrapAdmin(context.Background(), db, config); err != nil {
		t.Fatal(err)
	}
	var business, user, otherBusiness, account string
	if err = db.QueryRow(`SELECT m.business_id,u.id FROM ipo_pos.users u JOIN ipo_pos.business_memberships m ON m.user_id=u.id WHERE u.phone_number=$1`, config.AdminPhone).Scan(&business, &user); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO ipo_pos.businesses(name) VALUES('Unrelated sync business') RETURNING id`).Scan(&otherBusiness); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ipo_pos.customers(business_id,name) VALUES($1,'First sync customer'),($1,'Second sync customer');`, business); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ipo_pos.contact_names(business_id,phone_number,supplied_by_user_id,name) VALUES($1,'+919000009222',$2,'Shared sync name')`, business, user); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO ipo_pos.whatsapp_accounts(business_id,label,linked_by_user_id) VALUES($1,'Sync fixture',$2) RETURNING id`, business, user).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO ipo_pos.whatsapp_number_checks(business_id,phone_number,registered,whatsapp_account_id) VALUES($1,'+919000009222',false,$2)`, business, account); err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(db)
	if err != nil {
		t.Fatal(err)
	}
	app.passwordLogin = true
	server := httptest.NewServer(app.routes())
	defer server.Close()
	client := testClient{t: t, URL: server.URL, HTTP: server.Client()}
	code, data, _ := client.request("POST", "/api/login", map[string]string{"phone_number": config.AdminPhone, "password": config.AdminPassword}, "")
	expectStatus(t, 200, code, data)
	client.Token = field(t, data, "access_token")
	testContactDirectorySync(t, db, client, "/api/businesses/"+business, otherBusiness)
}

func TestDirectoryRevisionValidation(t *testing.T) {
	for _, value := range []string{"0", "1", "9223372036854775807"} {
		if !validDirectoryRevision(value) {
			t.Errorf("rejected %q", value)
		}
	}
	for _, value := range []string{"", "01", "-1", "+1", "1e3", " 1", "9223372036854775808"} {
		if validDirectoryRevision(value) {
			t.Errorf("accepted %q", value)
		}
	}
}

func testContactDirectorySync(t *testing.T, db *sql.DB, client testClient, base, otherBusiness string) {
	t.Helper()
	path := base + "/contacts/sync?limit=1"
	public := client
	public.Token = ""
	code, data, _ := public.request("GET", path, nil, "")
	expectStatus(t, 401, code, data)
	code, data, _ = client.request("GET", "/api/businesses/"+otherBusiness+"/contacts/sync", nil, "")
	expectStatus(t, 403, code, data)
	for _, query := range []string{"?revision=01", "?since_revision=-1", "?after_kind=customer&after_id=bad", "?after_kind=contact&after_id=%2B919000001010", "?cursor=bad"} {
		code, data, _ = client.request("GET", base+"/contacts/sync"+query, nil, "")
		expectStatus(t, 400, code, data)
	}
	code, data, _ = client.request("GET", path, nil, "")
	expectStatus(t, 200, code, data)
	revision := field(t, data, "revision")
	seen := map[string]bool{}
	for {
		if field(t, data, "revision") != revision {
			t.Fatal("page revision changed")
		}
		items := data["items"].([]any)
		if len(items) > 1 {
			t.Fatal("sync page ignored limit")
		}
		for _, item := range items {
			entry := item.(map[string]any)
			kind, id := field(t, entry, "kind"), field(t, entry, "id")
			if seen[kind+"/"+id] {
				t.Fatal("sync cursor repeated a record")
			}
			seen[kind+"/"+id] = true
			body := entry["data"].(map[string]any)
			if kind == "customer" && body["business_id"] != base[len("/api/businesses/"):] {
				t.Fatal("wrong business in snapshot")
			}
			if kind == "registration" && (body["registered"] != false || body["checked_at"] == nil) {
				t.Fatal("registration result omitted from sync")
			}
		}
		if data["next_kind"] == nil {
			break
		}
		code, data, _ = client.request("GET", path+"&revision="+revision+"&after_kind="+field(t, data, "next_kind")+"&after_id="+url.QueryEscape(field(t, data, "next_id")), nil, "")
		expectStatus(t, 200, code, data)
	}
	if len(seen) < 2 {
		t.Fatal("missing directory records")
	}
	code, data, _ = client.request("GET", base+"/contacts/sync?since_revision="+revision, nil, "")
	expectStatus(t, 200, code, data)
	if data["unchanged"] != true || len(data["items"].([]any)) != 0 {
		t.Fatal("unchanged directory downloaded again")
	}
	if _, err := db.Exec(`INSERT INTO ipo_pos.customers(business_id,name) VALUES($1,'Directory revision change')`, base[len("/api/businesses/"):]); err != nil {
		t.Fatal(err)
	}
	code, data, _ = client.request("GET", path+"&revision="+revision, nil, "")
	expectStatus(t, 409, code, data)
}

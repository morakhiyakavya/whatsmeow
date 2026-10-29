package main

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestBusinessDataSyncIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable schema_test database")
	}
	owner, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	var role, database string
	if err = owner.QueryRow(`SELECT current_user,current_database()`).Scan(&role, &database); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test database", err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	grants, err := os.ReadFile("../database/api_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(`CREATE ROLE business_sync_api_test NOLOGIN;` + strings.NewReplacer(`:"api_role"`, `business_sync_api_test`, `:"database_name"`, pgx.Identifier{database}.Sanitize()).Replace(string(grants)))
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["role"] = "business_sync_api_test"
	runtime := stdlib.OpenDB(*config)
	defer runtime.Close()
	bootstrap := configuration{BusinessName: "Business sync fixture", AdminName: "Sync admin", AdminPhone: "+919000009331", AdminPassword: "sync-fixture-password"}
	if err = bootstrapAdmin(context.Background(), runtime, bootstrap); err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(runtime)
	if err != nil {
		t.Fatal(err)
	}
	app.passwordLogin = true
	server := httptest.NewServer(app.routes())
	defer server.Close()
	client := testClient{t: t, URL: server.URL, HTTP: server.Client()}
	code, body, _ := client.request("POST", "/api/login", map[string]string{"phone_number": bootstrap.AdminPhone, "password": bootstrap.AdminPassword}, "")
	expectStatus(t, 200, code, body)
	client.Token = field(t, body, "access_token")
	var business, user, session, other string
	if err = owner.QueryRow(`SELECT m.business_id,u.id,s.id FROM ipo_pos.users u JOIN ipo_pos.business_memberships m ON m.user_id=u.id JOIN ipo_pos.login_sessions s ON s.user_id=u.id WHERE u.phone_number=$1`, bootstrap.AdminPhone).Scan(&business, &user, &session); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(`INSERT INTO ipo_pos.businesses(name) VALUES('Other sync business') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	base := "/api/businesses/" + business
	path := base + "/data/sync"
	public := client
	public.Token = ""
	code, body, _ = public.request("GET", path, nil, "")
	expectStatus(t, 401, code, body)
	code, body, _ = client.request("GET", "/api/businesses/"+other+"/data/sync", nil, "")
	expectStatus(t, 403, code, body)
	for _, q := range []string{"?since_revision=-1", "?revision=01", "?after_revision=1", "?cursor=bad", "?after_kind=bad&after_id=00000000-0000-0000-0000-000000000001&after_revision=1&revision=1"} {
		code, body, _ = client.request("GET", path+q, nil, "")
		expectStatus(t, 400, code, body)
	}
	code, body, _ = client.request("POST", base+"/ipos", map[string]string{"code": "SYNC-IPO", "name": "Sync IPO"}, "")
	expectStatus(t, 201, code, body)
	ipo := field(t, body, "id")
	code, body, _ = client.request("POST", base+"/customers", map[string]string{"name": "Sync customer"}, "")
	expectStatus(t, 201, code, body)
	customer := field(t, body, "id")
	const installation = "40000000-0000-4000-8000-000000000001"
	exec(`INSERT INTO ipo_pos.push_devices(installation_id,user_id,session_id,fcm_token) VALUES($1,$2,$3,'business-sync-token')`, installation, user, session)
	input := map[string]any{"ipo_id": ipo, "customer_id": customer, "occurred_at": "2026-10-29T10:00:00Z", "form_type_code": "RETAIL", "quantity": "2.5", "rate": "-10.25"}
	code, body, _ = client.request("POST", base+"/transactions", input, "business-sync-key")
	expectStatus(t, 201, code, body)
	transaction := field(t, body, "id")
	rev := func() string {
		t.Helper()
		var v string
		if err := owner.QueryRow(`SELECT revision::text FROM ipo_pos.business_data_revisions WHERE business_id=$1`, business).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	original := rev()
	code, body, _ = client.request("POST", base+"/transactions", input, "business-sync-key")
	expectStatus(t, 200, code, body)
	if rev() != original {
		t.Fatal("idempotent replay advanced revision")
	}
	// Rollbacks must publish neither a revision nor an invalidation.
	tx, err := owner.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE ipo_pos.transactions SET version=version+1,rate=99,change_reason='rolled back' WHERE business_id=$1 AND id=$2`, business, transaction); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if rev() != original {
		t.Fatal("rollback leaked a revision")
	}
	seen := map[string]map[string]any{}
	code, body, headers := client.request("GET", path+"?limit=1", nil, "")
	expectStatus(t, 200, code, body)
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("financial response cacheable")
	}
	version := field(t, body, "revision")
	for {
		for _, raw := range body["items"].([]any) {
			entry := raw.(map[string]any)
			key := field(t, entry, "kind") + "/" + field(t, entry, "id")
			if seen[key] != nil {
				t.Fatal("duplicate page record")
			}
			seen[key] = entry
		}
		if body["next_revision"] == nil {
			break
		}
		q := "?limit=1&revision=" + version + "&after_revision=" + field(t, body, "next_revision") + "&after_kind=" + field(t, body, "next_kind") + "&after_id=" + field(t, body, "next_id")
		code, body, _ = client.request("GET", path+q, nil, "")
		expectStatus(t, 200, code, body)
	}
	record := seen["transaction/"+transaction]["data"].(map[string]any)
	if record["quantity"] != "2.500000" || record["total_amount"] != "-25.63" {
		t.Fatal("decimal precision changed", record)
	}
	if seen["ipo/"+ipo]["data"].(map[string]any)["source_basis"] == nil {
		t.Fatal("analysis source omitted")
	}
	code, body, _ = client.request("GET", path+"?since_revision="+version, nil, "")
	expectStatus(t, 200, code, body)
	if body["unchanged"] != true || len(body["items"].([]any)) != 0 {
		t.Fatal("unchanged data redownloaded")
	}
	assumptions := map[string]any{"expected_version": 0, "gmp": "25.5", "subject2_pricing_factor": "0.8", "expected_applications": map[string]any{"RETAIL": "100"}, "change_reason": "Sync forecast"}
	code, body, _ = client.request("PUT", base+"/ipos/"+ipo+"/analytics-assumptions", assumptions, "")
	expectStatus(t, 200, code, body)
	exec(`UPDATE ipo_pos.transactions SET version=version+1,status='cancelled',change_reason='cancelled for sync' WHERE business_id=$1 AND id=$2`, business, transaction)
	code, body, _ = client.request("GET", path+"?since_revision="+version, nil, "")
	expectStatus(t, 200, code, body)
	if len(body["items"].([]any)) != 2 {
		t.Fatal("delta included unchanged data", body)
	}
	code, body, _ = client.request("GET", path+"?revision="+version, nil, "")
	expectStatus(t, 409, code, body)
	exec(`UPDATE ipo_pos.ipos SET price_high=123 WHERE id=$1`, ipo)
	exec(`SELECT ipo_pos.capture_ipo_dashboard_basis($1)`, ipo)
	// Fanout must include shared analysis source changes, with no financial payload.
	sent := 0
	sender := pushSenderFunc(func(_ context.Context, token string, data map[string]string) pushOutcome {
		sent++
		if token != "business-sync-token" || len(data) != 4 || data["type"] != "business_data_changed" || data["user_id"] != user || data["business_id"] != business || data["revision"] != rev() {
			t.Fatal("private/wrong push payload", data)
		}
		exec(`UPDATE ipo_pos.transactions SET version=version+1,change_reason='newer while sending' WHERE business_id=$1 AND id=$2`, business, transaction)
		return pushOutcome{}
	})
	if worked, err := deliverContactPush(context.Background(), owner, sender, "business"); err != nil || !worked || sent != 1 {
		t.Fatal("business push failed", worked, err, sent)
	}
	var pending int
	if err = owner.QueryRow(`SELECT count(*) FROM ipo_pos.business_push_outbox WHERE business_id=$1`, business).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("send completion erased newer change", err)
	}
	for _, q := range []string{`UPDATE ipo_pos.business_data_revisions SET revision=999`, `DELETE FROM ipo_pos.business_data_changes`, `DELETE FROM ipo_pos.business_push_outbox`, `SELECT ipo_pos.record_business_data_change('` + business + `','transaction','` + transaction + `')`} {
		if _, err = runtime.Exec(q); err == nil {
			t.Fatal("runtime forged sync state", q)
		}
	}
	// Readers cannot observe a later revision until the writer commits.
	tx, err = owner.Begin()
	if err != nil {
		t.Fatal(err)
	}
	before := rev()
	if _, err = tx.Exec(`UPDATE ipo_pos.transactions SET version=version+1,change_reason='pending commit' WHERE business_id=$1 AND id=$2`, business, transaction); err != nil {
		t.Fatal(err)
	}
	code, body, _ = client.request("GET", path+"?since_revision="+before, nil, "")
	expectStatus(t, 200, code, body)
	if body["unchanged"] != true {
		t.Fatal("uncommitted change became visible")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	code, body, _ = client.request("GET", path+"?since_revision="+before, nil, "")
	expectStatus(t, 200, code, body)
	if len(body["items"].([]any)) != 1 {
		t.Fatal("committed change skipped")
	}
	exec(`UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE business_id=$1 AND user_id=$2`, business, user)
	if _, err = deliverContactPush(context.Background(), owner, pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome {
		t.Fatal("revoked device received financial hint")
		return pushOutcome{}
	}), "business"); err != nil {
		t.Fatal(err)
	}
	code, body, _ = client.request("GET", path, nil, "")
	if code != 401 && code != 403 {
		t.Fatal("revoked membership read data", code)
	}
}

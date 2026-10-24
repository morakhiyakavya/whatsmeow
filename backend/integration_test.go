package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestDecimalBoundary(t *testing.T) {
	for _, test := range []struct {
		Input, Want  string
		Negative, OK bool
	}{
		{"2.500000", "2.5", false, true}, {"000000000001.000001", "1.000001", false, true},
		{"999999999999.999999", "999999999999.999999", false, true}, {"-20.500", "-20.5", true, true},
		{"-0.000", "0", true, true}, {"1.0000001", "", false, false}, {"1000000000000", "", false, false},
		{"NaN", "", false, false}, {"Infinity", "", false, false}, {"1e3", "", false, false},
		{"-1", "", false, false}, {" 1", "", false, false}, {"1.", "", false, false},
	} {
		got, ok := decimal(test.Input, test.Negative)
		if got != test.Want || ok != test.OK {
			t.Errorf("decimal(%q)=(%q,%v), want (%q,%v)", test.Input, got, ok, test.Want, test.OK)
		}
	}
}

type testClient struct {
	t          *testing.T
	URL, Token string
	HTTP       *http.Client
}

func (c testClient) request(method, path string, body any, key string) (int, map[string]any, http.Header) {
	c.t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, c.URL+path, bytes.NewReader(encoded))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	var object map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		if err = json.Unmarshal(data, &object); err != nil {
			c.t.Fatalf("invalid response: %s", data)
		}
	}
	return resp.StatusCode, object, resp.Header
}
func expectStatus(t *testing.T, want, got int, data map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("status=%d, want %d; response=%v", got, want, data)
	}
}
func field(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	if !ok {
		t.Fatalf("missing string %s in %v", key, object)
	}
	return value
}

func TestManualLedgerIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("use database/Test-Schema.ps1 -WithApiTests for isolated PostgreSQL tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("invalid test connection")
	}
	defer admin.Close()
	ctx := context.Background()
	var owner, database string
	if err = admin.QueryRow("SELECT current_user,current_database()").Scan(&owner, &database); err != nil || owner != "schema_test" {
		t.Fatal("integration tests require the disposable schema_test cluster")
	}
	grants, err := os.ReadFile("../database/api_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.Exec("CREATE ROLE ipo_api_test NOLOGIN;" + strings.NewReplacer(
		`:"api_role"`, pgx.Identifier{"ipo_api_test"}.Sanitize(),
		`:"database_name"`, pgx.Identifier{database}.Sanitize()).Replace(string(grants)))
	if err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test connection")
	}
	config.RuntimeParams["role"] = "ipo_api_test"
	db := stdlib.OpenDB(*config)
	defer db.Close()
	db.SetMaxOpenConns(10)
	bootstrap := configuration{BusinessName: "Fixture business A", AdminName: "Admin A", AdminPhone: "+919000001001", AdminPassword: "fixture-password-12345"}
	if err = bootstrapAdmin(ctx, db, bootstrap); err != nil {
		t.Fatal(err)
	}
	if err = bootstrapAdmin(ctx, db, bootstrap); err != nil {
		t.Fatal(err)
	}
	second := bootstrap
	second.BusinessName = "Fixture business B"
	second.AdminName = "Admin B"
	second.AdminPhone = "+919000001002"
	if err = bootstrapAdmin(ctx, db, second); err != nil {
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
	status, object, healthHeaders := client.request("GET", "/health", nil, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "status") != "ok" || healthHeaders.Get("Cache-Control") != "no-store" {
		t.Fatal("health response changed or could be cached")
	}
	status, object, _ = client.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, object)
	status, object, _ = client.request("POST", "/api/login", map[string]string{"phone_number": bootstrap.AdminPhone, "password": "wrong-password"}, "")
	expectStatus(t, 401, status, object)
	status, object, _ = client.request("POST", "/api/login", map[string]string{"phone_number": bootstrap.AdminPhone, "password": bootstrap.AdminPassword}, "")
	expectStatus(t, 200, status, object)
	client.Token = field(t, object, "access_token")
	status, object, _ = client.request("GET", "/api/me", nil, "")
	expectStatus(t, 200, status, object)
	businesses := object["businesses"].([]any)
	if len(businesses) != 1 {
		t.Fatalf("bootstrap duplicated memberships: %v", businesses)
	}
	businessID := field(t, businesses[0].(map[string]any), "id")
	base := "/api/businesses/" + businessID
	var otherBusiness, otherCustomer string
	if err = admin.QueryRow("SELECT id FROM ipo_pos.businesses WHERE name=$1", second.BusinessName).Scan(&otherBusiness); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow("INSERT INTO ipo_pos.customers(business_id,name) VALUES($1,'Other customer') RETURNING id", otherBusiness).Scan(&otherCustomer); err != nil {
		t.Fatal(err)
	}
	status, object, _ = client.request("GET", "/api/businesses/"+otherBusiness+"/customers", nil, "")
	expectStatus(t, 403, status, object)
	status, object, _ = client.request("POST", base+"/staff", map[string]string{"name": "Cashier", "phone_number": "+919000001003", "password": "cashier-password-12345"}, "")
	expectStatus(t, 201, status, object)
	cashierID := field(t, object, "user_id")
	cashier := client
	cashier.Token = ""
	status, object, _ = cashier.request("POST", "/api/login", map[string]string{"phone_number": "+919000001003", "password": "cashier-password-12345"}, "")
	expectStatus(t, 200, status, object)
	cashier.Token = field(t, object, "access_token")
	status, object, _ = cashier.request("POST", base+"/ipos", map[string]string{"code": "NO", "name": "No permission"}, "")
	expectStatus(t, 403, status, object)
	status, object, _ = cashier.request("GET", base+"/staff", nil, "")
	expectStatus(t, 403, status, object)
	status, object, _ = client.request("POST", base+"/ipos", map[string]any{"code": "FIXTURE-IPO", "name": "Fixture IPO", "opens_on": "2026-10-05", "closes_on": "2026-10-08", "price_low": "250.5", "price_high": "300"}, "")
	expectStatus(t, 201, status, object)
	ipoID := field(t, object, "id")
	if field(t, object, "price_low") != "250.500000" {
		t.Fatal("IPO decimal response lost precision")
	}
	status, object, _ = cashier.request("GET", base+"/form-types", nil, "")
	expectStatus(t, 200, status, object)
	if len(object["items"].([]any)) != 8 {
		t.Fatal("form catalogue incomplete")
	}
	forms := make(map[string]string)
	for _, item := range object["items"].([]any) {
		form := item.(map[string]any)
		forms[field(t, form, "code")] = field(t, form, "name")
	}
	if forms["SHAREHOLDER"] != "Shareholder" || forms["SHARES"] != "Shares" {
		t.Fatal("shareholder and shares must be distinct form types")
	}
	status, object, _ = cashier.request("POST", base+"/customers", map[string]any{"name": "Same name", "phone_number": "+919000001010", "referral_note": "Neighbour"}, "")
	expectStatus(t, 201, status, object)
	customerID := field(t, object, "id")
	status, object, _ = cashier.request("POST", base+"/customers", map[string]any{"name": "Same name", "referred_by_customer_id": customerID}, "")
	expectStatus(t, 201, status, object)
	status, object, _ = cashier.request("POST", base+"/customers", map[string]any{"name": "Bad referral", "referred_by_customer_id": otherCustomer}, "")
	expectStatus(t, 400, status, object)
	status, object, _ = client.request("GET", base+"/customers?limit=1", nil, "")
	expectStatus(t, 200, status, object)
	firstCustomer := field(t, object["items"].([]any)[0].(map[string]any), "id")
	pageCursor := field(t, object, "next_cursor")
	status, object, _ = client.request("GET", base+"/customers?limit=1&cursor="+pageCursor, nil, "")
	expectStatus(t, 200, status, object)
	if field(t, object["items"].([]any)[0].(map[string]any), "id") == firstCustomer || object["next_cursor"] != nil {
		t.Fatal("pagination repeated or skipped customers")
	}
	testContactNames(t, admin, client, cashier, base, otherBusiness, customerID, cashierID)
	request := map[string]any{"ipo_id": ipoID, "customer_id": customerID, "occurred_at": "2026-10-04T10:00:00+05:30", "form_type_code": "CE", "category": "subject_1", "ce_pe_value": "20", "quantity": "2.5", "rate": "250", "salesperson_user_id": cashierID}
	status, object, _ = cashier.request("POST", base+"/transactions", request, "")
	expectStatus(t, 400, status, object)
	for _, invalid := range []map[string]any{{"quantity": "1.1234567"}, {"quantity": "-1.1234567"}, {"quantity": "0"}, {"quantity": "-0.000"}, {"rate": "-250.1234567"}, {"rate": 250.0}, {"rate": "NaN"}, {"ce_pe_value": nil}, {"category": "unknown"}, {"customer_id": otherCustomer}, {"total_amount": "1"}} {
		copy := cloneMap(request)
		for key, value := range invalid {
			copy[key] = value
		}
		status, object, _ = cashier.request("POST", base+"/transactions", copy, "invalid-"+fmt.Sprint(invalid))
		expectStatus(t, 400, status, object)
	}
	status, object, _ = cashier.request("POST", base+"/transactions", request, "order-1")
	expectStatus(t, 201, status, object)
	transactionID := field(t, object, "id")
	if field(t, object, "total_amount") != "625.00" || object["version"] != float64(1) || field(t, object, "origin") != "manual" {
		t.Fatal("initial transaction calculation/history incorrect")
	}
	equivalent := cloneMap(request)
	equivalent["quantity"] = "2.500000"
	equivalent["occurred_at"] = "2026-10-04T04:30:00Z"
	status, object, headers := cashier.request("POST", base+"/transactions", equivalent, "order-1")
	expectStatus(t, 200, status, object)
	if field(t, object, "id") != transactionID || headers.Get("Idempotency-Replayed") != "true" {
		t.Fatal("retry created a different transaction")
	}
	mismatch := cloneMap(request)
	mismatch["rate"] = "251"
	status, object, _ = cashier.request("POST", base+"/transactions", mismatch, "order-1")
	expectStatus(t, 409, status, object)
	status, object, _ = client.request("POST", base+"/transactions", request, "order-1")
	expectStatus(t, 409, status, object)
	edit := cloneMap(request)
	edit["quantity"] = "3.25"
	edit["ce_pe_value"] = "40.5"
	edit["category"] = "subject_2"
	edit["expected_version"] = 1
	edit["change_reason"] = "Corrected quantity"
	status, object, _ = client.request("PUT", base+"/transactions/"+transactionID, edit, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "total_amount") != "812.50" || object["version"] != float64(2) || field(t, object, "origin") != "manual" {
		t.Fatal("edited calculation/version incorrect")
	}
	status, object, _ = client.request("PUT", base+"/transactions/"+transactionID, edit, "")
	expectStatus(t, 409, status, object)
	status, object, _ = cashier.request("POST", base+"/transactions", request, "order-1")
	expectStatus(t, 200, status, object)
	if object["version"] != float64(2) {
		t.Fatal("creation replay overwrote edited transaction")
	}
	status, object, _ = cashier.request("POST", base+"/transactions/"+transactionID+"/cancel", map[string]any{"expected_version": 2, "change_reason": "Customer cancelled"}, "")
	expectStatus(t, 200, status, object)
	if field(t, object, "status") != "cancelled" || object["version"] != float64(3) {
		t.Fatal("cancellation did not preserve history")
	}
	status, object, _ = cashier.request("POST", base+"/transactions/"+transactionID+"/cancel", map[string]any{"expected_version": 3, "change_reason": "Retry"}, "")
	expectStatus(t, 409, status, object)
	status, object, _ = client.request("GET", base+"/transactions/"+transactionID+"/history?limit=2", nil, "")
	expectStatus(t, 200, status, object)
	history := object["items"].([]any)
	if len(history) != 2 || object["next_before_version"] != float64(2) {
		t.Fatal("history pagination incorrect")
	}
	status, object, _ = client.request("GET", base+"/transactions/"+transactionID+"/history?before_version=2", nil, "")
	expectStatus(t, 200, status, object)
	original := object["items"].([]any)[0].(map[string]any)
	if field(t, original, "total_amount") != "625.00" || field(t, original, "ce_pe_value") != "20.000000" || original["version"] != float64(1) {
		t.Fatal("original snapshot lost")
	}
	status, object, _ = client.request("GET", base+"/transactions?status=cancelled&ipo_id="+ipoID+"&customer_id="+customerID+"&from=2026-10-04T00:00:00Z&to=2026-10-05T00:00:00Z", nil, "")
	expectStatus(t, 200, status, object)
	if len(object["items"].([]any)) != 1 {
		t.Fatal("transaction filters incorrect")
	}
	status, object, _ = client.request("GET", base+"/transactions?status=confirmed", nil, "")
	expectStatus(t, 200, status, object)
	if len(object["items"].([]any)) != 0 {
		t.Fatal("cancelled transaction included in confirmed filter")
	}

	// Concurrent repeated creation creates one current record and one snapshot.
	var wg sync.WaitGroup
	statuses := make(chan int, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := cashier.request("POST", base+"/transactions", request, "concurrent-order")
			statuses <- code
		}()
	}
	wg.Wait()
	close(statuses)
	created := 0
	for code := range statuses {
		if code == 201 {
			created++
		} else if code != 200 {
			t.Fatalf("concurrent creation status %d", code)
		}
	}
	if created != 1 {
		t.Fatalf("concurrent creation inserted %d records", created)
	}
	var current, versions int
	if err = db.QueryRow("SELECT count(*) FROM ipo_pos.transactions WHERE business_id=$1", businessID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM ipo_pos.transaction_versions WHERE business_id=$1", businessID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if current != 2 || versions != 4 {
		t.Fatalf("duplicates or missing snapshots: current=%d versions=%d", current, versions)
	}
	if _, err = db.Exec("UPDATE ipo_pos.transaction_versions SET change_reason='tampered' WHERE business_id=$1", businessID); err == nil {
		t.Fatal("history mutation permitted")
	}
	if _, err = db.Exec("TRUNCATE ipo_pos.transactions CASCADE"); err == nil {
		t.Fatal("API role has truncate privileges")
	}
	if _, err = db.Exec("CREATE TABLE ipo_pos.unwanted(id integer)"); err == nil {
		t.Fatal("API role has schema ownership")
	}
	if _, err = db.Exec("DELETE FROM ipo_pos.transactions WHERE business_id=$1", businessID); err == nil {
		t.Fatal("API role can delete transactions")
	}
	var otherUser string
	if err = admin.QueryRow("SELECT id FROM ipo_pos.users WHERE phone_number=$1", second.AdminPhone).Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	other := client
	other.Token = ""
	status, object, _ = other.request("POST", "/api/login", map[string]string{"phone_number": second.AdminPhone, "password": second.AdminPassword}, "")
	expectStatus(t, 200, status, object)
	other.Token = field(t, object, "access_token")
	t.Run("Signed transactions", func(t *testing.T) {
		testSignedTransactionAPI(t, cashier, base, request)
	})
	t.Run("Shareholder transactions", func(t *testing.T) {
		testShareholderTransactionAPI(t, cashier, base, request)
	})
	t.Run("WhatsApp account ownership", func(t *testing.T) {
		testWhatsAppAccounts(t, db, admin, client, cashier, other, businessID, otherBusiness, cashierID)
	})
	loginCashier := func() {
		app.limiter = loginLimiter{windows: make(map[string]loginWindow)}
		status, object, _ = cashier.request("POST", "/api/login", map[string]string{"phone_number": "+919000001003", "password": "cashier-password-12345"}, "")
		expectStatus(t, 200, status, object)
		cashier.Token = field(t, object, "access_token")
	}
	loginCashier()
	t.Run("WhatsApp pairing lifecycle", func(t *testing.T) {
		testWhatsAppPairing(t, dsn, db, app, client, cashier, other, businessID, cashierID)
	})
	loginCashier()
	t.Run("POS confirmation API", func(t *testing.T) {
		testConfirmationAPI(t, admin, db, client, cashier, other, businessID, otherBusiness, request)
	})
	t.Run("POS confirmation delivery", func(t *testing.T) {
		testConfirmationDelivery(t, dsn, admin, cashier, businessID, cashierID, request)
	})
	loginCashier()
	t.Run("POS confirmation formatting", func(t *testing.T) {
		testConfirmationFormatting(t, admin, client, businessID, request)
	})
	t.Run("Passwordless and multi-device sessions", func(t *testing.T) { testPasswordless(t, dsn, admin, db, app, client, other, businessID) })
	t.Run("Development signup through login", func(t *testing.T) { testDevelopmentSignup(t, dsn, admin, db, app, client, businessID) })
	status, object, _ = other.request("GET", "/api/businesses/"+otherBusiness+"/transactions/"+transactionID, nil, "")
	expectStatus(t, 404, status, object)
	status, object, _ = other.request("GET", "/api/businesses/"+otherBusiness+"/transactions/"+transactionID+"/history", nil, "")
	expectStatus(t, 404, status, object)
	if _, err = admin.Exec("UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE user_id=$1", otherUser); err != nil {
		t.Fatal(err)
	}
	status, object, _ = other.request("GET", "/api/businesses/"+otherBusiness+"/customers", nil, "")
	expectStatus(t, 403, status, object)
	// A fresh API instance/pool authenticates the persisted token.
	reopened := stdlib.OpenDB(*config)
	defer reopened.Close()
	fresh, err := newAPI(reopened)
	if err != nil {
		t.Fatal(err)
	}
	resumed := httptest.NewServer(fresh.routes())
	defer resumed.Close()
	restarted := client
	restarted.URL = resumed.URL
	restarted.HTTP = resumed.Client()
	status, object, _ = restarted.request("GET", "/api/me", nil, "")
	expectStatus(t, 200, status, object)
	status, object, _ = restarted.request("POST", "/api/logout", nil, "")
	expectStatus(t, 204, status, object)
	status, object, _ = client.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, object)
	hashBytes, _ := sessionHash(&http.Request{Header: http.Header{"Authorization": []string{"Bearer " + cashier.Token}}})
	if _, err = admin.Exec("UPDATE ipo_pos.login_sessions SET expires_at=now()-interval '1 second',created_at=now()-interval '1 hour' WHERE token_hash=$1", hashBytes); err != nil {
		t.Fatal(err)
	}
	status, object, _ = cashier.request("GET", "/api/me", nil, "")
	expectStatus(t, 401, status, object)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	status, object, healthHeaders = client.request("GET", "/health", nil, "")
	expectStatus(t, 503, status, object)
	if field(t, object["error"].(map[string]any), "code") != "database_unavailable" || healthHeaders.Get("Cache-Control") != "no-store" {
		t.Fatal("failed health check changed its error contract or could be cached")
	}
}

func cloneMap(input map[string]any) map[string]any {
	copy := make(map[string]any, len(input))
	for key, value := range input {
		copy[key] = value
	}
	return copy
}

func TestLoginLimiterAndRequestBoundaries(t *testing.T) {
	limiter := loginLimiter{windows: map[string]loginWindow{}}
	for i := 0; i < 10; i++ {
		if !limiter.allow("127.0.0.1:1234") {
			t.Fatal("unexpected rate limit")
		}
	}
	if limiter.allow("127.0.0.1:5678") {
		t.Fatal("port change bypassed rate limit")
	}
	limiter.windows["127.0.0.1"] = loginWindow{Until: time.Now().Add(-time.Second)}
	if !limiter.allow("127.0.0.1:1234") {
		t.Fatal("rate window did not reset")
	}
	for _, body := range []string{`{"name":"a","unknown":1}`, `{"name":"a"} {}`, strings.Repeat("x", 65<<10)} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var input struct {
			Name string `json:"name"`
		}
		if decode(w, r, &input) || w.Code != 400 {
			t.Fatal("request boundary accepted malformed/oversized JSON")
		}
	}
}

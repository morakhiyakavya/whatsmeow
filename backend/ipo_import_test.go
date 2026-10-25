package main

import (
	"context"
	"encoding/json"
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

func TestIPOImportDatabaseErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, dsn := range []string{"postgres://user:private-import-secret@localhost:invalid/db", "postgres://user:private-import-secret@127.0.0.1:1/db?sslmode=disable"} {
		db, err := openIPOImportDatabase(ctx, dsn)
		if db != nil || err == nil || strings.Contains(err.Error(), "private-import-secret") {
			t.Fatal("invalid/canceled importer connection did not fail safely")
		}
	}
}

func TestIPOCatalogueIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("use database/Test-Schema.ps1 -WithApiTests")
	}
	db, err := openIPOImportDatabase(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role, database string
	if err = db.QueryRow(`SELECT current_user,current_database()`).Scan(&role, &database); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test cluster")
	}
	if db.Stats().MaxOpenConnections != 4 {
		t.Fatal("importer connection pool is not bounded")
	}
	var searchPath string
	if err = db.QueryRow(`SHOW search_path`).Scan(&searchPath); err != nil || searchPath == "whatsapp" {
		t.Fatal("standalone importer selected WhatsApp storage", err)
	}
	grants, err := os.ReadFile("../database/api_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE ROLE ipo_catalogue_test NOLOGIN;` + strings.NewReplacer(`:"api_role"`, `"ipo_catalogue_test"`, `:"database_name"`, pgx.Identifier{database}.Sanitize()).Replace(string(grants)))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgx.ParseConfig(dsn)
	cfg.RuntimeParams["role"] = "ipo_catalogue_test"
	runtime := stdlib.OpenDB(*cfg)
	defer runtime.Close()
	ctx := context.Background()
	app, err := newAPI(runtime)
	if err != nil {
		t.Fatal(err)
	}
	app.ipoJobToken = strings.Repeat("a", 43)
	app.passwordLogin = true
	if err = bootstrapAdmin(ctx, runtime, configuration{BusinessName: "Catalogue fixture", AdminName: "Catalogue admin", AdminPhone: "+919000009101", AdminPassword: "fixture-password-12345"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.routes())
	defer server.Close()
	client := testClient{t: t, URL: server.URL, HTTP: server.Client()}
	status, body, _ := client.request("POST", "/api/login", map[string]string{"phone_number": "+919000009101", "password": "fixture-password-12345"}, "")
	expectStatus(t, 200, status, body)
	client.Token = field(t, body, "access_token")
	_, body, _ = client.request("GET", "/api/me", nil, "")
	business := body["businesses"].([]any)[0].(map[string]any)["id"].(string)
	base := "/api/businesses/" + business
	status, body, _ = client.request("POST", base+"/ipos", map[string]string{"code": "MANUAL-CATALOGUE", "name": "Source Fixture Limited"}, "")
	expectStatus(t, 201, status, body)
	manualID := field(t, body, "id")
	client.Token = "wrong-job-key"
	status, body, _ = client.request("POST", "/internal/jobs/ipo-refresh", nil, "")
	expectStatus(t, 401, status, body)
	client.Token = app.ipoJobToken
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", server.URL+"/internal/jobs/ipo-refresh", nil)
			req.Header.Set("Authorization", "Bearer "+app.ipoJobToken)
			res, e := server.Client().Do(req)
			if e != nil {
				t.Error(e)
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 202 {
				t.Error(res.StatusCode)
			}
		}()
	}
	wg.Wait()
	var queued int
	if err = db.QueryRow(`SELECT count(*) FROM ipo_pos.ipo_import_runs`).Scan(&queued); err != nil || queued != 1 {
		t.Fatal("duplicate jobs", queued, err)
	}
	for _, query := range []string{`UPDATE ipo_pos.ipo_import_runs SET status='succeeded'`, `INSERT INTO ipo_pos.ipo_subscriptions(ipo_id,category,label,source,observed_at) VALUES('` + manualID + `','total','Total','nse',now())`, `UPDATE ipo_pos.ipos SET board='SME' WHERE id='` + manualID + `'`, `INSERT INTO ipo_pos.ipos(code,name,catalogue_source,source_issue_id) VALUES('FORGED','forged','nse','FORGED')`} {
		if _, err := runtime.Exec(query); err == nil {
			t.Fatal("runtime wrote trusted catalogue state", query)
		}
	}
	ptr := func(s string) *string { return &s }
	var today time.Time
	if err := db.QueryRow(`SELECT (now() AT TIME ZONE 'Asia/Kolkata')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	item := ipoSourceIssue{Symbol: "SOURCEFIXTURE", Name: "Source Fixture Limited", Board: "Mainboard", Open: ptr(today.Format("2006-01-02")), Close: ptr(today.AddDate(0, 0, 3).Format("2006-01-02")), Low: ptr("900"), High: ptr("1000"), Lot: 200, Registrar: "Fixture Registrar", DetailOK: true, EnrichmentOK: true, EnrichmentURL: "https://ipowatch.in/source-fixture-ipo/", Timeline: map[string]any{}, Subscriptions: []ipoSubscription{{Category: "total", Label: "Total", Multiple: ptr("2.5"), Offered: ptr("1000"), Bid: ptr("2500")}}, Reservations: []ipoReservation{{Category: "Retail", Percentage: ptr("35"), Shares: ptr("1000"), Basis: "Net offer"}}, Shareholdings: []ipoShareholding{{Holder: "Promoters", Pre: ptr("90"), Post: ptr("70")}}, Lockins: []ipoLockin{{Label: "30 days", Date: "2026-11-19"}}}
	collect := func(context.Context) ([]ipoSourceIssue, map[string]string) {
		return []ipoSourceIssue{item}, map[string]string{"current": "ok", "upcoming": "ok", "past": "ok"}
	}
	if found, err := runIPOImport(ctx, db, collect); err != nil || !found {
		t.Fatal(found, err)
	}
	var importedID string
	if err = db.QueryRow(`SELECT id FROM ipo_pos.ipos WHERE source_issue_id='SOURCEFIXTURE'`).Scan(&importedID); err != nil || importedID == manualID {
		t.Fatal("manual row was merged", err)
	}
	if err = saveSourceIPO(ctx, db, item); err != nil {
		t.Fatal(err)
	}
	var replayID, jobStatus string
	_ = db.QueryRow(`SELECT id FROM ipo_pos.ipos WHERE source_issue_id='SOURCEFIXTURE'`).Scan(&replayID)
	_ = db.QueryRow(`SELECT status FROM ipo_pos.ipo_import_runs`).Scan(&jobStatus)
	if replayID != importedID || jobStatus != "succeeded" {
		t.Fatal(replayID, importedID, jobStatus)
	}
	client.Token = ""
	status, body, _ = client.request("GET", base+"/ipos/import-status", nil, "")
	expectStatus(t, 401, status, body)
	status, body, _ = client.request("POST", "/api/login", map[string]string{"phone_number": "+919000009101", "password": "fixture-password-12345"}, "")
	expectStatus(t, 200, status, body)
	client.Token = field(t, body, "access_token")
	status, body, _ = client.request("GET", base+"/ipos", nil, "")
	expectStatus(t, 200, status, body)
	found := false
	for _, raw := range body["items"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == importedID {
			found = true
			apps := row["category_applications"].([]any)
			if len(apps) != 3 || apps[1].(map[string]any)["min_amount"] != "400000.00" || len(row["reservations"].([]any)) != 1 || len(row["promoter_shareholdings"].([]any)) != 1 || len(row["anchor_lockins"].([]any)) != 1 {
				t.Fatal(row)
			}
		}
	}
	if !found {
		t.Fatal("imported product not readable")
	}
	// A failed and then incomplete refresh preserves previous terms and enrichments.
	_, err = db.Exec(`UPDATE ipo_pos.ipo_import_runs SET status='pending',next_attempt_at=now()-interval '1 minute'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runIPOImport(ctx, db, func(context.Context) ([]ipoSourceIssue, map[string]string) {
		return nil, map[string]string{"current": "failed"}
	}); err != nil {
		t.Fatal(err)
	}
	partial := ipoSourceIssue{Symbol: item.Symbol, Name: item.Name, Board: item.Board, Subscriptions: []ipoSubscription{{Category: "total", Label: "Total", Multiple: ptr("3")}}}
	if err = saveSourceIPO(ctx, db, partial); err != nil {
		t.Fatal(err)
	}
	var lot int64
	var high string
	if err = db.QueryRow(`SELECT lot_size,price_high::text FROM ipo_pos.ipos WHERE id=$1`, importedID).Scan(&lot, &high); err != nil || lot != 200 || high != "1000.000000" {
		t.Fatal(lot, high, err)
	}
	var offered, bid, multiple string
	if err = db.QueryRow(`SELECT offered_shares::text,bid_shares::text,multiple::text FROM ipo_pos.ipo_subscriptions WHERE ipo_id=$1`, importedID).Scan(&offered, &bid, &multiple); err != nil || offered != "1000" || bid != "2500" || multiple != "3.000000" {
		t.Fatal("partial subscription erased previous fields", offered, bid, multiple, err)
	}
	// A held collector lock prevents a second worker from executing the same work.
	_, _ = db.Exec(`UPDATE ipo_pos.ipo_import_runs SET status='pending',attempts=0,next_attempt_at=now()-interval '1 minute'`)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := runIPOImport(ctx, db, func(context.Context) ([]ipoSourceIssue, map[string]string) {
			close(started)
			<-release
			return collect(ctx)
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	if worked, err := runIPOImport(ctx, db, collect); err != nil || worked {
		t.Fatal("overlapping import", worked, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Restart recovers an expired lease, and a third interrupted attempt becomes terminal.
	_, _ = db.Exec(`UPDATE ipo_pos.ipo_import_runs SET status='running',attempts=1,next_attempt_at=now()-interval '1 minute'`)
	if worked, err := runIPOImport(ctx, db, collect); err != nil || !worked {
		t.Fatal("expired lease not recovered", worked, err)
	}
	_, _ = db.Exec(`UPDATE ipo_pos.ipo_import_runs SET status='running',attempts=3,next_attempt_at=now()-interval '1 minute'`)
	if worked, err := runIPOImport(ctx, db, collect); err != nil || worked {
		t.Fatal("exhausted run replayed", worked, err)
	}
	if err = db.QueryRow(`SELECT status FROM ipo_pos.ipo_import_runs`).Scan(&jobStatus); err != nil || jobStatus != "failed" {
		t.Fatal("exhausted lease not terminal", jobStatus, err)
	}
	status, body, _ = client.request("GET", base+"/ipos?board=invalid", nil, "")
	expectStatus(t, 400, status, body)
	status, body, _ = client.request("GET", base+"/ipos?active=true&board=Mainboard", nil, "")
	expectStatus(t, 200, status, body)
	t.Run("Product picker lifetime", func(t *testing.T) {
		date := func(days int) string { return today.AddDate(0, 0, days).Format("2006-01-02") }
		fixtures := []struct {
			code                              string
			listing, close                    *string
			expected, precision, sourceStatus string
			visible                           bool
		}{
			{"LISTED", ptr(date(-1)), ptr(date(-8)), "", "", "", false},
			{"LISTING-TODAY", ptr(date(0)), ptr(date(-15)), "", "", "", true},
			{"FUTURE-LISTING", ptr(date(3)), ptr(date(-15)), "", "", "", true},
			{"EXPECTED-PAST", nil, nil, date(-1), "date", "", false},
			{"EXPECTED-TODAY", nil, nil, date(0), "date", "", true},
			{"EXPECTED-FUTURE", nil, ptr(date(-15)), date(3), "date", "", true},
			{"CONFIRMED-WINS", ptr(date(-1)), nil, date(3), "date", "", false},
			{"CONFIRMED-FUTURE", ptr(date(3)), nil, date(-1), "date", "", true},
			{"UNKNOWN", nil, nil, "October 2026", "text", "", true},
			{"BAD-EXPECTED", nil, nil, "2026-02-31", "date", "", true},
			{"AWAITING-LISTING", nil, ptr(date(-5)), "", "", "", true},
			{"OLD-UNKNOWN", nil, ptr(date(-8)), "", "", "", false},
			{"WITHDRAWN", ptr(date(3)), nil, "", "", "withdrawn", false},
			{"POSTPONED", nil, nil, "", "", "postponed", false},
		}
		ids := map[string]bool{}
		for _, f := range fixtures {
			timeline, _ := json.Marshal(map[string]any{"ipo listing": map[string]string{"value": f.expected, "precision": f.precision, "status": "expected"}})
			var id string
			if err := db.QueryRow(`INSERT INTO ipo_pos.ipos(code,name,board,listing_on,closes_on,timeline,source_status)
			VALUES($1,$1,'Mainboard',$2,$3,$4,NULLIF($5,'')) RETURNING id`, "LIFETIME-"+f.code, f.listing, f.close, string(timeline), f.sourceStatus).Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids[id] = f.visible
		}
		for _, filter := range []string{"", "&active=true", "&active=false"} {
			seen := map[string]bool{}
			path := base + "/ipos?limit=2&board=Mainboard" + filter
			for pages := 0; pages < 30; pages++ {
				status, result, _ := client.request("GET", path, nil, "")
				expectStatus(t, 200, status, result)
				for _, raw := range result["items"].([]any) {
					id := field(t, raw.(map[string]any), "id")
					if seen[id] {
						t.Fatal("pagination repeated IPO")
					}
					seen[id] = true
				}
				if result["next_cursor"] == nil {
					break
				}
				if pages == 29 {
					t.Fatal("pagination did not finish")
				}
				path = base + "/ipos?limit=2&board=Mainboard" + filter + "&cursor=" + result["next_cursor"].(string)
			}
			for id, want := range ids {
				if seen[id] != want {
					t.Fatalf("lifetime visibility mismatch for %s%s: got %v want %v", id, filter, seen[id], want)
				}
			}
		}
		var retained int
		if db.QueryRow(`SELECT count(*) FROM ipo_pos.ipos WHERE code LIKE 'LIFETIME-%'`).Scan(&retained) != nil || retained != len(fixtures) {
			t.Fatal("hidden products were deleted")
		}
	})
	var state json.RawMessage
	if err = db.QueryRow(`SELECT to_jsonb(j) FROM ipo_pos.ipo_import_runs j`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	t.Log("verified trusted grants, deduplication, source/manual isolation, stable identities, calculations, failure retention and worker locking")
}

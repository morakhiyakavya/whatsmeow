package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type ipoQueryCounter struct{ queries []string }

func (c *ipoQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.queries = append(c.queries, data.SQL)
	return ctx
}

func (*ipoQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestIPOImportBatchIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("use database/Test-Schema.ps1 -WithApiTests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	counter := &ipoQueryCounter{}
	cfg.Tracer = counter
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	var role string
	if err := db.QueryRow(`SELECT current_user`).Scan(&role); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test cluster", err)
	}
	ctx := context.Background()
	ptr := func(s string) *string { return &s }
	items := make([]ipoSourceIssue, 40)
	for n := range items {
		items[n] = ipoSourceIssue{Symbol: fmt.Sprintf("BATCHFIXTURE%02d", n), Name: fmt.Sprintf("Batch fixture %02d", n), Board: "Mainboard",
			Low: ptr("100.25"), High: ptr("101.25"), DetailOK: true, Lot: 150, Registrar: "Fixture registrar",
			IssueText: "Fixture issue size", IssueShares: ptr("123456789012345678901234"), IssueAmount: ptr("1000000.25"),
			EnrichmentOK: true, EnrichmentURL: "https://ipowatch.in/batch-fixture-ipo/",
			Timeline:      map[string]any{"listing": map[string]string{"value": "2026-11-01", "status": "expected", "precision": "date"}},
			Subscriptions: []ipoSubscription{{Category: "total", Label: "Total", Offered: ptr("1000"), Multiple: ptr("2.5")}},
			Reservations: []ipoReservation{
				{Category: "Retail", Percentage: ptr("30"), Basis: "Earlier table"},
				{Category: "Retail", Percentage: ptr("35"), Basis: "Net offer"}},
			Shareholdings: []ipoShareholding{
				{Holder: "Promoters", Pre: ptr("95"), Post: ptr("75")},
				{Holder: "Promoters", Pre: ptr("90"), Post: ptr("70")}},
			Lockins: []ipoLockin{
				{Label: "Published expiry", Date: "2026-11-18"},
				{Label: "Published expiry", Date: "2026-11-19"}},
		}
	}
	queriesFor := func(items []ipoSourceIssue) int {
		t.Helper()
		counter.queries = nil
		if err := saveSourceIPOs(ctx, db, items); err != nil {
			t.Fatal(err)
		}
		begins, commits := 0, 0
		for _, query := range counter.queries {
			if strings.EqualFold(query, "begin") {
				begins++
			}
			if strings.EqualFold(query, "commit") {
				commits++
			}
		}
		if begins != 1 || commits != 1 {
			t.Fatalf("batch used %d transactions and %d commits", begins, commits)
		}
		return len(counter.queries)
	}
	single, batch := queriesFor(items[:1]), queriesFor(items[1:])
	if single != batch || batch > 11 {
		t.Fatalf("query count grows with IPO count: single=%d batch=%d", single, batch)
	}
	t.Logf("one IPO and 39 IPOs each use %d database calls, including one BEGIN/COMMIT", batch)
	// Replaying identical facts must not create extra history versions.
	queriesFor(items)
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM ipo_pos.ipo_dashboard_basis_versions v JOIN ipo_pos.ipos i ON i.id=v.ipo_id
	 WHERE i.source_issue_id LIKE 'BATCHFIXTURE%'`).Scan(&count); err != nil || count != 2*len(items) {
		t.Fatal("source history changed on replay", count, err)
	}
	// Sparse refreshes retain terms and enrichments, and merge subscription fields.
	partial := ipoSourceIssue{Symbol: items[0].Symbol, Name: items[0].Name, Board: items[0].Board,
		Subscriptions: []ipoSubscription{{Category: "total", Label: "Total", Multiple: ptr("3")}}}
	if err := saveSourceIPOs(ctx, db, []ipoSourceIssue{partial}); err != nil {
		t.Fatal(err)
	}
	var complete bool
	if err := db.QueryRow(`SELECT i.lot_size=150 AND i.registrar='Fixture registrar' AND i.price_high=101.25
	 AND i.issue_size_shares=123456789012345678901234 AND i.issue_size_amount=1000000.25
	 AND i.timeline->'listing'->>'value'='2026-11-01' AND s.offered_shares=1000 AND s.multiple=3
	 AND r.percentage=35 AND p.pre_percentage=90 AND p.post_percentage=70 AND l.expires_on='2026-11-19'
	 FROM ipo_pos.ipos i JOIN ipo_pos.ipo_subscriptions s ON s.ipo_id=i.id
	 JOIN ipo_pos.ipo_reservations r ON r.ipo_id=i.id JOIN ipo_pos.ipo_promoter_shareholdings p ON p.ipo_id=i.id
	 JOIN ipo_pos.ipo_anchor_lockins l ON l.ipo_id=i.id WHERE i.source_issue_id=$1`, partial.Symbol).Scan(&complete); err != nil || !complete {
		t.Fatal("sparse batch erased facts or exact numbers", err)
	}
	// A late constraint failure must roll back the IPO and its initial history too.
	bad := items[1]
	bad.Symbol, bad.Name = "BATCHINVALID", "Invalid batch fixture"
	bad.Reservations = []ipoReservation{{Category: "Retail", Percentage: ptr("101"), Basis: "Net offer"}}
	good := ipoSourceIssue{Symbol: "BATCHVALID", Name: "Valid batch fixture", Board: "SME"}
	changed := items[3]
	changed.High = ptr("999")
	if err := saveSourceIPOs(ctx, db, []ipoSourceIssue{changed, good, bad}); err == nil {
		t.Fatal("invalid percentage accepted")
	}
	if err := db.QueryRow(`SELECT count(*) FROM ipo_pos.ipos WHERE source_issue_id IN ('BATCHVALID','BATCHINVALID')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed batch committed catalogue rows", count, err)
	}
	if err := db.QueryRow(`SELECT price_high=101.25 AND (SELECT count(*) FROM ipo_pos.ipo_dashboard_basis_versions v WHERE v.ipo_id=i.id)=2
	 FROM ipo_pos.ipos i WHERE source_issue_id=$1`, changed.Symbol).Scan(&complete); err != nil || !complete {
		t.Fatal("failed batch altered existing facts/history", err)
	}
	identityConflict := items[2]
	identityConflict.Name = "Unexpected renamed issuer"
	invalid := ipoSourceIssue{Symbol: "INVALID SPACE", Name: "Invalid identity", Board: "Mainboard"}
	results := map[string]string{}
	saved, err := persistSourceIPOs(ctx, db, []ipoSourceIssue{good, bad, identityConflict, invalid, partial}, results)
	if err == nil || saved != 2 || len(results) != 3 || results["storage:"+bad.Symbol] != "failed" || results["storage:"+identityConflict.Symbol] != "failed" || results["storage:"+invalid.Symbol] != "failed" {
		t.Fatal("failed rows blocked valid IPOs or were miscounted", saved, results, err)
	}
	var absentID string
	if err := db.QueryRow(`SELECT id FROM ipo_pos.ipos WHERE source_issue_id=$1`, bad.Symbol).Scan(&absentID); err != sql.ErrNoRows {
		t.Fatal("invalid issue survived rollback", err)
	}
	// Cancelled work must return promptly without committing a valid subset.
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	good.Symbol = "BATCHCANCELED"
	saved, err = persistSourceIPOs(cancelCtx, db, []ipoSourceIssue{good, partial}, map[string]string{})
	if saved != 0 || err == nil {
		t.Fatal("canceled batch was committed", saved, err)
	}
	t.Run("worker batch boundary and partial accounting", func(t *testing.T) {
		var jobID string
		if err := db.QueryRow(`INSERT INTO ipo_pos.ipo_import_runs(scheduled_for) VALUES('2000-01-01') RETURNING id`).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM ipo_pos.ipo_import_runs WHERE id=$1`, jobID)
		catalogue := make([]ipoSourceIssue, 101)
		for n := range catalogue {
			catalogue[n] = ipoSourceIssue{Symbol: fmt.Sprintf("BATCHJOB%03d", n), Name: fmt.Sprintf("Batch job fixture %03d", n), Board: "SME"}
		}
		counter.queries = nil
		worked, err := runIPOImport(ctx, db, func(context.Context) ([]ipoSourceIssue, map[string]string) {
			return catalogue, map[string]string{"current": "ok"}
		})
		if err != nil || !worked {
			t.Fatal("worker batch failed", worked, err)
		}
		begins := 0
		for _, query := range counter.queries {
			if strings.EqualFold(query, "begin") {
				begins++
			}
		}
		if begins != 2 {
			t.Fatal("worker did not use bounded batches", begins)
		}
		var status string
		if err := db.QueryRow(`SELECT status,imported_count FROM ipo_pos.ipo_import_runs WHERE id=$1`, jobID).Scan(&status, &count); err != nil || status != "succeeded" || count != len(catalogue) {
			t.Fatal("successful job accounting", status, count, err)
		}
		if _, err := db.Exec(`UPDATE ipo_pos.ipo_import_runs SET status='pending',next_attempt_at=now() WHERE id=$1`, jobID); err != nil {
			t.Fatal(err)
		}
		worked, err = runIPOImport(ctx, db, func(context.Context) ([]ipoSourceIssue, map[string]string) {
			return []ipoSourceIssue{catalogue[0], bad}, map[string]string{"current": "ok"}
		})
		if err != nil || !worked {
			t.Fatal("partial worker batch failed", worked, err)
		}
		if err := db.QueryRow(`SELECT status,imported_count,source_results->>'storage:BATCHINVALID'='failed'
		 FROM ipo_pos.ipo_import_runs WHERE id=$1`, jobID).Scan(&status, &count, &complete); err != nil || status != "partial" || count != 1 || !complete {
			t.Fatal("partial job accounting", status, count, complete, err)
		}
	})
}

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func ipoFilingCalendarFixture(rows []ipoCalendarEntry) string {
	wire, _ := json.Marshal(map[string]any{"initialIpos": rows})
	flight, _ := json.Marshal([]any{1, "2:" + string(wire)})
	return "<script>self.__next_f.push(" + string(flight) + ")</script>"
}

func TestIPOFilingCalendar(t *testing.T) {
	const filing = "https://www.sebi.gov.in/filings/public-issues/jul-2025/foodlink-fandb-holdings-india-limited_94988.html"
	page := `<table><tr><td>Draft Offer Documents filed with SEBI</td>
		<td><a href="` + filing + `">Foodlink F&amp;B Holdings India Limited</a></td></tr></table>`
	c := newIPOSourceClient()
	requests := 0
	c.http.Transport = ipoTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host != sebiIPOHost || r.URL.Query().Get("search") != "Foodlink F&B Holdings" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(page)), Header: make(http.Header)}, nil
	})
	schedules := []ipoCalendarEntry{
		{"Foodlink F&B Holdings", "Mainboard", "Upcoming", "25 Nov 2026", "27 Nov 2026"},
		{"Hindustan Coca Cola", "Mainboard", "Upcoming", "TBA", "TBA"},
		{"Existing IPO", "Mainboard", "Upcoming", "25 Nov 2026", "27 Nov 2026"},
		{"Old IPO", "Mainboard", "Upcoming", "01 Nov 2026", "03 Nov 2026"},
	}
	items := c.collectFilingCalendar(context.Background(), []byte(ipoFilingCalendarFixture(schedules)),
		[]ipoSourceIssue{{Name: "Existing IPO Limited", Board: "Mainboard"}}, "2026-11-18", map[string]string{})
	if requests != 1 || len(items) != 2 {
		t.Fatal(requests, items)
	}
	item := items[1]
	wire, _ := json.Marshal(item.Timeline)
	if item.Name != "Foodlink F&B Holdings India Limited" || item.FilingURL != filing || item.Open != nil ||
		item.Close != nil || item.High != nil || item.Lot != 0 || expectedIPODate(wire, "opening") != "2026-11-25" ||
		expectedIPODate(wire, "closing") != "2026-11-27" {
		t.Fatal(item)
	}
	if _, found := matchIPOFiling([]byte(strings.ReplaceAll(page, sebiIPOHost, "other.invalid")), "Foodlink F&B Holdings"); found != "" {
		t.Fatal("accepted non-SEBI filing")
	}
}

func TestIPOFilingCalendarIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("use disposable schema_test database")
	}
	ctx := context.Background()
	db, err := openIPOImportDatabase(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err := db.QueryRow(`SELECT current_user`).Scan(&role); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test database")
	}
	today := time.Now().In(time.FixedZone("IST", 19800))
	open, close := today.AddDate(0, 0, 2).Format(time.DateOnly), today.AddDate(0, 0, 4).Format(time.DateOnly)
	item := ipoSourceIssue{Name: "Calendar Fixture Limited", Board: "Mainboard",
		FilingURL: "https://www.sebi.gov.in/filings/public-issues/nov-2026/calendar-fixture_001.html",
		Timeline: map[string]any{
			"opening": map[string]string{"value": open, "precision": "date", "status": "expected"},
			"closing": map[string]string{"value": close, "precision": "date", "status": "expected"},
		}}
	var jobID string
	if err := db.QueryRow(`INSERT INTO ipo_pos.ipo_import_runs(scheduled_for) VALUES('2000-01-02') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM ipo_pos.ipo_import_runs WHERE id=$1`, jobID)
	worked, err := runIPOImport(ctx, db, func(context.Context) ([]ipoSourceIssue, map[string]string) {
		return []ipoSourceIssue{item}, map[string]string{"current": "source request failed", "filing calendar": "ok"}
	})
	if !worked || err != nil {
		t.Fatal(worked, err)
	}
	var status string
	var saved int
	if err := db.QueryRow(`SELECT status, imported_count FROM ipo_pos.ipo_import_runs WHERE id=$1`, jobID).Scan(&status, &saved); err != nil || status != "partial" || saved != 1 {
		t.Fatal(status, saved, err)
	}
	var id, eligible string
	if err := db.QueryRow(`SELECT i.id,lifecycle.selectable_until::text FROM ipo_pos.ipos i `+ipoLifecycleJoin+
		` WHERE i.name=$1 AND i.catalogue_source='filing' AND opens_on IS NULL AND closes_on IS NULL`, item.Name).
		Scan(&id, &eligible); err != nil || eligible != close {
		t.Fatal(id, eligible, err)
	}
	live := ipoSourceIssue{Symbol: "CALFIX", Name: item.Name, Board: item.Board, Open: &open, Close: &close}
	if err := saveSourceIPOs(ctx, db, []ipoSourceIssue{live}); err != nil {
		t.Fatal(err)
	}
	if n, err := saveFilingIPOs(ctx, db, []ipoSourceIssue{item}); err != nil || n != 0 {
		t.Fatal("filing recreated an exchange-owned record", n, err)
	}
	var currentID string
	var count int
	if err := db.QueryRow(`SELECT i.id,(SELECT count(*) FROM ipo_pos.ipos WHERE name=$1) FROM ipo_pos.ipos i
		WHERE i.name=$1 AND catalogue_source='nse' AND code='NSE:CALFIX' AND symbol='CALFIX'
		AND opens_on IS NOT NULL AND NOT timeline ? 'opening'`, item.Name).
		Scan(&currentID, &count); err != nil || currentID != id || count != 1 {
		t.Fatal("provisional IPO wasn't adopted exactly once", currentID, count, err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIPOApplications(t *testing.T) {
	for _, test := range []struct {
		board, price, open string
		lot                int64
		want               []int64
	}{
		{"Mainboard", "1000", "2026-10-19", 200, []int64{1, 2, 6}},
		{"Mainboard", "100", "2026-10-19", 1000, []int64{1, 3, 11}},
		{"Mainboard", "125.5", "2026-10-19", 100, []int64{1, 16, 80}},
		{"SME", "100", "2026-10-19", 2000, []int64{2, 3, 6}},
		{"SME", "100", "2025-06-30", 2000, nil},
		{"SME", "100", "2026-10-19", 1000, nil},
		{"Mainboard", "0", "2026-10-19", 100, nil},
		{"Mainboard", "10", "2026-10-19", 0, nil},
		{"unknown", "100", "2026-10-19", 100, nil},
	} {
		got := ipoApplications(test.board, test.price, test.lot, test.open)
		if len(got) != len(test.want) {
			t.Fatalf("%+v: %+v", test, got)
		}
		for i, want := range test.want {
			if got[i].MinLots != want || got[i].Currency != "INR" {
				t.Fatalf("%+v: %+v", test, got)
			}
		}
	}
	apps := ipoApplications("Mainboard", "1000", 200, "2026-10-19")
	if apps[1].Amount != "400000.00" || apps[1].MinShares != "400" || *apps[1].MaxLots != 5 {
		t.Fatal(apps)
	}
}

func TestIPOSourceParsing(t *testing.T) {
	input := `[{"symbol":"EXAMPLE","companyName":"Example India Limited","series":"EQ","issueStartDate":"15-OCT-2026","issueEndDate":"19-Oct-2026","issuePrice":"Rs. 90 to Rs. 100","noOfTime":"0","noOfSharesOffered":"1E6","noOfsharesBid":"0"}]`
	items, err := parseIPOFeed([]byte(input), false, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	item := items[0]
	if *item.Open != "2026-10-15" || *item.High != "100.000000" || item.Subscriptions[0].Multiple == nil || *item.Subscriptions[0].Offered != "1000000" {
		t.Fatal(item)
	}
	for _, bad := range []string{`{"error":"blocked"}`, `[{"companyName":"Missing symbol"}]`, strings.Replace(input, "19-Oct-2026", "31-Feb-2026", 1), strings.Replace(input, "Rs. 90 to Rs. 100", "Rs. 120 to Rs. 100", 1)} {
		if _, err := parseIPOFeed([]byte(bad), false, time.Now()); err == nil {
			t.Fatal("invalid source accepted", bad)
		}
	}
	for _, bad := range []string{"NaN", "Infinity", "-1", "1e10000000", "1/2"} {
		if n := sourceNumber(bad, true); n != nil {
			t.Fatal("invalid count accepted", bad, *n)
		}
	}
	if n := sourceNumber("3.22871892E8", true); n == nil || *n != "322871892" {
		t.Fatal(n)
	}
	if sourceNumber("1/2", false) != nil || percentage("-5%") != nil {
		t.Fatal("non-decimal or negative percentage accepted")
	}
	expected := strings.ReplaceAll(strings.ReplaceAll(input, "15-OCT-2026", "November 2026"), "19-Oct-2026", "TBA")
	if future, err := parseIPOFeed([]byte(expected), false, time.Now()); err != nil || future[0].Open != nil || future[0].Timeline["opening"] == nil {
		t.Fatal("tentative date lost", future, err)
	}
	detail := `{"issueInfo":{"symbol":"EXAMPLE","dataList":[{"title":"Lot Size","value":"2000 Equity Shares"},{"title":"Minimum Order Quantity","value":"4000 Equity Shares"},{"title":"Name of the Registrar","value":"Example Registrar"}]},"bidDetails":[{"srNo":"3","category":"Individual Investors","noOfshareBid":"12000","noofapplication":"3","noOfTime":"0","noOfSharesOffered":"0"}]}`
	item.Board = "SME"
	if err := parseIPODetail([]byte(detail), &item); err != nil || item.Lot != 2000 || !item.DetailOK {
		t.Fatal(item, err)
	}
	s := item.Subscriptions[len(item.Subscriptions)-1]
	if s.Category != "individual" || s.Multiple != nil || *s.Applications != "3" {
		t.Fatal(s)
	}
	if err := parseIPODetail([]byte(strings.Replace(detail, "EXAMPLE", "OTHER", 1)), &item); err == nil {
		t.Fatal("wrong identity accepted")
	}
}

func TestIPOEnrichment(t *testing.T) {
	fixture := `<html><h1>Example India IPO</h1>
<table><tr><th>Investor Category</th><th>Shares</th><th>Percentage</th></tr><tr><td>Retail</td><td>1,000</td><td>35%</td></tr></table>
<table><tr><th>Particular</th><th>Pre IPO %</th><th>Post IPO %</th></tr><tr><td>Promoters</td><td>90%</td><td>70%</td></tr></table>
<table><tr><td>Anchor lock-in period end date (30 days)</td><td>Nov 19, 2026</td></tr><tr><td>Anchor lock-in period end date (90 days)</td><td>TBA</td></tr></table>
<table><tr><td>IPO Listing</td><td>October 22, 2026</td></tr><tr><td>IPO Issue Size</td><td>INR 12.50 Crore</td></tr></table></html>`
	item := ipoSourceIssue{Name: "Example India Limited"}
	if err := parseIPOEnrichment(strings.NewReader(fixture), &item, "https://ipowatch.in/example-india-ipo/"); err != nil {
		t.Fatal(err)
	}
	if len(item.Reservations) != 1 || len(item.Shareholdings) != 1 || len(item.Lockins) != 1 || *item.IssueAmount != "125000000.00" || !item.EnrichmentOK {
		t.Fatalf("%+v", item)
	}
	if item.Lockins[0].Date != "2026-11-19" || *item.Shareholdings[0].Post != "70.000000" {
		t.Fatal(item)
	}
	other := ipoSourceIssue{Name: "Other Issuer Limited"}
	if err := parseIPOEnrichment(strings.NewReader(fixture), &other, "https://ipowatch.in/example-india-ipo/"); err == nil {
		t.Fatal("wrong issuer accepted")
	}
	misleading := strings.Replace(fixture, "<h1>Example India IPO</h1>", "<h1>Another Company IPO</h1><aside>Example India IPO</aside>", 1)
	if err := parseIPOEnrichment(strings.NewReader(misleading), &item, "https://ipowatch.in/example-india-ipo/"); err == nil {
		t.Fatal("sidebar reference accepted as issuer identity")
	}
	changed := strings.ReplaceAll(fixture, "Investor Category", "Unrecognised Header")
	changed = strings.ReplaceAll(changed, "Particular", "Other")
	if err := parseIPOEnrichment(strings.NewReader(changed), &item, "https://ipowatch.in/example-india-ipo/"); err != nil {
		t.Fatal("independent fields should survive", err)
	}
}

type ipoTransport func(*http.Request) (*http.Response, error)

func (f ipoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIPOSourceFailureAndHostBounds(t *testing.T) {
	s := &ipoSourceClient{http: &http.Client{Transport: ipoTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
	})}}
	items, results := s.collect(context.Background())
	if len(items) != 0 || results["current"] != "source HTTP 503" || results["upcoming"] != "source HTTP 503" || results["past"] != "source HTTP 503" {
		t.Fatal(items, results)
	}
	for _, host := range []string{"http://www.nseindia.com/api", "https://attacker.invalid/a", "https://www.nseindia.com.attacker.invalid/"} {
		if _, err := s.get(context.Background(), host); err == nil {
			t.Fatal("source host not bounded", host)
		}
	}
	app := &api{}
	r := httptest.NewRequest("POST", "/internal/jobs/ipo-refresh", nil)
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestIPOSourceSession(t *testing.T) {
	s := newIPOSourceClient()
	warmed := false
	s.http.Transport = ipoTransport(func(r *http.Request) (*http.Response, error) {
		headers := make(http.Header)
		body := "[]"
		if r.URL.Path == "/" {
			warmed = true
			headers.Add("Set-Cookie", "nse_session=fixture; Path=/; Secure")
			body = "<html></html>"
		} else {
			cookie, err := r.Cookie("nse_session")
			if !warmed || err != nil || cookie.Value != "fixture" || r.Referer() != "https://www.nseindia.com/market-data/all-upcoming-issues-ipo" || r.Header.Get("Accept-Language") == "" {
				t.Fatal("NSE API request lacks the established site session")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: headers}, nil
	})
	_, results := s.collect(context.Background())
	if results["current"] != "ok" || results["upcoming"] != "ok" || results["past"] != "ok" {
		t.Fatal(results)
	}
}

func TestIPOLiveSources(t *testing.T) {
	if os.Getenv("IPO_LIVE_SOURCE_TEST") != "1" {
		t.Skip("explicit read-only live source check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	items, results := newIPOSourceClient().collect(ctx)
	if len(items) == 0 {
		t.Fatal("no source IPOs", results)
	}
	details, enrichment, reservations, holdings, lockins := 0, 0, 0, 0, 0
	for _, item := range items {
		if item.DetailOK {
			details++
		}
		if item.EnrichmentOK {
			enrichment++
		}
		reservations += len(item.Reservations)
		holdings += len(item.Shareholdings)
		lockins += len(item.Lockins)
	}
	encoded, _ := json.Marshal(results)
	t.Logf("issues=%d details=%d enrichment=%d reservation_rows=%d shareholding_rows=%d lockin_rows=%d results=%s", len(items), details, enrichment, reservations, holdings, lockins, encoded)
	if reservations == 0 || holdings == 0 {
		t.Fatal("selected live enrichment was not demonstrated")
	}
}

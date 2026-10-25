package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func enrichmentFixture(name string) string {
	return `<h1>` + name + ` IPO Date, Review</h1><table><tr><th>Investor Category</th><th>Shares (%)</th></tr><tr><td>Retail</td><td>35%</td></tr></table>`
}

func TestIPOEnrichmentMappings(t *testing.T) {
	for symbol, mapping := range ipoEnrichmentMappings {
		t.Run(symbol, func(t *testing.T) {
			item := ipoSourceIssue{Symbol: symbol, Name: mapping.name}
			s := &ipoSourceClient{http: &http.Client{Transport: ipoTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != mapping.url {
					t.Fatal("verified source was not preferred", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(enrichmentFixture(mapping.title))), Header: make(http.Header)}, nil
			})}}
			if err := s.enrich(context.Background(), &item, nil); err != nil || !item.EnrichmentOK || item.EnrichmentURL != mapping.url {
				t.Fatal("verified mapping failed", err)
			}
			wrong := ipoSourceIssue{Symbol: symbol, Name: "Different Issuer Limited"}
			if err := parseIPOEnrichment(strings.NewReader(enrichmentFixture(mapping.title)), &wrong, mapping.url); err == nil {
				t.Fatal("reused NSE symbol bypassed issuer validation")
			}
			if issuerKey(mapping.name) != issuerKey(mapping.title) {
				if err := parseIPOEnrichment(strings.NewReader(enrichmentFixture(mapping.title)), &item, "https://ipowatch.in/other-ipo/"); err == nil {
					t.Fatal("alias accepted outside its verified page")
				}
			}
		})
	}
}

func TestIPOEnrichmentLinkDiscovery(t *testing.T) {
	fixture := `<a href="/published-name-ipo/">Example (India) Ltd IPO</a>
<a href="https://ipowatch.in/published-name-ipo#dates">Example India Limited</a>
<a href="/first-ipo/">Ambiguous Limited</a><a href="/second-ipo/">Ambiguous Ltd</a><a href="/first-ipo/">Ambiguous</a>
<a href="https://attacker.invalid/foreign-ipo/">Foreign</a>
<a href="http://ipowatch.in/insecure-ipo/">Insecure</a>
<a href="https://user:password@ipowatch.in/credentials-ipo/">Credentials</a>
<a href="/tracking-ipo/?token=private">Tracking</a>
<a href="/example-ipo-gmp-grey-market-premium/">GMP Page</a>
<a href="/encoded%2fpath-ipo/">Encoded</a>`
	links, err := parseIPOEnrichmentLinks(strings.NewReader(fixture))
	if err != nil || links[enrichmentLinkKey("Example India Limited")] != "https://ipowatch.in/published-name-ipo/" {
		t.Fatal("published relative/absolute link was not resolved", err, links)
	}
	if len(links) != 2 || links["ambiguous"] != "" {
		t.Fatal("untrusted URLs or ambiguous names entered discovery", links)
	}
}

func TestIPOEnrichmentFailureReasons(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{"blocked", "blocked", "source HTTP 403", 403},
		{"missing", "missing", "source HTTP 404", 404},
		{"soft missing", `<title>Page not found - IPO Watch</title><h1>404 Page Not Found</h1>`, "enrichment page missing", 200},
		{"wrong issuer", enrichmentFixture("Wrong Company"), "enrichment issuer unverified", 200},
		{"missing fields", `<h1>Example IPO</h1>`, "enrichment fields unavailable", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			s := &ipoSourceClient{http: &http.Client{Transport: ipoTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
			})}}
			item := ipoSourceIssue{Name: "Example Limited"}
			if err := s.enrich(context.Background(), &item, map[string]string{"example": "https://ipowatch.in/example-ipo/"}); err == nil || err.Error() != test.want || item.EnrichmentOK {
				t.Fatal("specific failure lost", err)
			}
			if requests != 1 {
				t.Fatal("duplicate candidate requests", requests)
			}
		})
	}
	t.Run("private transport error", func(t *testing.T) {
		s := &ipoSourceClient{http: &http.Client{Transport: ipoTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("private transport details")
		})}}
		if err := s.enrich(context.Background(), &ipoSourceIssue{Name: "Example Limited"}, nil); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("transport details leaked", err)
		}
	})
}

func TestIPOEnrichmentIndexOncePerRefresh(t *testing.T) {
	indexRequests := 0
	today := time.Now().Format("2006-01-02")
	s := &ipoSourceClient{http: &http.Client{Transport: ipoTransport(func(r *http.Request) (*http.Response, error) {
		body := "[]"
		switch r.URL.Path {
		case "/":
			body = "<html></html>"
		case "/api/ipo-current-issue":
			body = fmt.Sprintf(`[{"symbol":"ALPHA","companyName":"Example Alpha Limited","series":"EQ","issueStartDate":"%s"},{"symbol":"BETA","companyName":"Example Beta Limited","series":"EQ","issueStartDate":"%s"}]`, today, today)
		case "/api/ipo-detail":
			body = fmt.Sprintf(`{"issueInfo":{"symbol":%q,"dataList":[{"title":"Lot Size","value":"100"}]}}`, r.URL.Query().Get("symbol"))
		case "/upcoming-ipo-list/":
			indexRequests++
			body = `<a href="/published-alpha-ipo/">Example Alpha</a><a href="/published-beta-ipo/">Example Beta</a>`
		case "/published-alpha-ipo/":
			body = enrichmentFixture("Example Alpha")
		case "/published-beta-ipo/":
			body = enrichmentFixture("Example Beta")
		case "/api/public-past-issues", "/api/all-upcoming-issues":
		default:
			t.Fatal("unexpected request instead of a discovered link", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	for refresh := 1; refresh <= 2; refresh++ {
		items, results := s.collect(context.Background())
		if len(items) != 2 || indexRequests != refresh || results["enrichment:ALPHA"] != "ok" || results["enrichment:BETA"] != "ok" {
			t.Fatal("index was not shared within each refresh", indexRequests, results)
		}
	}
}

func TestIPOEnrichmentLiveMappings(t *testing.T) {
	if os.Getenv("IPO_LIVE_SOURCE_TEST") != "1" {
		t.Skip("explicit read-only live source check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s := newIPOSourceClient()
	data, err := s.get(ctx, ipoEnrichmentIndex)
	if err != nil {
		t.Fatal(err)
	}
	links, err := parseIPOEnrichmentLinks(strings.NewReader(string(data)))
	if err != nil || len(links) == 0 {
		t.Fatal("published link discovery failed", err)
	}
	for symbol, mapping := range ipoEnrichmentMappings {
		item := ipoSourceIssue{Symbol: symbol, Name: mapping.name}
		if err := s.enrich(ctx, &item, links); err != nil || len(item.Reservations) == 0 || len(item.Shareholdings) == 0 {
			t.Fatal(symbol, "enrichment failed", err)
		}
		t.Logf("%s: reservations=%d promoter_rows=%d source=%s", symbol, len(item.Reservations), len(item.Shareholdings), item.EnrichmentURL)
	}
}

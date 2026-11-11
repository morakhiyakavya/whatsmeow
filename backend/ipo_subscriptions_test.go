package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const combinedIPODetailFixture = `{
 "issueInfo":{"symbol":"EXAMPLE","dataList":[{"title":"Bid Lot","value":"20 Equity Shares"}]},
 "activeCat":{"updateTime":"Updated as on 28-Sep-2026 14:39:00","dataList":[
  {"srNo":"Sr.No.","category":"Category","noOfShareOffered":"No. of shares"},
  {"srNo":"1","category":"QIB","noOfShareOffered":"100","noOfSharesBid":"200","noOfTotalMeant":"2"},
  {"srNo":"2","category":"NII","noOfShareOffered":"300","noOfSharesBid":"900","noOfTotalMeant":"3"},
  {"srNo":"2.1","category":"Large NII","noOfShareOffered":"200","noOfSharesBid":"600","noOfTotalMeant":"3"},
  {"srNo":"2.2","category":"Small NII","noOfShareOffered":"100","noOfSharesBid":"300","noOfTotalMeant":"3"},
  {"srNo":"3","category":"Retail","noOfShareOffered":"600","noOfSharesBid":"900","noOfTotalMeant":"1.5"},
  {"srNo":null,"category":"Total","noOfShareOffered":"1E3","noOfSharesBid":"2E3","noOfTotalMeant":"2"}
 ]},
 "bidDetails":[{"srNo":"3","category":"Retail NSE only","noOfSharesOffered":"600","noOfsharesBid":"500","noOfTime":"0.833333","noofapplication":"25"}]
}`

func TestCombinedIPOSubscriptions(t *testing.T) {
	item := ipoSourceIssue{Symbol: "EXAMPLE", Board: "Mainboard"}
	if err := parseIPODetail([]byte(combinedIPODetailFixture), &item); err != nil {
		t.Fatal(err)
	}
	if len(item.Subscriptions) != 6 {
		t.Fatal("combined categories lost or header imported", item.Subscriptions)
	}
	for _, row := range item.Subscriptions {
		if row.Coverage != "NSE+BSE" || row.Applications != nil || row.SourceObservedAt == nil || row.SourceObservedAt.UTC().Format(time.RFC3339) != "2026-09-28T09:09:00Z" {
			t.Fatal("wrong coverage, NSE-only application count or source timestamp", row)
		}
		if row.Category == "retail" && (*row.Bid != "900" || *row.Multiple != "1.500000") {
			t.Fatal("combined figures replaced by NSE-only bids", row)
		}
		if row.Category == "total" && (*row.Bid != "2000" || *row.Offered != "1000") {
			t.Fatal("published total was summed with overlapping categories", row)
		}
	}
	for _, active := range []string{`null`, `{}`, `{"dataList":[]}`,
		`{"dataList":[{"srNo":"Sr.No.","category":"Category","noOfShareOffered":"No. of shares"}]}`,
		`{"dataList":[{"srNo":"3","category":"Retail","noOfShareOffered":"-1","noOfSharesBid":"unknown"}]}`} {
		var body map[string]json.RawMessage
		_ = json.Unmarshal([]byte(combinedIPODetailFixture), &body)
		body["activeCat"] = json.RawMessage(active)
		input, _ := json.Marshal(body)
		missing := ipoSourceIssue{Symbol: "EXAMPLE", Board: "Mainboard"}
		if err := parseIPODetail(input, &missing); err != nil || len(missing.Subscriptions) != 1 || !missing.DetailOK {
			t.Fatal("missing combined data must retain NSE-only categories while accepting terms", missing, err)
		}
		row := missing.Subscriptions[0]
		if row.Coverage != "NSE only" || row.Category != "retail" || *row.Offered != "600" || *row.Bid != "500" || *row.Applications != "25" {
			t.Fatal("wrong NSE-only fallback", row)
		}
	}
	for _, value := range []string{"", "garbage", "Updated as on 31-Feb-2026 12:00:00"} {
		if sourceTimestamp(value) != nil {
			t.Fatal("invalid source timestamp accepted", value)
		}
	}
	for _, value := range []string{"28-SEP-2026 14:39:00", "As on 28-Sep-2026 14:39 IST"} {
		if sourceTimestamp(value) == nil {
			t.Fatal("published timestamp format rejected", value)
		}
	}
}

func TestPartialCombinedIPOSubscriptions(t *testing.T) {
	ptr := func(s string) *string { return &s }
	var body map[string]json.RawMessage
	_ = json.Unmarshal([]byte(combinedIPODetailFixture), &body)
	body["activeCat"] = json.RawMessage(`{"updateTime":"28-Sep-2026 14:39:00","dataList":[
 {"srNo":"1","category":"QIB","noOfShareOffered":"100","noOfSharesBid":"200","noOfTotalMeant":"2"}]}`)
	body["demandGraph"] = json.RawMessage(`{"timestamp":"28-Sep-2026 14:38:00"}`)
	input, _ := json.Marshal(body)
	item := ipoSourceIssue{Symbol: "EXAMPLE", Board: "Mainboard"}
	if err := parseIPODetail(input, &item); err != nil || len(item.Subscriptions) != 2 {
		t.Fatal("missing per-category fallback", item, err)
	}
	for _, row := range item.Subscriptions {
		if row.Category == "qib" && (row.Coverage != "NSE+BSE" || row.Applications != nil) ||
			row.Category == "retail" && (row.Coverage != "NSE only" || row.Applications == nil || row.SourceObservedAt == nil || row.SourceObservedAt.UTC().Format(time.RFC3339) != "2026-09-28T09:08:00Z") {
			t.Fatal("mixed category provenance", row)
		}
	}
	// A combined headline from the feed must not be replaced by NSE-only detail totals.
	body["bidDetails"] = json.RawMessage(`[{"category":"Total","noOfSharesOffered":"1000","noOfsharesBid":"100"}]`)
	input, _ = json.Marshal(body)
	item.Subscriptions = []ipoSubscription{{Category: "total", Coverage: "NSE+BSE", Bid: ptr("2000")}}
	if err := parseIPODetail(input, &item); err != nil || *item.Subscriptions[0].Bid != "2000" || item.Subscriptions[0].Coverage != "NSE+BSE" {
		t.Fatal("combined headline replaced by NSE-only total", item, err)
	}
}

func TestNSECatalogueScope(t *testing.T) {
	for _, host := range []string{"https://api.bseindia.com/BseIndiaAPI/api/", "https://user:password@www.nseindia.com/"} {
		if _, err := newIPOSourceClient().get(context.Background(), host); err == nil {
			t.Fatal("unsupported collector host accepted", host)
		}
	}
}

func TestNSECombinedLiveSources(t *testing.T) {
	if os.Getenv("IPO_LIVE_SOURCE_TEST") != "1" {
		t.Skip("explicit read-only NSE subscription check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := newIPOSourceClient()
	_, _ = client.get(ctx, "https://www.nseindia.com/")
	data, err := client.get(ctx, "https://www.nseindia.com/api/ipo-current-issue")
	if err != nil {
		t.Fatal(err)
	}
	items, err := parseIPOFeed(data, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Board != "Mainboard" {
			continue
		}
		data, err := client.get(ctx, "https://www.nseindia.com/api/ipo-detail?symbol="+url.QueryEscape(item.Symbol)+"&series=EQ")
		if err == nil {
			err = parseIPODetail(data, &item)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range item.Subscriptions {
			if row.Category == "retail" && row.Coverage == "NSE+BSE" {
				t.Logf("Verified published combined retail subscription for %s, source time %v", item.Symbol, row.SourceObservedAt)
				return
			}
		}
	}
	t.Skip("no current NSE mainboard IPO with published combined retail demand")
}

func TestIPOSubscriptionCoverageUpgrade(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("use disposable schema_test database")
	}
	db, err := openIPOImportDatabase(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err := db.QueryRow(`SELECT current_user`).Scan(&role); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test database")
	}
	ptr := func(s string) *string { return &s }
	item := ipoSourceIssue{Symbol: "COMBINEDFIXTURE", Name: "Combined fixture", Board: "Mainboard",
		Subscriptions: []ipoSubscription{{Category: "retail", Label: "NSE only", Offered: ptr("100"), Bid: ptr("200"), Applications: ptr("10"), Multiple: ptr("2")}}}
	if err := saveSourceIPOs(context.Background(), db, []ipoSourceIssue{item}); err != nil {
		t.Fatal(err)
	}
	published := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	item.Subscriptions = []ipoSubscription{{Category: "retail", Label: "Combined", Coverage: "NSE+BSE", Bid: ptr("500"), SourceObservedAt: &published}}
	if err := saveSourceIPOs(context.Background(), db, []ipoSourceIssue{item}); err != nil {
		t.Fatal(err)
	}
	var valid bool
	if err := db.QueryRow(`SELECT coverage='NSE+BSE' AND offered_shares IS NULL AND applications IS NULL AND multiple IS NULL
	 AND bid_shares=500 AND source_observed_at=$1 FROM ipo_pos.ipo_subscriptions s JOIN ipo_pos.ipos i ON i.id=s.ipo_id WHERE i.symbol='COMBINEDFIXTURE'`, published).Scan(&valid); err != nil || !valid {
		t.Fatal("coverage upgrade mixed NSE-only fields with consolidated bids", err)
	}
	item.Subscriptions = []ipoSubscription{{Category: "retail", Label: "Combined", Coverage: "NSE+BSE", Offered: ptr("1000"), SourceObservedAt: &published}}
	if err := saveSourceIPOs(context.Background(), db, []ipoSourceIssue{item}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT offered_shares=1000 AND bid_shares=500 AND applications IS NULL
	 FROM ipo_pos.ipo_subscriptions s JOIN ipo_pos.ipos i ON i.id=s.ipo_id WHERE i.symbol='COMBINEDFIXTURE'`).Scan(&valid); err != nil || !valid {
		t.Fatal("sparse combined observation erased known combined fields", err)
	}
	// Older, undated and NSE-only observations must leave the timestamped combined row intact.
	older := published.Add(-time.Minute)
	for _, row := range []ipoSubscription{
		{Category: "retail", Label: "Older combined", Coverage: "NSE+BSE", Bid: ptr("1"), SourceObservedAt: &older},
		{Category: "retail", Label: "Undated combined", Coverage: "NSE+BSE", Bid: ptr("3")},
		{Category: "retail", Label: "NSE only", Bid: ptr("2")},
	} {
		item.Subscriptions = []ipoSubscription{row}
		if err := saveSourceIPOs(context.Background(), db, []ipoSourceIssue{item}); err != nil {
			t.Fatal(err)
		}
	}
	var payload []byte
	if err := db.QueryRow(`SELECT ` + ipoCatalogueJSON + ` FROM ipo_pos.ipos i WHERE i.symbol='COMBINEDFIXTURE'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"bid_shares": "500"`) || !strings.Contains(string(payload), `"stale": true`) || strings.Contains(string(payload), `"coverage": "NSE only"`) {
		t.Fatal("API lost combined provenance or publication freshness", string(payload))
	}
	item.Symbol, item.Name = "LEGACYSUBFIXTURE", "Legacy subscription fixture"
	item.Subscriptions = []ipoSubscription{{Category: "retail", Label: "NSE only", Bid: ptr("99")}}
	if err := saveSourceIPOs(context.Background(), db, []ipoSourceIssue{item}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT ` + ipoCatalogueJSON + ` FROM ipo_pos.ipos i WHERE i.symbol='LEGACYSUBFIXTURE'`).Scan(&payload); err != nil || !strings.Contains(string(payload), `"coverage": "NSE only"`) || !strings.Contains(string(payload), `"bid_shares": "99"`) {
		t.Fatal("legacy mainboard fallback lost its NSE-only counts or coverage", err, string(payload))
	}
}

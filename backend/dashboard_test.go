package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLivePositionArithmetic(t *testing.T) {
	for _, test := range []struct {
		name, buy, sell, buyAmount, sellAmount, gmp, net, cost, profit, average string
	}{
		{"long", "10", "8", "5000", "4800", "200", "2.000000", "200.00", "200.00", "100.000000"},
		{"short", "8", "10", "4000", "6000", "200", "-2.000000", "-2000.00", "1600.00", "1000.000000"},
		{"flat-credit", "10", "10", "5000", "6000", "200", "0.000000", "-1000.00", "1000.00", ""},
		{"negative-gmp", "10", "8", "-5000", "-4800", "-200", "2.000000", "-200.00", "-200.00", "-100.000000"},
		{"fractional", "0.000003", "0.000001", "0.02", "0.01", "999999.999999", "0.000002", "0.01", "1.99", "5000.000000"},
		{"large", "999999999999.999999", "0", "999999999999999999999999.99", "0", "0", "999999999999.999999", "999999999999999999999999.99", "-999999999999999999999999.99", "1000000000000.000001"},
	} {
		t.Run(test.name, func(t *testing.T) {
			buy, _ := positionNumber(test.buy)
			sell, _ := positionNumber(test.sell)
			amount, _ := positionNumber(test.buyAmount)
			credit, _ := positionNumber(test.sellAmount)
			p := livePosition{GMP: &test.gmp, CalculatedAt: time.Date(2026, 11, 5, 18, 30, 0, 0, time.UTC), Categories: []positionBucket{{
				Form: "SHARES", Subject: "none", Count: 2, BuyQuantity: test.buy, SellQuantity: test.sell,
				NetQuantity: buy.Sub(buy, sell).FloatString(6), BuyAmount: test.buyAmount, SellAmount: test.sellAmount,
				NetAmount: amount.Sub(amount, credit).FloatString(2),
			}}}
			if err := calculateLivePosition(&p); err != nil {
				t.Fatal(err)
			}
			if p.Shares == nil || *p.Shares != test.net || p.PremiumCost == nil || *p.PremiumCost != test.cost || p.ProfitLoss == nil || *p.ProfitLoss != test.profit || p.Status != "complete" {
				t.Fatalf("wrong position: %+v", p)
			}
			if test.average == "" {
				if p.BreakEven != nil {
					t.Fatal("flat position has break-even", *p.BreakEven)
				}
			} else if p.BreakEven == nil || *p.BreakEven != test.average {
				t.Fatalf("wrong break-even: %v", p.BreakEven)
			}
			if p.Date != "2026-11-06" {
				t.Fatal("date is not in IST", p.Date)
			}
			if p.Categories[0].NetAmount != p.NetRecordedAmount || p.Categories[0].Shares == nil || *p.Categories[0].Shares != *p.Shares {
				t.Fatal("summary/detail mismatch")
			}
			if test.sell == "0" && p.Categories[0].SellAverage != nil {
				t.Fatal("no sell quantity must have no average")
			}
		})
	}
}

func TestLivePositionIncompleteInputs(t *testing.T) {
	for _, test := range []struct {
		form, subject, availability, reason string
		costKnown                           bool
	}{
		{"RETAIL", "subject_1", "unknown", "allotment_model_unavailable", true},
		{"RETAIL_199", "subject_2", "unknown", "allotment_model_unavailable", false},
		{"SHNI", "none", "unknown", "allotted_share_basis_missing", true},
		{"BHNI", "subject_1", "unknown", "allotment_model_unavailable", true},
		{"SHAREHOLDER", "subject_1", "not_offered", "shareholder_source_inconsistency", true},
		{"SHAREHOLDER", "subject_1", "unknown", "shareholder_availability_unknown", true},
	} {
		t.Run(test.form+"/"+test.subject+"/"+test.availability, func(t *testing.T) {
			p := livePosition{GMP: new("200"), ShareholderAvailability: test.availability, Categories: []positionBucket{
				{Form: "SHARES", Subject: "none", Count: 1, BuyQuantity: "5", SellQuantity: "0", NetQuantity: "5", BuyAmount: "100", SellAmount: "0", NetAmount: "100"},
				{Form: test.form, Subject: test.subject, Count: 2, BuyQuantity: "10", SellQuantity: "8", NetQuantity: "2", BuyAmount: "5000", SellAmount: "4800", NetAmount: "200"},
			}}
			if err := calculateLivePosition(&p); err != nil {
				t.Fatal(err)
			}
			if p.Shares != nil || p.ProfitLoss != nil || p.ProfitLossBasis != nil || p.Status != "partial" || p.KnownShares != "5.000000" || p.NetRecordedAmount != "300.00" {
				t.Fatalf("incomplete result invented or omitted: %+v", p)
			}
			if (p.PremiumCost != nil) != test.costKnown {
				t.Fatal("wrong cost completeness")
			}
			wantCost := "100.00"
			if test.costKnown {
				wantCost = "300.00"
			}
			if p.KnownPremiumCost != wantCost {
				t.Fatal("partial cost masquerades as total", p.KnownPremiumCost)
			}
			found := false
			for _, reason := range p.Categories[1].Reasons {
				if reason.Code == test.reason {
					found = true
				}
			}
			if !found {
				t.Fatal("missing reason", p.Categories[1].Reasons)
			}
			if p.Categories[1].BuyAverage == nil || *p.Categories[1].BuyAverage != "500.000000" || p.Categories[1].SellAverage == nil || *p.Categories[1].SellAverage != "600.000000" {
				t.Fatal("separate weighted rates lost")
			}
		})
	}
	p := livePosition{Categories: []positionBucket{{Form: "SHARES", Subject: "none", Count: 1, BuyQuantity: "2", SellQuantity: "0", NetQuantity: "2", BuyAmount: "1", SellAmount: "0", NetAmount: "1"}}}
	if err := calculateLivePosition(&p); err != nil {
		t.Fatal(err)
	}
	if p.ProfitLoss != nil || p.Shares == nil || p.PremiumCost == nil || p.BreakEven == nil || *p.BreakEven != "0.500000" {
		t.Fatal("missing GMP hid known exposure/cost or became zero")
	}
	for _, s := range []string{"NaN", "Infinity", "1/2", "1e3", ""} {
		if _, err := positionNumber(s); err == nil {
			t.Fatal("invalid calculation input accepted", s)
		}
	}
}

func testLivePositionAPI(t *testing.T, owner *sql.DB, admin, cashier, other testClient, base, otherBase string, request map[string]any) {
	t.Helper()
	status, body, _ := admin.request("POST", base+"/ipos", map[string]string{"code": "ANALYTICS-BOOK", "name": "Analytics book"}, "")
	expectStatus(t, 201, status, body)
	ipo := field(t, body, "id")
	path := base + "/dashboard/ipos/" + ipo
	status, body, _ = cashier.request("GET", path+"?include=categories", nil, "")
	expectStatus(t, 200, status, body)
	if body["empty"] != true || len(body["categories"].([]any)) != 0 {
		t.Fatal("pre-trade IPO must have an empty book", body)
	}
	status, body, _ = admin.request("PUT", base+"/ipos/"+ipo+"/analytics-assumptions", map[string]any{"expected_version": 0, "gmp": "200", "subject2_pricing_factor": "0.8", "expected_applications": map[string]any{"RETAIL": "4"}, "change_reason": "Forecast before terms"}, "")
	expectStatus(t, 200, status, body)
	status, body, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body["expected_applications"].(map[string]any), "RETAIL") != "4" || body["empty"] != true {
		t.Fatal("pre-IPO demand forecast was hidden", body)
	}
	trade := cloneMap(request)
	trade["ipo_id"] = ipo
	trade["form_type_code"] = "SHARES"
	trade["category"] = "none"
	trade["ce_pe_value"] = nil
	trade["quantity"] = "10"
	trade["rate"] = "500"
	status, body, _ = cashier.request("POST", base+"/transactions", trade, "analytics-buy")
	expectStatus(t, 201, status, body)
	buyID := field(t, body, "id")
	trade["quantity"] = "-8"
	trade["rate"] = "600"
	status, body, _ = cashier.request("POST", base+"/transactions", trade, "analytics-sell")
	expectStatus(t, 201, status, body)
	sellID := field(t, body, "id")
	status, body, headers := cashier.request("GET", path+"?include=categories", nil, "")
	expectStatus(t, 200, status, body)
	if headers.Get("Cache-Control") != "no-store" || field(t, body, "net_shares") != "2.000000" || field(t, body, "net_premium_cost") != "200.00" || field(t, body, "estimated_profit_loss") != "200.00" || body["transaction_count"] != float64(2) {
		t.Fatal("live totals", body)
	}
	if strings.Contains(fmt.Sprint(body), "customer_id") || strings.Contains(fmt.Sprint(body), "phone_number") {
		t.Fatal("private transaction details in summary")
	}
	category := body["categories"].([]any)[0].(map[string]any)
	if field(t, category, "buy_average_rate") != "500.000000" || field(t, category, "sell_average_rate") != "600.000000" {
		t.Fatal("weighted rates", category)
	}
	status, summary, _ := admin.request("GET", path, nil, "")
	expectStatus(t, 200, status, summary)
	if _, present := summary["categories"]; present || summary["net_shares"] != body["net_shares"] {
		t.Fatal("summary and detail diverged", summary)
	}
	unauthenticated := cashier
	unauthenticated.Token = ""
	status, body, _ = unauthenticated.request("GET", path, nil, "")
	expectStatus(t, 401, status, body)
	status, body, _ = other.request("GET", path, nil, "")
	expectStatus(t, 403, status, body)
	status, body, _ = other.request("GET", otherBase+"/dashboard/ipos/"+ipo, nil, "")
	expectStatus(t, 200, status, body)
	if body["empty"] != true || body["gmp"] != nil || body["transaction_count"] != float64(0) {
		t.Fatal("another business's trades or assumptions leaked", body)
	}
	for _, query := range []string{"?include=pricing", "?include=categories&include=categories", "?date=not-a-date", "?date=9999-01-01", "?phase=all", "?limit=1"} {
		status, body, _ = cashier.request("GET", path+query, nil, "")
		expectStatus(t, 400, status, body)
	}
	status, body, _ = cashier.request("GET", path+"?date=2000-01-01", nil, "")
	expectStatus(t, 404, status, body)
	if field(t, body["error"].(map[string]any), "code") != "close_unavailable" {
		t.Fatal("history was reconstructed from current inputs")
	}
	status, body, _ = cashier.request("GET", base+"/dashboard/ipos/invalid", nil, "")
	expectStatus(t, 400, status, body)
	trade["quantity"] = "-7"
	trade["rate"] = "600"
	trade["expected_version"] = 1
	trade["change_reason"] = "Correct quantity"
	status, body, _ = cashier.request("PUT", base+"/transactions/"+sellID, trade, "")
	expectStatus(t, 200, status, body)
	status, body, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body, "net_shares") != "3.000000" || field(t, body, "net_premium_cost") != "800.00" || field(t, body, "estimated_profit_loss") != "-200.00" {
		t.Fatal("versions counted twice or edit ignored", body)
	}
	status, body, _ = cashier.request("POST", base+"/transactions/"+sellID+"/cancel", map[string]any{"expected_version": 2, "change_reason": "Cancel fixture sale"}, "")
	expectStatus(t, 200, status, body)
	status, body, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body, "net_shares") != "10.000000" || body["transaction_count"] != float64(1) {
		t.Fatal("cancelled row contributed", body)
	}
	if _, err := owner.Exec(`INSERT INTO ipo_pos.transactions
 (business_id,idempotency_key,origin,created_by_user_id,ipo_id,customer_id,occurred_at,
 form_type_code,category,quantity,rate,changed_by_user_id,change_reason)
 SELECT business_id,'analytics-page-'||n,'manual',created_by_user_id,ipo_id,customer_id,occurred_at,
 'SHARES','subject_1',1,0,changed_by_user_id,'Pagination fixture'
 FROM ipo_pos.transactions CROSS JOIN generate_series(1,101) n WHERE id=$1`, buyID); err != nil {
		t.Fatal(err)
	}
	status, body, _ = cashier.request("GET", path+"?include=categories", nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body, "net_shares") != "111.000000" || body["transaction_count"] != float64(102) || field(t, body, "net_premium_cost") != "5000.00" {
		t.Fatal("report limited to a ledger page or shares probability-scaled", body)
	}
	application := cloneMap(request)
	application["ipo_id"] = ipo
	application["form_type_code"] = "RETAIL"
	application["category"] = "subject_2"
	application["ce_pe_value"] = nil
	application["quantity"] = "2"
	application["rate"] = "100"
	status, body, _ = cashier.request("POST", base+"/transactions", application, "analytics-unresolved")
	expectStatus(t, 201, status, body)
	status, body, _ = cashier.request("GET", path+"?include=categories", nil, "")
	expectStatus(t, 200, status, body)
	if body["net_shares"] != nil || body["net_premium_cost"] != nil || body["estimated_profit_loss"] != nil || field(t, body, "calculation_status") != "partial" || field(t, body, "known_net_shares") != "111.000000" || field(t, body, "known_net_premium_cost") != "5000.00" || field(t, body, "net_recorded_amount") != "5200.00" {
		t.Fatal("Subject 2 face amount treated as expected cost", body)
	}
	inputs := map[string]any{"expected_version": 1, "gmp": "200", "subject2_pricing_factor": "0.8", "expected_applications": map[string]any{"RETAIL": "4"},
		"calculation_basis": map[string]any{"RETAIL": map[string]string{"shares_per_application": "12", "capacity": "2", "unit": "applications"}},
		"additional_cost":   "1.25", "change_reason": "Configure forecast fixture"}
	status, body, _ = cashier.request("PUT", base+"/ipos/"+ipo+"/analytics-assumptions", inputs, "")
	expectStatus(t, 403, status, body)
	status, body, _ = admin.request("PUT", base+"/ipos/"+ipo+"/analytics-assumptions", inputs, "")
	expectStatus(t, 200, status, body)
	if body["version"] != float64(2) || field(t, body, "additional_cost") != "1.25" {
		t.Fatal("new version lost calculation inputs", body)
	}
	status, body, _ = cashier.request("GET", path+"?include=categories", nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body, "net_shares") != "123.000000" || field(t, body, "net_premium_cost") != "5101.25" {
		t.Fatal("configured forecast did not apply", body)
	}
	revision := field(t, body, "revision")
	status, body, _ = cashier.request("GET", path+"?revision=0", nil, "")
	expectStatus(t, 409, status, body)
	status, body, _ = cashier.request("GET", path+"?revision="+revision, nil, "")
	expectStatus(t, 200, status, body)
	// An older client can update GMP without erasing the new calculation inputs.
	delete(inputs, "calculation_basis")
	delete(inputs, "additional_cost")
	inputs["expected_version"] = 2
	status, body, _ = admin.request("PUT", base+"/ipos/"+ipo+"/analytics-assumptions", inputs, "")
	expectStatus(t, 200, status, body)
	if field(t, body, "additional_cost") != "1.25" || len(body["calculation_basis"].(map[string]any)) != 1 {
		t.Fatal("legacy write erased basis/cost", body)
	}
	for _, invalid := range []string{`{"OTHER":{"shares_per_application":"12","capacity":"2","unit":"applications"}}`,
		`{"RETAIL":{"shares_per_application":"0","capacity":"2","unit":"applications"}}`,
		`{"RETAIL":{"shares_per_application":"12","capacity":"0","unit":"applications"}}`,
		`{"RETAIL":{"shares_per_application":"12","capacity":"2","unit":"bid_amount_crore"}}`} {
		if _, err := owner.Exec(`INSERT INTO ipo_pos.ipo_dashboard_assumption_versions
 (business_id,ipo_id,version,changed_by_user_id,change_reason,calculation_basis)
 SELECT business_id,ipo_id,4,changed_by_user_id,'Invalid fixture',$2::jsonb FROM ipo_pos.ipo_dashboard_assumption_versions WHERE ipo_id=$1 AND version=3`, ipo, invalid); err == nil {
			t.Fatal("database accepted invalid calculation basis", invalid)
		}
	}
	status, body, _ = cashier.request("GET", base+"/dashboard/ipos?limit=1", nil, "")
	expectStatus(t, 200, status, body)
	books := body["items"].([]any)
	if len(books) != 1 || body["next_after_id"] == nil {
		t.Fatal("book pagination", body)
	}
	first := field(t, books[0].(map[string]any), "ipo_id")
	status, body, _ = cashier.request("GET", base+"/dashboard/ipos?limit=1&after_id="+field(t, body, "next_after_id"), nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body["items"].([]any)[0].(map[string]any), "ipo_id") == first {
		t.Fatal("repeated book cursor")
	}
	if _, err := owner.Exec(`UPDATE ipo_pos.ipos SET archived_at=now(),listing_on='2000-01-01' WHERE id=$1`, ipo); err != nil {
		t.Fatal(err)
	}
	status, body, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body, "known_net_shares") != "123.000000" {
		t.Fatal("retained book disappeared after catalogue expiry")
	}
	for _, query := range []string{
		`UPDATE ipo_pos.ipos SET board='Mainboard',lot_size=12,price_high=1000 WHERE id=$1`,
		`INSERT INTO ipo_pos.ipo_reservations(ipo_id,category,reserved_shares,basis,source_url,observed_at)
 VALUES($1,'Retail Shares Offered',36,'shares','https://example.test',now())`,
		`SELECT ipo_pos.capture_ipo_dashboard_basis($1)`,
	} {
		if _, err := owner.Exec(query, ipo); err != nil {
			t.Fatal(err)
		}
	}
	status, body, _ = cashier.request("GET", base+"/ipos/"+ipo+"/analytics-assumptions", nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body["estimated_application_capacity"].(map[string]any), "RETAIL") != "3" ||
		field(t, body["expected_applications"].(map[string]any), "RETAIL") != "4" {
		t.Fatal("official capacity did not join the existing demand forecast", body)
	}
	status, body, _ = cashier.request("GET", path+"?include=categories", nil, "")
	expectStatus(t, 200, status, body)
	if field(t, body, "net_expected_shares") != "18.000000" || field(t, body, "net_direct_shares") != "111.000000" ||
		field(t, body, "net_pending_shares") != "129.000000" || field(t, body, "net_premium_cost") != "5151.25" {
		t.Fatal("report did not use the same official capacity as the editor", body)
	}
	if _, err := owner.Exec(`UPDATE ipo_pos.transactions SET status='cancelled',version=version+1,change_reason='Empty fixture book'
 WHERE ipo_id=$1 AND status='confirmed'`, ipo); err != nil {
		t.Fatal(err)
	}
	status, body, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, body)
	if body["empty"] != true || body["transaction_count"] != float64(0) || field(t, body, "net_shares") != "0.000000" || field(t, body, "net_premium_cost") != "1.25" {
		t.Fatal("empty/cancelled book is not explicit", body)
	}
	encoded, err := json.Marshal(body)
	if err != nil || !strings.Contains(string(encoded), `"calculation_version":3`) {
		t.Fatal("calculation provenance missing")
	}
}

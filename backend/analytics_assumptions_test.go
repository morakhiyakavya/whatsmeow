package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestAnalyticsAssumptionValidation(t *testing.T) {
	for _, test := range []struct {
		name, gmp, factor, count string
		valid                    bool
	}{
		{"normal", "240", "0.8", "800000", true},
		{"signed-normalized", "-000000000001.250000", "0.800000", "000800000", true},
		{"zero-factor", "0", "0", "1", true},
		{"one-factor", "-0", "1", strings.Repeat("9", 24), true},
		{"large-count", "240", "0.8", strings.Repeat("9", 25), false},
		{"zero-count", "240", "0.8", "000", false},
		{"fractional-count", "240", "0.8", "1.5", false},
		{"exponent-count", "240", "0.8", "8e5", false},
		{"negative-count", "240", "0.8", "-1", false},
		{"factor-low", "240", "-0.1", "1", false},
		{"factor-high", "240", "1.000001", "1", false},
		{"factor-precision", "240", "0.8000001", "1", false},
		{"nan-gmp", "NaN", "0.8", "1", false},
		{"gmp-precision", "0.0000001", "0.8", "1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := assumptionInput{ExpectedVersion: new(0), GMP: &test.gmp, Factor: &test.factor,
				Applications: &applicationForecasts{Retail: &test.count}, Reason: " forecast "}
			if err := input.normalize(); (err == nil) != test.valid {
				t.Fatalf("normalize: %v", err)
			}
			if test.valid && (input.Reason != "forecast" || strings.HasSuffix(*input.Factor, "000")) {
				t.Fatal("input not normalized")
			}
		})
	}
	input := assumptionInput{ExpectedVersion: new(0), Factor: new("0.8"), Applications: &applicationForecasts{}, Reason: "unknown forecast"}
	if err := input.normalize(); err != nil {
		t.Fatal("unknown inputs must stay null", err)
	}
	for _, change := range []func(*assumptionInput){
		func(v *assumptionInput) { v.ExpectedVersion = nil },
		func(v *assumptionInput) { v.ExpectedVersion = new(-1) },
		func(v *assumptionInput) { v.ExpectedVersion = new(2147483647) },
		func(v *assumptionInput) { v.Factor = nil },
		func(v *assumptionInput) { v.Applications = nil },
		func(v *assumptionInput) { v.Reason = " " },
		func(v *assumptionInput) { v.Reason = strings.Repeat("x", 2049) },
	} {
		bad := input
		change(&bad)
		if bad.normalize() == nil {
			t.Fatal("invalid revision accepted")
		}
	}
}

func testAnalyticsAssumptions(t *testing.T, owner, runtime *sql.DB, admin, cashier testClient, base, otherBusiness, ipoID, cashierID string) {
	t.Helper()
	path := base + "/ipos/" + ipoID + "/analytics-assumptions"
	status, body, headers := cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, body)
	if body["version"] != float64(0) || body["gmp"] != nil || headers.Get("Cache-Control") != "no-store" {
		t.Fatal("missing assumptions were not read-only unknown defaults", body)
	}
	basis := body["source_basis"].(map[string]any)
	if basis["shareholder_availability"] != "unknown" {
		t.Fatal("missing quota was inferred absent", basis)
	}
	var count int
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.ipo_dashboard_assumption_versions WHERE ipo_id=$1`, ipoID).Scan(&count); err != nil || count != 0 {
		t.Fatal("GET wrote a settings row", count, err)
	}
	input := map[string]any{"expected_version": 0, "gmp": "-25.5", "subject2_pricing_factor": "0.8",
		"expected_applications": map[string]any{"RETAIL": "800000", "SHNI": nil, "BHNI": nil, "SHAREHOLDER": nil},
		"change_reason":         "Initial forecast"}
	status, body, _ = cashier.request("PUT", path, input, "")
	expectStatus(t, 403, status, body)
	status, body, _ = admin.request("GET", "/api/businesses/"+otherBusiness+"/ipos/"+ipoID+"/analytics-assumptions", nil, "")
	expectStatus(t, 403, status, body)
	for key, value := range map[string]any{"changed_by_user_id": cashierID, "recorded_at": "2000-01-01T00:00:00Z", "business_id": otherBusiness} {
		forged := cloneMap(input)
		forged[key] = value
		status, body, _ = admin.request("PUT", path, forged, "")
		expectStatus(t, 400, status, body)
	}
	status, body, _ = admin.request("PUT", path, input, "")
	expectStatus(t, 200, status, body)
	if body["version"] != float64(1) || field(t, body, "gmp") != "-25.500000" {
		t.Fatal("signed forecast did not persist", body)
	}
	adminID := field(t, body, "changed_by_user_id")
	status, body, _ = admin.request("PUT", path, input, "")
	expectStatus(t, 409, status, body)
	if body["error"].(map[string]any)["code"] != "version_conflict" {
		t.Fatal(body)
	}
	input["expected_version"] = 1
	input["gmp"] = "240"
	input["subject2_pricing_factor"] = "0.6"
	encoded, _ := json.Marshal(input)
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("PUT", admin.URL+path, bytes.NewReader(encoded))
			req.Header.Set("Authorization", "Bearer "+admin.Token)
			response, err := admin.HTTP.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			response.Body.Close()
			results <- response.StatusCode
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for code := range results {
		if code == 200 {
			winners++
		} else if code != 409 {
			t.Fatal("unexpected concurrent result", code)
		}
	}
	if winners != 1 {
		t.Fatal("expected one winning version", winners)
	}
	status, body, _ = cashier.request("GET", path+"/history?limit=1", nil, "")
	expectStatus(t, 200, status, body)
	if len(body["items"].([]any)) != 1 || body["next_before_version"] != float64(2) {
		t.Fatal("history pagination", body)
	}
	status, body, _ = cashier.request("GET", path+"/history?before_version=2", nil, "")
	expectStatus(t, 200, status, body)
	old := body["items"].([]any)[0].(map[string]any)
	if field(t, old, "gmp") != "-25.500000" || old["version"] != float64(1) {
		t.Fatal("prior assumptions changed", old)
	}
	status, body, _ = cashier.request("GET", path+"/history?before_version=0", nil, "")
	expectStatus(t, 400, status, body)

	for _, statement := range []string{
		`UPDATE ipo_pos.ipo_dashboard_assumption_versions SET gmp=999`,
		`DELETE FROM ipo_pos.ipo_dashboard_assumption_versions`,
		`TRUNCATE ipo_pos.ipo_dashboard_assumption_versions`,
		`SELECT ipo_pos.capture_ipo_dashboard_basis('` + ipoID + `')`,
		`INSERT INTO ipo_pos.ipo_dashboard_basis_versions(ipo_id,version,shareholder_availability,snapshot) VALUES('` + ipoID + `',99,'not_offered','{}')`,
	} {
		if _, err := runtime.Exec(statement); err == nil {
			t.Fatal("runtime permission escaped", statement)
		}
	}
	for _, statement := range []string{
		`UPDATE ipo_pos.ipo_dashboard_assumption_versions SET gmp=999`,
		`DELETE FROM ipo_pos.ipo_dashboard_basis_versions`,
		`TRUNCATE ipo_pos.ipo_dashboard_basis_versions`,
	} {
		if _, err := owner.Exec(statement); err == nil {
			t.Fatal("append-only guard escaped", statement)
		}
	}
	var businessID string
	if err := owner.QueryRow(`SELECT business_id FROM ipo_pos.business_memberships WHERE user_id=$1`, adminID).Scan(&businessID); err != nil {
		t.Fatal(err)
	}
	for _, actorID := range []string{cashierID, "00000000-0000-0000-0000-000000000000"} {
		if _, err := runtime.Exec(`INSERT INTO ipo_pos.ipo_dashboard_assumption_versions(business_id,ipo_id,version,changed_by_user_id,change_reason)
 VALUES($1,$2,3,$3,'forged actor')`, businessID, ipoID, actorID); err == nil {
			t.Fatal("database actor guard escaped")
		}
	}
	if _, err := owner.Exec(`INSERT INTO ipo_pos.ipo_reservations(ipo_id,category,reserved_shares,basis,source_url,observed_at)
 VALUES($1,'Shareholder',0,'Explicit fixture quota','https://example.test/quota',clock_timestamp());`, ipoID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`SELECT ipo_pos.capture_ipo_dashboard_basis($1)`, ipoID); err != nil {
		t.Fatal(err)
	}
	input["expected_version"] = 2
	input["expected_applications"].(map[string]any)["SHAREHOLDER"] = "10000"
	status, body, _ = admin.request("PUT", path, input, "")
	expectStatus(t, 400, status, body)
	if _, err := owner.Exec(`UPDATE ipo_pos.ipo_reservations SET reserved_shares=100 WHERE ipo_id=$1 AND category='Shareholder'`, ipoID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`SELECT ipo_pos.capture_ipo_dashboard_basis($1)`, ipoID); err != nil {
		t.Fatal(err)
	}
	status, body, _ = admin.request("PUT", path, input, "")
	expectStatus(t, 200, status, body)
	status, body, _ = cashier.request("GET", path, nil, "")
	expectStatus(t, 200, status, body)
	if body["source_basis"].(map[string]any)["shareholder_availability"] != "available" {
		t.Fatal("published shareholder quota missing", body)
	}
	if _, err := owner.Exec(`SELECT ipo_pos.capture_ipo_dashboard_basis($1)`, ipoID); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(`SELECT count(*) FROM ipo_pos.ipo_dashboard_basis_versions WHERE ipo_id=$1`, ipoID).Scan(&count); err != nil || count != 3 {
		t.Fatal("basis deduplication/history", count, err)
	}
	for _, values := range [][3]any{
		{"NaN", "0.8", "1"}, {nil, "NaN", "1"}, {nil, "-0.1", "1"}, {nil, "1.1", "1"},
		{nil, "0.8", "0"}, {nil, "0.8", "1.5"}, {nil, "0.8", "1000000000000000000000000"},
	} {
		if _, err := runtime.Exec(`INSERT INTO ipo_pos.ipo_dashboard_assumption_versions
 (business_id,ipo_id,version,changed_by_user_id,change_reason,gmp,subject2_pricing_factor,retail_expected_applications)
 VALUES($1,$2,4,$3,'invalid boundary',$4,$5,$6)`, businessID, ipoID, adminID, values[0], values[1], values[2]); err == nil {
			t.Fatal("database accepted invalid numeric inputs", values)
		}
	}
	tx, err := runtime.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var serverTime bool
	err = tx.QueryRow(`INSERT INTO ipo_pos.ipo_dashboard_assumption_versions AS v
 (business_id,ipo_id,version,changed_by_user_id,change_reason,recorded_at)
 VALUES($1,$2,4,$3,'server timestamp','2000-01-01')
 RETURNING v.recorded_at>clock_timestamp()-interval '1 minute'`, businessID, ipoID, adminID).Scan(&serverTime)
	tx.Rollback()
	if err != nil || !serverTime {
		t.Fatal("database trusted client timestamp", err)
	}
	input["expected_version"] = 3
	input["gmp"] = nil
	input["subject2_pricing_factor"] = "0"
	input["expected_applications"].(map[string]any)["RETAIL"] = strings.Repeat("9", 24)
	status, body, _ = admin.request("PUT", path, input, "")
	expectStatus(t, 200, status, body)
	if body["gmp"] != nil || body["expected_applications"].(map[string]any)["RETAIL"] != strings.Repeat("9", 24) {
		t.Fatal("nullable GMP or large exact application count changed", body)
	}
}

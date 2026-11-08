package main

import (
	"fmt"
	"testing"
)

func TestCatalogueForecastBasisUsesSeparatePublishedQuotas(t *testing.T) {
	facts := []byte(`{"ipo":{"board":"Mainboard","lot_size":12,"price_high":"1119","opens_on":"2026-01-02"},
 "reservations":[{"category":"Retail Shares Offered","reserved_shares":"1205"},{"category":"NII","reserved_shares":"10800"}],
 "subscriptions":[{"category":"shni","offered_shares":"1800","coverage":"NSE+BSE"},{"category":"bhni","offered_shares":"3600","coverage":"NSE+BSE"}]}`)
	basis := catalogueForecastBasis(facts)
	if basis["RETAIL"] != (calculationBasis{"12", "100", "applications"}) ||
		basis["SHNI"] != (calculationBasis{"180", "10", "applications"}) ||
		basis["BHNI"] != (calculationBasis{"180", "20", "applications"}) || len(basis) != 3 {
		t.Fatal("wrong catalogue forecast basis", basis)
	}
	for _, snapshot := range []string{`{}`, `{"ipo":{"board":"Mainboard","lot_size":12},"reservations":[
 {"category":"Retail","reserved_shares":"120"},{"category":"Retail Shares Offered","reserved_shares":"240"},
 {"category":"NII","reserved_shares":"1200"}]}`} {
		if len(catalogueForecastBasis([]byte(snapshot))) != 0 {
			t.Fatal("incomplete or conflicting terms must remain unavailable")
		}
	}
	legacy := []byte(`{"ipo":{"board":"Mainboard","lot_size":12,"price_high":"1119","opens_on":"2026-01-02"},
 "subscriptions":[{"category":"retail","offered_shares":"1200","coverage":"NSE only"}]}`)
	if len(catalogueForecastBasis(legacy)) != 0 {
		t.Fatal("NSE-only mainboard quota used as consolidated capacity")
	}
}

func TestCatalogueForecastBasisUsesTwoLotsForSMEIndividual(t *testing.T) {
	for _, test := range []struct {
		name, opens, price, rows string
		want                     bool
	}{
		{"Vedant reservation", "2026-11-02", "94.000000", `"reservations":[
 {"category":"Retail","reserved_shares":"3396000"},{"category":"sNII < ₹10L","reserved_shares":"753600"},
 {"category":"bNII > ₹10L","reserved_shares":"1507200"}]`, true},
		{"individual reservation", "2025-07-01", "94", `"reservations":[
 {"category":"Individual Investors","reserved_shares":"3396000"}]`, true},
		{"exchange quota", "2026-11-02", "94", `"subscriptions":[
 {"category":"individual","offered_shares":"3396000","coverage":"NSE only"}]`, true},
		{"conflicting labels", "2026-11-02", "94", `"reservations":[
 {"category":"Retail","reserved_shares":"3396000"},{"category":"Individual","reserved_shares":"2400"}]`, false},
		{"legacy rules", "2025-06-30", "94", `"reservations":[{"category":"Retail","reserved_shares":"3396000"}]`, false},
		{"missing opening date", "", "94", `"reservations":[{"category":"Retail","reserved_shares":"3396000"}]`, false},
		{"invalid price basis", "2026-11-02", "80", `"reservations":[{"category":"Retail","reserved_shares":"3396000"}]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := fmt.Sprintf(`{"ipo":{"board":"SME","lot_size":1200,"price_high":%q,"opens_on":%q},%s}`, test.price, test.opens, test.rows)
			basis := catalogueForecastBasis([]byte(facts))
			if test.want && (len(basis) != 1 || basis["RETAIL"] != (calculationBasis{"2400", "1415", "applications"})) ||
				!test.want && len(basis) != 0 {
				t.Fatal("wrong SME forecast basis", basis)
			}
		})
	}
}

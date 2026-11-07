package main

import "testing"

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

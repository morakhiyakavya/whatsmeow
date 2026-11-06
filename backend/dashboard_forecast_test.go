package main

import (
	"math/big"
	"testing"
)

func samplePosition(t *testing.T) livePosition {
	t.Helper()
	p := livePosition{GMP: new("200"), Factor: "0.8", AdditionalCost: "25", ShareholderAvailability: "available",
		Applications: applicationForecasts{Retail: new("800000"), SHNI: new("1000"), BHNI: new("800"), Shareholder: new("500")},
		Basis: map[string]calculationBasis{
			"RETAIL": {"14", "200000", "applications"}, "RETAIL_199": {"14", "200000", "applications"},
			"SHNI": {"180", "100", "applications"}, "BHNI": {"180", "200", "applications"},
			"SHAREHOLDER": {"100", "100", "bid_amount_crore"},
		}}
	for _, row := range [][6]string{
		{"RETAIL", "subject_1", "10", "500", "8", "600"},
		{"RETAIL_199", "subject_1", "2", "700", "1", "800"},
		{"SHAREHOLDER", "subject_1", "3", "1000", "1", "1100"},
		{"SHNI", "subject_1", "4", "1000", "1", "1200"},
		{"SHNI", "subject_2", "2", "2000", "1", "2400"},
		{"BHNI", "subject_1", "5", "2000", "2", "2200"},
		{"BHNI", "subject_2", "3", "3000", "1", "3200"},
		{"SHARES", "none", "100", "20", "50", "30"},
		{"PE", "none", "10", "2", "4", "3"},
		{"CE", "none", "0", "0", "2", "1"},
	} {
		buy, _ := positionNumber(row[2])
		buyRate, _ := positionNumber(row[3])
		sell, _ := positionNumber(row[4])
		sellRate, _ := positionNumber(row[5])
		buyAmount := new(big.Rat).Mul(buy, buyRate)
		sellAmount := new(big.Rat).Mul(sell, sellRate)
		amount := new(big.Rat).Sub(buyAmount, sellAmount).FloatString(2)
		p.Categories = append(p.Categories, positionBucket{Form: row[0], Subject: row[1], Count: 2,
			BuyQuantity: row[2], SellQuantity: row[4], NetQuantity: new(big.Rat).Sub(buy, sell).FloatString(6),
			BuyAmount: buyAmount.FloatString(2), SellAmount: sellAmount.FloatString(2), NetAmount: amount, AgreedAmount: amount})
	}
	return p
}

func TestForecastAndPremiumOnlyCEPE(t *testing.T) {
	p := samplePosition(t)
	if err := calculateLivePosition(&p); err != nil {
		t.Fatal(err)
	}
	if p.Status != "complete" || *p.ExpectedShares != "347.500000" || *p.ConfirmedShares != "50.000000" ||
		*p.Shares != "397.500000" || *p.PremiumCost != "13241.00" || *p.BreakEven != "33.310692" {
		t.Fatalf("Signed forecast failed: %+v", p)
	}
	// Workbook totals round each pool to whole shares; the API keeps six-place bucket measures.
	if *positionString(mustPositionNumber(t, *p.Shares), 0) != "398" {
		t.Fatal("worksheet share display parity")
	}
	for _, b := range p.Categories {
		if b.Form == "PE" || b.Form == "CE" {
			if *b.Shares != "0.000000" || b.PremiumCost == nil || b.Subject1Rate != nil || b.Subject2Rate != nil {
				t.Fatal("CE/PE label affected shares or dropped premium", b)
			}
		}
		if b.Form == "SHAREHOLDER" && *b.ExpectedShares != "40.000000" {
			t.Fatal("shareholder was probability-discounted twice")
		}
	}
	for _, factor := range []string{"0.6", "0.8", "1"} {
		changed := samplePosition(t)
		changed.Factor = factor
		if err := calculateLivePosition(&changed); err != nil {
			t.Fatal(err)
		}
		if *changed.Shares != *p.Shares || *changed.PremiumCost != *p.PremiumCost {
			t.Fatal("pricing factor changed saved costs or shares")
		}
		for _, b := range changed.Categories {
			if b.Form == "SHNI" && *b.Subject2Rate != map[string]string{"0.6": "21600", "0.8": "28800", "1": "36000"}[factor] {
				t.Fatal("factor quote", b)
			}
		}
	}
}

func mustPositionNumber(t *testing.T, value string) *big.Rat {
	t.Helper()
	n, err := positionNumber(value)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSubject2UsesUnroundedAgreementAndRejectsUnsupportedRatio(t *testing.T) {
	p := livePosition{GMP: new("245"), Factor: "0.8", Applications: applicationForecasts{Retail: new("2")},
		Basis: map[string]calculationBasis{"RETAIL": {"12", "1", "applications"}}, Categories: []positionBucket{
			{Form: "RETAIL", Subject: "subject_2", Count: 1, BuyQuantity: "0.333333", SellQuantity: "0", NetQuantity: "0.333333", BuyAmount: "0.01", SellAmount: "0", NetAmount: "0.01", AgreedAmount: "0.00999999"}}}
	if err := calculateLivePosition(&p); err != nil {
		t.Fatal(err)
	}
	if *p.PremiumCost != "0.00" || *p.Shares != "1.999998" {
		t.Fatal("rounded face amount was used as expected cost", p)
	}
	p.Basis["RETAIL"] = calculationBasis{"12", "3", "applications"}
	if err := calculateLivePosition(&p); err != nil {
		t.Fatal(err)
	}
	if p.Shares != nil || p.PremiumCost != nil || p.Status != "partial" {
		t.Fatal("probability was capped or stale result retained")
	}
}

func TestCalculationBasisValidation(t *testing.T) {
	for _, basis := range []map[string]calculationBasis{
		{"OTHER": {"12", "1", "applications"}}, {"RETAIL": {"0", "1", "applications"}},
		{"RETAIL": {"12", "0", "applications"}}, {"RETAIL": {"12", "1.5", "applications"}},
		{"RETAIL": {"12", "1", "bid_amount_crore"}}, {"SHAREHOLDER": {"NaN", "1", "bid_amount_crore"}},
	} {
		if mapCalculationBasis(basis).normalize() == nil {
			t.Fatal("invalid calculation basis accepted", basis)
		}
	}
	basis := map[string]calculationBasis{"SHAREHOLDER": {"168.000000", "002500", "bid_amount_crore"}}
	if err := mapCalculationBasis(basis).normalize(); err != nil || basis["SHAREHOLDER"].Capacity != "2500" || basis["SHAREHOLDER"].Shares != "168" {
		t.Fatal("basis normalization", basis, err)
	}
}

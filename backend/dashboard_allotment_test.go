package main

import (
	"math/big"
	"testing"
)

func TestEstimatePool(t *testing.T) {
	pool := allotmentPool{ReservedShares: "10000", MinimumShares: 50, LotSize: 50, MinBidLots: 1, MaxBidLots: new(int64(16))}
	for _, tc := range []struct {
		name, forms, total, bidLots, method, shares, chance string
	}{
		{"lottery", "400", "", "5", "lottery", "25.000000", "0.500000"},
		{"full bid below quota", "100", "9000", "5", "full_bid", "250.000000", "1.000000"},
		{"full bid exactly at quota", "100", "10000", "5", "full_bid", "250.000000", "1.000000"},
		{"minimum and proportionate balance", "100", "15000", "5", "proportionate", "150.000000", "1.000000"},
		{"minimums use all shares", "200", "", "5", "minimum", "50.000000", "1.000000"},
		{"minimum bid", "100", "", "", "minimum", "50.000000", "1.000000"},
		{"minimum only with demand missing", "100", "", "5", "minimum_only", "50.000000", "1.000000"},
		{"market demand smaller than minimum bids", "100", "4999", "", "pool_demand_invalid", "", ""},
		{"market cannot include this bid", "100", "5000", "5", "pool_demand_invalid", "", ""},
		{"bid above category limit", "100", "", "17", "pool_bid_invalid", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := estimatePool(pool, tc.forms, bidForecast{tc.total, tc.bidLots}, "")
			if got.Method != tc.method {
				t.Fatalf("method %s, want %s", got.Method, tc.method)
			}
			if tc.shares == "" {
				if got.ExpectedShares != nil || got.AllotmentChance != nil {
					t.Fatal("invalid inputs produced an allotment estimate", got)
				}
			} else if *got.ExpectedShares != tc.shares || *got.AllotmentChance != tc.chance {
				t.Fatalf("estimate %s shares and %s chance, want %s and %s", *got.ExpectedShares, *got.AllotmentChance, tc.shares, tc.chance)
			}
		})
	}
}

func TestAllotmentMatchesSEBIScheduleXIVExamples(t *testing.T) {
	retail := allotmentPool{ReservedShares: "3500000", MinimumShares: 20, LotSize: 20, MinBidLots: 1}
	got := estimatePool(retail, "100000", bidForecast{"14000000", "16"}, "")
	if *got.ExpectedShares != "58.000000" {
		t.Fatal("mainboard Retail example A should allot 20 plus 38 shares", *got.ExpectedShares)
	}

	sme := allotmentPool{ReservedShares: "3600000", MinimumShares: 18000, LotSize: 6000, MinBidLots: 3, sme: true}
	got = estimatePool(sme, "100", bidForecast{"5400000", "4"}, "")
	if *got.ExpectedShares != "18000.000000" {
		t.Fatal("SME NII example A should drop the extra half of a trading lot", *got.ExpectedShares)
	}
	got = estimatePool(sme, "100", bidForecast{"5400000", "5"}, "")
	if *got.ExpectedShares != "24000.000000" {
		t.Fatal("SME NII example A should keep the extra whole trading lot", *got.ExpectedShares)
	}
	sme.ReservedShares, sme.MinimumShares, sme.MinBidLots, sme.MaxBidLots = "1440000", 12000, 2, new(int64(2))
	got = estimatePool(sme, "100", bidForecast{}, "")
	if *got.ExpectedShares != "12000.000000" || got.Method != "full_bid" {
		t.Fatal("SME Individual demand should follow from two lots per form")
	}
}

func TestNIIQuotaTransferAndSubjectTwo(t *testing.T) {
	small := allotmentPool{ReservedShares: "50000", MinimumShares: 50, LotSize: 50, MinBidLots: 1, transferEligible: true}
	large := allotmentPool{ReservedShares: "100000", MinimumShares: 50, LotSize: 50, MinBidLots: 3, transferEligible: true}
	pools := map[string]allotmentPool{"SHNI": small, "BHNI": large}
	inputs := bidForecasts{"SHNI": {ExpectedBidShares: "40000"}, "BHNI": {ExpectedBidShares: "106000"}}
	forms := applicationForecasts{SHNI: new("500"), BHNI: new("600")}
	moves := transferNIIQuota(pools, inputs, forms)
	if moves["SHNI"] != "-6000" || moves["BHNI"] != "6000" ||
		pools["SHNI"].ReservedShares != "44000" || pools["BHNI"].ReservedShares != "106000" {
		t.Fatal("transfer must conserve quota and fill only unmet demand", pools, moves)
	}
	inputs["SHNI"], inputs["BHNI"] = bidForecast{ExpectedBidShares: "65000"}, bidForecast{ExpectedBidShares: "90000"}
	pools["SHNI"], pools["BHNI"] = small, large
	if moves = transferNIIQuota(pools, inputs, forms); moves["SHNI"] != "10000" || moves["BHNI"] != "-10000" {
		t.Fatal("transfer should also apply in the opposite direction", moves)
	}
	delete(inputs, "SHNI")
	if len(transferNIIQuota(pools, inputs, forms)) != 0 {
		t.Fatal("unknown demand should leave the pools as published")
	}

	pool := allotmentPool{ReservedShares: "10000", MinimumShares: 50, LotSize: 50, MinBidLots: 1}
	estimate := estimatePool(pool, "100", bidForecast{"15000", "5"}, "")
	position := livePosition{Factor: "0.8", AllotmentEstimates: map[string]allotmentEstimate{"RETAIL": estimate}}
	bucket := positionBucket{Form: "RETAIL", Subject: "subject_2", AgreedAmount: "2000"}
	if err := forecastPositionBucket(&position, &bucket, big.NewRat(2, 1), big.NewRat(10, 1)); err != nil {
		t.Fatal(err)
	}
	if *bucket.ExpectedShares != "300.000000" || *bucket.PremiumCost != "2000.00" || *bucket.Subject2Rate != "1200" {
		t.Fatal("Subject 2 should use the 100% chance of any allotment", bucket)
	}
}

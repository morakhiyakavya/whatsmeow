package main

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Both the editor and report use the same snapshot, provided its issue terms still match.
const matchingForecastSnapshot = `CASE WHEN b.snapshot->'ipo'->>'board'=i.board
 AND b.snapshot->'ipo'->'lot_size'=to_jsonb(i.lot_size)
 AND b.snapshot->'ipo'->>'price_high' IS NOT DISTINCT FROM i.price_high::text
 AND b.snapshot->'ipo'->>'opens_on' IS NOT DISTINCT FROM i.opens_on::text
 THEN b.snapshot END`

type allotmentPool struct {
	ReservedShares   string `json:"reserved_shares"`
	MinimumShares    int64  `json:"minimum_allotment_shares"`
	LotSize          int64  `json:"lot_size"`
	MinBidLots       int64  `json:"min_bid_lots"`
	MaxBidLots       *int64 `json:"max_bid_lots,omitempty"`
	sme              bool
	transferEligible bool
	fixedBidLots     *int64
}

func (p allotmentPool) basis() calculationBasis {
	shares, _ := new(big.Int).SetString(p.ReservedShares, 10)
	if p.sme {
		// Published SME capacity is an estimate. The forecast tests the exact quota separately.
		shares.Add(shares, big.NewInt(p.MinimumShares/2))
	}
	capacity := shares.Quo(shares, big.NewInt(p.MinimumShares))
	return calculationBasis{strconv.FormatInt(p.MinimumShares, 10), capacity.String(), "applications"}
}

func catalogueAllotmentPools(snapshot json.RawMessage) map[string]allotmentPool {
	var facts struct {
		IPO struct {
			Board string `json:"board"`
			Lot   int64  `json:"lot_size"`
			Price string `json:"price_high"`
			Opens string `json:"opens_on"`
		} `json:"ipo"`
		Reservations []struct {
			Category string `json:"category"`
			Shares   string `json:"reserved_shares"`
		} `json:"reservations"`
		Subscriptions []struct {
			Category string `json:"category"`
			Shares   string `json:"offered_shares"`
			Coverage string `json:"coverage"`
		} `json:"subscriptions"`
	}
	pools := map[string]allotmentPool{}
	if json.Unmarshal(snapshot, &facts) != nil || facts.IPO.Lot <= 0 {
		return pools
	}
	minimums := map[string]int64{}
	bidSizes := map[string]ipoApplication{"retail": {MinLots: 1}}
	if facts.IPO.Board == "Mainboard" {
		minimums["retail"] = facts.IPO.Lot
	}
	for _, category := range ipoApplications(facts.IPO.Board, facts.IPO.Price, facts.IPO.Lot, facts.IPO.Opens) {
		bidSizes[category.Category] = category
		switch {
		case category.Category == "individual":
			bidSizes["retail"] = category
			if shares, err := strconv.ParseInt(category.MinShares, 10, 64); err == nil {
				minimums["retail"] = shares
			}
		case category.Category == "shni" && (facts.IPO.Board == "SME" || facts.IPO.Opens > "2022-04-01"):
			if shares, err := strconv.ParseInt(category.MinShares, 10, 64); err == nil {
				minimums["shni"] = shares
				if facts.IPO.Board == "Mainboard" {
					minimums["bhni"] = shares
				}
			}
		case facts.IPO.Board == "SME" && category.Category == "bhni" && facts.IPO.Lot <= math.MaxInt64/smeNIIMinLots:
			// Schedule XIV Part A2 uses the common NII allotment minimum for both SME pools.
			minimums["bhni"] = facts.IPO.Lot * smeNIIMinLots
		}
	}
	for category, minimum := range minimums {
		sourceCategory, coverage := category, "NSE+BSE"
		if facts.IPO.Board == "SME" {
			coverage = "NSE only"
			if category == "retail" {
				sourceCategory = "individual"
			}
		}
		var offered, offeredCoverage string
		for _, row := range facts.Reservations {
			label := strings.TrimSpace(strings.ToLower(row.Category))
			matches := label == category || label == category+" shares offered" || category == "retail" && label == "retail individual investors"
			switch {
			case category == "retail" && facts.IPO.Board == "SME":
				matches = matches || label == "individual" || label == "individual investors" || label == "individual shares offered"
			case category == "shni":
				matches = matches || label == "snii" || label == "snii < ₹10l"
			case category == "bhni":
				matches = matches || label == "bnii" || label == "bnii > ₹10l"
			}
			if row.Shares != "" && matches {
				if offered != "" && offered != row.Shares {
					offered = "conflicting"
					break
				}
				offered = row.Shares
			}
		}
		if offered == "" {
			for _, row := range facts.Subscriptions {
				if row.Category == sourceCategory && row.Coverage == coverage {
					offered = row.Shares
					offeredCoverage = row.Coverage
					break
				}
				if facts.IPO.Board == "Mainboard" && row.Category == sourceCategory && row.Coverage == "NSE only" {
					offered = row.Shares
					offeredCoverage = row.Coverage
				}
			}
		}
		if !applicationCountPattern.MatchString(offered) || strings.TrimLeft(offered, "0") == "" || bidSizes[category].MinLots <= 0 {
			continue
		}
		sme := facts.IPO.Board == "SME"
		pools[strings.ToUpper(category)] = allotmentPool{
			ReservedShares: strings.TrimLeft(offered, "0"), MinimumShares: minimum, LotSize: facts.IPO.Lot,
			MinBidLots: bidSizes[category].MinLots, MaxBidLots: bidSizes[category].MaxLots, sme: sme,
			transferEligible: (category == "shni" || category == "bhni") && (sme || offeredCoverage != "NSE only"),
		}
	}
	if retail, ok := pools["RETAIL"]; facts.IPO.Board == "Mainboard" && ok && retail.MaxBidLots != nil {
		retail.fixedBidLots = retail.MaxBidLots
		pools["RETAIL_199"] = retail
	}
	return pools
}

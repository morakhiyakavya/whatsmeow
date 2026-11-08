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

func catalogueForecastBasis(snapshot json.RawMessage) map[string]calculationBasis {
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
	basis := map[string]calculationBasis{}
	if json.Unmarshal(snapshot, &facts) != nil || facts.IPO.Lot <= 0 {
		return basis
	}
	// shortcut: estimate minimum allotment slots from published quotas until actual allotment models are available.
	minimums := map[string]int64{}
	if facts.IPO.Board == "Mainboard" {
		minimums["retail"] = facts.IPO.Lot
	}
	for _, category := range ipoApplications(facts.IPO.Board, facts.IPO.Price, facts.IPO.Lot, facts.IPO.Opens) {
		switch {
		case category.Category == "individual":
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
		var offered string
		for _, row := range facts.Reservations {
			label := strings.TrimSpace(strings.ToLower(row.Category))
			matches := label == category || label == category+" shares offered" || category == "retail" && label == "retail individual investors"
			if facts.IPO.Board == "SME" {
				switch category {
				case "retail":
					matches = matches || label == "individual" || label == "individual investors" || label == "individual shares offered"
				case "shni":
					matches = matches || label == "snii" || label == "snii < ₹10l"
				case "bhni":
					matches = matches || label == "bnii" || label == "bnii > ₹10l"
				}
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
					break
				}
			}
		}
		if !applicationCountPattern.MatchString(offered) {
			continue
		}
		shares, _ := new(big.Int).SetString(offered, 10)
		capacity := shares.Quo(shares, big.NewInt(minimum))
		if capacity.Sign() > 0 {
			basis[strings.ToUpper(category)] = calculationBasis{strconv.FormatInt(minimum, 10), capacity.String(), "applications"}
		}
	}
	return basis
}

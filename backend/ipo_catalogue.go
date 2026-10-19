package main

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"time"
)

// Source mappings and category-amount approach: see THIRD_PARTY_NOTICES.md.
type ipoApplication struct {
	Category  string `json:"category"`
	MinLots   int64  `json:"min_lots"`
	MaxLots   *int64 `json:"max_lots"`
	MinShares string `json:"min_shares"`
	Amount    string `json:"min_amount"`
	Price     string `json:"price_basis"`
	Currency  string `json:"currency"`
	Rule      string `json:"rule"`
}

func ipoApplications(board, price string, lot int64, opens string) []ipoApplication {
	out := []ipoApplication{}
	p, ok := new(big.Rat).SetString(price)
	if !ok || p.Sign() <= 0 || lot <= 0 {
		return out
	}
	perLot := new(big.Rat).Mul(p, new(big.Rat).SetInt64(lot))
	below := func(limit int64) int64 {
		q := new(big.Rat).Quo(new(big.Rat).SetInt64(limit), perLot)
		n := new(big.Int).Quo(q.Num(), q.Denom())
		if !n.IsInt64() || n.Int64() > 1_000_000_000 {
			return -1
		}
		return n.Int64()
	}
	retailMax, smallMax := below(200000), below(1000000)
	if retailMax < 0 || smallMax < 0 {
		return out
	}
	add := func(category string, min int64, max *int64, rule string) {
		if min <= 0 || (max != nil && min > *max) {
			return
		}
		shares := new(big.Int).Mul(big.NewInt(min), big.NewInt(lot))
		out = append(out, ipoApplication{category, min, max, shares.String(), new(big.Rat).Mul(p, new(big.Rat).SetInt(shares)).FloatString(2), price, "INR", rule})
	}
	switch board {
	case "Mainboard":
		add("retail", 1, &retailMax, "Mainboard: amount <= INR 200000")
		add("shni", retailMax+1, &smallMax, "Mainboard: INR 200000 < amount <= INR 1000000")
		add("bhni", smallMax+1, nil, "Mainboard: amount > INR 1000000")
	case "SME":
		// Earlier SME issues have different rules; do not reinterpret their history.
		if opens < "2025-07-01" || new(big.Rat).Mul(perLot, big.NewRat(2, 1)).Cmp(big.NewRat(200000, 1)) <= 0 {
			return out
		}
		two := int64(2)
		add("individual", 2, &two, "SME from 2025-07-01: exactly 2 lots; amount > INR 200000")
		add("shni", 3, &smallMax, "SME from 2025-07-01: more than 2 lots; amount <= INR 1000000")
		min := smallMax + 1
		if min < 3 {
			min = 3
		}
		add("bhni", min, nil, "SME from 2025-07-01: more than 2 lots; amount > INR 1000000")
	}
	return out
}

const ipoCatalogueJSON = ipoJSON + ` || jsonb_build_object(
 'issue_size_shares',i.issue_size_shares::text,'issue_size_amount',i.issue_size_amount::text,
 'subscriptions',COALESCE((SELECT jsonb_agg(to_jsonb(s)||jsonb_build_object(
 'offered_shares',s.offered_shares::text,'bid_shares',s.bid_shares::text,'applications',s.applications::text,
 'multiple',s.multiple::text,'stale',s.observed_at<now()-interval '90 minutes') ORDER BY s.category)
 FROM ipo_pos.ipo_subscriptions s WHERE s.ipo_id=i.id),'[]'::jsonb),
 'reservations',COALESCE((SELECT jsonb_agg(to_jsonb(s)||jsonb_build_object('reserved_shares',s.reserved_shares::text,
 'percentage',s.percentage::text) ORDER BY s.category) FROM ipo_pos.ipo_reservations s WHERE s.ipo_id=i.id),'[]'::jsonb),
 'promoter_shareholdings',COALESCE((SELECT jsonb_agg(to_jsonb(s)||jsonb_build_object('pre_percentage',s.pre_percentage::text,
 'post_percentage',s.post_percentage::text) ORDER BY s.holder) FROM ipo_pos.ipo_promoter_shareholdings s WHERE s.ipo_id=i.id),'[]'::jsonb),
 'anchor_lockins',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY s.expires_on,s.label)
 FROM ipo_pos.ipo_anchor_lockins s WHERE s.ipo_id=i.id),'[]'::jsonb),
 'catalogue_stale',i.last_seen_at IS NULL OR i.last_seen_at<now()-interval '90 minutes')`

func decorateIPO(data json.RawMessage) (json.RawMessage, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(data, &row); err != nil {
		return nil, err
	}
	stringField := func(key string) string { var s string; _ = json.Unmarshal(row[key], &s); return s }
	var lot int64
	if len(row["lot_size"]) > 0 && string(row["lot_size"]) != "null" {
		if err := json.Unmarshal(row["lot_size"], &lot); err != nil {
			return nil, errors.New("invalid catalogue lot")
		}
	}
	apps := ipoApplications(stringField("board"), stringField("price_high"), lot, stringField("opens_on"))
	row["category_applications"], _ = json.Marshal(apps)
	if string(row["issue_size_amount"]) != "null" && len(row["issue_size_amount"]) > 0 {
		row["issue_size_currency"] = json.RawMessage(`"INR"`)
	}
	today := time.Now().In(time.FixedZone("IST", 19800)).Format("2006-01-02")
	status := "unknown"
	open, close, listing := stringField("opens_on"), stringField("closes_on"), stringField("listing_on")
	switch {
	case stringField("source_status") == "postponed" || stringField("source_status") == "withdrawn":
		status = stringField("source_status")
	case listing != "" && listing <= today:
		status = "listed"
	case close != "" && close < today:
		status = "closed_awaiting_listing"
	case open != "" && open > today:
		status = "upcoming"
	case open != "" && close != "" && open <= today && today <= close:
		status = "open"
	}
	row["status"], _ = json.Marshal(status)
	return json.Marshal(row)
}

func positiveLot(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > 1_000_000_000 {
		return 0
	}
	return n
}

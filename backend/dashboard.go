package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"
)

type positionReason struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

type positionMeasures struct {
	ExpectedShares  *string          `json:"net_expected_shares"`
	ConfirmedShares *string          `json:"net_confirmed_shares"`
	Shares          *string          `json:"net_shares"`
	PremiumCost     *string          `json:"net_premium_cost"`
	BreakEven       *string          `json:"break_even_premium"`
	ProfitLoss      *string          `json:"estimated_profit_loss"`
	Reasons         []positionReason `json:"reasons"`
}

type positionBucket struct {
	Form         string  `json:"form_type_code"`
	Subject      string  `json:"category"`
	Unit         string  `json:"quantity_unit"`
	Count        int64   `json:"transaction_count"`
	BuyQuantity  string  `json:"buy_quantity"`
	SellQuantity string  `json:"sell_quantity"`
	NetQuantity  string  `json:"net_quantity"`
	BuyAmount    string  `json:"buy_recorded_amount"`
	SellAmount   string  `json:"sell_recorded_amount"`
	NetAmount    string  `json:"net_recorded_amount"`
	BuyAverage   *string `json:"buy_average_rate"`
	SellAverage  *string `json:"sell_average_rate"`
	AgreedAmount string  `json:"unrounded_agreed_amount"`
	Subject1Rate *string `json:"subject1_suggested_rate"`
	Subject2Rate *string `json:"subject2_suggested_rate"`
	positionMeasures
}

type livePosition struct {
	BusinessID              string                      `json:"business_id"`
	IPOID                   string                      `json:"ipo_id"`
	Code                    string                      `json:"code"`
	Name                    string                      `json:"name"`
	Board                   *string                     `json:"board"`
	Currency                string                      `json:"currency"`
	ViewKind                string                      `json:"view_kind"`
	CalculationVersion      int                         `json:"calculation_version"`
	CalculatedAt            time.Time                   `json:"calculated_at"`
	Date                    string                      `json:"date"`
	Count                   int64                       `json:"transaction_count"`
	Empty                   bool                        `json:"empty"`
	Status                  string                      `json:"calculation_status"`
	ProfitLossBasis         *string                     `json:"profit_loss_basis"`
	GMP                     *string                     `json:"gmp"`
	Revision                string                      `json:"revision"`
	Factor                  string                      `json:"subject2_pricing_factor"`
	Applications            applicationForecasts        `json:"expected_applications"`
	Basis                   map[string]calculationBasis `json:"calculation_basis"`
	AdditionalCost          string                      `json:"additional_cost"`
	AssumptionVersion       int                         `json:"assumption_version"`
	AssumptionsRecordedAt   *time.Time                  `json:"assumptions_recorded_at"`
	BasisVersion            *int                        `json:"basis_version"`
	SourceLastSeenAt        *time.Time                  `json:"source_last_seen_at"`
	SourceStale             bool                        `json:"source_stale"`
	ShareholderAvailability string                      `json:"shareholder_availability"`
	NetRecordedAmount       string                      `json:"net_recorded_amount"`
	KnownShares             string                      `json:"known_net_shares"`
	KnownExpectedShares     string                      `json:"known_net_expected_shares"`
	DirectShares            string                      `json:"net_direct_shares"`
	PendingShares           *string                     `json:"net_pending_shares"`
	KnownPremiumCost        string                      `json:"known_net_premium_cost"`
	Categories              []positionBucket            `json:"categories,omitempty"`
	positionMeasures
}

// Sum current active rows in PostgreSQL; only the fixed form/subject buckets reach Go.
const livePositionSQL = `WITH buckets AS (
 SELECT form_type_code,category,count(*) AS transaction_count,
  COALESCE(sum(quantity) FILTER (WHERE quantity>0),0)::text AS buy_quantity,
  COALESCE(sum(-quantity) FILTER (WHERE quantity<0),0)::text AS sell_quantity,
  sum(quantity)::text AS net_quantity,
  COALESCE(sum(total_amount) FILTER (WHERE quantity>0),0)::text AS buy_recorded_amount,
  COALESCE(sum(-total_amount) FILTER (WHERE quantity<0),0)::text AS sell_recorded_amount,
  sum(total_amount)::text AS net_recorded_amount,sum(quantity*rate)::text AS unrounded_agreed_amount
 FROM ipo_pos.transactions WHERE business_id=$1 AND ipo_id=$2 AND status='confirmed'
 GROUP BY form_type_code,category
) SELECT jsonb_build_object('business_id',$1::uuid,'ipo_id',i.id,'code',i.code,'name',i.name,'board',i.board,
 'currency',(SELECT currency_code FROM ipo_pos.businesses WHERE id=$1),'calculated_at',statement_timestamp(),
 'revision',COALESCE((SELECT revision FROM ipo_pos.business_data_revisions WHERE business_id=$1),0)::text,
 'gmp',a.gmp::text,'assumption_version',COALESCE(a.version,0),'assumptions_recorded_at',a.recorded_at,
 'calculation_basis',COALESCE(a.calculation_basis,'{}'::jsonb),'subject2_pricing_factor',COALESCE(a.subject2_pricing_factor,0.8)::text,
 'additional_cost',COALESCE(a.additional_cost,0)::text,
 'expected_applications',jsonb_build_object('RETAIL',a.retail_expected_applications::text,'SHNI',a.shni_expected_applications::text,
 'BHNI',a.bhni_expected_applications::text,'SHAREHOLDER',a.shareholder_expected_applications::text),
 'basis_version',b.version,'shareholder_availability',COALESCE(b.shareholder_availability,'unknown'),
 'forecast_snapshot',` + matchingForecastSnapshot + `,
 'source_last_seen_at',i.last_seen_at,'source_stale',i.last_seen_at IS NULL OR i.last_seen_at<now()-interval '90 minutes',
 'categories',COALESCE((SELECT jsonb_agg(to_jsonb(buckets) ORDER BY form_type_code,category) FROM buckets),'[]'::jsonb))
 FROM ipo_pos.ipos i
 LEFT JOIN LATERAL (SELECT * FROM ipo_pos.ipo_dashboard_assumption_versions
  WHERE business_id=$1 AND ipo_id=i.id ORDER BY version DESC LIMIT 1) a ON true
 LEFT JOIN LATERAL (SELECT version,shareholder_availability,snapshot FROM ipo_pos.ipo_dashboard_basis_versions
  WHERE ipo_id=i.id ORDER BY version DESC LIMIT 1) b ON true
 WHERE i.id=$2`

func (a *api) getLivePosition(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := analyticsIPO(w, r)
	if !ok {
		return
	}
	for key := range r.URL.Query() {
		if key != "include" && key != "date" && key != "revision" {
			fail(w, 400, "invalid_filter", "Only today's YYYY-MM-DD date and include=categories are supported")
			return
		}
	}
	q := r.URL.Query()
	if len(q["revision"]) > 1 || q.Get("revision") != "" && !validDirectoryRevision(q.Get("revision")) {
		fail(w, 400, "invalid_revision", "Supply a business-data revision")
		return
	}
	if len(q["include"]) > 1 || len(q["date"]) > 1 || q.Get("include") != "" && q.Get("include") != "categories" {
		fail(w, 400, "invalid_filter", "Use include=categories for the live breakdown")
		return
	}
	today := time.Now().In(time.FixedZone("IST", 19800)).Format("2006-01-02")
	if date := q.Get("date"); date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil || date > today {
			fail(w, 400, "invalid_date", "Supply today's date in Asia/Kolkata using YYYY-MM-DD")
			return
		}
		if date < today {
			fail(w, 404, "close_unavailable", "Original daily closes are not available in this increment")
			return
		}
	}
	var data json.RawMessage
	if err := a.db.QueryRowContext(r.Context(), livePositionSQL, user.BusinessID, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "book_unavailable", "IPO not found")
		} else {
			dbFailure(w, err)
		}
		return
	}
	var result struct {
		livePosition
		ForecastSnapshot json.RawMessage `json:"forecast_snapshot"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		dbFailure(w, err)
		return
	}
	if q.Get("revision") != "" && q.Get("revision") != result.Revision {
		fail(w, 409, "business_data_changed", "Business data changed; refresh before loading this report")
		return
	}
	for code, basis := range catalogueForecastBasis(result.ForecastSnapshot) {
		result.Basis[code] = basis
	}
	if err := calculateLivePosition(&result.livePosition); err != nil {
		dbFailure(w, err)
		return
	}
	if q.Get("include") == "categories" {
		writeJSON(w, 200, struct {
			livePosition
			Categories []positionBucket `json:"categories"`
		}{result.livePosition, result.Categories})
		return
	}
	result.Categories = nil
	writeJSON(w, 200, result.livePosition)
}

func (a *api) listPositionBooks(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	after := r.URL.Query().Get("after_id")
	if p.After != nil || after != "" && !validUUID(after) {
		fail(w, 400, "invalid_cursor", "Use the returned after_id")
		return
	}
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	}
	queryJSON(w, r, a.db, 200, `WITH books AS (
 SELECT i.id,i.code,i.name,i.board FROM ipo_pos.ipos i WHERE i.id>$2::uuid
 AND EXISTS(SELECT 1 FROM ipo_pos.transactions t WHERE t.business_id=$1 AND t.ipo_id=i.id)
 ORDER BY i.id LIMIT ($3+1)
 ), visible AS (SELECT * FROM books ORDER BY id LIMIT $3)
 SELECT jsonb_build_object('items',COALESCE((SELECT jsonb_agg(jsonb_build_object('ipo_id',id,'code',code,'name',name,'board',board) ORDER BY id) FROM visible),'[]'::jsonb),
 'next_after_id',CASE WHEN (SELECT count(*) FROM books)>$3 THEN (SELECT id FROM visible ORDER BY id DESC LIMIT 1) END)`, user.BusinessID, after, p.Limit)
}

func positionNumber(s string) (*big.Rat, error) {
	// Inputs are PostgreSQL numeric strings, including sums beyond transaction limits.
	if strings.ContainsAny(s, "/eE") {
		return nil, errors.New("invalid position decimal")
	}
	n, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("invalid position decimal")
	}
	return n, nil
}

func positionString(n *big.Rat, places int) *string {
	s := n.FloatString(places)
	if strings.HasPrefix(s, "-") && strings.Trim(s[1:], "0.") == "" {
		s = s[1:]
	}
	return &s
}

func valuePosition(m *positionMeasures, gmp *big.Rat) error {
	if m.Shares == nil || m.PremiumCost == nil {
		m.Reasons = append(m.Reasons, positionReason{"break_even_premium", "position_incomplete"}, positionReason{"estimated_profit_loss", "position_incomplete"})
		return nil
	}
	shares, err := positionNumber(*m.Shares)
	if err != nil {
		return err
	}
	cost, err := positionNumber(*m.PremiumCost)
	if err != nil {
		return err
	}
	if shares.Sign() != 0 {
		m.BreakEven = positionString(new(big.Rat).Quo(cost, shares), 6)
	} else {
		m.Reasons = append(m.Reasons, positionReason{"break_even_premium", "zero_net_shares"})
	}
	if gmp == nil {
		m.Reasons = append(m.Reasons, positionReason{"estimated_profit_loss", "gmp_missing"})
	} else {
		m.ProfitLoss = positionString(new(big.Rat).Sub(new(big.Rat).Mul(shares, gmp), cost), 2)
	}
	return nil
}

func calculateLivePosition(p *livePosition) error {
	p.ViewKind, p.CalculationVersion = "live", 3
	p.Count = 0
	p.positionMeasures = positionMeasures{Reasons: []positionReason{}}
	p.Date = p.CalculatedAt.In(time.FixedZone("IST", 19800)).Format("2006-01-02")
	p.Reasons = []positionReason{}
	var gmp *big.Rat
	var err error
	if p.GMP != nil {
		gmp, err = positionNumber(*p.GMP)
		if err != nil {
			return err
		}
	}
	shares, cost, recorded, expectedShares, confirmedShares := new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
	directShares := new(big.Rat)
	expectedKnown, confirmedKnown, costKnown := true, true, true
	for i := range p.Categories {
		b := &p.Categories[i]
		b.positionMeasures = positionMeasures{Reasons: []positionReason{}}
		b.Subject1Rate, b.Subject2Rate = nil, nil
		p.Count += b.Count
		values := make([]*big.Rat, 6)
		for j, s := range []string{b.BuyQuantity, b.SellQuantity, b.NetQuantity, b.BuyAmount, b.SellAmount, b.NetAmount} {
			values[j], err = positionNumber(s)
			if err != nil {
				return err
			}
		}
		b.BuyQuantity = *positionString(values[0], 6)
		b.SellQuantity = *positionString(values[1], 6)
		b.NetQuantity = *positionString(values[2], 6)
		b.BuyAmount = *positionString(values[3], 2)
		b.SellAmount = *positionString(values[4], 2)
		b.NetAmount = *positionString(values[5], 2)
		if values[0].Sign() != 0 {
			b.BuyAverage = positionString(new(big.Rat).Quo(values[3], values[0]), 6)
		}
		if values[1].Sign() != 0 {
			b.SellAverage = positionString(new(big.Rat).Quo(values[4], values[1]), 6)
		}
		recorded.Add(recorded, values[5])
		b.ExpectedShares = new("0.000000")
		b.ConfirmedShares = new("0.000000")
		b.PremiumCost = &b.NetAmount
		switch b.Form {
		case "SHARES":
			directShares.Add(directShares, values[2])
			b.Unit = "shares"
			b.ConfirmedShares = &b.NetQuantity
			b.Shares = &b.NetQuantity
		case "CE", "PE":
			b.Unit = "underlying_shares"
			b.Shares = new("0.000000")
			b.Reasons = append(b.Reasons, positionReason{"suggested_rate", "pricing_guidance_deferred"})
		default:
			b.Unit = "applications"
			if b.Subject == "none" {
				b.ConfirmedShares = nil
				b.Reasons = append(b.Reasons, positionReason{"net_confirmed_shares", "allotted_share_basis_missing"})
			} else {
				if err := forecastPositionBucket(p, b, values[2], gmp); err != nil {
					return err
				}
			}
			if b.Form == "SHAREHOLDER" && p.ShareholderAvailability != "available" {
				code := "shareholder_availability_unknown"
				if p.ShareholderAvailability == "not_offered" {
					code = "shareholder_source_inconsistency"
					b.ExpectedShares, b.Shares = nil, nil
				}
				b.Reasons = append(b.Reasons, positionReason{"net_shares", code})
			}
		}
		if b.ExpectedShares == nil {
			expectedKnown = false
		}
		if b.ConfirmedShares == nil {
			confirmedKnown = false
		}
		if b.ExpectedShares != nil {
			n, err := positionNumber(*b.ExpectedShares)
			if err != nil {
				return err
			}
			expectedShares.Add(expectedShares, n)
		}
		if b.ConfirmedShares != nil {
			n, err := positionNumber(*b.ConfirmedShares)
			if err != nil {
				return err
			}
			confirmedShares.Add(confirmedShares, n)
		}
		if b.Shares != nil {
			n, err := positionNumber(*b.Shares)
			if err != nil {
				return err
			}
			shares.Add(shares, n)
		}
		if b.PremiumCost == nil {
			costKnown = false
		} else {
			n, err := positionNumber(*b.PremiumCost)
			if err != nil {
				return err
			}
			cost.Add(cost, n)
		}
		if err = valuePosition(&b.positionMeasures, gmp); err != nil {
			return err
		}
	}
	p.Empty = p.Count == 0
	if p.AdditionalCost != "" {
		extra, err := positionNumber(p.AdditionalCost)
		if err != nil {
			return err
		}
		cost.Add(cost, extra)
	}
	p.NetRecordedAmount = *positionString(recorded, 2)
	p.KnownShares = *positionString(shares, 6)
	p.KnownExpectedShares = *positionString(expectedShares, 6)
	p.DirectShares = *positionString(directShares, 6)
	p.PendingShares = nil
	p.KnownPremiumCost = *positionString(cost, 2)
	if expectedKnown {
		p.ExpectedShares = positionString(expectedShares, 6)
		p.PendingShares = positionString(new(big.Rat).Add(expectedShares, directShares), 6)
	} else {
		p.Reasons = append(p.Reasons, positionReason{"net_expected_shares", "application_or_contract_basis_unavailable"})
	}
	if confirmedKnown {
		p.ConfirmedShares = positionString(confirmedShares, 6)
	} else {
		p.Reasons = append(p.Reasons, positionReason{"net_confirmed_shares", "application_or_contract_basis_unavailable"})
	}
	if expectedKnown && confirmedKnown {
		p.Shares = &p.KnownShares
	} else {
		p.Reasons = append(p.Reasons, positionReason{"net_shares", "position_incomplete"})
	}
	if costKnown {
		p.PremiumCost = &p.KnownPremiumCost
	} else {
		p.Reasons = append(p.Reasons, positionReason{"net_premium_cost", "application_or_contract_cost_unavailable"})
	}
	if err = valuePosition(&p.positionMeasures, gmp); err != nil {
		return err
	}
	p.Status = "partial"
	if p.Shares != nil && p.PremiumCost != nil && p.ProfitLoss != nil {
		p.Status = "complete"
		p.ProfitLossBasis = new("forecast")
	}
	return nil
}

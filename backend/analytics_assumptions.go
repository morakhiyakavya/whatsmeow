package main

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type applicationForecasts struct {
	Retail      *string `json:"RETAIL"`
	SHNI        *string `json:"SHNI"`
	BHNI        *string `json:"BHNI"`
	Shareholder *string `json:"SHAREHOLDER"`
}

type assumptionInput struct {
	ExpectedVersion *int                  `json:"expected_version"`
	GMP             *string               `json:"gmp"`
	Factor          *string               `json:"subject2_pricing_factor"`
	Applications    *applicationForecasts `json:"expected_applications"`
	Reason          string                `json:"change_reason"`
}

var applicationCountPattern = regexp.MustCompile(`^[0-9]{1,24}$`)

func (in *assumptionInput) normalize() error {
	if in.ExpectedVersion == nil || *in.ExpectedVersion < 0 || *in.ExpectedVersion >= 2147483647 {
		return errors.New("Supply expected_version between 0 and 2147483646")
	}
	if in.GMP != nil {
		value, ok := decimal(*in.GMP, true)
		if !ok {
			return errors.New("GMP must be a signed decimal string with at most six fractional digits")
		}
		in.GMP = &value
	}
	if in.Factor == nil {
		return errors.New("Supply subject2_pricing_factor as a decimal string between 0 and 1")
	}
	value, ok := decimal(*in.Factor, true)
	if !ok {
		return errors.New("Subject 2 pricing factor must have at most six fractional digits")
	}
	factor, _ := new(big.Rat).SetString(value)
	if factor.Sign() < 0 || factor.Cmp(big.NewRat(1, 1)) > 0 {
		return errors.New("Subject 2 pricing factor must be between 0 and 1")
	}
	in.Factor = &value
	if in.Applications == nil {
		return errors.New("Supply expected_applications; unknown category counts may be null")
	}
	for _, count := range []*string{in.Applications.Retail, in.Applications.SHNI, in.Applications.BHNI, in.Applications.Shareholder} {
		if count == nil {
			continue
		}
		if !applicationCountPattern.MatchString(*count) {
			return errors.New("Expected application counts must be positive integer strings of at most 24 digits")
		}
		normal := strings.TrimLeft(*count, "0")
		if normal == "" {
			return errors.New("Use null for unknown application counts, not zero")
		}
		*count = normal
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if !validText(in.Reason, 2048) {
		return errors.New("Supply a nonempty change_reason of at most 2048 bytes")
	}
	return nil
}

const assumptionJSON = `jsonb_build_object('business_id',v.business_id,'ipo_id',v.ipo_id,
 'version',v.version,'gmp',v.gmp::text,'subject2_pricing_factor',v.subject2_pricing_factor::text,
 'expected_applications',jsonb_build_object('RETAIL',v.retail_expected_applications::text,
 'SHNI',v.shni_expected_applications::text,'BHNI',v.bhni_expected_applications::text,
 'SHAREHOLDER',v.shareholder_expected_applications::text),'recorded_at',v.recorded_at,
 'changed_by_user_id',v.changed_by_user_id,'change_reason',v.change_reason)`

func analyticsIPO(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("ipoID")
	if !validUUID(id) {
		fail(w, 400, "invalid_ipo", "Supply a valid IPO ID")
		return "", false
	}
	return strings.ToLower(id), true
}

func (a *api) getAnalyticsAssumptions(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := analyticsIPO(w, r)
	if !ok {
		return
	}
	queryJSON(w, r, a.db, 200, `SELECT coalesce((SELECT `+assumptionJSON+`
 FROM ipo_pos.ipo_dashboard_assumption_versions v WHERE business_id=$1 AND ipo_id=i.id
 ORDER BY version DESC LIMIT 1),jsonb_build_object('business_id',$1::uuid,'ipo_id',i.id,
 'version',0,'gmp',null,'subject2_pricing_factor','0.8','expected_applications',
 jsonb_build_object('RETAIL',null,'SHNI',null,'BHNI',null,'SHAREHOLDER',null),
 'recorded_at',null,'changed_by_user_id',null,'change_reason',null)) || jsonb_build_object(
 'source_basis',(SELECT to_jsonb(b) FROM ipo_pos.ipo_dashboard_basis_versions b
 WHERE b.ipo_id=i.id ORDER BY version DESC LIMIT 1),'catalogue_last_seen_at',i.last_seen_at)
 FROM ipo_pos.ipos i WHERE i.id=$2`, user.BusinessID, id)
}

func (a *api) putAnalyticsAssumptions(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := analyticsIPO(w, r)
	if !ok {
		return
	}
	var input assumptionInput
	if !decode(w, r, &input) {
		return
	}
	if err := input.normalize(); err != nil {
		fail(w, 400, "invalid_assumptions", err.Error())
		return
	}
	var data json.RawMessage
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO ipo_pos.ipo_dashboard_assumption_versions AS v
 (business_id,ipo_id,version,gmp,subject2_pricing_factor,retail_expected_applications,
 shni_expected_applications,bhni_expected_applications,shareholder_expected_applications,
 changed_by_user_id,change_reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
 RETURNING `+assumptionJSON, user.BusinessID, id, *input.ExpectedVersion+1, input.GMP,
		input.Factor, input.Applications.Retail, input.Applications.SHNI, input.Applications.BHNI,
		input.Applications.Shareholder, user.UserID, input.Reason).Scan(&data)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			fail(w, 409, "version_conflict", "Assumptions changed; fetch the current version before retrying")
			return
		}
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, data)
}

func (a *api) analyticsAssumptionHistory(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := analyticsIPO(w, r)
	if !ok {
		return
	}
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	if p.After != nil {
		fail(w, 400, "invalid_page", "History uses before_version, not cursor")
		return
	}
	var before any
	if text := r.URL.Query().Get("before_version"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 2147483647 {
			fail(w, 400, "invalid_page", "Invalid before_version")
			return
		}
		before = n
	}
	var exists bool
	if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM ipo_pos.ipos WHERE id=$1)`, id).Scan(&exists); err != nil {
		dbFailure(w, err)
		return
	}
	if !exists {
		fail(w, 404, "not_found", "IPO not found")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+assumptionJSON+` FROM ipo_pos.ipo_dashboard_assumption_versions v
 WHERE business_id=$1 AND ipo_id=$2 AND ($3::integer IS NULL OR version<$3)
 ORDER BY version DESC LIMIT $4`, user.BusinessID, id, before, p.Limit+1)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer rows.Close()
	items := []json.RawMessage{}
	var next *int
	last := 0
	for rows.Next() {
		var data json.RawMessage
		if err = rows.Scan(&data); err != nil {
			dbFailure(w, err)
			return
		}
		if len(items) == p.Limit {
			next = &last
			break
		}
		var entry struct{ Version int }
		if err = json.Unmarshal(data, &entry); err != nil {
			dbFailure(w, err)
			return
		}
		last = entry.Version
		items = append(items, data)
	}
	if err = rows.Err(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_before_version": next})
}

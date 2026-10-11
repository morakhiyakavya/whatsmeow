package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	phonePattern   = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	decimalPattern = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,6})?$`)
)

func validUUID(s string) bool              { return uuidPattern.MatchString(s) }
func validText(s string, max int) bool     { return strings.TrimSpace(s) != "" && len(s) <= max }
func optionalUUID(s *string) bool          { return s == nil || validUUID(*s) }
func optionalText(s *string, max int) bool { return s == nil || len(*s) <= max }

// Validate decimal strings before PostgreSQL can round them. Normalization makes
// equivalent inputs (2.50 and 2.5) produce the same creation request fingerprint.
func decimal(s string, negative bool) (string, bool) {
	if !decimalPattern.MatchString(s) || (!negative && strings.HasPrefix(s, "-")) {
		return "", false
	}
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "-"
		s = s[1:]
	}
	whole, fraction, _ := strings.Cut(s, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction != "" {
		whole += "." + fraction
	}
	if whole == "0" {
		sign = ""
	}
	return sign + whole, true
}

type cursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}
type page struct {
	Limit int
	After *cursor
}

func readPage(w http.ResponseWriter, r *http.Request) (page, bool) {
	p := page{Limit: 50}
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			fail(w, 400, "invalid_page", "limit must be between 1 and 100")
			return p, false
		}
		p.Limit = n
	}
	if s := r.URL.Query().Get("cursor"); s != "" {
		if len(s) > 512 {
			fail(w, 400, "invalid_cursor", "Invalid cursor")
			return p, false
		}
		data, err := base64.RawURLEncoding.DecodeString(s)
		var c cursor
		if err != nil || json.Unmarshal(data, &c) != nil || c.At.IsZero() || !validUUID(c.ID) {
			fail(w, 400, "invalid_cursor", "Invalid cursor")
			return p, false
		}
		p.After = &c
	}
	return p, true
}
func (p page) args() (any, any) {
	if p.After == nil {
		return nil, nil
	}
	return p.After.At, p.After.ID
}

func paginated(w http.ResponseWriter, rows *sql.Rows, p page) {
	defer rows.Close()
	items := []json.RawMessage{}
	var last cursor
	more := false
	for rows.Next() {
		var data json.RawMessage
		var c cursor
		if err := rows.Scan(&data, &c.At, &c.ID); err != nil {
			dbFailure(w, err)
			return
		}
		if len(items) == p.Limit {
			more = true
			break
		}
		items = append(items, data)
		last = c
	}
	if err := rows.Err(); err != nil {
		dbFailure(w, err)
		return
	}
	var next *string
	if more {
		data, _ := json.Marshal(last)
		s := base64.RawURLEncoding.EncodeToString(data)
		next = &s
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (a *api) listCustomers(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	at, id := p.args()
	rows, err := a.db.QueryContext(r.Context(), `SELECT to_jsonb(c),c.created_at,c.id FROM ipo_pos.customers c
        WHERE business_id=$1 AND archived_at IS NULL AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3::uuid))
        ORDER BY created_at DESC,id DESC LIMIT $4`, user.BusinessID, at, id, p.Limit+1)
	if err != nil {
		dbFailure(w, err)
		return
	}
	paginated(w, rows, p)
}

func (a *api) createCustomer(w http.ResponseWriter, r *http.Request, user actor) {
	var input struct {
		Name         string  `json:"name"`
		Phone        *string `json:"phone_number"`
		Referrer     *string `json:"referred_by_customer_id"`
		ReferralNote *string `json:"referral_note"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validText(input.Name, 256) || (input.Phone != nil && !phonePattern.MatchString(*input.Phone)) || !optionalUUID(input.Referrer) || !optionalText(input.ReferralNote, 2048) {
		fail(w, 400, "invalid_customer", "Supply a name, optional international phone and valid referral details")
		return
	}
	queryJSON(w, r, a.db, 201, `INSERT INTO ipo_pos.customers AS c(business_id,name,phone_number,referred_by_customer_id,referral_note,name_supplied_by_user_id)
        VALUES($1,$2,$3,$4,$5,$6) RETURNING to_jsonb(c)`, user.BusinessID, input.Name, input.Phone, input.Referrer, input.ReferralNote, user.UserID)
}

const ipoJSON = `to_jsonb(i) || jsonb_build_object('price_low',i.price_low::text,'price_high',i.price_high::text)`

func (a *api) listIPOs(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	at, id := p.args()
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+ipoJSON+`,i.created_at,i.id FROM ipo_pos.ipos i
        WHERE archived_at IS NULL AND ($1::timestamptz IS NULL OR (created_at,id)<($1,$2::uuid))
        ORDER BY created_at DESC,id DESC LIMIT $3`, at, id, p.Limit+1)
	if err != nil {
		dbFailure(w, err)
		return
	}
	paginated(w, rows, p)
}

func (a *api) createIPO(w http.ResponseWriter, r *http.Request, user actor) {
	var input struct {
		Code     string         `json:"code"`
		Name     string         `json:"name"`
		Issuer   *string        `json:"issuer_name"`
		Opens    *string        `json:"opens_on"`
		Closes   *string        `json:"closes_on"`
		Listing  *string        `json:"listing_on"`
		Low      *string        `json:"price_low"`
		High     *string        `json:"price_high"`
		Metadata map[string]any `json:"metadata"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	if !validText(input.Code, 64) || !validText(input.Name, 256) || !optionalText(input.Issuer, 256) {
		fail(w, 400, "invalid_ipo", "Supply an IPO code and name")
		return
	}
	for _, d := range []*string{input.Opens, input.Closes, input.Listing} {
		if d != nil {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				fail(w, 400, "invalid_date", "IPO dates must use YYYY-MM-DD")
				return
			}
		}
	}
	for _, d := range []*string{input.Low, input.High} {
		if d != nil {
			value, ok := decimal(*d, false)
			if !ok {
				fail(w, 400, "invalid_decimal", "Prices must be nonnegative decimal strings with at most six fractional digits")
				return
			}
			*d = value
		}
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, _ := json.Marshal(input.Metadata)
	queryJSON(w, r, a.db, 201, `INSERT INTO ipo_pos.ipos AS i(code,name,issuer_name,opens_on,closes_on,listing_on,price_low,price_high,metadata)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+ipoJSON, input.Code, input.Name, input.Issuer, input.Opens, input.Closes, input.Listing, input.Low, input.High, string(metadata))
}

func (a *api) listFormTypes(w http.ResponseWriter, r *http.Request, user actor) {
	queryJSON(w, r, a.db, 200, "SELECT jsonb_build_object('items',jsonb_agg(to_jsonb(f) ORDER BY code)) FROM ipo_pos.form_types f")
}

func (a *api) listStaff(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	at, id := p.args()
	rows, err := a.db.QueryContext(r.Context(), `SELECT jsonb_build_object('user_id',u.id,'name',u.name,'phone_number',u.phone_number,'role',m.role),m.joined_at,u.id
        FROM ipo_pos.business_memberships m JOIN ipo_pos.users u ON u.id=m.user_id
        WHERE m.business_id=$1 AND m.disabled_at IS NULL AND u.disabled_at IS NULL
        AND ($2::timestamptz IS NULL OR (m.joined_at,u.id)<($2,$3::uuid)) ORDER BY m.joined_at DESC,u.id DESC LIMIT $4`, user.BusinessID, at, id, p.Limit+1)
	if err != nil {
		dbFailure(w, err)
		return
	}
	paginated(w, rows, p)
}

func (a *api) createStaff(w http.ResponseWriter, r *http.Request, user actor) {
	var input struct {
		Name     string `json:"name"`
		Phone    string `json:"phone_number"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Role == "" {
		input.Role = "cashier"
	}
	if !validText(input.Name, 256) || !phonePattern.MatchString(input.Phone) || !validPassword(input.Password) || (input.Role != "admin" && input.Role != "cashier") {
		fail(w, 400, "invalid_staff", "Supply a name, international phone, 12–72 byte password and admin/cashier role")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(w, 500, "internal_error", "Could not create staff credentials")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(r.Context(), "INSERT INTO ipo_pos.users(name,phone_number) VALUES($1,$2) RETURNING id", input.Name, input.Phone).Scan(&id); err != nil {
		dbFailure(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO ipo_pos.business_memberships(business_id,user_id,role) VALUES($1,$2,$3)", user.BusinessID, id, input.Role); err != nil {
		dbFailure(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO ipo_pos.user_credentials(user_id,password_hash) VALUES($1,$2)", id, string(hash)); err != nil {
		dbFailure(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"user_id": id, "name": input.Name, "phone_number": input.Phone, "role": input.Role})
}

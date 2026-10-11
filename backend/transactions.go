package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type transactionFields struct {
	IPOID          string    `json:"ipo_id"`
	CustomerID     string    `json:"customer_id"`
	OccurredAt     time.Time `json:"occurred_at"`
	FormType       string    `json:"form_type_code"`
	Category       string    `json:"category"`
	CEPE           *string   `json:"ce_pe_value"`
	Quantity       string    `json:"quantity"`
	Rate           string    `json:"rate"`
	StaffID        *string   `json:"salesperson_user_id"`
	ConversationID *string   `json:"conversation_id"`
}
type transactionCreate struct {
	transactionFields
	Reason            string  `json:"change_reason"`
	WhatsAppAccountID *string `json:"whatsapp_account_id,omitempty"`
}
type transactionEdit struct {
	transactionFields
	ExpectedVersion int    `json:"expected_version"`
	Reason          string `json:"change_reason"`
}

func (f *transactionFields) validate(w http.ResponseWriter) bool {
	if !validUUID(f.IPOID) || !validUUID(f.CustomerID) || !optionalUUID(f.StaffID) || !optionalUUID(f.ConversationID) || f.OccurredAt.IsZero() || f.OccurredAt.Year() < 1 || f.OccurredAt.Year() > 9999 {
		fail(w, 400, "invalid_transaction", "Supply valid IPO/customer IDs, transaction time and optional staff/conversation IDs")
		return false
	}
	f.IPOID = strings.ToLower(f.IPOID)
	f.CustomerID = strings.ToLower(f.CustomerID)
	f.OccurredAt = f.OccurredAt.UTC()
	for _, id := range []*string{f.StaffID, f.ConversationID} {
		if id != nil {
			*id = strings.ToLower(*id)
		}
	}
	switch f.FormType {
	case "RETAIL", "RETAIL_199", "SHNI", "BHNI", "SHARES", "CE", "PE":
	default:
		fail(w, 400, "invalid_form_type", "Unknown form type")
		return false
	}
	if f.Category == "" {
		f.Category = "none"
	}
	if f.Category != "none" && f.Category != "subject_1" && f.Category != "subject_2" {
		fail(w, 400, "invalid_category", "category must be none, subject_1 or subject_2")
		return false
	}
	if (f.FormType == "CE" || f.FormType == "PE") != (f.CEPE != nil) {
		fail(w, 400, "invalid_ce_pe_value", "ce_pe_value is required only for CE and PE")
		return false
	}
	quantity, ok := decimal(f.Quantity, true)
	if !ok || quantity == "0" {
		fail(w, 400, "invalid_quantity", "quantity must be a nonzero decimal string with at most six fractional digits (positive for buy, negative for sell)")
		return false
	}
	rate, ok := decimal(f.Rate, false)
	if !ok {
		fail(w, 400, "invalid_rate", "rate must be a nonnegative decimal string with at most six fractional digits")
		return false
	}
	f.Quantity = quantity
	f.Rate = rate
	if f.CEPE != nil {
		value, ok := decimal(*f.CEPE, true)
		if !ok {
			fail(w, 400, "invalid_ce_pe_value", "ce_pe_value must be a decimal string with at most six fractional digits")
			return false
		}
		*f.CEPE = value
	}
	return true
}

// SQL numeric -> JSON strings preserves decimal precision in Go/web/mobile.
const transactionJSON = `to_jsonb(t) || jsonb_build_object('quantity',t.quantity::text,'rate',t.rate::text,'total_amount',t.total_amount::text,'ce_pe_value',t.ce_pe_value::text)`

func (a *api) createTransaction(w http.ResponseWriter, r *http.Request, user actor) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !validText(key, 200) {
		fail(w, 400, "invalid_idempotency_key", "Idempotency-Key is required (1–200 bytes)")
		return
	}
	var input transactionCreate
	if !decode(w, r, &input) || !input.validate(w) {
		return
	}
	if !optionalUUID(input.WhatsAppAccountID) {
		fail(w, 400, "invalid_sender", "Supply a valid whatsapp_account_id")
		return
	}
	if input.WhatsAppAccountID != nil {
		*input.WhatsAppAccountID = strings.ToLower(*input.WhatsAppAccountID)
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		input.Reason = "Manual entry"
	}
	if !validText(input.Reason, 2048) {
		fail(w, 400, "invalid_reason", "change_reason must contain at most 2048 bytes")
		return
	}
	payload, _ := json.Marshal(input)
	fingerprint := sha256.Sum256(payload)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	var data json.RawMessage
	var createdID string
	err = tx.QueryRowContext(r.Context(), `INSERT INTO ipo_pos.transactions AS t
        (business_id,idempotency_key,origin,created_by_user_id,ipo_id,customer_id,occurred_at,form_type_code,
        category,ce_pe_value,quantity,rate,salesperson_user_id,conversation_id,changed_by_user_id,change_reason)
        VALUES($1,$2,'manual',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$3,$14)
        ON CONFLICT (business_id,idempotency_key) DO NOTHING RETURNING `+transactionJSON+`,t.id`,
		user.BusinessID, key, user.UserID, input.IPOID, input.CustomerID, input.OccurredAt, input.FormType, input.Category, input.CEPE, input.Quantity, input.Rate, input.StaffID, input.ConversationID, input.Reason).Scan(&data, &createdID)
	status := 201
	if errors.Is(err, sql.ErrNoRows) {
		var existingHash []byte
		var creator string
		err = tx.QueryRowContext(r.Context(), `SELECT `+transactionJSON+`,m.payload_hash,m.user_id,t.id FROM ipo_pos.manual_transaction_requests m
            JOIN ipo_pos.transactions t ON t.business_id=m.business_id AND t.id=m.transaction_id
            WHERE m.business_id=$1 AND m.idempotency_key=$2`, user.BusinessID, key).Scan(&data, &existingHash, &creator, &createdID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (!bytes.Equal(existingHash, fingerprint[:]) || creator != user.UserID)) {
			fail(w, 409, "idempotency_conflict", "This key already identifies a different creation request")
			return
		}
		if err != nil {
			dbFailure(w, err)
			return
		}
		status = 200
		w.Header().Set("Idempotency-Replayed", "true")
	} else if err != nil {
		dbFailure(w, err)
		return
	} else {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO ipo_pos.manual_transaction_requests
            (business_id,idempotency_key,transaction_id,user_id,payload_hash) VALUES($1,$2,$3,$4,$5)`, user.BusinessID, key, createdID, user.UserID, fingerprint[:])
		if err != nil {
			dbFailure(w, err)
			return
		}
	}
	if input.WhatsAppAccountID != nil {
		if status == 201 {
			if !queueConfirmation(w, r, tx, user, createdID, *input.WhatsAppAccountID) {
				return
			}
		}
		if err = confirmationResponse(r, tx, user, createdID, &data); err != nil {
			dbFailure(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, status, data)
}

func transactionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !validUUID(id) {
		fail(w, 400, "invalid_id", "Invalid transaction ID")
		return "", false
	}
	return id, true
}

func (a *api) getTransaction(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := transactionID(w, r)
	if !ok {
		return
	}
	queryJSON(w, r, a.db, 200, "SELECT "+transactionJSON+" FROM ipo_pos.transactions t WHERE business_id=$1 AND id=$2", user.BusinessID, id)
}

func (a *api) editTransaction(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := transactionID(w, r)
	if !ok {
		return
	}
	var input transactionEdit
	if !decode(w, r, &input) || !input.validate(w) {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedVersion < 1 || input.ExpectedVersion >= 2147483647 || !validText(input.Reason, 2048) {
		fail(w, 400, "invalid_revision", "Supply expected_version and a nonempty change_reason")
		return
	}
	var data json.RawMessage
	err := a.db.QueryRowContext(r.Context(), `UPDATE ipo_pos.transactions AS t SET
        ipo_id=$4,customer_id=$5,occurred_at=$6,form_type_code=$7,category=$8,ce_pe_value=$9,quantity=$10,rate=$11,
        salesperson_user_id=$12,conversation_id=$13,changed_by_user_id=$14,automation_run_id=NULL,
        version=version+1,change_reason=$15 WHERE business_id=$1 AND id=$2 AND version=$3 RETURNING `+transactionJSON,
		user.BusinessID, id, input.ExpectedVersion, input.IPOID, input.CustomerID, input.OccurredAt, input.FormType, input.Category, input.CEPE, input.Quantity, input.Rate, input.StaffID, input.ConversationID, user.UserID, input.Reason).Scan(&data)
	a.finishUpdate(w, r, user, id, data, err)
}

func (a *api) cancelTransaction(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := transactionID(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int    `json:"expected_version"`
		Reason          string `json:"change_reason"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedVersion < 1 || input.ExpectedVersion >= 2147483647 || !validText(input.Reason, 2048) {
		fail(w, 400, "invalid_revision", "Supply expected_version and a nonempty change_reason")
		return
	}
	var data json.RawMessage
	err := a.db.QueryRowContext(r.Context(), `UPDATE ipo_pos.transactions AS t SET status='cancelled',version=version+1,
        changed_by_user_id=$4,automation_run_id=NULL,change_reason=$5
        WHERE business_id=$1 AND id=$2 AND version=$3 AND status='confirmed' RETURNING `+transactionJSON, user.BusinessID, id, input.ExpectedVersion, user.UserID, input.Reason).Scan(&data)
	a.finishUpdate(w, r, user, id, data, err)
}

func (a *api) finishUpdate(w http.ResponseWriter, r *http.Request, user actor, id string, data json.RawMessage, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err = a.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM ipo_pos.transactions WHERE business_id=$1 AND id=$2)", user.BusinessID, id).Scan(&exists); err != nil {
			dbFailure(w, err)
			return
		}
		if !exists {
			fail(w, 404, "not_found", "Transaction not found")
			return
		}
		fail(w, 409, "version_conflict", "Transaction changed or is already cancelled; fetch the current record before retrying")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, data)
}

func (a *api) listTransactions(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	at, id := p.args()
	q := r.URL.Query()
	var ipo, customer, from, to any
	if s := q.Get("ipo_id"); s != "" {
		if !validUUID(s) {
			fail(w, 400, "invalid_filter", "Invalid ipo_id")
			return
		}
		ipo = s
	}
	if s := q.Get("customer_id"); s != "" {
		if !validUUID(s) {
			fail(w, 400, "invalid_filter", "Invalid customer_id")
			return
		}
		customer = s
	}
	for key, dst := range map[string]*any{"from": &from, "to": &to} {
		if s := q.Get(key); s != "" {
			value, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				fail(w, 400, "invalid_filter", "from/to must be RFC3339 timestamps")
				return
			}
			*dst = value
		}
	}
	if from != nil && to != nil && !from.(time.Time).Before(to.(time.Time)) {
		fail(w, 400, "invalid_filter", "from must be earlier than to")
		return
	}
	status := q.Get("status")
	if status != "" && status != "confirmed" && status != "cancelled" {
		fail(w, 400, "invalid_filter", "Invalid status")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+transactionJSON+`,t.occurred_at,t.id FROM ipo_pos.transactions t
        WHERE business_id=$1 AND ($2::timestamptz IS NULL OR (occurred_at,id)<($2,$3::uuid))
        AND ($4::uuid IS NULL OR ipo_id=$4) AND ($5::uuid IS NULL OR customer_id=$5)
        AND ($6::timestamptz IS NULL OR occurred_at >= $6) AND ($7::timestamptz IS NULL OR occurred_at < $7)
        AND ($8='' OR status=$8) ORDER BY occurred_at DESC,id DESC LIMIT $9`, user.BusinessID, at, id, ipo, customer, from, to, status, p.Limit+1)
	if err != nil {
		dbFailure(w, err)
		return
	}
	paginated(w, rows, p)
}

func (a *api) transactionHistory(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := transactionID(w, r)
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
	if s := r.URL.Query().Get("before_version"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 2147483647 {
			fail(w, 400, "invalid_page", "Invalid before_version")
			return
		}
		before = n
	}
	var exists bool
	if err := a.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM ipo_pos.transactions WHERE business_id=$1 AND id=$2)", user.BusinessID, id).Scan(&exists); err != nil {
		dbFailure(w, err)
		return
	}
	if !exists {
		fail(w, 404, "not_found", "Transaction not found")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+transactionJSON+` FROM ipo_pos.transaction_versions t
        WHERE business_id=$1 AND id=$2 AND ($3::integer IS NULL OR version<$3) ORDER BY version DESC LIMIT $4`, user.BusinessID, id, before, p.Limit+1)
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
		var version struct {
			Version int `json:"version"`
		}
		if err = json.Unmarshal(data, &version); err != nil {
			dbFailure(w, err)
			return
		}
		last = version.Version
		items = append(items, data)
	}
	if err = rows.Err(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_before_version": next})
}

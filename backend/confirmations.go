package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

// Keep protocol session bindings private; return only POS confirmation details.
const confirmationJSON = `to_jsonb(q)-'business_id'-'sender_device_jid'`

func (a *api) getConfirmation(w http.ResponseWriter, r *http.Request, user actor) {
	id, ok := transactionID(w, r)
	if !ok {
		return
	}
	queryJSON(w, r, a.db, 200, `SELECT `+confirmationJSON+` FROM ipo_pos.transaction_confirmations q
        WHERE business_id=$1 AND transaction_id=$2`, user.BusinessID, id)
}

func queueConfirmation(w http.ResponseWriter, r *http.Request, tx *sql.Tx, user actor, id, account string) bool {
	var jid sql.NullString
	err := tx.QueryRowContext(r.Context(), activeWhatsAppAccount, user.BusinessID, account, user.UserID).Scan(&jid)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 403, "invalid_sender", "Use your own active WhatsApp account in this business")
		return false
	}
	if err != nil {
		dbFailure(w, err)
		return false
	}
	if !jid.Valid {
		fail(w, 409, "sender_unpaired", "Pair the selected WhatsApp account first")
		return false
	}
	// Compose from the saved transaction; direction carries the quantity's sign.
	result, err := tx.ExecContext(r.Context(), `INSERT INTO ipo_pos.transaction_confirmations
        (business_id,transaction_id,transaction_version,whatsapp_account_id,created_by_user_id,
        sender_device_jid,recipient_phone_number,body)
        SELECT t.business_id,t.id,t.version,$3,$4,$5,c.phone_number,
        format('%s ipo %s %s %s%s%s at %s',
            replace(replace(i.name,E'\n',' '),E'\r',' '),
            CASE WHEN t.quantity > 0 THEN 'Buy' ELSE 'Sell' END,
            trim_scale(abs(t.quantity))::text,
            t.form_type_code,CASE WHEN t.ce_pe_value IS NULL THEN '' ELSE ' ('||trim_scale(t.ce_pe_value)::text||')' END,
            CASE WHEN t.form_type_code IN ('CE','PE','SHARES') THEN ''
                 WHEN t.category='none' THEN ' alloted' ELSE ' '||replace(t.category,'_',' ') END,
            trim_scale(t.rate)::text)
        FROM ipo_pos.transactions t JOIN ipo_pos.customers c ON c.business_id=t.business_id AND c.id=t.customer_id
        JOIN ipo_pos.ipos i ON i.id=t.ipo_id WHERE t.business_id=$1 AND t.id=$2 AND c.phone_number IS NOT NULL`,
		user.BusinessID, id, account, user.UserID, jid.String)
	if err != nil {
		dbFailure(w, err)
		return false
	}
	n, err := result.RowsAffected()
	if err != nil {
		dbFailure(w, err)
		return false
	}
	if n != 1 {
		fail(w, 409, "recipient_missing", "Save an international phone number on the selected customer first")
		return false
	}
	return true
}

func confirmationResponse(r *http.Request, tx *sql.Tx, user actor, id string, data *json.RawMessage) error {
	return tx.QueryRowContext(r.Context(), `SELECT $1::jsonb || jsonb_build_object('whatsapp_confirmation',`+confirmationJSON+`)
        FROM ipo_pos.transaction_confirmations q WHERE q.business_id=$2 AND q.transaction_id=$3`,
		string(*data), user.BusinessID, id).Scan(data)
}

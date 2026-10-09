package main

import (
	"net/http"
	"strings"
)

// A persisted binding says paired, never that a client is currently connected.
const whatsappAccountJSON = `jsonb_build_object('id',wa.id,'business_id',wa.business_id,
    'label',wa.label,'phone_number',wa.phone_number,'linked_by_user_id',wa.linked_by_user_id,
    'created_at',wa.created_at,'unlinked_at',wa.unlinked_at,'pairing_state',
    CASE WHEN wa.unlinked_at IS NOT NULL THEN 'unlinked'
         WHEN wa.device_jid IS NULL THEN 'unpaired' ELSE 'paired' END)`

func (a *api) createWhatsAppAccount(w http.ResponseWriter, r *http.Request, user actor) {
	// shortcut: each POST creates an account; add replay protection when the pairing UI retries submissions.
	var input struct {
		Label string `json:"label"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Label = strings.TrimSpace(input.Label)
	if !validText(input.Label, 256) {
		fail(w, 400, "invalid_account", "Supply a WhatsApp account label of 1–256 bytes")
		return
	}
	queryJSON(w, r, a.db, 201, `INSERT INTO ipo_pos.whatsapp_accounts AS wa
        (business_id,label,linked_by_user_id) VALUES($1,$2,$3) RETURNING `+whatsappAccountJSON,
		user.BusinessID, input.Label, user.UserID)
}

func (a *api) listWhatsAppAccounts(w http.ResponseWriter, r *http.Request, user actor) {
	p, ok := readPage(w, r)
	if !ok {
		return
	}
	at, id := p.args()
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+whatsappAccountJSON+`,wa.created_at,wa.id
        FROM ipo_pos.whatsapp_accounts wa WHERE business_id=$1
        AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3::uuid))
        ORDER BY created_at DESC,id DESC LIMIT $4`, user.BusinessID, at, id, p.Limit+1)
	if err != nil {
		dbFailure(w, err)
		return
	}
	paginated(w, rows, p)
}

func (a *api) getWhatsAppAccount(w http.ResponseWriter, r *http.Request, user actor) {
	id := r.PathValue("id")
	if !validUUID(id) {
		fail(w, 400, "invalid_id", "Invalid WhatsApp account ID")
		return
	}
	queryJSON(w, r, a.db, 200, "SELECT "+whatsappAccountJSON+" FROM ipo_pos.whatsapp_accounts wa WHERE business_id=$1 AND id=$2",
		user.BusinessID, id)
}

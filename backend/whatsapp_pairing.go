package main

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"rsc.io/qr"
)

func (a *api) disconnectWhatsAppAccount(w http.ResponseWriter, r *http.Request, user actor) {
	id, jid, ok := a.ownedWhatsAppAccount(w, r, user)
	if !ok {
		return
	}
	// Revoke locally first, even when the network cannot confirm remote device removal.
	if jid.Valid {
		if err := a.whatsapp.clearBinding(r.Context(), user, id, jid.String); err != nil {
			dbFailure(w, err)
			return
		}
	}
	a.whatsapp.mu.Lock()
	c := a.whatsapp.accounts[strings.ToLower(user.BusinessID+"/"+id)]
	a.whatsapp.mu.Unlock()
	remote := false
	if c != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
		defer cancel()
		result := make(chan error, 1)
		select {
		case c.logout <- result:
			select {
			case err := <-result:
				remote = err == nil
			case <-ctx.Done():
			}
		case <-c.done:
		case <-ctx.Done():
		}
	}
	writeJSON(w, 200, map[string]any{"state": "needs_relink", "remote_unlink_confirmed": remote})
}

func (a *api) ownedWhatsAppAccount(w http.ResponseWriter, r *http.Request, user actor) (string, sql.NullString, bool) {
	id := r.PathValue("id")
	if !validUUID(id) {
		fail(w, 400, "invalid_id", "Invalid WhatsApp account ID")
		return "", sql.NullString{}, false
	}
	var owner string
	var jid sql.NullString
	if err := a.db.QueryRowContext(r.Context(), "SELECT linked_by_user_id,device_jid FROM ipo_pos.whatsapp_accounts WHERE business_id=$1 AND id=$2", user.BusinessID, id).Scan(&owner, &jid); err != nil {
		dbFailure(w, err)
		return "", jid, false
	}
	if owner != user.UserID {
		fail(w, 403, "forbidden", "Only the registering staff member can manage this connection")
		return "", jid, false
	}
	if a.whatsapp == nil {
		fail(w, 503, "whatsapp_unavailable", "Configure WHATSAPP_DATABASE_URL to enable WhatsApp connections")
		return "", jid, false
	}
	return strings.ToLower(id), jid, true
}

func (a *api) pairWhatsAppAccount(w http.ResponseWriter, r *http.Request, user actor) {
	id, jid, ok := a.ownedWhatsAppAccount(w, r, user)
	if !ok {
		return
	}
	var input struct {
		Method string `json:"method"`
		Phone  string `json:"phone_number"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Method == "" {
		input.Method = "qr"
	}
	if (input.Method != "qr" && input.Method != "phone") || (input.Method == "phone" && !phonePattern.MatchString(input.Phone)) || (input.Method == "qr" && input.Phone != "") {
		fail(w, 400, "invalid_pairing", "Choose qr, or phone with an international phone_number")
		return
	}
	if jid.Valid {
		fail(w, 409, "already_paired", "Use connect for the saved session; revoke it in WhatsApp before relinking")
		return
	}
	a.startWhatsApp(w, user, id, input.Method, input.Phone)
}

func (a *api) connectWhatsAppAccount(w http.ResponseWriter, r *http.Request, user actor) {
	id, jid, ok := a.ownedWhatsAppAccount(w, r, user)
	if !ok {
		return
	}
	if !jid.Valid {
		fail(w, 409, "needs_relink", "Pair this account before connecting")
		return
	}
	a.startWhatsApp(w, user, id, "", "")
}

func (a *api) startWhatsApp(w http.ResponseWriter, user actor, id, method, phone string) {
	_, err := a.whatsapp.start(user, id, method, phone)
	if err != nil {
		fail(w, 409, err.Error(), "Connection is busy, at capacity or stopping; inspect connection state before retrying")
		return
	}
	// Codes are fetched separately, so a POST retry response cannot expose old secrets.
	writeJSON(w, 202, map[string]string{"account_id": id, "state": "accepted"})
}

func (a *api) whatsAppConnection(w http.ResponseWriter, r *http.Request, user actor) {
	id, jid, ok := a.ownedWhatsAppAccount(w, r, user)
	if !ok {
		return
	}
	state, found := a.whatsapp.state(user.BusinessID, id)
	if !found {
		state.State = "unpaired"
		if jid.Valid {
			state.State = "disconnected"
		}
	}
	writeJSON(w, 200, state)
}

func (a *api) whatsAppQR(w http.ResponseWriter, r *http.Request, user actor) {
	id, _, ok := a.ownedWhatsAppAccount(w, r, user)
	if !ok {
		return
	}
	state, found := a.whatsapp.state(user.BusinessID, id)
	if !found || state.State != "awaiting_qr" || state.Code == "" || !time.Now().Before(state.ExpiresAt) {
		fail(w, 409, "qr_unavailable", "No current QR code; inspect connection state")
		return
	}
	code, err := qr.Encode(state.Code, qr.M)
	if err != nil {
		fail(w, 500, "qr_failed", "Could not render the QR code")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(code.PNG())
}

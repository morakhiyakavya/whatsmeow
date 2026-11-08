package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type recipientPreparation struct {
	ctx    context.Context
	phone  string
	result chan error
}

func (a *api) whatsAppPrepareRecipient(w http.ResponseWriter, r *http.Request, user actor) {
	id, jid, ok := a.ownedWhatsAppAccount(w, r, user)
	if !ok {
		return
	}
	var input struct {
		Phone string `json:"phone_number"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !phonePattern.MatchString(input.Phone) {
		fail(w, 400, "invalid_phone", "Supply phone_number in international format, including + and country code")
		return
	}
	if !jid.Valid {
		fail(w, 409, "needs_relink", "Pair this account before preparing a recipient")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	a.whatsapp.mu.Lock()
	c := a.whatsapp.accounts[strings.ToLower(user.BusinessID+"/"+id)]
	a.whatsapp.mu.Unlock()
	if c == nil || c.state().State != "connected" {
		fail(w, 409, "whatsapp_not_connected", "Connect this account before preparing a recipient")
		return
	}
	request := recipientPreparation{ctx: ctx, phone: input.Phone, result: make(chan error, 1)}
	var err error
	select {
	case c.preparations <- request:
		select {
		case err = <-request.result:
		case <-c.done:
			err = errProfileUnavailable
		case <-ctx.Done():
			err = ctx.Err()
		}
	case <-c.done:
		err = errProfileUnavailable
	case <-ctx.Done():
		err = ctx.Err()
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		fail(w, 504, "whatsapp_prepare_timeout", "Recipient preparation did not complete")
	case err != nil:
		fail(w, 502, "whatsapp_prepare_failed", "Could not prepare this recipient")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func prepareRecipient(ctx context.Context, client pairingClient, device *store.Device, phone string) (resultErr error) {
	span := startOperation(ctx, "whatsapp.prepare", "whatsapp.prepare_recipient")
	defer func() { finishOperation(span, resultErr) }()
	ctx = span.Context()
	jid := types.NewJID(strings.TrimPrefix(phone, "+"), types.DefaultUserServer)
	lid, err := device.LIDs.GetLIDForPN(ctx, jid)
	if err != nil {
		return err
	}
	if lid.IsEmpty() {
		info, err := client.GetUserInfo(ctx, []types.JID{jid})
		if err != nil {
			return err
		}
		lid = info[jid].LID
		if lid.IsEmpty() {
			return errors.New("recipient has no WhatsApp identity")
		}
	}
	own := device.GetLID().ToNonAD()
	if own.IsEmpty() {
		own = device.GetJID().ToNonAD()
	}
	_, err = client.GetUserDevices(ctx, []types.JID{lid, own})
	return err
}

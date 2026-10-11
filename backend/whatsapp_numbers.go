package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

var errNumberCheckUnavailable = errors.New("WhatsApp account is not connected")
var errNumberCheckStorage = errors.New("could not store WhatsApp number check")

type numberCheck struct {
	ctx    context.Context
	phone  string
	result chan numberCheckResult
}

type numberCheckResult struct {
	registered bool
	checkedAt  time.Time
	err        error
}

func (a *api) checkWhatsAppNumber(w http.ResponseWriter, r *http.Request, user actor) {
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
		fail(w, 409, "needs_relink", "Pair this account before checking a number")
		return
	}
	result := a.whatsapp.checkNumber(r.Context(), user.BusinessID, id, input.Phone)
	switch {
	case errors.Is(result.err, errNumberCheckUnavailable):
		fail(w, 409, "whatsapp_not_connected", "Connect this account before checking a number")
	case errors.Is(result.err, context.DeadlineExceeded), errors.Is(result.err, context.Canceled):
		fail(w, 504, "whatsapp_check_timeout", "Number check did not complete; registration is unknown")
	case errors.Is(result.err, errNumberCheckStorage):
		fail(w, 503, "whatsapp_check_storage_failed", "Could not save the registration result; retry the check")
	case result.err != nil:
		fail(w, 502, "whatsapp_check_failed", "Could not check the number; registration is unknown")
	default:
		writeJSON(w, 200, map[string]any{"phone_number": input.Phone, "registered": result.registered, "checked_at": result.checkedAt})
	}
}

func (s *whatsAppService) checkNumber(ctx context.Context, businessID, id, phone string) numberCheckResult {
	s.mu.Lock()
	c := s.accounts[strings.ToLower(businessID+"/"+id)]
	s.mu.Unlock()
	if c == nil || c.state().State != "connected" {
		return numberCheckResult{err: errNumberCheckUnavailable}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	request := numberCheck{ctx: ctx, phone: phone, result: make(chan numberCheckResult, 1)}
	select {
	case c.numberChecks <- request:
	case <-c.done:
		return numberCheckResult{err: errNumberCheckUnavailable}
	case <-ctx.Done():
		return numberCheckResult{err: ctx.Err()}
	}
	select {
	case result := <-request.result:
		return result
	case <-c.done:
		return numberCheckResult{err: errNumberCheckUnavailable}
	case <-ctx.Done():
		return numberCheckResult{err: ctx.Err()}
	}
}

func checkRegisteredNumber(ctx context.Context, client pairingClient, phone string) (bool, error) {
	results, err := client.IsOnWhatsApp(ctx, []string{phone})
	if err != nil {
		return false, err
	}
	// Missing or mismatched results cannot establish that a number is unregistered.
	if len(results) != 1 || strings.TrimPrefix(results[0].Query, "+") != strings.TrimPrefix(phone, "+") {
		return false, errors.New("incomplete WhatsApp number check")
	}
	return results[0].IsIn, nil
}

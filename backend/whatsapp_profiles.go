package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

var errProfileUnavailable = errors.New("WhatsApp account is not connected")

type contactProfile struct {
	Phone        string  `json:"phone_number"`
	Name         *string `json:"name"`
	About        *string `json:"about"`
	PictureURL   *string `json:"picture_url"`
	PictureState string  `json:"picture_state"`
}

type profileRequest struct {
	ctx    context.Context
	phone  string
	result chan profileResult
}

type profileResult struct {
	profile contactProfile
	err     error
}

func (a *api) whatsAppContactProfile(w http.ResponseWriter, r *http.Request, user actor) {
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
		fail(w, 409, "needs_relink", "Pair this account before viewing a contact profile")
		return
	}
	result := a.whatsapp.contactProfile(r.Context(), user.BusinessID, id, input.Phone)
	switch {
	case errors.Is(result.err, errProfileUnavailable):
		fail(w, 409, "whatsapp_not_connected", "Connect this account before viewing a contact profile")
	case errors.Is(result.err, context.DeadlineExceeded), errors.Is(result.err, context.Canceled):
		fail(w, 504, "whatsapp_profile_timeout", "Contact profile lookup did not complete")
	case result.err != nil:
		fail(w, 502, "whatsapp_profile_failed", "Could not retrieve the contact profile")
	default:
		writeJSON(w, 200, result.profile)
	}
}

func (s *whatsAppService) contactProfile(ctx context.Context, businessID, id, phone string) profileResult {
	s.mu.Lock()
	c := s.accounts[strings.ToLower(businessID+"/"+id)]
	s.mu.Unlock()
	if c == nil || c.state().State != "connected" {
		return profileResult{err: errProfileUnavailable}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	request := profileRequest{ctx: ctx, phone: phone, result: make(chan profileResult, 1)}
	select {
	case c.profiles <- request:
	case <-c.done:
		return profileResult{err: errProfileUnavailable}
	case <-ctx.Done():
		return profileResult{err: ctx.Err()}
	}
	select {
	case result := <-request.result:
		return result
	case <-c.done:
		return profileResult{err: errProfileUnavailable}
	case <-ctx.Done():
		return profileResult{err: ctx.Err()}
	}
}

func lookupContactProfile(ctx context.Context, client pairingClient, contacts store.ContactStore, phone string) (contactProfile, error) {
	profile := contactProfile{Phone: phone}
	jid := types.NewJID(strings.TrimPrefix(phone, "+"), types.DefaultUserServer)
	users, err := client.GetUserInfo(ctx, []types.JID{jid})
	if err != nil {
		return profile, err
	}
	info, ok := users[jid]
	if !ok {
		return profile, errors.New("incomplete WhatsApp user info")
	}
	if info.Status != "" {
		profile.About = &info.Status
	}
	// Push names may be synced under either the phone JID or its privacy LID.
	var businessName string
	for _, contactJID := range []types.JID{jid, info.LID} {
		if contactJID.IsEmpty() {
			continue
		}
		contact, err := contacts.GetContact(ctx, contactJID)
		if err != nil {
			return profile, err
		}
		if contact.PushName != "" && profile.Name == nil {
			profile.Name = &contact.PushName
		}
		if businessName == "" {
			businessName = contact.BusinessName
		}
	}
	if profile.Name == nil && businessName != "" {
		profile.Name = &businessName
	}
	picture, err := client.GetProfilePictureInfo(ctx, jid, nil)
	switch {
	case errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized):
		profile.PictureState = "hidden"
	case errors.Is(err, whatsmeow.ErrProfilePictureNotSet):
		profile.PictureState = "not_set"
	case err != nil:
		return profile, err
	case picture == nil || picture.URL == "":
		return profile, errors.New("incomplete WhatsApp profile picture")
	default:
		profile.PictureURL, profile.PictureState = &picture.URL, "available"
	}
	return profile, nil
}

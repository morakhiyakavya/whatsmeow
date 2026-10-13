package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type profileContacts struct {
	store.ContactStore
	entries map[types.JID]types.ContactInfo
	err     error
}

func (c profileContacts) GetContact(_ context.Context, jid types.JID) (types.ContactInfo, error) {
	return c.entries[jid], c.err
}

func TestLookupContactProfile(t *testing.T) {
	const phone = "+919000001010"
	jid := types.NewJID(phone[1:], types.DefaultUserServer)
	lid := types.NewJID("123456", types.HiddenUserServer)
	for _, test := range []struct {
		name, pictureState string
		picture            *types.ProfilePictureInfo
		err                error
		failed             bool
	}{
		{"available", "available", &types.ProfilePictureInfo{URL: "https://example.com/photo.jpg"}, nil, false},
		{"hidden", "hidden", nil, fmt.Errorf("wrapped: %w", whatsmeow.ErrProfilePictureUnauthorized), false},
		{"unset", "not_set", nil, whatsmeow.ErrProfilePictureNotSet, false},
		{"missing", "", nil, nil, true},
		{"empty URL", "", &types.ProfilePictureInfo{}, nil, true},
		{"network failure", "", nil, errors.New("private failure"), true},
		{"timeout", "", nil, context.DeadlineExceeded, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePairingClient{
				userInfo: func(_ context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error) {
					if len(jids) != 1 || jids[0] != jid {
						t.Fatalf("wrong profile query: %v", jids)
					}
					return map[types.JID]types.UserInfo{jid: {Status: "Available", LID: lid}}, nil
				},
				pictureInfo: func(_ context.Context, got types.JID, params *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
					if got != jid || params != nil {
						t.Fatal("wrong picture query")
					}
					return test.picture, test.err
				},
			}
			contacts := profileContacts{entries: map[types.JID]types.ContactInfo{
				jid: {FullName: "Local alias", BusinessName: "Business"}, lid: {PushName: "WhatsApp name"},
			}}
			got, err := lookupContactProfile(context.Background(), client, contacts, phone)
			if (err != nil) != test.failed || got.PictureState != test.pictureState {
				t.Fatalf("profile=%+v error=%v", got, err)
			}
			if got.Phone != phone || got.Name == nil || *got.Name != "WhatsApp name" || got.About == nil || *got.About != "Available" {
				t.Fatalf("wrong profile details: %+v", got)
			}
			if (got.PictureURL != nil) != (test.pictureState == "available") {
				t.Fatal("picture URL availability is inconsistent")
			}
		})
	}
	client := &fakePairingClient{
		userInfo: func(context.Context, []types.JID) (map[types.JID]types.UserInfo, error) {
			return map[types.JID]types.UserInfo{jid: {}}, nil
		},
		pictureInfo: func(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
			return nil, whatsmeow.ErrProfilePictureNotSet
		},
	}
	for _, name := range []string{"", "Business"} {
		got, err := lookupContactProfile(context.Background(), client, profileContacts{entries: map[types.JID]types.ContactInfo{jid: {FullName: "Local alias", BusinessName: name}}}, phone)
		if err != nil || got.About != nil || (got.Name == nil) != (name == "") || (got.Name != nil && *got.Name != name) {
			t.Fatalf("wrong nullable details or business fallback: %+v, %v", got, err)
		}
	}
	if _, err := lookupContactProfile(context.Background(), client, profileContacts{err: errors.New("storage error")}, phone); err == nil {
		t.Fatal("ignored contact storage error")
	}
	for _, upstreamErr := range []error{nil, errors.New("upstream failure")} {
		client.userInfo = func(context.Context, []types.JID) (map[types.JID]types.UserInfo, error) {
			return map[types.JID]types.UserInfo{lid: {}}, upstreamErr
		}
		if _, err := lookupContactProfile(context.Background(), client, profileContacts{}, phone); err == nil {
			t.Fatal("missing or mismatched user info was accepted")
		}
	}
}

func testWhatsAppProfiles(t *testing.T, owner, other testClient, s *whatsAppService, client *fakePairingClient, path string) {
	t.Helper()
	const phone = "+919000001010"
	parts := strings.Split(path, "/")
	businessID, id := parts[3], parts[5]
	path += "/contact-profile"
	input := map[string]string{"phone_number": phone}
	public := owner
	public.Token = ""
	status, object, _ := public.request("POST", path, input, "")
	expectStatus(t, 401, status, object)
	status, object, _ = other.request("POST", path, input, "")
	expectStatus(t, 403, status, object)
	status, object, _ = owner.request("POST", strings.Replace(path, "/whatsapp-accounts/", "/whatsapp-accounts/bad-", 1), input, "")
	expectStatus(t, 400, status, object)
	for _, invalid := range []map[string]string{{}, {"phone_number": "919000001010"}, {"phone_number": "+0919000001010"}, {"phone_number": phone, "picture_url": "forged"}} {
		status, object, _ = owner.request("POST", path, invalid, "")
		expectStatus(t, 400, status, object)
	}
	jid := types.NewJID(phone[1:], types.DefaultUserServer)
	if _, _, err := client.device.Contacts.PutPushName(context.Background(), jid, "Profile name"); err != nil {
		t.Fatal(err)
	}
	client.userInfo = func(context.Context, []types.JID) (map[types.JID]types.UserInfo, error) {
		return map[types.JID]types.UserInfo{jid: {Status: "About me"}}, nil
	}
	for _, pictureErr := range []error{nil, whatsmeow.ErrProfilePictureUnauthorized, whatsmeow.ErrProfilePictureNotSet} {
		client.pictureInfo = func(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
			return &types.ProfilePictureInfo{URL: "https://example.com/photo.jpg"}, pictureErr
		}
		status, object, headers := owner.request("POST", path, input, "")
		expectStatus(t, 200, status, object)
		if headers.Get("Cache-Control") != "no-store" {
			t.Fatal("profile response permits caching")
		}
		if object["name"] != "Profile name" || object["about"] != "About me" || object["phone_number"] != phone {
			t.Fatalf("wrong profile: %v", object)
		}
		want := "available"
		if pictureErr == whatsmeow.ErrProfilePictureUnauthorized {
			want = "hidden"
		} else if pictureErr == whatsmeow.ErrProfilePictureNotSet {
			want = "not_set"
		}
		if object["picture_state"] != want || (object["picture_url"] != nil) != (pictureErr == nil) {
			t.Fatal("hidden/unset picture exposed a URL or was reported as available")
		}
	}
	for _, upstreamErr := range []error{errors.New("private upstream details"), context.DeadlineExceeded} {
		client.pictureInfo = func(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
			return nil, upstreamErr
		}
		status, object, _ = owner.request("POST", path, input, "")
		want := 502
		if upstreamErr == context.DeadlineExceeded {
			want = 504
		}
		expectStatus(t, want, status, object)
		raw, _ := json.Marshal(object)
		if object["name"] != nil || strings.Contains(string(raw), "private upstream details") {
			t.Fatal("failed lookup exposed partial profile or upstream details")
		}
	}
	finished := make(chan struct{})
	client.userInfo = func(ctx context.Context, _ []types.JID) (map[types.JID]types.UserInfo, error) {
		defer close(finished)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if result := s.contactProfile(ctx, businessID, id, phone); !errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatalf("profile lookup ignored cancellation: %v", result.err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled profile lookup held the worker")
	}
	client.userInfo, client.pictureInfo = nil, nil
	status, object, _ = owner.request("POST", path, input, "")
	expectStatus(t, 502, status, object)
	s.mu.Lock()
	c := s.accounts[strings.ToLower(businessID+"/"+id)]
	s.mu.Unlock()
	c.update("reconnecting", "", time.Time{})
	status, object, _ = owner.request("POST", path, input, "")
	expectStatus(t, 409, status, object)
	c.update("connected", "", time.Time{})
	// The worker must revalidate ownership/membership even after request authorization.
	var ownerID string
	if err := s.db.QueryRow("SELECT linked_by_user_id FROM ipo_pos.whatsapp_accounts WHERE business_id=$1 AND id=$2", businessID, id).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE business_id=$1 AND user_id=$2", businessID, ownerID); err != nil {
		t.Fatal(err)
	}
	result := s.contactProfile(context.Background(), businessID, id, phone)
	if _, err := s.db.Exec("UPDATE ipo_pos.business_memberships SET disabled_at=NULL WHERE business_id=$1 AND user_id=$2", businessID, ownerID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(result.err, errProfileUnavailable) {
		t.Fatalf("disabled membership reached profile lookup: %v", result.err)
	}
}

func TestContactProfileUnavailable(t *testing.T) {
	for _, c := range []*accountConnection{nil, {connectionState: connectionState{State: "connected"}, done: make(chan struct{})}} {
		if c != nil {
			close(c.done)
		}
		s := &whatsAppService{accounts: map[string]*accountConnection{"business/account": c}}
		if result := s.contactProfile(context.Background(), "business", "account", "+919000001010"); !errors.Is(result.err, errProfileUnavailable) {
			t.Fatalf("unavailable worker was accepted: %v", result.err)
		}
	}
}

func TestContactProfileWaiting(t *testing.T) {
	c := &accountConnection{connectionState: connectionState{State: "connected"}, profiles: make(chan profileRequest), done: make(chan struct{})}
	s := &whatsAppService{accounts: map[string]*accountConnection{"business/account": c}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if result := s.contactProfile(ctx, "business", "account", "+919000001010"); !errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatalf("queued profile lookup ignored its deadline: %v", result.err)
	}
	// A stopped worker also releases a caller already waiting for a result.
	finished := make(chan profileResult, 1)
	go func() { finished <- s.contactProfile(context.Background(), "business", "account", "+919000001010") }()
	select {
	case <-c.profiles:
	case <-time.After(time.Second):
		t.Fatal("profile request did not reach the worker")
	}
	close(c.done)
	select {
	case result := <-finished:
		if !errors.Is(result.err, errProfileUnavailable) {
			t.Fatalf("stopped worker returned %v", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("stopped worker held the caller")
	}
}

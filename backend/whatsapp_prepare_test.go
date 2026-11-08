package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type preparationLIDs struct {
	store.LIDStore
	lid types.JID
	err error
}

func (s preparationLIDs) GetLIDForPN(context.Context, types.JID) (types.JID, error) {
	return s.lid, s.err
}

func TestPrepareRecipient(t *testing.T) {
	const phone = "+919000001010"
	jid := types.NewJID(phone[1:], types.DefaultUserServer)
	lid := types.NewJID("123456", types.HiddenUserServer)
	own := types.NewJID("654321", types.HiddenUserServer)
	own.Device = 7
	for _, kind := range []string{"cached", "lookup", "missing", "lookup failure", "storage failure", "device failure"} {
		t.Run(kind, func(t *testing.T) {
			device := &store.Device{LID: own, LIDs: preparationLIDs{lid: lid}}
			if kind != "cached" {
				device.LIDs = preparationLIDs{}
			}
			if kind == "storage failure" {
				device.LIDs = preparationLIDs{err: errors.New("storage unavailable")}
			}
			lookups, devices := 0, 0
			client := &fakePairingClient{
				userInfo: func(_ context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error) {
					lookups++
					if len(jids) != 1 || jids[0] != jid {
						t.Fatal("prepared a different recipient")
					}
					if kind == "missing" {
						return nil, nil
					}
					if kind == "lookup failure" {
						return nil, errors.New("lookup failed")
					}
					return map[types.JID]types.UserInfo{jid: {LID: lid}}, nil
				},
				userDevices: func(_ context.Context, jids []types.JID) ([]types.JID, error) {
					devices++
					if len(jids) != 2 || jids[0] != lid || jids[1] != own.ToNonAD() {
						t.Fatal("did not prepare recipient and sender devices")
					}
					if kind == "device failure" {
						return nil, errors.New("device lookup failed")
					}
					return jids, nil
				},
			}
			err := prepareRecipient(context.Background(), client, device, phone)
			if (err != nil) != (kind != "cached" && kind != "lookup") {
				t.Fatalf("unexpected preparation outcome: %v", err)
			}
			if kind == "cached" && lookups != 0 || kind == "storage failure" && lookups != 0 {
				t.Fatal("unnecessary recipient lookup")
			}
			if (kind == "missing" || strings.HasSuffix(kind, "failure") && kind != "device failure") && devices != 0 {
				t.Fatal("device lookup continued after preparation failed")
			}
		})
	}
}

func testWhatsAppPreparation(t *testing.T, owner, other testClient, client *fakePairingClient, path string) {
	t.Helper()
	path += "/prepare-recipient"
	const phone = "+919000001010"
	input := map[string]string{"phone_number": phone}
	public := owner
	public.Token = ""
	status, body, _ := public.request("POST", path, input, "")
	expectStatus(t, 401, status, body)
	status, body, _ = other.request("POST", path, input, "")
	expectStatus(t, 403, status, body)
	for _, input := range []map[string]string{{}, {"phone_number": "919000001010"}, {"phone_number": phone, "body": "forged"}} {
		status, body, _ = owner.request("POST", path, input, "")
		expectStatus(t, 400, status, body)
	}
	client.userInfo = func(context.Context, []types.JID) (map[types.JID]types.UserInfo, error) {
		jid := types.NewJID(phone[1:], types.DefaultUserServer)
		return map[types.JID]types.UserInfo{jid: {LID: types.NewJID("123456", types.HiddenUserServer)}}, nil
	}
	client.userDevices = func(context.Context, []types.JID) ([]types.JID, error) { return nil, nil }
	status, body, _ = owner.request("POST", path, input, "")
	expectStatus(t, 204, status, body)
	client.userDevices = func(context.Context, []types.JID) ([]types.JID, error) {
		return nil, errors.New("private upstream details")
	}
	status, body, _ = owner.request("POST", path, input, "")
	expectStatus(t, 502, status, body)
	if strings.Contains(fmt.Sprint(body), "private upstream details") {
		t.Fatal("preparation exposed upstream details")
	}
}

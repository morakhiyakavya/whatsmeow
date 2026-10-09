package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
)

func TestContactNamesAndPhoneMappings(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "contacts.db")) + "?_pragma=foreign_keys(1)"
	container, err := sqlstore.New(ctx, "sqlite", dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Close() })
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	own := types.NewJID("919000000001", types.DefaultUserServer)
	device.ID = &own
	device.LID = types.NewJID("100000000000001", types.HiddenUserServer)
	device.PushName = "My profile"
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details: []byte{}, AccountSignature: make([]byte, 64),
		AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64),
	}
	if err := device.Save(ctx); err != nil {
		t.Fatal(err)
	}
	pn := types.NewJID("919000000002", types.DefaultUserServer)
	lid := types.NewJID("100000000000002", types.HiddenUserServer)
	if err := device.LIDs.PutLIDMapping(ctx, lid, pn); err != nil {
		t.Fatal(err)
	}
	if err := device.Contacts.PutAllContactNames(ctx, []store.ContactEntry{{JID: pn, FirstName: "Alice", FullName: "Alice Example"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := device.Contacts.PutPushName(ctx, lid, "Alice's profile"); err != nil {
		t.Fatal(err)
	}
	unmapped := types.NewJID("100000000000003", types.HiddenUserServer)
	for _, tc := range []struct {
		label                  string
		jid, alt               types.JID
		fallback, name, number string
	}{
		{"phone ID", pn, types.EmptyJID, "Profile", "Alice Example", "+919000000002"},
		{"mapped LID", lid, types.EmptyJID, "Profile", "Alice Example", "+919000000002"},
		{"alternate phone", unmapped, pn, "Profile", "Alice Example", "+919000000002"},
		{"unmapped LID", unmapped, types.EmptyJID, "Public profile", "Public profile", "unavailable"},
		{"missing name", unmapped, types.EmptyJID, "", "Unknown contact", "unavailable"},
		{"own phone", own, types.EmptyJID, "Wrong name", "My profile", "+919000000001"},
		{"own LID", device.LID, types.EmptyJID, "Wrong name", "My profile", "+919000000001"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			name, number := resolvePerson(ctx, device, tc.jid, tc.alt, tc.fallback)
			if name != tc.name || number != tc.number {
				t.Fatalf("got %q/%q; want %q/%q", name, number, tc.name, tc.number)
			}
		})
	}
	var out bytes.Buffer
	outgoing := viewerMessage(lid, 2)
	outgoing.Info.Sender = device.LID
	outgoing.Info.RecipientAlt = pn
	outgoing.Info.PushName = "Recipient name must not become sender"
	viewer := newMessageViewer(ctx, &out, device)
	viewer.live(outgoing)
	for _, want := range []string{`chat="Alice Example (+919000000002)"`, `sender="My profile"`, `number="+919000000001"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("outgoing identities were mixed up: %q; missing %q", out.String(), want)
		}
	}

	// Contact/mapping sync may finish after a message was first buffered.
	out.Reset()
	viewer = newMessageViewer(ctx, &out, device)
	first := viewerMessage(unmapped, 1)
	first.Info.PushName = "Public profile"
	viewer.live(first)
	if err := device.LIDs.PutLIDMapping(ctx, unmapped, pn); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	viewer.live(viewerMessage(unmapped, 3))
	if strings.Count(out.String(), `sender="Alice Example"`) != 2 || strings.Contains(out.String(), `number="unavailable"`) {
		t.Fatalf("buffered identities did not refresh after mapping sync: %q", out.String())
	}
}

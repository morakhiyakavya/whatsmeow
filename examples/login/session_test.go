package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
)

func TestSessionConfig(t *testing.T) {
	for _, dsn := range []string{"", "   ", "postgres://user:secret-password@localhost:bad-port/db"} {
		_, err := sessionConfig(dsn)
		if err == nil || strings.Contains(err.Error(), "secret-password") {
			t.Fatalf("expected a credential-safe configuration error, got %v", err)
		}
	}
	config, err := sessionConfig("postgres://user:secret-password@localhost/db?sslmode=disable&search_path=public")
	if err != nil {
		t.Fatal(err)
	}
	if config.RuntimeParams["search_path"] != "whatsapp" || config.ConnectTimeout != 10*time.Second {
		t.Fatal("session connections must use the dedicated schema and a connection timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openSessionStore(ctx, "postgres://user:secret-password@127.0.0.1:1/db?sslmode=disable"); err == nil || strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("expected a cancelled, credential-safe setup error, got %v", err)
	}
}

func TestPostgresSessionPersistence(t *testing.T) {
	dsn := os.Getenv("WHATSAPP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("run database/Test-Schema.ps1 -WithSessionTests for isolated PostgreSQL integration checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	container, err := openSessionStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// The runner supplies a new empty cluster; never reuse real account fixtures.
	devices, err := container.GetAllDevices(ctx)
	if err != nil || len(devices) != 0 {
		_ = container.Close()
		t.Fatal("session integration test requires a fresh, empty test database")
	}
	t.Cleanup(func() {
		if container != nil {
			_ = container.Close()
		}
	})
	first, err := selectSession(ctx, container, false, "")
	if err != nil || first.ID != nil {
		t.Fatalf("empty store should prepare a new session: %v", err)
	}
	firstID := types.NewJID("999000000001", types.DefaultUserServer)
	firstID.Device = 7
	first.ID = &firstID
	first.Account = &waAdv.ADVSignedDeviceIdentity{
		Details: []byte{}, AccountSignature: make([]byte, 64),
		AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64),
	}
	if err := first.Save(ctx); err != nil {
		t.Fatal(err)
	}
	firstKey := *first.NoiseKey.Priv
	if selected, err := selectSession(ctx, container, false, ""); err != nil || selected.GetJID() != firstID {
		t.Fatalf("one stored account should resume that account: %v", err)
	}
	second, err := selectSession(ctx, container, true, "")
	if err != nil || second.ID != nil {
		t.Fatalf("fresh pairing must not reuse a saved account: %v", err)
	}
	secondID := types.NewJID("999000000002", types.DefaultUserServer)
	secondID.Device = 8
	second.ID = &secondID
	second.Account = first.Account
	if err := second.Save(ctx); err != nil {
		t.Fatal(err)
	}
	peer := types.NewJID("999000000003", types.DefaultUserServer)
	if err := first.Contacts.PutContactName(ctx, peer, "Alice", "Alice for account one"); err != nil {
		t.Fatal(err)
	}
	if err := second.Contacts.PutContactName(ctx, peer, "Bob", "Bob for account two"); err != nil {
		t.Fatal(err)
	}
	if err := container.Close(); err != nil {
		t.Fatal(err)
	}
	container, err = openSessionStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selectSession(ctx, container, false, ""); err == nil {
		t.Fatal("multiple accounts must require explicit selection")
	}
	if _, err := selectSession(ctx, container, true, firstID.String()); err == nil {
		t.Fatal("conflicting selection flags must be rejected")
	}
	if _, err := selectSession(ctx, container, false, "not-a-device"); err == nil {
		t.Fatal("invalid device JID must be rejected")
	}
	if _, err := selectSession(ctx, container, false, "999999999999:1@s.whatsapp.net"); err == nil {
		t.Fatal("unknown device must not silently select another account")
	}
	for _, tc := range []struct {
		id   types.JID
		name string
	}{{firstID, "Alice for account one"}, {secondID, "Bob for account two"}} {
		loaded, err := selectSession(ctx, container, false, tc.id.String())
		if err != nil {
			t.Fatal(err)
		}
		if loaded.GetJID() != tc.id || (tc.id == firstID && *loaded.NoiseKey.Priv != firstKey) {
			t.Fatal("account identity/key did not persist across database reopen")
		}
		contact, err := loaded.Contacts.GetContact(ctx, peer)
		if err != nil || contact.FullName != tc.name {
			t.Fatalf("contacts must remain isolated per account: %v", err)
		}
	}
	config, err := sessionConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", config.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dedicated, public bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('whatsapp.whatsmeow_device') IS NOT NULL, to_regclass('public.whatsmeow_device') IS NOT NULL").Scan(&dedicated, &public); err != nil {
		t.Fatal(err)
	}
	if !dedicated || public {
		t.Fatal("library session tables must be created only in the whatsapp schema")
	}
}

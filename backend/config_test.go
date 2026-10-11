package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, port, addr, passwordLogin, wantAddr, wantError string
	}{
		{"hosted port", "10000", "", "", "0.0.0.0:10000", ""},
		{"explicit address", "invalid", "127.0.0.1:8181", "false", "127.0.0.1:8181", ""},
		{"local default", "", "", "", "127.0.0.1:8180", ""},
		{"invalid port", "abc", "", "", "", "PORT"},
		{"zero port", "0", "", "", "", "PORT"},
		{"oversized port", "65536", "", "", "", "PORT"},
		{"invalid login setting", "10000", "", "yes", "", "ALLOW_PASSWORD_LOGIN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://api:private-test-secret@localhost/postgres")
			t.Setenv("WHATSAPP_DATABASE_URL", "")
			t.Setenv("PORT", test.port)
			t.Setenv("API_ADDR", test.addr)
			t.Setenv("ALLOW_PASSWORD_LOGIN", test.passwordLogin)
			c, err := loadConfig(filepath.Join(t.TempDir(), "missing.env"))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) || strings.Contains(err.Error(), "private-test-secret") {
					t.Fatalf("expected redacted %s error, got %v", test.wantError, err)
				}
				return
			}
			if err != nil || c.Addr != test.wantAddr || c.PasswordLogin || c.DatabaseURL == "" {
				t.Fatalf("unexpected hosted configuration: address=%q passwordLogin=%v err=%v", c.Addr, c.PasswordLogin, err)
			}
		})
	}
	t.Run("missing configuration", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		if _, err := loadConfig(filepath.Join(t.TempDir(), "missing.env")); err == nil {
			t.Fatal("accepted missing file without an environment database URL")
		}
	})
	t.Run("invalid file remains an error", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://api:private-test-secret@localhost/postgres")
		path := filepath.Join(t.TempDir(), "invalid.env")
		if err := os.WriteFile(path, []byte("malformed line"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err == nil {
			t.Fatal("environment settings bypassed file validation")
		}
	})
}

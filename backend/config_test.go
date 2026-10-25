package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIPOJobConfiguration(t *testing.T) {
	for _, test := range []struct {
		token, worker, importer, wantDatabase string
		valid                                 bool
	}{
		{"", "", "", "", true}, {"short", "postgres://worker", "", "", false},
		{strings.Repeat("a", 43), "", "", "", false},
		{strings.Repeat("a", 43), "postgres://worker", "", "postgres://worker", true},
		{strings.Repeat("a", 43), "", "postgres://importer", "postgres://importer", true},
		{strings.Repeat("a", 43), "postgres://worker", "postgres://importer", "postgres://importer", true},
		{strings.Repeat("a", 43) + "\n", "postgres://worker", "", "", false},
	} {
		t.Setenv("DATABASE_URL", "postgres://runtime")
		t.Setenv("IPO_JOB_TOKEN", test.token)
		t.Setenv("WHATSAPP_DATABASE_URL", test.worker)
		t.Setenv("IPO_DATABASE_URL", test.importer)
		t.Setenv("FCM_PROJECT_ID", "")
		t.Setenv("APP_ENV", "")
		config, err := loadConfig(filepath.Join(t.TempDir(), "missing.env"))
		if (err == nil) != test.valid {
			t.Fatalf("valid=%v error=%v", test.valid, err)
		}
		if err != nil && strings.Contains(err.Error(), test.token) {
			t.Fatal("credential exposed")
		}
		if err == nil && (config.IPODatabaseURL != test.wantDatabase || config.WhatsAppDatabaseURL != test.worker) {
			t.Fatal("importer configuration changed WhatsApp settings or ignored database precedence")
		}
	}
}

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
			t.Setenv("APP_ENV", "production")
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

func TestDevelopmentSignupConfiguration(t *testing.T) {
	for _, environment := range []string{"", "production", "development", "developing", "Development"} {
		t.Run(environment, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://api:private-test-secret@localhost/postgres")
			t.Setenv("APP_ENV", environment)
			t.Setenv("ALLOW_PASSWORD_LOGIN", "false")
			t.Setenv("API_ADDR", "127.0.0.1:8180")
			c, err := loadConfig(filepath.Join(t.TempDir(), "missing.env"))
			if environment == "developing" || environment == "Development" {
				if err == nil || !strings.Contains(err.Error(), "APP_ENV") {
					t.Fatal("invalid environment did not fail closed")
				}
				return
			}
			if err != nil || c.DevelopmentSignup != (environment == "development") || c.PasswordLogin {
				t.Fatalf("incorrect signup/password gate: signup=%v password=%v err=%v", c.DevelopmentSignup, c.PasswordLogin, err)
			}
		})
	}
	t.Run("file setting and environment override", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://api:private-test-secret@localhost/postgres")
		t.Setenv("ALLOW_PASSWORD_LOGIN", "false")
		t.Setenv("API_ADDR", "127.0.0.1:8180")
		previous, set := os.LookupEnv("APP_ENV")
		if err := os.Unsetenv("APP_ENV"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if set {
				_ = os.Setenv("APP_ENV", previous)
			} else {
				_ = os.Unsetenv("APP_ENV")
			}
		})
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("APP_ENV=development\n"), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := loadConfig(path)
		if err != nil || !c.DevelopmentSignup {
			t.Fatal("file did not enable development signup")
		}
		t.Setenv("APP_ENV", "production")
		c, err = loadConfig(path)
		if err != nil || c.DevelopmentSignup {
			t.Fatal("production environment did not override development file")
		}
	})
}

func TestSentryConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://api:private-test-secret@localhost/postgres")
	t.Setenv("APP_ENV", "development")
	t.Setenv("ALLOW_PASSWORD_LOGIN", "false")
	t.Setenv("API_ADDR", "127.0.0.1:8180")
	t.Setenv("SENTRY_DSN", "https://public@example.invalid/1")
	t.Setenv("SENTRY_ENVIRONMENT", "")
	t.Setenv("SENTRY_RELEASE", "")
	t.Setenv("RENDER_GIT_COMMIT", "fixture-commit")
	c, err := loadConfig(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil || c.SentryDSN == "" || c.SentryEnvironment != "development" || c.SentryRelease != "fixture-commit" {
		t.Fatal("Sentry did not use hosted configuration/environment/release")
	}
	t.Setenv("SENTRY_ENVIRONMENT", "verification")
	t.Setenv("SENTRY_RELEASE", "verification-release")
	c, err = loadConfig(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil || c.SentryEnvironment != "verification" || c.SentryRelease != "verification-release" {
		t.Fatal("explicit Sentry settings did not override defaults")
	}
}

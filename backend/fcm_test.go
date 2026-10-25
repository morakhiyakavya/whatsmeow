package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestFCMSender(t *testing.T) {
	data := map[string]string{"type": "contact_profile_changed", "profile_revision": "42"}
	for _, test := range []struct {
		name, body, retryAfter string
		status                 int
		retry, unregistered    bool
	}{
		{"accepted", `{"name":"projects/vanitilabs/messages/test"}`, "", 200, false, false},
		{"missing message ID", `{}`, "", 200, false, false},
		{"unregistered", `{"error":{"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, "", 404, false, true},
		{"generic not found", `{"error":{"status":"NOT_FOUND"}}`, "", 404, false, false},
		{"invalid payload or token", `{"error":{"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`, "", 400, false, false},
		{"wrong details type", `{"error":{"details":[{"@type":"other","errorCode":"UNREGISTERED"}]}}`, "", 404, false, false},
		{"unauthorized", `{}`, "", 401, false, false},
		{"quota", `{}`, "120", 429, true, false},
		{"unavailable", `{}`, "", 503, true, false},
		{"bad response", `not-json`, "", 500, true, false},
		{"retry date", `{}`, time.Now().Add(3 * time.Minute).UTC().Format(http.TimeFormat), 503, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer google-oauth-token" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("incorrect FCM method or Google OAuth authorization")
				}
				var payload struct {
					Message map[string]json.RawMessage `json:"message"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Message) != 3 {
					t.Fatal("unexpected payload shape")
				}
				var message pushMessage
				encoded, _ := json.Marshal(payload.Message)
				_ = json.Unmarshal(encoded, &message)
				if message.Token != "device-token" || message.Android.Priority != "normal" || message.Android.TTL != "86400s" || len(message.Data) != 2 || message.Data["profile_revision"] != "42" {
					t.Error("incorrect target, priority, TTL or string data")
				}
				w.Header().Set("Retry-After", test.retryAfter)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			sender := &fcmSender{client: oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "google-oauth-token"})), endpoint: server.URL}
			outcome := sender.send(context.Background(), "device-token", data)
			if outcome.retry != test.retry || outcome.unregistered != test.unregistered || outcome.failed != (test.name != "accepted") {
				t.Fatalf("incorrect outcome: %+v", outcome)
			}
			if test.retryAfter != "" && outcome.after < 119*time.Second {
				t.Fatal("Retry-After was ignored")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome := (&fcmSender{client: http.DefaultClient, endpoint: "https://example.invalid"}).send(ctx, "token", data)
	if !outcome.retry || !outcome.failed {
		t.Fatal("cancelled request was not recoverable")
	}
}

func TestFCMConfiguration(t *testing.T) {
	for _, test := range []struct{ project, credentials string }{
		{"vanitilabs", ""}, {"https://wrong-project/path", "missing-secret"}, {"vanitilabs", "missing-secret"},
	} {
		_, err := newFCMSender(context.Background(), test.project, test.credentials)
		if err == nil || strings.Contains(err.Error(), "missing-secret") {
			t.Fatal("invalid configuration accepted or secret path exposed")
		}
	}
	for _, body := range []string{`{"project_info":{}}`, `{"type":"authorized_user","refresh_token":"private-secret"}`, `{"type":"service_account"}`, `{malformed`} {
		path := filepath.Join(t.TempDir(), "server-credentials.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := newFCMSender(context.Background(), "vanitilabs", path)
		if err == nil || strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), path) {
			t.Fatal("invalid client/developer/server credentials accepted or exposed")
		}
	}
	for _, project := range []string{"", "vanitilabs", "bad/path"} {
		t.Setenv("DATABASE_URL", "postgres://fixture")
		t.Setenv("APP_ENV", "production")
		t.Setenv("API_ADDR", "127.0.0.1:8180")
		t.Setenv("ALLOW_PASSWORD_LOGIN", "false")
		t.Setenv("FCM_PROJECT_ID", project)
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
		t.Setenv("WHATSAPP_DATABASE_URL", "")
		_, err := loadConfig(filepath.Join(t.TempDir(), "missing.env"))
		if (err == nil) != (project == "") {
			t.Fatal("FCM disabled/enabled configuration gate failed")
		}
	}
	t.Setenv("FCM_PROJECT_ID", "vanitilabs")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "private-file")
	t.Setenv("WHATSAPP_DATABASE_URL", "postgres://worker")
	if _, err := loadConfig(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Fatal("valid FCM settings rejected")
	}
}

func TestFCMServerCredentialTypes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(t.TempDir(), "subject-token")
	if err := os.WriteFile(tokenFile, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, credentials := range []map[string]any{
		{"type": "service_account", "client_email": "fixture@fixture.iam.gserviceaccount.com",
			"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
			"token_uri":   "https://oauth2.googleapis.com/token"},
		{"type": "external_account", "audience": "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/fixture/providers/fixture",
			"subject_token_type": "urn:ietf:params:oauth:token-type:jwt", "token_url": "https://sts.googleapis.com/v1/token",
			"credential_source": map[string]string{"file": tokenFile}},
	} {
		t.Run(credentials["type"].(string), func(t *testing.T) {
			data, err := json.Marshal(credentials)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "credentials.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			sender, err := newFCMSender(context.Background(), "vanitilabs", path)
			if err != nil || sender == nil {
				t.Fatal("supported server credentials rejected", err)
			}
			if sender.client.Timeout != 10*time.Second || sender.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
				t.Fatal("FCM timeout or redirect restriction lost")
			}
		})
	}
}

func TestProfilePushBackoffAndTokens(t *testing.T) {
	for attempt := 1; attempt <= 5; attempt++ {
		base := time.Minute * time.Duration(1<<(attempt-1))
		if delay := profilePushDelay(attempt, 0); delay < base || delay >= base+base/4 {
			t.Fatal("backoff is not bounded and exponential")
		}
		if profilePushDelay(attempt, time.Hour) != time.Hour {
			t.Fatal("Retry-After was not honoured")
		}
	}
	for _, token := range []string{"", "with space", "new\nline", "\x00", strings.Repeat("a", 4097)} {
		if validFCMToken(token) {
			t.Fatal("invalid registration token accepted")
		}
	}
	if !validFCMToken("valid-token:example_123") {
		t.Fatal("opaque FCM token rejected")
	}
}

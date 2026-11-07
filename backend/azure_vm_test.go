package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func azureVMFixture() azureVMConfig {
	return azureVMConfig{TenantID: "11111111-1111-4111-8111-111111111111", ClientID: "22222222-2222-4222-8222-222222222222",
		SubscriptionID: "33333333-3333-4333-8333-333333333333", ClientSecret: "private-secret+&value", ResourceGroup: "fixture_group", VMName: "fixture-vm"}
}

func azureJSON(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestAzureVMConfiguration(t *testing.T) {
	c := azureVMFixture()
	values := map[string]string{"AZURE_VM_RECOVERY_ENABLED": "true", "application_id": c.ClientID, "directory_id": c.TenantID,
		"secret_value": c.ClientSecret, "AZURE_SUBSCRIPTION_ID": c.SubscriptionID, "AZURE_RESOURCE_GROUP": c.ResourceGroup, "AZURE_VM_NAME": c.VMName}
	get := func(key string) string { return values[key] }
	got, err := loadAzureVMConfig(get)
	if err != nil || got == nil || *got != c {
		t.Fatal("existing credential aliases not supported", err)
	}
	values["AZURE_CLIENT_ID"] = c.TenantID
	got, err = loadAzureVMConfig(get)
	if err != nil || got.ClientID != c.TenantID {
		t.Fatal("canonical setting did not take precedence")
	}
	for _, key := range []string{"AZURE_SUBSCRIPTION_ID", "AZURE_RESOURCE_GROUP", "AZURE_VM_NAME", "secret_value", "directory_id"} {
		previous := values[key]
		for _, value := range []string{"", "invalid/" + c.ClientSecret} {
			values[key] = value
			// A client secret is opaque; only absence is invalid.
			if key == "secret_value" && value != "" {
				continue
			}
			if _, err := loadAzureVMConfig(get); err == nil || strings.Contains(err.Error(), c.ClientSecret) {
				t.Fatal("invalid configuration accepted or credential exposed", key, err)
			}
		}
		values[key] = previous
	}
	for _, flag := range []string{"", "false", "yes"} {
		values["AZURE_VM_RECOVERY_ENABLED"] = flag
		got, err := loadAzureVMConfig(get)
		if flag == "yes" && err == nil || flag != "yes" && (err != nil || got != nil) {
			t.Fatal("invalid opt-in behaviour", flag, err)
		}
	}
}

func TestAzureVMPowerRecovery(t *testing.T) {
	for _, state := range []string{"running", "starting", "stopping", "deallocating", "stopped", "deallocated", "unknown"} {
		t.Run(state, func(t *testing.T) {
			r := newAzureVMRecovery(azureVMFixture())
			starts := 0
			r.client.Transport = ipoTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "login.microsoftonline.com" {
					if err := req.ParseForm(); err != nil || req.Form.Get("client_secret") != r.config.ClientSecret ||
						req.Form.Get("client_id") != r.config.ClientID || req.Form.Get("scope") != "https://management.azure.com/.default" {
						t.Fatal("wrong credential form")
					}
					return azureJSON(200, `{"access_token":"private-token","token_type":"Bearer","expires_in":3600}`), nil
				}
				if req.URL.Host != "management.azure.com" || req.Header.Get("Authorization") != "Bearer private-token" || req.URL.Query().Get("api-version") != "2025-04-01" {
					t.Fatal("wrong management request")
				}
				if req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/instanceView") {
					return azureJSON(200, `{"statuses":[{"code":"ProvisioningState/succeeded"},{"code":"PowerState/`+state+`"}]}`), nil
				}
				if req.Method != "POST" || !strings.HasSuffix(req.URL.Path, "/start") {
					t.Fatal("unexpected VM mutation")
				}
				starts++
				return azureJSON(202, ""), nil
			})
			status, err := r.check(context.Background())
			if (err != nil) != (state == "unknown") || status.State != state || status.CheckedAt.IsZero() {
				t.Fatal("wrong state", status, err)
			}
			wantStarts := 0
			if state == "stopped" || state == "deallocated" {
				wantStarts = 1
				if status.Action != "start_requested" {
					t.Fatal("accepted startup must not claim the VM is running", status)
				}
			}
			if starts != wantStarts {
				t.Fatal("unexpected startup count", starts)
			}
		})
	}
}

func TestAzureVMRecoveryFailuresAreRedactedAndRetryable(t *testing.T) {
	for _, failure := range []string{"authentication", "malformed token", "state denied", "state malformed", "state absent", "capacity", "oversized", "transport"} {
		t.Run(failure, func(t *testing.T) {
			r := newAzureVMRecovery(azureVMFixture())
			starts := 0
			r.client.Transport = ipoTransport(func(req *http.Request) (*http.Response, error) {
				if failure == "transport" {
					return nil, errors.New("private-secret-transport")
				}
				if req.URL.Host == "login.microsoftonline.com" {
					if failure == "authentication" {
						return azureJSON(401, `{"error_description":"private-secret"}`), nil
					}
					if failure == "malformed token" {
						return azureJSON(200, `{"private-secret":true}`), nil
					}
					return azureJSON(200, `{"access_token":"private-token"}`), nil
				}
				if req.Method == "GET" {
					switch failure {
					case "state denied":
						return azureJSON(403, `{"private-secret":"denied"}`), nil
					case "state malformed":
						return azureJSON(200, `private-secret`), nil
					case "state absent":
						return azureJSON(200, `{"statuses":[]}`), nil
					case "oversized":
						return azureJSON(200, strings.Repeat("x", (64<<10)+1)), nil
					}
					return azureJSON(200, `{"statuses":[{"code":"PowerState/deallocated"}]}`), nil
				}
				starts++
				return azureJSON(409, `{"error":{"code":"AllocationFailed","message":"private-secret"}}`), nil
			})
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := r.check(context.Background()); err == nil || strings.Contains(err.Error(), "private-") {
					t.Fatal("failure swallowed or credential exposed", err)
				}
			}
			if failure == "capacity" && starts != 2 || failure != "capacity" && starts != 0 {
				t.Fatal("capacity retry or fail-closed behaviour changed", starts)
			}
		})
	}
}

func TestAzureVMRecoveryStopsAndDoesNotFollowRedirects(t *testing.T) {
	r := newAzureVMRecovery(azureVMFixture())
	requested := make(chan struct{})
	r.client.Transport = ipoTransport(func(req *http.Request) (*http.Response, error) {
		close(requested)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	stop := r.start(context.Background())
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not check on startup")
	}
	stop()
	if status := r.status.Load(); status.Action != "checking" {
		t.Fatal("shutdown recorded a spurious failure", status)
	}
	followed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/target" {
			followed = true
		}
		w.Header().Set("Location", "/target")
		w.WriteHeader(307)
	}))
	defer server.Close()
	r = newAzureVMRecovery(azureVMFixture())
	_, code, err := r.request(context.Background(), "POST", server.URL, "private-token")
	if err != nil || code != 307 || followed {
		t.Fatal("credential-bearing redirect followed", code, err)
	}
	encoded, _ := json.Marshal(r.status.Load())
	if strings.Contains(string(encoded), "private-") {
		t.Fatal("status exposed credentials")
	}
}

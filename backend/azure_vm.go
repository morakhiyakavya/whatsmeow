package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type azureVMConfig struct {
	TenantID, ClientID, ClientSecret, SubscriptionID, ResourceGroup, VMName string
}

func loadAzureVMConfig(get func(string) string) (*azureVMConfig, error) {
	enabled := get("AZURE_VM_RECOVERY_ENABLED")
	if enabled != "" && enabled != "true" && enabled != "false" {
		return nil, errors.New("AZURE_VM_RECOVERY_ENABLED must be true or false")
	}
	if enabled != "true" {
		return nil, nil
	}
	credential := func(key, alias string) string {
		if value := get(key); value != "" {
			return value
		}
		return get(alias)
	}
	c := &azureVMConfig{TenantID: credential("AZURE_TENANT_ID", "directory_id"), ClientID: credential("AZURE_CLIENT_ID", "application_id"),
		ClientSecret: credential("AZURE_CLIENT_SECRET", "secret_value"), SubscriptionID: get("AZURE_SUBSCRIPTION_ID"),
		ResourceGroup: get("AZURE_RESOURCE_GROUP"), VMName: get("AZURE_VM_NAME")}
	if !validUUID(c.TenantID) || !validUUID(c.ClientID) || !validUUID(c.SubscriptionID) ||
		strings.TrimSpace(c.ClientSecret) == "" || len(c.ClientSecret) > 4096 ||
		!validText(c.ResourceGroup, 90) || !validText(c.VMName, 64) ||
		strings.ContainsAny(c.ResourceGroup+c.VMName, `/\?#%`) {
		return nil, errors.New("azure VM recovery requires valid tenant/client/subscription IDs, client secret, resource group and VM name")
	}
	return c, nil
}

type azureVMStatus struct {
	State     string    `json:"state"`
	Action    string    `json:"action"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

type azureVMRecovery struct {
	config azureVMConfig
	client *http.Client
	status atomic.Pointer[azureVMStatus]
}

func newAzureVMRecovery(config azureVMConfig) *azureVMRecovery {
	r := &azureVMRecovery{config: config, client: &http.Client{Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	r.status.Store(&azureVMStatus{State: "unknown", Action: "checking"})
	return r
}

func (r *azureVMRecovery) start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			pollCtx, pollCancel := context.WithTimeout(ctx, 45*time.Second)
			status, err := r.check(pollCtx)
			pollCancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				status.Action, status.Error = "error", err.Error()
				log.Print(err)
				sentry.CaptureException(err)
			} else if status.Action == "start_requested" {
				log.Print("Azure VM recovery requested startup")
			}
			r.status.Store(&status)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

// Keep OAuth credentials and provider response bodies out of errors and telemetry.
func (r *azureVMRecovery) request(ctx context.Context, method, target, token string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, 0, errors.New("azure VM recovery request could not be created")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := r.client.Do(req)
	if err != nil {
		return nil, 0, errors.New("azure VM recovery request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, response.StatusCode, errors.New("azure VM recovery response could not be read")
	}
	return data, response.StatusCode, nil
}

func (r *azureVMRecovery) check(ctx context.Context) (azureVMStatus, error) {
	status := azureVMStatus{State: "unknown", Action: "checked", CheckedAt: time.Now().UTC()}
	c := r.config
	credentials := clientcredentials.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret,
		TokenURL: "https://login.microsoftonline.com/" + c.TenantID + "/oauth2/v2.0/token",
		Scopes:   []string{"https://management.azure.com/.default"}, AuthStyle: oauth2.AuthStyleInParams}
	auth, err := credentials.Token(context.WithValue(ctx, oauth2.HTTPClient, r.client))
	if err != nil || auth.AccessToken == "" {
		return status, errors.New("azure VM recovery authentication failed")
	}
	vm := "https://management.azure.com/subscriptions/" + c.SubscriptionID + "/resourceGroups/" + url.PathEscape(c.ResourceGroup) +
		"/providers/Microsoft.Compute/virtualMachines/" + url.PathEscape(c.VMName)
	data, code, err := r.request(ctx, http.MethodGet, vm+"/instanceView?api-version=2025-04-01", auth.AccessToken)
	if err != nil {
		return status, err
	}
	if code != http.StatusOK {
		return status, fmt.Errorf("azure VM recovery state HTTP %d", code)
	}
	var view struct {
		Statuses []struct{ Code string } `json:"statuses"`
	}
	if json.Unmarshal(data, &view) != nil {
		return status, errors.New("azure VM recovery state response is invalid")
	}
	for _, entry := range view.Statuses {
		if strings.HasPrefix(entry.Code, "PowerState/") {
			status.State = strings.TrimPrefix(entry.Code, "PowerState/")
		}
	}
	switch status.State {
	case "running", "starting", "stopping", "deallocating":
		return status, nil
	case "stopped", "deallocated":
		_, code, err = r.request(ctx, http.MethodPost, vm+"/start?api-version=2025-04-01", auth.AccessToken)
		if err != nil {
			return status, err
		}
		if code != http.StatusOK && code != http.StatusAccepted {
			return status, fmt.Errorf("azure VM recovery start HTTP %d", code)
		}
		status.Action = "start_requested"
		return status, nil
	default:
		status.State = "unknown"
		return status, errors.New("azure VM recovery power state is unavailable")
	}
}

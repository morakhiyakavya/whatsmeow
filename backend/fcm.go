package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var firebaseProjectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

type pushMessage struct {
	Token   string `json:"token"`
	Android struct {
		Priority string `json:"priority"`
		TTL      string `json:"ttl"`
	} `json:"android"`
	Data map[string]string `json:"data"`
}

type pushOutcome struct {
	unregistered bool
	retry        bool
	after        time.Duration
	failed       bool
}

type fcmSender struct {
	client   *http.Client
	endpoint string
}

func newFCMSender(ctx context.Context, project, credentialsFile string) (*fcmSender, error) {
	if !firebaseProjectPattern.MatchString(project) || credentialsFile == "" {
		return nil, errors.New("FCM requires a valid FCM_PROJECT_ID and GOOGLE_APPLICATION_CREDENTIALS secret-file path")
	}
	file, err := os.Open(credentialsFile)
	if err != nil {
		return nil, errors.New("cannot read FCM server credentials")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("cannot read FCM server credentials")
	}
	var identity struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if json.Unmarshal(data, &identity) != nil || (identity.Type != "service_account" && identity.Type != "external_account") {
		return nil, errors.New("FCM requires service-account or workload-identity credentials, not Android configuration or a developer login")
	}
	if identity.Type == "service_account" && (identity.ClientEmail == "" || identity.PrivateKey == "" || identity.TokenURI == "") {
		return nil, errors.New("incomplete FCM service-account credentials")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 10 * time.Second})
	credentials, err := google.CredentialsFromJSON(ctx, data, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return nil, errors.New("invalid FCM server credentials")
	}
	client := oauth2.NewClient(ctx, credentials.TokenSource)
	client.Timeout = 10 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &fcmSender{client: client, endpoint: "https://fcm.googleapis.com/v1/projects/" + project + "/messages:send"}, nil
}

func (s *fcmSender) send(ctx context.Context, token string, data map[string]string) pushOutcome {
	message := pushMessage{Token: token, Data: data}
	message.Android.Priority, message.Android.TTL = "normal", "86400s"
	body, err := json.Marshal(struct {
		Message pushMessage `json:"message"`
	}{message})
	if err != nil {
		return pushOutcome{failed: true}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return pushOutcome{failed: true}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return pushOutcome{failed: true, retry: true}
	}
	defer response.Body.Close()
	var reply struct {
		Name  string `json:"name"`
		Error struct {
			Details []struct {
				Type string `json:"@type"`
				Code string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&reply)
	if response.StatusCode == http.StatusOK && err == nil && reply.Name != "" {
		return pushOutcome{}
	}
	outcome := pushOutcome{failed: true, retry: response.StatusCode == 429 || response.StatusCode >= 500}
	for _, detail := range reply.Error.Details {
		if response.StatusCode == 404 && detail.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" && detail.Code == "UNREGISTERED" {
			outcome.unregistered = true
		}
	}
	// INVALID_ARGUMENT may describe the payload; never delete a token on that error alone.
	if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 32); err == nil && seconds > 0 {
		outcome.after = time.Duration(seconds) * time.Second
	} else if date, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
		outcome.after = time.Until(date)
	}
	return outcome
}

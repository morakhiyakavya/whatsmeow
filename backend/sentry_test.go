package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

type sentryTestTransport struct{ events []*sentry.Event }

func (t *sentryTestTransport) FlushWithContext(context.Context) bool { return true }
func (t *sentryTestTransport) Close()                                {}

func (t *sentryTestTransport) Configure(sentry.ClientOptions) {}
func (t *sentryTestTransport) SendEvent(event *sentry.Event)  { t.events = append(t.events, event) }
func (t *sentryTestTransport) Flush(time.Duration) bool       { return true }

func TestSentryHTTPPrivacyAndSampling(t *testing.T) {
	for _, development := range []bool{false, true} {
		options := sentryOptions(configuration{DevelopmentSignup: development})
		want := 0.1
		if development {
			want = 1
		}
		if options.TracesSampleRate != want {
			t.Fatalf("development=%v: sample rate=%v, want %v", development, options.TracesSampleRate, want)
		}
	}
	transport := &sentryTestTransport{}
	options := sentryOptions(configuration{SentryDSN: "https://public@example.invalid/1", SentryEnvironment: "test", SentryRelease: "test-release", DevelopmentSignup: true})
	options.Transport = transport
	options.TraceIgnoreStatusCodes = [][]int{}
	client, err := sentry.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	hub := sentry.NewHub(client, sentry.NewScope())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/businesses/{businessID}/test", func(http.ResponseWriter, *http.Request) {
		panic("private-panic-value")
	})
	handler := sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(mux)
	request := httptest.NewRequest("POST", "/api/businesses/private-business/test?phone_number=private-query", strings.NewReader("private-body"))
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("Cookie", "session=private-cookie")
	request = request.WithContext(sentry.SetHubOnContext(request.Context(), hub))
	func() {
		defer func() {
			if recover() != "private-panic-value" {
				t.Fatal("Sentry changed the existing panic behaviour")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()
	if len(transport.events) != 2 {
		t.Fatalf("expected a captured panic and trace, got %d events", len(transport.events))
	}
	for _, event := range transport.events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "private-") || event.Environment != "test" || event.Release != "test-release" {
			t.Fatalf("telemetry leaked fixture data or lost release/environment: %s", encoded)
		}
		if event.Type == "transaction" && event.Transaction != "POST /api/businesses/{businessID}/test" {
			t.Fatalf("trace did not use the route template: %q", event.Transaction)
		}
		if event.Type != "transaction" {
			if len(event.Threads) == 0 || event.Threads[0].Stacktrace == nil || len(event.Threads[0].Stacktrace.Frames) == 0 {
				t.Fatal("privacy filtering removed the diagnostic stack trace")
			}
		}
	}
	unmatched := httptest.NewRequest("GET", "/private-unmatched?token=private-query", nil)
	unmatched = unmatched.WithContext(sentry.SetHubOnContext(unmatched.Context(), sentry.NewHub(client, sentry.NewScope())))
	handler.ServeHTTP(httptest.NewRecorder(), unmatched)
	event := transport.events[len(transport.events)-1]
	encoded, err := json.Marshal(event)
	if err != nil || strings.Contains(string(encoded), "private-") || event.Transaction != "unmatched route" {
		t.Fatalf("unmatched request telemetry exposed fixture data: %s", encoded)
	}
}

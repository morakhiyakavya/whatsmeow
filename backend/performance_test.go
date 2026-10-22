package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type performanceCapture struct {
	sync.Mutex
	events  []*sentry.Event
	metrics []*sentry.Metric
	logs    []*sentry.Log
}

func (*performanceCapture) Configure(sentry.ClientOptions)        {}
func (*performanceCapture) Flush(time.Duration) bool              { return true }
func (*performanceCapture) FlushWithContext(context.Context) bool { return true }
func (*performanceCapture) Close()                                {}
func (c *performanceCapture) SendEvent(event *sentry.Event) {
	c.Lock()
	defer c.Unlock()
	c.events = append(c.events, event)
}

func performanceContext(t *testing.T) (context.Context, *performanceCapture) {
	t.Helper()
	capture := &performanceCapture{}
	options := sentryOptions(configuration{SentryDSN: "https://public@example.invalid/1", DevelopmentSignup: true})
	options.Transport = capture
	metricFilter, logFilter := options.BeforeSendMetric, options.BeforeSendLog
	options.BeforeSendMetric = func(metric *sentry.Metric) *sentry.Metric {
		metric = metricFilter(metric)
		capture.Lock()
		defer capture.Unlock()
		capture.metrics = append(capture.metrics, metric)
		return metric
	}
	options.BeforeSendLog = func(entry *sentry.Log) *sentry.Log {
		entry = logFilter(entry)
		capture.Lock()
		defer capture.Unlock()
		capture.logs = append(capture.logs, entry)
		return entry
	}
	client, err := sentry.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	hub := sentry.NewHub(client, sentry.NewScope())
	hub.Scope().SetAttributes(attribute.String("user.email", "private-email"), attribute.String("secret", "private-scope"))
	return sentry.SetHubOnContext(context.Background(), hub), capture
}

func TestPerformanceTracePrivacyAndIdlePoll(t *testing.T) {
	ctx, capture := performanceContext(t)
	span := startOperation(ctx, "queue.process", "confirmation.deliver")
	span.SetTag("job.key", performanceKey("private-record-id"))
	tracer := databaseTrace{pool: "whatsapp"}
	queryCtx := tracer.TraceQueryStart(span.Context(), nil, pgx.TraceQueryStartData{SQL: "SELECT 'private-sql'", Args: []any{"private-argument"}})
	tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{Err: errors.New("private-database-error")})
	finishOperation(span, nil)
	idle := startOperation(ctx, "queue.process", "ipo.import")
	finishPoll(idle, false, nil)
	capture.Lock()
	defer capture.Unlock()
	if len(capture.events) != 1 {
		t.Fatalf("idle poll exported a trace: %d events", len(capture.events))
	}
	event := capture.events[0]
	if event.Transaction != "confirmation.deliver" || len(event.Spans) != 1 || event.Spans[0].Op != "db.query" || event.Spans[0].Status != sentry.SpanStatusInternalError {
		t.Fatal("background/query tracing lost its identity, nesting or error status")
	}
	for _, data := range []any{capture.events, capture.metrics, capture.logs} {
		encoded, err := json.Marshal(data)
		if err != nil || strings.Contains(string(encoded), "private-") {
			t.Fatalf("telemetry exposed private fixture values: %s", encoded)
		}
	}
	if len(capture.metrics) == 0 || len(capture.logs) == 0 {
		t.Fatal("operation metrics/logs were not recorded")
	}
	for _, metric := range capture.metrics {
		if _, exists := metric.Attributes["job.key"]; exists {
			t.Fatal("high-cardinality job key entered metrics")
		}
		if _, exists := metric.Attributes["sentry.server.address"]; exists {
			t.Fatal("metric exposed host identity")
		}
	}
}

func TestPerformanceQueueCancellation(t *testing.T) {
	for _, test := range []struct {
		name     string
		deadline bool
		want     string
	}{{"canceled", false, "canceled"}, {"deadline", true, "deadline_exceeded"}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, capture := performanceContext(t)
			var cancel context.CancelFunc
			if test.deadline {
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			defer cancel()
			c := &accountConnection{connectionState: connectionState{State: "connected"}, profiles: make(chan profileRequest), done: make(chan struct{})}
			s := &whatsAppService{accounts: map[string]*accountConnection{"business/account": c}}
			result := s.contactProfile(ctx, "business", "account", "+919000001010")
			if operationOutcome(result.err) != test.want {
				t.Fatal("caller outcome changed")
			}
			capture.Lock()
			defer capture.Unlock()
			found := false
			for _, metric := range capture.metrics {
				if metric.Attributes["operation"].AsInterface() == "whatsapp.profile.queue_wait" && metric.Attributes["outcome"].AsInterface() == test.want {
					found = true
				}
			}
			if !found {
				t.Fatal("queue wait did not distinguish cancellation from deadline expiry")
			}
		})
	}
}

func TestPerformancePoolMetrics(t *testing.T) {
	ctx, capture := performanceContext(t)
	recordPoolStats(ctx, "api", sql.DBStats{MaxOpenConnections: 10, OpenConnections: 4, InUse: 3, Idle: 1, WaitCount: 7, WaitDuration: 250 * time.Millisecond})
	capture.Lock()
	defer capture.Unlock()
	values := map[string]float64{}
	for _, metric := range capture.metrics {
		if metric.Attributes["db.pool.name"].AsInterface() != "api" {
			t.Fatal("pool identity missing")
		}
		value, _ := metric.Value.Float64()
		values[metric.Name] = value
	}
	if values["server.db.pool.wait_count.total"] != 7 || values["server.db.pool.wait_seconds.total"] != .25 || values["server.db.pool.in_use"] != 3 {
		t.Fatal("pool saturation/wait metrics are incorrect")
	}
}

func TestPerformanceInstrumentationIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable schema_test database")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test connection")
	}
	config.Tracer = databaseTrace{pool: "api"}
	db := stdlib.OpenDB(*config)
	defer db.Close()
	db.SetMaxOpenConns(1)
	var role string
	if err := db.QueryRow("SELECT current_user").Scan(&role); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test database")
	}
	ctx, capture := performanceContext(t)
	app, err := newAPI(db)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/health?secret=private-query", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	app.routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal("instrumented health path failed")
	}
	held, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Millisecond)
	defer cancel()
	var one int
	if err := db.QueryRowContext(waitCtx, "SELECT 1").Scan(&one); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("pool-wait fixture failed")
	}
	held.Close()
	if db.Stats().WaitCount < 1 || db.Stats().WaitDuration <= 0 {
		t.Fatal("pool acquisition wait was not measured")
	}
	app.recordPerformance(ctx, configuration{})
	capture.Lock()
	defer capture.Unlock()
	found := false
	for _, event := range capture.events {
		if event.Transaction == "GET /health" {
			for _, span := range event.Spans {
				if span.Op == "db.query" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("real database query did not become a child of the HTTP trace")
	}
	metricFound := false
	for _, metric := range capture.metrics {
		if metric.Name == "server.queue.depth" {
			metricFound = true
		}
	}
	if !metricFound {
		t.Fatal("runtime role queue inventory failed")
	}
}

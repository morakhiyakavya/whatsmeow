package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"runtime/metrics"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"github.com/jackc/pgx/v5"
)

func startOperation(ctx context.Context, op, name string) *sentry.Span {
	if sentry.SpanFromContext(ctx) != nil {
		return sentry.StartSpan(ctx, op, sentry.WithDescription(name))
	}
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	ctx = sentry.SetHubOnContext(ctx, hub.Clone())
	return sentry.StartTransaction(ctx, name, sentry.WithOpName(op), sentry.WithDescription(name), sentry.WithTransactionSource(sentry.SourceTask))
}

func operationOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "error"
	}
}

func finishOperation(span *sentry.Span, err error) {
	outcome := operationOutcome(err)
	if err == nil && span.Status != sentry.SpanStatusUndefined && span.Status != sentry.SpanStatusOK {
		outcome = "error"
	}
	switch outcome {
	case "canceled":
		span.Status = sentry.SpanStatusCanceled
	case "deadline_exceeded":
		span.Status = sentry.SpanStatusDeadlineExceeded
	case "error":
		span.Status = sentry.SpanStatusInternalError
	default:
		if span.Status == sentry.SpanStatusUndefined {
			span.Status = sentry.SpanStatusOK
		}
	}
	duration := float64(time.Since(span.StartTime)) / float64(time.Millisecond)
	span.SetData("operation.outcome", outcome)
	sentry.NewMeter(span.Context()).Distribution("server.operation.duration", duration,
		sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("operation", span.Description), attribute.String("outcome", outcome)))
	if (span.IsTransaction() && span.Op == "queue.process") || err != nil || duration >= 1000 {
		entry := sentry.NewLogger(span.Context()).Info().String("operation", span.Description).String("outcome", outcome).Float64("duration_ms", duration)
		for _, key := range []string{"job.key", "worker.key"} {
			if value := span.Tags[key]; value != "" {
				entry = entry.String(key, value)
			}
		}
		entry.Emit("Server operation completed")
	}
	span.Finish()
}

// Correlate asynchronous work without exporting record IDs or phone numbers.
func performanceKey(id string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(id))))
}

func recordQueueAge(ctx context.Context, queue string, created time.Time) {
	sentry.NewMeter(ctx).Distribution("server.queue.age", max(0, time.Since(created).Seconds()),
		sentry.WithUnit(sentry.UnitSecond), sentry.WithAttributes(attribute.String("queue.name", queue)))
}

func finishPoll(span *sentry.Span, worked bool, err error) {
	if !worked && err == nil {
		span.Sampled = sentry.SampledFalse
		span.Finish()
		return
	}
	finishOperation(span, err)
}

type databaseTrace struct{ pool string }
type databaseSpanKey struct{}

func (t databaseTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if sentry.SpanFromContext(ctx) == nil {
		return ctx
	}
	op := "query"
	for word := range strings.FieldsSeq(data.SQL) {
		switch word := strings.ToUpper(word); word {
		case "SELECT", "WITH", "INSERT", "UPDATE", "DELETE", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "SET", "SHOW", "CREATE", "ALTER", "DROP":
			op = word
		}
		break
	}
	fingerprint := sha256.Sum256([]byte(data.SQL))
	span := sentry.StartSpan(ctx, "db.query", sentry.WithDescription(fmt.Sprintf("PostgreSQL %s %x", op, fingerprint[:8])))
	span.SetData("db.system.name", "postgresql")
	span.SetData("db.pool.name", t.pool)
	// SQL, arguments, DSNs and error text can contain application/session secrets.
	return context.WithValue(span.Context(), databaseSpanKey{}, span)
}

func (t databaseTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, _ := ctx.Value(databaseSpanKey{}).(*sentry.Span)
	if span == nil {
		return
	}
	span.Status = sentry.SpanStatusOK
	if data.Err != nil {
		span.Status = sentry.SpanStatusInternalError
		if errors.Is(data.Err, context.Canceled) {
			span.Status = sentry.SpanStatusCanceled
		} else if errors.Is(data.Err, context.DeadlineExceeded) {
			span.Status = sentry.SpanStatusDeadlineExceeded
		}
	}
	span.SetData("operation.outcome", operationOutcome(data.Err))
	span.Finish()
}

func (t databaseTrace) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	span := startOperation(ctx, "db.connection", "database.connect")
	span.SetData("db.pool.name", t.pool)
	return context.WithValue(span.Context(), databaseSpanKey{}, span)
}

func (t databaseTrace) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if span, _ := ctx.Value(databaseSpanKey{}).(*sentry.Span); span != nil {
		finishOperation(span, data.Err)
	}
}

func recordPoolStats(ctx context.Context, name string, stats sql.DBStats) {
	meter := sentry.NewMeter(ctx)
	options := sentry.WithAttributes(attribute.String("db.pool.name", name))
	for key, value := range map[string]float64{
		"connections": float64(stats.OpenConnections), "in_use": float64(stats.InUse),
		"idle": float64(stats.Idle), "limit": float64(stats.MaxOpenConnections),
		"wait_count.total": float64(stats.WaitCount), "wait_seconds.total": stats.WaitDuration.Seconds(),
	} {
		meter.Gauge("server.db.pool."+key, value, options)
	}
}

func (a *api) recordPerformance(ctx context.Context, config configuration) {
	recordPoolStats(ctx, "api", a.db.Stats())
	if a.whatsapp != nil {
		recordPoolStats(ctx, "whatsapp", a.whatsapp.db.Stats())
	}
	if a.ipoDatabase != nil {
		recordPoolStats(ctx, "ipo_import", a.ipoDatabase.Stats())
	}
	meter := sentry.NewMeter(ctx)
	for worker, enabled := range map[string]bool{"whatsapp": a.whatsapp != nil, "ipo_import": a.ipoDatabase != nil, "profile_push": a.whatsapp != nil && config.FCMProjectID != ""} {
		value := 0.0
		if enabled {
			value = 1
		}
		meter.Gauge("server.worker.enabled", value, sentry.WithAttributes(attribute.String("worker.name", worker)))
	}
	samples := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/sched/goroutines:goroutines"}, {Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(samples)
	meter.Gauge("server.runtime.heap", float64(samples[0].Value.Uint64()), sentry.WithUnit(sentry.UnitByte))
	meter.Gauge("server.runtime.goroutines", float64(samples[1].Value.Uint64()))
	meter.Gauge("server.runtime.gc_cpu_seconds.total", samples[2].Value.Float64(), sentry.WithUnit(sentry.UnitSecond))
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var confirmations, pushes, imports int64
	var confirmationAge, pushAge, importAge float64
	err := a.db.QueryRowContext(readCtx, `SELECT
	 (SELECT count(*) FROM ipo_pos.transaction_confirmations WHERE status IN ('pending','sending')),
	 COALESCE((SELECT extract(epoch FROM clock_timestamp()-min(created_at)) FROM ipo_pos.transaction_confirmations WHERE status IN ('pending','sending')),0),
	 (SELECT count(*) FROM ipo_pos.profile_push_outbox WHERE next_attempt_at<=now() AND attempts<5 AND expires_at>now()),
	 COALESCE((SELECT extract(epoch FROM clock_timestamp()-min(next_attempt_at)) FROM ipo_pos.profile_push_outbox WHERE next_attempt_at<=now() AND attempts<5 AND expires_at>now()),0),
	 (SELECT count(*) FROM ipo_pos.ipo_import_runs WHERE status IN ('pending','running','failed','partial') AND attempts<3 AND next_attempt_at<=now()),
	 COALESCE((SELECT extract(epoch FROM clock_timestamp()-min(next_attempt_at)) FROM ipo_pos.ipo_import_runs WHERE status IN ('pending','running','failed','partial') AND attempts<3 AND next_attempt_at<=now()),0)`).Scan(&confirmations, &confirmationAge, &pushes, &pushAge, &imports, &importAge)
	if err != nil {
		sentry.NewLogger(ctx).Warn().String("outcome", operationOutcome(err)).Emit("Performance queue snapshot unavailable")
		return
	}
	for _, queue := range []struct {
		name  string
		count int64
		age   float64
	}{{"confirmation", confirmations, confirmationAge}, {"profile_push", pushes, pushAge}, {"ipo_import", imports, importAge}} {
		option := sentry.WithAttributes(attribute.String("queue.name", queue.name))
		meter.Gauge("server.queue.depth", float64(queue.count), option)
		meter.Gauge("server.queue.oldest_age", max(0, queue.age), sentry.WithUnit(sentry.UnitSecond), option)
	}
}

func startPerformanceMonitor(ctx context.Context, a *api, config configuration) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			a.recordPerformance(ctx, config)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

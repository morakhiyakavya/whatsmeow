# Go server performance telemetry

Implemented on 22 October 2026 using the existing Sentry Go and pgx dependencies.
The implementation is verified locally; the hosted service needs deployment of
this commit before it emits the added instrumentation. Existing sampling remains
100% for development and 10% for production. Standalone metrics are independent
of that trace sampling, subject to Sentry ingestion limits.

## What is measured

| Signal | Coverage |
|---|---|
| HTTP | Existing request traces, plus `server.http.duration` in milliseconds, grouped by route template |
| PostgreSQL | `db.query` child spans for query execution and row fetching, plus connection establishment; SQL operation and fingerprint only |
| SQL pools | Open, in-use, idle and maximum connections; cumulative wait count and wait seconds for the API and WhatsApp pools |
| Profile/number lookups | Queue-admission wait, account processing, user-info, picture and registration calls, including storage spans |
| Account lifecycle | Connection acquisition, connect/reconnect, phone pairing, logout, ownership guards, binding, picture invalidation and recovery |
| Confirmations | Claim/storage spans, queue age, WhatsApp sending, result persistence and successful creation-to-sent duration |
| Profile notifications | Claim/storage, lateness from the due time, push attempt, retry/unregistered flags |
| IPO import | Claim/storage, job age, collection, source HTTP requests, persistence, import count and outcome |
| Queues/runtime | Queue depth and oldest due age, heap objects, goroutines, cumulative GC CPU seconds and worker-enabled flags |

The monitor runs at startup and once per minute only when a Sentry DSN is
configured. Its read-only aggregate query has a two-second deadline. Worker flags
describe application configuration, not proof that a remote connection is healthy.
Account-state logs provide the latter's available lifecycle evidence. Render
remains the source for host CPU/memory utilisation.

## Timing and correlation

`server.operation.duration` records milliseconds with static `operation` and
`outcome` attributes. Cancellation and deadline expiry have distinct outcomes,
even where the existing HTTP contract returns 504 for both. Expected hidden/unset
picture responses are not marked as remote errors. Background completion, slow
operations and failures emit a structured completion log; account-state changes
emit a separate log. Empty successful job polls are discarded from traces and
operation metrics.

`job.key` and `worker.key` are SHA-256 digests of internal UUID identifiers, used
only in traces/logs. A transaction's confirmation enqueue and its later delivery
share the job key; per-account work shares the worker key. They are deliberately
excluded from metric labels. Existing queued records require no migration.
These correlation keys link independently sampled traces; this is not a claim
that an asynchronous job retains the original HTTP trace ID across restarts.

`server.queue.age` is a distribution in seconds:

- Confirmation: creation to claim, including transaction commit and worker waiting.
- Profile push: time overdue relative to the claimed attempt's original due time.
- IPO import: time since job creation, including prior retries.

`server.confirmation.total` measures creation to successfully persisted `sent`
state. It does not prove recipient-phone delivery or reading. `confirmation.send`
and `confirmation.persist` separate the send call from its final database update.

`server.db.pool.*` gauges include `connections`, `in_use`, `idle`, `limit`,
`wait_count.total` and `wait_seconds.total`, grouped by `db.pool.name`.
The `.total` values are process-lifetime cumulative gauges and reset at restart;
compare changes over time. Per-request SQL pool acquisition delays are not
individually attributed by the pgx query hook. Explicit WhatsApp account/import
connection acquisition has its own span; the shared pool statistics cover the
remaining contention.

Other gauges are `server.queue.depth`, `server.queue.oldest_age` (seconds),
`server.worker.enabled`, `server.runtime.heap` (bytes),
`server.runtime.goroutines`, and `server.runtime.gc_cpu_seconds.total` (seconds).
Queue gauges cover confirmation pending/sending records, currently due eligible
profile pushes and currently due retryable imports.

## Privacy and operating limits

No request bodies, headers, cookies, query parameters, raw SQL, bound arguments,
customer/message text, phone numbers, device/session keys or database URLs are
added. Error values remain redacted. Log/metric attributes use an allowlist and
discard the SDK's hostname and scope-provided user attributes. Source HTTP spans
contain only the validated source hostname and response status, without issuer
URLs. Job/account UUIDs are digested before correlation.

This instruments application boundaries; it does not profile every CPU function,
retain payloads, trace every WhatsApp library-internal goroutine, or expose pprof.
Go function/CPU flamegraphs are not added. Traces retain the existing SDK span
cap, so a large import can truncate SQL detail; stage durations/counts and metrics
remain available. Extra telemetry has runtime and ingestion overhead; no Sentry
plan, budget, sampling rate, notification rule or Render resource was changed.

## Verification and finding data

The full disposable PostgreSQL schema/API suite passed. After final lifecycle
instrumentation changes, the affected ledger/delivery, profile push, recovery,
profile, privacy and performance checks passed again; unaffected catalogue/source
checks reused the full-suite result. Tests exercise query/HTTP nesting, cancellation
versus deadlines, pool waits, queue snapshots, idle-poll suppression, privacy and
real worker send/persistence spans with simulated WhatsApp delivery. Backend vet
passed. No customer message or hosted application write was used for verification.

The actual API was started locally through its normal `run` path with its existing
private Sentry/runtime configuration, a verification environment/release, and
WhatsApp, importer and FCM workers disabled. Health returned 200 and synthetic
invalid-token reads returned 401. The temporary local server was then stopped.
Sentry confirmed:

- [HTTP request with its PostgreSQL child span](https://vanitilabs.sentry.io/explore/traces/trace/8488802afb9c2608de11ce48e138c0c6).
- [Database connection trace](https://vanitilabs.sentry.io/explore/traces/trace/170873bc3e0f1741fb689a66c40d862e).
- Request-duration, operation-duration, runtime, pool-wait, worker-enabled and
  queue-depth metrics in `environment=performance-verification`,
  `release=performance-check-20261022`.

In [Sentry Traces](https://vanitilabs.sentry.io/explore/traces/?project=4512250484883456),
filter by the deployed release and `span.op:db.query`, `span.op:whatsapp.send` or
transaction `confirmation.deliver`. Use a job/worker key to correlate relevant
logs and traces. In [Sentry Metrics](https://vanitilabs.sentry.io/explore/metrics/?project=4512250484883456),
select the metric names above, then group by operation, outcome, pool or queue.
Remove the verification filters when examining the deployed service.

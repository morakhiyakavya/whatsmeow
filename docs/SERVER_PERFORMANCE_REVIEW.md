# Go server performance review — 22 October 2026

The recorded traffic does not show CPU, memory or routine SQL saturation. The
largest confirmed delay is the WhatsApp confirmation workflow: one of four
recent confirmations took **12.61 seconds from creation to being marked sent**.
The current telemetry cannot establish production capacity or identify the
precise function responsible for each wait.

This review made no hosted application, schema, configuration or customer-message
changes. Hosted SQL ran inside explicitly read-only transactions, with verified
TLS, the existing runtime roles and statement/lock timeouts. Existing rollout
documentation changes were preserved.

## Deployed service and resources

- Service: [whatsapp-pos-dev](https://dashboard.render.com/web/srv-db5nundckfvc73arsvkg),
  one Free instance in Singapore, source commit
  `0571bfbd7e93cddf6f8088ac37aca78418c00fd1`.
- Render resource window: 21 October 11:30 UTC to 22 October 11:27 UTC
  (21 October 17:00 IST to 22 October 16:57 IST), at 15-minute resolution.
- Maximum returned CPU usage: 0.00748 CPU against a 0.15 CPU limit, approximately
  **5% of the limit**. This sampled series cannot exclude shorter bursts.
- Maximum returned memory usage: **90.75 MiB of 512 MiB**; final returned sample
  approximately 20.9 MiB. The samples show no current memory exhaustion or
  sustained growth; they cannot rule out every memory leak.
- No failure, restart or hardware-failure events were returned for the preceding
  seven days. Requested Render HTTP metrics and matching application logs were
  empty; empty results do not establish that no relevant event occurred.
- Three sequential public health requests from the development computer returned
  200. A fresh connection took **1.463 s**, including 0.691 s DNS and connection/TLS
  setup; the two reused-connection requests took **0.178 s and 0.182 s**. These
  health-only samples are not a mobile-device or load benchmark.

## Database execution and connection capacity

The hosted database contained 931 contact-name observations, 121 IPOs, four
transactions and one linked WhatsApp account at inspection. These are small
datasets. The connection snapshot showed 14 database sessions, including the
audit's two sessions, against PostgreSQL's configured maximum of 60. No blocked
session was observed. Some other roles' activity fields were hidden.

The exact existing SELECT expressions were read from the Go source and executed
with `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`, without returning customer records:

| Read path | PostgreSQL execution time |
|---|---:|
| IPO catalogue | 4.915 ms |
| Contact directory, empty search | 4.671 ms |
| Contact directory, substring search | 2.573 ms |
| Transaction list | 0.157 ms |
| Confirmation lookup | 1.351 ms |
| Active account lookup | 1.906 ms |

These are individual execution samples, excluding network and planning. IPO
planning additionally took 21.873 ms. Transaction listing used
`transactions_date_idx`; the catalogue used `ipos_created_idx`. Sequential scans
of small observation/account/session tables are not, by themselves, defects. No
invalid or unready application index was found.

The available roles could not access the `extensions` schema containing
`pg_stat_statements`. Historical slow-query rankings are therefore **unavailable**;
permissions were not changed. The database's two cumulative deadlocks date from
the statistics reset on 2 October and cannot be attributed to this application or
the reported lag.

The API pool permits ten connections; the WhatsApp pool permits forty. Each active
WhatsApp account retains one connection for its session locks. The application
currently caps active accounts at 32. Those limits leave finite shared capacity
for WhatsApp storage and background jobs; they are not measured capacity claims.
Live Go pool wait counts/durations are not exported, so the database snapshot
cannot rule out earlier application-pool waits.

## WhatsApp confirmation waiting

Four stored confirmations from the preceding seven days were marked sent. Their
creation-to-sent durations were **1.254 s, 1.020 s, 0.999 s and 12.609 s**.
The longest consisted of **7.915 s before the attempt timestamp** and **4.694 s
from attempt to completion**. These timestamps include database/worker overhead;
they do not measure pure network time or actual recipient-phone delivery/read
receipts. None was pending or sending at inspection.

The long confirmation was created at 16:41:32 UTC (22:11:32 IST) on 21 October,
attempted at 16:41:40 and marked sent at 16:41:44. The HTTP save response itself
completed much earlier; its duration does not describe this background work.

[The account loop](../backend/whatsapp_service.go) handles profiles, number checks,
confirmation sends and lifecycle work serially. Confirmation pickup polls every
two seconds and selects at most one job per tick. A slow operation can delay the
others on the same account. The confirmation send allows twenty seconds, whereas
a profile caller gives up after eight seconds. This is a concrete blocking risk,
but the current traces do not prove it caused the observed 7.915-second queue wait.

Profile lookup also performs `GetUserInfo` and `GetProfilePictureInfo` sequentially
on every request. Cached contact names are read between them. There is currently
no span breakdown of these operations.

Sentry recorded 60 profile requests over seven days: 43 successful, nine 504,
seven 502 and one 409. The nine 504s lasted **0.038–1.429 seconds**, all below the
eight-second profile limit. The handler maps both `context.Canceled` and
`context.DeadlineExceeded` to 504. Earlier client cancellation/deadlines are a
plausible explanation; these records must not be described as proof that the
backend exhausted its eight-second timeout.

Two profile-notification outbox jobs were due with **zero attempts**, the oldest
approximately eighteen hours overdue. Whether the FCM worker was configured and
running could not be established from the returned telemetry. This is a separate
notification-freshness issue, not a demonstrated cause of all UI lag.

## Bounded local scale check

A disposable local PostgreSQL installation received the existing application
migrations and synthetic contact/session fixtures. No WhatsApp client was created.
The real authenticated HTTP routing, membership checks and contact-search handler
were exercised with a missing-name substring search: 32 requests per scenario,
at one and eight concurrent callers, with the API's ten-connection/five-idle pool.

| Synthetic contacts | Callers | Median | 95th percentile |
|---|---:|---:|---:|
| 1,000 | 1 | 4.00 ms | 6.25 ms |
| 1,000 | 8 | 7.42 ms | 252.33 ms |
| 20,000 | 1 | 38.33 ms | 70.34 ms |
| 20,000 | 8 | 65.99 ms | 204.61 ms |

All 128 requests succeeded; the local pool recorded no waits. Small samples,
connection setup, caching and the development computer influence the tails. These
numbers must not be extrapolated into hosted requests-per-second capacity.
They demonstrate the directory-size sensitivity of the existing scan/sort search.
Its current substring implementation already documents this deliberate limit.
Contact uploads also execute up to 100 separate INSERT/UPDATE statements in one
transaction; that path has an avoidable round-trip cost, but no hosted upload
latency measurement was available in this review.

The schema runner's native-process output capture stalled after startup. Migrations
and the scale check completed through direct commands against that same isolated
cluster. The fixture server was stopped successfully. Automatic approval review
rejected recursive removal, so its directory was retained at
`C:/Users/Admin/AppData/Local/Temp/whatsmeow-ipo-schema-187fc094ee2a43828f2fd6477275ae2c`.

## Priorities and remaining evidence

1. Measure per-account queue wait, WhatsApp lookup/send stages, confirmation
   completion and Go SQL-pool waits. Distinguish client cancellation from server
   deadline expiry while retaining existing telemetry privacy protections.
2. Investigate the delayed confirmation and unattempted profile-notification jobs.
   Review account scheduling without weakening session locks, ownership checks,
   durable delivery state or duplicate-message protection.
3. Optimise contact search and batch imports when their measured workload warrants
   it. Current hosted directory reads are fast; the local scale result exposes a
   growth limit rather than a demonstrated current SQL bottleneck.

No Go CPU/function profiles were available in Sentry over seven days, and no live
profiling endpoint or pool-stat export was found in the source. No production
stress test, customer message, session rotation, deployment or resource upgrade was
performed. Full transaction-write throughput and recipient-phone delivery remain
unmeasured. No unexpired access session was available for an authenticated-success
plan; authentication and refresh lookup plans covered misses only. Existing
profile result/deadline/shutdown tests passed; release builds
and unrelated suites were unnecessary for this read-only review.

Evidence links: [profile traces](https://vanitilabs.sentry.io/explore/traces/?project=4512250484883456&query=http.status_code%3A504&statsPeriod=7d),
[Go profiling](https://vanitilabs.sentry.io/explore/profiling/?project=4512250484883456&statsPeriod=7d),
[worker](../backend/whatsapp_service.go),
[delivery](../backend/confirmation_delivery.go),
[profile lookup](../backend/whatsapp_profiles.go),
[contacts](../backend/contacts.go), [pools/startup](../backend/main.go).

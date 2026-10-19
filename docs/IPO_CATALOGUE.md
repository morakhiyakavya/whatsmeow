# IPO catalogue ingestion

The Go backend adapts selected NSE/IPO Watch mappings and category amount logic
from `hedaprateek/ipo-tracker`; see [third-party notices](../THIRD_PARTY_NOTICES.md).
No second Node service, fork, external GMP collector, investment reports or
allotment-odds calculations are included. Business GMP and transaction rates are
independent and are never changed by ingestion.

## Installation

Apply [migration 013](../database/013_ipo_catalogue.sql) after 001–012 as migration
owner, then reapply [runtime grants](../database/api_grants.sql) with the existing
`database_name` and `api_role` variables. Fresh setup/tests include 013; startup
requires it even with ingestion disabled. It adds product fields and five related
tables without deleting products, users, sessions, transactions or history.

Set `IPO_JOB_TOKEN` privately using at least 32 random bytes (43 URL-safe base64
characters). The setting accepts 43–128 non-whitespace characters. The existing
trusted `WHATSAPP_DATABASE_URL` worker connection supplies catalogue writes;
importing itself requires no WhatsApp account, approval or customer send. An empty
token disables execution and rejects triggers. Never expose it in a URL/app/log.

## Trigger and worker

```http
POST /internal/jobs/ipo-refresh
Authorization: Bearer <dedicated job credential>
```

Send no body. Successful `202` returns `id`, `status`, `scheduled_for`. Repeated
requests within a UTC half-hour return the same job. POS tokens cannot authorise
it; invalid credentials return 401 and nonempty bodies return 400, with no-store.
The request commits work before acknowledgement and performs no source HTTP work.
Runtime may insert scheduled times/read results, but cannot forge imported
identities, change job state or write observations; the trusted worker owns writes.

The existing Go process polls every five seconds. A database advisory lock avoids
overlap across processes. Collection has a six-minute budget; saving has 90 seconds.
Source requests use 15-second timeouts, 4 MiB limits and restricted hosts/redirects.
Failed/partial jobs retry after five minutes, at most three attempts. Interrupted
runs are reclaimable after a ten-minute lease; exhausted interrupted jobs fail.
There is no independent half-hour timer: a missed Sentry request is reflected by
freshness; the next trigger imports current data. Acceptance is not completion.
Actual failures reach the existing privacy-filtered Sentry SDK and generic logs.

## Read API

`GET /api/businesses/{businessID}/ipos` retains authentication, membership checks
and keyset pagination. Optional validated filters are `board=Mainboard|SME` and
`active=true|false`. The active view excludes explicitly postponed/withdrawn
issues, confirmed listing dates before today, and issues closed over seven days
ago; dates use Asia/Kolkata. All retained products remain readable without it.

New product fields include provider identity, board/exchange, source status/URL,
lot size, registrar, issue-size wording/shares/amount (INR), last-seen/terms times,
expected timeline values, derived status and `catalogue_stale`. Related arrays:

- `subscriptions`: category/label, offered/bid shares, application count, multiple,
  source, coverage, observation time and staleness. Total is separately published;
  overlapping NII parent/child rows are never added together.
- `reservations`: category, reserved shares, percentage, basis, source URL/time.
- `promoter_shareholdings`: holder, pre/post percentages, source URL/time.
- `anchor_lockins`: published label/date and source URL/time.
- `category_applications`: category, min/max lots, min shares/amount, price basis,
  INR currency and the applicable rule.

Decimals are strings; bounded lot counts are integers. Missing values remain
unknown; empty enrichment arrays mean no observation, not proven absence. Latest
observations are upserted, not a historical quote series. Expected timeline values
remain labelled separately from confirmed opening/closing/listing dates.

Amounts use the upper price band and whole lots. Mainboard minima correctly
respect strict lower boundaries, fixing the upstream exact-boundary rounding.
SME rules apply only from 1 July 2025: Individual Investor is exactly two lots
above INR 200000; NII is more than two lots, with the applicable INR 1000000 split.
Older SME rules or missing price/lot/opening inputs yield unavailable calculations.
See [NSE FAQ](https://www.nseindia.com/static/trade/e-ipo-faqs) and
[circular IPO68604](https://nsearchives.nseindia.com/content/circulars/IPO68604.pdf).

`GET /api/businesses/{businessID}/ipos/import-status` returns `latest_run`,
`last_successful_refresh`, `last_partial_refresh`, coverage and staleness, under
the same membership checks. Results contain status codes/counts, not raw upstream
errors/credentials. Catalogue/subscription observations become stale after 90 minutes.

## Sources and preservation

NSE current/upcoming/recent-past feeds populate the catalogue. Details/enrichment
target open, upcoming within 21 days and closed within seven days. Older products
remain available. Subscription coverage is explicitly NSE-only; complete BSE-only
coverage is unverified. Provider keys match only provider-owned rows; manual
products are not joined by name. Renamed/reused identities require review.
Invalid/partial/empty responses preserve prior data, and failed rows do not block
valid issues. Disappearance does not imply withdrawal. NSE list issue size can
exclude anchors, so its wording is preserved without inventing a total.

IPO Watch candidates are issuer-verified; later recognised tables supersede
template duplicates. Unknown placeholders and unpublished anchor dates stay
unknown; lock-ins are never inferred from listing dates. Source-data rights are
excluded from upstream's code licence and require the operator's arrangements.

## Rollout and verification

After deployment and a controlled import, add a separate Sentry uptime monitor:
`https://whatsapp-pos-dev.onrender.com/internal/jobs/ipo-refresh`, POST, 1800-second
interval, protected Authorization header, empty body, response capture disabled.
Keep the existing health monitor. The user approved the possible USD 1/month
second-monitor charge. Verify entitlement and a scheduled trigger/completed job
before reporting scheduling live; health/202 alone do not prove completion.

Run backend tests/vet, disposable hosted/schema/API/session checks and a deployment
build. Tests cover category boundaries, source/identity failures, missing fields,
restricted grants, deduplication, stable IDs, retention, locking and restart recovery.
`IPO_LIVE_SOURCE_TEST=1` enables read-only `TestIPOLiveSources`; it writes no data.

Rollback starts by disabling the trigger/worker and reverting callers. Review SQL
and grants separately; Git revert does not reverse migrations. Preserve products
referenced by transactions. Job retention is operator-managed; no unrelated
cleanup scheduler is installed.

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
and keyset pagination. It always returns the product-picker lifecycle: upcoming
(including tentative dates), open and awaiting-listing issues, through the listing
day inclusive in Asia/Kolkata. A confirmed listing date takes precedence; otherwise
a valid exact date from the expected listing timeline is used. Once that date is
before today, the IPO disappears from this list. With no usable listing date,
the existing seven-day grace after closing applies; undated upcoming products
remain visible. Explicitly postponed/withdrawn issues are excluded. Filtering
happens before pagination; historical products and transaction references remain
stored. Optional `board=Mainboard|SME` is unchanged. Legacy `active=true|false`
values remain accepted and validated but cannot bypass the lifecycle restriction.

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

Current development rollout (19 October 2026): migration 013 is installed locally
and in Supabase; Render is live on `9f3cfa8`. The protected trigger/deduplication
works, but NSE access from Render is intermittent: early and later runs returned
HTTP 403, while an intervening scheduled run imported 118 issues. Source failure
is visible in job results and Sentry issue `WHATSAPP-POS-API-2`.

At the user's request, a one-off local collection refreshed the hosted database
on 19 October 2026, finishing at 10:41:38 UTC (16:11:38 IST). Manual run
`d430c51a-18d0-4ccf-986e-c37aabbddb50` saved all 118 collected products: 82 Mainboard,
36 SME, all with price bands. The database contains 55 subscription rows, 24
reservation rows, 14 promoter rows, 10 lot/registrar records and eight issue amounts.
Ten products produce category application calculations. Historical products retain
core fields; detailed enrichment targets the active/recent window. No published
anchor dates were available. Status is partial because TONBO and ZETWERK enrichment
was unavailable; all three feeds and ten NSE details succeeded. The manual run is
annotated `execution=local_manual` and excluded from automatic retries. Existing
users/businesses/memberships, POS sessions, transactions/history, retry records,
confirmations and manual products passed before/after checksum verification.

The user enabled monitor `10687890` after resolving its billing-capacity block.
Verified a real scheduled POST at 09:17:02 UTC (14:47:02 IST) on 19 October 2026:
Sentry received HTTP 202 in 474 ms. A brief 60-second verification interval was
then restored to 1800 seconds; the monitor remains active and the separate health
monitor is unchanged. Trigger scheduling works; recurring collection still needs
reliable source access. A later failed scheduled run exhausted its three attempts
without erasing the previously imported products. The manual refresh above is a
temporary population, not a new local scheduler or a source-access resolution.

The separate Sentry uptime monitor uses:
`https://whatsapp-pos-dev.onrender.com/internal/jobs/ipo-refresh`, POST, 1800-second
interval, protected Authorization header, empty body, response capture disabled.
Keep the existing health monitor. The user approved the possible USD 1/month
second-monitor charge. Additional monitors require PAYG, which Sentry only offers
on paid plans; the earlier USD 1 estimate excludes that base subscription. Do not
treat the Developer plan as supporting a USD 1 add-on. See
[monitor billing](https://docs.sentry.io/pricing/quotas/manage-cron-monitors/).
Verify the resulting import separately; health/202 alone do not prove data
collection completed.

Run backend tests/vet, disposable hosted/schema/API/session checks and a deployment
build. Tests cover category boundaries, source/identity failures, missing fields,
restricted grants, deduplication, stable IDs, retention, locking and restart recovery.
`IPO_LIVE_SOURCE_TEST=1` enables read-only `TestIPOLiveSources`; it writes no data.

Rollback starts by disabling the trigger/worker and reverting callers. Review SQL
and grants separately; Git revert does not reverse migrations. Preserve products
referenced by transactions. Job retention is operator-managed; no unrelated
cleanup scheduler is installed.

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
Current source also requires migrations 014 and
[015](../database/015_dashboard_assumptions.sql) before startup. Migration 015 adds
immutable calculation-fact snapshots and separate business assumption history.
Successful catalogue saves capture changed facts atomically; unchanged fetches do
not duplicate them. Business GMP/counts/factors are never changed by ingestion.
Existing installations require an explicit migration; rollout notes below describe
their last deployed version.

The consolidated-subscription increment requires migrations 001–018 followed by
[019](../database/019_combined_ipo_subscriptions.sql). Migration 019 allows the
`NSE+BSE` subscription coverage label and adds nullable `source_observed_at`.
Existing observations keep their original coverage. Apply 019 once as migration
owner before deploying the new backend. Fresh local/hosted setup and the schema
runner include it. This source increment is awaiting production migration and deployment.

Set `IPO_JOB_TOKEN` privately using at least 32 random bytes (43 URL-safe base64
characters). The setting accepts 43–128 non-whitespace characters. Set
`IPO_DATABASE_URL` to the trusted worker connection for catalogue writes; when
omitted it falls back to `WHATSAPP_DATABASE_URL` for existing deployments. Importing
uses its own bounded PostgreSQL pool and requires no WhatsApp account, approval or
customer send. An IPO-only host sets `IPO_DATABASE_URL` and `IPO_JOB_TOKEN` while
leaving `WHATSAPP_DATABASE_URL` unset. An empty token disables execution and rejects
triggers. Never expose credentials in a URL/app/log.

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

Persistence uses batches of at most 100 IPOs, with set-based writes for catalogue
rows, subscriptions and enrichments, followed by dashboard-basis capture in the
same transaction. Database calls remain fixed per successful batch. A rejected
batch rolls back and splits into smaller batches to isolate failed issues while
retaining valid ones; only committed issues contribute to the import count.

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
  source, coverage, fetch observation time, `source_observed_at` when published,
  and staleness. Staleness uses publication time when available, otherwise fetch
  observation time. Mainboard rows prefer `coverage=NSE+BSE`; categories without
  combined observations retain and expose their `coverage=NSE only` counts.
  Total is separately published;
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

Reservations come from recognised allocation columns, excluding subscription/bid
tables. Tables with share counts take precedence over percentage-only duplicates.
Combined `shares (percentage)` cells retain both units. A zero percentage beside
positive shares is omitted as inconsistent; counts are preserved and missing
counts are never inferred from a percentage. Each row retains the source's column
labels in `basis`; denominators are not assumed to match across tables.
A successful nonempty reservation observation removes obsolete IPO Watch category
labels for that issue in the same transaction, while preserving other providers,
immutable earlier snapshots and business inputs. Missing/failed reservation
observations retain the previous set. The next successful import captures the
corrected current set for business-data synchronisation.

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
remain available. Automatic discovery covers NSE-listed issues, including issues
also listing on BSE. The importer contacts NSE and IPO Watch. Provider keys match only provider-owned rows; manual
products are not joined by name. Renamed/reused identities require review.
Invalid/partial/empty responses preserve prior data, and failed rows do not block
valid issues. Disappearance does not imply withdrawal. NSE list issue size can
exclude anchors, so its wording is preserved without inventing a total.

Mainboard subscriptions come directly from NSE's `activeCat.dataList`, the
exchange-published NSE+BSE consolidated book. Header rows are skipped, numeric
fields use `noOfShareOffered`, `noOfSharesBid` and `noOfTotalMeant`, and
`activeCat.updateTime` supplies publication time. The current-issue feed also
provides a published consolidated headline total. Totals and overlapping NII
parent/child rows remain separate. Categories without usable combined rows fall
back to `bidDetails`, preserving `NSE only` coverage, application counts and the
`demandGraph.timestamp` publication time. Existing combined observations are retained
when a later refresh provides only NSE data; their fields are never mixed.

NSE SME issues use the single-exchange `bidDetails` book, labelled `NSE only`.
This retains their published application counts when combined-table placeholders
are empty or zero. Mainboard combined rows have unknown application counts unless
the consolidated source publishes them; NSE-only application counts are never
carried into a combined row. Changing a row's coverage replaces its numeric
fields atomically; sparse updates within the same coverage retain known values.
An NSE-only observation cannot overwrite a combined row, and older or undated
observations cannot overwrite a timestamped combined observation. Mainboard forecast
capacity uses separate published reservation share counts first, then combined
offered-share observations, then NSE-only category observations when no combined
observation is available. NSE-only capacity is a single-exchange estimate.

`IPO_LIVE_SOURCE_TEST=1` enables read-only `TestNSECombinedLiveSources`, which checks
published consolidated demand when a current mainboard issue is available. It
reports a skip when the current feed has no suitable issue.

IPO Watch candidates are issuer-verified; later recognised tables supersede
template duplicates. Unknown placeholders and unpublished anchor dates stay
unknown; lock-ins are never inferred from listing dates. Source-data rights are
excluded from upstream's code licence and require the operator's arrangements.

Each refresh reads the published [IPO calendar](https://ipowatch.in/upcoming-ipo-list/)
once and extracts only issuer names and IPO page links. It does not import GMP.
Link discovery ignores foreign hosts, non-HTTPS URLs, credentials, queries, encoded
paths and GMP pages. Conflicting URLs for the same normalized name are rejected;
candidate discovery never bypasses the page's separate issuer verification.
Verified symbol/legal-name/URL mappings handle TONBO's shortened title, MKSONS's
punctuation-dependent URL and ZETWERK's shorter name/URL. An alias works only for
that exact issuer and source page. Other issues use a discovered link first and
the existing generated URLs as fallback, with duplicate requests removed.

Import results retain specific errors such as `source HTTP 403`, `source HTTP 404`,
`enrichment page missing` (including HTTP-200 not-found pages),
`enrichment issuer unverified` and `enrichment fields unavailable`. Transport
details remain private. Failed refreshes continue preserving prior observations.
`IPO_LIVE_SOURCE_TEST=1` also enables read-only `TestIPOEnrichmentLiveMappings` for
the three verified mappings; it requires reservation and promoter rows from each.

## Rollout and verification

The 25 October Azure importer rollout is recorded in
[the Azure VM guide](AZURE_VM_DEPLOYMENT.md). The refresh monitor now targets the
Azure Indian-region host; Render retains the application/WhatsApp workers with
`IPO_JOB_TOKEN` empty, so it cannot claim catalogue jobs from the shared queue.
The historical Render source-access results below remain relevant: a successful
trigger is not evidence of a completed or fully enriched collection.

Current development rollout (21 October 2026): migration 013 is installed locally
and in Supabase; Render is live on `0571bfb` (linked-device branding update).
The protected trigger/deduplication
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

The separate Sentry uptime monitor now uses:
`https://172-198-153-142.sslip.io/internal/jobs/ipo-refresh`, POST, 1800-second
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

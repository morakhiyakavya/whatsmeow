# Project TODO

## IPO products and user GMP

- [ ] Implement the [catalogue collection contract](PROJECT_VISION.md#ipo-information)
  using the existing Go API/database, with a Sentry uptime POST every 30 minutes,
  validated source coverage, stable issue IDs and preserved historical products.
- [x] Add required IPO terms, expected/confirmed dates, category application
  amounts adapted from the reviewed frontend, source-timestamped subscription and
  reservation data, promoter shareholding and anchor lock-in dates through
  migrations and the existing catalogue API; verify category boundaries and
  Mainboard/SME differences. Follow the
  [final delivery plan](PROJECT_VISION.md#final-delivery-plan-19-october-2026).
- [x] Add the backend business-shared GMP, expected application-count and Subject 2
  factor/history API, with migration 015, source-fact snapshots, admin-only writes,
  cashier reads, business isolation and concurrent-version checks.
- [x] Apply migration 015/runtime grants locally and on Supabase; deploy the tested
  backend on the existing Render service and verify health/authentication boundaries.
- [ ] Build user controls for GMP, expected application counts and Subject 2 factor.
- [ ] Implement the agreed [application rate guidance](PROJECT_VISION.md#application-rate-guidance-agreed-implementation-pending)
  with the workbook's Subject 1 probability-based rate and Subject 2 GMP-value
  factor. Give users an adjustable Subject 2 factor starting at 80%; changes affect
  suggestions, not saved transaction rates, amounts or share quantities. Include
  the input controls and business-isolated GMP/forecast settings when delivering
  this feature; its API and screens remain unimplemented.

## IPO positions and history

- [x] Document the [current dashboard requirements](IPO_POSITION_REPORTING.md),
  business management controls, expected application-count inputs, daily closing
  preservation and NSE-first/BSE-fallback reference without payment tracking.
- [x] Draft the [additive database and API design](IPO_ANALYTICS_DESIGN.md), reusing
  the ledger/versioning and distinguishing unknown actual costs from bank payments.
- [ ] Implement the [position and reporting contract](IPO_POSITION_REPORTING.md):
  cumulative net position first; buy/sell details and Retail, Retail 199, SHNI,
  BHNI and Shareholder expected-share contributions on request, all reconciling.
  Shareholder is conditional on the IPO's quota; distinguish offered/not offered/
  unknown, do not require an inapplicable forecast, and retain existing history.
- [ ] Add daily trading activity and historical closing positions using transaction
  versions and dated assumptions/IPO inputs. Lock closing GMP, expected application
  counts and other calculation inputs with the day's position, average cost and
  estimated profit/loss; compare today's trades/current average without rewriting
  yesterday's record or adding its estimated profit to today's result.
- [ ] Add manual confirmed-allotment reconciliation, already-allotted application
  share conversion and an automatic/manual first-trade listing reference: NSE first,
  BSE fallback. Collect category forecasts as expected total application counts.
  Payment/collection tracking and after-listing settlement are outside this scope.
- [ ] Deliver the design incrementally: assumptions/source history and admin API;
  shared exact calculations and dashboard/category/detail reads; allotted-share
  capture/reconciliation; daily closes/history; verified listing reference/result.

## Production business onboarding

- [ ] Give the project owner a platform-level master-admin role, separate from
  business admin/cashier membership. The owner will create and authorise businesses.
- [ ] Add master-admin-controlled business provisioning and staff invitation or
  assignment; WhatsApp ownership alone must not authorise access to a business.
- [ ] Keep one WhatsApp login/signup flow using the existing start/status/exchange
  endpoints as this model evolves; do not introduce a separate signup flow.
- [ ] Replace development-created business/profile placeholders through an
  explicit transition that preserves user IDs, account IDs, sessions and history.

For now, only `APP_ENV=development` enables automatic signup. A verified new number
receives its own isolated development business and business-admin membership;
it does not receive platform master-admin privileges. Production and unset
`APP_ENV` require provisioned staff. Business creation and authorisation by the
project owner remain future work, as requested.

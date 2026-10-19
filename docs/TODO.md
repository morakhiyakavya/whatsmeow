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
- [ ] Add business-shared, user-maintained GMP with attribution/history and
  business isolation; agree write permissions and quote types before implementation.

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

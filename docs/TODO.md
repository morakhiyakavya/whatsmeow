# Project TODO

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

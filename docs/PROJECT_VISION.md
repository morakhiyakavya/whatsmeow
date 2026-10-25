# Project vision: WhatsApp-connected IPO transaction platform

## What I am building

I want to build a web and mobile application for businesses that handle IPO-related form transactions. The application should record transactions entered by staff and automatically identify transactions from private WhatsApp conversations using AI and business-defined rules.

It should bring customer information, IPO information, transaction records, WhatsApp conversation evidence, and transaction history into one system. PostgreSQL will store the business data and linked WhatsApp sessions, while a Go backend will serve the web and mobile applications.

The system handles both buying IPO forms from customers and selling them onward.
Use **transaction** throughout. Direction comes from the signed quantity, from the
business's perspective: positive means Buy, negative means Sell, and zero is invalid.
Rate may be positive, negative or zero. The calculated total is the rounded product
of quantity and rate; neither rate nor total determines direction. No separate
Buy/Sell field or position is required.

## Two ways to create a transaction

### 1. Automatic detection from WhatsApp

- A business links its WhatsApp account through Linked devices.
- The server receives incoming and outgoing messages from that account.
- Only readable text from private 1:1 chats is relevant. Groups, broadcasts, statuses, newsletters, media, reactions, and media captions are excluded.
- A potential transaction message is evaluated together with the five immediately preceding readable texts from the same private conversation, when available: up to six messages including the trigger. Use their timestamps to understand when the transaction happened. The existing terminal viewer's ten-message buffer is a diagnostic implementation, not the detector's required context window.
- AI extracts possible transaction details from the conversation.
- Business-defined rules decide whether the transaction should be recorded automatically.
- Accepted transactions are created automatically; mandatory manual approval is not the default workflow.
- Incomplete, ambiguous, or rejected detections retain their decision records without creating an unjustified transaction.
- Later messages about an existing transaction must be reconciled with that transaction rather than automatically creating another one.

The WhatsApp integration reads conversations to identify transactions and sends a customer message when a staff member creates a manual transaction.

### 2. Manual entry from the web or mobile app

Staff can select the IPO and customer, enter the form details, quantity, rate, and transaction time, and create the transaction through the Go API.

After the manual transaction is successfully saved, the Go API sends a WhatsApp message to the selected customer. The sender is the WhatsApp account linked from the mobile phone of the user creating the transaction. The backend sends through that user's linked session, so the customer receives the message from the user's WhatsApp number.

The outgoing message must be associated with the existing manual transaction. When it appears in the conversation stream, automatic detection must not create a duplicate transaction from that message.

Both methods use the same transaction records, calculations, validation, and version-history mechanism. The system records whether the original transaction came from manual entry or automation.

## IPOs, forms, and categories

### IPO information

IPOs are the products in a shared catalogue. The existing database stores their
identity, names, confirmed opening/closing/listing dates, optional price band and
extra metadata. Migration 013 and the catalogue API now support the expanded
fields and durable refresh workflow below. See the
[implementation and operating guide](IPO_CATALOGUE.md) for coverage and rollout.

The required collection scope is:

| Data | Required representation |
|---|---|
| Identity | Stable internal IPO ID, issuer/name, source issue identifier, exchange/symbol where available, Mainboard or SME |
| Price band | Exact decimal lower/upper prices; distinguish fixed-price issues and unknown/unannounced prices |
| Overall subscription | Subscription multiple, with underlying offered/bid shares when published, source coverage and observation time |
| Subscription by investor category | QIB, total NII/HNI, sNII/SHNI, bNII/BHNI, Retail or SME Individual Investor, and issue-specific reservations when available; preserve source labels |
| Dates | Opening, closing and listing; preserve expected dates/windows separately from confirmed dates and leave unknown dates unknown |
| Base lot size | Shares per exchange bid lot |
| Category application requirements | Applicable minimum/maximum lots, corresponding shares and amount for Retail/Individual Investor, SHNI and BHNI; retain the rule and price basis when calculated |
| Investor reservations | Reserved share counts and percentages by category, with the stated issue/net-offer basis; unavailable values remain unknown |
| Promoter shareholding | Published pre-IPO and post-IPO percentages, with the source's holder labels and observation time |
| Anchor lock-ins | Published expiry dates and associated allocation portions when available; do not invent dates from an assumed listing-date offset |
| Registrar | Issuer-appointed registrar name and official allotment link when available; separate from listing exchange |
| Issue size | Share count and monetary amount with explicit currency/units; distinguish total issue, fresh issue and offer for sale when available |

Investor subscription categories are separate from the transaction's
None/Subject 1/Subject 2 category. A base lot size is not a different share count
for every investor category: category application limits refer to multiples of
that lot. Preserve issue/board-specific rules and effective dates rather than
applying Mainboard retail assumptions to SME issues. For example, NSE's current
[e-IPO FAQ](https://www.nseindia.com/static/trade/e-ipo-faqs) distinguishes SME
Individual Investors from NII by the number of lots; see also
[circular IPO68604](https://nsearchives.nseindia.com/content/circulars/IPO68604.pdf).
Do not sum overlapping NII parent/subcategory rows or subscription multiples.
Unavailable subscription is unknown, not zero; distinguish exchange-only figures
from consolidated figures before displaying a total.

Adapt the reviewed repository's frontend `categoryOptions` calculation into one
shared Go calculation used by the catalogue API. Return minimum lots, shares and
application amount with the price basis and applicable category rules. Preserve
strict/inclusive amount boundaries correctly, use exact decimal arithmetic and
validate Mainboard/SME differences before reuse. Missing price/lot/rule inputs
produce unavailable calculations. Do not copy its investment recommendations or
allotment-odds estimates along with the amount calculation.

Useful additional fields to confirm are allotment/refund/demat dates, final issue
price, face value, prospectus/RHP links and issue status.
These are candidates, not a requirement to collect every company financial metric.

### Catalogue collection and lifecycle

- Use **Sentry Uptime Monitoring** to send an authenticated POST every **30
  minutes** to the dedicated endpoint on the Indian-region Azure VM. Render
  retains the application/WhatsApp workers, with its importer disabled. The uptime
  request triggers the backend importer on Azure;
  Sentry does not host its code. No GitHub Actions schedule or paid Render cron
  is required for this selected design. See the
  [Sentry request options](https://docs.sentry.io/product/monitors-and-alerts/monitors/uptime-monitoring/#configuring-an-uptime-monitor).
- `POST /internal/jobs/ipo-refresh` uses a dedicated, rotatable job
  credential in a header, not a phone user's POS session or URL parameter. Persist
  accepted work before returning a fast acknowledgement; imports run in the
  existing Go process outside the request lifetime. Deduplicate repeated triggers,
  prevent overlapping imports and resume interrupted work after restart with
  bounded retries/timeouts. Manual verification requests must obey the same rules.
- Store run/source results, last successful refresh and freshness. Request
  acceptance is not import completion: capture actual import failures in Sentry
  and expose stale/partial data through the catalogue API. A delayed/missed uptime
  request must be visible, and the next accepted trigger refreshes current data.
- Discover new issues and refresh existing terms, dates, status and subscriptions.
  Target active/upcoming and recently closed issues for ongoing updates; retain
  older records and final observations for transaction history. Lifecycle states
  include upcoming, open, closed/awaiting listing, listed, postponed or withdrawn
  when supported by evidence. Leaving the active list does not delete the product.
- Reuse the existing Go backend, IPO catalogue and read API. Selectively adapt
  collection logic and the category-amount calculation from the reviewed
  [ipo-tracker repository](https://github.com/hedaprateek/ipo-tracker), retaining
  required notices for copied code. Do not depend on its published JSON or import
  its investor dashboard, GMP scraper or investment-report features. This is
  selective adaptation into our repository, not a fork or a second Node service;
  upstream fixes must be evaluated and ported deliberately.
- Select permitted sources and verify coverage of both Mainboard and SME issues,
  including NSE/BSE coverage, before claiming a complete catalogue. Preserve
  source references, provider timestamps where available, local fetch times and
  per-source/run results. Confirmed and tentative dates must remain distinguishable.
- Validate parsed shapes, identities, dates, decimals and units before upserting.
  Keep internal IDs stable and avoid automatic name-prefix joins. Partial/empty
  responses or missing fields must not erase valid data, imply withdrawal or
  recreate a product. Surface stale/partial coverage and parser failures for repair.
- Keep changing subscription observations separate from product identity. Add
  relational fields/observations through migrations as the source contract is
  implemented; JSON metadata is for extras, not the only definition of core fields.
- Before delivery, check parser fixtures for Mainboard/SME and schema changes,
  tentative dates, category/amount boundaries, repeated imports, partial failures,
  restart scheduling, stale data and preservation of transaction references.

### Final delivery plan (19 October 2026)

1. **Source fixtures and field mapping:** validate current NSE issue/detail data
   and IPO Watch reservation, promoter-shareholding and anchor-lock-in data.
   Record which required values are available and which need another permitted
   source, including coverage gaps for BSE/SME and tentative dates. Keep missing
   values explicit. Reuse only the selected code, retaining required notices.
2. **Database and catalogue API:** extend existing IPO records and related data
   through additive migrations. Preserve stable IPO IDs, transaction references
   and manual data provenance; store source/coverage/timestamps and import state.
   Update existing catalogue reads for the agreed fields and calculated category
   amounts, reusing current authentication and decimal-string conventions.
3. **Collector and calculation:** port the selected extraction/mapping logic into
   Go with bounded requests, source validation and safe upserts. Implement one
   exact-decimal category calculation and test strict amount boundaries and board
   differences. Record partial failures without erasing valid prior observations.
4. **Trigger and execution:** implement the protected endpoint, durable job state,
   non-overlapping worker, restart recovery and run/freshness diagnostics in the
   existing backend. Test duplicates, invalid credentials, cancellation, timeouts,
   failures and interrupted runs with synthetic data before live configuration.
5. **Rollout:** after the implementation is reviewed and verified, apply the
   migration, deploy to the existing Render service and configure one Sentry
   uptime monitor with POST, a protected header and a 30-minute interval. Verify
   a controlled import and subsequent scheduled triggers, final stored data and
   error reporting. Configure actual uptime entitlements and alert settings then;
   the plan itself creates no resources or credentials and performs no deployment.

Completion means the agreed data is readable through the catalogue API, category
amounts are correct, scheduled triggers produce completed imports, source failures
and stale results are identifiable, and existing transactions and business GMP
remain intact. This increment does not implement user GMP-entry screens/API,
investment reports, allotment checking, or a replacement web/mobile dashboard.

### User-maintained GMP (backend deployed; screens pending)

GMP is normally entered or updated by users and **shared within their business**.
It is separate from the global IPO catalogue: one business's value must not change
another business's value. Keep the supplying user, effective/recorded time and
change history with each update. Business admins manage shared GMP and forecasting
inputs; cashiers use/read them without changing them. Whether one or multiple GMP
quote types are needed remains to be agreed beyond the initial single GMP value.
Migration 015 and the backend assumptions GET/PUT/history endpoints implement these
business-isolated settings, with database-enforced admin writes, exact validation
and immutable revisions. They passed disposable database checks; migration 015
and runtime grants are installed locally and on Supabase. The API is deployed on
Render; user screens remain pending.
External GMP collection is outside the initial importer scope. A catalogue refresh
must never overwrite user GMP or an agreed transaction rate. IPO price band, GMP
and transaction rate remain distinct values, and neither lot size nor market-data
updates change the agreed transaction amount calculation.

### Form types

The initial form types are:

- Retail
- Retail 199
- SHNI
- BHNI
- Shareholder
- Shares
- CE
- PE

These are catalogue entries. Transaction-specific numbers are not separate catalogue combinations.
Shareholder availability is IPO-specific: the form type remains in the shared
catalogue, but its forecast/pricing fields are applicable only when the IPO offers
a shareholder quota. Missing source data leaves availability unconfirmed, not
automatically absent. Preserve previous Shareholder transactions and history.

### CE and PE values

Examples include `20 CE`, `40 PE`, and potentially fractional values such as `20.5 CE`.

- CE or PE is the form type.
- The number is a separate `ce_pe_value` on the transaction.
- It is mandatory when the form type is CE or PE.
- It supports decimal values even though values will usually be integers.
- It is preserved in both the current transaction and every transaction version.
- It is not a quantity, units field, or multiplier in the transaction amount.

### Categories

Each transaction has one category:

- None
- Subject 1
- Subject 2

Subject 1 and Subject 2 will have different calculations in a future analytics module.
For application types, None means already allotted and contributes known shares
without a forecast probability reduction. These share/settlement calculations do
not change the existing quantity-times-rate transaction amount contract. See the
[position and reporting specification](IPO_POSITION_REPORTING.md).

### Application rate guidance (agreed; implementation pending)

Follow the reviewed workbook's rate formulas. These are suggested application
rates, separate from an entered transaction's agreed rate or amount. Users must
be able to increase or decrease the Subject 2 pricing factor; its initial value
is 80%. Do not reinterpret that input as an allotment probability or guaranteed
profit margin, or introduce a separate rupee-profit deduction into these formulas.

Let `Q` be shares per successful application, `G` the applicable GMP in INR per
share, `p` the category's forecast allotment probability and `f` the Subject 2
pricing factor expressed as a fraction (80% means 0.8):

```text
Subject 1 suggested rate = ROUND(Q * G * p, 0)
Subject 2 suggested rate = ROUND(Q * G * f, 0)
```

In the workbook, `p = required applications / expected applications`. The inputs
must represent comparable counts for that category and a valid probability;
missing inputs or an invalid denominator must remain unavailable, not become
zero. The probability model still needs issue/category-specific validation before
implementation. The Subject 2 rate is conditional on successful allotment, so its
formula does not additionally multiply by `p`. Keep whole-rupee rounding as in the
workbook; preserve signed GMP and rate values without converting them to absolutes.

For the workbook's retail buy example, `Q = 12`, `G = 240` and
`p = 7086444 / 9000000`: Subject 1 is INR 2268 and Subject 2 at 80% is INR 2304.
Changing only the Subject 2 factor gives:

| Subject 2 pricing factor | Suggested rate (INR per successful application) |
|---|---:|
| 60% | 1728 |
| 80% | 2304 |
| 100% | 2880 |

Label the control **Subject 2 pricing factor (% of GMP value)**. For positive GMP,
explain that a lower factor quotes a lower application rate and leaves more of the
modeled premium with the business; a higher factor does the opposite. At 80%, the
remaining 20% is a share of modeled premium value before costs and price changes,
not an 80% profit margin or a guaranteed return. Do not describe negative-GMP
results using the same positive-premium profit claim.

Changing this control updates Subject 2 rate suggestions only. It must not change
Subject 1 suggestions, forecast or confirmed share quantities, previously agreed
transaction rates, transaction amounts, or historical settlements. Market-data
refreshes must not overwrite the user's pricing factor. Keep business-specific
assumptions isolated; setting persistence and write permissions belong with the
planned business GMP/forecast workflow.

Apply the shared calculation consistently to each supported category and applicable
buy/sell quote. Reuse the mathematical pattern for Shareholder rather than copying
the workbook's blank-cell references. Retail 199 and Shareholder share quantities
must be defined from the actual issue/allotment model, not borrowed from another
category. The workbook's existing totals do not depend on its pricing-factor cells;
the app must preserve that separation between guidance and contracted amounts.

Before implementation is complete, verify workbook parity, lower/default/higher
factors, whole-rupee rounding, zero/negative GMP, missing/invalid forecast inputs,
business isolation, and preservation of saved transaction amounts. GMP/forecast
storage from migration 015 and its settings API are implemented and deployed;
the calculation/pricing API and user controls remain planned.

### Position and historical reporting (agreed; implementation pending)

Show cumulative net positions per IPO first, with purchases/sales and category-level
expected shares available on request. Business admins enter expected application
counts by category; the app derives the forecast probability and shares. Preserve
daily closing GMP, other calculation inputs, quantities, average cost and estimated
profit/loss so new trades and forecasts do not overwrite yesterday's record. Support
daily activity and comparisons with the previous closing position. Use NSE's first
trade as the listing reference and BSE's first trade as fallback, with automatic
collection or attributed manual entry. Shared assumptions and pricing factors are
admin-controlled within each business. The [position and reporting specification](IPO_POSITION_REPORTING.md)
defines these views and allotted trades. Payments, outstanding balances and after-
listing settlement are handled separately by the user and are outside this scope.
The assumptions/history API and source-basis storage are implemented and deployed;
reporting, reconciliation, closing and listing APIs/screens remain planned.

The [database and API design](IPO_ANALYTICS_DESIGN.md) proposes additive versioned
inputs, manual coverage, daily closes and authenticated contracts for this dashboard.
The assumptions/source-basis portion is applied; the remaining contracts are planned.

## Transaction details and amount calculation

A transaction records:

| Detail | Meaning |
|---|---|
| IPO | The IPO concerned |
| Customer | The customer involved, including the customer from whom forms are bought |
| Transaction time | When the transaction happened |
| Form type | Retail, CE, PE, or another supported type |
| Category | None, Subject 1, or Subject 2 |
| CE/PE value | The transaction-specific numeric value, where required |
| Number of forms | Signed quantity: positive for Buy, negative for Sell; integers and fractions are supported, zero is invalid |
| Rate | The agreed rate for this transaction |
| Total amount | Rate multiplied by number of forms |
| Responsible staff | The user associated with the transaction, when assigned |
| Creation source | Manual entry or automatic detection |

The agreed calculation is:

```text
total = rate × number_of_forms
```

There is no additional units multiplier. The CE/PE value and subject category do not change this calculation.

Example:

```text
Form type:       CE
CE/PE value:     20
Category:        Subject 1
Number of forms: 2.5
Rate:            250
Total:           625
```

Numeric quantities and rates should be stored as exact decimals rather than binary floating-point values. The current schema uses six decimal places for quantities/rates/CE-PE values and rounds totals to two decimal places. These are initial implementation choices; the final precision and rounding requirements should be confirmed before deployment.

## Customers and referrals

Customer records include:

- Customer name.
- Contact number, when known.
- The customer who referred them, if the referrer is already in the system.
- A referral note/name when the referrer is not an existing customer.

Customer names are not assumed to be unique. Customer information belongs to a business and should not be exposed to another business.

Staff may share selected mobile contacts with their business after granting phone
contacts permission. Match by international phone number within that business and
preserve each staff member's supplied name separately, with supplier ID, source and
server timestamps. This lets a colleague resolve a number they have not saved.
Contact dropdowns display the requesting staff member's uploaded name first, or
the latest colleague contribution when they have none. They search displayed names
and numbers as staff type. The POS customer name takes precedence over imported
customer suggestions; different names or customers sharing a number must not be
silently merged. Staff may withdraw
their own contributions. Permission collection remains the mobile app's responsibility.

## Businesses, staff, and account access

The platform supports businesses with multiple staff rather than only one independent shop per user.

Staff records include a name and contact number. The initial roles are **admin** and **cashier**, assigned within each business. The manual API implements a baseline permission matrix: both roles can create/view customers and create/view/edit/cancel transactions; admins also create/view staff and create IPO catalogue entries. The deployed assumptions settings endpoints enforce admin-only GMP, forecast and pricing-factor edits, with cashier read/history access. Their user screens remain pending.

Passwordless first-device login now approves WhatsApp phone-number linking for a
provisioned staff user. In `APP_ENV=development`, new numbers can sign up through
those same login endpoints after WhatsApp approval, receiving an isolated development
business and its admin membership. Production requires provisioned staff. The
project owner will become master admin and create/authorise businesses as the
platform scales; this is tracked in [TODO.md](TODO.md). Several POS devices share
that user's one backend WhatsApp connection, with separate renewable sessions.
The original signed-in device can
display a two-minute, one-use token for another device; no second WhatsApp pairing
is performed. Users who lose every POS session can approve a fresh recovery through
a primary-phone WhatsApp self-chat challenge; successful exchange replaces POS
sessions/push registrations while retaining the existing link, memberships and
transaction identities. See the [Android recovery contract](ANDROID_ACCOUNT_RECOVERY.md).
Removal from WhatsApp's Linked devices invalidates the user's POS
sessions when detected, preserving users and history. Device-only logout, all-POS
logout and WhatsApp disconnect are distinct actions. Temporary network loss keeps
login. Backend integration checks passed; live passwordless login and mobile
screens remain unchecked. The legacy 12-hour password login is an opt-in local
bootstrap/migration feature, disabled by default. The detailed flows and
identity, membership, logout and receipt-queue safeguards are in the
[mobile authentication guide](POS_AND_CONTACTS_GUIDE.md#mobile-authentication-and-whatsapp-login).

The web and mobile applications communicate with the Go backend. Account credentials and PostgreSQL connection settings remain on the server.

## WhatsApp connection and session persistence

- Web onboarding should support a QR code approved through WhatsApp's Linked devices screen.
- Mobile onboarding should also support phone-number pairing codes, making linking possible when the application and WhatsApp are on the same phone.
- Saved WhatsApp credentials, encryption sessions, contacts, and sync state should live in PostgreSQL through whatsmeow's SQL store.
- The application should reconnect using the saved account session after a restart.
- Revoked or invalid sessions require relinking.
- Application account IDs remain stable even if a linked-device credential is replaced.

The current implementation has PostgreSQL-backed terminal QR login and authenticated backend QR/phone-code pairing endpoints with PNG rendering, stable account mapping, saved-session reconnection and revocation handling. Web/mobile pairing screens remain planned.

Authenticated account owners can check whether one international phone number is
registered on WhatsApp through their existing connected account. Successful
results are stored once per business/number with a check timestamp and account
provenance, and are readable through the shared-contact lookup. Unchecked numbers
remain unknown and failed lookups preserve prior observations. Stored status is
not proof of number ownership and does not replace a fresh registration check.

## AI decisions and transaction history

### Automatic transaction evidence

For an automatic detection, retain:

- The exact conversation text supplied to AI.
- Structured input message snapshots, including direction and revisions.
- Extracted transaction details.
- The AI decision and explanation.
- Business-rule evaluation results.
- Model, prompt, and rule versions.
- A link to the transaction version produced by an accepted decision.

This should make it possible to answer: **Which messages caused this transaction to be recorded, and why did the system accept it?**

### Transaction changes

`transactions` holds the current record. `transaction_versions` preserves complete snapshots, including the original version.

Every edit should preserve the earlier state and record the new state, change time, change actor, and reason. Original creation source should remain intact even when a cashier corrects an automatically created transaction.

Repeated message delivery, retried AI jobs, and repeated API requests must not create duplicate records. Separate orders in the same conversation must still be allowed; identifying whether messages refer to the same order is an explicit part of the detection workflow.

## Scalability principles

- Keep core business records relational and ownership explicit.
- Enforce relationships, required fields, numeric validity, and retry identities in PostgreSQL.
- Preserve transaction history automatically, with current state and its snapshot written atomically.
- Use expected version numbers to prevent simultaneous edits from silently overwriting one another.
- Index common business, IPO, customer, conversation, and date searches.
- Use bounded conversation context for routine processing without assuming an entire order always fits in that window.
- Keep AI extraction separate from rule evaluation and transaction writes.
- Version rules and AI inputs so later changes remain traceable.
- Add new fields and capabilities through migrations as concrete requirements emerge.

Scalability here means a clear model that can grow safely; it does not imply implementing every future feature immediately.

## Current progress

Implemented and tested:

- Go setup and a runnable private WhatsApp text viewer.
- QR pairing, reconnection handling, sender names/numbers when available, and incoming/outgoing text filtering.
- Up to 10 older texts of conversation context, subject to WhatsApp history availability.
- A PostgreSQL application schema for IPOs, customers, staff, transactions, versions, messages, and automation evidence.
- Database-enforced transaction amount calculation and automatic immutable version snapshots.
- PostgreSQL WhatsApp session support with explicit account selection.
- Isolated PostgreSQL tests using synthetic account data, including persistence across reopen and account-specific contacts.
- A runnable Go HTTP API with phone/password sign-in, bcrypt password hashes, PostgreSQL-backed bearer sessions, logout, expiry, and business membership checks.
- Passwordless number-linking login for provisioned staff, linked access/renewal sessions, original-device one-use tokens, own-device session management and automatic revocation; one active WhatsApp link per user/number is enforced. Legacy password login is explicitly opt-in; mobile and live passwordless checks remain next.
- Customer/referral entry, staff creation, shared IPO/form catalogues, and manual transaction creation/editing/cancellation/history through authenticated APIs.
- Authenticated shared contact-name upload, number lookup and withdrawal, with staff provenance and customer-name precedence; mobile permission screens remain to be built.
- Owner-only contact-profile lookup through a connected WhatsApp account returns synced names, available About text, a photo URL with hidden/unset states and a persistent revision. Picture events and observed visibility changes queue account-scoped FCM invalidations for session-bound Android installations; sending requires private server credentials. See [the Android handover](FCM_ANDROID_HANDOVER.md). Privacy restrictions are respected; Android integration and live delivery remain unchecked/unimplemented.
- The transaction POST accepts an owned paired WhatsApp sender and saves a composed confirmation intent atomically with the transaction; retries reuse it and the mobile API can read its status. The connected worker sends it and records acknowledgements or uncertain failures; restart, cancellation, changed-data and disabled-member paths have isolated tests. Live receipt sending remains unchecked.
- Exact decimal strings at the API boundary, creation idempotency with original request fingerprints, optimistic edits, and bounded pagination/filtering.
- Local `ipo_platform` PostgreSQL database setup with separate owner/runtime roles and randomly generated credentials in an ignored, access-restricted root `.env`.
- Isolated API integration tests under a restricted database role, including concurrent creation retries, business isolation, permissions, and immutable transaction history.
- Authenticated WhatsApp account registration/list/detail APIs: stable account IDs, business ownership and the signed-in registering user; restricted runtime grants prevent callers from claiming session bindings.
- Backend QR/phone pairing, owner-only code/PNG retrieval, verified bindings, saved-session recovery and revocation handling; cross-process account locks and isolated protocol lifecycle tests. Live phone-code pairing and automatic restoration after a backend restart were verified locally on 2026-10-10.

Still to implement or connect:

- User screens for deployed business-shared GMP, application forecasts
  and pricing-factor/history API, separate from catalogue ingestion.
- Remote deployment configuration. Local PostgreSQL-backed WhatsApp pairing and saved-session restoration are configured and verified.
- Password reset, master-admin-controlled production business onboarding, staff membership management, and expansion of the baseline admin/cashier permissions; see [TODO.md](TODO.md).
- Web/mobile pairing screens on the implemented QR/phone API and deployment hardening of the trusted worker credentials/capacity.
- Persistent WhatsApp ingestion into the application message archive; the manual business APIs are implemented.
- Live validation of the implemented manual-transaction WhatsApp confirmation flow and a reviewed retry workflow for uncertain/failed sends.
- AI integration, transaction extraction, reconciliation, and business-rule execution.
- Web and mobile interfaces for linking accounts, viewing conversations, entering transactions, and inspecting history/evidence.
- Phone-number pairing screens in the application UI; the backend and local helper now support phone codes.
- Subject 1/Subject 2 analytics, including the agreed [application rate guidance](#application-rate-guidance-agreed-implementation-pending)
  and user-adjustable Subject 2 pricing factor.

The previous SQLite `session.db` has been preserved. Its real account data has not been migrated to PostgreSQL. A PostgreSQL-backed first login currently requires fresh pairing.

## Next delivery sequence

Keep each milestone in a separate local commit after its relevant checks pass:

1. **Account registry (implemented):** register and inspect accounts through authenticated business routes. Session identity comes from the trusted pairing worker.
2. **Authenticated pairing (backend implemented and phone-tested):** owner-only QR/phone codes and QR PNGs, verified bindings, saved-session recovery, stable IDs through relinking and database account locks. Live phone-code pairing and automatic restoration passed locally; live QR pairing and device revocation remain unchecked.
3. **Mobile POS API and confirmation delivery (backend implemented):** the authenticated transaction API records a confirmation intent with the transaction and the worker sends from the creating user's verified linked account. Delivery status is separate from transaction persistence; live sending and mobile integration remain next checks.
4. **Transaction detection context:** reuse the viewer's private-text filtering and identity normalization when connecting the detector; assemble the potential transaction message and five immediately preceding texts from the same account/conversation. Retain the evidence needed for POS decisions rather than prioritising a general conversation browser.
5. **AI extraction and reconciliation:** determine whether a transaction actually happened and when, retain exact evidence, validate versioned business rules, and reconcile follow-up messages before automatic transaction writes. A manual confirmation must never produce another transaction for its existing transaction.

The web/mobile screens can then build on these contracts. Subject analytics remain
planned; application rate guidance is agreed above, while the complete forecast,
manual allotment reconciliation and listing-result calculation still need implementation.

### Mobile POS and WhatsApp confirmations

The immediate product outcome is a mobile POST that records a transaction and causes a
WhatsApp confirmation to be composed and sent. The existing
`POST /api/businesses/{businessID}/transactions` route, authentication, validation,
decimal handling, idempotency and immutable transaction history are the foundation.
The backend extends these existing implementations and has isolated database/sender
tests. The delivered increments are:

1. **Mobile API readiness.** Reuse the reviewed login/session expiry and logout,
   active user/business authorisation, payload validation, request limits and
   idempotent transaction writer. The request/response contract is documented. Require HTTPS
   for deployment and protected token storage in the mobile app; a shared secret
   embedded in every app installation is not user authentication. Identify the
   selected customer and the creating user's authorised linked account explicitly.
2. **Transaction and confirmation intent.** Save the transaction and its durable pending
   confirmation in the same database transaction. Generate the message from saved
   transaction data as `{ipo} ipo {Buy|Sell} {forms} {form} {optional category} at {rate}`;
   use the absolute quantity for forms and preserve the rate's sign. Resolve the
   recipient from the selected customer. CE, PE and SHARES omit category; RETAIL,
   RETAIL_199, SHNI, BHNI and SHAREHOLDER display `alloted` for category `none`. Repeated mobile requests
   with the same idempotency key must reuse the transaction and confirmation intent.
3. **Delivery and status.** Use the existing account worker and whatsmeow sender to
   send after database commit. Check sender ownership and eligibility, record the
   WhatsApp message ID and delivery state, and expose that state to the mobile app.
   Keep transaction success distinct from pending/failed messaging. There is one send
   attempt; uncertain acknowledgements/interrupted attempts are recorded for review
   without an automatic resend. Changed/cancelled transactions or changed recipient/sender
   data block an unsent receipt.

Acceptance checks: invalid/expired/revoked sessions and cross-business requests are
rejected; another user's account cannot be used as sender; totals come from saved
data; concurrent/replayed POSTs reuse one transaction and confirmation intent; pending
delivery survives restart; WhatsApp failure preserves the transaction and reports status.
Use isolated database and fake sender tests first. Sending to a real customer needs
an explicit test recipient and deliberate test transaction; never put customer messages,
credentials or linked-device keys in Git or test output.

Transaction detection follows this increment. Shared filtering is an implementation step
when the detector needs it, not a separate POS deliverable. A full message browser,
historical backfill, attachments and unrelated viewer refactoring are deferred.

## Decisions still open

- The exact acceptance, amendment, cancellation, and duplicate-order rules for automation.
- What the CE/PE numeric value should be called in business-facing UI.
- Expansion of the implemented phone/password authentication and baseline role permissions for deployment and additional workflows.
- Frontend technologies for web and mobile.
- Final numeric precision, currency, and rounding requirements.
- Final catalogue source/coverage contracts, optional IPO fields and GMP quote
  types. Expected application counts, NSE-first/BSE-fallback listing references
  and admin-only business assumption edits are agreed; required IPO fields,
  30-minute collection cadence, rate guidance and reporting behaviour are specified
  above. Payment/collection handling is outside the app's current scope.

Net positions, historical reporting and manual allotment reconciliation are agreed
future analytics requirements, not implemented ledger features; see [the reporting
specification](IPO_POSITION_REPORTING.md). Payment tracking, collections, after-
listing settlement and media processing remain outside the current app scope.

## Related project files

- Database design: `docs/POS_DATABASE_DESIGN.md`
- Initial PostgreSQL schema: `database/001_initial.sql`
- Viewer and local PostgreSQL setup: `examples/login/README.md`
- Schema/session validation runner: `database/Test-Schema.ps1`
- Manual API, permissions, setup and usage: `backend/README.md`
- API authentication/idempotency migration: `database/002_api.sql`
- WhatsApp account pagination migration: `database/003_whatsapp_accounts.sql`
- Shared contact names and customer attribution migration: `database/004_contact_names.sql`
- Durable transaction confirmation migration: `database/005_transaction_confirmations.sql`
- Passwordless session/link revocation migration: `database/006_passwordless_sessions.sql`
- Phone-number registration result migration: `database/007_whatsapp_number_checks.sql`
- Signed Buy/Sell quantity migration: `database/008_signed_transaction_quantity.sql`
- Signed transaction rate migration: `database/009_signed_transaction_rate.sql`
- Development signup migration: `database/010_development_signup.sql`
- Local database creation: `scripts/Setup-LocalDatabase.ps1`
- API startup and smoke check: `scripts/Start-Api.ps1`, `scripts/Smoke-Api.ps1`
- Local pairing helper and offline self-check: `scripts/Pair-WhatsApp.ps1`, `scripts/Test-PairWhatsApp.ps1`
- Manual API examples: `backend/requests.http`

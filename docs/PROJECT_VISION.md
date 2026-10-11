# Project vision: WhatsApp-connected IPO transaction platform

## What I am building

I want to build a web and mobile application for businesses that handle IPO-related form transactions. The application should record transactions entered by staff and automatically identify transactions from private WhatsApp conversations using AI and business-defined rules.

It should bring customer information, IPO information, transaction records, WhatsApp conversation evidence, and transaction history into one system. PostgreSQL will store the business data and linked WhatsApp sessions, while a Go backend will serve the web and mobile applications.

The current use case includes buying IPO forms from customers. We are using the neutral term **transaction** because the complete distinction between purchases and onward sales has not yet been defined.

## Two ways to create a transaction

### 1. Automatic detection from WhatsApp

- A business links its WhatsApp account through Linked devices.
- The server receives incoming and outgoing messages from that account.
- Only readable text from private 1:1 chats is relevant. Groups, broadcasts, statuses, newsletters, media, reactions, and media captions are excluded.
- A potential sale message is evaluated together with the five immediately preceding readable texts from the same private conversation, when available: up to six messages including the trigger. Use their timestamps to understand when the sale happened. The existing terminal viewer's ten-message buffer is a diagnostic implementation, not the detector's required context window.
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

The system needs an IPO catalogue containing the IPO's identity and relevant information. The initial database design allows names, identifiers, relevant dates, optional price-band data, and additional metadata. Further IPO fields can be added as the business requirements become clearer.

### Form types

The initial form types are:

- Retail
- Retail 199
- SHNI
- BHNI
- Shares
- CE
- PE

These are catalogue entries. Transaction-specific numbers are not separate catalogue combinations.

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

Subject 1 and Subject 2 will have different calculations in a future analytics module. Those calculations do not belong in the initial transaction-entry or amount-calculation workflow. For now, the categories are stored as classifications for later use.

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
| Number of forms | The quantity; integers and fractional quantities are supported |
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
The POS customer name takes precedence over imported suggestions; different names
or customers sharing a number must not be silently merged. Staff may withdraw
their own contributions. Permission collection remains the mobile app's responsibility.

## Businesses, staff, and account access

The platform supports businesses with multiple staff rather than only one independent shop per user.

Staff records include a name and contact number. The initial roles are **admin** and **cashier**, assigned within each business. The manual API now implements a baseline permission matrix: both roles can create/view customers and create/view/edit/cancel transactions; admins also create/view staff and create IPO catalogue entries. Further permissions remain open as workflows expand.

Users sign in to this application separately from connecting WhatsApp. A business can own linked WhatsApp accounts, and authorized staff can share access to those accounts' data. Each linked WhatsApp account has its own saved server session; every staff member or browser does not need another WhatsApp pairing.

The web and mobile applications communicate with the Go backend. Account credentials and PostgreSQL connection settings remain on the server.

## WhatsApp connection and session persistence

- Web onboarding should support a QR code approved through WhatsApp's Linked devices screen.
- Mobile onboarding should also support phone-number pairing codes, making linking possible when the application and WhatsApp are on the same phone.
- Saved WhatsApp credentials, encryption sessions, contacts, and sync state should live in PostgreSQL through whatsmeow's SQL store.
- The application should reconnect using the saved account session after a restart.
- Revoked or invalid sessions require relinking.
- Application account IDs remain stable even if a linked-device credential is replaced.

The current implementation has PostgreSQL-backed terminal QR login and authenticated backend QR/phone-code pairing endpoints with PNG rendering, stable account mapping, saved-session reconnection and revocation handling. Web/mobile pairing screens remain planned.

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
- Customer/referral entry, staff creation, shared IPO/form catalogues, and manual transaction creation/editing/cancellation/history through authenticated APIs.
- Authenticated shared contact-name upload, number lookup and withdrawal, with staff provenance and customer-name precedence; mobile permission screens remain to be built.
- The transaction POST accepts an owned paired WhatsApp sender and saves a composed confirmation intent atomically with the sale; retries reuse it and the mobile API can read its status. Delivery is the next worker increment.
- Exact decimal strings at the API boundary, creation idempotency with original request fingerprints, optimistic edits, and bounded pagination/filtering.
- Local `ipo_platform` PostgreSQL database setup with separate owner/runtime roles and randomly generated credentials in an ignored, access-restricted root `.env`.
- Isolated API integration tests under a restricted database role, including concurrent creation retries, business isolation, permissions, and immutable transaction history.
- Authenticated WhatsApp account registration/list/detail APIs: stable account IDs, business ownership and the signed-in registering user; restricted runtime grants prevent callers from claiming session bindings.
- Backend QR/phone pairing, owner-only code/PNG retrieval, verified bindings, saved-session recovery and revocation handling; cross-process account locks and isolated protocol lifecycle tests. Live phone-code pairing and automatic restoration after a backend restart were verified locally on 2026-10-10.

Still to implement or connect:

- Remote deployment configuration. Local PostgreSQL-backed WhatsApp pairing and saved-session restoration are configured and verified.
- Password reset, public account onboarding, staff membership management, and expansion of the baseline admin/cashier permissions.
- Web/mobile pairing screens on the implemented QR/phone API and deployment hardening of the trusted worker credentials/capacity.
- Persistent WhatsApp ingestion into the application message archive; the manual business APIs are implemented.
- Sending a WhatsApp message to the selected customer after a manual transaction is saved, using the creating user's linked WhatsApp account.
- AI integration, transaction extraction, reconciliation, and business-rule execution.
- Web and mobile interfaces for linking accounts, viewing conversations, entering transactions, and inspecting history/evidence.
- Phone-number pairing screens in the application UI; the backend and local helper now support phone codes.
- Subject 1/Subject 2 analytics and their business formulas.

The previous SQLite `session.db` has been preserved. Its real account data has not been migrated to PostgreSQL. A PostgreSQL-backed first login currently requires fresh pairing.

## Next delivery sequence

Keep each milestone in a separate local commit after its relevant checks pass:

1. **Account registry (implemented):** register and inspect accounts through authenticated business routes. Session identity comes from the trusted pairing worker.
2. **Authenticated pairing (backend implemented and phone-tested):** owner-only QR/phone codes and QR PNGs, verified bindings, saved-session recovery, stable IDs through relinking and database account locks. Live phone-code pairing and automatic restoration passed locally; live QR pairing and device revocation remain unchecked.
3. **Mobile POS API and confirmation delivery (next):** review the existing authenticated transaction API for mobile use, record a confirmation intent with the sale, and send from the creating user's verified linked account. Track delivery independently of sale persistence and associate the outgoing message with that sale.
4. **Sale detection context:** reuse the viewer's private-text filtering and identity normalization when connecting the detector; assemble the potential sale message and five immediately preceding texts from the same account/conversation. Retain the evidence needed for POS decisions rather than prioritising a general conversation browser.
5. **AI extraction and reconciliation:** determine whether a sale actually happened and when, retain exact evidence, validate versioned business rules, and reconcile follow-up messages before automatic transaction writes. A manual confirmation must never produce another sale for its existing transaction.

The web/mobile screens can then build on these contracts. Subject analytics follow once their formulas are agreed.

### Next increment: authenticated mobile POS and WhatsApp confirmations

The immediate product outcome is a mobile POST that records a sale and causes a
WhatsApp confirmation to be composed and sent. The existing
`POST /api/businesses/{businessID}/transactions` route, authentication, validation,
decimal handling, idempotency and immutable transaction history are the foundation.
Extend them; do not create a parallel sale writer or authentication implementation.
Deliver the work in three small local commits with their relevant tests:

1. **Mobile API readiness.** Review login/session expiry and logout, active user and
   business authorisation, payload validation, request limits and retry behaviour.
   Define the request/response contract and address demonstrated gaps. Require HTTPS
   for deployment and protected token storage in the mobile app; a shared secret
   embedded in every app installation is not user authentication. Identify the
   selected customer and the creating user's authorised linked account explicitly.
2. **Sale and confirmation intent.** Save the sale and its durable pending
   confirmation in the same database transaction. Generate the message from saved
   sale data, including customer, IPO/form, quantity, rate, total and transaction
   time; resolve the recipient from the selected customer. Repeated mobile requests
   with the same idempotency key must reuse the sale and confirmation intent.
3. **Delivery and status.** Use the existing account worker and whatsmeow sender to
   send after database commit. Check sender ownership and eligibility, record the
   WhatsApp message ID and delivery state, and expose that state to the mobile app.
   Keep sale success distinct from pending/failed messaging. Bound retries and treat
   an uncertain send acknowledgement explicitly rather than blindly sending again.

Acceptance checks: invalid/expired/revoked sessions and cross-business requests are
rejected; another user's account cannot be used as sender; totals come from saved
data; concurrent/replayed POSTs reuse one sale and confirmation intent; pending
delivery survives restart; WhatsApp failure preserves the sale and reports status.
Use isolated database and fake sender tests first. Sending to a real customer needs
an explicit test recipient and deliberate test sale; never put customer messages,
credentials or linked-device keys in Git or test output.

Sale detection follows this increment. Shared filtering is an implementation step
when the detector needs it, not a separate POS deliverable. A full message browser,
historical backfill, attachments and unrelated viewer refactoring are deferred.

## Decisions still open

- Whether to distinguish purchases from onward sales in the initial transaction workflow.
- The exact acceptance, amendment, cancellation, and duplicate-order rules for automation.
- What the CE/PE numeric value should be called in business-facing UI.
- Expansion of the implemented phone/password authentication and baseline role permissions for deployment and additional workflows.
- Frontend technologies for web and mobile.
- Final numeric precision, currency, and rounding requirements.
- Complete IPO fields and analytical formulas.

Inventory, payment settlement, and media processing are not part of the agreed initial scope. Their requirements should be established separately before implementation.

## Related project files

- Database design: `docs/POS_DATABASE_DESIGN.md`
- Initial PostgreSQL schema: `database/001_initial.sql`
- Viewer and local PostgreSQL setup: `examples/login/README.md`
- Schema/session validation runner: `database/Test-Schema.ps1`
- Manual API, permissions, setup and usage: `backend/README.md`
- API authentication/idempotency migration: `database/002_api.sql`
- WhatsApp account pagination migration: `database/003_whatsapp_accounts.sql`
- Shared contact names and customer attribution migration: `database/004_contact_names.sql`
- Durable sale confirmation migration: `database/005_transaction_confirmations.sql`
- Local database creation: `scripts/Setup-LocalDatabase.ps1`
- API startup and smoke check: `scripts/Start-Api.ps1`, `scripts/Smoke-Api.ps1`
- Local pairing helper and offline self-check: `scripts/Pair-WhatsApp.ps1`, `scripts/Test-PairWhatsApp.ps1`
- Manual API examples: `backend/requests.http`

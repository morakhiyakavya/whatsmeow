# venweb Project and Business Overview

Newcomer guide | 29 October 2026

## 1 Purpose and current scope

venweb is a WhatsApp-connected platform for businesses that buy IPO-related forms from customers and sell them onward. It brings staff-entered transactions, customer identities, shared IPO information and the evidence behind a transaction into one business record. The intended next layer is to turn that record into understandable positions and forecasts, then automate eligible transaction entry from private WhatsApp conversations.

IPO means initial public offering. Here, a form trade is an agreement involving an application or its related exposure; recording it does not submit an exchange application or establish successful allotment. POS refers to the staff transaction-entry workflow. GMP means grey market premium, maintained here as a business estimate.

The immediate business value is a consistent record of what was agreed, by whom, with whom and when. Shared contact names reduce identification work; immutable revisions make corrections traceable; safe retries reduce duplicate orders; and separately tracked WhatsApp confirmations make communication failures visible.

The repository is a fork of the whatsmeow Go library with the application added in `backend/`, `database/` and `docs/`. The native Kotlin/Compose Android client lives in the sibling `transactions` project. Library capabilities alone do not establish that a venweb feature has been built.

### Capability status

| Capability | Position at this review |
|---|---|
| Manual transactions, customers and immutable history | Implemented in the Go API |
| WhatsApp-linked login, additional devices and recovery | Backend implemented; Android flows exist |
| Customer confirmation messages | Durable queue and status tracking implemented; live delivery still needs verification |
| Shared contacts and profile freshness | Backend and Android implementation present; real-device push/background delivery remains unverified |
| Shared IPO catalogue and importer | Implemented; source coverage can be partial or stale |
| Business GMP, application forecasts and Subject 2 factor | Versioned API implemented; Android working copy displays saved inputs read-only |
| Transaction and analysis-input synchronisation | Implemented in backend and Android working copy; deployment and device verification must match the build |
| Net positions, rate suggestions and profit or loss reports | Agreed design; calculation/reporting APIs and screens remain planned |
| AI extraction from WhatsApp | Planned; schema foundations and a diagnostic text viewer exist |

This is an implementation overview, not a live service-health report. Deployment records at review time show the updated application on Render, with the Azure importer rollout interrupted by connectivity failure. Android changes include development working-copy functionality, not a signed production release.

Payments, collections, outstanding balances, tax invoicing, after-listing settlement and continuing exchange-market portfolio tracking are outside the current product scope. No revenue model, fee schedule or customer-volume forecast is established by these specifications.

## 2 People records and ownership

A business is the operating boundary. Its customers, transactions, contact contributions and management assumptions must remain isolated from other businesses. A staff member may have business memberships; selecting a business is part of using the application. The IPO and form-type catalogues are shared across the platform.

### Roles and authority

| Action | Business admin | Cashier |
|---|---|---|
| Read and create customers | Yes | Yes |
| Read, create, edit and cancel transactions; inspect history | Yes | Yes |
| Share and resolve contact names | Yes | Yes |
| Withdraw a shared name | Own contribution | Own contribution |
| Create or view staff; create manual IPO entries | Yes | No |
| Read saved GMP, forecasts, factor and their history | Yes | Yes |
| Change saved GMP, forecasts and factor | Yes | No |
| Pair a WhatsApp account or use it as a sender | Own registered account | Own registered account |

An admin does not gain permission to send from a colleague's WhatsApp account. A future platform master-admin role for production business onboarding is separate from the implemented business-admin role.

### Identity and contact rules

Keep three identities distinct: the staff login phone, that user's linked WhatsApp sender and the customer's receiving phone. Passwordless linking checks that the approved WhatsApp number matches the requested staff identity. Reading phone contacts and linking WhatsApp are separate actions.

A customer is a transaction counterparty. A shared contact is a contributed name for a phone number; it does not, by itself, create a customer. Customer names and phone numbers are not unique customer identifiers, and records must not be silently merged because either happens to match. A customer can reference an existing same-business referrer or retain a referral note when that person is not a customer.

Staff names for the same international number are preserved separately with attribution. The requesting staff member's contribution takes priority for shared-name display; otherwise the latest colleague contribution is used. An explicit POS customer name takes precedence over imported suggestions. Withdrawing one's contribution does not delete another person's name or the customer's transaction history.

Normal production login requires provisioned staff. Development mode can create an isolated development business after WhatsApp approval. Each POS device receives its own session while sharing the user's backend WhatsApp connection. Device logout, all-device logout and WhatsApp unlinking have different effects. Detected unlinking revokes linked POS access without deleting business history; temporary network loss does not itself end login.

## 3 Transaction rules and examples

Every transaction identifies an IPO, customer, transaction time, form type, category, signed quantity and agreed rate. It also retains staff attribution where supplied, creation origin, status, version and change reason. Transaction time means when the trade happened; recording time means when the system saved a version.

### Direction and amount

Direction is always from the business's perspective: a positive quantity is Buy, a negative quantity is Sell, and zero is invalid. Quantities may be fractional. Rates may be positive, negative or zero. Neither the rate's sign nor the amount's sign determines direction.

**Recorded amount = round(signed quantity × agreed rate, 2).**

The database calculates the amount. Quantity, rate and CE/PE values support up to six fractional digits at the API boundary and travel as decimal strings. The amount has two fractional digits. There is no additional lot-size, share-count, category or CE/PE-value multiplier in this calculation.

| Quantity | Rate in INR | Recorded amount in INR | Direction |
|---:|---:|---:|---|
| 2.5 | 250 | 625.00 | Buy |
| -2.5 | 250 | -625.00 | Sell |
| 2.5 | -250 | -625.00 | Buy |
| -2.5 | -250 | 625.00 | Sell |
| 3 | 0 | 0.00 | Buy |

### Form types and categories

The supported form types are Retail, Retail 199, SHNI, BHNI, Shareholder, Shares, CE and PE. Application counts and share counts have different units; they cannot be added as if they were interchangeable.

For CE or PE, a separate transaction value such as 20 or 20.5 is required. For other types it must be absent. For example, CE value 20, quantity 2.5 and rate 250 still produces INR 625.00. CE/PE valuation and settlement rules remain unresolved; recording those trades does not mean the platform can calculate their profit or loss.

The transaction categories are None, Subject 1 and Subject 2. These are separate from published investor categories such as QIB, Retail and NII. For application trades, None means already allotted in the agreed analytics design. Subject 1 and Subject 2 have different future forecast-cost treatment, but both retain the same recorded quantity-times-rate amount.

An active transaction has status `confirmed`; a retained cancellation has status `cancelled`. **Confirmed in the ledger does not mean allotment is confirmed.** Cancelled transactions retain their record and history but must be excluded from current active-position calculations.

Shareholder availability is IPO-specific. The current ledger accepts the global Shareholder form type; the planned IPO-specific entry restriction is not yet implemented. Historical Shareholder transactions must remain visible even when later source information conflicts with them.

## 4 Manual workflow and record integrity

### From entry to customer confirmation

1. The staff member signs in, selects an authorised business and resolves or creates the customer.
2. They choose an eligible IPO and form type, enter category, signed quantity, agreed rate and transaction time, then review the trade.
3. The API validates the request and saves the transaction, its initial immutable version and the original request identity atomically.
4. When a WhatsApp sender is selected, the same commit also saves the message intent. The sender must be the creating user's active paired account, and the customer must have an international phone number. Invalid prerequisites reject that creation request.
5. A background worker sends after the commit. The app tracks the saved transaction and its message outcome separately.

The API supports creation without a selected WhatsApp sender. A successfully saved trade is not proof that its message was sent, delivered to the handset or read. The current `sent` outcome represents the server's successful send response; it is not a customer read receipt.

The worker checks that the sender, transaction version and recipient are still valid before sending. An edited or cancelled transaction, changed recipient or revoked sender must not produce an obsolete confirmation. Pending and sending work continues on the server independently of the current app screen.

An interrupted or unconfirmed send becomes uncertain. The implementation makes one send attempt; uncertain outcomes need review before any safe resend workflow is introduced. Refreshing message status must not submit another trade or silently send the message again.

### Safe retries and corrections

Creation requires a business-scoped idempotency key. An identical retry from the original creator reuses the existing trade; reusing the key for a different payload or actor produces a conflict. A genuine second order needs a new key. This protects against duplicate taps, network retries and a lost save response.

Edits and cancellations require the expected current version and a nonempty reason. If another user has changed the trade, the stale request is rejected rather than overwriting their work. Every successful change adds a complete immutable snapshot. Cancel instead of deleting. Original creation identity and manual/automatic origin remain intact after corrections.

The Android client has transaction entry, review, saved-receipt and transaction-list flows. Its local cache supports reads and catch-up, but creating customers, authentication and transaction saves still require the backend. Offline transaction submission is not implemented.

## 5 Market facts and management assumptions

The product keeps three different prices separate: official IPO price terms, business-entered GMP and the rate agreed on a transaction. GMP is the business's premium estimate in INR per share. A source refresh or changed estimate must never rewrite an agreed transaction rate or amount.

### Shared catalogue facts

The importer stores IPO identity, board, dates, price bands and available lot, subscription, reservation, registrar, issue-size, promoter and anchor-lock-in information. It preserves source evidence and observation times. Confirmed dates and expected timelines remain distinguishable. Unknown data stays unknown; an empty source response is not evidence of zero subscription, withdrawal or absence of a quota.

The selected architecture triggers the Azure importer through an authenticated Sentry POST every 30 minutes. A fast acknowledgement means the job was queued, not that collection succeeded. The Go worker persists results and failures; partial or stale coverage is visible through the API. Missing/failed fields preserve valid existing observations and stable IPO references.

The consolidated-subscription source increment targets NSE-listed IPOs and reads NSE's published combined NSE+BSE mainboard figures. NSE SME observations retain single-exchange coverage. This increment is awaiting production migration 019 and deployment. Overlapping NII parent and subcategory figures must not be summed. Published share-subscription multiples do not automatically establish an applicant's success probability.

Application amounts use the shared issue-specific lot and price rules where supported. A base lot, shares applied for and shares actually allotted are different quantities. Do not hardcode Retail 199 as a fixed lot count, or assume Mainboard and SME rules are interchangeable.

The entry picker normally retains upcoming, open and awaiting-listing issues through their listing day in Asia/Kolkata. A seven-day post-close grace applies when no usable listing date exists; explicit postponed/withdrawn issues are excluded. Leaving the picker does not delete the IPO or its historical trades.

### Business assumptions

Admins maintain a business-specific GMP, expected application counts and Subject 2 pricing factor for each IPO. The factor defaults to 80%, with an implemented range of 0% through 100%. Counts are positive integers or unknown; zero is not a substitute for an unknown count. Retail 199 shares the Retail forecast input, while its share-conversion basis remains distinct.

Each update records a complete immutable revision, actor, server time and reason. Version checks protect concurrent updates. Reading an untouched IPO shows unknown GMP/counts and the default factor without creating a revision. A complete replacement can clear omitted values, so an editor must preserve any settings the user intends to retain.

Shareholder availability has three states: available, not offered and unknown. Positive published reservation supports available; explicit zero supports not offered; missing observations remain unknown. A new Shareholder forecast is rejected when the quota is confirmed not offered. Earlier inputs and trades remain in history.

## 6 Business insights and agreed calculation rules

These rules describe the planned calculation layer. Persisting its inputs is implemented; calculated pricing and position reports are not yet delivered.

### Allotment model and suggested rates

For a validated category model, let Q be shares per successful application, G the business GMP per share, A the comparable application capacity at 1×, E expected applications, p = A ÷ E, and f the Subject 2 pricing factor.

Use the probability only when units and issue rules support it and 0 ≤ p ≤ 1. Missing inputs, a zero denominator or an unsupported basis make the affected calculation unavailable. Do not silently cap an invalid probability or replace it with zero.

**Subject 1 suggested rate = round(Q × G × p, 0).**

**Subject 2 suggested rate = round(Q × G × f, 0).**

Subject 1 pricing includes forecast success probability. Subject 2 pricing is conditional on successful allotment, so its suggested rate does not multiply by p again. These are whole-rupee suggestions, not replacements for saved rates.

For Q = 12, G = INR 240 and p = 7,086,444 ÷ 9,000,000, Subject 1 suggests INR 2,268. Subject 2 at 80% suggests INR 2,304; at 60% it suggests INR 1,728; at 100% it suggests INR 2,880. The 80% factor is not an 80% profit margin or a guarantee. Changing f changes Subject 2 guidance only, not forecasts, existing agreements or history.

### Position and cost contributions

| Trade kind | Share contribution | Premium cost contribution |
|---|---|---|
| Subject 1 application | Signed quantity × p × Q | Saved signed transaction amount |
| Subject 2 application | Signed quantity × p × Q | Signed quantity × agreed rate × p as expected cost |
| Already allotted application | Signed quantity × confirmed shares per application | Saved signed transaction amount |
| Direct Shares | Signed share quantity | Saved signed premium amount |
| CE or PE | Requires an agreed instrument model | Valuation remains unsupported |

Subject 2 expected cost differs from its recorded face amount. Actual Subject 2 cost needs successful quantities at their original agreed rates. A total confirmed share count alone may not resolve that cost.

The proposed book measures are net shares = confirmed net shares + remaining forecast net shares; net premium cost = the sum of signed cost contributions; and estimated result = net shares × valuation premium − net premium cost. Break-even premium is net premium cost ÷ net shares only when net shares is nonzero. Negative Sell amounts already carry their sign; do not subtract them twice.

For ten Subject 1 buys at INR 500 and eight sells at INR 600, net premium cost is INR 200. With Q = 14 and p = 1/4, net forecast shares are 7. At GMP INR 200, estimated result is INR 1,200. This is a model example, not realised or guaranteed profit.

## 7 Reporting history and the trading cycle

The intended dashboard starts with one cumulative net position per IPO. Staff can expand into buy/sell quantities, separate weighted average rates, form/category contributions, forecast and confirmed shares, premium cost and underlying transactions. Share counts from different IPOs must not be combined into one fungible share position.

Show missing inputs beside the affected measure. A known subtotal is useful, but cannot be labelled the complete book when a contributing category is unresolved. Keep zero-share positions with a remaining cost or credit visible; avoid division by zero. CE/PE uncertainty must not disappear from a supposedly complete profit result.

### Daily activity and historical closes

Daily activity means trades occurring on that day. A closing position is cumulative through the cutoff, including earlier exposure. Dates use Asia/Kolkata, with an inclusive start and exclusive next-day boundary.

The planned historical view preserves what was known then: the applicable transaction versions, GMP, forecast, official basis and reconciliation inputs. Later corrections and backdated entries must not silently rewrite the original close. A corrected reconstruction needs an explicit label. Immutable ledger versions are a foundation; they are not themselves a completed daily-report engine.

For example, 100 shares at INR 100 premium have an estimated gain of INR 5,000 at GMP INR 150. Buying another 100 at INR 200 brings the cumulative average premium to INR 150. Yesterday's stored estimate stays unchanged; it is not an extra realised profit to add to today's valuation.

### Allotment and listing

Initial allotment reconciliation is planned as an admin-entered confirmed remaining position with dated transaction coverage. It replaces the forecasts it covers rather than adding confirmed shares on top of them. New uncovered trades contribute once. Corrections to covered trades require reconciliation review. Automatic allotment checking and onward-contract matching remain future work.

The agreed listing reference is NSE's first executed trade, with BSE's first executed trade as fallback. Prefer NSE even if BSE traded earlier. Preserve exchange, price, trade time, source and correction attribution. A later fetch must not label the latest price as the first trade.

Final premium or discount equals the selected first-trade price minus confirmed final issue price. GMP and a provisional price band cannot silently substitute. If the reference is missing, the final result remains pending attributed manual input. Listing completes the app's IPO trading cycle; payment settlement is handled separately by the user.

### Decisions these views should support

Management should be able to see which IPOs carry long or short exposure, how new trades changed averages, which categories drive expected shares, how sensitive estimates are to GMP and application forecasts, and where missing data prevents a reliable result. Forecasts explain exposure and uncertainty; they do not establish a bank balance or secured profit.

## 8 Automation architecture and onboarding

### Planned WhatsApp detection

The intended detector uses readable text from private one-to-one chats only. Groups, broadcasts, status, newsletters, media, captions and reactions are excluded. Each potential transaction uses the triggering text and five immediately preceding readable texts from the same conversation when available. The terminal viewer's ten-message context buffer is diagnostic and must not define the detector window.

AI proposes transaction details; validated business rules determine whether to record them automatically. Mandatory human approval is not the default intended workflow. Incomplete or ambiguous candidates retain a decision record without creating an unjustified trade. Follow-up messages must reconcile with an existing order; genuine repeat orders remain possible.

Retain exact text and message revisions, direction/time, extraction, rule results, explanation, model/prompt/rule versions and the resulting transaction-version link. A manual confirmation seen in the chat stream must link to its existing transaction rather than create another. Exact acceptance, amendment, cancellation and semantic duplicate-order policies still require agreement before this workflow is implemented.

### System responsibilities

| Component | Responsibility |
|---|---|
| Android client | Staff workflow, protected local reads, drafts, receipt display and authenticated catch-up |
| Go backend on Render | Business API, access checks, WhatsApp connection and background delivery |
| Azure importer | Source collection in the selected separate host role |
| Supabase PostgreSQL | Authoritative business records, immutable versions, linked sessions and durable work |
| Firebase Cloud Messaging | Minimal change hints that cause authorised API refreshes |
| Sentry | Service/error visibility and scheduled importer triggers |

Push hints are not financial records and contain no rates, amounts or customer details. Android refreshes through business-scoped authenticated feeds, stages pages and publishes the cache atomically. Missed or delayed pushes are handled through foreground, reconnection, manual/staleness and periodic catch-up. Cached inputs do not imply that analytics were calculated, and an old cache must not appear newly refreshed after failure.

### First steps for a new contributor

Start with signed quantities, saved amounts and forecast versus confirmed outcomes. Trace a manual creation through its version and confirmation queue, then review business ownership and retries. Use synthetic/disposable checks; customer-facing sends require separate verification.

Next increments are input-editing screens, validated pricing/positions, allotment reconciliation, daily closes and listing references, followed by message archiving and AI extraction. Device/background-delivery checks and production onboarding decisions remain. No delivery dates are committed here.

### Reference map

- [Product vision](PROJECT_VISION.md) and [implemented API](../backend/README.md).
- [Reporting requirements](IPO_POSITION_REPORTING.md) and [analytics design](IPO_ANALYTICS_DESIGN.md).
- [Catalogue sources and lifecycle](IPO_CATALOGUE.md).
- [Business sync](BUSINESS_DATA_SYNC.md) and [Android rollout handover](ANDROID_BUSINESS_SYNC_HANDOVER.md).
- [Android working-copy guide](../../transactions/README.md), in the sibling project.

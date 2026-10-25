# IPO positions and historical reporting

Agreed behaviour. Settings/history API and source-fact snapshots are implemented
locally in migration 015; reporting APIs and screens remain planned.
Reuse the transaction ledger, immutable versions, shared IPO catalogue and business
memberships. Pricing follows the [agreed workbook formulas](PROJECT_VISION.md#application-rate-guidance-agreed-implementation-pending).
The [database and API design](IPO_ANALYTICS_DESIGN.md) translates these requirements
into proposed additive storage and contracts; neither document applies migrations.

## Dashboard requirements

The primary user is a staff member reviewing their business's IPO trading book.
Admins additionally maintain shared assumptions and confirmed position inputs.

| View | Required content | Interaction |
|---|---|---|
| Current dashboard | One net-position summary per relevant IPO; phase, net expected shares, net confirmed shares, total exposure, net premium cost/credit, break-even premium and modeled profit/loss where calculable | Default view; no full transaction list |
| IPO detail | Category contributions, buy/sell quantities and amounts, separate weighted average rates, subject split, applicable pricing suggestions and calculation inputs | Expand the selected IPO on request |
| Today's activity | Transactions occurring today; buy/sell movement and change from the previous closing position | Open activity when requested |
| Historical day | Locked closing GMP, forecasts, quantities, costs/average and estimated result, with the original transaction versions | Select a past date |
| Management inputs | Business GMP, expected application counts per allocation category and Subject 2 pricing factor, initially 80% | Admin edit; cashier read/use |
| Allotment result | Manual confirmed remaining share position, coverage and unresolved actual-cost components | Admin records the reconciliation initially |
| Listing result | NSE first-trade reference, BSE fallback or attributed manual entry; final issue price and premium/discount | Display final trading result when inputs are complete |

"Relevant IPOs" means the business's trading book, not every entry in the global
catalogue. A historical or closed book remains accessible after its IPO leaves
the transaction-entry picker. Keep long/short direction visible and preserve
zero-share positions with a cost/credit or result. Do not present an all-IPO total
share count as one fungible position.

Label share/application counts, premiums and amounts with their units. Separate
forecast shares from confirmed shares; total exposure combines them only when
their source transactions do not overlap. An entry's `confirmed` transaction status
means it is active in the ledger, not that allotment has been confirmed.

For missing, stale or unsupported inputs, show the specific reason beside the
affected measure. Preserve known figures and expose partial coverage rather than
replacing unknowns with zero. A subtotal of known categories must not masquerade
as the complete IPO total. Expose source/assumption times and the selected date.

Opening a summary/detail/history view is read-only. Changes to assumptions update
current suggestions/estimates without rewriting agreements or prior closes. Show
the author and time of shared changes. No payment-entry, outstanding-balance or
post-listing portfolio flow is required.

An empty trading book shows an explicit no-transactions state. Distinguish that
from a failed request or incomplete calculation. A failed refresh may retain the
last visible result with its timestamp/error, but must not present it as newly
calculated. Expand controls must be keyboard accessible; long/short and incomplete
states need text labels rather than colour alone.

## Net position first

Show each IPO's cumulative net position (buys minus sells) first. Label forecast
shares, confirmed shares, premium cost/credit and estimated result separately.
Share counts from different IPOs are not interchangeable. On request, expand into
buying/selling quantities, separate weighted average rates and amounts, subjects,
already-allotted applications, direct shares and individual transactions. Summary
and detail use one calculation, not independently editable totals.

Keep application counts and share counts distinct. A zero-share position can have
a nonzero cost/credit; retain the amount without dividing by zero or hiding it.

## Category breakdown

Maintain separate expected-share contributions for Retail, Retail 199, SHNI, BHNI
and Shareholder where applicable. Shareholder is optional per IPO: show its forecast
input, pricing suggestion and category row only when that IPO offers a shareholder
reservation, or when an existing position/history needs to remain visible. It is
not a permanently required field on every IPO. For a confirmed absent quota, do not
require a Shareholder forecast or treat its absence as an incomplete calculation.

Missing source data is not proof that the quota does not exist. Distinguish offered,
not offered and availability unconfirmed; explain unconfirmed availability when
relevant rather than inventing a quota or forcing a zero. Keep the global
Shareholder form type and previous transactions/snapshots intact even if the
category is unavailable for another IPO or later source information changes.

Show the total first and the category breakdown when requested,
including buy, sell and net figures. Category net contributions reconcile to the
IPO's application-derived expected shares; direct shares and confirmed allotments
remain separately identifiable without counting a replaced forecast twice.

Retail and Retail 199 are separate trading buckets but may use the same published
retail allocation pool. Do not duplicate its reservations or subscription figures.

## Daily activity and historical positions

Support today, yesterday and earlier dates using Asia/Kolkata calendar days,
inclusive start/exclusive next-day boundaries.

- Daily activity means transactions occurring within that day, with buy/sell
  quantities and amounts, net movement and transaction detail on request.
- Closing position is cumulative through that day's cutoff, including earlier
  open exposure; it is not just that day's buys minus sells.
- Explain changes through trades, assumptions, manual allotment reconciliation
  and corrections. GMP changes valuation; allotment forecasts change expected
  shares; the Subject 2 factor changes rate suggestions only.

Reuse immutable transaction versions and their transaction/recording times. Select
one applicable version per transaction at the cutoff, not every historical version.
Preserve cancellations, corrections and business isolation. The default historical
view represents what was known then; later corrections/backdated entries must not
silently rewrite it. Label any corrected reconstruction explicitly.

Preserve dated GMP, category forecasts, pricing factors, share-conversion/IPO terms
and manual reconciliation inputs/provenance used for historical results. The
importer currently retains latest observations, so it cannot alone reproduce past
valuations. Preserve an end-of-day record of closing GMP and all calculation inputs,
position quantities, premium cost/average and estimated profit/loss. Later forecast
changes and purchases must not overwrite that original closing record. Retain the
supporting input/transaction versions without a duplicate transaction ledger. Never
fill missing historical inputs with today's values while presenting the result as
yesterday's forecast.

When requested, compare the previous closing position with today's new purchases,
sales, revised cumulative average and current result. This lets management see how
new trades changed the average and exposure. For example, 100 shares at INR 100
premium have INR 5000 estimated profit at a closing GMP of INR 150. Buying another
100 shares at INR 200 brings cumulative average premium cost to INR 150; it does
not change the stored previous-day estimate. The live combined result still uses
the actual current position and valuation. A historical estimated gain is a saved
observation, not secured trading profit or an extra credit to add to today's result.

## Official terms and forecast entry

Obtain base lot size, price basis, reservations and category application limits/
share counts from published data and the shared exact-decimal category calculation
where coverage permits. Keep missing values unknown and use issue-specific rules;
do not hardcode Retail 199 as 14 lots or borrow another category's allocation rule.

Base lot size, shares applied for and actual shares allotted are different values.
Application limits do not establish an individual's allotment or the shares
received conditional on success. Use a validated allotment model before allotment
and the user's confirmed result afterwards until the checker is implemented.

Business admins enter the expected total application count for each category;
current reported subscription remains secondary. For comparable application counts,
800000 expected applications against a 200000 capacity means 4x demand and a modeled
1-in-4 success rate. Derive the multiple, probability and expected shares from that
one forecast; do not require separate editable versions of the same assumption.
A published share-demand multiple is not automatically an application success
probability. Keep forecast changes attributed, dated and isolated to the business.

## Allotted trades and manual reconciliation

For Retail, Retail 199, SHNI, BHNI and Shareholder application trades, category
`none` means already allotted. Add known allotted shares immediately, without a
probability reduction. Do not multiply SHARES quantities again or invent CE/PE
settlement rules from this application convention.

Initially, the user obtains allotments and reconciles onward contracts manually,
then supplies confirmed remaining shares. A dated
reconciliation identifies transactions covered; subsequent allotted applications
and share trades update it without re-adding replaced forecasts. Preserve original
transactions and reconciliation corrections. Individual allotment checking and
automatic onward matching remain future features.

Payments, collections, outstanding balances and after-listing settlement are handled
by the user separately and are outside this app's current scope. Do not require a
manual final-payment or outstanding-payment amount, or treat its absence as a
blocker to position reporting. Premium costs/credits and profit/loss are trading
calculations from transactions and available allotment information, not a payment
ledger. If an actual cost component cannot yet be determined from those inputs,
identify it as unresolved rather than inventing a bank balance or profit figure.

## Listing reference and closure

The user's listing reference is NSE's first executed trade price, with BSE's first
executed trade as the fallback when the NSE value is unavailable or not applicable.
Use NSE even if the BSE trade happened earlier. The value may be imported
automatically or entered manually. Record the exchange actually used, price,
trade timestamp, source/entry method and attributed correction history. A delayed
collector must retrieve the first-trade/opening record, not label the latest price
at fetch time as the first trade.

Final premium/discount is the selected first-trade price minus the final issue
price. GMP, indicative prices and a provisional upper price band must not silently
substitute for confirmed values. Do not average exchange prices or pick whichever
quote the collector happened to fetch first. If neither reference is available,
leave the final listing result pending manual entry. Replacing a recorded fallback
after the fact is an attributed correction, not a silent change to final history.

Listing closes the app's IPO trading cycle. Retain final trading positions,
transaction history, premium costs/credits and profit/loss results. Payment and
collection work is handled separately by the user. Ongoing exchange-market
portfolio tracking is outside this scope.

## Roles

Implemented business roles are `admin` and `cashier`. Both currently manage
transactions/customers and read transaction history; staff and manual IPO creation
are admin operations. Use business `admin` for top management. A platform master-
admin for business provisioning is separate planned work, not an implemented role.

For planned analytics, business admins change shared GMP, category forecasts/share
assumptions and the Subject 2 pricing factor. Cashiers use/read those inputs and
suggestions but cannot change them. Record who changed each value and when, retain
prior versions, enforce access on the server and keep business assumptions isolated.
Ingestion updates source-owned facts, never management's forecast or pricing factor.

## Checks for delivery

Verify summary/detail/category reconciliation; signs and units; zero-share balances;
allotted trades and reconciliation cutoffs; daily versus cumulative figures; IST
boundaries; later edits/cancellations/backdated entries; original/corrected history;
missing historical inputs; first-trade/manual correction handling; and admin,
cashier and cross-business access. Existing transaction history is a foundation,
not proof that historical position reports or settings controls already exist.

Acceptance examples include ten Subject 1 purchases at INR 500 and eight sales
at INR 600: net application cost is INR 200. With 14 shares per success and a
1-in-4 model, expected net shares are 7 and estimated result at GMP INR 200 is
INR 1200. An already-allotted purchase of three applications with 14 confirmed
shares each adds 42 confirmed shares without probability scaling. Yesterday's
saved estimate remains unchanged when today's trade raises the cumulative average.

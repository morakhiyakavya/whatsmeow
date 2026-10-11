# POS transactions and shared contacts: operating and mobile integration guide

Reviewed against the implemented Go API on **11 October 2026**.

This guide explains how a staff member records an IPO/form transaction, how a phone
shares contact names with a business, and how later changes affect colleagues.
**Current behaviour** means the backend already supports it. **Mobile work** means
the app must still implement the screen, device access or synchronisation described.

For server installation, migrations and pairing diagnostics, use the
[API reference](../backend/README.md). For the wider product direction, use the
[project vision](PROJECT_VISION.md). This guide describes the current IPO/form
transaction system; it does not imply inventory, payment collection, tax invoicing
or a general retail checkout has been implemented.

Quick navigation: [mobile authentication and WhatsApp login](#mobile-authentication-and-whatsapp-login),
[phone uploads](#4-how-phone-contacts-should-be-uploaded),
[shared name lookup](#5-how-colleagues-resolve-an-unknown-number),
[contact changes and deletions](#6-how-changes-new-contacts-and-deletions-reflect),
[customer creation](#7-a-shared-contact-is-not-yet-a-pos-customer),
[transaction entry](#9-record-a-transaction),
[message status](#10-whatsapp-confirmation-and-delivery-status), and
[remaining mobile work](#13-minimum-remaining-mobile-implementation).

## 1. What can be done today

| Capability | Current position |
|---|---|
| Passwordless mobile sign-in by approving WhatsApp phone-number linking | Backend implemented; live passwordless sign-in and mobile screen remain unchecked |
| Stay signed in through renewable sessions tied to a valid WhatsApp link | Backend implemented: 15-minute access, rotating 30-day renewal |
| Additional POS devices using a one-use token displayed on the original device | Backend implemented; token lasts two minutes |
| App logout without deleting the user | Implemented; currently leaves WhatsApp linked |
| Upload contact names, look up a number and withdraw your own name contribution | Implemented |
| Preserve separate names supplied by different staff | Implemented |
| Create a customer and record a manual transaction | Implemented |
| Calculate the total and preserve transaction revisions | Implemented |
| Queue and send a WhatsApp confirmation through the creating user's paired account | Implemented and tested with simulated sends; live receipt sending remains unchecked |
| Restore the saved WhatsApp session after a server restart | Implemented and verified with a real paired phone |
| Phone permission screens, contact change detection and checkout screens | Mobile work |
| Instantly push contact changes to every colleague's screen | Not implemented |
| Infer a transaction from WhatsApp text and five preceding messages | Future work |

You can exercise the backend using an HTTP client now. A complete phone workflow
still requires the mobile app.

## 2. The three phone identities

Keep these separate when using or building the app:

| Identity | Purpose |
|---|---|
| App login phone | Identifies the provisioned staff member approving WhatsApp sign-in |
| Paired WhatsApp phone | Sends the transaction confirmation through that staff member's linked account |
| Customer phone | Receives the confirmation; stored on the selected POS customer |

For passwordless login, the approved WhatsApp number must match the staff user's
registered phone. The older password-authenticated pairing workflow can link a
different number and remains a migration/development feature. Permission to read phone
contacts does not pair WhatsApp. WhatsApp pairing does not upload the phone's
address book into the shared business contacts API.

Each business has its own customer records and contributed contact names. A staff
member belonging to two businesses must explicitly choose which business receives
an upload. An upload to one business does not propagate to the other.

Admins and cashiers can upload/resolve contacts, create customers, record transactions,
edit/cancel transactions and inspect history. Only admins can create staff or IPO entries.
Only the registering user can pair an account or use it as the transaction's sender;
being an admin does not grant the right to send through a colleague's account.

## 3. Staff setup before the first transaction

1. In production, an administrator creates the staff account. With
   `APP_ENV=development`, a new number can use the same WhatsApp login flow to
   create its own isolated development business after approval. There is no
   endpoint for adding an existing user to another business.
2. Staff sign in by approving linking for their registered WhatsApp number, or
   receive a device token from an already signed-in POS device.
3. The app reads their active businesses and asks them to select one if necessary.
4. Staff can optionally share selected phone contacts with that business.
5. Passwordless first-device login establishes their owned WhatsApp account for
   receipts; additional POS devices reuse that account.
6. The IPO/form catalogue must be available. The IPO catalogue is currently shared
   across the application; customer and contact records are business-specific.

All HTTP examples below use placeholders such as `{businessID}` and `{customerID}`.
Replace them with IDs returned by the API. Names and numbers in examples are
illustrative, not approved test recipients. Sending a transaction request with a paired
sender can cause an actual customer message.

Use the passwordless flows below to obtain your device's session. For subsequent
protected requests use:

```http
Authorization: Bearer YOUR_ACCESS_TOKEN
```

Get the authorised business IDs:

```http
GET /api/me
Authorization: Bearer YOUR_ACCESS_TOKEN
```

The mobile app must keep tokens in protected platform storage, use HTTPS for a
deployed service, and renew expired access with its current refresh credential.
If renewal is unavailable, authenticate again. Do not put server database
credentials or a shared administrator token in the app. The current localhost HTTP
listener is for development; a phone cannot reach the computer through the phone's
own `127.0.0.1` address. Remote access/deployment is separate setup work.

### Mobile authentication and WhatsApp login

The backend now supports passwordless sign-in and several POS devices using
**one WhatsApp link per user/number**. The backend holds that connection; each
phone, tablet or browser receives its own POS session. A second POS device never
pairs WhatsApp again.

Phone-code linking was previously verified with a real phone. The new login,
renewal, token exchange and automatic sign-out have isolated database/protocol
coverage; a live passwordless login and mobile screens still need verification.

#### First device: WhatsApp phone-number sign-in

In production (also the default when `APP_ENV` is absent), an administrator must
first provision the staff user and active business membership. The staff creation
API accepts a name and phone without an app password. With `APP_ENV=development`,
the same endpoints and bodies below also sign up a new number: only after approved
WhatsApp identity verification, the worker atomically creates the user, an isolated
INR development business, its admin membership and the WhatsApp binding. Initial
names are the phone number and `Development business PHONE`; no password is created.
Unapproved or expired attempts create no user/business. Signup cannot join an
existing business, reactivate a disabled user/membership or bypass an archived
business. The future master-admin provisioning model is in [TODO.md](TODO.md).

For existing users, the supplied login number must match their registered phone
exactly. Existing placeholder
login numbers need an explicit correction/migration before this login can be used;
a previously paired different number does not silently change the app identity.

From the first POS device, without an app bearer token:

```http
POST /api/auth/whatsapp/start
Content-Type: application/json

{"phone_number":"+919000001003","device_name":"My primary phone"}
```

HTTP 202 returns `attempt_token`, `state: "pending"` and `expires_at`. Keep this
private credential on the requesting device. The attempt lasts three minutes and
cannot be shared between callers. Only one active proof attempt owns a given account;
unknown numbers in production, already-linked and overlapping attempts return a generic pending shape
without account/user IDs or another caller's code. If no code becomes available,
check staff provisioning or use the additional-device token flow below. This is
not a promise that every accepted request starts a new WhatsApp pairing.

Poll with the attempt credential in the body, about every two seconds:

```http
POST /api/auth/whatsapp/status
Content-Type: application/json

{"attempt_token":"YOUR_PRIVATE_ATTEMPT_TOKEN"}
```

`awaiting_phone` contains the linking `code` and its expiry. Enter that code in
WhatsApp's Linked devices phone-number linking flow. It is entered in WhatsApp;
our app does not send an OTP message to the number. Codes and attempt credentials
must not be logged, placed in URLs, backed up or shared. The server checks that the
actual approved WhatsApp identity exactly matches the requested number.

When status is `verified`, exchange the proof once:

```http
POST /api/auth/whatsapp/exchange
Content-Type: application/json

{"attempt_token":"YOUR_PRIVATE_ATTEMPT_TOKEN"}
```

Success returns `access_token`, `refresh_token`, `token_type: "Bearer"`,
`expires_at`, `refresh_expires_at`, `session_id`, `user_id` and
`whatsapp_account_id`. Then use `GET /api/me` to select an authorised business.
An unverified, expired or consumed attempt cannot issue a session. Existing
owner-authenticated pairing endpoints stay protected; they were not made public.

The account/user IDs remain stable across logout and relinking. A returning user
with no signed-in POS device must remove the old POS link in WhatsApp if it is still
present, then repeat verified number linking. Saved server credentials or typing
an already-linked number alone never authorise a new device.

#### Additional devices: token displayed on the original device

On an already signed-in POS device, ask to **Add another device**:

```http
POST /api/auth/device-tokens
Authorization: Bearer YOUR_ACCESS_TOKEN
Content-Type: application/json

{}
```

HTTP 201 returns `device_token` and `expires_at`. Display the token on this original
signed-in device as a QR code or copyable text. It is a random 256-bit credential,
represented by 43 URL-safe characters; it is short-lived, not a short numeric PIN.
It expires after **two minutes** and works **once**. Creating another token on the
same source session immediately invalidates its previous token. Logging out or
revoking that source session also invalidates its outstanding token.

The second device scans/pastes it and supplies a label:

```http
POST /api/auth/device-tokens/exchange
Content-Type: application/json

{"device_token":"TOKEN_FROM_ORIGINAL_DEVICE","device_name":"Office laptop"}
```

Success returns a separate access/refresh session for the same user and WhatsApp
account. Concurrent redemption creates exactly one session. The original phone
remains signed in, and no WhatsApp pairing/client is created for the laptop.
Possession of this token is the approval: anyone given it can redeem it before
expiry. Display/share it only deliberately; never use a public QR generation site,
put the credential in a URL, or send it to someone else's device.

#### Persistent login and renewal

Access tokens last **15 minutes**. Refresh tokens expire after **30 days without
successful renewal**; renewal extends that deadline another 30 days and rotates
both credentials. Thus active users can remain signed in while their WhatsApp link
and membership remain eligible. Token expiry does not require another pairing
while valid renewal credentials are available.

```http
POST /api/auth/refresh
Content-Type: application/json

{"refresh_token":"YOUR_CURRENT_REFRESH_TOKEN"}
```

Store both returned credentials atomically in protected storage. The session ID
and device label stay the same. The old access and refresh tokens stop working.
Serialize refresh requests per device: concurrent or repeated use of the old
refresh token cannot renew again. A lost response after a successful one-time
exchange or rotation can leave that device without usable credentials; use a new
token from another signed-in device, or the fresh-link recovery flow. There is no
replay of a previously consumed secret to recover its newly issued credentials.

Closing WhatsApp, closing the POS app or a temporary connection outage does not
revoke login. The backend does not know whether WhatsApp is open or being used.
An offline device can hold a local draft, but cannot save a transaction until the API is
reachable. Account/membership eligibility is still checked on protected requests.

#### Signed-in devices and logout

`GET /api/sessions`, with a bearer token, returns up to 100 eligible sessions in
`items`, including ID, device label, timestamps and a `current` flag. Credentials
and token hashes are omitted. Labels are supplied by the user/app, not verified
hardware identities. There is currently no pagination beyond the latest 100.

| Action | API | Result |
|---|---|---|
| Sign out of this POS device | `POST /api/logout` | Removes this session and its renewal/outstanding device token; other devices and WhatsApp continue |
| Remove one of my POS devices | `DELETE /api/sessions/{sessionID}` | Removes only a session belonging to the signed-in user; repeated removal is safe |
| Sign out of every POS device | `POST /api/logout-all` | Removes all this user's app sessions and device tokens; the WhatsApp connection remains |
| Disconnect the shared WhatsApp connection | `POST /api/businesses/{businessID}/whatsapp-accounts/{accountID}/disconnect` | Owner-only; clears the binding and revokes all this user's POS sessions |
| Remove the POS entry from WhatsApp's Linked devices list | No POS request required | Worker detects revocation, clears the binding and revokes this user's POS sessions |

The first three logout/removal endpoints return HTTP 204. Explicit WhatsApp
`disconnect` returns HTTP 200 with `state: "needs_relink"` and
`remote_unlink_confirmed`. Local access is revoked before attempting remote removal.
If remote removal cannot be confirmed, remove the old entry in WhatsApp before
fresh pairing. This action does not log the person out of their primary WhatsApp
app or unlink unrelated devices.

The binding's generation changes on removal/replacement. Revocation invalidates
access and refresh credentials; relinking never reactivates the old sessions.
Still-pending receipts from that link are retained as `cancelled` with
`error_code: "sender_unlinked"`, so they cannot silently send after relinking.
Already attempted/acknowledged messages cannot be retracted. Ordinary POS logout
leaves previously authorised queued receipts and the WhatsApp sender intact.

User rows, registered numbers, memberships, customers, transactions, immutable history
and contributed contact names are preserved. Logout does not withdraw contacts;
use the separate contribution withdrawal API. A different number is not an
automatic transfer of someone's old user/business history.

Revocation is enforced once the backend detects it and persists it. An outage can
delay that detection, and an offline POS screen cannot be instantly cleared; its
next authenticated request is rejected after revocation. Server-confirmed logout
requires connectivity. Merely clearing a phone's local token does not revoke the
server session.

#### Deployment and legacy-password transition

`ALLOW_PASSWORD_LOGIN` defaults to `false`: normal deployments use passwordless
login. Explicit `true` enables the old phone/password route solely for local
bootstrap or a controlled migration; those legacy sessions expire after 12 hours
and cannot issue additional-device tokens. They are also revoked when their user's
active WhatsApp binding is removed. Keeping the flag enabled still permits fresh
password authentication, so turn it off for the intended WhatsApp-only policy.

The local setup script explicitly enables this compatibility flag because it
creates a placeholder bootstrap phone. Existing installations must apply migration
006 and the documented runtime grants before using the new endpoints. Do not
silently overwrite a real user's phone/profile to migrate a development placeholder.
HTTPS, protected mobile storage and worker availability are required for deployment.

Authentication entry/exchange/refresh calls share the existing per-process limit
of 10 attempts per minute/source IP; starts additionally allow three per minute
per phone through PostgreSQL. Status polling and signed-in device operations are
separate. Shared deployment-wide IP limits remain future work. Database indexes
and per-user worker locks prevent a second active link for the same user/number;
conflicting existing bindings make the migration fail instead of choosing one.

## 4. How phone contacts should be uploaded

### Staff experience — mobile work

The phone app should provide this sequence:

1. Staff open **Share contacts with this business**.
2. Explain that selected names and numbers become visible to authorised colleagues
   in that business, with attribution to the uploading staff member.
3. Ask for operating-system contacts permission and let staff choose which contacts
   to share. Device access permission and agreement to business sharing are distinct.
4. Read only the contacts accessible under the granted permission.
5. Prepare the selected names/numbers and upload them in bounded batches.
6. Report successful batches and errors. Keep failed batches available for retry.

If permission is denied, the app must not read contacts. Staff can still enter a
POS customer manually and record a transaction. Contact permission is not a requirement
for the authenticated transaction API.

### Preparing numbers and names

The server requires a `+` followed by 7–15 digits, with a nonzero first digit:
for example `+919000001010`. Spaces, brackets, hyphens and local-only numbers are
rejected. This is format validation, not proof that the number exists or uses
WhatsApp. The backend does not infer a country code or normalise local numbers.

The app must resolve a number's country before upload. If it cannot do so reliably,
ask the user to correct or skip that number. Do not blindly prepend a country code
to every number. For a contact with several selected numbers, send a separate
entry for each number. If multiple phone records resolve to the same number, choose
one name before sending; the same number cannot appear twice in one batch.

Names are trimmed and must be nonempty, with at most 256 UTF-8 bytes. That is a byte
limit, so some names can reach it with fewer than 256 characters. Only name and
phone number are accepted; do not include device contact IDs, emails, photos,
addresses, a supplier ID or a client timestamp in the request.

### Upload request — implemented

```http
POST /api/businesses/{businessID}/contacts
Authorization: Bearer YOUR_ACCESS_TOKEN
Content-Type: application/json

{
  "contacts": [
    {"phone_number":"+919000001010","name":"Mehul Shah"},
    {"phone_number":"+919000001011","name":"Asha Patel"}
  ]
}
```

Success is HTTP 200:

```json
{"accepted":2}
```

There must be 1–100 distinct numbers in each request, within the 64 KiB JSON body
limit. A batch is atomic: one invalid entry rejects the whole batch. For more than
100 numbers, split the upload; earlier successful batches remain saved if a later
batch fails. `accepted` counts valid entries in that request, including unchanged
entries; it does not mean that many new contacts were created.

The server derives the supplier from the signed-in user and stores:

| Field | Meaning |
|---|---|
| Business | The authorised business receiving the contribution |
| Phone number | The exact validated international number |
| Name | This staff member's current supplied name for that number |
| `supplied_by_user_id` | The authenticated uploader |
| `source` | Always `mobile_contacts` for these uploads |
| `received_at` | When this contribution was first inserted |
| `updated_at` | When inserted or last changed to a different name |

There is one current contribution per **business + number + staff user**. Uploading
the same name again does not create a duplicate or change its timestamps.
Uploading a different name replaces only that user's contribution and advances
its `updated_at`; it preserves the original `received_at`. Earlier spellings are
not retained in a contact-name history.

The API trusts the authenticated app to have obtained consent; it cannot observe
the phone's permission prompt. The source label is not independent proof that the
name came from a genuine phone address book.

## 5. How colleagues resolve an unknown number

```http
GET /api/businesses/{businessID}/contacts?phone_number=%2B919000001010
Authorization: Bearer YOUR_ACCESS_TOKEN
```

URL-encode the leading `+` as `%2B`. The response contains:

| Field | Meaning |
|---|---|
| `phone_number` | Number being resolved |
| `customer_count` and `customers` | Matching active POS customer records |
| `source_count` and `name_sources` | Staff contributions and their provenance |
| `suggested_name` | Preferred suggestion, or `null` when none is available |

Each mobile name source includes its name, supplier ID, the supplier's current
staff display name (`supplied_by_name`), source and timestamps. Each customer match
includes its ID, name and `name_supplied_by_user_id`. Up to 100 customers and 100
sources are returned per lookup; counts can be larger. There is currently no
pagination for additional lookup matches and no endpoint to download all contacts.

### Name-selection rules

1. **Exactly one active POS customer has that number:** its chosen customer name
   wins, with `source: "customer_record"`, the customer ID and recorded name author.
2. **Zero or multiple active POS customers have that number:** the latest changed
   mobile contribution is suggested, if one exists. Equal timestamps are resolved
   consistently by supplier ID, not by seniority or confidence.
3. **No applicable suggestion:** `suggested_name` is `null`. When several customers
   exist and no mobile name exists, the matching customers are still returned for
   explicit selection.

Example: Priya has not saved a number. Arun uploaded it as **Mehul Shah**. Priya can
look it up and see Arun's suggestion without saving it on her own phone. If Neha
later uploads **Mehul Trading**, both contributions remain visible. With no single
matching POS customer, the most recently changed contribution becomes the suggestion.
This is a deterministic convenience rule, not verification of which name is correct.

If the business has one customer named **Mehul Shah — IPO**, that customer name
continues to win even after Neha's upload. Multiple customer records sharing the
number stay separate. The app must not silently select or merge them by name.

## 6. How changes, new contacts and deletions reflect

| Phone or staff action | Required app request | Backend effect | What colleagues see after a fresh lookup |
|---|---|---|---|
| Save a new contact and choose to share it | POST the new number/name | Adds the caller's contribution | New suggestion/source, subject to customer precedence |
| Rename a shared contact | POST the same number with the new name | Replaces only the caller's name | Updated source; may become preferred |
| Upload an unchanged contact | POST the same number/name | Keeps the row and timestamps unchanged | No change in source priority |
| Another staff member shares the same number | POST under their own login | Adds a separate contribution | Both sources remain visible |
| Remove a shared number or stop sharing it | DELETE that number under the original contributor's login | Removes only that user's contribution | Remaining sources/customer name still apply |
| Change a contact's phone number | POST the new number; DELETE the old one if no longer shared | Treats them as separate identities | New lookup resolves independently; old sources may remain |
| Omit a previously uploaded contact from a later POST | No deletion request | Keeps the old contribution | Old source remains visible |
| Revoke phone contacts permission | Stop reading; explicitly withdraw saved contributions if requested | No automatic deletion | Existing uploads remain until withdrawn |

### Changes require synchronisation — mobile work

Editing the phone's address book alone does not update the server. The app must
read the permitted change and submit it. Once a batch commits, colleagues' next
lookup uses that data. There is no contact push notification, WebSocket subscription
or automatic refresh of an already-open screen.

A minimal app should offer **Sync selected contacts**, refresh number lookup when
opening a customer/transaction screen, and refresh after an upload or withdrawal. Automatic
device change observation can follow platform-specific implementation later. Do not
promise background synchronisation until it is built and tested on the chosen mobile
platform.

### Withdrawing a contribution — implemented

```http
DELETE /api/businesses/{businessID}/contacts?phone_number=%2B919000001010
Authorization: Bearer YOUR_ACCESS_TOKEN
```

Success is HTTP 204 with no JSON body. Repeating the DELETE is safe. It cannot remove
a colleague's contribution, a POS customer or any transaction. There is no endpoint for
bulk withdrawal or for an admin to delete another contributor's name.

If a phone number changes, posting the replacement does not erase the old number.
Uploading the replacement and withdrawing the old number are separate requests,
not one atomic operation. The app must track failures and retry the unfinished step.
Before deleting an old number, ensure another selected phone contact still does not
share that number under the same staff user.

### Permission withdrawal, devices and offline changes

Revoking OS permission only blocks further device reads. Already uploaded names
remain in PostgreSQL. A **Stop sharing and remove my uploads** action needs explicit
DELETE requests for known contributed numbers. While offline, the app must show
withdrawals as pending, retain enough state to finish them, and send them when an
authenticated connection returns. There is currently no server-wide withdrawal
endpoint or list of all the caller's uploaded numbers.

Contributions belong to the staff account, not a handset. Two phones signed in as
the same staff user update the same contribution. The last different name received
by the server wins for that user/number; there is no device-level provenance,
client-version check or stale-update rejection. Avoid parallel sync jobs, and do not
replay an old renamed value after sending a newer one. Retrying an old unchanged-name
batch after a newer rename can revert that rename.

Changing phones or reinstalling the app does not remove server contributions. A
local sync manifest should track shared numbers and acknowledged names per
staff/business; losing that manifest currently prevents comprehensive automatic
withdrawal without additional server support. A full address-book replacement
protocol is not implemented: POST is an incremental add/update operation.

Disabled staff cannot access the API, but their previously uploaded contributions
remain available to authorised colleagues. Disabling a membership does not purge
its names. Staff display names in lookup results reflect the current user record;
the supplier ID is the durable attribution.

## 7. A shared contact is not yet a POS customer

Contact uploads provide suggestions. They do not create customer records. A transaction
requires a selected customer ID, and its WhatsApp recipient comes from that
customer's stored phone number, not directly from the shared-contact lookup.

For a new customer:

1. Look up the number and review any matching customers or contributed names.
2. Select an existing customer if appropriate. If there are several, choose explicitly.
3. If a new record is needed, confirm its name and number and create it.
4. Save the returned customer ID for the transaction.

```http
POST /api/businesses/{businessID}/customers
Authorization: Bearer YOUR_ACCESS_TOKEN
Content-Type: application/json

{
  "name":"Mehul Shah",
  "phone_number":"+919000001010",
  "referral_note":"Introduced by an existing client"
}
```

The API also accepts `referred_by_customer_id` when the referrer is already a
customer. It records the creating staff user as the customer name's author.
If Priya creates a customer using Arun's suggestion, the customer name author is
Priya; Arun remains the supplier of his mobile contribution. There is no additional
field linking the chosen customer spelling back to a particular imported suggestion.
Older customer records may have an unknown name author.

Customer names and phone numbers are not unique identifiers. Customer creation
currently has no idempotency key; repeated POSTs can create duplicate customers.
After an uncertain network response, refresh the customer list/number lookup before
deciding to create another. Concurrent staff creation can still produce duplicates;
there is no automatic merge.

There is currently no customer edit/archive API. Phone contact renames, number
changes or withdrawals never rewrite the saved POS customer. A future customer
correction workflow must be explicit; do not create a replacement transaction simply to
change a contact name.

## 8. Prepare the WhatsApp sender

For passwordless users, use the `whatsapp_account_id` from the login response;
the first-device flow has already prepared and verified this account. Extra POS
devices share it. Do not register another account to add a POS device.

For the legacy authenticated pairing workflow, registration remains available:

Register once and retain the returned account ID:

```http
POST /api/businesses/{businessID}/whatsapp-accounts
Authorization: Bearer YOUR_ACCESS_TOKEN
Content-Type: application/json

{"label":"My business WhatsApp"}
```

Registration itself does not pair the phone and has no idempotency key. Do not
register a new account on each transaction or restart; use the saved ID and account list.

For phone-number pairing:

```http
POST /api/businesses/{businessID}/whatsapp-accounts/{accountID}/pair
Authorization: Bearer YOUR_ACCESS_TOKEN
Content-Type: application/json

{"method":"phone","phone_number":"+YOUR_WHATSAPP_NUMBER"}
```

Poll `GET .../whatsapp-accounts/{accountID}/connection` about every two seconds.
Enter the returned code in WhatsApp's Linked devices phone-number linking flow.
Wait for `connected`; never log or persist the pairing code. QR pairing is also
supported through the [pairing reference](../backend/README.md#authenticated-pairing-and-reconnecting).

Saved eligible sessions reconnect when the backend starts. An owner can request
`POST .../whatsapp-accounts/{accountID}/connect` to reconnect. A revoked/missing
session needs relinking using the same account ID. `paired` metadata indicates a
saved binding; it does not prove the account is online.

## 9. Record a transaction

### Staff checkout flow — mobile work

1. Choose the customer and verify their saved recipient number.
2. Select an IPO and form type from the catalogue.
3. Enter category, signed quantity, rate and the actual transaction time. Positive
   quantity means the business buys; negative means it sells. Zero is invalid.
4. For CE/PE, also enter the separate CE/PE value.
5. Choose whether to send a WhatsApp confirmation and, if so, select your own paired
   account.
6. Review the details and tap **Save transaction** once.
7. Show the saved transaction and its separate message status.

The API calls these records `transactions`. The quantity's sign determines Buy
versus Sell from the business's perspective; there is no separate direction field.
A saved `confirmed`
transaction is a ledger entry, not proof that payment was received.

Read the catalogue using `GET .../ipos` and `GET .../form-types`. Supported form
codes are `RETAIL`, `RETAIL_199`, `SHNI`, `BHNI`, `SHARES`, `CE` and `PE`.
Customer and IPO lists default to 50 records, maximum 100 per page; follow
`next_cursor` until the required selection is available.

### Transaction request — implemented

Create one unique `Idempotency-Key` for this intended transaction and preserve both the
key and the original request until its outcome is known.

```http
POST /api/businesses/{businessID}/transactions
Authorization: Bearer YOUR_ACCESS_TOKEN
Idempotency-Key: mobile-transaction-20261011-example-001
Content-Type: application/json

{
  "ipo_id":"{ipoID}",
  "customer_id":"{customerID}",
  "occurred_at":"2026-10-11T14:30:00+05:30",
  "form_type_code":"CE",
  "category":"subject_1",
  "ce_pe_value":"20",
  "quantity":"2.5",
  "rate":"250",
  "whatsapp_account_id":"{accountID}"
}
```

| Field | Rule |
|---|---|
| `ipo_id`, `customer_id` | IDs of available records; customer must belong to this business |
| `occurred_at` | Actual transaction time in RFC3339 with time-zone offset; stored in UTC |
| `form_type_code` | One of the supported exact codes |
| `category` | `none`, `subject_1` or `subject_2`; omission defaults to `none` |
| `ce_pe_value` | Decimal string required for CE/PE; omit or use null for other forms |
| `quantity` | Nonzero signed decimal string: positive for Buy, negative for Sell |
| `rate` | Signed decimal string; positive, negative and zero are valid |
| `whatsapp_account_id` | Optional; your own active paired account in this business |
| `salesperson_user_id` | Optional responsible staff ID; does not change sender ownership |
| `change_reason` | Optional creation explanation; defaults to `Manual entry` |

Decimal inputs allow up to 12 integer digits and six fractional digits. Send
strings such as `"2.5"`, not JSON floating-point numbers. The server calculates:

```text
total = round(quantity × rate, 2)
2.5 × 250 = INR 625.00
-2.5 × 250 = INR -625.00
```

The request above is a Buy. For a separate Sell, use the same request fields with
`"quantity":"-2.5"` and a new unique `Idempotency-Key`. Both first successes return
HTTP 201. These are excerpts of their saved transaction responses:

```json
{"quantity":"2.500000","rate":"250.000000","total_amount":"625.00","version":1,"status":"confirmed"}
```

```json
{"quantity":"-2.500000","rate":"250.000000","total_amount":"-625.00","version":1,"status":"confirmed"}
```

Rate may also be negative. With the same request fields and `"rate":"-250"`,
quantity `"2.5"` is a Buy totaling `"-625.00"`; quantity `"-2.5"` is a Sell totaling
`"625.00"`. Each separate transaction needs its own unique retry key.

| Quantity | Rate | Direction | Saved total |
|---|---|---|---|
| `"2.5"` | `"250"` | Buy | `"625.00"` |
| `"-2.5"` | `"250"` | Sell | `"-625.00"` |
| `"2.5"` | `"-250"` | Buy | `"-625.00"` |
| `"-2.5"` | `"-250"` | Sell | `"625.00"` |

Use quantity's sign for direction, never rate or total. A zero rate or rounding can
produce a zero total for either direction. The API returns no separate Buy/Sell field.

The transaction inherits the business's currency, which defaults to INR; the creation
request has no currency selector. Category and CE/PE value do not multiply the total. The app may show a
preview, but the saved database total is authoritative. Do not send `total_amount`,
a message body or a caller-supplied creator ID; unknown fields are rejected.

### What is saved together

With a requested sender, one database transaction saves the transaction, its first
immutable history version, its creation retry identity and a composed pending
WhatsApp confirmation. If the customer has no phone, the sender is unauthorised,
or the account is unpaired, the creation fails without saving a partial transaction.

Omit `whatsapp_account_id` to save a transaction without requesting a message. There is
currently no endpoint to attach a confirmation later to such a transaction. Adding a
sender to a retry of that original request changes its payload and returns a
conflict; using a new key would create a second transaction.

First success returns HTTP 201 with the transaction ID, version, total and other saved
fields. When a sender was requested, it also includes `whatsapp_confirmation`.
This confirms database persistence; it does not mean sending has completed.

### Safe retries

For a timeout or uncertain response, retry with the **same key, original payload
and staff account**. An identical retry returns HTTP 200 with
`Idempotency-Replayed: true`, reusing the transaction and confirmation. The response shows
the transaction's current state if it has since been edited or cancelled.

A different payload, sender or creator under the same key returns HTTP 409
`idempotency_conflict`. Do not generate a new key merely because the network failed.
Generate a new key only for a genuinely separate transaction. On-device offline drafts
are mobile work: until the server accepts one, the transaction is not saved in the business
ledger and no confirmation is queued.

## 10. WhatsApp confirmation and delivery status

The backend composes a fixed text from saved data. For the example above, the body
has this shape, with the actual selected IPO name and generated transaction reference:

```text
Transaction confirmation
Type: Buy
Customer: Mehul Shah
IPO: Example IPO
Form: CE (20.000000)
Category: subject_1
Quantity: 2.500000
Rate: INR 250.000000
Total: INR 625.00
Transaction time: 2026-10-11T09:00:00.000000Z
Reference: {transactionID}
```

The recipient is the selected customer's stored phone. The sender is the creating
staff member's requested linked WhatsApp account. The receipt is a snapshot of the
initial transaction; contact uploads and later transaction edits do not rewrite its body.

Read its latest status:

```http
GET /api/businesses/{businessID}/transactions/{transactionID}/confirmation
Authorization: Bearer YOUR_ACCESS_TOKEN
```

No requested confirmation returns HTTP 404. The response includes the body,
recipient, sender account ID, transaction version, status, message ID when assigned,
error code and timestamps. It omits the private protocol session binding. Authorised
business colleagues can inspect this receipt; pairing secrets remain owner-only.

| Status | Meaning | Suggested app display/action |
|---|---|---|
| `pending` | Saved and waiting for a connected eligible worker | Transaction saved; message pending |
| `sending` | Message ID recorded and a send attempt underway | Transaction saved; sending |
| `sent` | WhatsApp acknowledged the message | Transaction saved; message acknowledged |
| `uncertain` | The attempt may have reached the customer; acknowledgement/recovery is inconclusive | Transaction saved; check the chat before any further action |
| `failed` | Worker blocked the unsent receipt because relevant saved data changed | Transaction saved; receipt requires review |
| `cancelled` | Worker found the transaction cancelled before claiming it for sending | Transaction cancelled; queued receipt blocked |

`sent` is not proof of delivery to the handset, reading, payment or agreement.
There is one send attempt and no manual resend endpoint. An uncertain result is
never automatically resent. Retrying the transaction POST does not resend its receipt.

The connected worker checks for pending receipts every two seconds, one receipt
per account per tick; the send can take longer. There is no delivery-time guarantee.
Offline paired accounts can accumulate pending receipts. The configured backend
worker must run to send them; the POS phone does not perform the protocol send
itself. Pending intents persist across server restarts. An interrupted `sending`
attempt becomes `uncertain` when its account worker recovers.

The worker checks sender eligibility, transaction status/version and recipient details
before claiming a pending receipt. An edit blocks the old pending snapshot;
cancellation blocks it; a changed recipient or sender also blocks it. Once a send
attempt begins, editing or cancelling the transaction cannot guarantee stopping that
message, and it cannot retract an already acknowledged message. No correction or
cancellation notification is sent automatically.

Common recorded receipt error codes are `transaction_changed`,
`transaction_cancelled`, `recipient_changed`, `sender_changed`, `send_unconfirmed`
and `interrupted_send`. Link removal/replacement cancels pending receipts with
`sender_unlinked`. These describe message processing, not failure to save the
transaction. Lack of an eligible connected worker can leave a receipt pending.

## 11. Correct or cancel a saved transaction

Fetch the current transaction first with `GET .../transactions/{transactionID}`. To edit it, use
`PUT .../transactions/{transactionID}` with the complete editable transaction fields, the current
`expected_version` and a nonempty `change_reason`. This is a full replacement of
editable fields, not a partial patch. Do not include `whatsapp_account_id` in an edit.

To cancel:

```http
POST /api/businesses/{businessID}/transactions/{transactionID}/cancel
Authorization: Bearer YOUR_ACCESS_TOKEN
Content-Type: application/json

{"expected_version":1,"change_reason":"Customer cancelled the order"}
```

Successful edits/cancellation advance the version and preserve earlier snapshots.
A stale version returns HTTP 409 `version_conflict`; reload the transaction before acting.
Cancellation preserves the record and amount with `status: "cancelled"`; it does
not delete history. Read `GET .../transactions/{transactionID}/history` for versions,
newest first. List active ledger entries using `GET .../transactions?status=confirmed`.

Contact changes never edit or cancel transactions. Customer/contact data and transaction
history have separate purposes.

## 12. Errors the mobile app must handle

Errors have the form:

```json
{"error":{"code":"invalid_contacts","message":"..."}}
```

| HTTP/result | App response |
|---|---|
| 400 validation/unsupported JSON | Correct the fields or batch; do not repeatedly submit unchanged invalid data |
| 401 invalid/expired session | Sign in again; preserve an unresolved transaction's original retry key/payload for the same staff user |
| 403 business access or `invalid_sender` | Verify selected business and account ownership; do not switch to a colleague's credentials |
| 404 missing record/confirmation | Verify selected IDs; a transaction created without a receipt has no confirmation record |
| 409 `sender_unpaired` | Pair the requested account; this rejected creation did not save a transaction |
| 409 `recipient_missing` | The selected customer needs a saved phone; the rejected creation did not save a transaction; customer editing is not implemented yet |
| 409 `idempotency_conflict` | Reconcile the original intended transaction; do not silently create another |
| 409 `version_conflict` | Fetch current transaction and review the competing change |
| 429 login rate limit | Honour `Retry-After`; current login limit is 10 attempts/minute/source IP |
| Network timeout/500 on transaction creation | Outcome may be unknown; retry the original transaction under its original key |
| Network timeout on contact upload | Retry only if this is still the latest intended name update; preserve batch order |
| Network timeout on customer/account creation | Check existing records before another POST; these routes have no creation retry key |

Use independent indicators for **transaction saved** and **message status**. A messaging
failure after persistence must not lead to another transaction POST with a new key.

## 13. Minimum remaining mobile implementation

1. Passwordless sign-in, original-device token display/QR rendering, protected
   credential storage, atomic renewal, business selection and device/logout screens.
2. Contact permission and sharing selection; international number preparation;
   ordered batches; a local sync manifest; explicit withdrawal and visible pending
   offline changes.
3. Number lookup with supplier attribution, customer precedence and explicit choice
   when several customers share a number. Refresh when opening the relevant screen.
4. Customer selection/creation and IPO/form selection.
5. WhatsApp account selection/pairing and connection feedback.
6. Transaction review and submission with a persistent key/payload; a saved-transaction screen,
   receipt status polling, edit/cancel history and clear error handling.

Start with manual **Sync selected contacts** and the existing API. Automatic
background sync, full-list export, bulk withdrawal, customer correction,
device conflict resolution and safe receipt resend need explicit further work.
The backend already has integration coverage for contact provenance, batch/replay
behaviour, business isolation, atomic transaction/receipt creation and delivery recovery.

Not checked in this documentation task: live phone contact synchronisation or a
real transaction receipt. The mobile app and production deployment remain to be built;
uploaded names can become stale until the app submits changes, and an uncertain
message may already have reached its recipient.

# IPO transaction API

This Go service implements the manual ledger milestone. It uses PostgreSQL for
application sign-in, business membership, customers, the shared IPO/form catalogue,
transactions, complete immutable transaction history, and business-owned WhatsApp
account registration. No frontend is required to exercise the API.

The [IPO catalogue importer](../docs/IPO_CATALOGUE.md) adds source-owned IPO terms,
subscriptions, reservations, promoter shareholding, anchor lock-ins and category
application amounts. A protected Sentry uptime POST queues a durable refresh job.
Apply migration 013 and runtime grants before starting this version.

For the complete staff and mobile workflow, read the
[POS transactions and shared contacts guide](../docs/POS_AND_CONTACTS_GUIDE.md). It covers
transaction entry, contact uploads, name changes, new numbers, withdrawals, provenance
and the distinction between implemented APIs and remaining mobile work.

For linked-account sign-in recovery, Android approval/expiry/retry behaviour and
private session diagnostics, see the [Android recovery contract](../docs/ANDROID_ACCOUNT_RECOVERY.md).

## Local setup and start

From the repository root in PowerShell:

```powershell
# Once on a new machine/database. Uses your local PostgreSQL admin authentication.
./scripts/Setup-LocalDatabase.ps1
# Once to create the initial business/admin (safe to repeat; preserves passwords).
./scripts/Start-Api.ps1 -Bootstrap
# Keep this terminal running. Ctrl+C stops the API.
./scripts/Start-Api.ps1
```

The current workspace already has the `ipo_platform` database and root `.env`.
The setup script refuses to overwrite an existing `.env`, database or role.
For the local legacy bootstrap flow, explicitly set `ALLOW_PASSWORD_LOGIN=true`
and use `BOOTSTRAP_ADMIN_PHONE` and `BOOTSTRAP_ADMIN_PASSWORD` from `.env`.
Password login is disabled by default. Normal staff use the passwordless API below.
The initial phone `+919000000001` is a development placeholder, unrelated to your
WhatsApp number. An admin can create staff with actual phone numbers through
`POST /staff`. Bootstrap settings seed a new account; changing them later does
not reset an existing user's password or profile.

The scripts locate Go on PATH or the installed Go 1.27.1 directory. Direct usage:

```powershell
cd backend
go run . -env ../.env -bootstrap
go run . -env ../.env
```

`API_ADDR` defaults to `127.0.0.1:8180`. The environment file supports comments,
`KEY=value` lines and optional surrounding quotes; it does not execute shell code
or expand variables. Process environment variables override matching API settings.
On a host that provides `DATABASE_URL`, the environment file may be absent.
Without `API_ADDR`, a valid `PORT` binds the service to `0.0.0.0:PORT`.
See the [Render deployment guide](../docs/RENDER_DEPLOYMENT.md) for the hosted configuration.

In another terminal:

```powershell
./scripts/Smoke-Api.ps1
```

This checks health, login, business access, the catalogue and WhatsApp account
reads, then logs out. It does not create demo customers or ledger entries. For a full flow, use
`backend/requests.http` or the isolated integration runner below.

## Sentry monitoring

The Go `net/http` service uses Sentry project
[`vanitilabs/whatsapp-pos-api`](https://vanitilabs.sentry.io/settings/projects/whatsapp-pos-api/).
Set `SENTRY_DSN` privately in the environment or the existing `.env` file; leaving
it unset disables telemetry. `SENTRY_ENVIRONMENT` defaults to `APP_ENV`, or
`production` when unset. `SENTRY_RELEASE` defaults to Render's `RENDER_GIT_COMMIT`;
set it explicitly for a named local release.

Tracing captures **100% in `APP_ENV=development`** and 10% in production.
The HTTP middleware captures panics and request timings while preserving Go's
existing panic behaviour. Unexpected database failures are also captured;
normal authentication and validation rejections are not reported as exceptions.
Selected structured logs cover API startup and unexpected database failures.
Sentry Go 0.50 enables logs by default; the older `EnableLogs` option is unnecessary.
This does not forward all Render or WhatsApp library logs.

Telemetry excludes request bodies, headers, cookies, query parameters, user
information, raw request URLs, breadcrumbs, panic/error values and stack-frame
variables. Stack functions, files, lines, release and environment remain available.
Matched traces use route templates instead of business/customer IDs; unmatched
routes receive a generic name. Error values are deliberately generic, so use the
stack trace and protected operational logs for diagnosis. Shutdown flushes buffered
telemetry for up to two seconds.

The [Sentry uptime monitor](https://vanitilabs.sentry.io/monitors/10630540/)
requests the deployed `/health` every five minutes. It requires three consecutive
failures to open a downtime issue and one success to recover. The project's
high-priority notification rule is enabled. Monitoring does not guarantee continuous
Free-plan availability, and `/health` does not prove WhatsApp connectivity.
There is no frontend/mobile Session Replay integration in this repository.

## Database credentials

For a fresh hosted development database, follow the
[Supabase setup guide](../docs/SUPABASE_SETUP.md). It reuses the same migrations and
runtime permissions as local setup and keeps database passwords out of commands.

The root `.env` contains randomly generated local credentials:

| Setting | Purpose |
|---|---|
| `DATABASE_URL` | `ipo_platform_api` runtime role; no schema ownership, transaction deletion or truncation |
| `MIGRATION_DATABASE_URL` | `ipo_platform_owner`, which owns the application schemas |
| `WHATSAPP_DATABASE_URL` | Trusted pairing worker/viewer connection for WhatsApp migrations, sessions and verified account bindings |
| `APP_ENV` | `development` enables signup through the existing WhatsApp login flow; absent/`production` requires provisioned staff |
| `BOOTSTRAP_ADMIN_PASSWORD` | Initial app password, hashed with bcrypt when bootstrapped |

PostgreSQL listens on localhost:5432; the application database is `ipo_platform`.
Passwords are never printed by the setup or smoke scripts. `.env` is ignored by
Git and its Windows ACL grants access only to the current user and SYSTEM.
The connection uses `sslmode=disable` for local development. A remote deployment
needs verified TLS and credentials supplied privately to the server.

For a new database, apply `database/001_initial.sql`, `002_api.sql` and
`003_whatsapp_accounts.sql`, `004_contact_names.sql` and
`005_transaction_confirmations.sql`, `006_passwordless_sessions.sql`, then
`007_whatsapp_number_checks.sql`, `008_signed_transaction_quantity.sql`,
`009_signed_transaction_rate.sql`, `010_development_signup.sql` and
`011_contact_profile_push.sql`, `012_account_recovery.sql` and
`013_ipo_catalogue.sql` once,
in that order, as its migration owner. The API
does not execute migrations on startup. Apply only missing migrations to an
existing database. For the local installation that already has migrations 001/002:

```powershell
# From the repository root; enter the owner password privately when prompted.
& 'C:/Program Files/PostgreSQL/17/bin/psql.exe' -X -h localhost -U ipo_platform_owner -d ipo_platform -v ON_ERROR_STOP=1 -f database/003_whatsapp_accounts.sql
& 'C:/Program Files/PostgreSQL/17/bin/psql.exe' -X -h localhost -U ipo_platform_owner -d ipo_platform -v ON_ERROR_STOP=1 -c 'GRANT INSERT (business_id, label, linked_by_user_id) ON ipo_pos.whatsapp_accounts TO ipo_platform_api;'
```

For an existing installation through migration 003, apply `004_contact_names.sql`
and grant `SELECT, INSERT, UPDATE, DELETE ON ipo_pos.contact_names` to the API role.
The local setup script and disposable test role already include these grants.

After migration 004, apply `005_transaction_confirmations.sql` and grant
`SELECT, INSERT ON ipo_pos.transaction_confirmations` to the API role. Delivery
updates belong to the trusted WhatsApp worker, not the request role.

After migration 005, apply `006_passwordless_sessions.sql` and grant `UPDATE ON
ipo_pos.login_sessions`, `INSERT, UPDATE, DELETE ON ipo_pos.device_login_tokens`,
`SELECT ON ipo_pos.login_attempts, ipo_pos.device_login_tokens`,
`UPDATE (consumed_at) ON ipo_pos.login_attempts` and `UPDATE (id) ON
ipo_pos.whatsapp_accounts` to the runtime role, in addition to its existing grants.
The identity-column grant permits row locking without granting binding/generation
updates. Existing users, sessions and account IDs are preserved; conflicting active
user/number links make the migration fail safely. Review those conflicts explicitly.

After migration 006, apply `007_whatsapp_number_checks.sql` and grant `SELECT ON
ipo_pos.whatsapp_number_checks` to the runtime role. Only the trusted worker needs
`INSERT, UPDATE` on this table; the local worker uses the migration owner. Do not
grant result writes to the ordinary API role. The local setup/test scripts already
apply migration 007. Startup requires it, including when the worker is disabled.

After migration 007, apply `008_signed_transaction_quantity.sql` as the migration
owner before starting this API version. It permits nonzero signed quantities in
both current transactions and immutable history: positive means the business buys,
negative means it sells. Existing values and snapshots are preserved; no new
runtime grants are needed. The generated total is the rounded product of quantity
and rate; direction comes only from quantity.
Rollback to positive-only constraints requires resolving any negative current or
historical quantities explicitly; do not delete or rewrite immutable history.

After migration 008, apply `009_signed_transaction_rate.sql` as the migration owner
before starting this API version. Rate accepts positive, negative and zero values
in current transactions and immutable history; existing values are preserved and
no new grants are required. Returning to nonnegative-only rates requires explicit
resolution of negative current or historical rates; never rewrite immutable history.

Migration 007 rollback: revert/stop its callers before removing the table. Export
stored checks if needed; dropping the table loses those observations only.

After migration 009, apply `010_development_signup.sql` as the migration owner.
It reserves temporary signup identities on login attempts without creating staff
or business records. Existing runtime grants remain unchanged. Rollback requires
stopping/reverting this API before removing its three signup columns; preserve
users and businesses already created by approved development signups.

Migration 006 rollback needs a database backup and a deliberate session transition,
not only a Git revert. Stop/revert its callers, revoke the newly linked app sessions,
then remove the added token/attempt tables, triggers, indexes and session/account
columns in dependency order. Preserve users, business history and WhatsApp account
IDs; never drop the application or WhatsApp session schemas as a rollback shortcut.

The runtime grants are in `database/api_grants.sql`, reused by the local setup,
Supabase setup and API integration fixture. Set its psql `api_role` and
`database_name` variables when applying it elsewhere. The narrowly scoped identity-column UPDATE
grants let PostgreSQL's transaction guard take `FOR SHARE` row locks. The runtime
role does not own tables or have DDL/TRUNCATE permissions.

The existing WhatsApp viewer can use the root `.env` without copying credentials:

```powershell
./scripts/Start-WhatsAppViewer.ps1
# Explicit selection when multiple sessions exist:
./scripts/Start-WhatsAppViewer.ps1 -Session 'YOUR-SAVED-DEVICE-JID'
```

This still displays text in the terminal. Live pairing requires scanning the QR
code yourself; the old SQLite login and existing viewer configuration are preserved.

## Authentication and permissions

Passwordless login approves WhatsApp phone-number linking for a provisioned staff
user. With `APP_ENV=development`, the same start/status/exchange endpoints also
sign up an unknown number after WhatsApp approval, with the same request/response
bodies. Approval creates one user, one isolated INR development business and an
admin membership atomically with the verified WhatsApp binding. The initial user
name is their phone; the business name is `Development business PHONE`. No app
password is created. Abandoned, expired or wrong-number attempts create neither
staff nor businesses. Existing users retain their identities and memberships;
disabled users/memberships and archived businesses cannot bypass restrictions
through signup. Production/absent `APP_ENV` keeps
provisioned-staff login only. See [the onboarding TODO](../docs/TODO.md).

The first device obtains a linked access/refresh session; further devices
redeem a one-use token displayed on a signed-in device. There is only one active
WhatsApp link per user/number, enforced by database indexes and worker locks.
Send `Authorization: Bearer TOKEN`; PostgreSQL stores hashes of credentials.
Access lasts 15 minutes; rotating renewal lasts 30 days from successful renewal.
Logout deletes that device's session; process restarts preserve eligible sessions.
Linked-device revocation invalidates all the user's POS sessions and cancels old
pending receipts without deleting users or history. Temporary disconnects retain
login. Every business request still checks active membership and business state.

The old `POST /api/login` phone/password route is available only when
`ALLOW_PASSWORD_LOGIN=true` is explicitly enabled for local bootstrap/migration.
Its sessions last 12 hours and cannot issue device login tokens. Turn the flag off
for WhatsApp-only authentication; it defaults to false.

| Operation | Admin | Cashier |
|---|---|---|
| View/create customers | Yes | Yes |
| Upload/lookup shared contact names | Yes | Yes |
| Withdraw a mobile contact name | Own contribution | Own contribution |
| Register/view WhatsApp accounts in their business | Yes | Yes |
| Pair/connect an account or read its pairing code | Registering user only | Registering user only |
| View IPOs and form types | Yes | Yes |
| Create IPOs in the shared catalogue | Yes | No |
| View/create staff in their business | Yes | No |
| View/create/edit/cancel transactions and view history | Yes | Yes |

There is no separate signup endpoint or client-supplied actor identity. Login attempts are
limited to 10 per minute per source IP, with bounded process memory. The server
does not trust forwarded-IP headers. Multi-instance deployment will need shared
rate limits. Password reset, membership management/disable endpoints, MFA and
production onboarding are subsequent work.

## API contract

All business routes use `/api/businesses/{business_id}`. Get authorized business
IDs from `/api/me`. Supplying another business ID never confers access.

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Database readiness via a read-only `SELECT 1` |
| POST | `/api/login`, `/api/logout` | Opt-in legacy password login / current-device logout |
| POST | `/api/auth/whatsapp/start`, `/api/auth/whatsapp/status`, `/api/auth/whatsapp/exchange` | Start, inspect and consume a private first-device proof |
| POST | `/api/auth/device-tokens`, `/api/auth/device-tokens/exchange` | Generate on a signed-in device / redeem once on another device |
| POST | `/api/auth/refresh` | Rotate access/refresh credentials |
| GET, DELETE | `/api/sessions`, `/api/sessions/{id}` | List own sessions / revoke one own session |
| POST | `/api/logout-all` | Revoke all this user's POS sessions |
| GET | `/api/me` | User and active businesses |
| PUT | `/api/push-devices/{installationID}` | Register/replace an installation's FCM token for the authenticated POS session |
| GET, POST | `/staff` | List/create staff |
| GET, POST | `/customers` | List/create customers, including referrals |
| GET, POST | `/ipos` | List IPOs through their listing day/create shared catalogue entries |
| GET | `/ipos/import-status` | Source coverage, refresh runs and freshness |
| GET | `/form-types` | Supported form catalogue |
| GET, POST | `/whatsapp-accounts` | List/register business-owned WhatsApp accounts |
| GET | `/whatsapp-accounts/{id}` | Read one business-owned account |
| POST | `/whatsapp-accounts/{id}/pair` | Start QR or phone-code pairing (registering user) |
| POST | `/whatsapp-accounts/{id}/connect` | Reconnect the mapped saved session (registering user) |
| POST | `/whatsapp-accounts/{id}/check-number` | Check one number through the registering user's connected account |
| POST | `/whatsapp-accounts/{id}/contact-profile` | Read one contact's available WhatsApp profile through the registering user's connected account |
| GET | `/whatsapp-accounts/{id}/connection` | Connection state and current code (registering user) |
| GET | `/whatsapp-accounts/{id}/qr` | Current QR as an uncached PNG (registering user) |
| POST | `/whatsapp-accounts/{id}/disconnect` | Owner-only local revocation and best-effort remote unlink |
| GET, POST | `/transactions` | List/create transactions |
| GET, PUT | `/transactions/{id}` | Read or fully replace editable transaction fields |
| POST | `/transactions/{id}/cancel` | Preserve the transaction with cancelled status |
| GET | `/transactions/{id}/history` | Complete snapshots, newest first |

Amounts, rates, quantities, CE/PE values and IPO prices are **JSON strings**.
Inputs allow up to 12 integer digits and six fractional digits. Quantity must be
nonzero: positive means Buy and negative means Sell from the business's perspective.
Rate may be positive, negative or zero. Totals are calculated only in PostgreSQL as
`round(rate * quantity, 2)`. Either direction can have a positive, negative or zero
total; always derive direction from quantity, not rate or total. Category and CE/PE value
never multiply the amount.
`ce_pe_value` is mandatory for CE/PE and must be null/omitted for other form types.
`category` defaults to `none`; alternatives are `subject_1` and `subject_2`.
Timestamps use RFC3339, and IPO dates use YYYY-MM-DD.

Creation requires an `Idempotency-Key` header (1–200 bytes, unique within the
business). The first request returns 201; identical retries by its creator return
200 and `Idempotency-Replayed: true`, with the current transaction. Different
payloads/actors using that key return 409. The original normalized request hash
is retained even after edits. A legitimate second order must use a new key.

Edits and cancellations require `expected_version` and a nonempty `change_reason`.
A successful write advances the version and produces its snapshot atomically.
A stale version returns 409; a record outside the selected business returns 404.
Retries cannot create extra versions. Cancellation preserves amount and history;
use `status=confirmed` when listing current active transactions.

Lists use `limit` (default 50, maximum 100), return `items` and `next_cursor`, and
accept that cursor for the next page. Transactions additionally accept `ipo_id`,
`customer_id`, `status`, `from` (inclusive), and `to` (exclusive). Preserve filters
when following cursors. History uses `before_version` / `next_before_version`
instead of a timestamp cursor. All lists are bounded; transaction/customer/IPO
pagination uses indexed keys rather than offsets.

Requests reject unknown JSON fields, multiple JSON values and bodies over 64 KiB.
Errors use `{"error":{"code":"...","message":"..."}}`. Database errors do
not disclose SQL, connection credentials or customer data.

## Verification

```powershell
# From backend: unit checks and static analysis.
go test ./...
go vet ./...
# From repository root: full API/schema/session checks in a disposable database.
./database/Test-Schema.ps1 -WithApiTests -WithSessionTests -GoExecutable 'C:/Users/Admin/AppData/Local/Programs/Go1.27.1/go/bin/go.exe'
```

The integration tests run with a restricted runtime role and synthetic data in a
temporary cluster. They cover authentication, logout/expiry/restart persistence,
admin/cashier permissions, business isolation, referrals, decimal and CE/PE
validation, concurrent creation retries, mismatched idempotency requests,
optimistic edits, cancellation, pagination/filtering and immutable history.
They never modify your regular database or real WhatsApp accounts.

## WhatsApp account registration

Create an account with `POST /whatsapp-accounts` and `{"label":"Cashier phone"}`.
The API assigns a stable UUID, the authorized business and the signed-in user as
`linked_by_user_id`. Both admins and cashiers can register their own accounts and
view their business's accounts. Another business's account ID returns 404 when
queried through an authorized business route. Lists use bounded cursor pagination.

Registration prepares the ownership record for pairing. It returns
`pairing_state: "unpaired"` and a null phone number. The label is trimmed and must
contain 1–256 bytes. Each successful POST creates a new account; retain its returned
ID and use GET to inspect it. There is no registration replay key in this increment.
Caller-supplied business/user IDs, phone numbers, device JIDs and lifecycle fields
are rejected. The runtime role can insert only the three ownership/label columns;
it cannot supply or update session bindings. Account metadata responses omit the protocol device JID.

`paired` means a binding is persisted; it does not mean WhatsApp is online.
`unlinked` means the record has an unlink timestamp. The trusted pairing worker
establishes verified bindings and clears revoked/missing sessions. The terminal
viewer remains separate and does not attach its saved sessions to these records.

To revert this increment, revert its Git commit and remove only the additive index
and registration grant as the database owner. Existing account records can remain:

```sql
BEGIN;
REVOKE INSERT (business_id, label, linked_by_user_id) ON ipo_pos.whatsapp_accounts FROM ipo_platform_api;
DROP INDEX ipo_pos.whatsapp_accounts_created_idx;
COMMIT;
```

Integration tests also cover account ownership, admin/cashier registration,
cross-business denial, forged binding rejection, restricted column grants,
pagination and stable account identity across simulated pairing/unlinking.

## WhatsApp number registration checks

Use `POST /api/businesses/{businessID}/whatsapp-accounts/{id}/check-number` with
the registering user's bearer token and `{"phone_number":"+919876543210"}`.
The account must be paired and connected. Another staff member, including an admin,
cannot use this account for checks; other businesses cannot access it.

A successful response includes `phone_number`, `registered` and an ISO timestamp
in `checked_at`, for example
`{"phone_number":"+919876543210","registered":true,"checked_at":"2026-10-11T12:00:00Z"}`.
`registered:false` means WhatsApp reports the number as unregistered. This is a
registration check, not proof that the caller owns the queried number. Results can
change. Each successful check saves its result in `ipo_pos.whatsapp_number_checks`,
one row per business and phone number, with the checking account ID and timestamp.
Repeated checks replace that row; upstream failures preserve the previous result.
An unchecked number has no row, so unknown is never treated as false. No message is sent.

The existing `GET /api/businesses/{businessID}/contacts?phone_number=%2B919876543210`
returns `whatsapp_registered` (`true`, `false`, or `null` when unchecked) and
`whatsapp_checked_at` without performing another WhatsApp query. Status is shared
within the business and independent of names, customer IDs and contact suppliers.
Deleting a supplied name does not delete a successful number check. The POST always
queries WhatsApp again; stored results are not used to bypass a fresh lookup.

Numbers use the existing international-format validation (`+`, nonzero country-code
start, 7–15 digits); unknown JSON fields are rejected. Invalid input returns 400,
an unpaired/disconnected account returns 409, unavailable WhatsApp configuration or
a failed result write returns 503, an upstream error or incomplete response returns 502, and a timeout
returns 504. Error responses never contain `registered:false`; registration is
unknown when the lookup fails. Lookup errors do not expose upstream details.

Checks run serially inside the existing account worker, with an eight-second budget
including time waiting for that worker. Request cancellation and service shutdown
cancel the lookup. Use this endpoint for individual checks; bulk contact scanning
and a dedicated lookup rate limiter are not implemented. Apply migration 007 and
its SELECT grant before starting this version of the API.

## WhatsApp contact profiles

POST `/api/businesses/{businessID}/whatsapp-accounts/{id}/contact-profile` with
the account owner's bearer token and `{"phone_number":"+919876543210"}`.
The account must be paired and connected. The same owner-only and active-business
checks as number lookup apply, including to admins. Numbers use the existing
international-format validation; unknown JSON fields are rejected.

```json
{
  "phone_number": "+919876543210",
  "name": "Contact's WhatsApp name",
  "about": "Available",
  "picture_url": "https://example.com/photo.jpg",
  "picture_state": "available",
  "profile_revision": "42"
}
```

`name` comes from this account's synced WhatsApp push name (phone or privacy LID),
falling back to a synced business name; it is `null` when unavailable. It may be
stale and is not the caller's uploaded address-book alias or identity verification.
`about` is the available WhatsApp About text, or `null` when empty/hidden/unavailable;
the library cannot distinguish those cases. Photo privacy is respected:
`picture_state` is `available`, `hidden`, or `not_set`, with a `null` URL in the
last two cases. A hidden photo still returns 200 with other available details.
Photo URLs can expire; fetch the profile again when needed. The frontend should
display returned text as text, not HTML, and use a placeholder for missing photos.

Invalid requests return 400; unpaired/disconnected accounts return 409; disabled
WhatsApp configuration returns 503; upstream/storage errors or incomplete responses
return 502; cancellation/deadline expiry returns 504. Failed requests return no
partial profile or upstream error details. Missing user info does not establish
whether the number is registered; use `/check-number` for registration checks.

Lookups run serially in the existing account worker with an eight-second budget,
including waiting; service shutdown cancels them. Responses are `no-store`.
No WhatsApp message is sent, business contact/customer records and registration
checks are not updated, and photos are not downloaded or archived on the server.
The library may update its existing synced business names/LID mappings.

Migration `011_contact_profile_push.sql` adds persistent photo revisions, session-bound
FCM registrations and an asynchronous notification outbox. Successful profile reads
now include `profile_revision`, a positive decimal integer encoded as a string.
Picture-change/removal events and observed hidden/unset transitions invalidate
registered devices. Failures never count as removal. Sending is disabled unless
`FCM_PROJECT_ID` and private server credentials are configured; Android registration
and cache handling are still required. See the complete
[Android/FCM handover](../docs/FCM_ANDROID_HANDOVER.md) for migration/grants, credential
setup, registration/event contracts, retries and remaining live checks.
Startup requires migration 011 even when sending is disabled. Bulk lookup, dedicated
rate limiting and a profile screen remain unimplemented; fetch selected contacts on demand.

## Authenticated pairing and reconnecting

Set `WHATSAPP_DATABASE_URL` in the server environment/configuration, pointing to
the same PostgreSQL database as `DATABASE_URL`. The local `.env` already does this.
The worker initializes library tables under `whatsapp`; no application migration
or extra privileges for `ipo_platform_api` are required by this increment.
Without the worker URL, manual APIs continue to work and connection routes return 503.
The worker's local owner connection can update bindings; deployment should supply
a dedicated trusted role with WhatsApp schema and account binding permissions.

1. Register an account, then retain its returned UUID. All following routes require
   a bearer token for that same registering user, including when another staff member is an admin.
2. `POST /whatsapp-accounts/{id}/pair` with `{"method":"qr"}` starts QR pairing.
   Alternatively send `{"method":"phone","phone_number":"+YOUR_REAL_NUMBER"}`.
3. Poll `GET .../{id}/connection` every two seconds. `awaiting_qr` returns QR data
   in `code` and its `expires_at`; `GET .../{id}/qr` renders the current code as PNG.
   `awaiting_phone` returns the phone code. These responses use `Cache-Control: no-store`;
   never log, share or retain pairing codes in application storage.
4. Scan the PNG from WhatsApp's Linked devices screen, or choose Link with phone
   number and enter the phone code. Pairing must reach `connected` before it is complete.
   `finishing_pairing` covers WhatsApp's required reconnect after accepting the pairing.

For the local bootstrap admin, the helper performs authenticated polling and logs
out its temporary application session when finished. Register the account first
through `backend/requests.http`, then run:

```powershell
./scripts/Pair-WhatsApp.ps1 -AccountId 'YOUR-REGISTERED-UUID'
# Open the temporary PNG path printed by the helper; it refreshes as codes rotate.
# Or, when linking from the same phone:
./scripts/Pair-WhatsApp.ps1 -AccountId 'YOUR-REGISTERED-UUID' -Method phone -WhatsAppPhone '+YOUR_REAL_NUMBER'
```

The helper deletes its temporary QR file on exit. Other staff use their own tokens
through the documented API; the helper uses only the local bootstrap credentials.
`POST /pair` retries reuse the same active method/number; changing either during an
attempt returns 409. Starting a connection returns 202; inspect `/connection` for
asynchronous failures such as `busy`, `connection_failed`, `pairing_failed` or
`binding_failed`. A persisted account already paired returns 409 from `/pair`.

Saved mapped sessions reconnect on API startup. Owners can also `POST .../{id}/connect`;
retries reuse an existing connected client. Temporary interruptions reconnect through
whatsmeow. If the primary phone revokes the device, the worker clears the binding and
reports `needs_relink`; the application account ID and business records remain intact.
Missing saved credentials also require relinking. Use the same account UUID for a new
pairing. To explicitly unlink, revoke the device in WhatsApp's Linked devices screen.

Each active account holds a PostgreSQL advisory lock on a dedicated connection.
Another API process reports `busy` before loading its session keys. Workers discard
the lock connection on exit, and recheck account/membership/business eligibility and
database connectivity every 15 seconds. Failed checks stop the connection. Do not
run the standalone viewer using a session simultaneously managed by this API;
the viewer does not participate in application account locks.

This initial worker supports 32 active accounts per process and retains at most
64 connection states. Startup restores at most 32 eligible mappings. Increase
the worker/pool capacity before larger deployments. A crash after the library saves
new credentials but before their verified binding is written can leave an unclaimed
device: remove that stale device on the phone and pair again. Existing arbitrary
saved sessions are never adopted through caller-supplied JIDs. Passkey pairing
challenges are currently reported as pairing failures.

The isolated integration suite uses fake protocol clients plus real PostgreSQL to
check QR PNGs, owner-only secret access, retry reuse, phone codes, verified binding,
restart recovery, revoked/missing sessions, disabled membership and cross-process
account locks. It never pairs a real phone. The helper has an offline self-check:

```powershell
./scripts/Test-PairWhatsApp.ps1
```

Live phone-code pairing was verified locally on 2026-10-10. The registered account
retained its ID and verified phone binding after backend restarts, and startup
restored the connection without another pairing or connect request. The connected
response no longer exposed a pairing code. Live QR pairing and device revocation
have not been checked; those paths have isolated protocol tests.

Message ingestion, web/mobile UI, AI extraction/reconciliation and analytics remain
subsequent milestones. The worker can send requested POS confirmations; it does not
archive conversations yet.

## Shared business contact names

After the mobile app obtains contacts permission and the user chooses which contacts
to share with a business, POST `/api/businesses/{businessID}/contacts` with:

```json
{"contacts":[{"phone_number":"+919000001010","name":"Customer name on this phone"}]}
```

Send 1–100 distinct numbers per request in international format. The existing 64 KiB
JSON limit applies. The batch is atomic. Uploads update only the authenticated user's
name for a number; unchanged replays preserve its timestamp. Source (`mobile_contacts`),
supplier ID, receipt time and last name-change time are recorded on the server.
One user's upload cannot forge another supplier or overwrite their name.

GET the same route with `?phone_number=%2B919000001010` to resolve a number across
the business. It returns matching customer records, up to 100 name sources with
staff attribution, total counts, a personal `display_name`, and a suggested name.
`display_name` uses the requesting staff member's uploaded name first, otherwise
the latest colleague contribution (ties use supplier ID); it is `null` without
an uploaded name. It includes `name`, `source`, `supplied_by_user_id`,
`supplied_by_name` and `is_own`. A single active customer's
chosen name takes precedence; otherwise the latest contributed name is suggested
with its source. Multiple customers sharing a number remain distinct; names are
not identity verification and no customer is automatically created or merged.
New customer records record their name's author; older records have an unknown author.

DELETE the same route with that number withdraws only the caller's contribution.
An empty lookup returns no suggestion. Reads and writes require active business
membership; another business's contacts are inaccessible. The server cannot verify
that a phone permission prompt was accepted: the mobile app must obtain consent and
handle permission withdrawal. This API stores the current name per staff/number,
not a history of all previous spellings. Removing migration 004 requires backing up
and dropping `contact_names` and the new customer attribution column after reverting
its callers; reverting Git alone does not remove database records.

For a contact dropdown, GET `/api/businesses/{businessID}/contacts?q=Mehul&limit=20`
without `phone_number`. This lists uploaded business contacts, one item per number,
using the same personal-name preference as `display_name`. Each item has
`phone_number` plus those five display-name fields. Search is a case-insensitive
literal substring of the displayed name or number; another colleague's hidden alias
does not match when your own name is displayed. `%` and `_` are literal characters.
`q` is trimmed, accepts at most 256 UTF-8 bytes, and may be empty to browse the directory.
Limits are 1–100 (default 50). Results are ordered by number, with `items: []` for no
matches and `next_after_phone: null` on the last page. For another page, URL-encode
`next_after_phone` as `after_phone`, retaining the same query/limit. Reset pagination
when the query or business changes. This route uses `after_phone`, not the UUID
`cursor` used by other lists. Customer-only records are available through `/customers`,
not this uploaded-contact directory; resolve a selected number to choose its customer.

The Android UI remains to be built. Debounce text edits by about 250 ms, cancel
the previous request, and apply only the latest request's response, checking its
request ID, business/session and current input. Fetch the first page with `limit=20` for each edit; do not poll
every second while idle. Editing the text invalidates any previous selected number.
Saved uploads/withdrawals are visible on the next request; there is no server push.
Substring search scans the business directory; production-sized load testing and
search indexes remain future work if measured latency requires them.

## Mobile POS confirmations

Use the existing login and `Authorization: Bearer TOKEN` for each request; obtain
business IDs from `/api/me`. The server determines the actor and checks active user,
business and role. The mobile app must store tokens in protected platform storage,
honour expiry/logout and use HTTPS in deployment. Do not embed bootstrap credentials
or a shared master token in the app. The local HTTP listener is for development.

To request a WhatsApp receipt, add `whatsapp_account_id` to the existing transaction
POST body and send the same `Idempotency-Key` on every retry of that transaction. The account
must be paired, active, in the business and registered by the requesting user. A
missing customer phone or an unavailable/unauthorised sender rejects the creation
atomically. Omitting the new field preserves existing manual-entry behaviour.

The transaction, initial immutable version, retry fingerprint and confirmation intent are
saved in one transaction. The server composes the receipt from saved customer,
IPO/form, category, quantity, rate, database-calculated total, time and transaction reference;
clients cannot supply an arbitrary message body. The receipt is a snapshot of the
initial transaction; later edits do not silently rewrite it. Replays reuse the transaction and
confirmation and reject changed data or a different sender under the same key.

The POST response includes `whatsapp_confirmation` when requested. GET
`/api/businesses/{businessID}/transactions/{id}/confirmation` reads the latest status
and receipt under normal business permissions; no confirmation returns 404.
The queued intent survives restart. The trusted worker claims one pending receipt
per connected account every two seconds and sends with a 20-second timeout. Its
existing account lock prevents competing workers from using the same session.
It rechecks current ownership/membership/binding, customer phone and transaction version;
cancellation blocks the send, and changed recipients, senders or transaction versions fail
the pending receipt. New transaction confirmations can queue while the account is offline.
An active worker configuration (`WHATSAPP_DATABASE_URL`) is needed to deliver them.

Statuses are `pending`, `sending`, `sent`, `uncertain`, `failed` and `cancelled`.
`sent` means WhatsApp acknowledged the message, not that the customer read it.
The message ID is persisted before sending. A send error, mismatched acknowledgement,
or worker restart after an interrupted attempt produces `uncertain`; it is never
automatically resent. The transaction remains saved. An uncertain result may have reached
the customer and needs review. There is currently no manual resend endpoint; this
conservative single-attempt policy avoids blind duplicates. Receipt IDs must also
be used by the future detector to recognise confirmations for existing transactions.

Migration 005 rollback requires backing
up pending/attempted delivery records and dropping its table after reverting callers;
Git rollback alone does not undo the database migration.

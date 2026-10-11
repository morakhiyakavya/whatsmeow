# IPO transaction API

This Go service implements the manual ledger milestone. It uses PostgreSQL for
application sign-in, business membership, customers, the shared IPO/form catalogue,
transactions, complete immutable transaction history, and business-owned WhatsApp
account registration. No frontend is required to exercise the API.

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
Read `BOOTSTRAP_ADMIN_PHONE` and `BOOTSTRAP_ADMIN_PASSWORD` from `.env` to sign in.
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

In another terminal:

```powershell
./scripts/Smoke-Api.ps1
```

This checks health, login, business access, the catalogue and WhatsApp account
reads, then logs out. It does not create demo customers or ledger entries. For a full flow, use
`backend/requests.http` or the isolated integration runner below.

## Database credentials

The root `.env` contains randomly generated local credentials:

| Setting | Purpose |
|---|---|
| `DATABASE_URL` | `ipo_platform_api` runtime role; no schema ownership, transaction deletion or truncation |
| `MIGRATION_DATABASE_URL` | `ipo_platform_owner`, which owns the application schemas |
| `WHATSAPP_DATABASE_URL` | Trusted pairing worker/viewer connection for WhatsApp migrations, sessions and verified account bindings |
| `BOOTSTRAP_ADMIN_PASSWORD` | Initial app password, hashed with bcrypt when bootstrapped |

PostgreSQL listens on localhost:5432; the application database is `ipo_platform`.
Passwords are never printed by the setup or smoke scripts. `.env` is ignored by
Git and its Windows ACL grants access only to the current user and SYSTEM.
The connection uses `sslmode=disable` for local development. A remote deployment
needs verified TLS and credentials supplied privately to the server.

For a new database, apply `database/001_initial.sql`, `002_api.sql` and
`003_whatsapp_accounts.sql` and `004_contact_names.sql` once, in that order, as its migration owner. The API
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

The runtime grants are in `scripts/Setup-LocalDatabase.ps1`; use your runtime role
name when applying them elsewhere. The narrowly scoped identity-column UPDATE
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

`POST /api/login` accepts `phone_number` and `password`, returning a random bearer
token with a 12-hour expiry. Send `Authorization: Bearer TOKEN`. PostgreSQL stores
only the token's SHA-256 hash. Logout deletes the session; process restarts preserve
active sessions. Users with disabled accounts cannot authenticate. Every business
request checks the active membership and business state against PostgreSQL.

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

There is no public signup or client-supplied actor identity. Login attempts are
limited to 10 per minute per source IP, with bounded process memory. The server
does not trust forwarded-IP headers. Multi-instance deployment will need shared
rate limits. Password reset, membership management/disable endpoints, MFA and
public onboarding are subsequent work.

## API contract

All business routes use `/api/businesses/{business_id}`. Get authorized business
IDs from `/api/me`. Supplying another business ID never confers access.

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Database readiness |
| POST | `/api/login`, `/api/logout` | Sign in/out |
| GET | `/api/me` | User and active businesses |
| GET, POST | `/staff` | List/create staff |
| GET, POST | `/customers` | List/create customers, including referrals |
| GET, POST | `/ipos` | List/create shared IPO catalogue entries |
| GET | `/form-types` | Supported form catalogue |
| GET, POST | `/whatsapp-accounts` | List/register business-owned WhatsApp accounts |
| GET | `/whatsapp-accounts/{id}` | Read one business-owned account |
| POST | `/whatsapp-accounts/{id}/pair` | Start QR or phone-code pairing (registering user) |
| POST | `/whatsapp-accounts/{id}/connect` | Reconnect the mapped saved session (registering user) |
| GET | `/whatsapp-accounts/{id}/connection` | Connection state and current code (registering user) |
| GET | `/whatsapp-accounts/{id}/qr` | Current QR as an uncached PNG (registering user) |
| GET, POST | `/transactions` | List/create transactions |
| GET, PUT | `/transactions/{id}` | Read or fully replace editable transaction fields |
| POST | `/transactions/{id}/cancel` | Preserve the transaction with cancelled status |
| GET | `/transactions/{id}/history` | Complete snapshots, newest first |

Amounts, rates, quantities, CE/PE values and IPO prices are **JSON strings**.
Inputs allow up to 12 integer digits and six fractional digits. Quantity must be
positive; rate must be nonnegative. Totals are calculated only in PostgreSQL as
`round(rate * quantity, 2)`. Category and CE/PE value never multiply the amount.
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

Message ingestion, manual confirmation sending, web/mobile UI, AI extraction and
reconciliation, and analytics remain subsequent milestones. This worker does not
send customer messages or archive conversations yet.

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
staff attribution, total counts, and a suggested name. A single active customer's
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

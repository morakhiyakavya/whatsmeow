# IPO transaction API

This Go service implements the manual ledger milestone. It uses PostgreSQL for
application sign-in, business membership, customers, the shared IPO/form catalogue,
transactions, and complete immutable transaction history. No frontend is required
to exercise the API.

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

This checks health, login, business access and the catalogue, then logs out. It
does not create demo customers or ledger entries. For a full flow, use
`backend/requests.http` or the isolated integration runner below.

## Database credentials

The root `.env` contains randomly generated local credentials:

| Setting | Purpose |
|---|---|
| `DATABASE_URL` | `ipo_platform_api` runtime role; no schema ownership, transaction deletion or truncation |
| `MIGRATION_DATABASE_URL` | `ipo_platform_owner`, which owns the application schemas |
| `WHATSAPP_DATABASE_URL` | Owner connection for the viewer's library-managed WhatsApp migrations/session store |
| `BOOTSTRAP_ADMIN_PASSWORD` | Initial app password, hashed with bcrypt when bootstrapped |

PostgreSQL listens on localhost:5432; the application database is `ipo_platform`.
Passwords are never printed by the setup or smoke scripts. `.env` is ignored by
Git and its Windows ACL grants access only to the current user and SYSTEM.
The connection uses `sslmode=disable` for local development. A remote deployment
needs verified TLS and credentials supplied privately to the server.

For an existing database, apply `database/001_initial.sql` and `002_api.sql` once,
in that order, as its migration owner. The API does not execute migrations on
startup. For an existing installation of the initial schema, apply only `002`.
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

WhatsApp message ingestion into the business archive, manual confirmation sending,
web/mobile UI, AI extraction/reconciliation and analytics remain subsequent milestones.

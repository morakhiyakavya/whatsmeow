# Supabase development database setup

Use a fresh Supabase project for this application. The Go backend will connect to
PostgreSQL directly; the mobile app will call the Go API. Existing local data and
WhatsApp credentials are not copied by this setup.

## Current development project

Project `tqfwoudoxundtrhogmwa` was installed and verified on 11 October 2026 using
the Singapore session pooler on port `5432`. PostgreSQL reports version 17.11.
All ten application migrations are installed: `ipo_pos` has 22 tables, and the
backend initialized 17 WhatsApp credential tables at library schema version 16.
No users, businesses or linked WhatsApp devices have been provisioned there yet.
Do not rerun the fresh-install script for this project.

Migration 008 was applied on 11 October 2026 and verified on both current
transactions and immutable history. Quantity is nonzero and signed: positive for
Buy, negative for Sell. The existing total formula is unchanged and uses the signed
quantity. Rollback-only schema assertions passed against this database, including
Sell amounts, direction corrections and preserved history; no test records remain.
Migration 009 was also applied on 11 October 2026. Both current and historical
rates accept positive, negative and zero values, while NaN remains rejected.
Hosted rollback-only checks passed for all quantity/rate sign combinations,
rate edits, zero rates and immutable history. Buy/Sell still comes only from
quantity, so a Sell can have a positive total and a Buy can have a negative total.
No test records remain. Migration 010 was applied on 11 October 2026 and its
signup columns, identity constraints and restricted runtime permissions were
verified with rollback-only schema assertions. No hosted users/businesses were
created. The separate local database now has migrations 008, 009 and 010 too;
its existing user and business were preserved. Both private configuration files
use `APP_ENV=development`; Render now uses the same setting on its deployed
signup build. Real WhatsApp signup approval remains unchecked.

Generated, separate role credentials are saved privately in the Git-ignored
root `.env.supabase`, with file access restricted to your Windows account and
SYSTEM. The verified root certificate is in `.env.supabase-ca.crt`. Your existing
local credentials and data were preserved.

To run against this database locally, from the repository root:

```powershell
Set-Location backend
go run . -env ../.env.supabase
```

The private configuration binds to `127.0.0.1:8181`. Environment variables override
the file, so remove any existing application connection overrides before testing.
For Render, copy the two runtime URLs privately from this file and replace each
URL's `sslrootcert` value with `database/supabase-ca.crt`, the bundled public CA.
Omit the local `API_ADDR`; the service uses Render's `PORT` to listen publicly.
Follow the [Render deployment guide](RENDER_DEPLOYMENT.md).
Keep the migration URL for administration. Never paste credentials into chat.

The project administrator password was shared in chat during setup. Rotate it
through Supabase Database Settings and update `supabase_database_passowrd` in
your local `.env`. The application uses the separate generated role passwords.

## 1. Check the project

In Supabase SQL Editor, run this read-only query:

```sql
SELECT current_database(), current_user, version();
```

PostgreSQL 17 or later is required. Supabase's database is normally named
`postgres`; keep that name. Prefer a region near the Render API, currently Singapore.

In the project's Connect dialog, select **Session pooler** and copy its host,
port and username. Typically the port is `5432` and the admin username is
`postgres.PROJECT_REF`. Copy the actual values rather than constructing the host.
The session pooler supports IPv4 and session-level locks required by our worker:
[Supabase connection modes](https://supabase.com/docs/guides/database/connecting-to-postgres).

Keep `ipo_pos` and `whatsapp` out of the project's exposed Data API schemas.
The backend enforces business access; these tables should be accessed through it.
Custom schemas are exposed explicitly through Data API settings:
[Supabase custom schemas](https://supabase.com/docs/guides/api/using-custom-schemas).

## 2. Connect privately from PowerShell

Download the project's root certificate from Database Settings → SSL Configuration
and save it outside Git, for example as `C:/Users/Admin/Downloads/supabase-ca.crt`.
Keep hostnames intact so certificate validation can check the destination.

From the repository root, replace the three placeholders below with your actual
session-pooler values. These are connection metadata, not passwords.

```powershell
$env:PGSSLMODE = 'verify-full'
$env:PGSSLROOTCERT = 'C:/Users/Admin/Downloads/supabase-ca.crt'
& 'C:/Program Files/PostgreSQL/17/bin/psql.exe' -X -W -h 'POOLER_HOST' -p 5432 -U 'postgres.PROJECT_REF' -d postgres
```

Enter the project's database password at PostgreSQL's password prompt. This is
the database password chosen when creating the project, not your Supabase account
password. Keep it out of chat and command arguments. If certificate validation or
connection fails, stop and inspect the redacted error and the copied endpoint;
do not disable certificate validation to work around it.

## 3. Install the application storage

At the `postgres=>` prompt, run:

```text
\i 'C:/Users/Admin/Documents/programming/whatsmeow/database/Setup-Supabase.psql'
```

The [setup file](../database/Setup-Supabase.psql) installs our eleven existing
migrations and the [shared runtime grants](../database/api_grants.sql). It creates:

| Role | Purpose |
|---|---|
| `ipo_platform_api` | Restricted connection for normal API requests |
| `ipo_platform_owner` | Application migration owner and trusted WhatsApp worker |

Supabase supports separate PostgreSQL login roles:
[Postgres roles](https://supabase.com/docs/guides/database/postgres/roles).
The setup does not change Supabase's managed schemas or its admin password.
It checks for existing application schemas/roles before creating anything. If an
installation or partially applied setup already exists, it stops; apply only
missing migrations after review. Migrations commit individually, so a later error
can leave the earlier migrations installed. Do not drop schemas or blindly rerun.

After the success message, set a different generated password for each new role:

```text
\password ipo_platform_owner
\password ipo_platform_api
```

These commands prompt privately and ask you to repeat each password. Store them
in your password manager. The new roles cannot authenticate by password until
you set their passwords. This does not change the project's `postgres` password.

Verify the installation:

```sql
SELECT schemaname, count(*) AS tables
FROM pg_tables
WHERE schemaname IN ('ipo_pos','whatsapp')
GROUP BY schemaname;

SELECT
  has_schema_privilege('ipo_platform_api','ipo_pos','USAGE') AS api_can_use_app,
  has_schema_privilege('ipo_platform_api','whatsapp','USAGE') AS api_can_read_keys,
  has_table_privilege('ipo_platform_api','ipo_pos.transactions','DELETE') AS api_can_delete_sales;
```

The permission results must be **true, false, false**. The `whatsapp` schema is
initially empty: the backend creates the library's credential tables on startup.
Exit with `\q`.

## 4. Configure the hosted backend

In Render's private environment settings, use the copied session-pooler endpoint
with these custom-role usernames:

| Setting | Username |
|---|---|
| `DATABASE_URL` | `ipo_platform_api.PROJECT_REF` |
| `WHATSAPP_DATABASE_URL` | `ipo_platform_owner.PROJECT_REF` |
| `MIGRATION_DATABASE_URL` (administration only) | `ipo_platform_owner.PROJECT_REF` |

The format is:

```text
postgresql://ROLE.PROJECT_REF:ENCODED_ROLE_PASSWORD@POOLER_HOST:5432/postgres?sslmode=verify-full&sslrootcert=database/supabase-ca.crt
```

The repository bundles Supabase's public root certificate in
`database/supabase-ca.crt`; it contains no passwords or private keys. Alternatively,
upload the certificate as a Render secret file and use its actual mounted path in
the URL: [Render secret files](https://render.com/docs/configure-environment-variables).
Percent-encode reserved characters in passwords.
Use each new role's password, not the project's admin password. Session-pooler
custom-role usernames follow `ROLE.PROJECT_REF`:
[Supabase connection strings](https://supabase.com/docs/guides/database/connecting-to-postgres).
Keep `ALLOW_PASSWORD_LOGIN=false` for the passwordless application.
Use `APP_ENV=development` only for the development service to let new numbers
sign up through WhatsApp approval. Apply `010_development_signup.sql` after 009;
no new runtime grants are required. Production/unset `APP_ENV` disables signup.

After startup, verify `/health` and the account connection state. Development
signup creates an isolated business/admin only after WhatsApp approval. Otherwise bootstrap the
initial business/admin separately using the actual registered WhatsApp number;
see [the API guide](../backend/README.md) and [mobile authentication](POS_AND_CONTACTS_GUIDE.md).
The free database can pause after low activity; see
[Supabase's pausing policy](https://supabase.com/docs/guides/platform/free-project-pausing).

## Inspect tables and apply future migrations

Open this Supabase project → SQL Editor → New query and run:

```sql
SELECT schemaname, count(*) AS tables
FROM pg_tables
WHERE schemaname IN ('ipo_pos', 'whatsapp')
GROUP BY schemaname;

SELECT schemaname, tablename
FROM pg_tables
WHERE schemaname IN ('ipo_pos', 'whatsapp')
ORDER BY schemaname, tablename;

SELECT code, name FROM ipo_pos.form_types ORDER BY code;
```

Currently expect 22 POS tables, 17 WhatsApp tables and seven form types seeded by
migration 001. In Table Editor, select schema `ipo_pos` to browse the application
tables. Many tables are empty until you create a business, staff or sales.
The `whatsapp` schema contains private linked-device credentials; inspect its
structure without sharing credential rows. You do not need to expose either
schema in the Data API to inspect it in the dashboard.

A migration is a versioned SQL file that changes the database structure or seed
data. Our ten numbered files already ran on this project. The API does not apply
them on startup, and these files are not tracked by Supabase CLI's migration
history. Do not run `supabase db push` expecting it to recognise this installation.

For a future change, we add and test the next numbered file, review its effect on
existing data, apply it once through the migration-owner connection, verify the
specific new table/column/constraint and record the result. Never edit an already
applied migration to change existing databases. A Render redeploy alone does not
apply new POS SQL.

From the repository root, this is the command template for a future migration;
`NEXT_MIGRATION.sql` is a placeholder and does not currently exist:

```powershell
$env:PGSSLMODE = 'verify-full'
$env:PGSSLROOTCERT = (Resolve-Path database/supabase-ca.crt).Path
& 'C:/Program Files/PostgreSQL/17/bin/psql.exe' -X -W -h 'aws-0-ap-southeast-1.pooler.supabase.com' -p 5432 -U 'ipo_platform_owner.tqfwoudoxundtrhogmwa' -d postgres -v ON_ERROR_STOP=1 -f 'database/NEXT_MIGRATION.sql'
```

Enter the generated **owner-role password** privately at the prompt; it is the
password component of `MIGRATION_DATABASE_URL` in your private `.env.supabase`,
not the project's `postgres` password. If new API permissions are needed, update
the shared `database/api_grants.sql` and apply it as the project administrator:

```powershell
& 'C:/Program Files/PostgreSQL/17/bin/psql.exe' -X -W -h 'aws-0-ap-southeast-1.pooler.supabase.com' -p 5432 -U 'postgres.tqfwoudoxundtrhogmwa' -d postgres -v ON_ERROR_STOP=1 -v database_name=postgres -v api_role=ipo_platform_api -f database/api_grants.sql
```

The second prompt uses the project's administrator password. Applying a grant
file again is safe; rerunning a schema-creation migration is not necessarily safe.
If applying a migration fails, stop and inspect what committed before retrying.

SQL Editor can also execute a reviewed migration's SQL. Run `SET ROLE
ipo_platform_owner;`, the complete SQL file, and `RESET ROLE;` together to preserve
application schema ownership. Do not paste the fresh `Setup-Supabase.psql` or
`api_grants.sql` into SQL Editor: they contain psql-specific commands/variables.
Use SQL Editor for inspection unless a specific migration has been reviewed for
that execution path. Never run the destructive disposable `schema_test.sql`
against your hosted project.

For data-changing migrations, arrange a backup first. A compensating SQL migration
can reverse some schema changes; reverting Git does not restore deleted data or
automatically reverse a deployed migration.

## Verification and remaining work

The disposable runner can exercise the setup as a non-superuser administrator,
including existing-installation rejection, schema guards and the shared API grants:

```powershell
./database/Test-Schema.ps1 -WithHostedSetupTests -WithApiTests -WithSessionTests
```

The actual development project also passed certificate and hostname verification,
authentication with both custom roles, and a runtime insert/read probe that was
rolled back. The API role cannot access WhatsApp credentials, delete sales or
inherit the owner role; Supabase's `anon`, `authenticated` and `service_role`
roles cannot use either application schema. Backend startup against the hosted
database passed `/health` (200), unauthenticated `/api/me` rejection (401) and
disabled password-login rejection (403). The temporary server was stopped.

The [Render development API](RENDER_DEPLOYMENT.md#current-deployment) is deployed;
its public HTTPS health and authentication boundary checks passed. Initial
business/admin provisioning, live WhatsApp pairing and saved-session restoration
remain unverified. Replacement workers now wait for the
previous deployment's locks and resume through the existing account checks;
disposable integration tests cover takeover and shutdown while waiting. A healthy
HTTP endpoint alone does not prove WhatsApp is connected. Database changes are
not undone by reverting a Git commit.

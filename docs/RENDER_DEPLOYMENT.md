# Render development API

Deploy the existing Go API and WhatsApp worker as one web service. Supabase stores
both POS records and WhatsApp credentials. The mobile app uses the service's HTTPS
URL; it never receives database credentials. This deployment does not provision a
business/admin or copy local WhatsApp sessions.

## Current deployment

Deployed and verified on 19 October 2026 in `kavya's Workspace`:

- API base URL: [whatsapp-pos-dev](https://whatsapp-pos-dev.onrender.com).
- [Live health endpoint](https://whatsapp-pos-dev.onrender.com/health).
- [Render dashboard](https://dashboard.render.com/web/srv-db5nundckfvc73arsvkg).
- Deployed commit: `038317029b7bf5ae42774baceb37c49a5b98f1e5` on `main`.
- Deployment: `dep-dbb3o7jl550s7387akgg`; service: `srv-db5nundckfvc73arsvkg`.
- One Free instance in Singapore; automatic deployments disabled.

Render reported the deployment live. Public HTTPS checks passed: `/health` returns
200 with `{"status":"ok"}`, unauthenticated `/api/me` returns 401, and password
login returns 403. Responses carry `Cache-Control: no-store`. The server started
on `0.0.0.0:10000` using the two private Supabase role connections and bundled CA.

The concise transaction confirmations were deployed on 19 October 2026 at
15:50:34 UTC (21:20:34 IST). Render built the exact commit above using its existing
cache, started the server and reported `live`. Health returned 200 with
`{"status":"ok"}`; unauthenticated me and transaction-list requests returned 401,
all with `no-store`. Deployment-window error logs were empty. New confirmations
use `{ipo} ipo {Buy|Sell} {forms} {form} {optional category} at {rate}`. RETAIL,
RETAIL_199, SHNI and BHNI show `alloted` for category `none`; CE, PE and SHARES
always omit category. Previously queued bodies remain unchanged. Reused passing
database/API, simulated delivery, 14 formatting cases and vet checks from Batch 33.
No migration, environment or scheduler change was needed. No authenticated hosted
transaction or real customer message was created; live delivery remains unchecked.

The approved Sentry deployment also publishes the existing contact search/profile
commits and the read-only database health query. Only `SENTRY_DSN` and
`SENTRY_ENVIRONMENT=development` were merged into Render's environment; the update
triggered the exact pushed commit, so no duplicate deployment was requested.
Sentry confirmed the hosted `IPO API started` log and HTTP traces tagged with that
release. The active uptime monitor subsequently returned HTTP 200. The temporary
verification route is absent (404/no-store), and deployment-window error logs were
empty. Real WhatsApp profile retrieval, signup and customer delivery remain unchecked.

This deployment supports two-way transactions through signed quantity: positive
means Buy, negative means Sell, and zero is rejected. Rate may be positive, negative
or zero. The generated total is the rounded product of signed quantity and rate;
its sign does not determine direction. Confirmations use Transaction wording
and derive Buy/Sell only from quantity. Supabase migrations 008 and 009 were
installed and rollback-only schema assertions passed before this deploy. Render built the exact
pushed commit and reported it live; startup succeeded with the migration guard.
Post-deploy health/authentication/no-store checks passed and no error-level logs
were returned for the deployment window. No test staff or real messages were created.

The integration could not set an HTTP health-check path, so the actual service
uses Render's default TCP check. In its dashboard, open Settings → Health Check
Path and set `/health` to include database availability in platform health checks.
The Blueprint already specifies this value for a Blueprint-created service.
Live WhatsApp signup/pairing approval remains unchecked.

Development signup is deployed. Supabase migration 010 is installed and verified,
and Render's environment was updated with `APP_ENV=development` while preserving
the existing credentials and disabled password login. The environment update
triggered a deployment of the exact pushed signup checkpoint; no second deploy
was triggered. The service reached live, listened on `0.0.0.0:10000`, passed public
health/authentication/no-store checks and returned no deployment-window errors.
The same WhatsApp start/status/exchange bodies support login and approved new-number
signup. No real signup or customer message was performed during verification.

The FCM contact-photo invalidation backend was deployed on 14 October 2026.
Applied Supabase migration `011_contact_profile_push.sql` and the shared runtime
grants first, preserving the existing user and business. Verified the new tables,
restricted runtime writes and rollback-only registration/revision/outbox/disablement
behavior; no probe records remain. Pushed FCM commit `c269a0e` and triggered
one manual deployment with the existing build cache. Render built that exact commit
and reported it live; startup passed the migration-011 guard and listened on
`0.0.0.0:10000`. Public health 200, unauthenticated me 401, disabled password login
403 and unauthenticated PUT `/api/push-devices/{installationID}` 401 all passed
with `no-store` headers. Deployment-window error logs were empty.

No Render environment values or secret files were changed by this deploy. No FCM
server credentials are configured in the local hosted environment file; actual
Google credential readiness, authenticated hosted registration/profile requests and
live device delivery remain unverified. Sending requires `FCM_PROJECT_ID` and a
private server credential file, and Android still needs token/event integration;
see [the Android/FCM handover](FCM_ANDROID_HANDOVER.md). The local application
database had not received migration 011 at that deployment. No real WhatsApp/customer message was sent.

The account-recovery API was deployed on 17 October 2026 at 12:20:24 UTC
(17:50:24 IST), after applying migration `012_account_recovery.sql` and shared
runtime grants to Supabase. Local PostgreSQL received missing migrations 011/012
and the same grants. Verified migration ownership, client-to-pooler TLS with
certificate/hostname verification, restricted runtime permissions, rollback-only
session deletion/audit probes and preserved account, membership, session and
transaction/retry/confirmation identities. No probe records remain.

Render built the exact reviewed commit with its cache, passed the migration-012
startup guard, listened on `0.0.0.0:10000` and reached `live`. Hosted checks passed:
health 200, unauthenticated me/logout-all 401, disabled password login 403,
linked-account start 202, status 200 with `next_step: send_recovery_message`, and
401 for both unapproved attempt-token and challenge-only exchanges. All responses
were `no-store`; session/link identities were unchanged by the smoke test. The
unapproved verification attempt expires normally. No WhatsApp approval, session
replacement or customer message was performed. Deployment-window logs show no
startup failure. Android integration and live primary-phone approval remain
unverified; see the [recovery contract](ANDROID_ACCOUNT_RECOVERY.md).

The IPO backend was deployed on 19 October 2026 after applying migration 013 and
runtime grants locally and to Supabase. The first source checkpoint `33b386b`
was deployed by merging only the private `IPO_JOB_TOKEN`; no duplicate deploy
was requested. Follow-up `76e63bb` establishes the NSE public cookie session and
records safe HTTP/schema failures. Render built both exact commits and reached
live; the current deployment finished at 09:11:55 UTC. Public health 200,
unauthenticated API/job-trigger 401 and authenticated job acceptance 202 passed.
Repeated triggers returned one durable half-hour job. Existing user/business/POS
session records were preserved; no real customer messages or transactions were made.

**Recurring collection is unreliable:** the NSE current/upcoming/past APIs returned
HTTP 403 from Render despite the session/header integration. An intervening
scheduled run imported 118 products, but later runs again failed with HTTP 403.
Source failure reached Sentry issue `WHATSAPP-POS-API-2`. This is not reliable
scheduled data collection.

The separate Sentry monitor `10687890` is configured for POST every 1800 seconds,
with the dedicated header credential, empty body and response capture disabled.
Sentry initially forced it disabled and refused activation with "You don't have
enough pay-as-you-go available to create a new seat". The existing health monitor
`10630540` remains active at 60 seconds and has successful scheduled HTTP 200 checks.
No billing plan was changed. Sentry's current documentation limits PAYG to paid
plans: the USD 1 additional monitor price excludes any required base subscription.
The user subsequently enabled the IPO monitor. A brief one-minute verification
interval produced a scheduled POST at 09:17:02 UTC (14:47:02 IST), returning HTTP
202 in 474 ms; the active interval was then restored to 1800 seconds. The health
monitor remains active and unchanged. No additional job was created in the same
half-hour. The existing import exhausted three attempts with zero issues and NSE
HTTP 403 on every feed. Scheduling is verified; permitted hosted source access
remains unresolved.

The user authorised a one-off local collection into the hosted database on
19 October 2026. Run `d430c51a-18d0-4ccf-986e-c37aabbddb50` finished at 10:41:38 UTC
(16:11:38 IST), saving all 118 collected products (82 Mainboard, 36 SME). All three
feeds and ten NSE detail requests succeeded. TONBO/ZETWERK enrichment was unavailable,
so the run honestly reports partial coverage. Verified 55 subscription rows, 24
reservation rows, 14 promoter rows and ten products with category calculations;
published anchor dates remain unavailable. The runtime catalogue handlers and
before/after preservation checks were verified separately. This used the existing
collector, advisory lock, import writer and private hosted worker connection through
a temporary operator entry point, removed after use. The manual run is labelled
`execution=local_manual` with automatic retries suppressed; the Sentry monitor and
deployed application were unchanged. No local recurring process was installed.
See the
[catalogue operating guide](IPO_CATALOGUE.md).

## Service configuration

The IPO picker lifecycle change was deployed on 19 October 2026 at 11:45:44 UTC
(17:15:44 IST) using commit `9f3cfa83aa66d95260107187920d82fdeb823957`.
The existing list URL now
filters by lifecycle without requiring `active=true`: confirmed listing dates take
precedence over exact expected listing dates, and products disappear on the next
Indian calendar day. Unknown listing dates retain the documented closing-date
grace. The exact query returned ten eligible products on hosted storage under the
restricted runtime role; all 118 products, including 104 past listings, remain stored.
Integration checks covered pagination and both legacy active values. Hosted health
200 and unauthenticated catalogue/me 401 passed with no-store; startup and the
deployment-window error log checks passed. No migration, environment, monitor or
data change was needed. No authenticated hosted request was made without a POS
access token; authenticated behavior was verified through disposable HTTP tests.

The root [render.yaml](../render.yaml) defines the development service. It uses
Render's native Go runtime, one Free instance in Singapore and manual deployments.
Keep Root Directory empty: `backend/go.mod` replaces whatsmeow with `../`, so the
backend needs the repository's root module at build time.
[Render monorepo directories](https://render.com/docs/monorepo-support).

| Setting | Value |
|---|---|
| Repository | `https://github.com/morakhiyakavya/whatsmeow` |
| Branch | `main` |
| Name | `whatsapp-pos-dev` |
| Runtime | Go |
| Region | Singapore |
| Plan | Free |
| Root Directory | Empty |
| Build Command | `go -C backend build -tags netgo -trimpath -ldflags "-s -w" -o ipo-api .` |
| Start Command | `./backend/ipo-api` |
| Health Check Path | `/health` |
| Auto-deploy | Off |

Render builds the pushed Git commit, not your uncommitted local files. Confirm the
deployment's commit matches the reviewed checkpoint. The repository's Go module
selects its required toolchain; native Go runtimes are maintained by Render.
[Render Go support](https://render.com/docs/language-support).

In Render's private Environment settings, set only:

| Key | Value |
|---|---|
| `DATABASE_URL` | Restricted API URL from `.env.supabase`, with `sslrootcert=database/supabase-ca.crt` |
| `WHATSAPP_DATABASE_URL` | Worker URL from `.env.supabase`, with the same certificate path |
| `ALLOW_PASSWORD_LOGIN` | `false` |
| `APP_ENV` | `development` for this dev service; production/unset disables automatic signup |
| `SENTRY_DSN` | DSN for `vanitilabs/whatsapp-pos-api`, supplied privately |
| `SENTRY_ENVIRONMENT` | `development` for this service |

Sentry instrumentation is deployed and hosted logs/traces are verified in
`vanitilabs/whatsapp-pos-api`. The runtime uses `RENDER_GIT_COMMIT` as
the Sentry release unless `SENTRY_RELEASE` is explicitly set. Development captures
100% of HTTP traces; production captures 10%. Request data and error values are
excluded from telemetry while stack traces remain available. See the
[Sentry monitoring contract](../backend/README.md#sentry-monitoring).

Preserve `sslmode=verify-full`, the session-pooler host, port `5432` and each
role's separate password. The bundled [certificate](../database/supabase-ca.crt)
is Supabase's public Root 2021 CA, obtained over verified HTTPS from the certificate
URL used by Supabase's dashboard:
[Supabase certificate source](https://supabase-downloads.s3-ap-southeast-1.amazonaws.com/prod/ssl/prod-ca-2021.crt).
Keep passwords in Render's environment settings; never in YAML or Git.
[Render secrets](https://render.com/docs/configure-environment-variables).

Do not copy the local `API_ADDR` or migration/admin credentials into the web
service. Render supplies `PORT`; the API listens on `0.0.0.0:PORT` when `API_ADDR`
is absent. A missing local `.env` is accepted when the environment provides
`DATABASE_URL`; existing malformed files still fail validation.

Create the service via the connected Render integration, or use New → Web Service
with the settings above. For Blueprint deployment, use New → Blueprint and select
this repository; supply the two private URLs when prompted. A tool-created web
service might use Render's default TCP health check; set `/health` in its dashboard
when the tool cannot specify that field.

## Verify the deployed service

The deployed health handler executes a read-only `SELECT 1` with a one-second timeout
through the API's database connection. External `/health` requests
generate database queries without reading customer data or changing records.
A monitor can request it every five minutes to prevent Render's idle shutdown;
this also generates activity for Supabase, but neither provider guarantees
continuous availability on its Free plan. The active
[Sentry monitor](https://vanitilabs.sentry.io/monitors/10630540/) now checks this URL
every five minutes, with a ten-second timeout and response capture disabled.

Open `https://YOUR-SERVICE.onrender.com/health`; expect `{"status":"ok"}`.
An unauthenticated `GET /api/me` must return 401. With password login disabled,
`POST /api/login` must return 403. These checks prove API/database connectivity and
the authentication boundary, not a connected WhatsApp session.

After applying migration 010 and deploying its API, new numbers can sign up through
the existing `/api/auth/whatsapp/start`, `/status` and `/exchange` flow when
`APP_ENV=development`. Request/response bodies are unchanged. Approved new numbers
receive an isolated development business/admin; abandoned attempts create no staff
or business records. Existing accounts retain their memberships and restrictions.
The [master-admin business provisioning TODO](TODO.md) remains future work.

For production/provisioned-only access, provision the initial business and admin with the actual staff phone separately
using `-bootstrap` against `.env.supabase`; provide explicit business/admin names
and a private bootstrap password as described in the [API guide](../backend/README.md).
Do this locally because Render Free services have no interactive shell. Keep
password login disabled; staff then use the
[WhatsApp passwordless flow](POS_AND_CONTACTS_GUIDE.md#mobile-authentication-and-whatsapp-login).

Saved WhatsApp connections restore on startup. During overlapping deployments,
the replacement worker reports `waiting_for_worker` until the previous instance
releases its account/user locks, then rechecks the mapping before loading keys.
Fresh pairing still reports `busy` when another process owns the locks. Keep one
service instance; do not point a local worker at the hosted database while also
expecting Render to own the same account.

Render Free services sleep after 15 minutes without inbound traffic. This stops
the WhatsApp worker, so continuous incoming-message processing requires an
always-running service plan. It is suitable for development and manual API tests.
[Render Free limits](https://render.com/docs/free).

## Database changes and rollback

POS migrations 001–013 are already applied in the current Supabase project
and local database. Do not run migrations in the build command, the
start command or every deploy.
For subsequent changes, follow the
[Supabase table and migration instructions](SUPABASE_SETUP.md#inspect-tables-and-apply-future-migrations).

For application rollback, redeploy a reviewed earlier compatible commit. A Git or
Render rollback does not undo SQL changes. Prefer additive migrations compatible
with both versions during a rollout; destructive changes need a backup and a
separate, reviewed recovery plan.

## Verification status

Environment-only configuration and port validation have unit coverage. Disposable
PostgreSQL integration tests cover the normal pairing lifecycle, lock-protected
replacement takeover, disconnect/shutdown while waiting and existing authentication/POS
behavior. Native Windows and Linux amd64 builds passed. The compiled backend ran
against Supabase using environment-only configuration, hosted port binding and the
bundled CA; health and authentication boundary checks passed, then the temporary
server stopped. The public CA matches the official HTTPS download. Live Render
deployment and public health/authentication checks also passed for the commit
listed above. Live WhatsApp pairing, saved-session restoration and a customer
confirmation send from Render remain unchecked.

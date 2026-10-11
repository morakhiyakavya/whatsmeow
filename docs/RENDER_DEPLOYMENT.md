# Render development API

Deploy the existing Go API and WhatsApp worker as one web service. Supabase stores
both POS records and WhatsApp credentials. The mobile app uses the service's HTTPS
URL; it never receives database credentials. This deployment does not provision a
business/admin or copy local WhatsApp sessions.

## Current deployment

Deployed and verified on 11 October 2026 in `kavya's Workspace`:

- API base URL: [whatsapp-pos-dev](https://whatsapp-pos-dev.onrender.com).
- [Live health endpoint](https://whatsapp-pos-dev.onrender.com/health).
- [Render dashboard](https://dashboard.render.com/web/srv-db5nundckfvc73arsvkg).
- Deployed commit: `b6d5f2c6aa98a8ad826f06876aaec8ada68d283d` on `main`.
- Deployment: `dep-db5og7navr4c739h4t90`; service: `srv-db5nundckfvc73arsvkg`.
- One Free instance in Singapore; automatic deployments disabled.

Render reported the deployment live. Public HTTPS checks passed: `/health` returns
200 with `{"status":"ok"}`, unauthenticated `/api/me` returns 401, and password
login returns 403. Responses carry `Cache-Control: no-store`. The server started
on `0.0.0.0:10000` using the two private Supabase role connections and bundled CA.

This deployment supports two-way transactions through signed quantity: positive
means Buy, negative means Sell, and zero is rejected. Rate remains nonnegative;
the generated total uses signed quantity. Confirmations use Transaction wording
and derive Buy/Sell from quantity. Supabase migration 008 was installed and its
rollback-only schema assertions passed before this deploy. Render built the exact
pushed commit and reported it live; startup succeeded with the migration guard.
Post-deploy health/authentication/no-store checks passed and no error-level logs
were returned for the deployment window. No test staff or real messages were created.

The integration could not set an HTTP health-check path, so the actual service
uses Render's default TCP check. In its dashboard, open Settings → Health Check
Path and set `/health` to include database availability in platform health checks.
The Blueprint already specifies this value for a Blueprint-created service.
Initial business/admin provisioning and live WhatsApp pairing remain separate.

## Service configuration

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

Open `https://YOUR-SERVICE.onrender.com/health`; expect `{"status":"ok"}`.
An unauthenticated `GET /api/me` must return 401. With password login disabled,
`POST /api/login` must return 403. These checks prove API/database connectivity and
the authentication boundary, not a connected WhatsApp session.

Provision the initial business and admin with the actual staff phone separately
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

All nine existing POS migrations are already applied in the current Supabase
project. Do not run them in the build command, the start command or every deploy.
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

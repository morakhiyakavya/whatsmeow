# Azure VM deployment

## Current state — 25 October 2026

Azure runs the **IPO importer** independently, so source requests originate from
the Indian-region VM. Render remains the application/WhatsApp host. Both use the
existing Supabase database; no WhatsApp-worker handover has occurred.

| Setting | Current value |
|---|---|
| VM / resource group | `venweb-api` / `venweb-api_group` |
| Azure region / zone | `indiasouthcentral` / `1` |
| OS / architecture | Ubuntu 24.04.5 LTS / x86-64 |
| Size | `Standard_B2ats_v2`; about 836 MiB usable RAM |
| Allocation | Spot, eviction policy Deallocate |
| Public IP | `172.198.153.142` |
| API base URL | `https://172-198-153-142.sslip.io` |
| Health | `https://172-198-153-142.sslip.io/health` |
| Application service | `venweb-api.service`, enabled at boot |
| HTTPS proxy | Caddy 2.11.7 from its official stable APT repository |
| Source | `e410eb6395777908b30a1b34ba60eb4c1776132f` plus the verified enrichment/source-discovery change recorded in the task ledger |
| Sentry release | `azure-enrichment-6321943f2f4b` |
| Linux binary SHA-256 | `6321943f2f4b6560ed554a132e0014b78455351f210bba3eed9c78a61481a569` |

Clients continue using Render for sign-in, pairing, messaging and application
APIs. Sentry's refresh monitor `10687890` targets Azure's
`/internal/jobs/ipo-refresh` every 1800 seconds. The separate health monitor stays
on Render. Render's `IPO_JOB_TOKEN` is empty, so its old importer is disabled and
cannot claim work from the shared queue. The configuration-only deployment
`dep-dbf0a5navr4c73cc3j80` reused source `c4bc1a3` and reached live at
13:34:02 UTC (19:04:02 IST), preserving its WhatsApp configuration. The repository's
`render.yaml` also keeps the Render token empty for future Blueprint syncs.
See [the Render deployment record](RENDER_DEPLOYMENT.md) and
[catalogue ingestion](IPO_CATALOGUE.md).

## Runtime and secrets

The native binary runs as the unprivileged `venweb` system user. systemd restarts
it after failures, enables it at boot and sends SIGTERM on stop. The service uses
`NoNewPrivileges`, a read-only filesystem sandbox and an inaccessible home tree.
Caddy forwards HTTPS requests to `127.0.0.1:8180`; its admin listener is also local.
The API port is not bound to the public interface.

- `/opt/venweb/ipo-api`: root-owned executable, mode 0755.
- `/opt/venweb/database/supabase-ca.crt`: public Supabase CA, mode 0644.
- `/etc/venweb`: root-owned, group `venweb`, mode 0750.
- `/etc/venweb/api.env`: active API/importer configuration, root:`venweb`, mode 0640.
- `/etc/venweb/api.api-only.env`: original staging configuration for rollback.
- `/etc/venweb/api.full.env`: prepared full configuration with the existing worker
  connection and IPO job token; same ownership/mode, **not active**. Review it
  against the current importer configuration before any future WhatsApp handover.
- `/opt/venweb/ipo-api.previous`: previous API-only binary for rollback.
- `/opt/venweb/ipo-api.before-enrichment` and
  `/etc/venweb/api.before-enrichment.env`: the preceding standalone-importer
  binary/configuration for rollback of source-resolution changes.
- `/etc/systemd/system/venweb-api.service`: installed from
  [the service definition](../deploy/azure-vm/venweb-api.service).
- `/etc/caddy/Caddyfile`: [the proxy template](../deploy/azure-vm/Caddyfile), with
  `api.example.com` replaced by the temporary hostname.

The existing `.env.supabase` supplied only the runtime database URLs, application
mode, password-login setting, Sentry DSN and IPO job token. Migration/bootstrap
credentials and local settings were not copied. Both URLs retain
`sslmode=verify-full`, the existing session pooler and separate roles; their CA
path now resolves to `/opt/venweb/database/supabase-ca.crt`. The app reads its own
environment-file format through `-env`; systemd does not reinterpret secrets.

`APP_ENV=development` and `ALLOW_PASSWORD_LOGIN=false` are preserved from the
existing deployment. `SENTRY_RELEASE` identifies the deployed binary because Azure
does not supply `RENDER_GIT_COMMIT`. No FCM credentials were introduced.
The active file supplies `IPO_DATABASE_URL` with the trusted worker identity and
the existing `IPO_JOB_TOKEN`, but omits `WHATSAPP_DATABASE_URL`. Startup opens a
separate bounded/traced importer pool without initializing WhatsApp storage or
restoring accounts. There are no WhatsApp/delivery/profile-push workers on Azure.
A 1 GiB swapfile was enabled and recorded in `/etc/fstab`; it is a memory-pressure
fallback, not extra physical RAM.

## Checks and operational limits

The Linux amd64 build, backend tests and vet passed using installed Go 1.27.1
with `GOTOOLCHAIN=local` and preserved build caches. The uploaded binary hash
matches the local build. systemd unit verification, Caddy configuration validation,
Let’s Encrypt issuance and public HTTPS checks passed. `/health` returned 200,
unauthenticated `/api/me` and transaction reads returned 401, and disabled password
login returned 403; all responses had `Cache-Control: no-store`.

Service identity, permissions, localhost binding, startup and restart were checked.
The independent importer change passed the full backend unit/API/integration suite
against a new disposable PostgreSQL cluster with migrations 001–015 (22.345 seconds),
plus backend vet and the Linux build. Checks cover importer database precedence,
legacy fallback, startup/cancellation error redaction, pool limits, telemetry,
trigger authentication/deduplication, trusted grants, locking, retry/recovery and
preservation of stored terms. The temporary database was stopped and removed by
the existing runner. No schema, customer transaction or WhatsApp session was
changed for rollout; no customer message was sent. WhatsApp restoration,
authenticated customer flows, load and reboot recovery remain untested on Azure.

The original live Azure import (`fe5d4ef5-ca75-419d-96c2-483035fd5542`) ran from
13:35:47 to 13:36:54 UTC (19:05:47–19:06:54 IST). Its authenticated trigger was
accepted, all three NSE feeds and eight detail requests succeeded, and 115 issues
were persisted without removing the catalogue's existing 122 products. Five
IPO Watch enrichments succeeded, producing 15 fresh reservation rows and 10 fresh
promoter rows. TONBO, MKSONS and ZETWERK enrichment remained unavailable; no fresh
anchor rows were available. The result is **partial**, not a claim of complete
source coverage. The existing retry policy remains in force. See
[the Azure collection trace](https://vanitilabs.sentry.io/explore/traces/trace/2e8966ad02acbfd27272dd61f31c58eb).

The enrichment/source-discovery fix was deployed at 14:41 UTC (20:11 IST).
It was built from an isolated export of `e410eb6` plus only
`backend/ipo_enrichment.go`, `backend/ipo_sources.go` and the new enrichment tests;
the independent pending persistence-batching changes were excluded. The exact
snapshot passed the full disposable PostgreSQL/backend suite (25.663 seconds),
vet and the Linux build. A read-only test on the Azure VM then confirmed all three
verified mappings, each with three reservation and two promoter rows, plus real
published-calendar link discovery (0.97 seconds). Public health and rejection of
unauthenticated refresh requests passed after deployment. Runtime credentials,
the 1800-second Sentry schedule and the Render/WhatsApp split were preserved.

The initial uptime 401 was caused by pointing the monitor to API-only staging,
where the job token/importer were intentionally absent. Azure now accepts the
existing credential. For verification the existing refresh monitor was temporarily
set to 60 seconds, then restored to 1800 seconds. Its private Authorization header
was synchronized with Azure's existing job credential; its URL, POST method,
timeout, response-capture setting and thresholds were preserved. Sentry subsequently
reported successful scheduled HTTP 202 checks at 13:38:02, 13:39:02 and 13:40:02 UTC
(19:08:02, 19:09:02 and 19:10:02 IST), confirming the monitor credential works.
The direct authenticated trigger and completed collection were verified separately.
No new monitor or scheduling service was created.

The post-fix full refresh `3bc2e606-3139-4646-92ac-4883c3fc7d13` ran from
14:42:22 to 14:43:28 UTC (20:12:22–20:13:28 IST) and finished **succeeded** on its
first attempt, saving 115 IPOs. All three NSE feeds, eight detail requests and all
eight targeted enrichments returned `ok`, including TONBO, MKSONS and ZETWERK.
Read-only database checks confirmed three fresh reservation rows and two fresh
promoter rows for each of those three issuers. This verifies the supported refresh
scope; it does not establish complete BSE coverage or guarantee every optional
field will be published in future source responses.

Before a production cutover:

- Replace the Spot VM allocation with a regular VM if continuous availability is
  required. Azure can evict Spot VMs with 30 seconds' notice; systemd cannot restart
  a deallocated VM. `maxPrice=-1` does not prevent capacity eviction. There is no
  guaranteed reallocation. [Azure Spot behaviour](https://learn.microsoft.com/en-us/azure/virtual-machines/spot-vms).
- Check memory under the actual linked-account load. This VM is smaller than the
  initially recommended 2 GiB; no capacity/load test has been performed.
- The backend currently throttles authentication by `RemoteAddr`. Behind Caddy,
  users share the proxy's ten-attempt/minute bucket. Add and test explicitly trusted
  proxy handling before multi-user cutover; never trust arbitrary forwarded headers.
- Confirm the Azure public IP is static, SSH is restricted in the NSG, and only
  necessary web ports are public. SSH/HTTP/HTTPS reachability was checked; Azure
  network rules and IP allocation were not inspected through the control plane.
- Replace the temporary third-party `sslip.io` hostname with an owned domain or
  Azure DNS label when available. Its address depends on this IP remaining stable.

## Future WhatsApp handover when authorised

1. Suspend `whatsapp-pos-dev` in its
   [Render dashboard](https://dashboard.render.com/web/srv-db5nundckfvc73arsvkg).
   The connected Render tools can inspect the service but cannot suspend it.
2. Confirm suspension and release of the existing workers. Do not unlink accounts,
   clear session tables, bootstrap users or reapply migrations.
3. On the VM, activate the prepared configuration and restart:

   ```sh
   sudo cp --preserve=mode,ownership /etc/venweb/api.env /etc/venweb/api.staging.env
   sudo install -o root -g venweb -m 0640 /etc/venweb/api.full.env /etc/venweb/api.env
   sudo systemctl restart venweb-api
   sudo journalctl -u venweb-api --since '5 minutes ago' --no-pager
   ```

4. Verify health, existing worker lock ownership, WhatsApp restoration and Sentry
   logs without sending a synthetic customer message. Health alone does not prove
   a connected WhatsApp session. Existing database locks coordinate ownership.
5. Move health monitor `10630540` to Azure's `/health`. The IPO refresh monitor
   `10687890` already targets Azure; preserve its URL, 1800-second interval and
   private Authorization header. Preserve capture settings and thresholds, and
   verify scheduled results. No new monitor is required.
6. Switch client API base URLs and verify a real authorised sign-in. Keep Render
   suspended for rollback; do not delete its configuration during the handover.

To roll back a future WhatsApp handover, stop Azure's service before resuming
Render, then restore Azure's IPO-only configuration and the health/client URLs.
Keep IPO collection on Azure unless moving it is intentional. To roll back the
importer rollout itself, stop Azure's service and restore `ipo-api.previous` plus
`api.api-only.env`; separately restore the Render token/configuration and refresh
monitor URL if returning imports to Render. That restores the previous geographic
source-access limitation. Never leave two intended import owners enabled.

## Subsequent releases

The [backend CI/CD workflow](CI_CD.md) can deploy the tested `main` revision to
both hosts while preserving Render's application/WhatsApp role and Azure's
IPO-only role. Its Azure helper verifies the binary checksum and rolls back on
startup/local health failure. Activation requires GitHub Actions secrets and
publication of the workflow; it does not restart an evicted Spot VM.

Build from the repository root on the development machine. Keep the root module
available: `backend/go.mod` replaces `go.mau.fi/whatsmeow` with `../`.

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go -C backend build -tags netgo -trimpath -ldflags '-s -w' -o ipo-api .
# In this shell, remove GOOS/GOARCH/CGO_ENABLED before running native Go tests.
```

Transfer the binary over SSH, compare SHA-256, preserve a rollback copy, then stop
the service, install the new binary as root with mode 0755 and restart. Keep secret
files outside Git. Validate Caddy before reloading changed proxy configuration.
Do not run database migrations in service startup or build commands.

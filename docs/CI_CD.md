# Backend CI/CD

The [backend workflow](../.github/workflows/backend-deploy.yml) tests pull requests
to `main`. A push to `main`, or a manual run on `main`, tests the same revision and
then deploys it independently to both existing hosts:

- Render `whatsapp-pos-dev`: application API and WhatsApp workers, with
  `IPO_JOB_TOKEN` empty.
- Azure `venweb-api`: IPO importer, with `IPO_DATABASE_URL` and `IPO_JOB_TOKEN`
  present and `WHATSAPP_DATABASE_URL` absent.

Deployment never copies production database credentials or changes those roles.
Keep native Render auto-deploy disabled: this workflow owns the deploy trigger.
Changing a file locally does not deploy it; it must be committed and pushed.

## Checks and rollout

CI checks import formatting and static analysis across all three modules, then
applies every numbered migration to a disposable PostgreSQL 17 service and
runs the existing schema assertions, root library tests/vet, backend tests/vet
including database integration, and viewer/session tests/vet. It also tests the
deployment helpers against simulated endpoints/services. It never connects to
Supabase or sends a customer WhatsApp message. Live-source tests remain opt-in.

After the checks pass, CI builds a Linux amd64 binary with a SHA-256 manifest.
Render builds the exact tested commit using its existing build command. Its job
polls the returned deployment ID until that commit is live, then checks `/health`.
Azure receives the workflow artifact over SSH using a pinned host key. The
root-owned helper verifies the checksum before stopping the service, keeps a
rollback binary/release, restarts, and restores the previous release if startup
or local database health fails. The job also checks public HTTPS health.

The two deploy jobs run independently: Azure being stopped does not prevent
Render's deployment. The overall workflow fails if either deployment fails;
inspect and rerun only the failed job when its cause is fixed. Deployment runs
are serialized and are not automatically cancelled during binary replacement.
Health verifies database reachability; it does not prove WhatsApp connectivity,
customer delivery or completion of an IPO import. Sentry retains its existing
30-minute IPO trigger; deployment does not enqueue extra refreshes.

## One-time setup

Use repository [Actions secrets](https://github.com/morakhiyakavya/whatsmeow/settings/secrets/actions):

| Secret | Value |
|---|---|
| `RENDER_API_KEY` | A Render API key authorized for the confirmed workspace. |
| `AZURE_DEPLOY_KEY` | The dedicated `venweb-deploy` SSH private key; keep it out of Git. |
| `AZURE_KNOWN_HOSTS` | The VM's previously verified SSH host-key entry for `172.198.153.142`. |

On Azure, install [the helper](../deploy/azure-vm/deploy-venweb.sh) as
`/usr/local/sbin/deploy-venweb`, owned by root with mode 0755. Create a locked
`venweb-deploy` user; install only its dedicated public key in `authorized_keys`
with the `restrict` option. Give that user passwordless sudo permission only for
this helper, using a root-owned mode-0440 file in `/etc/sudoers.d/` validated with
`visudo -cf`. Do not grant it general sudo or give CI the administrator's PEM key.

The service needs `EnvironmentFile=-/etc/venweb/release.env`, as recorded in
[the service template](../deploy/azure-vm/venweb-api.service). A drop-in can add it
to an existing installation without replacing the unit. Reload systemd after
installing the drop-in; no service restart is needed until the next deployment.
The helper writes only the release file and binary; `/etc/venweb/api.env` stays
private and unchanged. A successful deployment records the Git SHA in
`/opt/venweb/REVISION` and sets the same Sentry release through systemd.

Repository administrators should restrict who can push to `main`: those changes
can deploy executable code that reads the application's runtime secrets. PR jobs
do not receive deployment secrets, and the workflow token has read-only repository
permissions. No database migration is run on either production host by CI/CD;
apply additive schema changes separately before deploying code that requires them.

## Recovery and limitations

For Render, inspect the deployment ID in the Actions log and the Render dashboard
before retrying an ambiguous trigger failure. A failed build keeps the old release;
an accepted trigger alone is not considered a successful deployment.

For Azure, check `/opt/venweb/REVISION`, `systemctl status venweb-api` and the
Sentry release. SSH/checksum failures leave the running binary untouched. A local
health failure rolls back the binary/release; a public HTTPS failure after local
success requires inspecting Caddy/networking and does not automatically undo the
healthy application binary. To restore the retained compatible binary manually,
stop the service, restore `ipo-api.rollback` and the matching release file, then
start and verify health. Application rollback never reverses database changes.
Changes to the root-owned helper or systemd unit require an administrator to
install them separately; routine CI deployments replace only the application.

The current VM is Spot: CI/CD cannot deploy while it is deallocated, and does not
start or provision VMs. An external restart watchdog is a separate addition.
Confirm that the public IP is static before relying on the pinned IP/temporary
hostname across eviction and reallocation. Full deployment activation requires
the workflow to be pushed and all three secrets to be configured; a local file
alone does not enable GitHub Actions.

## Preparation verified on 25 October 2026

All three Actions secrets are configured; the Render key was validated against
the existing service and confirmed workspace. The dedicated Azure user/key,
restricted sudo helper and release drop-in are installed. SSH authentication,
sudo scope, systemd validation and rejection of an invalid deployment passed
without replacing the current application binary or private runtime config.

Root tests/vet, migrations 001–015/schema assertions, the full backend integration
suite (30.310 seconds), viewer/session tests (6.935 seconds), both module vet
checks, a Linux build and actionlint 1.7.12 passed. Simulated deployment checks
covered a healthy rollout, checksum rejection, start/health failure and signal
rollback, absent previous release configuration, Render failure/commit mismatch
and bounded polling. Production health is unchanged. The Windows fixture runner
stalled after starting PostgreSQL; completed the same checks directly against its
verified fresh `schema_test` cluster and stopped it afterwards. Automatic approval
review blocked removal of that stopped temporary fixture; it was retained.

The user subsequently authorized publication and activation. The first
[workflow run](https://github.com/morakhiyakavya/whatsmeow/actions/runs/40208806200)
passed its checks and both deployments at `6f7cadb93cb93a3ada57f1fc630c08059e46a248`.
Render deployment `dep-dbf2bgfb9u1s73bchfo0` became live at 15:53:27 UTC
(21:23:27 IST); Azure's revision file, Sentry release and public health confirmed
the same SHA. Azure still omits WhatsApp credentials and keeps its IPO token.
This shipped the batching and enrichment fixes to both hosts.

The separate existing Go workflow exposed earlier import formatting, error-casing,
redundant event-assertion and deprecated Google credential-loader warnings. The
follow-up corrects those and adds import/static-analysis checks to the deployment
gate. The typed Google loader retains the existing service-account/workload
identity allowlist; synthetic credential-construction tests cover both supported
types without requesting a token or contacting Google. No dependency or schema
change is required. Subsequent runs publish the tested SHA to both hosts; inspect
the latest Actions run, Render deployment and Azure revision for the current SHA.

# Git task checkpoints

Commit threshold: **3 completed tasks**. Follow the policy in `../AGENTS.md`.

## Pending

None.

## Checkpoints

### Batch 1 — Project workflow and credential exclusions

1. Add the automatic Git checkpoint policy and persistent task ledger.
   - Verification: reviewed both documents and their diff for the agreed three-task threshold and scoped staging.
   - Paths: `AGENTS.md`, `docs/TASK_CHECKPOINT.md`.

2. Add a ponytail startup guide and make it part of the project instructions.
   - Verification: reviewed document content/diffs and checked all local Markdown links resolve; confirmed the three-task threshold is unchanged.
   - Paths: `AGENTS.md`, `START.md`, `docs/TASK_CHECKPOINT.md`.

3. Checkpoint the existing root Git exclusions for credentials, API executables and logs.
   - Verification: reviewed `.gitignore`; `git check-ignore` confirms root environment files, API executables and logs are excluded; no credentials, session databases or executables are tracked.
   - Paths: `.gitignore`, `docs/TASK_CHECKPOINT.md`.

Application work predating this policy remains outside the count until reviewed and recorded in a later batch. Smaller batches below are explicitly authorized by the user's request for multiple reversible commits.

### Batch 2 — PostgreSQL ledger foundation

1. Review and checkpoint the existing application schema, API migration, schema tests and database design.
   - Verification: reviewed SQL, design and test runner; `database/Test-Schema.ps1 -WithApiTests -WithSessionTests` passed all schema, restricted-role API and synthetic session persistence assertions; staged diff passed `git diff --cached --check`.
   - Paths: `database/001_initial.sql`, `database/002_api.sql`, `database/Test-Schema.ps1`, `database/schema_test.sql`, `docs/POS_DATABASE_DESIGN.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 3 — PostgreSQL private-text viewer

1. Review and checkpoint the existing QR login, explicit session selection, contact resolution and bounded private-text viewer.
   - Verification: reviewed implementation, fixtures and documentation; root/viewer `go test ./...`, viewer `go vet ./...` and isolated PostgreSQL session persistence checks passed; staged diff passed `git diff --cached --check`; session data and local environment configuration are ignored.
   - Paths: `examples/login/.env.example.ps1`, `examples/login/.gitignore`, `examples/login/README.md`, `examples/login/contacts.go`, `examples/login/contacts_test.go`, `examples/login/go.mod`, `examples/login/go.sum`, `examples/login/main.go`, `examples/login/main_test.go`, `examples/login/session.go`, `examples/login/session_test.go`, `examples/login/viewer.go`, `examples/login/viewer_test.go`, `docs/TASK_CHECKPOINT.md`.

### Batch 4 — Authenticated manual ledger and local operation

1. Review and checkpoint the existing authenticated customer/staff/IPO/transaction API and integration fixtures.
   - Verification: reviewed routes, authentication, validation, business scoping, decimal handling, concurrency and immutable history; backend `go test ./...`, `go vet ./...` and restricted-role PostgreSQL integration checks passed.
   - Paths: `backend/auth.go`, `backend/config.go`, `backend/go.mod`, `backend/go.sum`, `backend/integration_test.go`, `backend/main.go`, `backend/resources.go`, `backend/transactions.go`, `docs/TASK_CHECKPOINT.md`.

2. Review and checkpoint local setup/start/smoke scripts, example configuration, API requests and project documentation.
   - Verification: reviewed configuration, script error handling, credential exclusions and documentation/diffs; parsed all PowerShell scripts; reviewed HTTP examples against implemented routes; setup refuses existing resources. Live smoke and new local database provisioning were not rerun.
   - Paths: `.env.example`, `README.md`, `backend/README.md`, `backend/requests.http`, `docs/PROJECT_VISION.md`, `scripts/Setup-LocalDatabase.ps1`, `scripts/Smoke-Api.ps1`, `scripts/Start-Api.ps1`, `scripts/Start-WhatsAppViewer.ps1`, `docs/TASK_CHECKPOINT.md`.

### Batch 5 — Authenticated WhatsApp account registry

1. Add authenticated, business-owned WhatsApp account registration/list/detail APIs, indexed pagination, restricted registration grants and the next delivery sequence.
   - Verification: backend `go test ./...` and `go vet ./...` passed; isolated schema/API integration tests passed, including ownership, pagination, forged binding rejection and stable IDs through simulated pairing/unlinking; applied migration 003 and column grants locally and verified binding INSERT/UPDATE remains forbidden; live local API smoke passed and its temporary server was stopped; PowerShell syntax, local Markdown links and content/diffs reviewed.
   - Paths: `backend/whatsapp_accounts.go`, `backend/whatsapp_accounts_test.go`, `backend/main.go`, `backend/integration_test.go`, `backend/README.md`, `backend/requests.http`, `database/003_whatsapp_accounts.sql`, `database/Test-Schema.ps1`, `scripts/Setup-LocalDatabase.ps1`, `scripts/Smoke-Api.ps1`, `docs/PROJECT_VISION.md`, `docs/POS_DATABASE_DESIGN.md`, `docs/TASK_CHECKPOINT.md`.
   - Limits: live QR/phone pairing, reconnection and session adoption remain next; registration POST has no replay key. The additive local database index/grant need the documented SQL rollback if this increment is reverted.

### Batch 6 — Authenticated WhatsApp pairing and recovery

1. Implement owner-authorized QR/phone pairing, PNG rendering, verified account binding, saved-session recovery and revocation handling.
   - Verification: backend `go test ./...` and `go vet ./...` passed; isolated PostgreSQL API/session checks passed with fake protocol clients, including owner-only codes, QR PNGs, retry reuse, canonical UUIDs, cross-process locks, phone codes, restart recovery, revocation, missing credentials and disabled membership. Local API startup/storage initialization and read-only smoke passed. Live phone pairing and race instrumentation were not run (CGO is disabled).
   - Paths: `backend/config.go`, `backend/main.go`, `backend/go.mod`, `backend/go.sum`, `backend/whatsapp_service.go`, `backend/whatsapp_pairing.go`, `backend/whatsapp_service_test.go`, `backend/integration_test.go`, `backend/requests.http`, `docs/TASK_CHECKPOINT.md`.
   - Limits: 32 active accounts per worker, 64 connection states, no passkey pairing support; a crash between library credential persistence and account binding requires fresh pairing. No arbitrary session adoption or customer messages.

### Batch 7 — Local pairing helper and lifecycle documentation

1. Add a local QR/phone pairing helper with offline checks and document the implemented lifecycle, permissions, setup and limits.
   - Verification: `scripts/Test-PairWhatsApp.ps1` passed offline QR, phone, saved-session, failure and cleanup assertions without reading real configuration or calling HTTP; PowerShell parser checks passed; reviewed documentation/diffs and local links against implemented routes. No live WhatsApp pairing was attempted; the temporary local smoke server was stopped.
   - Paths: `scripts/Pair-WhatsApp.ps1`, `scripts/Test-PairWhatsApp.ps1`, `backend/README.md`, `docs/PROJECT_VISION.md`, `docs/POS_DATABASE_DESIGN.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 8 — Live phone pairing verification

1. Verify live phone-code pairing and automatic startup restoration, and record the completed milestone.
   - Verification: paired the user's phone through the authenticated helper; confirmed the stable account ID, verified phone binding and absence of a pairing code after connection. Restarted the backend and confirmed `connected` using read-only account/status requests without a pair/connect request. Reviewed documentation content/diff; no customer messages were sent; the temporary test server was stopped. Live QR pairing, device revocation and race instrumentation remain unchecked.
   - Paths: `backend/README.md`, `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 9 — Reuse policy and mobile POS delivery priority

1. Record the user's explicit requirement to avoid duplicate code and reuse existing implementations and database constraints.
   - Verification: reviewed instruction content and diff; the existing reuse rule was strengthened without adding a second rule or changing the commit policy. Documentation only; no runtime tests required.
   - Paths: `AGENTS.md`, `docs/TASK_CHECKPOINT.md`.

2. Define the next archive increment around existing schema, viewer logic and API helpers, with three implementation commits and explicit acceptance checks.
   - Verification: reviewed the current schema constraints/index, viewer filtering and identity helpers, account-worker integration point and API routes; reviewed the plan content/diff for reuse, business isolation and capture limits. Documentation only; no implementation or live message capture performed.
   - Paths: `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.
   - Superseded by task 3 after the user clarified that mobile POS confirmation delivery takes priority over an archive/browser.

3. Reprioritise the next increment to authenticated mobile POS confirmation delivery and define sale detection as the trigger plus five preceding private texts.
   - Verification: reviewed existing login/session/business checks and the transactional, idempotent sale writer; updated and reviewed the vision/diff to reuse those implementations, distinguish sale persistence from message delivery, and defer standalone archive/viewer work. Documentation only; no API changes or customer messages sent. Production authentication hardening and delivery remain unimplemented.
   - Paths: `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 10 — Shared contact names and provenance

1. Add business-shared mobile contact names with authenticated staff provenance, number lookup and withdrawal; preserve chosen customer names.
   - Verification: backend `go test ./...`, `go vet ./...` and disposable PostgreSQL schema/API integration checks passed, including multiple contributors, replay stability, forged-source rejection, cross-business denial, own-source withdrawal and unknown-number lookup. Applied additive migration 004 and scoped grants locally without importing contacts; script syntax and documentation/diffs reviewed. Mobile permission handling is not implemented; names remain suggestions and only the current value per contributor is stored.
   - Paths: `database/004_contact_names.sql`, `database/Test-Schema.ps1`, `scripts/Setup-LocalDatabase.ps1`, `backend/contacts.go`, `backend/contacts_test.go`, `backend/resources.go`, `backend/integration_test.go`, `backend/main.go`, `backend/README.md`, `backend/requests.http`, `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 11 — Atomic mobile POS confirmation intents

1. Extend the authenticated sale POST with an optional owned WhatsApp sender, atomic composed confirmation intent and business-scoped status reads.
   - Verification: backend `go test ./...`, `go vet ./...` and isolated schema/API checks passed, including saved totals, concurrent retries, forged body/sender denial, atomic missing-recipient/unpaired rejection, separate businesses and denied runtime delivery updates. Updated the history guard fixture for the new foreign key and kept account fixtures isolated; reviewed migration/setup/contracts/diffs. No customer messages sent; this checkpoint queues only, with delivery next.
   - Paths: `database/005_transaction_confirmations.sql`, `database/schema_test.sql`, `database/Test-Schema.ps1`, `scripts/Setup-LocalDatabase.ps1`, `backend/confirmations.go`, `backend/confirmations_test.go`, `backend/transactions.go`, `backend/integration_test.go`, `backend/main.go`, `backend/README.md`, `backend/requests.http`, `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 12 — POS confirmation delivery and recovery

1. Deliver queued sale confirmations through the existing locked account worker, preserving recorded message IDs and conservative uncertain-send handling.
   - Verification: backend tests/vet and isolated PostgreSQL API/session checks passed, including actual worker ticks, pending restore, recorded/wire IDs, POST replay, cancelled/edited sales, changed recipients/senders, disabled staff and interrupted-send recovery. Applied migration 005 and scoped local grants; read-only API/contact smoke and saved WhatsApp auto-restore passed; no live receipts, contacts or sales were created. Temporary server stopped; module tidy reused the existing protobuf dependency. Reviewed source, examples, script syntax and documentation/diffs. Live sending, race instrumentation, production HTTPS and mobile permission UI remain unchecked/unimplemented; uncertain attempts require review and are not automatically resent.
   - Paths: `backend/confirmation_delivery.go`, `backend/confirmation_delivery_test.go`, `backend/whatsapp_service.go`, `backend/whatsapp_service_test.go`, `backend/integration_test.go`, `backend/go.mod`, `backend/README.md`, `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 13 — POS, contacts and authentication guide

1. Document the complete manual POS and shared-contact workflow, including upload,
   contact changes/withdrawal, provenance, customer selection, sale retries and
   confirmation status; distinguish implemented APIs from remaining mobile work.
   - Verification: reviewed the guide against authentication, contacts, customer/catalogue, transaction, confirmation and delivery code plus schema constraints; resolved local file/section links and parsed all nine JSON request/response examples; reviewed documentation content/diffs and whitespace. Documentation only; no live contacts, sales or messages created, and runtime tests were not rerun.
   - Paths: `docs/POS_AND_CONTACTS_GUIDE.md`, `backend/README.md`, `docs/TASK_CHECKPOINT.md`.

2. Document the requested WhatsApp-number mobile authentication direction and distinguish it from current password login and linked-device pairing.
   - Verification: reviewed existing session issuance/expiry/logout, owner-only pairing, verified-number binding and unlink/reconnect handling; reviewed the guide/vision updates for accurate existing versus proposed behaviour, retained users, revocation, login proof and queued receipt implications; local guide links and nine JSON examples passed checks; documentation diffs/whitespace reviewed. No authentication code changed, runtime tests rerun or real sessions revoked.
   - Paths: `docs/POS_AND_CONTACTS_GUIDE.md`, `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.

### Batch 14 — Passwordless login and shared-link POS sessions

1. Implement WhatsApp-approved passwordless login and independent POS-device sessions sharing one backend WhatsApp link, including original-device two-minute one-use tokens, rotating renewal, session management and unlink revocation without deleting users.
   - Verification: backend `go test ./...` and `go vet ./...` passed; isolated `database/Test-Schema.ps1 -WithApiTests -WithSessionTests` passed. Coverage includes provisioned staff only, private pairing proof, wrong-number/expired-proof rejection, one-use concurrent redemption, token expiry/rotation, renewal across API restart, device/all-device logout, retained users, membership isolation and runtime grant restrictions. Deterministic concurrent renewal/token issuance/redemption during unlink passed without lock inversion; existing POS confirmation and PostgreSQL session-persistence checks passed. Migration 006 and scoped grants were applied locally; existing saved WhatsApp restoration passed a read-only smoke check and the temporary server was stopped. Source/diffs, PowerShell syntax, guide links and fourteen JSON examples were reviewed; no real customer messages, sales, contacts or new pairing were created.
   - Paths: `.env.example`, `backend/auth.go`, `backend/config.go`, `backend/confirmation_delivery_test.go`, `backend/integration_test.go`, `backend/main.go`, `backend/passwordless.go`, `backend/passwordless_test.go`, `backend/requests.http`, `backend/resources.go`, `backend/whatsapp_pairing.go`, `backend/whatsapp_service.go`, `backend/whatsapp_service_test.go`, `database/006_passwordless_sessions.sql`, `database/Test-Schema.ps1`, `scripts/Setup-LocalDatabase.ps1`, `docs/TASK_CHECKPOINT.md`.
   - Limits: mobile login/token display/QR UI and live passwordless sign-in remain unimplemented or unchecked; race instrumentation was not run (CGO disabled). The existing local admin phone is a development placeholder and needs an explicit correction before live passwordless sign-in. Offline/cross-process disconnect revokes locally but cannot guarantee remote device removal; remove the old linked device in WhatsApp before fresh pairing. Migration 006 is additive and was applied locally; reverting code alone does not undo database changes. Password login is disabled by default, with an explicit local bootstrap opt-in.

### Batch 15 — WhatsApp number registration checks and persistence

Explicitly checkpointed below the three-task threshold at the user's request.

1. Expose an authenticated, owner-only WhatsApp number-registration check through the existing account worker.
   - Verification: backend `go test ./...` and `go vet ./...` passed; isolated `database/Test-Schema.ps1 -WithApiTests -WithSessionTests` passed. Checks cover authentication, owner/admin and cross-business isolation, UUID case, invalid phones/forged fields, unpaired/disconnected accounts, registered/unregistered results, missing/mismatched/duplicate results, upstream error redaction, timeout and worker recovery after cancellation. Reviewed source, documentation and HTTP example against the route, existing phone validation and worker lifecycle; diff whitespace check passed. No live number lookup or customer message performed; no migration or dependency added.
   - Paths: `backend/whatsapp_numbers.go`, `backend/whatsapp_numbers_test.go`, `backend/whatsapp_service.go`, `backend/whatsapp_service_test.go`, `backend/main.go`, `backend/README.md`, `backend/requests.http`, `docs/TASK_CHECKPOINT.md`.
   - Limits: one number per request; checks share the account worker and have an eight-second budget including waiting. No dedicated lookup rate limiter, bulk scan or cache; live lookup and race instrumentation remain unchecked. Registration does not prove number ownership.

2. Persist successful WhatsApp number-registration results once per business/number and expose them through contact lookup.
   - Verification: backend tests/vet and isolated `database/Test-Schema.ps1 -WithApiTests -WithSessionTests` passed, including true/false updates with saved timestamps/account provenance, unknown numbers, preservation after upstream failure/cancellation, storage failure, cross-business read isolation, denied runtime writes and schema phone/null/duplicate/cross-business constraints. Applied additive migration 007 to the local database and its SELECT grant; verified the boolean column and that the runtime role cannot INSERT/UPDATE. Reviewed source, migration, table explanation, setup/API contracts and diffs; PowerShell parser and whitespace checks passed. No live number lookup or customer message performed.
   - Paths: `database/007_whatsapp_number_checks.sql`, `database/schema_test.sql`, `database/Test-Schema.ps1`, `scripts/Setup-LocalDatabase.ps1`, `backend/main.go`, `backend/whatsapp_numbers.go`, `backend/whatsapp_numbers_test.go`, `backend/whatsapp_service.go`, `backend/whatsapp_service_test.go`, `backend/contacts.go`, `backend/contacts_test.go`, `backend/README.md`, `docs/POS_DATABASE_DESIGN.md`, `docs/PROJECT_VISION.md`, `docs/TASK_CHECKPOINT.md`.
   - Limits: stores the latest successful observation, not a history; absent rows mean unchecked. Existing numbers were not backfilled. Live lookup and race instrumentation remain unchecked; results can become stale. Migration 007 was applied locally and needs a deliberate database rollback if reverted; no backend restart was performed.

### Batch 16 — Upstream synchronization

1. Merge eight upstream commits through c386243 into the local fork, preserving the 15 application commits.
   - Verification: GitHub metadata confirmed tulir/whatsmeow as the fork parent; fetched upstream/main and reviewed the conflict-free merge, protocol/schema changes and affected callers. Root, backend and viewer Go tests passed; backend vet passed. Disposable hosted-setup/schema/API/session checks passed on the merged workspace, including the preserved Supabase fixtures. Staged whitespace checks passed. Upstream changes were taken as published; no generated protocol files were edited.
   - Paths: appstate.go, appstate/keys.go, internals.go, proto/waAICommon/WAWebProtobufsAICommon.pb.go, proto/waAICommon/WAWebProtobufsAICommon.proto, proto/waDeviceCapabilities/WAWebProtobufsDeviceCapabilities.pb.go, proto/waDeviceCapabilities/WAWebProtobufsDeviceCapabilities.proto, proto/waE2E/WAWebProtobufsE2E.pb.go, proto/waE2E/WAWebProtobufsE2E.proto, proto/waHistorySync/WAWebProtobufsHistorySync.pb.go, proto/waHistorySync/WAWebProtobufsHistorySync.proto, proto/waSyncAction/WAWebProtobufSyncAction.pb.go, proto/waSyncAction/WAWebProtobufSyncAction.proto, proto/waWeb/WAWebProtobufsWeb.pb.go, proto/waWeb/WAWebProtobufsWeb.proto, proto/waWebLabyrinthWaWasm/WAWebLabyrinthWaWasm.pb.go, proto/waWebLabyrinthWaWasm/WAWebLabyrinthWaWasm.proto, receipt.go, socket/framesocket.go, store/clientpayload.go, types/user.go, user.go, docs/TASK_CHECKPOINT.md.
   - Limits: live WhatsApp and race instrumentation were not run. Uncommitted Supabase setup work remains separate; its pending ledger entry is preserved outside this merge commit.
   - Checkpoint: the requested Git synchronization requires this merge commit; unrelated pending work is not included.

### Batch 17 — Supabase storage and Render deployment preparation

1. Prepare fresh Supabase PostgreSQL setup with separate migration/worker and runtime roles, reusing all seven migrations and one shared runtime-grants file across local setup, hosted setup and API fixtures.
   - Verification: `database/Test-Schema.ps1 -WithHostedSetupTests -WithApiTests -WithSessionTests` passed, including non-superuser hosted installation, rejection of existing application schemas/roles, schema behavior assertions, runtime permission restrictions and existing POS/authentication/WhatsApp storage checks; backend `go vet ./...`, PowerShell parser checks, guide file links and diff review passed. Reviewed the setup guide against official Supabase connection/custom-role/TLS documentation and Render secret-file documentation. No local application database, credentials or live Supabase resources were changed.
   - Paths: `database/Setup-Supabase.psql`, `database/api_grants.sql`, `database/Test-Schema.ps1`, `scripts/Setup-LocalDatabase.ps1`, `backend/integration_test.go`, `backend/README.md`, `docs/SUPABASE_SETUP.md`, `docs/TASK_CHECKPOINT.md`.
   - Limits: actual Supabase connection metadata and private password entry remain necessary; remote TLS, project permissions, migrations and WhatsApp restoration are unchecked. Migrations commit individually and a failure requires review of the installed subset. Keep application/session schemas out of Supabase's exposed Data API; Render overlapping-deploy worker recovery remains separate work.

2. Install and verify the development Supabase storage with private, separate runtime and migration/worker credentials.
   - Verification: connected through the Singapore session pooler using certificate and hostname verification; confirmed the project was empty and PostgreSQL 17.11 before applying all seven migrations. Both custom roles authenticated; runtime INSERT/SELECT probe passed and rolled back without leaving data. Verified restricted API permissions and denied schema access for Supabase Data API roles. Backend startup created 17 WhatsApp credential tables at library schema version 16; live hosted-database health (200), unauthenticated API rejection (401) and disabled password-login rejection (403) passed. Verified zero provisioned users/businesses/devices, Git exclusions and protected credential-file ACL; stopped the temporary server and reviewed documentation content/diff.
   - Paths: `docs/SUPABASE_SETUP.md`, `docs/TASK_CHECKPOINT.md`. External changes: application schemas and roles in Supabase project `tqfwoudoxundtrhogmwa`. Private local `.env.supabase` and `.env.supabase-ca.crt` are ignored and excluded from commits.
   - Limits: Render deployment, initial business/admin provisioning, live WhatsApp pairing and saved-session restoration remain unchecked; migrations and external database changes require deliberate database rollback if reverted. Rotate the Supabase administrator password shared in chat; generated application role passwords are separate. Existing local configuration and application data were preserved.

3. Prepare the Supabase-backed Render API with environment-only configuration, validated hosted port binding, lock-safe WhatsApp worker takeover and deployment/migration instructions.
   - Verification: backend unit tests and vet passed; disposable hosted-setup/schema/API/session tests passed, followed by API integration coverage for disconnect while waiting. Checks cover replacement takeover, cancellation, waiting-state reuse, no client before locks, preserved fresh-pairing busy behavior and existing POS/passwordless workflows. Native Windows and Linux amd64 deployment builds passed. The compiled backend ran against Supabase with no environment file, the bundled verified public CA and hosted port binding; health 200, unauthenticated API 401 and disabled password login 403 passed, then the temporary server stopped. The bundled CA matches the official HTTPS download; actual table/catalogue queries, local documentation links and source/configuration diffs were reviewed.
   - Paths: `backend/config.go`, `backend/config_test.go`, `backend/whatsapp_service.go`, `backend/whatsapp_service_test.go`, `backend/README.md`, `database/supabase-ca.crt`, `render.yaml`, `docs/RENDER_DEPLOYMENT.md`, `docs/SUPABASE_SETUP.md`, `docs/TASK_CHECKPOINT.md`.
   - Limits: live Render deployment and WhatsApp takeover remain unchecked; publishing the local commits requires the user's explicit push authorization. Render Free sleeps when idle and does not provide continuous WhatsApp processing; no initial business/admin was provisioned. API/worker passwords remain private and only the public CA is tracked; database rollback is separate from Git rollback.

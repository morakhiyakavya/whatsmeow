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

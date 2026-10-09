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

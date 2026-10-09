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

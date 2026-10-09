# Project instructions

- In Python projects, look for a virtual environment first and run Python code through that environment.
- In React projects, use nvm to select the appropriate Node.js/npm version when needed.
- Prefer code quality over quantity. Consider time complexity, space complexity, and maintainability before coding.
- Write responses in English C2.

## Ponytail workflow

- At the start of each coding session, read and follow [START.md](START.md).
- Use the installed **ponytail:ponytail** skill at level **full** for coding tasks; locate and read its `SKILL.md` through the available-skills catalogue. Respect later user instructions to switch levels or stop ponytail.
- Make the smallest complete change, reuse existing code and dependencies, and finish affected callers, tests, fixtures, and configuration. Preserve validation, error handling, security, and accessibility.
- Verify nontrivial changes with a meaningful test or self-check. End responses with what was skipped or unchecked and any material risk.

## Automatic Git checkpoints

- Commit locally after **3 completed tasks**. This is standing authorization to create those commits during coding sessions. Do not push unless the user requests it.
- Read `docs/TASK_CHECKPOINT.md` at the start of work and update it after each completed task so the count survives new chats.
- A task is one distinct user-facing change or fix whose implementation and relevant checks are complete. Do not count tool calls, individual files, planning, failed checks, or unfinished work. Documentation tasks require a content/diff review.
- Record each completed task, its verification, and the exact repository-relative paths belonging to it under Pending. Work present before this policy was added starts outside the count; review it before explicitly adding it to a batch.
- When Pending reaches 3 tasks, review the batch diff, perform any remaining relevant checks, and make one coherent commit with a message describing the completed work. If work completes faster, checkpoint immediately at the threshold; do not wait for another user prompt.
- Stage only the batch's reviewed changes and the ledger. Never use blanket staging, include unrelated staged changes, or commit credentials, local configuration, WhatsApp session data, logs, or generated executables. For shared files, stage only the batch's hunks. Preserve other work and existing staged changes.
- Before committing, move the pending entries to a numbered batch under Checkpoints; include that ledger update in the same commit. Verify that Git created the commit. If the commit fails, restore the entries to Pending, retain their count, and report the failure. Do not bypass failing checks or hooks.
- If fewer than 3 tasks are complete at the end of a session, leave them in Pending and report the count. An explicit user request to commit can checkpoint a smaller completed batch.
- This policy is executed by coding agents during their sessions; it does not install a background watcher. If ownership of changes is unclear, record the blocker and preserve the pending tasks until it is resolved.

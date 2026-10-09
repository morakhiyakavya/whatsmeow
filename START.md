# Start a coding session

1. Read [AGENTS.md](AGENTS.md) and [the task ledger](docs/TASK_CHECKPOINT.md), then inspect `git status --short` to identify existing changes.
2. Use **ponytail:ponytail**, level **full**, for coding work. Locate and read the installed skill's `SKILL.md` through the available-skills catalogue. Honour a user's later request to change the level or stop ponytail.
3. Read [the project vision](docs/PROJECT_VISION.md) and the relevant component documentation: [API](backend/README.md) or [WhatsApp viewer](examples/login/README.md). Confirm current behaviour from the code before changing it.
4. Identify the callers, tests, fixtures, and configuration the task touches. Reuse existing code and dependencies; make the smallest complete change. Preserve validation, error handling, security, and accessibility.
5. Run relevant checks. Add a small meaningful test for new nontrivial logic; documentation-only changes need a content/diff review. Mark any deliberate implementation shortcut with `shortcut: <the limit>, <when to upgrade>`.
6. Record completed tasks in the ledger and commit locally at **3 tasks**, following AGENTS.md. Report the outcome and pending count, ending with what was skipped or unchecked and any material risk.

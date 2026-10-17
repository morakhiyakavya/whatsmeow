# Android sign-in recovery and session diagnostics

The existing WhatsApp login endpoints now handle an already-linked account even
when it has zero POS sessions. No authenticated device or logout request is needed.
A link or supplied phone number never constitutes login proof. Recovery requires a
fresh, explicit WhatsApp approval from the primary phone, then a one-use exchange
from the Android installation that started that attempt. The backend sends no
recovery message and does not disconnect or replace the WhatsApp device.

## Endpoints and approval

All requests use HTTPS and JSON; all responses are `Cache-Control: no-store`.
These three endpoints are public and require no bearer session. Unknown fields
are rejected. Never put secrets in URLs, logs, analytics or crash reports.

1. POST `/api/auth/whatsapp/start`:

   ```json
   {"phone_number":"+919876543210","device_name":"My Android phone"}
   ```

   `phone_number` uses international format (`+`, nonzero first digit, 7–15 digits).
   `device_name` is trimmed and must contain 1–128 UTF-8 bytes. A 202 response is:

   ```json
   {
     "attempt_token":"<43-character opaque secret>",
     "state":"pending",
     "expires_at":"2026-10-17T12:03:00Z",
     "next_step":"check_status",
     "recovery_message":"VENWEB: approve replacing my POS sessions <different 43-character secret>"
   }
   ```

   Keep the attempt token and message only for this attempt in protected temporary
   storage. They are different secrets: knowing the WhatsApp approval message alone
   cannot exchange a session. The response has the same shape for linked, unknown,
   disabled and otherwise ineligible numbers. Development signup still follows
   the existing pairing flow; production does not enrol unknown numbers.

2. POST `/api/auth/whatsapp/status`:

   ```json
   {"attempt_token":"<the original attempt token>"}
   ```

   Poll every two seconds until the start response's absolute `expires_at`.
   An already-linked account and an unavailable/ineligible number both return:

   ```json
   {"state":"pending","expires_at":"2026-10-17T12:03:00Z","next_step":"send_recovery_message"}
   ```

   Present: **“Recover access using WhatsApp. This will sign out your other POS
   sessions. Only approve a recovery you started yourself.”** Offer a copy action
   for the complete `recovery_message`. Ask the user to open WhatsApp on the
   **primary phone**, open **Message yourself**, paste the exact text as a new
   plain message, and press Send. Do not send it to a support agent, colleague,
   customer, bot or group. Quoted, forwarded, edited, history-sync and messages
   from other linked devices cannot approve recovery. Both phone and backend
   WhatsApp connection must be online; an existing connection alone grants nothing.

   An unlinked eligible account continues normal Linked devices pairing and
   receives `next_step:"link_whatsapp"` with its current `state`, `method`, `code`
   and code `expires_at`. Use the phone code in WhatsApp's Linked devices UI.
   `connecting`/`finishing_pairing` may have no code yet. Codes may expire before
   the overall attempt deadline. A missing/restarted/failed pairing worker or an
   expired code gives `next_step:"restart_login"`; stop polling that attempt.

   After the worker receives the authenticated primary-phone self-chat approval,
   status becomes `{"state":"verified","expires_at":"...","next_step":"exchange"}`.
   The challenge must match this account, number, link generation and attempt,
   have been sent after creation (WhatsApp timestamps have second precision), and
   arrive before expiry. The API accepts no caller-submitted “approved” flag,
   message body, JID, user ID or server-connection claim as proof.

3. POST `/api/auth/whatsapp/exchange`:

   ```json
   {"attempt_token":"<the original attempt token>"}
   ```

   A successful 200 response is the existing session contract:

   ```json
   {
     "access_token":"<secret>",
     "refresh_token":"<secret>",
     "token_type":"Bearer",
     "expires_at":"2026-10-17T12:15:30Z",
     "refresh_expires_at":"2026-11-16T12:00:30Z",
     "session_id":"<new UUID>",
     "user_id":"<existing UUID>",
     "whatsapp_account_id":"<existing UUID>"
   }
   ```

   Recovery atomically rechecks active user, membership, business and unchanged
   link; deletes that user's old POS sessions; issues one fresh session; consumes
   the proof and invalidates sibling recovery attempts. Concurrent exchanges of
   the same or different approved attempts produce at most one successful
   replacement. An unsuccessful exchange rolls back all these changes. Normal
   first-link exchanges and authenticated device-token login retain their existing
   semantics. Approval itself does not revoke a session; successful exchange does.

   Persist both new credentials atomically before making authenticated requests.
   Fetch `/api/me` and confirm the recovered user/business before replaying queued
   work. Old device tokens, push registrations and pending push jobs disappear
   through existing session cascades. Register this installation's current FCM
   token again using `PUT /api/push-devices/{installationID}`; never copy an old
   installation's credentials or registration into the new session.

## Expiry, limits, offline behaviour and terminal outcomes

- Attempts and verified recovery proofs expire **three minutes from start**;
  approval does not extend the deadline. No public endpoint extends or resets it.
- Starts permit **three attempts per phone per minute**, including unknown numbers;
  starts and exchanges/refresh share **ten requests per remote address per minute
  per process**. Status polling is not counted by that limiter. A 429 uses the
  existing generic `rate_limited` error and `Retry-After: 60`. The phone limit is
  database-serialized; the address limiter is process-local. Configure trusted
  ingress limits before multi-replica or public high-volume operation.
- Independent attempts receive independent challenges. Starting an unauthorised
  recovery cannot supersede, revoke or disconnect an existing session/link. Only
  a successful, freshly approved exchange invalidates competing attempts.
- Expired, consumed or invalid status tokens return 401 `invalid_token`.
  Exchange before approval or after expiry returns 401 `login_unverified`; a
  racing consumed exchange can return `invalid_token`. Revoked/ineligible links
  can return `link_revoked`. All are terminal for that attempt: discard its
  secrets and offer **Start again**. The error envelope remains
  `{"error":{"code":"...","message":"..."}}`; private diagnostic reasons are
  never added to public errors. Do not infer account existence from pending/expiry.
- Invalid JSON/phone/device names return 400. Missing worker configuration returns
  503 `whatsapp_unavailable`; worker capacity/start conflicts can return 409
  `login_busy`. Unexpected database failures retain the generic 500 contract.
  Retry network/503/500 errors with bounded backoff only until the attempt deadline.
- If Android or the backend/WhatsApp connection is offline, display **Reconnect
  and try again** when appropriate and always stop at `expires_at`. Offline state
  alone never grants access: approval must be received and verified before expiry.
  Re-send a fresh approval
  after reconnection, within the same live attempt, or start again after expiry.
  Connectivity is deliberately not revealed by public recovery status. Worker
  restarts preserve challenges and verified proofs in PostgreSQL, but a message
  missed while offline may need sending again. No offline ownership bypass exists.
- If exchange succeeded but its response was lost, the proof remains consumed.
  Start and approve another recovery; never blindly loop exchanges. A lost refresh
  response likewise requires recovery if the rotated credentials were not saved.
- Abandoning a screen merely discards its secrets. Attempts expire automatically.
  Do not call `logout-all` or disconnect as a recovery precondition.

Keep users, business memberships, customers, transactions, immutable versions,
confirmation/message IDs and server idempotency records. On Android, retain queued
transactions and their original `Idempotency-Key`, payload, account and business
identity across authentication failures. Resume them only after the same recovered
identity is confirmed; isolate queues when switching users. Recovery neither cancels
pending WhatsApp confirmations nor regenerates transaction identities.

## Refresh and logout investigation

Access tokens last 15 minutes; rotating refresh tokens last 30 days from renewal.
A bearer 401 can mean access expiry alone: Android should serialize refreshes,
atomically replace **both** tokens, and retry the original request once. Do not
clear credentials for transport failures, 429 or 5xx. Refresh rejects reused old
tokens, so concurrent refreshes or non-atomic persistence can cause apparent logout.
Do not interpret a temporary WhatsApp disconnection as a POS logout.

Migration 012 adds private `ipo_pos.auth_events`. Session deletion triggers record
`logout`, `logout_all`, `device_removed`, `session_expired`,
`whatsapp_link_changed`, `recovery_replaced`, or fallback `session_deleted`.
Refresh records `refresh_rotated` or a private rejection reason:
`refresh_expired`, `user_disabled`, `membership_disabled`, `business_archived`,
`link_changed`, `unknown_refresh`. Events contain time, internal user/session UUIDs
when known and configured release for API operations, **no credentials, token hashes,
phone numbers, message content or push tokens**. Worker/SQL-trigger operations may
have an empty release. Failed diagnostic writes emit only a generic server log.
`unknown_refresh` deliberately covers malformed ownership history: a well-formed
unknown, previously rotated or deleted token cannot be distinguished after its hash
has been replaced/deleted. Invalidly formatted tokens are rejected before lookup.

Operators can inspect events by existing internal user UUID/time using a protected
database connection. Delete events older than 30 days in operator maintenance:
`DELETE FROM ipo_pos.auth_events WHERE occurred_at < now()-interval '30 days';`.
Runtime grants permit insertion/read, but no event update/deletion. There is no
public audit endpoint or new scheduled job. Correlate refresh/session events with
Sentry route/status/release and startup logs; deployment/restart alone does not
delete PostgreSQL sessions or rotate signing keys (tokens are opaque DB secrets).

**Observed on 17 October 2026:** read-only checks found one local user with zero
sessions and an intact generation-1 link; no disabled user/membership or archived
business. Hosted storage had one existing session, still refreshable until
13 November 2026 20:23:54 UTC, with access expiry 14 October 20:38:54 UTC, an intact
generation-2 link, and no disablements/archival. Sentry recorded successful refresh
on 14 October 20:23:54 UTC and rejected refresh on 17 October 11:31:37 UTC
(17:01:37 IST), both on release `c269a0e`. The latter was followed by repeated
WhatsApp login status polling. No logout/disconnect route appeared in the seven-day
aggregate query. See [refresh traces](https://vanitilabs.sentry.io/explore/traces/?project=4512250484883456&query=transaction%3A%22POST%20%2Fapi%2Fauth%2Frefresh%22&statsPeriod=7d).

This establishes refresh rejection as the observed hosted failure, **not its exact
cause**. Persisted refresh expiry had not elapsed; current storage gives no evidence
of account disablement or total session revocation. A stale/incorrect refresh token
is plausible, but historical token input, client persistence and deletion reasons
were intentionally not retained. Local zero-session deletion predates this audit.
No logout cause can be attributed conclusively to a deployment or operator action.
Capture Android's non-secret error code/time, app version, session UUID and refresh
serialization/persistence outcome to correlate the next incident; never capture
credentials. This repository contains no Android source.

## Rollout and verification

Apply `database/012_account_recovery.sql` once as the migration owner and reapply
`database/api_grants.sql` with the existing database/role variables before running
this API. Fresh local and hosted setup include it. Startup rejects older schemas.
It is additive except for narrowing the exclusive pairing-attempt index; no user,
membership, transaction, WhatsApp credential or existing session is deleted by the
migration. Rollback requires deliberate schema/grant work after reverting callers.

The disposable PostgreSQL/fake-protocol suite covers zero-session linked recovery,
fresh live approval, wrong source/device/chat, expired/replayed proofs, concurrent
replacement, offline waiting, unauthorized logout/disconnect, preserved database
records and transaction retry identities, push cleanup and private refresh reasons.
Real primary-phone self-chat delivery, Android screens/secure persistence and
hosted migration/deployment remain separate rollout checks. Manually unlinking in
WhatsApp is an exceptional fallback, not the recovery protocol.

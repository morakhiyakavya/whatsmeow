# Android handover: transactions and analysis inputs

Use this document as the Android developer/agent entry point. The implementation
baseline is backend commit `ccf6c5c`; the detailed protocol is
[BUSINESS_DATA_SYNC.md](BUSINESS_DATA_SYNC.md). Existing contact/profile contracts
remain in [FCM_ANDROID_HANDOVER.md](FCM_ANDROID_HANDOVER.md).

## Service addresses and ownership

| Purpose | Address |
|---|---|
| Android API base | `https://whatsapp-pos-dev.onrender.com` |
| Readiness | `GET /health` |
| Verify signed-in account and memberships | `GET /api/me` |
| Transaction/analysis-input catch-up | `GET /api/businesses/{businessID}/data/sync` |
| Contact directory catch-up | `GET /api/businesses/{businessID}/contacts/sync` |
| Azure importer health, operational reference only | `https://172-198-153-142.sslip.io/health` |

Keep Android on Render. Azure imports shared IPO data and runs the same backend
revision, but has no WhatsApp workers. Do not implement client failover to Azure
or send sign-in/pairing traffic there. Both hosts use the same Supabase database;
Android communicates through the Go API, never directly with Supabase.

Backend prerequisites are migrations 001–017 and refreshed runtime grants.
Deploy migration 017 and the endpoint before distributing this Android build.
Readiness alone proves neither that a particular revision is live nor that FCM
reached a handset; confirm the deployment workflow and explicit device tests.

## FCM is a refresh hint

```json
{"type":"business_data_changed","user_id":"<recipient UUID>","business_id":"<business UUID>","revision":"42"}
```

This event contains no transaction body, customer identity, amount or analysis
result. Keep the existing normal-priority, data-only FCM registration flow and
POS authentication. Match the event to the signed-in user and an authorised
business. Persist the highest revision and a dirty marker before scheduling work;
ignore duplicate/older revisions. Use integer/decimal-string comparisons without
floating-point conversion. Never treat a push as proof that a transaction saved.

## Delta requests

Use `Authorization: Bearer <POS access token>` over HTTPS. Renew the existing POS
session through its current workflow; do not add another authentication system.

```http
GET /api/businesses/{businessID}/data/sync?limit=100&since_revision=0
Authorization: Bearer <POS access token>
```

`since_revision=0` obtains the initial retained records. Thereafter send the last
fully committed local revision. A response has `revision` (string), `unchanged`
(boolean), `items` and three nullable next-cursor fields:

```json
{
  "revision":"42",
  "unchanged":false,
  "items":[{
    "kind":"transaction",
    "id":"<transaction UUID>",
    "data":{"id":"<transaction UUID>","business_id":"<business UUID>","quantity":"2.500000","rate":"-10.250000","total_amount":"-25.63","version":2,"status":"cancelled"}
  }],
  "next_revision":null,"next_kind":null,"next_id":null
}
```

The transaction body above is abbreviated; retain the complete returned object.
Supported kinds:

- `transaction`: authoritative current record, including cancellations. Preserve
  decimal strings and the server's rounding; do not rebuild totals from pushes.
- `assumptions`: the business's latest saved GMP, expected application counts,
  Subject 2 pricing factor and version, keyed by `ipo_id`.
- `ipo`: decorated shared catalogue record and latest `source_basis`.
  `selectable_until` uses the existing catalogue lifetime; evaluate it in India
  time, including when cached data ages. Retain archived/unavailable rows in the
  cache while excluding them from new-trade choices.

A null `data` is a tombstone. Remove that kind/ID from local records. These are
latest-state deltas; use the existing transaction history API for audit versions.
Position balances, allotment reconciliation and computed reports remain pending;
do not present saved inputs as completed analysis.

When `next_revision` is non-null, keep the original `since_revision` and the
first response's `revision`, then pass all three cursor values:

```http
GET /api/businesses/{businessID}/data/sync?limit=100&since_revision=12&revision=42&after_revision=30&after_kind=transaction&after_id={returned UUID}
```

Continue until all three next fields are null. Stage every page, then publish
the complete delta and revision together. Never advance the revision after a
partial download. Reject malformed/mismatched records, repeated cursors and
responses below the highest relevant invalidation. A newer event arriving during
refresh must remain dirty until a sufficient response has been committed.

| Response | Android behaviour |
|---|---|
| 200, unchanged | Retain records; update the successful-check timestamp |
| 200, changed | Commit all pages and the revision atomically |
| 400 | Correct the request/cursor; do not discard the last good cache |
| 401 | Use existing renewal/sign-in handling; prevent unauthorised actions |
| 403 | Stop syncing the inaccessible business; honour refreshed memberships |
| 409 `business_data_changed` | Discard staged pages and restart from the last committed revision |
| Network/429/5xx | Retain data and dirty marker; retry with backoff/Retry-After |

## Existing Android implementation

The sibling `transactions` checkout already contains the implementation:

- `BusinessData.kt`: encrypted, context-bound SQLite rows, staged pages,
  atomic deltas, tombstones and local paging.
- `ContactDirectoryUpdates.kt`: the shared scoped dirty-marker mechanism;
  business changes use the separate `business_data_updates` namespace.
- `PhotoMessagingService.kt`: validates business-data events and schedules sync;
  deleted-message callbacks mark cached data dirty for recovery.
- `ContactDataSyncJob.kt` / `VenwebApp.kt`: share the existing network-constrained
  background scheduling, reconnect handling and best-effort periodic catch-up.
- `PosViewModel.kt`: refreshes on foreground entry, manual demand and entry to
  Transactions/Dashboard after a successful check is at least one minute old.
  Dirty events bypass this window. In-flight requests coalesce and preserve drafts,
  unresolved submissions, selected counterparties and WhatsApp receipts.
- `DeskScreens.kt`: local transaction pages and separately labelled saved
  GMP/factor inputs. No continuous foreground polling loop is required.

Storage belongs under `noBackupFilesDir`. Encrypt financial bodies using the
existing Android Keystore key, bind encryption to scope/kind/record identity and
clear the cache on logout/session rejection. Hashed IDs, kinds and ordering times
remain SQLite metadata; they are not a fully encrypted database file.

The development APK is `transactions/app/build/outputs/apk/debug/app-debug.apk`.
Its verified SHA-256 is
`A3DD50C00DD82537BA6A3078E2A6C414DE83A51EB7858189680C54C0182AFE60`.
The older root `transactions/venweb.apk` was retained; use the build-output APK
for this increment. Android source remains in that checkout alongside earlier
uncommitted work. Preserve those changes when continuing implementation.

## Verification and next device check

Backend migration/API tests, vet, Android unit tests, 41 selected emulator tests,
debug build, lint and APK signature verification passed locally. Tests cover
partial-page rollback, deltas/cancellations, scope/session changes, newer events,
draft preservation, forced refresh, local pagination and existing search/receipt
flows. See the task ledger for exact checkpoint evidence.

Still perform a deliberate real-device test: install the matching build after
deployment, sign in normally, observe a separately authorised transaction change
from another client, confirm its FCM-triggered refresh, then repeat with the
handset offline and verify catch-up on reconnect/foreground/manual refresh.
Confirm that no financial values appear in the FCM payload or lock-screen text.
Do not create customer transactions/messages merely to smoke-test deployment.
FCM/background execution is best-effort; offline transaction submission and a
production-signed release are outside this increment.

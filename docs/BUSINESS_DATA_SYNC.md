# Transaction and analysis-input synchronisation

Migration `017_business_data_sync.sql` extends the existing FCM sender to changes
in transactions, business analytics assumptions and shared IPO/source-basis data.
Apply it after 016, then reapply `database/api_grants.sql`, before starting the new
backend or installing the matching Android build. Existing installations are not
upgraded automatically. This implementation has not been deployed.

## Change signals

Transaction creation, edits and cancellation, saved assumption revisions and
catalogue/source-basis changes record a change and enqueue a notification in the
same database transaction. Rollbacks publish nothing. Replayed creation requests
do not advance revisions. Notifications coalesce per business/installation:

```json
{"type":"business_data_changed","user_id":"<recipient UUID>","business_id":"<business UUID>","revision":"42"}
```

The payload contains no amounts, rates, customer identities or analysis results.
Session, user, membership, business binding and device generation are checked
again before sending. The existing normal-priority transport, 24-hour lifetime,
five-attempt retry policy and generation-safe token removal are reused.
Revocation cascades through the installation to pending jobs; an accepted push
cannot be recalled. Android checks the recipient and business before persisting
its dirty marker. Contact/profile messages retain their existing contracts.

## Authenticated delta feed

`GET /api/businesses/{businessID}/data/sync?limit=100&since_revision=0`
requires the existing POS session and active business membership. Omitting
`since_revision` starts at zero. Records include cancelled transactions and
archived IPOs, allowing clients to replace obsolete state rather than silently
losing cancellations. Each record is the latest state, not every historical
version; existing transaction/history endpoints remain authoritative.

```json
{
  "revision":"42",
  "unchanged":false,
  "items":[{"kind":"transaction","id":"<transaction UUID>","data":{"id":"<transaction UUID>","business_id":"<business UUID>","quantity":"2.500000","rate":"-10.250000","total_amount":"-25.63","version":2,"status":"cancelled"}}],
  "next_revision":null,
  "next_kind":null,
  "next_id":null
}
```

Kinds are `transaction`, `assumptions` and `ipo`. Transactions reuse the existing
decimal-string representation and database rounding. Assumptions reuse the saved
business forecast/GMP/factor representation. IPOs reuse the existing decorated
catalogue data and include the latest `source_basis` and `selectable_until` date;
Android applies that date in India time when offering a trade. A null `data` is a
tombstone, removing a record from the cache. Financial values remain strings.

For another page, retain the original `since_revision`, send `revision` from the
first response and copy all three returned cursor fields as `after_revision`,
`after_kind`, `after_id`. Pages contain at most 100 records. Revision and data are
read in one PostgreSQL snapshot; a concurrent committed change returns 409
`business_data_changed`, requiring a restart from the last committed local
revision. Invalid cursors return 400. A current revision returns `unchanged:true`
and no records. A revision ahead of the server returns 409 rather than silently
acknowledging missing data.

The database stores one change key per business/record, with an index for revision
pagination. The revision row serialises writers through commit so a delta cannot
skip an earlier uncommitted write. Shared catalogue changes fan out to businesses
in a consistent order; initial business/catalogue publication shares an advisory
lock to avoid missing a concurrently created offering. Initial installation is
O(all retained records); subsequent downloads are O(changed records plus indexed
lookups). Shared catalogue writes have O(businesses) fanout, not per-device data
copies; pending FCM jobs still fan out per eligible installation. Production-scale
contention and storage costs have not been benchmarked.

## Android refresh policy

Android stores records in a separate native SQLite database under `noBackupFilesDir`.
Bodies use the existing Android Keystore encryption with context-bound scope,
kind and hashed record ID. User/business scope and record IDs are hashed; record
kind and creation-time ordering metadata are visible in SQLite. Pages are staged
without replacing usable data, then changes and the new revision commit together.
Interrupted pages, malformed responses and changed sessions retain the previous
snapshot. Newer dirty markers arriving during refresh remain pending.

Transactions reads and Load more use the local store. The dashboard reads saved
GMP and Subject 2 pricing factor from the same cache. No position calculation,
allotment engine, report generation or offline transaction submission is added:
these are still future backend features. Source basis and saved forecasts are
cached as inputs, not presented as completed analysis results.

Refresh is triggered by FCM, foreground entry, network reconnection, the existing
best-effort 15-minute background job, manual transaction/catalogue refresh and
entry to Transactions/Dashboard when the last successful check is at least one
minute old. Dirty events bypass that freshness window. Repeated foreground
requests share the in-flight sync; the background worker shares the store's
sync mutex. Only the selected business syncs automatically. Pending markers for
other businesses are retained until selected. Drafts, unresolved submissions and
WhatsApp receipt state are preserved; refresh failures have their own message.
No continuous foreground polling loop or extra Firebase dependency is introduced.

FCM and Android background execution can be delayed or missed. Cached data is
not proof that the latest server change has arrived. On-demand/foreground catch-up
is the recovery path, and server validation still governs transaction saves.

## Rollback

Stop the new backend first and revert the Android build before deliberately
removing migration 017's five triggers, three functions and three tables. This
loses sync revisions/pending hints, not transactions, their history or assumptions.
Reverting source alone does not reverse a migration. Preserve 011/016 tables used
by the earlier contact sender. No hosted migration, credential change or real
FCM/customer message is part of local implementation verification.

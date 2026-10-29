# Contact and WhatsApp profile invalidations for venweb Android

Transaction and analysis-input notifications use the separate
[business-data sync contract](BUSINESS_DATA_SYNC.md), requiring migration 017.
Their FCM payload contains only recipient/business IDs and a revision.

## Hosted rollout (26 October 2026 IST)

Migration 016 and the revision-checked sync endpoint are deployed. Render now
uses the dedicated `venweb-fcm` service account in project `vanitilabs`; Google's
`validate_only` accepted an existing installation without delivering a message.
See [the deployment record](RENDER_DEPLOYMENT.md) for source and credential setup.
The matching Android development APK is sibling `transactions/venweb.apk`,
version 0.2.3 (versionCode 6). Real FCM reception/background execution and a
production signing/release build remain unchecked.

## Local directory synchronisation

The sibling Android app now stores contact records locally in SQLite, with encrypted
row payloads using its existing Android Keystore key. Search and selection read this
database; FCM schedules catch-up and profile misses enqueue background requests.
Login, customer creation and transaction saves retain existing authenticated server
workflows. Initial installation requires one successful directory sync; cached
identity still requires the existing session verification before actions are enabled.

`GET /api/businesses/{businessID}/contacts/sync?limit=100` requires ordinary POS
authentication and active business membership. Responses include:

```json
{
  "revision": "42",
  "unchanged": false,
  "items": [{"kind":"registration","id":"+919000001010","data":{"phone_number":"+919000001010","registered":true,"checked_at":"2026-10-25T12:00:00Z"}}],
  "next_kind": null,
  "next_id": null
}
```

Kinds are `contact`, `customer` and `registration`. Customer payloads match the
existing customer list; contact payloads reuse the existing user-specific display
name/provenance query. Registration rows contain only number, boolean and check time.
The backend keeps at most limit+1 candidates from each indexed source before merging
the page. Each response's records and revision share one PostgreSQL statement snapshot.

For another page, send `revision` plus the exact returned `after_kind`/`after_id`
(URL-encode phone numbers). A changed revision returns 409 `directory_changed`:
restart from the first page and keep the previously committed local snapshot.
After a successful sync, a first-page `since_revision` returns `unchanged:true` and
empty items when current; revision `0` is valid before the first observed change.
Invalid cursors/revisions return 400. Session rejection remains 401 and inaccessible
businesses remain 403. Only complete snapshots replace local rows in one transaction;
renames, removals and registration changes therefore cannot leave half-applied data.

The app persists dirty markers before scheduling network-constrained native jobs,
checks on foreground/reconnection and schedules best-effort 15-minute catch-up for
the selected business. Pending markers for other businesses wait until selected.
Requests for missing profiles coalesce in a durable queue capped at 200 contacts;
jobs handle at most 32 per run. Unknown/expired registration observations are checked
in the background, then a directory sync saves the authoritative result locally.
Saved observations expire after 24 hours for filtering; unknown numbers stay selectable.
Photo/name/About metadata retains the existing event-driven no-expiry policy.

No new backend table, Firebase dependency or migration beyond 016 is
needed for this feed. Deploy the feed before installing the local-first Android
build. Full-snapshot cost at production scale, real FCM delivery and device background
scheduling remain unverified. FCM is a change hint rather than the only data source.

## Contact freshness extension (25 October 2026)

Apply [migration 016](../database/016_contact_directory_push.sql) after 001–015,
then reapply runtime grants before deploying this version. Startup requires its
directory outbox. Existing databases are not upgraded automatically. Rollback
requires stopping this version and deliberately removing the three directory
triggers, their function and the two new tables; contacts and transactions remain.

Shared-name additions, renames and withdrawals, customer creation/name/phone/archive
changes, and changed WhatsApp registration results advance a business directory
revision. Repeated identical names/results and timestamp-only registration checks
do not advance it. A committed change queues one latest invalidation per eligible
installation/business in `directory_push_outbox`; rollback queues nothing.
Its normal-priority data-only payload is:

```json
{"type":"contacts_changed","user_id":"<recipient UUID>","business_id":"<business UUID>","directory_revision":"42"}
```

Directory notifications carry no contact names, numbers or record bodies. Delivery
rechecks installation generation, renewable-session eligibility and active business
membership, including business-bound sessions. The existing sender, leases, retries,
24-hour lifetime and generation-safe token deletion serve both outboxes. Each sweep
processes at most 32 jobs from each queue. Neither notification is a delivery receipt.

WhatsApp `PushName`, `BusinessName` and `UserAbout` events now advance the existing
`profile_revision` and use the existing `contact_profile_changed` payload below.
They preserve the known photo state/ID and photo-event timestamp. No name/About
text is stored in the backend revision tables or sent in FCM. Known LIDs and a
push-name event's phone-number alternative are resolved; groups and unknown LIDs
are ignored. Duplicate metadata announcements may advance another revision; consumers
always refetch current state. WhatsApp address-book names remain separate from
staff-supplied shared names and saved customer records.

The Android implementation is in the sibling `transactions` repository supplied
by the user. It persists scoped directory watermarks and dirty markers before
refresh, ignores duplicate/older events, reloads customers and shared contacts,
and clears obsolete registration results without changing a draft or selected
counterparty. Refresh completion cannot acknowledge an invalidation that arrived
while it was running. Failed refreshes retain the dirty marker for a later retry;
foreground entry also refreshes the directory if the session is ready.
Reported deleted messages mark both directory and profiles stale.

Android's encrypted profile cache now includes the returned name and About text,
using the same scope, revision and 200-entry bound as pictures. The selected
counterparty shows a separately labelled WhatsApp name and About; it never replaces
the saved/shared name. Visible profiles refresh on invalidation and off-screen
profiles refresh when needed. Preserve the app's existing event-driven cache policy:
there is no hourly polling or time-based expiry. Unreported lost profile events can
therefore leave cached profiles stale until another invalidation or cache eviction.

This extension has not applied a live migration, configured Google credentials,
deployed either application or sent a real FCM message. Emulator transports verify
local handling separately from Google-to-device delivery.

The Go backend implements device registration, persistent profile revisions and
asynchronous Firebase Cloud Messaging HTTP v1 sending. Android integration and real
device delivery are separate work. The target Firebase project is `vanitilabs`,
sender ID `645009154369`, Android package `com.venweb.app`. No Firebase Authentication
or new signup flow is required: keep the existing POS authentication.

## Prepare the backend

1. Apply [migration 011](../database/011_contact_profile_push.sql) after migrations
   001–010 using the migration owner. Reapply [runtime grants](../database/api_grants.sql)
   with its existing `database_name` and `api_role` psql variables. The runtime role
   can manage registrations; only the trusted worker writes revisions/outbox jobs.
The original photo increment required migration 011; the current version also requires 016 above.
2. Keep `WHATSAPP_DATABASE_URL` configured with the trusted worker identity.
3. Supply a service identity with `roles/firebasecloudmessaging.admin` in
   `vanitilabs`. Mount a private service-account JSON or supported workload-identity
   credential file through the deployment secret store, outside tracked source.
   Set `GOOGLE_APPLICATION_CREDENTIALS` to that absolute file path and set
   `FCM_PROJECT_ID=vanitilabs`. For example, a Render secret file can be mounted as
   `/etc/secrets/firebase-server.json`; its contents never belong in Git or an APK.
4. Leaving `FCM_PROJECT_ID` empty disables the sender. Registration, event detection
   and profile revisions remain available. Enabling it without a worker URL or a
   credential path fails startup; malformed/client/developer-login credentials are
   rejected. Permission/project validation occurs on an actual Google request.

The Android `google-services.json` and Firebase CLI login are not server credentials.
The Go sender uses Google's OAuth library with the `firebase.messaging` scope and
short-lived Google access tokens, distinct from POS bearer tokens. It sends only to
`https://fcm.googleapis.com/v1/projects/{FCM_PROJECT_ID}/messages:send`.

This implementation does not create keys, change Google IAM, apply migrations to
an existing installation, or deploy itself. Migration 011 is additive. Rollback
requires stopping the new code first and explicitly removing its three tables,
two disablement triggers/function and session uniqueness constraint; this loses
registrations/revisions/pending invalidations, not users or transaction history.

## Register an Android installation

```http
PUT /api/push-devices/{installationID}
Authorization: Bearer <POS access token>
Content-Type: application/json

{"fcm_token":"<Firebase registration token>"}
```

Generate a random UUID once per installation. This installation ID is distinct
from the POS session ID, additional-device login token and FCM token. Register on
authenticated startup and token changes; retry failures without blocking POS.
Success is an empty `204` response with `Cache-Control: no-store`. Invalid UUIDs,
empty/oversized/whitespace tokens and unknown fields return `400`; invalid/expired
POS access tokens return `401`. Tokens are opaque strings of at most 4096 UTF-8 bytes.
Do not send a user or session ID: the server derives both from authentication.

Registration is idempotent and supports multiple installations per user. POS token
renewal retains the stable session ID and therefore its registration. A new FCM
token replaces the old token; another user/session taking over that installation
replaces the previous association. A token is associated with only one installation.
Logout/device revocation/all-device logout/unlink delete the relevant sessions and
cascade to registrations and queued notifications. User or membership disablement
also removes affected registrations; re-enabling requires registering again.
Renewable sessions remain eligible while their refresh lifetime is valid, even if
the short-lived access token expires. Every delivery rechecks session validity,
installation generation, active user/business membership, account ownership and
the current WhatsApp link generation.

## Read profiles and receive invalidations

The existing owner-only `POST
/api/businesses/{businessID}/whatsapp-accounts/{id}/contact-profile` request is unchanged.
Successful responses add one field to the current name/About/photo response:

```json
{
  "phone_number": "+919876543210",
  "name": "Contact's WhatsApp name",
  "about": "Available",
  "picture_url": "https://example.com/photo.jpg",
  "picture_state": "available",
  "profile_revision": "42"
}
```

`profile_revision` is a positive decimal integer encoded as a JSON string, scoped
to business + WhatsApp account + normalized phone. It is not Android's local
`picture_version` UUID. The first observation starts at `1`; a changed picture ID
advances it even if the URL stays the same. The server stores state/ID/revision,
not image bytes, image URLs, names or About text. It does not compare downloaded bytes.

WhatsMeow `Picture` events advance the revision on changes/removal. Duplicate known
events and older timestamps are ignored; notifications with the same timestamp but
different IDs/states are processed in arrival order. A later successful lookup
resolves event state and detects hidden/unset transitions. A failed lookup does
not change the stored observation or classify the picture as hidden/removed.
Known privacy LIDs are normalized using the existing account mapping; group photos
and unmapped LIDs are ignored. No extra lookup scans contacts to fill missing mappings.

A revision and its outbox jobs commit together before sending. The sender targets
each authorised registered installation with a normal-priority, data-only message:

```json
{
  "message": {
    "token": "<registered device token>",
    "android": {"priority": "normal", "ttl": "86400s"},
    "data": {
      "type": "contact_profile_changed",
      "user_id": "<recipient user ID>",
      "business_id": "<business ID>",
      "whatsapp_account_id": "<account ID>",
      "phone_number": "+919876543210",
      "profile_revision": "42"
    }
  }
}
```

There are no topics, visible notification payloads, image bytes/URLs, names, About
text or POS credentials. Pending events coalesce to the latest revision per
installation/contact. A separate sender processes up to 32 jobs per sweep, with
at most five attempts and a 24-hour pending lifetime. Leases survive restarts;
duplicates remain possible after an uncertain send. Transient network/429/5xx
failures retry with exponential delays starting at one minute, jitter and any
longer `Retry-After`. An explicit typed `UNREGISTERED` response removes only the
rejected registration generation. `INVALID_ARGUMENT` is conservatively retained
because it can describe a payload error; other permanent failures discard the job
and emit only a generic operational log. FCM acceptance does not prove app delivery.

No FCM request runs inside a POS handler or WhatsApp worker. Device switching or
logout cannot recall an already in-flight/accepted notification: Android must
validate the recipient and scope before using any event.

## Android implementation checklist

- Add compatible Firebase Messaging and Google services dependencies to `venweb`.
- Register the token using POS authentication on startup, login/user switching and
  token changes. Keep installation IDs stable; refresh registration after session replacement.
- Accept this event type only for the current `user_id` and matching business/account.
  Persist the highest revision as a decimal integer, without floating-point conversion.
- Evict the affected cached image when its revision is older. Persist invalidation
  before scheduling refresh; duplicate/out-of-order messages must not undo newer state.
- Refresh visible contacts through the profile API. Fetch off-screen contacts when
  needed; keep network/image work out of the FCM callback. Discard an in-flight
  response older than the highest invalidation, and check scope again before display.
- Preserve directory foreground refresh and reported-missed-message recovery; WhatsApp events,
  FCM delivery and background execution are not guaranteed. Show placeholders when
  photos are unavailable. Never infer verified customer identity from a photo.

## Verification

Backend tests use disposable PostgreSQL and synthetic WhatsApp/FCM clients. Real
Google credentials, Android token reception, device cache invalidation, offline
recovery and background delivery still require end-to-end testing. Free hosting
can interrupt WhatsApp connectivity and delay processing. No dedicated push-device
rate limiter, production load benchmark, delivery receipt tracking or photo archive
is included.

References: [HTTP v1 authentication](https://firebase.google.com/docs/cloud-messaging/send/v1-api),
[token management](https://firebase.google.com/docs/cloud-messaging/manage-tokens),
[FCM errors and retries](https://firebase.google.com/docs/cloud-messaging/error-codes),
[Android reception](https://firebase.google.com/docs/cloud-messaging/android/receive-messages).

# Contact-photo invalidations for venweb Android

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
   Startup now requires migration 011, including when FCM sending is disabled.
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
- Preserve hourly expiry refresh and refresh after reconnection; WhatsApp events,
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

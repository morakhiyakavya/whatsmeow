# Private WhatsApp text viewer

This standalone example uses the whatsmeow source in this checkout. It displays
a terminal QR code, saves your login in PostgreSQL, and reconnects on subsequent runs.
It shows incoming and outgoing text messages in private 1:1 chats, with names,
phone numbers when available, and up to 10 older texts from the same chat.
PostgreSQL stores the account keys, encryption sessions, and synced contacts in
the `whatsapp` schema. The runtime does not read or write `session.db`.

## Run on Windows

Use Go 1.27.1, the toolchain specified by this checkout. After installing Go,
restart your terminal (and your editor if its terminal cannot find `go`).

First create a local development database if you do not already have one.
Run `psql -h 127.0.0.1 -U postgres -d postgres`, then enter:

```sql
CREATE ROLE ipo_app LOGIN;
\password ipo_app
CREATE DATABASE ipo_app OWNER ipo_app;
```

The password prompt stays local. These commands are for a new development
database; use your existing database/user instead if already configured.
From the repository root, apply the application schema once:

```powershell
psql -h 127.0.0.1 -U ipo_app -d ipo_app -v ON_ERROR_STOP=1 -f database\001_initial.sql
```

Configure the demo in PowerShell:

```powershell
go version
cd examples\login
Copy-Item .env.example.ps1 .env.local.ps1
```

Edit `.env.local.ps1` with your database username, password, host, port, and name.
This file is ignored by Git. Percent-encode special characters in URL passwords.
The example uses `sslmode=disable` for localhost; configure `sslmode=verify-full`
and trusted certificates for a remote server. Then run:

```powershell
. ./.env.local.ps1
go mod download
go run .
```

On your phone, open **WhatsApp > Linked devices > Link a device** and scan the
QR code. Widen your terminal if the QR code wraps. Press **Ctrl+C** to disconnect;
run `go run .` again to reconnect with your saved session.

New pairings appear as **venweb** in WhatsApp's Linked devices list. Existing
linked devices retain their previous name until they are unlinked and paired again.

Wait for **Connected to WhatsApp** before treating login as complete. Pairing
acceptance comes first; WhatsApp then requests a reconnect (stream code `515`).
The demo enables automatic retries for temporary connection failures during
that reconnect and when resuming a saved session.

The first PostgreSQL login needs a new QR pairing; the old SQLite login is not
automatically migrated. Existing `session.db` files are preserved and still
ignored by Git. Whatsmeow initializes/upgrades its own PostgreSQL credential
tables on startup. The database user needs permissions to create/use `whatsapp`
and apply library migrations; no credentials are printed in connection errors.

If exactly one session is saved, `go run .` resumes it. With several accounts,
choose one explicitly; the demo never selects an arbitrary account:

```powershell
go run . -session "DEVICE-JID-PRINTED-AFTER-CONNECTION"
go run . -new-session
```

`-new-session` prepares another account for QR pairing. Do not use it for an
account already running in another process. To unlink, use WhatsApp's Linked
devices screen. The PostgreSQL credential store is shared across accounts, but
each account's keys and contacts remain separate. This is a terminal viewer;
the viewer has no application ownership checks. The manual business API and
application sign-in are implemented in [backend](../../backend/README.md);
authenticated WhatsApp account mapping remains a subsequent integration step.

For the new local database, `../../scripts/Start-WhatsAppViewer.ps1` reads
`WHATSAPP_DATABASE_URL` from the repository root `.env`. Run that script from
the repository root as `./scripts/Start-WhatsAppViewer.ps1`.

## View messages with context

```powershell
go run .
```

Wait for **Connected to WhatsApp**. Incoming messages are marked **IN**; messages
you send from your phone or another linked device are marked **OUT** when
WhatsApp syncs them to this viewer. Each block shows the private chat, sender's
name and phone number, message ID, timestamp, up to 10 older texts, and the
newest text marked **LATEST**. Groups, broadcasts, statuses, newsletters, media,
reactions, and messages without readable text are excluded from both live output
and history buffers. Press **Ctrl+C** to stop.

Names come from synced contacts (saved names first), profile names, or the
message's supplied name. Outgoing messages show your own name and phone number;
the chat label identifies the other person. WhatsApp sometimes identifies people
using a masked account ID (LID) instead of their phone number. The viewer uses
the accompanying phone identity or the synced LID-to-phone mapping to resolve
it. If no mapping exists, it shows **number unavailable**, or a partial number
when only a redacted number is supplied. LID digits are never treated as phone
numbers. Buffered contact labels refresh when more contact data becomes available.

When a private chat first receives a new text and its buffer is incomplete, the
viewer requests a page of 50 older messages from your primary phone using
WhatsApp's history sync protocol, retaining up to 10 older texts after filtering.
It shows the new text immediately, then refreshes the block
when history arrives. Automatic history sync also seeds the buffers. An initial
block may therefore contain fewer than 10 older messages. The phone must be able
to return history; fewer messages may exist, and WhatsApp may not supply them.
Keep your primary phone online while loading context.

Buffers retain only 10 older texts plus the newest per chat in memory, sorted
chronologically and deduplicated by message ID. They start fresh on restart and
are repopulated from available history. History for inactive chats is not dumped
to the terminal. A failed history request is reported, and a later message can
retry it.

Plain text and text replies appear in full. Media captions are excluded.
Message text and names are quoted so terminal control characters are escaped.

## Connection problems

- **TLS handshake timeout / EOF:** a connection failed or closed. The demo
  retries temporary connection failures for a paired session. If failures persist,
  check your internet connection and whether a VPN, proxy, firewall, or antivirus
  is interfering; try another network to compare.
- **401 / logged out:** WhatsApp rejected the saved session. The library removes
  the rejected device from PostgreSQL, and the demo exits with a recovery
  message. Remove any stale entry from your phone's Linked devices and run the
  demo again to scan a fresh QR code. Repeated `401` responses require further
  investigation; network retries cannot restore a rejected session.
- **Stream replaced:** another process connected with the same session. Stop
  the other copy before restarting this demo.
- **PostgreSQL setup failed:** check `WHATSAPP_DATABASE_URL`, server availability,
  database permissions, credentials and TLS settings. SQLSTATE is shown when
  the database reports one. The demo never falls back to SQLite.

## Verify the setup

From the repository root:

```powershell
go build ./...
go test ./...
cd examples\login
go test ./...
go build .
```

The example has its own Go module, so its checks must run from this directory.
The offline tests check database persistence, reconnect handling, both message
directions, linked-device wrappers, private/text-only filtering, contact names,
phone mappings, history ordering, buffer limits, duplicate suppression, and chat
isolation. They do not connect to WhatsApp.

To also verify real PostgreSQL storage using a temporary, isolated cluster:

```powershell
# From the repository root; PostgreSQL 17 tools and Go must be installed.
./database/Test-Schema.ps1 -WithSessionTests
```

This runs library migrations through the actual Go session-store code, saves two
synthetic account identities/keys and different contacts, closes/reopens the
database, and checks explicit account selection and schema placement. The runner
stops and removes its temporary cluster; it does not use your normal database,
your real WhatsApp account, or `session.db`. SQLite remains a test-only dependency
for the existing offline fixtures.

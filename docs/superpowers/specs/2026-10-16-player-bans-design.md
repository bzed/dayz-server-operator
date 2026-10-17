<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Player bans and ban evidence: design

Status: approved in brainstorming on 2026-10-16, to be turned into an implementation plan.
Implements the ban part of FR-33 and the `bans` table of §C18 in `IMPLEMENTATION_PLAN.md`. It is
deliberately narrower: no country or ASN bans, no CIDR ranges, no join policies.

## Goals

- Ban a player by Steam ID (and the BattlEye GUID derived from it), and optionally by IP address,
  for a time span or forever, with a reason.
- List, edit and revoke ("unban") bans, with a history of every change.
- Global bans (every instance of this dzo installation) and instance bans (a chosen set).
- Store evidence (log lines, notes, images) per player, added by admins or automatically by a
  future cheat detector, and link it to bans.
- The web UI can send one ban to several dzo backends it is connected to.

## Non-goals

- Sharing one ban list between dzo installations. Each installation has its own database. The web
  UI fans a ban out as separate API calls, and each backend keeps its own copy.
- Automatic bans. The cheat detector only files evidence. Auto-bans are a later, separate decision.
- Country/ASN bans, CIDR ranges, the DayZ `ban.txt` (unless V4 below shows it works), and hard
  deletion of bans.

## Decisions

| # | Decision |
|---|---|
| B1 | "Global" means every instance of one dzo installation (one host). |
| B2 | SQLite (pure Go `modernc.org/sqlite`, no cgo) in `cfg.Paths.DB`, behind a store interface with portable SQL, so that PostgreSQL can follow (D26). Migrations are embedded numbered SQL files applied by a small runner (no goose). The new dependency, its Debian package and its licence are checked and justified in the commit that adds it. |
| B3 | `dzo serve` is the only writer. Web UI, CLI and the detector use the serve API (`apiclient`). One exception: the instance start path opens the database read-only to render `bans.txt`, so bans hold even when serve is down. |
| B4 | Enforcement: the BattlEye `bans.txt` rendered from the database, plus a live enforcer in serve (a persistent BattlEye session per running instance). The DayZ `ban.txt` only if V4 confirms its format. |
| B5 | Evidence belongs to a player (Steam ID, GUID or IP), and bans reference evidence items. Revoking a ban keeps its evidence. |
| B6 | A ban covers one person through several targets: the Steam ID, the derived GUID (added automatically), and optional IPs. IP-only bans are possible. IPs are never added automatically, because carrier-grade NAT puts many players behind one address. Single addresses only, no CIDR, so that what is stored is what `bans.txt` enforces. |
| B7 | Nothing about the native file formats is assumed. A verification phase against a real server comes first, and its captures become golden test data. |

## Verification phase (before any enforcement code)

The tools: a local DayZ server with BattlEye, the dayz-dev skill's headless client with BattlEye
(`PROTON_BATTLEYE_RUNTIME` set), `scripts/test-client.sh` and the `DZO_LIVE_BE_*` gated Go tests.
Run on stable, and on experimental when it is available.

- **V1, the `bans.txt` format (no client needed).** Over RCon: `addBan <guid> 0 r1`,
  `addBan <guid> 5 r2`, `addBan <ip> 0 r3`, and a reason with spaces, UTF-8, quotes and one over 255
  characters. Then `writeBans`, then read `profiles/battleye/bans.txt`. This establishes the line
  format, the expiry encoding (permanent, and how a timed ban's time is written), the comment syntax
  and the reason limits. The file and the `bans` output are kept as golden test data. The other
  direction: dzo renders a file, then `loadBans`, and `bans` lists exactly those entries with the
  right expiry. A one-minute ban is gone after it expires and `writeBans` runs. If `bans.txt` cannot
  express something the database can, the renderer leaves it out and the live enforcer covers it.
  This is documented.
- **V2, the GUID derivation (client).** The dzo-admin mod gives the player's Steam ID and BattlEye
  the GUID in the same session. Check that `md5("BE" + little-endian uint64(steam_id))` equals the
  GUID, and keep the pair as test data. If it does not match, Steam-ID bans use the GUIDs observed
  in `sightings` plus the live kick, and the design is revisited.
- **V3, enforcement end to end (client).**
  - (a) A ban in the rendered `bans.txt` before the start: the client is refused, and the message is
    recorded.
  - (b) A ban added while the player is online: kicked within seconds, with the ban message.
  - (c) An expired or revoked ban: the client joins.
  - (d) An IP ban on the client's local address: refused.
- **V4, the DayZ `ban.txt` (client).** Its location, the Steam64 ID with and without a trailing
  comment, and whether the server re-reads it at runtime. It is implemented only if this shows that
  it works. Otherwise it is documented as not supported.

The results replace the "not verified" lines in `docs/faq.rst`. The client checks become a `--bans`
stage of `scripts/test-client.sh`, and V1 a `DZO_LIVE_BE_*` gated Go test, so both can be re-run
against new DayZ builds.

## Data model

```
sightings      guid, ip, name, instance, first_seen, last_seen   (PK guid, ip, instance)
```

A Steam ID maps to its GUID by the derivation that V2 verifies, so sightings need no Steam ID column.

```
bans           id, reason (internal), message (shown to the player), created_by, created_at,
               expires_at (NULL = permanent), scope (global|instances),
               revoked_by, revoked_at, revoke_reason (NULL while not revoked)
ban_instances  ban_id, instance                          (when scope = instances)
ban_targets    ban_id, kind (steam|guid|ip), value, derived (GUID derived from the Steam ID)
ban_history    ban_id, at, actor, change (JSON: create, edit with old/new values, revoke)
evidence       id, steam_id NULL, guid NULL, ip NULL, instance, source (detector name or user),
               kind (log|note|image), body (text, capped), file_sha256 NULL, created_at
ban_evidence   ban_id, evidence_id
```

- A ban is active when it is not revoked and `expires_at` is NULL or in the future. Expired and
  revoked bans stay as history.
- Every create, edit and revoke writes `ban_history` in the same transaction and also an entry to the
  existing JSONL audit log (`admin.Audit`).
- Evidence files live under `<Paths.DB>/evidence/<first two hex digits>/<sha256>`, mode `0o600`,
  directories `0o750`. Only PNG, JPEG and WebP are accepted, recognised by magic bytes, with a size
  cap. They are served with an explicit `Content-Type` from the stored kind and
  `X-Content-Type-Options: nosniff`, never inline in HTML.
- Every evidence item needs at least one of `steam_id`, `guid` or `ip`. Values are validated: the
  Steam ID is 17 digits in the 7656119… range, the GUID 32 lowercase hex digits, the IP parsed with
  `net/netip` (IPv4 only if V1 shows that BattlEye takes no IPv6).

## Enforcement

### Rendering `bans.txt`

A pure function `Render(activeBans, instance, now) []byte`, tested against the V1 golden files:

- One line per applicable target: GUIDs, Steam IDs as their derived GUID, and IPs, in the format
  established by V1. Timed bans carry their expiry the way BattlEye stores it.
- The message is sanitised: no newlines or control characters, and capped to the length V1 found.
- A header comment (if V1 shows comments are allowed) says the file is generated by dzo.
- Written atomically with mode `0o600`.

When it is written:

1. At instance start, in the runfiles step (`internal/runfiles`), from a read-only database. Serve
   does not have to run.
2. By serve after every ban change, for every affected instance, followed by RCon `loadBans`.
3. By serve on a one-minute sweep, so that expired bans leave the file.

dzo records the SHA-256 of what it wrote in `profiles/battleye/.dzo-bans.sha256`. When `bans.txt` differs from that (hand edits, or another RCon tool's `addBan`), serve imports its active entries that are not banned yet as instance bans `created_by = import:bans.txt` and renders again. The start path never overwrites a file changed outside dzo, and leaves it to serve. Removing a ban has to go through dzo.

### The live enforcer

Serve keeps one `battleye.Session` (the existing reconnecting type) per running instance, with all
goroutines tied to serve's context and waited for on shutdown.

- Connect events give the IP, GUID events the GUID. Both are recorded in `sightings`.
- On a GUID or IP that has an active ban for this instance: `kick <slot> <message>`, recorded in the
  audit log.
- The Steam ID comes from the dzo-admin mod's player sync. It is linked to the GUID by slot and name
  of the same moment, and accepted only if `derive(steam_id) == guid` (if V2 holds).
- When a ban is created, every affected instance's online players are checked, and a banned one is
  kicked at once.
- RCon failures are retried by the session. While the session is down, the ban still applies through
  `bans.txt` at the next `loadBans` or start. The API and UI show that instance as "pending sync" until
  `loadBans` succeeded.

## API

All under `/api/v1`, JSON, with body and field size limits, and every change audited.

| Method and path | Permission |
|---|---|
| `GET /bans` (filters: state active/expired/revoked, instance, query by Steam ID, name, GUID, IP) | `ban.view` |
| `POST /bans` | `ban.instance` or `ban.global`, see below |
| `GET /bans/{id}`, `GET /bans/{id}/history` | `ban.view` |
| `PUT /bans/{id}` (the full ban: reason, message, expiry, scope, targets, evidence links) | the same rules as creating it |
| `POST /bans/{id}/revoke` (with a reason) | the ban's creator (if still allowed to create it), or admin |
| `GET /evidence`, `GET /evidence/{id}`, `GET /evidence/{id}/file` | `ban.view` |
| `POST /evidence` (JSON, or multipart for an image) | `evidence.add` |
| `GET /players/{steam_id}/identities` (with the number of other Steam IDs per IP) | `ban.view` |

Roles (`api.rolePerms`):

- moderator: `ban.view`, `ban.instance` with an expiry of at most 7 days, `evidence.add`
- operator: plus permanent bans and IP targets
- admin: plus `ban.global`, and revoking bans that others created
- detector (a new role for API tokens of automated sources): `evidence.add` only, ranked below viewer
  so that it sees nothing else

The rules are enforced server side. The UI only hides what the server would refuse.

## Web UI

- A ban list per backend: filter by state and instance, search.
- A ban form, also reachable as "Ban" from the player list, with:
  - Steam ID, reason, message, duration (presets plus a custom value, or permanent), scope
  - known IPs as opt-in checkboxes, each showing how many other Steam IDs were seen behind it
  - evidence to link
  - "also send to" for the other connected backends, where each backend gives its own result
- A ban detail page with history, linked evidence, and edit and revoke.
- Player evidence on the player page. Images go through `<img>` from the API.
- Everything that changes state carries the existing CSRF token. All text goes through
  `html/template`.

## CLI

`dzo ban add|list|show|edit|revoke` and `dzo evidence add`, all through `apiclient` against serve,
documented in `docs/cli.rst` with the CLI help.

## Error handling

- Invalid input (identifier formats, unknown instance, an expiry in the past, a body over its size)
  gets 400 with a precise message. Missing permission gets 403.
- A ban change commits to the database first. Rendering and `loadBans` then run per instance, and any
  that fail are reported in the response as `sync` per instance and retried by the sweep. A ban
  change never fails only because one instance is offline.
- The enforcer never kicks on a parse doubt: an unparsable event is logged and ignored.
- Secrets (the RCon password, tokens) never appear in errors or logs.

## Testing

- Store: table-driven tests on a temporary SQLite database for create, edit, revoke, expiry,
  scope resolution, history and evidence linking. The SQL stays portable, and the PostgreSQL CI comes
  with the PostgreSQL backend.
- Renderer and importer: against the V1 golden files, including a round trip (import → render
  gives the same entries) and the sanitising of messages.
- The GUID derivation: against the V2 pair, plus known Arma vectors.
- Enforcer: driven by a fake RCon command interface and a fake clock (no sleeps), for kick on
  connect, kick on a new ban, ignoring an expired ban, and recording identities.
- API and web: permission matrix, CSRF, size limits, upload type checks, and that "also send to"
  reports each backend's result.
- Live: the `--bans` stage of `scripts/test-client.sh` (V2–V4) and the gated V1 Go test.
- Coverage stays at or above 85% (`make check`).

## Order of work

1. The verification phase (V1, V2), with golden data checked in.
2. The store (SQLite, migrations, the store interface) with its tests.
3. The renderer, the importer and the start-path rendering.
4. The API and permissions, then the CLI.
5. The live enforcer, then V3 end to end.
6. The web UI.
7. V4 and, if it works, the DayZ `ban.txt`.
8. Docs: `docs/faq.rst`, a new moderation page, `docs/api.rst`, `docs/cli.rst`, and
   `debian/changelog` for the new dependency.

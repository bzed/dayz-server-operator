<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Vehicle last driver, cleanup of unused vehicles, database backup

Status: design, not yet planned. Date: 2026-11-01.

## Why

Two admin problems, both mostly on vanilla (unmodded) servers:

1. **"My car is gone."** A player loses track of their vehicle (a DayZ bug, a
   glitch, a desync, they forgot where they parked). The admin needs to find
   it: look the player up in the vehicle list and see where the vehicle is now.
   Nothing more: no history of movements, no proof of what happened, no restore.
2. **Orphaned vehicles.** Vehicles nobody has used for weeks take up the
   Central Economy's slots. The admin wants to find them and delete them so
   the economy spawns fresh ones.

Today the mod reports every vehicle every `vehicles_s` (60 s) with its
persistent ID, position, state and current occupants, but nothing remembers
who used a vehicle once it is empty.

## Goals

- For every vehicle that is on the map now: the **last driver** (name and
  Steam ID) and **when it was last driven**, shown in the vehicle list on the
  instance page and sortable.
- From the vehicle list, a **link that zooms the map to the vehicle**.
- The records must **survive DayZ glitches**: a vehicle that drops out of the
  reports and comes back (a server that came up without its storage, a late
  mod, a bug) gets its record back unchanged.
- A totally fresh set of vehicles (storage wiped, vehicles deleted outside
  dzo) must be handled without manual cleanup of dzo's data.
- A **bulk cleanup**: delete vehicles not driven for longer than a threshold,
  as a separate step with a preview list and an extra confirmation.
- The database dzo keeps this in (`<paths.db>/dzo.sqlite`) is **backed up**.
  Today it is not (`docs/backups.rst`: "the backup of the database, which does
  not exist yet"); this also covers the bans, players and accounts already in
  it.

## Non-goals

- No history of vehicle positions, drivers or deletions. Only vehicles that
  are currently on the map are shown.
- No restoring of lost vehicles, no proof of what happened to one.
- **Nothing is changed on the vehicles in the game.** No new persistent
  variables on vehicles (`OnStoreSave`/`OnStoreLoad`), no lifetime changes,
  no despawn protection. The records live only in dzo.
- Passengers, opening the trunk, attaching parts and other interactions do
  not count as "use". **Use = taking the driver's seat.**
- No automatic (scheduled) deletion of unused vehicles. Every cleanup is
  started and confirmed by a person.

## Anti-goals (would make it a failure even if it works)

- A DayZ glitch that briefly reports no vehicles (or a different set) wipes
  dzo's records.
- The cleanup deletes a vehicle someone is sitting in, or deletes without the
  admin having seen the exact list.
- Removing the dzo-admin mod later breaks a server's storage. This is why
  nothing is written into the vehicles' own save data.
- A freshly installed dzo offers to delete every vehicle because none has a
  "last driven" yet.

## Current state (what exists)

- Mod: `servermods/dzo-admin`. `DZOAdminRegistry.s_Cars` (Hooks.c) holds every
  `CarScript`; `DZOAdminBikes` (Motorbike.c, `modded class MotorbikeScript`)
  and `DZOAdminBoats` (Boat.c, `modded class BoatScript`) do the same for the
  1.30 motorbikes and boats. `DZOAdminWorld.CollectVehicles` (World.c) builds
  the `vehicles` part of the snapshot; `VehicleId` is `"v" + GetPersistentID`
  (stable across restarts for persisted entities).
- Protocol: `DZOAdminVehicle` (Proto.c) / `admin.Vehicle`
  (`internal/admin/proto.go`). The snapshot says which parts it carries
  (`has`); a snapshot without `vehicles` leaves the vehicle list as it was.
- Hub: `internal/admin/hub.go`. Bicycles and helicopters are cars reclassified
  by the hub by class-name glob (`vehiclekinds.go`).
- Vehicle actions: `vehicle_delete` (with `force` for crewed vehicles) and the
  others, gated by permissions, logged to the audit log
  (`<paths.db>/audit.jsonl`).
- Web: the vehicle table is the `vehicles` fragment in
  `internal/web/templates/fragments.html`, polled every 15 s; the row panel
  has the per-vehicle Delete.
- Map: `/map?x=<east>&z=<north>&label=<text>` marks a place and zooms to it
  (`focusFromLink` in `internal/web/static/map.js`); the events page uses it.
- Database: one SQLite file `<paths.db>/dzo.sqlite` (modernc.org/sqlite),
  migrations in `internal/moderation/migrations/sqlite/`, periodic `VACUUM`
  (`serve.vacuum_days`).
- Backups: btrfs snapshots of each instance's subvolume
  (`internal/backup`), with reasons (`scheduled`, `manual`, ...), retention,
  pinning and `post_backup` hooks for offsite copies. `paths.db` is not an
  instance and is not snapshotted.

## Design

### 1. Recording the last driver (mod)

The mod hooks `OnDriverEnter(Human player)` on `CarScript`,
`MotorbikeScript` and `BoatScript` (declared on `Transport`,
`3_game/vehicles/transport.c`; check each override with the API index, as
AGENTS.md requires). On the server it remembers, **in memory only**, for that
vehicle: the driver's Steam ID (`GetIdentity().GetPlainId()`), their name, and
the time (`GetGame().GetTime()` or equivalent monotonic server time).

The vehicle snapshot carries it as new optional fields of `DZOAdminVehicle`:

- `driver`: Steam ID of the last driver since this server started, empty if
  none.
- `driver_name`: their name at that moment.
- `driven_ago`: seconds since they took the seat (the mod has no wall clock
  dzo can trust; dzo computes the time as `received - driven_ago`).

A driver who is in the seat when the snapshot is taken counts as driving
*now* (`driven_ago = 0`), so a long drive keeps the time current. Short drives
between two snapshots are still caught because the value stays in memory
until the next snapshot. Only a server crash within one `vehicles_s`
interval of a drive loses that one entry. This is accepted.

An older mod sends none of these fields; dzo then records nothing for that
instance (no fallback to the occupant list, since that does not say who
drove).

This is a change in `servermods/dzo-admin` and the mod protocol: `make
test-client` (stable and `ARGS=--exp`) must pass and be named in the commit
message. Only rebuild the PBO with this code change.

### 2. Keeping the records (dzo)

New table in `dzo.sqlite` (a new migration), one row per vehicle:

| column | meaning |
|---|---|
| instance | instance name |
| vehicle_id | the mod's id (`v<b1>.<b2>.<b3>.<b4>`) |
| type | class name (informational, and a guard, see below) |
| driver_steam_id, driver_name | last driver, empty if never seen driven |
| driven_at | wall-clock time of the last drive, null if never |
| first_seen | when dzo first saw this vehicle |
| last_seen | when it was last in a snapshot |
| hidden_since | null while the vehicle is on the map; else when it went missing |

Primary key `(instance, vehicle_id)`.

On every snapshot that carries `vehicles` (`has["vehicles"]`):

- Each reported vehicle is upserted: `last_seen = now`, `hidden_since =
  null`, `first_seen` set on insert. When the snapshot has a driver whose
  computed time is newer than `driven_at`, driver and `driven_at` are
  replaced. A vehicle that comes back after being hidden gets its old record
  back unchanged.
- Each record of that instance not in the snapshot and not yet hidden gets
  `hidden_since = now`. Nothing is deleted.
- Expansion covered vehicles (`covered`) keep their record but are not
  driveable while covered; they are upserted like any other.
- If a reported vehicle's `type` differs from the stored one under the same
  id, the id was reused by a different vehicle: the record is reset (new
  `first_seen`, no driver). Builder: confirm whether persistent IDs can be
  reused at all; if they cannot, this guard is cheap insurance.

A snapshot without `vehicles`, a mod that is not connected, or an instance
that is stopped changes nothing: records are only hidden by a snapshot that
says it lists the vehicles.

**Garbage collection.** A record hidden for longer than
`vehicles.forget_days` (default 7) is deleted. It runs with the existing
periodic database maintenance of `dzo serve`. A totally fresh set of vehicles
therefore needs no special case: the old records are hidden at the first
snapshot and gone after the retention; the new vehicles start with fresh
records.

**What the UI shows** is only the vehicles of the current snapshot, joined
with their records. Hidden records are never shown.

### 3. Vehicle list (web)

In the `vehicles` fragment:

- **Last driver** column: the name, linking to the player's page in the
  player database when there is one; the Steam ID on hover. Empty when the
  vehicle has not been seen driven.
- **Last driven** column: relative time ("3 d ago") with the exact time on
  hover; sortable by the underlying time. A vehicle without a drive shows
  "not since <first_seen date>" and sorts by `first_seen`.
- The **Position** cell becomes a link to
  `/map?x=<x>&z=<z>&label=<type> (owner <owner>, last driver <last driver>)`
  (each part only when known; owner and last driver are different things), using the existing
  `focusFromLink`. It points where the vehicle was at the last snapshot,
  which is enough for parked vehicles. No "follow this vehicle" mode.

The same two fields go into the API's vehicle list (`docs/api.rst`), so the
CLI and other clients get them.

### 4. Cleanup of unused vehicles

A **Clean up** button above the vehicle list, shown only to people with the
vehicle delete permission. It opens a separate step:

1. **Choose a threshold:** 2 weeks, 1 month, 2 months, or a custom number of
   days (bounded: at least 1, at most, say, 3650).
2. **Preview:** the list of vehicles that match, with class, kind, position
   (map link), last driver, last driven, health. A vehicle matches when its
   *age* is over the threshold, where age = now - `driven_at`, or now -
   `first_seen` for a vehicle never seen driven. Vehicles with anyone inside
   are never listed. Covered (Expansion) vehicles are not listed (they cannot
   be deleted through the existing action). The preview says how many will go
   and, when dzo's tracking for this instance started less than the threshold
   ago, that no vehicle can be old enough yet.
3. **Confirm:** a second, explicit confirmation naming the count ("Delete 14
   vehicles"). The request carries the exact list of ids from the preview, not
   the threshold, so a vehicle that becomes used in between is not deleted by
   surprise: the server re-checks each id (still present, still uncrewed,
   still older than the threshold) and skips the ones that are not.
4. Each delete goes through the existing `vehicle_delete` action without
   `force`, with the same permission and one audit log entry per vehicle (or
   one entry listing them; builder's call, but every id must be in the log).
   The result page says how many were deleted and which were skipped, and
   why.

Because age falls back to `first_seen`, a freshly installed dzo (or a fresh
set of vehicles) cannot delete anything until the threshold has passed since
it started tracking.

### 5. Database backup

`dzo.sqlite` gets a regular, consistent copy that the btrfs snapshots and the
offsite `post_backup` hooks can pick up:

- `dzo serve` writes `VACUUM INTO` copies to `<paths.db>/backup/dzo-<UTC
  time>.sqlite` (mode `0600`, written to a temporary name and renamed), every
  `serve.db_backup_hours` (default 24), keeping the newest
  `serve.db_backup_keep` (default 7). `VACUUM INTO` gives a consistent copy of
  a live WAL database without stopping anything.
- The `post_backup` hooks get these copies too, so an offsite copy exists.
  Builder: decide how the hook learns about them (a separate hook call with
  the copy's path, or a documented path to include).
- Whether `paths.db` should additionally be a btrfs subvolume snapshotted by
  `internal/backup` is left open: the copies above already make the data
  snapshot-safe wherever `paths.db` lives. If it is added, it must reuse the
  existing manager (index, retention, pinning), not a second mechanism.
- `docs/backups.rst` drops the "does not exist yet" line and documents the
  copies and how to restore from one (stop `dzo serve`, replace
  `dzo.sqlite`, remove `-wal`/`-shm`, start). `docs/moderation.rst` points
  there.

This part is independent of the vehicle work and can be its own commit (or PR)
first.

## Configuration (new options)

Each must be documented in the same commit, in the example and in prose
(AGENTS.md "Documenting configuration"):

- `vehicles.forget_days` (`site.yaml`, overridable in `instance.yaml`),
  default 7: days a vehicle's record is kept after the vehicle stopped being
  reported. Integer >= 1. Takes effect at the next maintenance run.
  Builder: pick the exact file and section that fits the existing layout;
  `serve:` in `config.yaml` is acceptable if per-instance does not fit.
- `serve.db_backup_hours`, default 24; 0 disables. `serve.db_backup_keep`,
  default 7, >= 1.
- The cleanup thresholds are UI choices, not configuration.

## Decided by the user

- Use = last driver only.
- Show only vehicles that are on the map now; no vehicle history.
- Keep dzo's records through glitches with a hide/unhide and a garbage
  collection; never change the vehicles in the game.
- Handle a totally fresh set of vehicles.
- Bulk delete by last-used age (presets weeks to months, or custom), as an
  extra step with its own preview list and an extra confirmation.
- `forget_days` defaults to 7: a vehicle missing for over a week (a server down
  that long, a storage restored late) comes back with a fresh record.
- The database must be in the backups, by snapshot or by a regular dump that
  can be snapshotted.

## Calls made in this design (check these)

- Last driver kept in the mod's memory and sent with the snapshot
  (`driver`, `driver_name`, `driven_ago`), not as a separate event message.
- Records in `dzo.sqlite`, keyed by instance and persistent vehicle id.
- "Never driven" vehicles age from `first_seen`, so nothing is deletable right
  after install.
- Cleanup never uses `force`, never lists crewed or covered vehicles,
  re-checks each id at confirm time.
- Map link reuses `?x=&z=&label=`; no live follow.
- Database backup by periodic `VACUUM INTO` copies, 24 h / keep 7; a
  `paths.db` btrfs snapshot left open.

## Left to the builder

- Package layout (a new `internal/vehicles` store on the shared database, or
  inside an existing package), and how the hub hands snapshots to it.
- Exact column names, indexes, migration number.
- Web routes and templates for the cleanup step, following the existing
  htmx and CSRF patterns; the API endpoints behind them.
- Whether the audit log gets one entry per vehicle or one per cleanup (all
  ids must be in it).
- Tests: the store (upsert, hide, unhide, type change, GC with a fake clock),
  the snapshot handling with and without `vehicles`, an older mod without the
  new fields, the cleanup selection and the confirm-time re-check, e2e for
  the list columns, the map link and the cleanup flow; a mod test-client run.
  Coverage stays at or above 85%.

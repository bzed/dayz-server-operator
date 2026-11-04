<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Vehicle Last Driver, Cleanup and Database Backup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the last driver and last drive time of every vehicle in the vehicle list, link each vehicle to the map, offer a previewed bulk cleanup of long-unused vehicles, and back up `dzo.sqlite` regularly.

**Architecture:** The dzo-admin mod remembers each vehicle's last driver in memory and sends it with the vehicle snapshot. A recorder in `dzo serve` (like `internal/presence`) keeps one row per vehicle in `dzo.sqlite`, hiding rows of vehicles missing from a snapshot and forgetting them after `admin.vehicle_forget_days`. The API merges those rows into its vehicle list and offers the cleanup; the web UI renders both. The moderation service writes `VACUUM INTO` copies of the database next to it.

**Tech Stack:** Go (net/http, database/sql with modernc.org/sqlite, html/template, htmx), Enforce Script (DayZ 1.30), Sphinx docs, chromedp e2e.

**Spec:** `docs/superpowers/specs/2026-11-01-vehicle-last-driver-design.md`

## Global Constraints

- Nothing on the vehicles in the game changes: no `OnStoreSave`/`OnStoreLoad`, no lifetime calls. Only the mod's memory and dzo's database.
- Use = taking the driver's seat. Passengers do not count.
- Only vehicles in the current snapshot are shown; hidden rows never are.
- `admin.vehicle_forget_days` default **7**, integer >= 1.
- `serve.db_backup_hours` default **24**, 0 = off, negative refused; `serve.db_backup_keep` default **7**, >= 1.
- Cleanup thresholds: presets **14, 30, 60 days** and custom **1..3650** days; at most **500** ids per cleanup request.
- Cleanup never uses `force`, never lists or deletes vehicles with occupants or covered (Expansion) vehicles, and re-checks every id at confirm time.
- Never-driven vehicles age from `first_seen`.
- Every new option documented in the same commit, in the YAML example of `docs/configuration.rst` with its default and in prose (AGENTS.md "Documenting configuration"). `make docs` passes.
- Mod changes: load the `dayz-dev` skill first, look up every class/method with its API index, run `make test-client` (stable and `ARGS=--exp`) and name it in the commit message. Rebuild the PBO only in the mod task.
- Times in SQLite are unix seconds (the existing convention). Files `0o600`, directories `0o750`.
- Tests write only under `t.TempDir()`, no `time.Sleep`; fake clocks and `require.Eventually`. Coverage stays >= 85%.
- `make check` passes before every push. Commit prefixes as in `git log` (`admin:`, `web:`, `api:`, `docs:`, `serve:`, ...); end commit messages with the Co-Authored-By line from the session.

## Review Focus

1. **An old mod without driver data:** every vehicle would look never driven, and a cleanup would offer vehicles people use. Expected: the cleanup refuses with a clear message on an instance whose mod hello lacks the `driver` feature. Test in Task 6.
2. **A snapshot with no (or few) vehicles right after a restart or a storage problem:** expected: rows are hidden, not deleted, and come back with their driver when the vehicles do. Tests in Task 3 and Task 4.
3. **Bad values from the mod:** negative, NaN or huge `driven_ago`, an invalid Steam ID, long names or ids. Expected: the drive is ignored (the vehicle is still recorded), names clipped. Tests in Task 2 and Task 3.
4. **A vehicle used or boarded between preview and confirm:** expected: it is skipped with a reason, the others are deleted. Test in Task 6.
5. **A drive time older than the stored one** (the mod restarted and reports an earlier drive, or snapshots arrive out of order): expected: `driven_at` never moves backwards. Test in Task 3.

---

### Task 1: Regular copies of the database

**Files:**
- Create: `internal/moderation/backup.go`, `internal/moderation/backup_test.go`
- Modify: `internal/moderation/service.go` (`maintain`, new fields), `internal/config/config.go` (Serve fields, methods, validation near line 406), `internal/config/config_test.go`, `internal/serve/serve.go` (wire the fields), `internal/backup/instance.go` (post_backup Extra), `internal/backup/instance_test.go`
- Docs: `docs/configuration.rst` (`serve:` example + prose), `docs/backups.rst` (replace the "does not exist yet" sentence, add a restore section), `docs/moderation.rst` (lines ~148 and ~218 point to backups)

**Interfaces:**
- Produces: `func (s *Store) BackupTo(ctx context.Context, dir string, now time.Time, keep int) (string, error)`; `func LatestBackup(dir string) (string, error)` (empty string, nil error when there is none); `Service.BackupDir string`, `Service.BackupEvery time.Duration`, `Service.BackupKeep int`; `config.Serve.DBBackupHours *int`, `DBBackupKeep *int`, `func (s Serve) DBBackupEvery() time.Duration`, `func (s Serve) DBBackupCount() int`; `serve.DBBackupDir(cfg) string` = `<paths.db>/backup`.

- [ ] **Step 1: Write the failing tests** in `internal/moderation/backup_test.go`:
  - `TestBackupToWritesAConsistentCopy`: open a store, create a ban, `BackupTo(ctx, dir, now, 7)`; the returned path is `dir/dzo-20261101T120000Z.sqlite` for `now = 2026-11-01 12:00:00 UTC`, mode `0o600`, dir mode `0o750`, and `Open` on the copy finds the ban.
  - `TestBackupToKeepsTheNewest`: nine calls an hour apart with `keep = 7` leave exactly the seven newest `dzo-*.sqlite`; an unrelated file in the dir is untouched; a leftover `*.tmp` is removed.
  - `TestLatestBackup`: none → `""`; several → the newest by name.
  - `TestServiceBacksUpWhenDue` (service test, fake `Tick` and `Now`): with `BackupEvery = 24h`, a sweep writes a copy, a sweep one hour later does not, one 24 h later does; `BackupEvery = 0` never writes one. Due state is the `meta` key `db_backup_at`, so a restart does not copy again early.
- [ ] **Step 2: Run** `go test ./internal/moderation -run 'Backup|LatestBackup' -v`. Expected: FAIL (undefined `BackupTo`).
- [ ] **Step 3: Implement** `BackupTo`: `MkdirAll(dir, 0o750)`, remove a stale `<name>.tmp`, `VACUUM INTO ?` the tmp path (a query parameter, never string building), `chmod 0o600`, rename to the final name, then delete the oldest `dzo-*.sqlite` beyond `keep`. Names use `now.UTC().Format("20060102T150405Z")`. In `maintain`, after the vacuum block, check `db_backup_at` like `VacuumDue`/`MarkVacuumed` and call `BackupTo`; log failures with `logf`, never stop the service.
- [ ] **Step 4: Run** the tests again. Expected: PASS.
- [ ] **Step 5: Config.** Test first in `config_test.go`: unset → `DBBackupEvery() == 24h`, `DBBackupCount() == 7`; `db_backup_hours: 0` → 0 (off); `db_backup_hours: -1` → error `serve.db_backup_hours: must be 0 (off) or a number of hours`; `db_backup_keep: 0` → error `serve.db_backup_keep: must be at least 1`. Implement, wire in `serve.Run` (`svc.BackupDir, svc.BackupEvery, svc.BackupKeep`).
- [ ] **Step 6: post_backup.** Test first in `internal/backup/instance_test.go`: with a copy in `<paths.db>/backup`, the post_backup hook's stdin JSON has `extra.db_backup` = that path; without one the key is absent. Implement in `For` with `moderation.LatestBackup` (if importing moderation from backup creates a cycle, move `LatestBackup` to `internal/backup` and call it from there).
- [ ] **Step 7: Docs.** `configuration.rst`: both keys in the `serve:` example (`db_backup_hours: 24  # default; 0 = off`, `db_backup_keep: 7  # default`) and prose: what they do, values, errors, that the copy is consistent while serve runs, takes effect on serve restart. `backups.rst`: a "The database" section: where the copies are, that `post_backup` hooks get `extra.db_backup`, and restoring (stop `dzo serve`, copy a backup over `dzo.sqlite`, delete `dzo.sqlite-wal` and `-shm`, start). Run `make docs`.
- [ ] **Step 8: Run** `make fmt-check vet lint test-race`. Expected: PASS.
- [ ] **Step 9: Commit** `serve: regular consistent copies of the database, for snapshots and post_backup hooks`.

---

### Task 2: Driver fields in the protocol and the hub

**Files:**
- Modify: `internal/admin/proto.go` (`Vehicle`), `internal/admin/hub.go` (`Sync`), `internal/admin/hub_test.go`, `internal/admin/proto_test.go` (or the existing decode test file)

**Interfaces:**
- Produces on `admin.Vehicle`:
  - from the mod: `Driver string \`json:"driver,omitempty"\``, `DriverName string \`json:"driver_name,omitempty"\``, `DrivenAgo float64 \`json:"driven_ago,omitempty"\``
  - set by the hub: `DrivenAt time.Time \`json:"-"\`` (zero when no valid drive)
  - filled by the API (Task 5): `LastDriver string \`json:"last_driver,omitempty"\``, `LastDriverName string \`json:"last_driver_name,omitempty"\``, `LastDriven *time.Time \`json:"last_driven,omitempty"\``, `FirstSeen *time.Time \`json:"first_seen,omitempty"\``
- Produces: feature name constant `admin.FeatureDriver = "driver"` (the mod's hello lists it; checked with `Hello.Has`).

- [ ] **Step 1: Write the failing tests** in `hub_test.go` with the hub's fake `now`:
  - `TestSyncSetsDrivenAt`: a vehicle with `Driver: "76561198000000001", DrivenAgo: 90` at `now` gets `DrivenAt == now - 90s`.
  - `TestSyncIgnoresBadDrives`: `DrivenAgo` of `-1`, `NaN`, `+Inf`, and `> 400 days`, and `Driver` that is not a valid Steam ID → `DrivenAt` zero, `Driver`/`DriverName` cleared, vehicle still listed. `moderation` imports `admin` (the audit log), so `admin` cannot import it: add an unexported check in `admin` with the same rule as `moderation.ValidSteamID`, and a test in `moderation` that the two agree on a table of ids.
  - `TestVehicleJSONHidesDrivenAt`: marshalled `Vehicle` has no `DrivenAt` key and omits empty driver fields.
- [ ] **Step 2: Run** `go test ./internal/admin -run 'DrivenAt|BadDrives|HidesDrivenAt' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** in the `req.Vehicles != nil` block of `Sync`, next to the kind loop. Clip `DriverName` to the length the player list already uses.
- [ ] **Step 4: Run** the tests. Expected: PASS. Run `go test ./internal/admin/...` (nothing else changed).
- [ ] **Step 5: Commit** `admin: the last driver of a vehicle in the protocol`.

---

### Task 3: The vehicle records table

**Files:**
- Create: `internal/moderation/migrations/sqlite/0005_vehicles.sql`, `internal/moderation/vehicles.go`, `internal/moderation/vehicles_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  ```go
  type VehicleSeen struct {
      ID, Type, Driver, DriverName string
      DrivenAt time.Time // zero: no drive reported
  }
  type VehicleRecord struct {
      ID, Type, Driver, DriverName string
      DrivenAt, FirstSeen, LastSeen time.Time // DrivenAt zero: never seen driven
  }
  func (r VehicleRecord) LastUse() time.Time // DrivenAt, or FirstSeen when never driven
  func (s *Store) SyncVehicles(ctx context.Context, instance string, seen []VehicleSeen, now time.Time) error
  func (s *Store) VehicleRecords(ctx context.Context, instance string) (map[string]VehicleRecord, error) // visible rows only
  func (s *Store) ForgetVehicles(ctx context.Context, instance string, cutoff time.Time) (int64, error)  // deletes rows hidden before cutoff
  ```
- Table `vehicles` (instance, vehicle_id, type, driver_steam_id, driver_name, driven_at NULL, first_seen, last_seen, hidden_since NULL), primary key `(instance, vehicle_id)`, index on `(instance, hidden_since)`. Header comment and SPDX as in `0004_accounts.sql`.

- [ ] **Step 1: Write the failing tests** (`vehicles_test.go`, store in `t.TempDir()`):
  - `TestSyncVehiclesRecordsAndKeepsTheDriver`: sync A with driver at t1, then A without driver at t2 → record keeps driver and `DrivenAt == t1`, `LastSeen == t2`, `FirstSeen == t1`.
  - `TestSyncVehiclesNeverGoesBack`: stored `DrivenAt = t2`; a sync reporting a drive at `t1 < t2` by someone else → unchanged.
  - `TestSyncVehiclesHidesAndRestores`: sync {A,B}, then {} (an empty, non-nil slice), `VehicleRecords` is empty; sync {A} → A comes back with its old driver and `FirstSeen`; B stays hidden.
  - `TestSyncVehiclesTypeChangeResets`: A as `Offroad_02` with a driver, then A as `Hatchback_02` → no driver, `FirstSeen` = the new time.
  - `TestSyncVehiclesIsPerInstance`: the same id on two instances are two records; a sync of one never hides the other's.
  - `TestSyncVehiclesClipsAndRejects`: an id longer than 64 bytes is skipped; an invalid Steam ID records the vehicle but no driver; a 500-byte name is clipped like player names (`clip(name, maxName)`).
  - `TestForgetVehicles`: rows hidden before the cutoff are deleted, visible rows and rows hidden after it stay; the count is returned.
  - `TestLastUse`: never driven → `FirstSeen`; driven → `DrivenAt`.
- [ ] **Step 2: Run** `go test ./internal/moderation -run 'Vehicles|LastUse' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** `SyncVehicles` in one `s.tx`: upsert each seen vehicle (`ON CONFLICT ... DO UPDATE`, `driven_at = MAX`-style, only when the new drive is newer; reset when `type` differs; `hidden_since = NULL`), then `UPDATE vehicles SET hidden_since = ? WHERE instance = ? AND hidden_since IS NULL AND vehicle_id NOT IN (...)`. Use a temp table or chunked parameter lists for the NOT IN (snapshots can hold thousands of vehicles; stay under SQLite's parameter limit). All values as query parameters.
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Commit** `moderation: a record of each vehicle's last driver, kept through missing snapshots`.

---

### Task 4: The vehicle recorder and `admin.vehicle_forget_days`

**Files:**
- Create: `internal/vehicles/vehicles.go`, `internal/vehicles/vehicles_test.go`, `internal/vehicles/main_test.go` (copy the goleak/TestMain pattern of `internal/presence/main_test.go`)
- Modify: `internal/site/schema.go` (`AdminConfig.VehicleForgetDays *int \`yaml:"vehicle_forget_days,omitempty"\``, `Validate`), `internal/site/schema_test.go`, `internal/serve/serve.go` (start, reload, stop like `presence.Recorder`), `internal/serve/serve_test.go`
- Docs: `docs/configuration.rst` (`admin:` example in `instance.yaml` near line 587, prose near line 733; say whether it is a site default the same way the other `admin.*` keys are), `docs/admin-map.rst` or the page that describes the vehicle list (one paragraph on how records are kept)

**Interfaces:**
- Consumes: `admin.Vehicle.DrivenAt`, `Driver`, `DriverName` (Task 2); `Store.SyncVehicles`, `Store.ForgetVehicles` (Task 3).
- Produces:
  ```go
  type Store interface {
      SyncVehicles(ctx context.Context, instance string, seen []moderation.VehicleSeen, now time.Time) error
      ForgetVehicles(ctx context.Context, instance string, cutoff time.Time) (int64, error)
  }
  type Source interface {
      Vehicles() []admin.Vehicle
      Subscribe(ctx context.Context) <-chan admin.StreamEvent
  }
  type Recorder struct {
      Store  Store
      Now    func() time.Time
      Tick   <-chan time.Time // the forget sweep (default hourly)
      Logf   func(string, ...any)
      Forget func(instance string) time.Duration // from admin.vehicle_forget_days
  }
  func (r *Recorder) Set(ctx context.Context, hubs func(string) (Source, bool), instances []string)
  func (r *Recorder) Wait()
  func FromRegistry(reg *admin.Registry) func(string) (Source, bool)
  func ForgetAfter(a site.AdminConfig) time.Duration // unset: 7 days
  ```

- [ ] **Step 1: Write the failing tests** (fake source with a channel, fake store recording calls):
  - `TestRecorderSyncsEachVehiclesEvent`: a `vehicles` event with two vehicles → one `SyncVehicles` with both, `DrivenAt` passed through.
  - `TestRecorderIgnoresOtherEvents`: `players`/`status` events → no sync.
  - `TestRecorderDoesNotHideOnStart`: a source whose `Vehicles()` is nil at start → no sync until the first event (no info is not "no vehicles").
  - `TestRecorderForgets`: a tick → `ForgetVehicles(instance, now - Forget(instance))`.
  - `TestRecorderStopsWithContext`: `Set` with a cancelled context, `Wait` returns; goroutines do not leak.
  - `TestForgetAfter` (or in `schema_test.go`): unset → `7*24h`; `vehicle_forget_days: 0` → `Validate` error `admin.vehicle_forget_days must be at least 1`.
  - In `serve_test.go`: a mod snapshot through the mod listener ends up as a record in the store (`require.Eventually`).
- [ ] **Step 2: Run** `go test ./internal/vehicles ./internal/site -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the recorder by following `internal/presence/presence.go` (per-instance goroutine, `Set` diffing instances, `Wait`). In `serve.Run` create it after the store opens, `Set` it after `AddHubs` and again on reload, stop it before the store closes. `Forget` looks the instance up in the resolved site tree (rebuild the lookup on reload).
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Docs** for `admin.vehicle_forget_days`: example `vehicle_forget_days: 7  # default; days a vehicle's record is kept after it stopped being reported`, prose: what it is for, values, the error, that a vehicle back after more than this starts a new record (no driver, new age), takes effect on the next `dzo serve` reload. Run `make docs`.
- [ ] **Step 6: Commit** `serve: record the vehicles of every instance and forget the ones gone for admin.vehicle_forget_days`.

---

### Task 5: Last driver in the API vehicle list

**Files:**
- Modify: `internal/api/server.go` (the `GET .../vehicles` route at line 219; a `Vehicles VehicleRecords` field on `Server`), `internal/api/server_test.go` (or the vehicle API test file), `internal/serve/serve.go` (`apiSrv.Vehicles = store`)
- Docs: `docs/api.rst` (the vehicle list fields)

**Interfaces:**
- Consumes: `Store.VehicleRecords`, `VehicleRecord` (Task 3); the API fields on `admin.Vehicle` (Task 2).
- Produces:
  ```go
  type VehicleRecords interface {
      VehicleRecords(ctx context.Context, instance string) (map[string]moderation.VehicleRecord, error)
  }
  func (s *Server) vehiclesWithRecords(ctx context.Context, hub *admin.Hub) ([]admin.Vehicle, error)
  ```
  `vehiclesWithRecords` fills `LastDriver`, `LastDriverName`, `LastDriven` (nil when never driven) and `FirstSeen` from the record of each live vehicle. With `Vehicles` nil (no database) it returns the hub's list unchanged.

- [ ] **Step 1: Write the failing test** `TestVehicleListHasTheLastDriver`: a hub with two vehicles, records for one with a driver; `GET /api/v1/instances/alpha/vehicles` returns `last_driver`, `last_driver_name`, `last_driven`, `first_seen` for it, and only `first_seen` for the other. `TestVehicleListWithoutRecords`: `Server.Vehicles` nil → the same JSON as before.
- [ ] **Step 2: Run** `go test ./internal/api -run VehicleList -v`. Expected: FAIL.
- [ ] **Step 3: Implement** and use `vehiclesWithRecords` in the route. Leave the stream (`send("vehicles", ...)` near line 532) unchanged.
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Docs** in `api.rst`: the four new fields, that they come from dzo's records and not the mod. `make docs`.
- [ ] **Step 6: Commit** `api: the last driver and last drive of each vehicle in the vehicle list`.

---

### Task 6: The cleanup API

**Files:**
- Create: `internal/api/cleanup.go`, `internal/api/cleanup_test.go`
- Modify: `internal/api/server.go` (two routes next to line 249), `internal/apiclient/client.go` (+ test), `docs/api.rst`

**Interfaces:**
- Consumes: `vehiclesWithRecords` (Task 5), `admin.FeatureDriver` (Task 2), `admin.KindVehicleDelete`, `Hub.Enqueue`, `Audit.Log`.
- Produces:
  ```go
  // GET  /api/v1/instances/{instance}/vehicles/unused?days=N   (PermDelete)
  type UnusedVehicles struct {
      Days     int             `json:"days"`
      Since    *time.Time      `json:"tracked_since,omitempty"` // the oldest first_seen of a live vehicle
      Vehicles []admin.Vehicle `json:"vehicles"`
  }
  // POST /api/v1/instances/{instance}/vehicles/cleanup         (PermDelete)
  type CleanupRequest struct {
      Days int      `json:"days"`
      IDs  []string `json:"ids"`
  }
  type CleanupResult struct {
      Deleted []string         `json:"deleted"`
      Skipped []CleanupSkipped `json:"skipped"`
  }
  type CleanupSkipped struct{ ID, Reason string } // json "id", "reason"
  func unusedOf(vs []admin.Vehicle, days int, now time.Time) []admin.Vehicle
  // apiclient
  func (c *Client) UnusedVehicles(ctx context.Context, instance string, days int) (api.UnusedVehicles, error)
  func (c *Client) CleanupVehicles(ctx context.Context, instance string, days int, ids []string) (api.CleanupResult, error)
  ```
  A vehicle is unused when it has no occupants, is not covered, and `now - LastUse > days` (LastUse = `LastDriven`, else `FirstSeen`; a vehicle without `FirstSeen` is never unused). Sorted oldest first.

- [ ] **Step 1: Write the failing tests** (`cleanup_test.go`, fake clock, a hub synced with a hello that has `features: ["driver"]`):
  - `TestUnusedListsOnlyOldEmptyVehicles`: vehicles driven 40 d ago, 10 d ago, never driven but first seen 40 d ago, 40 d old with an occupant, 40 d old covered; `days=30` → the first and third, oldest first.
  - `TestUnusedRefusesBadDays`: `days` missing, `0`, `3651`, `abc` → 400 `days must be a whole number from 1 to 3650`.
  - `TestUnusedRefusesAnOldMod` (Review Focus 1): hello without `driver` → 409 `the dzo-admin mod of this instance does not report drivers; update it to 0.8.0`. Same for `POST cleanup`.
  - `TestCleanupDeletesAndSkips` (Review Focus 4): ids {old, now-occupied, driven-since, unknown}; result deletes `old` (one `vehicle_delete` command queued, `Force` false) and skips the others with reasons `crew inside`, `used since the preview`, `not on the map`.
  - `TestCleanupAudit`: each deleted id has an audit entry `Action: "vehicle_delete"`, `Target: <id>`, `Detail: "cleanup: not driven for 30 days"`; each skipped one an entry with `OK: false` and the reason.
  - `TestCleanupLimits`: 501 ids → 400 `at most 500 vehicles per cleanup`; an empty list → 400; viewer token → 403; offline mod → the existing offline status.
  - apiclient tests for both methods against an `httptest` server.
- [ ] **Step 2: Run** `go test ./internal/api ./internal/apiclient -run 'Unused|Cleanup' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** One `s.actions.Allow` check per cleanup request (not per vehicle; the per-target limiter is not used here). Enqueue without waiting; the response comes back at once.
- [ ] **Step 4: Run** the tests. Expected: PASS.
- [ ] **Step 5: Docs** in `api.rst`: both endpoints, parameters, limits, errors, the re-check, the audit entries. `make docs`.
- [ ] **Step 6: Commit** `api: list vehicles not driven for a number of days and delete a confirmed selection of them`.

---

### Task 7: The mod reports the last driver

Load the `dayz-dev` skill before this task. Check `Transport.OnDriverEnter(Human player)`, `Transport.CrewDriver()`, `CGame.GetTime()` and the `MotorbikeScript`/`BoatScript` classes with `dayz-api.py` before using them.

**Files:**
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Proto.c` (`DZOAdminVehicle`: `string driver; string driver_name; float driven_ago;`), `.../4_World/DZOAdmin/Hooks.c` (`modded class CarScript`), `.../4_World/DZOAdmin/Motorbike.c`, `.../4_World/DZOAdmin/Boat.c`, `.../4_World/DZOAdmin/World.c` (`CollectVehicles`), `.../5_Mission/DZOAdmin/Service.c` (hello features), `.../3_Game/DZOAdmin/Version.c` (`DZOADMIN_VERSION = "0.8.0"`), `servermods/dzo-admin/README.md`
- Modify: `testmods/dzo-client-probe` only if needed; `scripts/test-client.sh` (new checks)
- Modify: the servermods lock / checksum files `make servermods-lock` updates; `debian/changelog` if the packaged mod version is recorded there

**Interfaces:**
- Consumes: the JSON names from Task 2 (`driver`, `driver_name`, `driven_ago`) and `admin.FeatureDriver` = `"driver"`.
- Produces: a static helper `DZOAdminDrivers` in 4_World with `static void Entered(Transport t, Human player)` and `static void Fill(Transport t, DZOAdminVehicle v)`, keyed by `DZOAdminWorld.VehicleId`, storing Steam ID, name and `GetGame().GetTime()`. Entries are removed in each `EEDelete`.

- [ ] **Step 1: Add the test-client checks first** in `scripts/test-client.sh`, after "probe: the player sits in a car" (the probe seats the player in the driver seat of a new Offroad_02):
  - `vehicle state: the car has a driver`: the API's `.[] | select(.type == "Offroad_02" and .driver == "<the player's steam id>")` appears (`waitfor 30`).
  - `vehicle record: the last driver is kept`: after the player gets out (or after the existing move checks), `last_driver` is still the player and `last_driven` is set.
  - `hello: the mod reports the driver feature`: the hub status/hello shows `driver` (or grep `[DZOAdmin] features: ... driver` in the script log).
- [ ] **Step 2: Implement** the overrides: `override void OnDriverEnter(Human player)` in `modded class CarScript`, `MotorbikeScript` and `BoatScript`, each `super.OnDriverEnter(player)` then `DZOAdminDrivers.Entered(this, player)` on the server only. In each `Collect`, call `DZOAdminDrivers.Fill`: if `CrewDriver()` is a player with an identity, that player counts as driving now (`driven_ago = 0`, and the remembered entry is refreshed); else the remembered entry with `driven_ago = (GetGame().GetTime() - at) / 1000.0`. Add `"driver"` to `s.features` unconditionally. Keep the code inside the existing `#ifndef DAYZ_1_29` guards where the files already use them.
- [ ] **Step 3: Build** the PBO with the skill's `dayz-mod-pack.sh build` (this is the one task that changes mod code), boot a local server and compare the script module counts with vanilla.
- [ ] **Step 4: Run** `make test-client` and `make test-client ARGS=--exp`. Expected: every verdict PASS, including the three new ones.
- [ ] **Step 5: Run** `make servermods-check` and `go test ./...`. Expected: PASS.
- [ ] **Step 6: Commit** `admin: the mod reports the last driver of each vehicle (dzo-admin 0.8.0)`, with `make test-client passed (stable and --exp)` in the message.

---

### Task 8: Vehicle list columns and map link (web)

**Files:**
- Modify: `internal/web/templates/fragments.html` (`vehicles`, lines 43-62), `internal/web/handlers.go` (`listView` gets `Now time.Time`; `vehiclesFragment` sets it), `internal/web/web_test.go`, `internal/web/e2e_pages_test.go` (or the e2e file that covers the instance page), `internal/web/static/app.css` if a column needs a style
- Docs: `docs/web.rst` (the vehicle list)

**Interfaces:**
- Consumes: `LastDriver`, `LastDriverName`, `LastDriven`, `FirstSeen` on `admin.Vehicle` (Task 5); the `ago` and `stamp` template funcs; the map's `?x=&z=&label=` link (`focusFromLink` in `static/map.js`).

- [ ] **Step 1: Write the failing tests:**
  - `TestVehicleFragmentShowsTheLastDriver` (`web_test.go`, real API server with a store in `t.TempDir()`): the fragment has a "Last driver" header, the driver's name linking to `/b/main/players/<steamid>` with the Steam ID in `title`, "3 d ago" with the exact `stamp` time in `title`, and for a never-driven vehicle `not since 2026-10-29`.
  - `TestVehicleFragmentLinksToTheMap`: the position cell is `<a href="/b/main/i/alpha/map?x=7500&amp;z=7600&amp;label=Offroad_02+%28Bob%29">7500 / 7600</a>` (x/z rounded to whole metres, the label URL-escaped by `html/template`).
  - `TestE2EVehicleLinkOpensTheMap` (chromedp): clicking the position opens the map with one `.focus` marker near the centre and the label in its tooltip (see `TestE2EMapOpensOnALink`).
  - Sorting: the "Last driven" `<td>` has a `data-sort` value of the unix time of `LastDriven`, else of `FirstSeen`, so the existing table sort orders by it.
- [ ] **Step 2: Run** `go test ./internal/web -run 'VehicleFragment' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the two columns after "Owner" and the link. Escape everything through `html/template`; build the query with `url.Values` in Go (a `mapLink` helper in `handlers.go`) rather than in the template.
- [ ] **Step 4: Run** the tests and `make e2e`. Expected: PASS.
- [ ] **Step 5: Docs** in `web.rst`: the two columns, what "not since" means, the map link. `make docs`.
- [ ] **Step 6: Commit** `web: last driver and last drive in the vehicle list, and a link to the vehicle on the map`.

---

### Task 9: The cleanup step (web)

**Files:**
- Create: `internal/web/cleanup.go`, `internal/web/cleanup_test.go`, `internal/web/e2e_cleanup_test.go`
- Modify: `internal/web/server.go` (routes), `internal/web/templates/fragments.html` (a "Clean up" button above the vehicle table when `Can.Delete`; `cleanup` and `cleanupResult` fragments), `internal/web/perms.go` only if `can` lacks the delete flag
- Docs: `docs/web.rst`

**Interfaces:**
- Consumes: `apiclient.UnusedVehicles`, `apiclient.CleanupVehicles`, `api.UnusedVehicles`, `api.CleanupResult` (Task 6); `canOn(r, inst).Delete`; the CSRF check `s.csrfOK`.
- Produces routes:
  - `GET  <inst>/vehicles/cleanup?days=N`: the step: the threshold choice (14, 30, 60 days and a number input 1..3650, default 30) and, once a threshold is chosen, the preview table (class, kind, position with the map link of Task 8, last driver, last driven, health) with "Delete N vehicles" as the button text. With no matches: "No vehicle has gone unused for N days." When `tracked_since` is less than N days ago, the page also says "dzo has been tracking vehicles on this instance only since <date>; no vehicle can be older than that yet."
  - `POST <inst>/vehicles/cleanup` (CSRF-checked, form fields `days` and repeated `id`): sends exactly the previewed ids; renders "Deleted N vehicles; skipped M:" followed by each skipped id with its reason.

- [ ] **Step 1: Write the failing tests:**
  - `TestCleanupPageNeedsDelete`: a viewer gets no button on the instance page and 403 on both routes.
  - `TestCleanupPreview`: `days=30` lists the old empty vehicles with their map links; the confirm form has one hidden `id` input per listed vehicle, `days=30`, and the button reads `Delete 2 vehicles`.
  - `TestCleanupPreviewSaysTrackingIsYoung`: every `first_seen` 3 days ago, `days=14` → the tracking-since sentence and no form.
  - `TestCleanupConfirm`: POST with the two ids → the result text, two `vehicle_delete` commands queued on the hub.
  - `TestCleanupCSRF`: POST without the token → 403.
  - `TestCleanupOldMod`: the API's 409 message is shown as a flash, no form.
  - `TestE2ECleanupFlow` (chromedp): open the step, pick "1 month", see the list, click Delete, accept the confirmation dialog (`hx-confirm="Delete 2 vehicles? This cannot be undone."`), see the result.
- [ ] **Step 2: Run** `go test ./internal/web -run Cleanup -v`. Expected: FAIL.
- [ ] **Step 3: Implement** following the existing panels: htmx for the threshold change and the confirm, `hx-confirm` as the second confirmation, a full page fallback when htmx is missing is not needed (the rest of the UI needs it too).
- [ ] **Step 4: Run** the tests and `make e2e`. Expected: PASS.
- [ ] **Step 5: Docs** in `web.rst`: the cleanup step, the thresholds, what is never deleted, the re-check, the audit entries, and that an old mod blocks it. `make docs`.
- [ ] **Step 6: Run** `make check`. Expected: PASS, coverage >= 85%.
- [ ] **Step 7: Commit** `web: a cleanup step that deletes the vehicles not driven for a chosen time, after a preview and a confirmation`.

---

## Order and dependencies

Task 1 is independent and goes first. Tasks 2 and 3 are independent of each other; 4 needs both; 5 needs 3; 6 needs 2 and 5; 7 needs 2 (and 4 and 5 for its record check); 8 needs 5; 9 needs 6 and 8. One branch, `feature/vehicle-last-driver`, one commit per task.

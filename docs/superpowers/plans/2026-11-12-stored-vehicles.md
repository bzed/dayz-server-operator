<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Stored-Vehicle Actions (Expansion Virtual Garage) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admins list the vehicles stored in DayZ Expansion's virtual garage, retrieve them to a map position, and move their spawn point — with an in-game notification to the online owners.

**Architecture:** The dzo-admin mod collects stored vehicles from `ExpansionGarageModule` into the existing vehicles sync (a `stored` flag tells them apart) and carries out two new commands that restore the vehicle from Expansion's binary entity-storage file, place or re-save it, and update the garage JSON. dzo's Go side passes the flag through, keeps stored vehicles out of the cleanup, and adds two API routes that the web vehicle list and map call; the map draws one marker per "show on map" link, never a stored-vehicle layer.

**Tech Stack:** Go (std `net/http`, `html/template`), Enforce Script in `servermods/dzo-admin`, htmx + Leaflet (`internal/web/static/map.js`), Expansion 1.30 APIs verified in `/home/bzed/workspace/dayz/mod-sources/DayZ-Expansion-Scripts`.

**Spec:** `docs/superpowers/specs/2026-11-12-stored-vehicles-design.md`

## Global Constraints

- `make check` must pass: fmt, vet, golangci-lint v2.14.0, reuse, licences, vulncheck, `test-race`, fuzz, e2e (needs Chrome and the JS assets of docs/development.rst), coverage at or above 85%.
- Every new file gets an SPDX header (`SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>` + `SPDX-License-Identifier: AGPL-3.0-or-later`), like its neighbours.
- No new configuration options; no new dependencies; no protocol version bump (the changes are additive; the `garage` hello feature gates the new commands).
- Any commit touching `servermods/`, `internal/admin`, or the vehicle actions in `internal/api`/`internal/web` runs the mod boot check (steps below) and the final task runs `make test-client` plus `make test-client ARGS=--exp`; their commit messages say that it passed.
- Enforce Script: only the Expansion APIs named in this plan, all verified against the source tree above; null-check every accessor; no C# syntax.
- Tests write only under `t.TempDir()`; no `time.Sleep` waits; one logical change per commit with the area prefixes of `git log` (`admin:`, `web:`, `api:`, `docs:`).
- Behaviour changes document their user-visible side in the same commit (`docs/api.rst`, `docs/web.rst`, the mod's README).

## Review Focus

1. **A stored vehicle whose .bin is missing** (`broken`): the list still shows it and both actions return a clear error instead of silently dropping it or crashing. Pinned by: Task 1 (JSON with `broken: true` parses), Task 5 (restore failure path returns the error text), Task 8 (verification step).
2. **The cleanup must never offer or delete a stored vehicle** — the delete command cannot even address it. Pinned by: Task 2 (`TestCleanupSkipsStoredVehicles`).
3. **An old mod or old dzo on the other side**: a sync without the new fields parses (no stored vehicles), and dzo answers 409 instead of sending commands the mod cannot run. Pinned by: Task 1 (`TestSyncParsesVehicleWithoutStoredFields`), Task 3 (`TestStoredVehicleActionsNeedGarageFeature`).
4. **A failed spawn-move must not lose the vehicle**: restore with `deleteRestored = false`, delete the entity on a failed re-save; the .bin keeps the old position and the garage entry stays. Pinned by: Task 5 (algorithm) and Task 8 (spawn-move verification with a real .bin).
5. **Stored ids must never be confused with live ids**: the `g` prefix, `FindStored` only matches `g...`, and the map skips stored entries in the Vehicles layer. Pinned by: Task 4 (`StoredId`), Task 7 (`TestE2EMapSkipsStoredVehiclesInLayer`).

---

### Task 1: Protocol — stored-vehicle fields and command kinds

**Files:**
- Modify: `internal/admin/proto.go` (Vehicle struct, kind constants)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Proto.c` (DZOAdminVehicle)
- Modify: `servermods/dzo-admin/README.md` (protocol section: the new vehicle fields)
- Create: `internal/admin/proto_test.go`

**Interfaces:**
- Produces (mod, `DZOAdminVehicle` — JSON keys are the Enforce field names): `bool stored;` `string stored_by;` `int territory_id;` (0 = none) `ref array<string> owners;` `bool broken;`
- Produces (Go, `admin.Vehicle`): `Stored Flag \`json:"stored,omitempty"\``, `StoredBy string \`json:"stored_by,omitempty"\``, `TerritoryID int \`json:"territory_id,omitempty"\``, `Owners []string \`json:"owners,omitempty"\``, `Broken Flag \`json:"broken,omitempty"\``
- Produces (Go): `KindStoredVehicleRetrieve = "storedvehicle_retrieve"`, `KindStoredVehicleMove = "storedvehicle_move"`, `FeatureGarage = "garage"`

- [ ] **Step 1: Write the failing tests**

In `internal/admin/proto_test.go` (SPDX header like the package's other files), two tests over the raw JSON the mod sends (modelled on the sync bodies in `internal/admin/hub_test.go`):

`TestSyncParsesStoredVehicle`: a `SyncRequest` JSON body whose `vehicles` array holds one stored vehicle — `{"id":"g1.2.3.4","kind":"car","type":"OffroadHatchback","x":100,"y":0,"z":200,"yaw":0,"stored":true,"stored_by":"76561198000000001","territory_id":0,"owners":["76561198000000001"],"broken":false}` — unmarshals, `Normalise()`s, and `hub.Sync`s into a hub whose `Vehicles()` returns the vehicle with `Stored` true, `StoredBy` set, `Owners` of one, `TerritoryID` 0, `Broken` false.

`TestSyncParsesVehicleWithoutStoredFields`: the same body without any of the five new keys yields `Stored` false and `Owners` nil (an old mod).

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/admin/ -run TestSyncParses`
Expected: FAIL (build error: the fields do not exist)

- [ ] **Step 3: Implement the Go side**

Add the five `Vehicle` fields and the three constants to `internal/admin/proto.go`, with comments in the file's style: `Stored` marks a vehicle in the Expansion garage (not in the world), `StoredBy` the Steam ID of the player whose garage holds it, `TerritoryID` the territory of the garage (0 = none), `Owners` the Steam IDs that may retrieve it, `Broken` a missing entity-storage file (it can never come back).

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/admin/ -run TestSyncParses`
Expected: PASS

- [ ] **Step 5: Mirror the fields in the mod**

In `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Proto.c`, extend `DZOAdminVehicle` after `covered` with the same five fields and comments in the file's style (`// DayZ Expansion: stored in the virtual garage, ...`). Document them in the protocol section of `servermods/dzo-admin/README.md` next to the existing vehicle fields.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/proto.go internal/admin/proto_test.go servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Proto.c servermods/dzo-admin/README.md
git commit -m "admin: stored-vehicle fields in the sync protocol"
```

### Task 2: The cleanup leaves stored vehicles alone

**Files:**
- Modify: `internal/api/cleanup.go` (`unusedOf`, the switch in `cleanupVehicles`)
- Test: `internal/api/cleanup_test.go`

**Interfaces:**
- Consumes: `admin.Vehicle.Stored` (Task 1).

- [ ] **Step 1: Write the failing tests**

In `internal/api/cleanup_test.go`, modelled on the existing cleanup tests:

`TestUnusedVehiclesSkipsStored`: `unusedOf` with a stored vehicle whose `FirstSeen` is a year old returns a list without it (and with an equally old live vehicle, to prove only the flag matters).

`TestCleanupSkipsStoredVehicles`: sync a hub with one stored vehicle, `GET .../vehicles/unused?days=1` does not list it, and `POST .../vehicles/cleanup` with its id answers `Skipped` with the reason `"stored in the Expansion garage"` and enqueues nothing.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/api/ -run 'Stored'`
Expected: FAIL (stored vehicles are listed)

- [ ] **Step 3: Implement**

In `unusedOf` extend the skip condition to `|| bool(v.Stored)`; in `cleanupVehicles`'s switch add `case bool(v.Stored): skip("stored in the Expansion garage")` before the crew case.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/api/ -run 'Cleanup|Stored|Unused'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/cleanup.go internal/api/cleanup_test.go
git commit -m "api: keep garage-stored vehicles out of the cleanup"
```

### Task 3: API routes — retrieve and spawn-move

**Files:**
- Modify: `internal/api/server.go` (routes, handlers)
- Test: `internal/api/vehicles_test.go`
- Modify: `docs/api.rst`

**Interfaces:**
- Consumes: `admin.KindStoredVehicleRetrieve`, `admin.KindStoredVehicleMove`, `admin.FeatureGarage` (Task 1); the `s.run` / `route` / `fail` helpers of `internal/api/server.go`.
- Produces: `POST /api/v1/instances/{instance}/vehicles/{id}/retrieve` and `POST /api/v1/instances/{instance}/vehicles/{id}/spawnmove`, both `PermTeleport`, body `{"x":<float>,"z":<float>}`. Both answer 409 with "the dzo-admin mod of this instance does not report the garage; update it" when the hub's hello features lack `garage` (the check of `cleanupReady` in `internal/api/cleanup.go` is the pattern). Audit actions: `storedvehicle_retrieve` and `storedvehicle_move`, target `"<vehicle id> to <x>/<z>"`.

- [ ] **Step 1: Write the failing tests**

In `internal/api/vehicles_test.go` (the `newEnv`/`e.do`/`e.token`/`modAnswers` harness of `internal/api/api_test.go`):

`TestStoredVehicleRetrieve`: hub synced with `Hello: true` and `Features: []string{"garage"}`, `modAnswers("")` in place, operator token, `POST .../vehicles/g1.2.3.4/retrieve` with `{"x":100,"z":200}` answers 200 and `ActionResult.OK`, and the audit tail ends with action `storedvehicle_retrieve`.

`TestStoredVehicleSpawnMove`: the same for `POST .../vehicles/g1.2.3.4/spawnmove`, audit action `storedvehicle_move`.

`TestStoredVehicleActionsNeedGarageFeature`: the same POSTs against a hub whose hello has no `garage` feature answer 409.

`TestStoredVehicleActionsAreTeleportPerm`: a viewer token answers 403 (the pattern of the permission cases in `internal/api/api_test.go`).

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/api/ -run TestStoredVehicle`
Expected: FAIL (404, no such routes)

- [ ] **Step 3: Implement**

In `internal/api/server.go` add the two routes next to `s.moveVehicle`, a `storedVehicleReady(w, c) bool` helper mirroring `cleanupReady` (connected check + `c.hub.Status().Hello.Has(admin.FeatureGarage)`), and two handlers that share a body struct `{"x","z" float64}` and build `admin.Command{Kind: ..., Vehicle: r.PathValue("id"), X: b.X, Z: b.Z}` through `s.run(w, r, c, cmd, "storedvehicle_retrieve"/"storedvehicle_move", fmt.Sprintf("%s to %.0f/%.0f", cmd.Vehicle, b.X, b.Z))`.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/api/ -run TestStoredVehicle`
Expected: PASS

- [ ] **Step 5: Document and commit**

Add both routes to `docs/api.rst` next to `vehicle_move` (same wording style: what it does, the permission, the body, the 409).

```bash
git add internal/api/server.go internal/api/vehicles_test.go docs/api.rst
git commit -m "api: retrieve and spawn-move routes for garage-stored vehicles"
```

### Task 4: The mod lists stored vehicles and reports the garage feature

**Files:**
- Create: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Garage.c`
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c` (features, vehicles part)
- Modify: `servermods/dzo-admin/README.md` (the `garage` feature)

**Interfaces:**
- Consumes: the `DZOAdminVehicle` fields of Task 1; `ExpansionGarageModule.s_Instance` (static, may be null), `GetGarageData()` (public, returns `array<ExpansionGarageData>`, may be null), `ExpansionGarageData.m_Vehicles`/`m_OwnerUID`/`m_TerritoryID`, `ExpansionGarageVehicleData.m_OwnerUIDs`/`m_ClassName`/`m_Position`/`m_Orientation`/`m_GlobalID`/`IsGlobalIDValid()`/`GetEntityStorageFileName()` — all public, verified in `DayZExpansion/Garage/Scripts/4_World/DayZExpansion_Garage/Systems/Garage/`.
- Produces (for Task 5): `class DZOAdminGarage` in `4_World/DZOAdmin/Garage.c`, everything inside `#ifdef EXPANSIONMODGARAGE`:
  - `static string StoredId(ExpansionGarageVehicleData vehicleData)` → `"g" + m_GlobalID[0] + "." + [1] + "." + [2] + "." + [3]` (the `g` keeps the id shape of `DZOAdminWorld.VehicleId` and can never collide with a live `v...` id)
  - `static bool FindStored(string id, out ExpansionGarageData garageData, out ExpansionGarageVehicleData vehicleData)` — iterates `GetGarageData()`, matches `StoredId(vd) == id`, returns false when not found
  - `static void Collect(array<ref DZOAdminVehicle> list)` — for every garage, every valid vehicle data: `id = StoredId(vd)`, `type = m_ClassName`, `x/y/z` from `m_Position`, `yaw = m_Orientation[0]`, `stored = true`, `stored_by = garageData.m_OwnerUID`, `territory_id = garageData.m_TerritoryID < 0 ? 0 : m_TerritoryID`, `owners` = a fresh `array<string>` with the values of `m_OwnerUIDs` (never the shared ref), `broken = !FileExist(vd.GetEntityStorageFileName())`; `kind` stays empty (the hub classifies by class name)

- [ ] **Step 1: Write Garage.c**

New file with the repo's SPDX header and the file-comment style of `World.c`; only the three statics above, no actions yet. Null-check `ExpansionGarageModule.s_Instance` and `GetGarageData()`.

- [ ] **Step 2: Wire it into the sync**

In `Service.c`: inside the `m_Hello` features block, after the `expansion` insert, `#ifdef EXPANSIONMODGARAGE` → `s.features.Insert("garage");` `#endif`. In the vehicles part, after `DZOAdminWorld.CollectVehicles(s.vehicles);`, `#ifdef EXPANSIONMODGARAGE` → `DZOAdminGarage.Collect(s.vehicles);` `#endif`. Document the `garage` feature in the README's protocol section.

- [ ] **Step 3: Verify the mod compiles with and without Expansion**

Run: `make servermods && scripts/test-client.sh --no-client`
Expected: PASS lines, no script errors in the server RPT (the ifdef guard compiles to nothing without Expansion).

Run: `scripts/test-client.sh --expansion --no-client`
Expected: PASS lines; the server RPT's `[DZOAdmin] features:` line lists `garage`.

- [ ] **Step 4: Commit**

```bash
git add servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Garage.c servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c servermods/dzo-admin/README.md
git commit -m "admin: report the Expansion garage's stored vehicles

test-client --no-client and --expansion --no-client passed."
```

### Task 5: The mod retrieves and spawn-moves stored vehicles

**Files:**
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Garage.c` (actions)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/World.c` (extract `PlaceVehicle` from `VehicleMove`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c` (dispatch)
- Modify: `servermods/dzo-admin/README.md` (command kinds)

**Interfaces:**
- Consumes: `FindStored`/`StoredId` (Task 4); `ExpansionEntityStorageModule.RestoreFromFile(fileName, out entity, placeholder, player, deleteRestored)` and `.SaveToFile(entity, fileName, inventoryOnly, placeholder, removeOrphaned)` (static, `DayZExpansion/Core/Scripts/4_World/DayZExpansion_Core/Classes/ExpansionEntityStorage.c` lines 1130/1160); `ExpansionEntityStoragePlaceholder.GetByStoredEntityGlobalID(TIntArray)`; `ExpansionItemSpawnHelper.IsSpawnPositionFree(vector position, vector orientation, string className, out Object blockingObject)`; `PlayerBase.GetPlayerByUID(string)`; `ExpansionNotification(...).Create(identity)`; `ExpansionStatic.GetItemDisplayNameWithType(string)`; `ExpansionVehicle.Get(out ExpansionVehicle, EntityAI)` and `.GetModelZeroPointDistanceFromGround()`.
- Produces: `DZOAdminWorld.PlaceVehicle(EntityAI e, float x, float z)` — the placement half of today's `VehicleMove` body: `restY = BoatScript.Cast(e) ? RestY(x, z) : TopY(x, z)`, `pos = Vector(x, restY + 1.0, z)`, `SetPosition(pos)`, keep yaw (`SetOrientation(Vector(yaw, 0, 0))` from `e.GetOrientation()[0]`), `GetPhysics().SetVelocity("0 0 0")` when a physics exists, `dBodySetAngularVelocity(e, "0 0 0")`, `dBodyActive(e, ActiveState.ACTIVE)`. `VehicleMove` calls it and keeps its crew/damage-protection logic unchanged.
- Produces: `DZOAdminGarage.Retrieve(DZOAdminCommand c)` and `DZOAdminGarage.Move(DZOAdminCommand c)`, both `static string` returning `""` or an error text.

- [ ] **Step 1: Extract PlaceVehicle**

Move the placement block of `VehicleMove` into `static vector PlaceVehicle(EntityAI e, float x, float z)` and call it from `VehicleMove`. No behaviour change.

- [ ] **Step 2: Implement the shared restore and notify helpers**

In `Garage.c`, still inside the ifdef:

`protected static bool Restore(ExpansionGarageVehicleData vehicleData, out EntityAI entity)`: look up the placeholder (`GetByStoredEntityGlobalID(vehicleData.m_GlobalID)`), call `RestoreFromFile(vehicleData.GetEntityStorageFileName(), entity, placeholder, null, false)` — **`deleteRestored = false` is the safety net: the .bin survives a failed action** — and on success `if (placeholder) placeholder.Delete();` (it is invisible and damage-immune; cargo has moved to the entity).

`protected static void NotifyOwners(ExpansionGarageVehicleData vehicleData, string text)`: `displayName = ExpansionStatic.GetItemDisplayNameWithType(vehicleData.m_ClassName)`; for every uid in `m_OwnerUIDs`, `PlayerBase p = PlayerBase.GetPlayerByUID(uid)`, and when `p && p.GetIdentity()`, `ExpansionNotification("Server", "Your " + displayName + " " + text, "", ARGB(255, 52, 152, 219), 7, ExpansionNotificationType.GARAGE).Create(p.GetIdentity());` — online owners only, exactly like `World.c`'s `SendExpansion`.

- [ ] **Step 3: Implement Retrieve**

Algorithm (order matters):
1. `FindStored(c.vehicle, garageData, vehicleData)` → else `"stored vehicle not found"`.
2. World bounds check like `VehicleMove` (`c.x`/`c.z` within `GetGame().GetWorld().GetWorldSize()`) → else `"position is outside the world"`.
3. `Restore(vehicleData, entity)` → else `"could not restore it from its storage file"`.
4. `DZOAdminWorld.PlaceVehicle(entity, c.x, c.z)`.
5. `DeleteFile(vehicleData.GetEntityStorageFileName());` — best effort, with a comment saying why the return is not checked (the restore left the file, and Expansion's own `LoadVehicle` deletes it too).
6. `garageData.RemoveVehicle(vehicleData); garageData.Save();`
7. `NotifyOwners(vehicleData, "was taken out of the garage by an admin");`
8. Return `""`.

- [ ] **Step 4: Implement Move (spawn point)**

Algorithm:
1–2. Same as Retrieve.
3. Blocked check first, at the target:
   `Object blocking; if (!ExpansionItemSpawnHelper.IsSpawnPositionFree(Vector(c.x, DZOAdminWorld.TopY(c.x, c.z), c.z), vehicleData.m_Orientation, vehicleData.m_ClassName, blocking))` → return `"the spawn point is blocked by " + blocking.GetDisplayName()` (null-check `blocking`'s display name the way `RPC_RetrieveVehicleRequest` does).
4. `Restore(vehicleData, entity)` → else `"could not restore it from its storage file"`.
5. `DZOAdminWorld.PlaceVehicle(entity, c.x, c.z)` (keeps the stored orientation).
6. `if (!ExpansionEntityStorageModule.SaveToFile(entity, vehicleData.GetEntityStorageFileName()))` → `GetGame().ObjectDelete(entity);` and return `"could not save the vehicle back; its spawn point is unchanged"` (the .bin still has the old position — nothing lost).
7. Update the garage entry the way `SetDataFromVehicleObject` does: `modelZeroPoint = 0; ExpansionVehicle ev; if (ExpansionVehicle.Get(ev, entity)) modelZeroPoint = ev.GetModelZeroPointDistanceFromGround();` then `p = entity.GetPosition(); vehicleData.m_Position = Vector(p[0], p[1] - modelZeroPoint, p[2]); vehicleData.m_Orientation = entity.GetOrientation();`
8. `garageData.Save();` then `GetGame().ObjectDelete(entity);`
9. `NotifyOwners(vehicleData, "had its garage spawn point moved by an admin");`
10. Return `""`.

- [ ] **Step 5: Dispatch the commands**

In `Service.c`'s command dispatch, inside `#ifdef EXPANSIONMODGARAGE`: `else if (c.kind == "storedvehicle_retrieve") err = DZOAdminGarage.Retrieve(c);` and `else if (c.kind == "storedvehicle_move") err = DZOAdminGarage.Move(c);`. Document both kinds in the README's command-kind list (fields used: `vehicle`, `x`, `z`).

- [ ] **Step 6: Verify the mod compiles with and without Expansion**

Run: `make servermods && scripts/test-client.sh --no-client && scripts/test-client.sh --expansion --no-client`
Expected: PASS lines, no script errors; the existing action checks (teleport, vehicle move) still pass — `PlaceVehicle` is the same code, moved.

- [ ] **Step 7: Commit**

```bash
git add servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Garage.c servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/World.c servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c servermods/dzo-admin/README.md
git commit -m "admin: retrieve and spawn-move actions for garage-stored vehicles

test-client --no-client and --expansion --no-client passed."
```

### Task 6: Web — stored vehicles in the list and the vehicle panel

**Files:**
- Modify: `internal/web/handlers.go` (`vehiclesFragment` view, `mapLink`)
- Modify: `internal/web/templates/fragments.html` (`vehicles`, `vpanel`)
- Modify: `docs/web.rst`
- Test: `internal/web/vehicles_test.go` (create)

**Interfaces:**
- Consumes: `admin.Vehicle.Stored/StoredBy/TerritoryID/Owners/Broken` (Task 1); the API routes of Task 3.
- Produces: the `vehicles` fragment renders live vehicles as today (`.Items`) and a second table for `.Stored` (a new field on the fragment view, `[]admin.Vehicle`); `mapLink` appends `&sv=<id>` to the map URL when `v.Stored` and labels it `"<type> (stored by <stored by>)"`. The `vpanel` fragment shows, for a stored vehicle (gated on `Can.Teleport`), two small forms with `x`/`z` number inputs posting to `{{$base}}/retrieve` and `{{$base}}/spawnmove`, target `#panel-flash`; live vehicles keep today's buttons unchanged.

- [ ] **Step 1: Write the failing tests**

In `internal/web/vehicles_test.go`, modelled on the fragment render tests of `internal/web` (see `internal/web/web_test.go` for the render helper): render the `vehicles` template with one live and one stored vehicle (`Stored: true`, `StoredBy: "76561198000000001"`, `Owners: ["76561198000000001"]`, `Type: "ExpansionOlga"`); assert the stored row is in its own section headed "Stored in the Expansion garage", the stored-by cell links `/b/<backend>/players/76561198000000001`, the position link contains `sv=g1.2.3.4`, and a broken one shows a "no storage file" note. Render `vpanel` with a stored vehicle and `Can.Teleport`; assert both forms with their routes. Assert the live table is unchanged for a non-stored vehicle.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/web/ -run TestVehicles`
Expected: FAIL (no stored section)

- [ ] **Step 3: Implement**

`vehiclesFragment` splits the hub list into live `.Items` and `.Stored` (keep the `listView` for the live part); `mapLink` gains the `sv` parameter and label. In `fragments.html`, add the stored table (columns: Class, Position, Stored by, Owners, note, Actions button — the Actions button opens the same `vpanel`) and branch `vpanel` on `.Vehicle.Stored`. Column content: stored-by and owners render as player-page links (the pattern of the last-driver cell), territory as `territory #<id>` when `TerritoryID` > 0.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/web/`
Expected: PASS

- [ ] **Step 5: Document and commit**

Describe the stored section and its actions in `docs/web.rst` where the vehicle list is documented.

```bash
git add internal/web/handlers.go internal/web/templates/fragments.html internal/web/vehicles_test.go docs/web.rst
git commit -m "web: stored vehicles in the vehicle list and panel"
```

### Task 7: Map — one marker per stored vehicle, with the two actions

**Files:**
- Modify: `internal/web/static/map.js`
- Test: `internal/web/e2e_map_test.go`

**Interfaces:**
- Consumes: the `sv` map-URL parameter (Task 6), the API routes of Task 3, the vehicle stream entries with `stored: true`.
- Produces: the map's Vehicles layer never draws stored vehicles; `focusFromLink` draws, when `sv=<id>` is present, a single marker (icon `fa-warehouse`, the focus-marker pattern of `map.js` lines 971–992) whose context menu has "Retrieve to coordinates…", "Move spawn point to coordinates…" (both open the x/z dialog of the live "Move to coordinates…" item and `act('/vehicles/<sv>/retrieve'|'/spawnmove', {x, z})`), and "Remove marker".

- [ ] **Step 1: Write the failing e2e tests**

In `internal/web/e2e_map_test.go`, modelled on `TestE2EMapOpensOnALink` and `TestE2EMapFollowsTheStream`:

`TestE2EMapShowsAStoredVehicle`: open `/map?x=100&z=200&sv=g1.2.3.4&label=ExpansionOlga`; assert the marker is present with the warehouse icon and its menu lists both actions.

`TestE2EMapSkipsStoredVehiclesInLayer`: the stream (the e2e harness's fake vehicle push) carries one stored vehicle; assert the Vehicles layer has no marker for it.

- [ ] **Step 2: Run to verify they fail**

Run: `DZO_E2E_CHROME=... DZO_E2E_ASSETS=... go test -tags e2e ./internal/web/ -run TestE2EMap` (the assets setup of docs/development.rst; `make e2e` runs the same)
Expected: FAIL

- [ ] **Step 3: Implement**

In `onVehicles`, skip entries with `v.stored`. In `focusFromLink`, read `sv`; when present, build the marker with the two dialog items (copy the field spec of the live "Move to coordinates…" item) and the remove item; the marker stays until removed (it is not stream-driven).

- [ ] **Step 4: Run to verify they pass**

Run: `make e2e`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/static/map.js internal/web/e2e_map_test.go
git commit -m "web: stored-vehicle marker and retrieve/spawn-move actions on the map"
```

### Task 8: End-to-end against a real server, and the full gates

**Files:**
- No new product files; fixes, if any, go to the files of Tasks 1–7.

**Interfaces:**
- Consumes: everything above. Needs the Steam DayZ Server, the Expansion Workshop mods (CF, Dabs, Expansion — as `scripts/test-client.sh --expansion` loads them), and garage data to test with: copy `expansion/garage/*.json` and the matching `expansion/entitystorage/*.bin` of one real instance (ask the operator for the instance; the files are under its `storage_1`) into the test tree's mission storage.

- [ ] **Step 1: Boot a local server with Expansion and dzo-admin**

Use the dayz-dev skill's `make-server-tree.sh` + `free-ports.sh` flow (`scripts/test-client.sh --expansion --no-client` is the model; `--keep` keeps its tree). Copy the garage data named in the Interfaces block into the tree's `mpmissions/<mission>/storage_1/expansion/`. Start the server, `dzo serve`, and wait for the hello.

- [ ] **Step 2: Verify the list**

`dzo vehicle list <instance>` (or `curl .../vehicles`) shows the stored vehicles with class, owners, stored-by and position; the RPT has no script errors.

- [ ] **Step 3: Verify retrieve**

`POST .../vehicles/<stored id>/retrieve` with coordinates next to the garage; the answer is OK, the vehicle appears in the live vehicle list at the position, its garage JSON entry is gone, and the .bin is deleted. With no player online the notification path must not error (the RPT stays clean).

- [ ] **Step 4: Verify spawn-move**

`POST .../vehicles/<another stored id>/spawnmove` with free coordinates; the answer is OK, the garage JSON's `m_Position` changed, and retrieving the vehicle in-game (or checking the .bin via a second spawn-move) shows the new position. A spawn-move onto a blocked position answers "the spawn point is blocked by ...". A spawn-move of a broken vehicle answers the restore error.

- [ ] **Step 5: Run the required gates**

Run: `make test-client && make test-client ARGS=--exp`
Expected: PASS (note it in the commit message of the final fix commit, if any).

Run: `make check`
Expected: PASS, coverage at or above 85%.

- [ ] **Step 6: Commit any fixes and the verification note**

```bash
git commit -m "admin: fix <what the end-to-end run found>

test-client, test-client --exp and an end-to-end run against a local
Expansion server with real garage data passed."
```

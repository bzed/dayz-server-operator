# Stored-vehicle actions (Expansion virtual garage) — Design

Date: 2026-11-12
Status: agreed in conversation; implemented by docs/superpowers/plans/2026-11-12-stored-vehicles.md

## Intent and why

Players on the Expansion servers routinely build base parts on the exact spot where a
virtual-garage vehicle was stored. On retrieve, `ExpansionGarageModule.RPC_RetrieveVehicleRequest`
checks `ExpansionItemSpawnHelper.IsSpawnPositionFree` at the stored position and refuses with
"position blocked" — the vehicle is stuck in the garage until someone dismantles the base part.
Admins need to get those vehicles out, and to move the spawn point of stored vehicles so the
problem does not repeat. The admin needs to *see* the surroundings to pick a free spot, so the
map is part of the flow.

## How Expansion stores a garage vehicle (verified in the source)

- Per owner (player Steam ID, or the territory's garage) in
  `$mission:storage_<id>/expansion/garage/<OwnerUID>.json`: class name, position, orientation,
  global ID (4 ints), owner UIDs, skin (`ExpansionGarageData` / `ExpansionGarageVehicleData`).
- The full vehicle state (cargo, attachments, fluids) in a binary `FileSerializer` file
  `$mission:storage_<id>/expansion/entitystorage/<globalID-hex>.bin`
  (`ExpansionEntityStorageModule`). **The spawn position is read from this .bin** (line 606 of
  `ExpansionEntityStorage.c`), not from the garage JSON — the JSON position only feeds the
  blocked-check and the menu.
- Depending on `Garage.UseVirtualStorageForCargo`, an invisible `ExpansionGarageVehicle`
  placeholder object may hold the cargo in the world (it is damage-immune and buried 1 m below
  the surface). `ExpansionGarageModule.LoadVehicle` deletes it on retrieve.

Everything needed is public API: `ExpansionGarageModule.s_Instance.GetGarageData()`,
`m_Vehicles`, `m_Position`/`m_Orientation`, `garageData.Save()`,
`ExpansionEntityStorageModule.RestoreFromFile`/`SaveToFile`,
`ExpansionItemSpawnHelper.IsSpawnPositionFree`, `PlayerBase.GetPlayerByUID`,
`ExpansionNotification`. The `EXPANSIONMODGARAGE` compile define is set by the
`0_DayZExpansion_Garage_Preload` PBO, the same mechanism dzo-admin already uses with
`EXPANSIONMODVEHICLE`.

## Goals

1. **List**: stored vehicles appear in dzo's vehicle list — not on the map by default — with
   class, position, the owners that may retrieve them, and who stored the vehicle (player or
   territory). A "show on map" link opens the map centered on the stored position with a single
   marker for that vehicle.
2. **Retrieve** (primary flow): an admin action takes a stored vehicle out of the garage and
   places it at chosen coordinates, with cargo and fluids, the same careful placement
   `vehicle_move` uses (highest surface + 1 m, zeroed velocities, brief damage protection).
3. **Spawn-move** (secondary flow): an admin action changes where a stored vehicle will spawn
   when its owner retrieves it; the vehicle stays in the garage. The new position is validated
   with `IsSpawnPositionFree` first so the owner does not hit "position blocked" again.
4. **Notification**: the owning players get an in-game Expansion notification (garage type) when
   they are online and an admin retrieved or spawn-moved their vehicle. Offline owners are not
   notified; the action is in dzo's audit log either way.

## Non-goals

- Preventing players from building on spawn spots (root cause) — separate concern, not built here.
- Deleting stored vehicles, or moving them between garages/owners.
- Changing garage permissions, prices or Expansion settings.
- Territory *names* in the list (the territory id is shown; the map's territory circles carry names).

## Decisions

- Stored vehicles ride the existing vehicles sync part with a `stored` flag (and `stored_by`,
  `territory_id`, `owners`, `broken` fields); the web decides presentation. The map's Vehicles
  layer skips them (hundreds would overlap); the "show on map" link draws one marker with the
  two actions in its context menu (plus the same actions in the vehicle panel for the no-map case).
- Stored-vehicle ids are `g<i>.<i>.<i>.<i>` from the Expansion global ID — the same shape as the
  live `v<i>.<i>.<i>.<i>` ids, never colliding.
- Both actions are one permission: the existing teleport permission (`PermTeleport`), and the
  API refuses with 409 when the mod does not report the new `garage` hello feature.
- Both flows restore the vehicle from its .bin with `deleteRestored = false` and only delete the
  .bin after success, so a failed spawn-move cannot lose the vehicle: on a failed re-save the
  entity is deleted and the garage state is unchanged.
- A stored vehicle whose .bin is missing is listed with a `broken` flag (it can never come back);
  the actions return a clear error for it.
- The unused-vehicles cleanup never lists and never deletes stored vehicles.
- In placeholder mode (cargo in the world object) a spawn-move re-saves without the placeholder:
  the vehicle becomes virtual-cargo stored, which is harmless (the placeholder is invisible and
  Expansion logs a cosmetic warning on the later retrieve).
- No new configuration options anywhere.

## Verification

- Go unit/API/web tests per task (see the plan); `make check` including the e2e browser tests.
- The mod compiles and boots both without Expansion (the `#ifdef` guard) and with it
  (`scripts/test-client.sh --no-client` and `--expansion --no-client`).
- End to end against a local server with Expansion and real garage data copied from an instance:
  the stored vehicle is listed, retrieve puts it in the world, spawn-move changes the position in
  the garage JSON and the .bin, and the audit log records both.

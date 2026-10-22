<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Rebuilt houses and wells on the map Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Load the `dayz-dev` skill before Task 1 (AGENTS.md).

**Goal:** The admin map shows every map building that players have rebuilt with the DayZ 1.30 rebuilding mechanic, on two layers ("Rebuilt houses", "Rebuilt wells"). Hovering one shows how many parts are built, the weakest part's health and how long until decay has worn it down.

**Architecture:** As with territory flags (plan 2026-10-19), the dzo-admin mod reports these as ordinary markers, so the protocol, endpoints and SSE kinds stay the same. The mod registers every `BuildingBase` that has a construction component when it initialises (no world scan), and `DZOAdminRebuilt.Collect` emits a marker for each one with at least one built part. The marker's `ttl` is the seconds until the first decaying part reaches its decay floor. `map.js` words that ttl for this source as "worn down in …" instead of the flags' "goes down in …".

**Tech Stack:** Enforce Script (dzo-admin, DayZ 1.30 build 25963285), Leaflet `map.js`, Sphinx docs.

**Spec:** the request: "The map is also missing rebuilt houses and wells based on the new dayz basebuilding mechanics and their TTL."

## What the game does (checked in the 1.30 scripts, `dta/scripts.pbo`)

- Rebuildable structures are the `cfgVehicles` types that have a `Construction` class and are not `BaseBuildingBase` (`RebuildingSpawner.InitAllRebuildableTypes`). In vanilla 1.30 that includes `Land_House_*` under `4_World/Entities/Building/Rebuildable/` and `Land_IrrigationTunnel_Entrance_01`. Wells (`Well : BuildingSuper`) qualify only if their config has `Construction`. Task 1 Step 1 lists the real set on stable and experimental.
- The component is `Rebuilding : ConstructionBase`, reached through `BuildingBase.GetConstructionBasic()` (cast to `ConstructionBase`). Parts: `GetConstructionParts()` → `map<string, ConstructionPart>`, `part.IsBuilt()`, `part.CanPartDecay()`, `part.GetPartDecayRate()` (HP/s, negative), `part.GetPartDecayThreshold()` (fraction of max health).
- Other names used below, checked with `dayz-api.py` (1.30 build 25963285): `Object.GetWaterSourceObjectType()` → `EWaterSourceObjectType` (`NONE=-1 WELL=0 THROUGH=1`), `EntityAI.GetPersistentID(out int b1..b4)`, `Object.GetDisplayName()`, `BuildingBase.GetCodeLock(string slotName)` / `GetCombinationLock(string slotName)` with the constants `BuildingBase.ATTACHMENT_SLOT_CODE_LOCK` (`"Att_CodeLock"`) and `ATTACHMENT_SLOT_COMBINATION_LOCK` (`"Att_CombinationLock"`), `DamageSystem.GetDamageZoneFromComponentName(EntityAI, string, out string)` (static, returns bool), `ConstructionBasic.CanConstructionDecay()` (static), `RebuildingSpawner.RebuildAllWoodenPartsFiltered(ConstructionBase, EntityAI)` (static, for the spike). The index types `Weather.GetSandstorm()` as `Sandstorm`, but `IsActive()` is declared on `SandstormController`. Use the call exactly as vanilla does (`g_Game.GetWeather().GetSandstorm().IsActive()`, `ConstructionBase.c:337`) and do not store the result in a typed variable.
- Decay (`ConstructionBase.ProcessConstructionDecay`): each built, decaying part loses `rate × coef` HP per second in its damage zone (`DamageSystem.GetDamageZoneFromComponentName(entity, partName, zone)`) and stops at `threshold × GetMaxHealth(zone, "Health")`. `coef` is 1.5 during a sandstorm (`SANDSTORM_DECAY_RATE_COEF`), otherwise 1. Decay is off when `cfggameplay.json` has `disableBaseDecay` (`ConstructionBasic.CanConstructionDecay()`). **Decay does not remove the part.** It stops at the floor, so the "TTL" is the time until the weakest part is worn down to its floor, not until the building disappears. The docs must say this plainly.

## Global Constraints

- Mod rules as in the flags plan: no world scan (register in `EEInit`, remove in `EEDelete`), forward slashes, null-check every cast, `#ifdef` nothing vanilla. Signatures come from `dayz-api.py class ConstructionBase --inherited` / `class ConstructionPart`, never from memory.
- Layers `Rebuilt houses` (icon `home`, colour `#8f6f00`, the `--signal` token) and `Rebuilt wells` (icon `tint`, colour `#2f6fb5`), both `visibility: "operator"`, `source: "rebuilt"`.
- Marker id: `"r" + <persistent id>` when the building has one, else `"r" + type + "@" + round(x) + "," + round(z)`. A static map building has no persistent id, but its position is fixed. Task 1 Step 1 checks which applies.
- `ttl` is an int ≥ 1, or absent when no part decays (decay off, or nothing built that decays). Never 0 or negative.
- The PBO is rebuilt because mod code changes. The mod version goes 0.5.0 → 0.6.0, the protocol stays 1.
- `make check`, `make test-client` (stable and `ARGS=--exp`) pass, and the commit message says so (AGENTS.md: `servermods/`).
- `servermods/dzo-admin` is its own repo: commit there first, then the pointer and the PBO artefacts in dzo, with a `debian/changelog` entry.

## Review Focus

- A sandstorm is active: the ttl uses coef 1.5 (shorter), and a prop says `sandstorm: decaying faster`.
- `disableBaseDecay` is true: no `ttl`, prop `decay: off`, and the marker is still shown.
- A building where every built part is already at its floor: no `ttl`, prop `decay: worn down`. Not "worn down in <1m".
- A part whose damage zone name is empty (`GetDamageZoneFromComponentName` gives `""`, which the game skips too): ignored for the ttl but counted in "parts built".
- Hundreds of rebuildable buildings registered on a map such as Nasdara: `Collect` stays within one markers tick. Measure in Task 1 Step 6 and record the count and time in the commit message.

---

### Task 1: dzo-admin reports rebuilt structures

**Files:**
- Create: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Rebuilt.c` (class `DZOAdminRebuilt`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Hooks.c` (`DZOAdminRegistry.s_Rebuildables`; in `modded class BuildingBase` `EEInit`/`EEDelete`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c:211` (call after `DZOAdminFlags.Collect`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Version.c` (`"0.6.0"`), `servermods/dzo-admin/README.md`

**Interfaces:**
- Consumes: `DZOAdminMarker`, `DZOAdminKV`, `DZOAdminLayer` (`3_Game/Proto.c`); the `Layer`/`Prop` helper pattern of `Flags.c` (copy it as `protected static` into `DZOAdminRebuilt`; do not make `Flags.c` public for it).
- Produces: `static void Collect(array<ref DZOAdminMarker> result, array<ref DZOAdminLayer> layers)`; markers as in Global Constraints with `label` = the display name of the type (`GetDisplayName()`, falling back to the type), and props in this order: `parts: <built>/<total>`, `weakest: <part> <pct>%` (lowest health among built parts), `floor: <pct>%` (its decay threshold), and `decay: off` / `decay: worn down` / `sandstorm: decaying faster` when they apply, plus `lock: code lock` / `lock: combination lock` when `GetCodeLock(ATTACHMENT_SLOT_CODE_LOCK)` / `GetCombinationLock(ATTACHMENT_SLOT_COMBINATION_LOCK)` returns non-null. Whether a rebuilt house carries its lock in that slot or per door is a Step 1 question; report it only if Step 1 finds a lock there. Task 2 relies on `source == "rebuilt"` and the `ttl`.

- [ ] **Step 1: Spike on a real server** (dayz-dev `testing/local-server.md`: isolated tree, free ports, dzo-admin as `-servermod`). Use a temporary script in the spike tree only, never committed. It logs (a) every `cfgVehicles` type with a `Construction` class that is not `BaseBuildingBase`, and whether it is a `Well` (`GetWaterSourceObjectType() == EWaterSourceObjectType.WELL`); (b) whether static map buildings run `EEInit` and have a persistent id (`GetPersistentID`, all four zero = none); (c) for one rebuildable house, after `RebuildingSpawner.RebuildAllWoodenPartsFiltered`, the parts' `IsBuilt`, `CanPartDecay`, rate, threshold and zone health. Run this on stable and on experimental (`--exp`). Write the findings into the commit message of Step 7. If wells have no `Construction` on either build, keep the wells layer code, say so in the docs, and tell the user before going on.
- [ ] **Step 2: Registry.** `static ref array<BuildingBase> s_Rebuildables` in `DZOAdminRegistry`. In the existing `modded class BuildingBase`, `EEInit` (server only) inserts `this` when `ConstructionBase.Cast(GetConstructionBasic())` is non-null, and `EEDelete` removes it before `super`.
- [ ] **Step 3: `DZOAdminRebuilt.Collect`.** Per registered building: skip null, count parts, skip if none built. Compute the ttl:

```
coef = sandstorm active ? 1.5 : 1.0          // g_Game.GetWeather().GetSandstorm().IsActive()
ttl = none
if ConstructionBasic.CanConstructionDecay():
  for each built part with CanPartDecay() and a non-empty zone:
    hp = GetHealth(zone, "Health"); floor = threshold * GetMaxHealth(zone, "Health")
    rate = -GetPartDecayRate() * coef         // HP/s lost, > 0
    if hp > floor and rate > 0: ttl = min(ttl, ceil((hp - floor) / rate))
```

  Layer and props as in Interfaces.
- [ ] **Step 4: Call it** in `Service.c` after `DZOAdminFlags.Collect(s.markers, s.layers);`.
- [ ] **Step 5: Version 0.6.0, README** (module table: `Rebuilt.c`, the two layers).
- [ ] **Step 6: Verify on the real server**, stable and `--exp`. Rebuild two parts of a house with the spike helper (and a well if Step 1 found one). Check the script log has no errors, and that `GET /api/v1/instances/<i>/map/markers?layer=Rebuilt%20houses` of `dzo serve` returns the marker with `ttl` > 0 and the props. Set `disableBaseDecay: true` in `cfggameplay.json`, restart, and see `decay: off` with no ttl. Time `Collect` with `TickCount` around it and log it once at boot. Record the registered count and the milliseconds.
- [ ] **Step 7: Build the PBO** as earlier mod bumps did (`git log -3 -- servermods`, `dayz-mod-pack.sh build`).
- [ ] **Step 8: `make test-client`** and `make test-client ARGS=--exp`. Expected: pass.
- [ ] **Step 9: Commit** in the submodule (`rebuilt: report rebuilt houses and wells with their decay as markers; version 0.6.0`, with the Step 1 and Step 6 findings), then in dzo: `admin: dzo-admin 0.6.0 reports rebuilt houses and wells` (submodule pointer, PBO artefacts, `debian/changelog`, the `test-client` result in the message).

### Task 2: The map words the decay

**Files:**
- Modify: `internal/web/static/map.js:734-738` (`ttlText`)
- Modify: `internal/admin/proto.go:114` (the `Source` comment lists `flags`, `territory`, `rebuilt`)
- Modify: `docs/admin-map.rst` (layers section), `docs/web.rst` (Live map)
- Test: `internal/web/e2e_map_test.go`

**Interfaces:**
- Consumes: markers with `source: "rebuilt"` (Task 1).
- Produces: `ttlText(m)` returns `'worn down in ' + span(m.ttl)` for `source === 'rebuilt'`, and the flags text otherwise.

- [ ] **Step 1: Write the failing test** `TestE2ERebuiltHouseShowsItsDecay`: `e.hub.Sync` with a marker `{layer:"Rebuilt houses", source:"rebuilt", icon:"home", ttl: 7380, props:[{k:"parts",v:"3/12"},{k:"weakest",v:"wall_1 64%"}], visibility:"operator", x:5000, z:5000}`. Hovering it shows a tooltip containing `worn down in 2h 3m`, `parts: 3/12` and `weakest: wall_1 64%`. The layer control lists `Rebuilt houses`.
- [ ] **Step 2: Run it.** `make e2e ARGS='-run TestE2ERebuilt'`. Expected: FAIL on the "worn down" text.
- [ ] **Step 3: Implement** the `ttlText` branch.
- [ ] **Step 4: Run** `make e2e` and `make test`. Expected: PASS.
- [ ] **Step 5: Docs.** `docs/admin-map.rst`: the two layers, who sees them (operators and admins), what the props mean, and that "worn down" is the decay floor (the part stays, weakened), faster in a sandstorm, off with `disableBaseDecay`. Also that it needs dzo-admin 0.6.0, and the wells caveat from Task 1 Step 1 if it applies. `docs/web.rst` Live map: one sentence. Run `make docs`.
- [ ] **Step 6: Commit** `web: rebuilt houses and wells show when decay wears them down`.

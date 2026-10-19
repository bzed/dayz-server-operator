# Map flags and territories Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show every territory flag on the admin map (hover: time until it goes down), and with DayZ Expansion a thin circle per territory (hover/click: the territory name).

**Architecture:** The dzo-admin mod reports flags as ordinary markers, so no protocol change, no new endpoint and no new SSE kind: layer `Flags` (point, icon `flag`, `ttl` = seconds of refresher time left) and, with Expansion, layer `Territories` (shape `circle`, `label` = territory name, `radius`, `source` = `territory`). The map's `onMarkers` learns two things: a marker with a `ttl` shows "goes down in …" in its tooltip and menu, and a `territory` circle is drawn thin. The mod's version goes to 0.4.0 (its code changes, so its PBO is rebuilt; protocol stays 1).

**Tech Stack:** Enforce Script (dzo-admin, DayZ 1.30, optional Expansion), Go 1.x (`internal/admin`, `internal/web`), Leaflet in `internal/web/static/map.js`, Sphinx docs.

**Spec:** the request, verbatim: "vanilla / no expansion: show a flag on the map (fa-flag if possible) for each flagpole on the map, hovering over it shows the ttl / time for the flag to go down. expansion: add a thin circle for the territory size, popup: show the territory name."

## Global Constraints

- Icon `flag` (Fork Awesome `fa-flag`; `iconClass` in `map.js` already falls back to a dot if the font lacks it).
- Vanilla flag time is `TerritoryFlag.m_RefresherTimeRemaining`, an `int` in seconds (public field, checked in the 1.30 API with `dayz-api.py class TerritoryFlag`). It does not include the final 5-day refresh the game adds; the docs say so.
- Expansion code only under `#ifdef EXPANSIONMODBASEBUILDING`. Territory radius is `GetExpansionSettings().GetTerritory().TerritorySize`; name is `flag.GetTerritory().GetTerritoryName()`, valid when `flag.HasExpansionTerritoryInformation()`.
- Mod rules: no world scan (register in `EEInit`, remove in `EEDelete`, like `DZOAdminRegistry.s_Cars`); forward slashes; null-check everything.
- Flag and territory markers are `visibility: "operator"` (the lowest role that sees them: operators and admins, not viewers or moderators), set by the mod. Other mod markers stay `admin`.
- All text from the server reaches the DOM through `textContent` (existing `tooltip()`/`mk()`).
- PBO is rebuilt only because mod code changed (Task 1); Go, web and docs tasks do not rebuild it.
- `make check` passes (coverage ≥ 85%); every new file needs an SPDX header; `docs/` updated in the same commits.
- `servermods/dzo-admin` is its own git repo (submodule): commit there first, then the pointer in the dzo repo.

## Review Focus

- A flag with `m_RefresherTimeRemaining == 0` (refresher expired, flag about to decay): shown with "goes down: now", not hidden and not negative.
- A flag kit or a half-built flagpole: `TerritoryFlag` exists from the moment the pole is placed; it is still shown (it is a flagpole on the map).
- Expansion territory flag with an empty name, or a flag that is not a territory (`HasExpansionTerritoryInformation()` false): no circle, flag marker only.
- Two flags in one territory (Expansion allows one per territory, vanilla none): marker ids are per flag (`"f" + GetID()`), never collide.
- Very large `ttl` (40 days = 3 456 000 s) and values under a minute format sensibly (`span()` already returns `<1m`).

---

### Task 1: dzo-admin reports flags and territories as markers

**Files:**
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Hooks.c` (registry + `modded class TerritoryFlag`)
- Create: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Flags.c` (`DZOAdminFlags.Collect`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c:204` (call after `WatchMarkers`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Version.c` (`DZOADMIN_VERSION = "0.4.0"`), `servermods/dzo-admin/README.md` (module table, flags)

**Interfaces:**
- Consumes: `DZOAdminMarker` (`3_Game/Proto.c`), `DZOAdminKV`.
- Produces: markers on layer `Flags` (`id` `"f<GetID()>"`, `shape` `point`, `icon` `flag`, `color` `#c0392b`, `label` `Flag`, `ttl` = remaining seconds, `source` `flags`, `visibility` `operator`) and, only for Expansion territories, layer `Territories` (`id` `"t<GetID()>"`, `shape` `circle`, `radius` = territory size, `color` `#2f6fb5`, `label` = territory name, `source` `territory`, `visibility` `operator`). Task 2 and 3 rely on exactly these names.

- [ ] **Step 1: Registry and hook.** In `Hooks.c` add `static ref array<TerritoryFlag> s_Flags = new array<TerritoryFlag>;` to `DZOAdminRegistry`, and `modded class TerritoryFlag` overriding `EEInit` (server only: insert) and `EEDelete` (remove, then `super`), like `CarScript`.
- [ ] **Step 2: `static void Collect(array<ref DZOAdminMarker> result)` in `Flags.c`** (class `DZOAdminFlags`). One flag marker per non-null registered flag, `ttl = Math.Max(0, flag.m_RefresherTimeRemaining)`; a ttl of 0 means "no expiry" on the wire, so for an expired refresher send `ttl = 0` and a prop `k="refresher" v="expired"` (Task 2 shows "now" for that prop). Under `#ifdef EXPANSIONMODBASEBUILDING`, when `HasExpansionTerritoryInformation()` and the name is not empty, also add the circle marker and a prop `k="territory" v=<name>` on the flag marker.
- [ ] **Step 3: Call it** in `Service.c` right after `DZOAdminWatch.WatchMarkers(s.markers);`.
- [ ] **Step 4: Version 0.4.0** in `Version.c`; README: mention `Flags.c` and the two layers.
- [ ] **Step 5: Verify on a real server** with the `dayz-dev` skill (`testing/local-server.md`: isolated tree, `scripts/free-ports.sh`, dzo-admin as `-servermod`, wipe storage). Place a flag with the in-game debug or a mission-persisted one, then check the script log shows no errors and `GET /markers?layer=Flags` of `dzo serve` (or the sync body) has the marker with `ttl` > 0. Repeat once with Expansion loaded (`Territories` circle with the name; repeat with a vanilla-only tree: no `Territories` layer, no script errors). Record the evidence in the commit message.
- [ ] **Step 6: Build the PBO** as the previous mod releases did (`git log -3 -- servermods`, `dayz-mod-pack.sh build`); check script module counts match a vanilla boot.
- [ ] **Step 7: Commit** in the submodule (`flags: report territory flags and Expansion territories as markers; version 0.4.0`), then in the dzo repo `git add servermods/dzo-admin` plus the rebuilt PBO artefacts the earlier mod bumps committed, `admin: dzo-admin 0.4.0 reports flags and territories` (with `debian/changelog` entry).

### Task 2: Hub keeps `ttl` and the territory source (Go)

**Files:**
- Test: `internal/admin/hub_test.go`, `internal/api/api_test.go` (role filtering in `internal/api/server.go:270`, `visible(m.Visibility, role)`)
- Modify: `internal/admin/proto.go:114` (comment on `Source`: add `flags, territory`)

**Interfaces:**
- Consumes: `Marker{TTL, Source}` as above. Produces: nothing new; this task pins that `Hub.Markers` returns them unchanged.

- [ ] **Step 1: Write `TestHubKeepsFlagMarkers`** in the style of the existing hub tests: sync a request with a `Flags` marker (`TTL: 86400, Source: "flags", Icon: "flag"`) and a `Territories` circle (`Radius: 150, Source: "territory", Label: "Camp"`); give both `Visibility: "operator"` and assert `hub.Markers("Flags")[0].TTL == 86400`, `hub.Markers("Territories")[0].Radius == 150`, `Source`, `Visibility == "operator"`, and that `hub.Layers()` lists both layers.
- [ ] **Step 1b: Write `TestMarkersFlagsOperatorOnly`** in `internal/api/api_test.go` in the style of the existing marker visibility test: with those two markers, `/markers` as `viewer` and `moderator` returns neither, as `operator` and `admin` returns both, and `/layers` lists them only for those roles.
- [ ] **Step 2:** `go test ./internal/admin ./internal/api -run 'TestHubKeepsFlagMarkers|TestMarkersFlagsOperatorOnly' -v`. Expected PASS if nothing drops the fields (the task's job is to prove it; if it FAILS, fix `styledLocked`/`Layers` so the fields survive).
- [ ] **Step 3: Commit** `admin: test that flag and territory markers keep ttl, radius and source`.

### Task 3: Map draws flags with their time and territories thin

**Files:**
- Modify: `internal/web/static/map.js` (`onMarkers`, `bindTip` call, new `ttlText`)
- Modify: `internal/web/static/app.css` (thin territory circle class)
- Test: `internal/web/web_test.go` (static asset assertion)

**Interfaces:**
- Consumes: markers from Task 1 (`ttl`, `source`, `props`, `shape`, `radius`).
- Produces: `function ttlText(m): string` in `map.js`: `''` when `m.ttl` is 0 and no prop `refresher`=`expired`; `'goes down now'` for expired; else `'goes down in ' + span(m.ttl)`.

- [ ] **Step 1: Failing test** `TestMapScriptHandlesFlags` in `web_test.go`: read the embedded `static/map.js` the way the existing tests read static files; assert it contains `ttlText` and `'territory'`. Run `go test ./internal/web -run TestMapScriptHandlesFlags` → FAIL.
- [ ] **Step 2: `ttlText`** as specified (reuses `span`). In `onMarkers`, `lines = ttlText(m)` first, then the props; so tooltip and the context-menu `sub` show it. For `m.source === 'territory'` circles use `{ color, weight: 1, opacity: .9, fillOpacity: .04, className: 'terr' }` instead of weight 2 / fillOpacity .15, and `bindTip` title = `m.label` (the name; falls back to `m.id`). Keep the click menu (`plainSpec`, zoom to the radius).
- [ ] **Step 3:** CSS `.terr { stroke-width: 1px; }` next to `.ev` if Leaflet's `weight` needs the class for `vector-effect`; otherwise omit.
- [ ] **Step 4: Run** `go test ./internal/web` → PASS; open the map against the Task 1 test server (dayz-dev skill) and check by eye: flag icon on each flag with "goes down in 23d 4h" on hover, thin blue circle with the territory name on hover/click, layers toggle in the layer control.
- [ ] **Step 5: Commit** `web: flags with their remaining time and thin Expansion territory circles on the map`.

### Task 4: Docs and full check

**Files:**
- Modify: `docs/admin-map.rst` (new section "Flags and territories" after "Active events"; mention in the mod's feature list near line 11–35), `debian/changelog` (if not done in Task 1)

- [ ] **Step 1:** Document: layers `Flags` and `Territories`, what the time means (refresher time left, the game adds one final 5-day refresh), Expansion-only circles with the configured `TerritorySize`, visibility `operator` (operators and admins only), mod version 0.4.0 needed (an older mod simply shows nothing).
- [ ] **Step 2:** `make docs` (warnings are errors), then `make check`; expected: all steps pass, coverage ≥ 85%.
- [ ] **Step 3: Commit** `docs: flags and territories on the admin map`.

---

## Self-review

- Spec: vanilla flag icon + time on hover (Tasks 1, 3); Expansion thin circle + name (Tasks 1, 3). Covered.
- Types: ids `f<ID>`/`t<ID>`, layers `Flags`/`Territories`, sources `flags`/`territory`, prop `refresher=expired` are used identically in Tasks 1–3.
- Visibility: both layers are operator-only (Task 1 sets it, Task 2 pins the role filtering, Task 4 documents it).
- Open point for review: "time to go down" is refresher time left. If you would rather include the final 5-day refresh, add `GameConstants.REFRESHER_FREQUENCY_DEFAULT` to `ttl` in Task 1 Step 2 (one line).

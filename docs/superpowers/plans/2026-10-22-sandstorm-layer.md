<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Sandstorm on the map Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Load the `dayz-dev` skill before Task 1 (AGENTS.md). If the rebuilt-structures plan has not shipped yet, do both mod changes in one mod release (0.6.0). Otherwise this is 0.7.0.

**Goal:** While a sandstorm is active, the admin map shows where it is as a thin sand-coloured wash, denser where the storm is stronger, plus one marker at the storm's centre giving the direction, speed and time until it reaches its end.

**Architecture:** The dzo-admin mod samples the storm on a grid each markers tick and sends it as ordinary markers, so the protocol and API stay the same. Each grid row's runs of cells above an intensity band become one rectangle polygon, in two bands (light, heavy), on the layer `Sandstorm` with `source: "sandstorm"`, plus one point marker at `GetPosition()`. `map.js` draws `sandstorm` polygons without outline, with low opacity, non-interactive and below every other marker, and gives the centre marker an arrow rotated to the storm's heading.

**Tech Stack:** Enforce Script (DayZ 1.30), Leaflet `map.js`, Sphinx.

**Spec:** the request: "sandstorm: can we add a thin "sand" layer on the map with the location of the storm (if there is such data available)".

## What the game offers (1.30 build 25963285, `3_Game/Sandstorm.c`, checked with `dayz-api.py class SandstormController`)

`g_Game.GetWeather().GetSandstorm()` returns the `SandstormController` (`typedef SandstormController Sandstorm`). Every call below except `IsActive` and `GetMagnitude` requires an active storm:

| Call | Meaning (from the doc comments) |
|---|---|
| `bool IsActive()` | "True for as long as there is sandstorm active somewhere" |
| `vector GetPosition()` | current position of the storm in world space |
| `vector GetDirection()` | direction from its start position to its end position |
| `float GetSpeed()` | m/s it advances |
| `float GetRemainingMovementDuration()` | seconds until it reaches its end position at its current speed |
| `bool IsPositionAtEnd()` | it has arrived |
| `float GetIntensity(vector worldPos)` | intensity at a point |
| `float GetPointOnEdge(vector worldPos, out vector edge)` | distance to the storm's edge, negative inside |
| `float GetMagnitude()` | peak intensity, independent of position |

Nothing says what shape the storm has (a disc, a band, a front). That is why the plan samples `GetIntensity` instead of drawing a shape it would have to assume. The world size comes from `GetGame().GetWorld().GetWorldSize()`. How often storms happen is `cfggameplay.json` `SandstormData.sandstormFrequency`. Storms are expected on Nasdara; Task 1 Step 1 checks whether other maps can have one.

## Global Constraints

- Mod rules: no per-frame work. Sample only in the markers tick (`markers_s`, default 10 s) and only while `IsActive()`. Null-check `GetWeather()` and `GetSandstorm()`. Forward slashes.
- Grid: 48 × 48 cells over `GetWorldSize()` (320 m cells on a 15 360 m world), sampled at cell centres at height 0 (`Vector(x, 0, z)`; Step 1 checks that height does not matter, otherwise use `GetGame().SurfaceY`). That is 2 304 `GetIntensity` calls per tick; Task 1 Step 6 measures them, and the grid drops to 32 × 32 if a tick costs more than 5 ms.
- Bands: light `≥ 0.15`, heavy `≥ 0.5` of `GetMagnitude()`-relative intensity (`GetIntensity / max(GetMagnitude, 0.01)`); Step 1 may adjust both values, and the spike output must justify any change. The heavy band is drawn on top of the light one, not cut out of it.
- Layer `Sandstorm`: icon `asterisk`, colour `#c8a464`, `visibility: "viewer"` (weather is not secret). Polygon ids `s<band><row>_<run>`. Centre marker id `storm`, `label` "Sandstorm", props `heading: <compass, e.g. NE (47°)>`, `speed: <km/h>`, `strength: <round(GetMagnitude*100)>%`, `ttl` = `ceil(GetRemainingMovementDuration())` while > 0, otherwise prop `moving: arrived`.
- `map.js` draws polygons with `source === 'sandstorm'` as `{stroke: false, fillColor: color, fillOpacity: band === 'heavy' ? 0.28 : 0.14, interactive: false, pane: 'sandPane'}`. The band comes from a prop `band`. `sandPane` is a custom Leaflet pane below `overlayPane` (z-index 350). No animation. With `prefers-reduced-motion` nothing changes, because there is no motion.
- dzo-admin version bump, PBO rebuild, `make test-client` stable and `--exp`, submodule commit first, `debian/changelog`.

## Review Focus

- A world without sandstorms (Chernarus): `GetSandstorm()` may be null, or `IsActive()` always false. No layer is sent, no error is logged, and the Layers control shows no "Sandstorm" entry.
- A storm that is fading in or out (low intensity everywhere): nothing above the light band means no polygons, but the centre marker still shows while `IsActive()`.
- A storm that has arrived (`IsPositionAtEnd()`): no ttl, prop `moving: arrived`, and the map says "arrived", not "<1m".
- `GetDirection()` is (0,0,0): heading prop omitted, and the arrow is not drawn (no NaN rotation).
- A storm that ends between two ticks: the next markers sync has no Sandstorm markers, and `onMarkers` clears the layer (it already clears all layers each time).

---

### Task 1: dzo-admin samples the storm

**Files:**
- Create: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Sandstorm.c` (class `DZOAdminSandstorm`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c` (call after the flags and rebuilt collectors), `3_Game/DZOAdmin/Version.c`, `README.md`

**Interfaces:**
- Consumes: `DZOAdminMarker`, `DZOAdminPoint`, `DZOAdminKV`, `DZOAdminLayer`.
- Produces: `static void Collect(array<ref DZOAdminMarker> result, array<ref DZOAdminLayer> layers)`. Polygon markers have `shape "polygon"`, four `points` (the run's rectangle corners), props `band: light|heavy`, `source "sandstorm"`. The centre marker is as in Global Constraints. Task 2 relies on `source`, the `band` prop, and the `heading` prop's degrees in parentheses.

- [ ] **Step 1: Spike on a real Nasdara server** (dayz-dev `testing/local-server.md`; a temporary script in the spike tree only). Start a storm with `GetSandstorm().Start(30, false)`. Every 10 s, log `GetPosition`, `GetDirection`, `GetSpeed`, `GetRemainingMovementDuration`, `GetMagnitude`, and `GetIntensity` on a 16 × 16 grid at height 0 and at surface height. Also log `GetPointOnEdge` for the same points. From that, write down the storm's shape and size, whether height matters, and sensible band thresholds. On Chernarus (stable and `--exp`), log whether `GetSandstorm()` is null and what `IsActive()` returns. Keep the log excerpt for the commit message.
- [ ] **Step 2: Sampling** in `Collect`: return at once unless the controller is non-null and active. Walk the grid row by row, tracking runs of cells at or above each band, and emit one rectangle per run and band.
- [ ] **Step 3: Centre marker** with the props; heading from `GetDirection()` as `Math.Atan2(dir[0], dir[2]) * Math.RAD2DEG` normalised to 0–360 (0 = north, +z), skipped for a zero vector.
- [ ] **Step 4: Call it** in `Service.c` after the other collectors. Version bump and README (module table, layer).
- [ ] **Step 5: Verify on the real server**, stable and `--exp`, on Nasdara with a started storm. The script log has no errors, and `GET /api/v1/instances/<i>/map/markers?layer=Sandstorm` returns polygons and the `storm` marker. Chernarus: no Sandstorm markers and no errors.
- [ ] **Step 6: Measure** `Collect` with `TickCount` once per minute while active (removed or behind the mod's debug flag before committing). Record the milliseconds. Use a 32 × 32 grid if it exceeds 5 ms.
- [ ] **Step 7: PBO, `make test-client`** (stable and `ARGS=--exp`), commits: submodule `sandstorm: report an active sandstorm as a sampled area and a centre marker`, then dzo `admin: dzo-admin reports the sandstorm`, with the spike findings and the measurement.

### Task 2: The map draws the sand

**Files:**
- Modify: `internal/web/static/map.js` (`onMarkers`: the `sandstorm` source; create `sandPane` once after the map), `internal/web/static/app.css` (`.storm` arrow marker)
- Modify: `internal/admin/proto.go:114` (`Source` comment adds `sandstorm`), `docs/admin-map.rst`, `docs/web.rst`
- Test: `internal/web/e2e_map_test.go`

**Interfaces:**
- Consumes: Task 1 markers.
- Produces: in `onMarkers`, a `source === 'sandstorm'` polygon is drawn in `sandPane` with the style from Global Constraints and no tooltip or menu. The centre marker uses `markerIcon` with an extra child `<i class="fa fa-long-arrow-up storm-dir">`, rotated with `style.transform = 'rotate(' + deg + 'deg)'`, where `deg` is parsed from the `heading` prop's parentheses. Its `ttlText` reads "reaches its end in …" for this source.

- [ ] **Step 1: Write the failing test** `TestE2ESandstormDrawsAQuietArea`: `e.hub.Sync` with two polygons (light and heavy) and the `storm` marker (`heading: NE (47°)`, ttl 1500). Assert: two `path` elements in `.leaflet-pane.leaflet-sandPane-pane` (Leaflet's pane class naming), neither with `leaflet-interactive`. Hovering the centre marker shows "reaches its end in 25m" and `heading: NE (47°)`. The arrow element has `rotate(47deg)`.
- [ ] **Step 2: Run it.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `make e2e` and `make test`. Look at a screenshot (dark and light) to check the wash is visible but leaves players and roads readable. Adjust only the two opacities if not.
- [ ] **Step 5: Docs:** `docs/admin-map.rst` (the Sandstorm layer: what the wash and the two strengths mean, that it is sampled on a 320 m grid so the edge is approximate, the centre marker, that it needs the dzo-admin version from Task 1, and that only maps with sandstorms show it); `docs/web.rst` Live map: one sentence. Run `make docs`.
- [ ] **Step 6: Commit** `web: show an active sandstorm on the map`.

<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Map teleport drag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Task 1 also needs superpowers:systematic-debugging.

**Goal:** Dragging a player's icon on the admin map follows the pointer from start to drop, at any zoom level and across other icons, and the dropped icon stays where it was dropped until the teleport is confirmed or cancelled.

**Architecture:** Only `internal/web/static/map.js` (plus a few CSS lines) changes. A player entry in `pm` gets an explicit drag lifecycle (`idle → held → dragging → pending → idle`) instead of the single `e.drag` field, and the stream handler `onPlayers` leaves an entry alone in every state except `idle`. Browser e2e tests (chromedp, real Leaflet) reproduce each symptom first.

**Tech Stack:** Leaflet (Debian `libjs-leaflet`), plain ES5 in `map.js`, chromedp e2e tests (`//go:build e2e`).

**Spec:** the request: "check the teleport function on the map, it seems that pulling a player stops tracking when you come across other items and it seems to be a bit flaky on higher zoom levels."

## Findings from the code review (`internal/web/static/map.js`)

These are confirmed by reading the code. Task 1 checks them in a browser before anything is fixed:

1. **Updates between pointer-down and `dragstart` move the icon away from the pointer.** `e.drag` is set only in `dragstart`, and Leaflet fires that only after the pointer has moved past its click tolerance. A `players` event in that window runs `e.m.setLatLng(ll(p))` (line 639) and, if the health bucket changed, `e.m.setIcon(...)` (line 641). `setIcon` replaces the icon element that the `L.Draggable` is bound to, so the drag never starts or stops partway. That fits "stops tracking". At high zoom one second of walking is many pixels, so the jump is large and the drag looks "flaky on higher zoom levels".
2. **After the drop the icon snaps back to the player before the confirmation.** `dragend` sets `e.drag = null` (line 612) and then opens the "Teleport …?" menu. The next `players` event (every sync) moves the icon back to the player's live position while the menu still asks.
3. **Any pan or zoom cancels a pending drop.** `map.on('movestart zoomstart', closeMenu)` (line 419) calls the menu's `dismiss`, which puts the icon back. A mouse-wheel zoom to check the drop point (the natural thing to do at high zoom) throws the drop away.
4. **A failed teleport puts the icon at `from`,** the position at drag start, which is stale by then. It should go to the player's latest reported position.
5. **Other icons react while the drag passes over them.** They open their tooltips and rise (`riseOnHover`). This is the likeliest "come across other items" trigger after finding 1. Task 1 decides whether it actually breaks the drag.

## Global Constraints

- No new dependency. `map.js` stays ES5 (`var`, no arrow functions), as it is now. All server text goes in through `textContent`.
- Works with the Leaflet that Debian trixie ships (the CI e2e container installs `libjs-leaflet`) and keeps working with 1.7 (`ElIcon` comment).
- Keyboard and menu teleport ("Teleport to coordinates…", "Teleport a player here…") keep working unchanged.
- `prefers-reduced-motion` is still respected (no new animation).
- `docs/admin-map.rst` and `docs/web.rst` describe the drag behaviour in the same commit as the fix.
- `make check` and `make e2e` pass.

## Review Focus

- The player disconnects while the drop confirmation is open: the menu closes, nothing is sent, and no exception is thrown (`pm[id]` is gone).
- The player dies or enters a vehicle during the drag (`playerState` changes, so `setIcon` would be due): the icon is not replaced until the entry is idle again, then it gets the new state.
- A viewer or moderator (`draggable: false`): pointer-down on the icon must not enter `held` and freeze the icon. Only draggable markers take part in the lifecycle.
- A plain click (pointer-down and pointer-up with no movement) opens the player menu as today and leaves the entry `idle` afterwards, so live updates resume.
- A drop outside the world bounds (dragged into the padded `maxBounds` area): the confirmation shows the clamped coordinates, the same rule `focusFromLink` applies: 0 ≤ x, z ≤ `size`.

---

### Task 1: Reproduce in the browser

**Files:**
- Create: `internal/web/e2e_drag_test.go` (`//go:build e2e`)
- Modify: `internal/web/e2e_test.go` (add the drag helper next to `clickUntil`)

**Interfaces:**
- Produces: `func (b *browser) drag(t *testing.T, sel string, path [][2]float64, steps int)`. It presses the left button on the centre of the first element matching `sel`, moves through `path` (container-relative pixel offsets from the start) in `steps` interpolated `mouseMoved` events with 16 ms between them (`chromedp.Sleep` is allowed here because it drives the input and does not wait for a result), then releases. It uses `github.com/chromedp/cdproto/input` `DispatchMouseEvent`. Also `func (b *browser) mapZoom(z float64)`, which evaluates `document.getElementById('map')._dzoMap.setZoom(z, {animate:false})`.
- `map.js` exposes the map for tests only as `el._dzoMap = map` (an element property, no global). The helper reads `document.getElementById('map')._dzoMap`.

- [ ] **Step 1: Write the failing tests.** They use the env from `e2e_map_test.go` (one player `7656` at a known position, four vehicles). Put one vehicle on the drag path with `e.hub.Sync`.
  - `TestE2EDragAcrossAVehicleFollowsThePointer`: drag 200 px east across the vehicle. Assert the confirmation menu is open (`.ctx:not([hidden])`) and its text contains the x of the drop point within 2 m (computed with `map.containerPointToLatLng` through `Evaluate`).
  - `TestE2EUpdateBeforeDragStartDoesNotBreakTheDrag`: press the button, call `e.hub.Sync` with the player 50 m away and a different health (forces `setIcon`), then move and release. Assert that the confirmation opens at the pointer.
  - `TestE2EDropStaysUntilConfirmed`: drop, then `e.hub.Sync` with the player at its old place twice. Assert `markerStyle(".pm-wrap")` is unchanged and the menu is still open.
  - `TestE2EZoomKeepsAPendingDrop`: drop, `mapZoom(current+1)`. Assert the menu is still open and Teleport sends `x`/`z` of the drop (`e.commandsOf("teleport") == 1` after clicking the submit).
  - `TestE2EDragAtMaxZoom`: `mapZoom(map.getMaxZoom())`, then the same drag across another player marker. Assert as in the first test.
- [ ] **Step 2: Run them and record which fail and how.** Run `make e2e ARGS='-run TestE2EDrag|TestE2EUpdate|TestE2EDrop|TestE2EZoom'` (or the `go test -tags e2e` line the Makefile uses). Expected: findings 1–3 fail. If the vehicle-crossing test fails although the pointer-down update is not involved, finding 5 is real: note the Leaflet version (`L.version`) and which event stops arriving (add a temporary `console.log` of `drag` events and read it with chromedp's console listener), then delete the logging.
- [ ] **Step 3: Commit the failing tests on the branch** (they are skipped in CI only by the e2e build tag, so commit them together with Task 2 if the branch must stay green: `git commit` after Task 2 Step 4).

### Task 2: Explicit drag lifecycle in `map.js`

**Files:**
- Modify: `internal/web/static/map.js:567-647` (`teleport`, `makePlayer`, `onPlayers`) and `:419` (`movestart zoomstart`)
- Modify: `internal/web/static/app.css` (the `.dragging-player` rule)
- Modify: `docs/admin-map.rst`, `docs/web.rst` (Live map paragraph)

**Interfaces:**
- Consumes: Task 1 tests.
- Produces: the entry shape `{ m, data, key, state: 'idle'|'held'|'dragging'|'pending', from: LatLng|null, to: {x,z}|null }`, with `e.drag` removed. `function live(e)` reports `e.state === 'idle'`. `function settle(e)` sets the state to `idle`, then applies `e.data` (position, icon if the key changed, tooltip), exactly what `onPlayers` does today for an idle entry. Factor that into `function apply(e)` and call it from both places.

- [ ] **Step 1: `held` on pointer-down.** Only when `can('operator')`: in `makePlayer` attach `pointerdown` (and `mousedown`/`touchstart` for 1.7) on the icon element after `add` (the element exists then; re-attach in `m.on('add')`). The handler sets `state = 'held'`. A `pointerup`/`mouseup` on `document` that arrives while the entry is still `held` (no drag happened, so it was a click) calls `settle(e)`.
- [ ] **Step 2: `dragging` and `pending`.** `dragstart` sets `state = 'dragging'` and `from = m.getLatLng()`, and adds the class `dragging-player` to `#map`. `dragend` clamps the drop to `[0,size]` on both axes, sets `state = 'pending'` and `to`, removes the class and opens the confirmation as today. Confirm calls `teleport(e.data, to)`. On success put the marker at `to`, then `settle`. On failure `settle` alone, so the icon goes to the latest reported position, not `from` (finding 4). Dismiss calls `settle`.
- [ ] **Step 3: `onPlayers` skips non-idle entries.** It sets `e.data = p` always and calls `apply(e)` only when `live(e)`. When a player that is `pending` or `held` leaves the list, close the menu if it belongs to that player (keep a reference `cur.owner = e` in `openMenu`) and remove the marker as today.
- [ ] **Step 4: A pending drop survives pan and zoom.** Change line 419 so `movestart zoomstart` close the menu only when no entry is `pending`. On `moveend`, re-anchor an open pending confirmation at `map.latLngToContainerPoint(m.getLatLng()).add([20,-10])` (call `place()` with the new `cur.pt`).
- [ ] **Step 5: Other icons ignore the pointer during a drag.** Only if Task 1 Step 2 showed finding 5. In `app.css`: `#map.dragging-player .leaflet-marker-icon:not(.leaflet-drag-target), #map.dragging-player .leaflet-interactive { pointer-events: none; }` and `#map.dragging-player .leaflet-tooltip { display: none; }`. If Leaflet does not set `leaflet-drag-target` on the dragged icon (check `L.version`), add the class `pm-src` to it in `dragstart` and use that.
- [ ] **Step 6: Run the e2e tests and the unit tests.** `make e2e` and `make test`. Expected: every Task 1 test passes, plus the existing `TestE2EMap*`.
- [ ] **Step 7: Docs.** `docs/admin-map.rst` (drag section) and `docs/web.rst` (Live map): the icon stops following live updates from the moment you press it until you confirm or cancel; you can pan and zoom to check the drop point before confirming; if the game server refuses, the icon goes back to where the player is now. Run `make docs`.
- [ ] **Step 8: Commit.** `make check`, then:

```bash
git add internal/web/static/map.js internal/web/static/app.css internal/web/e2e_drag_test.go internal/web/e2e_test.go docs/admin-map.rst docs/web.rst
git commit -m "web: a dragged player stays under the pointer and at its drop point until confirmed"
```

`make test-client` is not needed: the teleport action and its API are unchanged, only the browser side changes.

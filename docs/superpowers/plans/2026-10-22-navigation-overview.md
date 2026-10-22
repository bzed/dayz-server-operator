<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Navigation and overview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Use frontend-design for the CSS of Task 2.

**Goal:** Every page of the web interface reaches every other page in one click. The dashboard shows what you currently have to open the map to see: world, in-game time, uptime, next restart, mod version.

**Architecture:** The layout gets two levels of navigation, both server-rendered and without JavaScript. The header keeps the brand and adds an **instance switcher** (a `<details>` menu of every backend's instances) and the installation-wide pages (players, bans, audit). Every instance page (overview, map, events) gets an **instance bar** with three tabs and the instance's live facts. The instance lists come from a small per-backend cache in `web.Server`, so a page render does not add one API round trip per backend. The dashboard and the instance bar render the same `admin.Status` fields through one template, `"facts"`.

**Tech Stack:** Go `html/template`, htmx (unchanged), CSS in `internal/web/static/app.css`, chromedp e2e.

**Spec:** the request: "navigating is sometimes a bit cumbersome as not every page is reachable from every page. Some information is missing in the overview while you need to find it in the map's header."

## Findings from the UI review

| Page | Reaches | Cannot reach |
|---|---|---|
| Dashboard `/` | instance, map, per-backend audit and bans | events (only through the instance) |
| Instance | map, events, (header) audit, bans | other instances, dashboard only through the brand |
| Map | instance ("players and vehicles"), events | other instances. The header is the same, but the map's own links sit in a different place (`.maptitle`) |
| Events | instance, map | other instances |
| Bans, ban, ban form, audit | (header) | any instance. Audit's "Instance" column and a ban's instances are plain text |

- The header nav repeats "<backend> audit" and "<backend> bans" once per backend. With three installations that is six links and no instances.
- The dashboard row has players and vehicles only. The map title has in-game time with sun or moon, players online, uptime (`hello.since`) and next restart (`next_restart`), and these are only drawn by `map.js`. The instance page has the mod version and world in a sentence, but no time, uptime or restart.
- Section names drift: "Open map", "Open live map", "Live map", "players and vehicles", "Players and vehicles". One name per destination from now on: **Overview**, **Map**, **Events**, **Players** (the player database plan), **Bans**, **Audit log**.
- The `Profile` button in the player panel is disabled. The player database plan enables it; this plan only reserves the "Players" header link (shown once that plan's route exists, see Task 1).

## Design

The existing identity stays: "a survey map under a desk lamp" (`app.css` palette, Barlow Condensed for names, Source Sans 3 for text, flare red only for danger). Nothing is re-themed. The new elements borrow from the same subject: a sheet index on the edge of a printed map.

- **Colour:** existing tokens only: `--head` and `--head-ink` for the header, `--surface` for the instance bar, `--online` and `--muted` for the state dot, `--line` for rules. No new hex values.
- **Type:** instance names and tabs in `--display` 600, at the h2 size. The facts in `--text` with `font-variant-numeric: tabular-nums`, so clocks and durations do not jitter as they update.
- **Layout:**

```
┌ header (dark) ──────────────────────────────────────────────────────────────┐
│ ⌖ dzo   [▾ alpha · chernarusplus ]   Players  Bans  Audit log      docs  v │
└─────────────────────────────────────────────────────────────────────────────┘
┌ instance bar (surface, only on instance pages) ─────────────────────────────┐
│ alpha  ● connected   ☀ 14:05  12 online  up 3h 12m  restart in 47m  mod 0.6 │
│ Overview   Map   Events                  (active tab: underline in --ink)    │
└─────────────────────────────────────────────────────────────────────────────┘
```

  With more than one backend, the switcher groups instances under the backend name, and "Players / Bans / Audit log" link to the current page's backend (the dashboard: the first backend, with the others in the switcher's footer). Content is left-aligned. Below 700 px the facts wrap under the name and the tabs become a full-width row of three equal cells.
- **Principle:** the instance bar is the one bold element. It is the same on overview, map and events, so moving between them changes only what is under it. On the map page it replaces `.maptitle`, and the map fills what is left of the window.

Reviewed against a generic admin-dashboard default: no sidebar, no cards, no all-caps labels, no breadcrumbs (the switcher plus tabs already say where you are). The dot keeps its existing meaning (connected).

## Global Constraints

- No new JavaScript dependency. The switcher is `<details>`/`<summary>`. Closing it on outside click is a progressive enhancement in `rows.js` (no inline script; the CSP forbids it).
- The page works with the backend unreachable: switcher entries for that backend show "unreachable" and the page still renders (today's `backendView.Err` behaviour).
- The cache never serves another user's role data: it holds instance names, connection state and world only, the same for every viewer of the web process.
- `aria-current="page"` on the active tab and on the active switcher entry. Visible focus on every link (existing `:focus-visible` rules).
- `make check` and `make e2e` pass. `docs/web.rst` is updated in the same commits.

## Review Focus

- A backend whose `Instances` call fails or times out: the header renders, the switcher shows the backend with "unreachable", and the dashboard shows the error as today.
- An instance that exists in the site but whose mod never connected (`Hello == nil`): the facts show "not connected" only, not "up 0m" or an empty clock.
- `next_restart` in the past (the restart is overdue or running): "restart now", as `map.js` says.
- Instance names that need escaping in a URL (`html/template` escapes them in `href`). Test an instance named `a b` in the cache test.
- A narrow phone (360 px): no horizontal scroll on any page, and the map is still at least 60 % of the window height.

---

### Task 1: Navigation data

**Files:**
- Create: `internal/web/nav.go`
- Modify: `internal/web/server.go` (`page` struct, `page()` builder, `Server` fields)
- Modify: `internal/web/handlers.go`, `internal/web/bans.go`, `internal/web/events.go` (pass instance and section)
- Test: `internal/web/nav_test.go`

**Interfaces:**
- Produces:

```go
type navInstance struct{ Name, World string; Connected bool }
type navBackend struct{ Name string; Instances []navInstance; Err string }

// nav returns the instance lists of all backends, at most navTTL old.
func (s *Server) nav(ctx context.Context) []navBackend
const navTTL = 15 * time.Second
```

  `page` gains `Nav []navBackend`, `Instance string`, `Section string` (`"overview"`, `"map"`, `"events"`, `"players"`, `"bans"`, `"audit"`, `"dashboard"`) and `HasPlayers bool` (false until the player database plan sets it). The constructor becomes `func (s *Server) page(r *http.Request, title, backend, instance, section string, data any) page`.
- The cache is a `sync.Mutex`-guarded struct on `Server` with `at time.Time` and the slice, and a `now func() time.Time` field for tests (default `time.Now`). Fetch all backends concurrently, each with a `context.WithTimeout(ctx, 3*time.Second)`. A failed fetch keeps that backend's previous list and sets `Err`.

- [ ] **Step 1: Write the failing tests** in `nav_test.go`, using `newEnv`:
  - `TestNavIsCachedForTheTTL`: two `GET /` calls within the TTL make one `Instances` API call (count the fake API's requests). After the fake clock passes `navTTL` there is a second call.
  - `TestNavKeepsTheLastListWhenABackendFails`: the first fetch succeeds, then the fake API returns 500. `nav()` still lists the instances, with `Err` set.
  - `TestEveryPageHasTheSwitcher`: `GET` of `/`, the instance page, the map, events, bans, a ban page, the ban form and audit each contain `href="/b/main/i/alpha"` inside `<details class="switch"`.
- [ ] **Step 2: Run them.** `go test ./internal/web -run 'TestNav|TestEveryPage'`. Expected: FAIL (`s.nav undefined`).
- [ ] **Step 3: Implement `nav.go`** and switch every `s.page(...)` call to the new signature with the right section.
- [ ] **Step 4: Run** `go test ./internal/web`. Expected: PASS.
- [ ] **Step 5: Commit** `web: navigation data for every page, cached per backend`.

### Task 2: Header switcher, instance bar, one name per page

**Files:**
- Modify: `internal/web/templates/layout.html` (`head` template: header, plus a new `"instbar"` template)
- Modify: `internal/web/templates/instance.html`, `map.html`, `events.html` (use `instbar`, drop their own link rows and `.maptitle` links)
- Modify: `internal/web/templates/audit.html` (instance column links to the instance), `bans.html` (scope instances link to the instance)
- Modify: `internal/web/static/app.css` (header switcher, `.instbar`, map page height), `internal/web/static/rows.js` (close an open `<details class="switch">` on outside click and on Escape)
- Modify: `internal/web/static/map.js:787-824` (stats now live in the instance bar: same element ids, so `drawStats` keeps working; check the selectors)
- Test: `internal/web/web_test.go`, `internal/web/e2e_pages_test.go`

**Interfaces:**
- Consumes: `page.Nav`, `page.Instance`, `page.Section` (Task 1); the `"facts"` template (Task 3; until then `instbar` renders name and tabs only).
- Produces: template `"instbar"`, called as `{{template "instbar" .}}` with the `page`. Tab hrefs: `/b/{b}/i/{i}`, `/b/{b}/i/{i}/map`, `/b/{b}/i/{i}/events`.

- [ ] **Step 1: Write the failing tests:**
  - `TestInstanceBarMarksTheActiveTab` (web_test): the map page has `aria-current="page"` on the Map tab only, and the events page on the Events tab only.
  - `TestAuditLinksTheInstance`: an audit entry for `alpha` renders `<a href="/b/main/i/alpha">alpha</a>`.
  - `TestMapPageHasNoSecondLinkRow`: the map page has no `.maptitle a`.
  - `TestE2ESwitcherReachesAnotherInstance` (e2e, two instances in the env): open the switcher on the events page of `alpha`, click `beta`, and land on `beta`'s overview. Press Escape on an open switcher and it closes.
  - `TestE2ENoHorizontalScrollOnAPhone`: at 360×740, for `/`, the instance page, map, events and bans, `document.documentElement.scrollWidth <= 360`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement** the templates and CSS following the Design section. Rename the link texts to the single names (Overview, Map, Events, Bans, Audit log). The map page's body height is `100dvh` minus the header and instance bar: make `body.map` a column flex with `main` as `flex: 1` (as now) and the bar as a normal flex child.
- [ ] **Step 4: Run** `go test ./internal/web` and `make e2e`. Expected: PASS, including the existing `TestE2EMap*` tests.
- [ ] **Step 5: Screenshot check.** Take chromedp screenshots of dashboard, overview, map and events at 1280 and 360 px, light and dark (`prefers-color-scheme` emulation), into a `mktemp -d /tmp/dzo-shots.XXXX` directory. Look at them, and fix overlaps or misalignment before committing.
- [ ] **Step 6: Docs.** `docs/web.rst`: a short "Finding your way" section before the page list (the switcher, the three tabs, header links). Rename the page entries to the single names. Run `make docs`.
- [ ] **Step 7: Commit** `web: instance switcher and instance bar on every page`.

### Task 3: Overview facts on the dashboard and the instance bar

**Files:**
- Modify: `internal/web/server.go` (template funcs `ago`, `until`)
- Modify: `internal/web/templates/fragments.html` (new `"facts"` template), `dashboard.html`, `layout.html` (`instbar` calls `facts`)
- Modify: `internal/web/handlers.go` (`dashboard` passes a `now`; the instance, map and events handlers fetch `Status` once and pass it to the bar)
- Test: `internal/web/web_test.go`

**Interfaces:**
- Produces:

```go
// span formats a duration like map.js span(): "<1m", "12m", "3h 12m", "2d 4h".
func span(d time.Duration) string
```

  Template funcs `"ago" func(now, t time.Time) string` (returns `span(now.Sub(t))`) and `"until" func(now time.Time, t *time.Time) string` (`"now"` when `t` is not after `now`, else `"in " + span`). Template `"facts"` takes `struct{ S admin.Status; Now time.Time }` (build it with a `facts` func in the FuncMap: `"facts" func(s admin.Status, now time.Time) factsView`). Each fact has a `class` matching the existing `map.js` ids (`st-clock`, `st-players`, `st-up`, `st-restart`), so the live map keeps updating them in place.

- [ ] **Step 1: Write the failing tests:**
  - `TestSpanMatchesTheMap`: table of `0s→"<1m"`, `59s→"<1m"`, `12m→"12m"`, `3h12m→"3h 12m"`, `50h→"2d 2h"`, the same as `map.js` `span()`.
  - `TestDashboardShowsTheFacts`: hub with `Hello{World:"chernarusplus", ModVersion:"0.6.0", Since: now-3h12m}`, game time `14:05`, next restart in 47 min. The dashboard row contains `chernarusplus`, `14:05`, `up 3h 12m`, `restart in 47m`, `0.6.0`.
  - `TestFactsWithoutTheMod`: `Hello == nil` gives "not connected" and none of `up `, `restart`, the clock.
  - `TestRestartOverdueSaysNow`: `NextRestart` one minute ago gives `restart now`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Dashboard columns: Instance, State, World, Time, Players, Vehicles, Up, Next restart, Mod. The Time column has the sun/moon glyph from the hour, as `drawStats` does. On ≤700 px only Instance, State, Players and Next restart stay visible (existing `.rows` mobile pattern). Drop the per-row "Open map" button: the instance name links to Overview, and a map icon link in the row goes to Map, with an accessible name.
- [ ] **Step 4: Run** `make test` and `make e2e`. Expected: PASS.
- [ ] **Step 5: Docs.** `docs/web.rst` Dashboard entry lists the columns; the Live map entry says the facts are in the instance bar on every instance page. Run `make docs`.
- [ ] **Step 6: Commit** `web: dashboard and instance bar show world, time, uptime, restart and mod version`.

# Message Ways (chat, notification, Expansion) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `dzo msg`, the API and the web interface can send a message to one player or everyone by one of four *ways* (RCon `say`, coloured chat, the vanilla notification system, an Expansion notification of a chosen type), dzo-admin tells dzo whether Expansion is present, and the restart/update countdown picks the way per time window and interval (default: the last 10 minutes as notifications, once a minute, plus "NOW" 10 s before the kick).

**Architecture:** The wire protocol gains three optional fields on `admin.Command` (`notify_type`, `title`, `seconds`) and a `features` list in the mod's hello; the mod's `World.Message` gets an Expansion branch behind `#ifdef EXPANSIONMODCORE`. The API's `message` route takes the same fields and learns the style `rcon`, served by an RCon hook that `dzo serve` injects. The countdown stays in `internal/instance/restart.go` (a CLI process, driven by systemd timers); `instance.Announcement` gains a list of rules and `Restarter` a `Notify` hook that the CLI backs with the API client, falling back to RCon `say -1` whenever the API, the mod or the way is unavailable.

**Tech Stack:** Go (cobra, net/http, html/template + htmx), Enforce Script (dzo-admin), Sphinx docs.

**Spec:** the request in this session plus the probe results in `testmods/dzo-notify-probe` (screenshots reviewed 2026-10-19: vanilla = top-centre toast; Expansion restyles it; `ExpansionNotification(title, text, icon, color, time, type).Create(identity)` works from a server-only mod for TOAST, BAGUETTE, ACTIVITY, KILLFEED, MARKET, GARAGE; the define is `EXPANSIONMODCORE`). No separate spec file.

## Global Constraints

- Ways: `rcon`, `chat`, `notification`, `expansion`. Expansion types: `toast`, `baguette`, `activity`, `killfeed`, `market`, `garage` (default `toast`).
- `dzo msg`, the API and the web interface keep the existing `style` values (`chat`, `action`, `blue`, `important`, `friendly`, `notification`) and add `rcon` and `expansion`. `expansion` takes `notify_type`.
- Text at most 400 bytes (existing hub limit); `title` at most 64 bytes, default `Server`; `seconds` 0 = default 10, at most 60.
- Notification and Expansion announcements of one countdown are at least 1 minute apart. The only exception is the NOW notice.
- All countdown times (`within`, `every`, `at`, `now`) count down to the **kick** (the end of the countdown, while players are still in), like the existing marks. With `kick: 0` that is the restart itself.
- Default rules when `announce.ways` is unset: `{way: rcon, at: [30m, 20m, 15m]}` and `{within: 10m, way: notification, every: 1m, now: 10s}`. The old second-by-second chat countdown in the last minute is gone from the default.
- Fallback chain, never fatal: expansion unsupported by the mod -> `notification`; mod not connected, API unreachable, no token, or the command refused -> RCon `say -1`. A failure is logged once per countdown step, never aborts the restart.
- dzo-admin protocol version stays 1 (additive fields). The mod code changes, so the PBO is rebuilt and `debian/changelog` gets an entry (see memory rule: rebuild the PBO only because code changed).
- Follow AGENTS.md: SPDX headers, `make check` before pushing, tests through the public API, no `time.Sleep` in tests, coverage stays >= 85%, docs in the same commit as behaviour.
- Commits end with the attribution trailer the harness gives for the session.

## Review Focus

- Countdown shorter than 10 minutes (`--minutes 3`, `--now`): the first announcement still goes out through the rule that covers its time; `--now` (minutes 0) announces nothing and still kicks.
- `kick: 0` and `now` longer than the countdown: NOW lands at 10 s before the restart; a `now` that does not fit inside the countdown is skipped, not sent at once.
- Mod connected without Expansion but a rule says `expansion`: the announcement arrives as a vanilla notification, and the config validates (dzo cannot know at config time).
- Hub not connected, no token, or a refused command during a countdown: the same text reaches the players over RCon and the restart still happens at the time asked for.
- Overlapping or duplicate rules (`within` twice, `within: 0` twice, `now` set on two rules, `notification` every 30s, `type` on a non-expansion way): rejected with an error naming the rule.
- Web: the Expansion type selector is offered only when the backend's mod reported the `expansion` feature; a forced POST without it shows a flash error, not a 500.

---

## File Structure

- `internal/admin/proto.go`, `hub.go`: wire fields, `Hello.Features`, message validation.
- `internal/api/server.go`: message body, the `rcon` style, `Server.Say` hook.
- `internal/apiclient/client.go`: `Message` takes a request struct.
- `internal/serve/` (new `rconsay.go`): builds `Server.Say` from the instance's RCon.
- `servermods/dzo-admin/...`: `Proto.c`, `World.c`, `Service.c` (hello features), version bump.
- `cmd/dzo/player.go`: `dzo msg` flags. `cmd/dzo/updates.go`: `rconRestarter` gets the notifier.
- `internal/web/handlers.go`, `templates/instance.html`: broadcast form and player message form.
- `internal/site/schema.go` + new `announceways.go`: `AnnounceWay`, defaults, validation.
- `internal/resolve/announce.go` (new): `AnnouncementOf`.
- `internal/instance/restart.go` + new `announcerules.go`: rules, timeline, `Notify`.
- `internal/updates/updates.go`, `cmd/dzo/shortcuts.go`, `lifecycle.go`: use `AnnouncementOf`.
- `docs/{cli,api,web,operations,configuration}.rst`, `debian/changelog`, `testmods/dzo-notify-probe/` (committed as the Expansion test aid).

---

### Task 1: Wire protocol and hub validation

**Files:**
- Modify: `internal/admin/proto.go` (`Command`, `SyncRequest`), `internal/admin/hub.go` (`Hello`, `validate`, hello handling at line ~133)
- Test: `internal/admin/hub_test.go`

**Interfaces:**
- Produces: `Command.NotifyType string` (`json:"notify_type"`), `Command.Title string` (`json:"title"`), `Command.Seconds int` (`json:"seconds"`); `SyncRequest.Features []string` (`json:"features"`); `Hello.Features []string` (`json:"features,omitempty"`); `func (h Hello) Has(feature string) bool`; `var NotifyTypes = []string{"toast","baguette","activity","killfeed","market","garage"}`; style `expansion` valid; style `rcon` is **not** valid in the hub (it never reaches the mod).

- [ ] **Step 1: Write the failing tests** in `hub_test.go`: `TestMessageExpansionNeedsTheFeature` (hello without `features`, `Submit` of `{Kind: message, Style: expansion, NotifyType: toast, Text: "x"}` -> error containing `expansion notifications are not available`; with `Features: ["expansion"]` in the hello it is queued), `TestMessageExpansionTypeIsChecked` (`NotifyType: "popup"` -> error naming the allowed types; empty type becomes `toast`), `TestMessageTitleAndSecondsLimits` (65-byte title, `Seconds: 61` rejected; zero values become `Server` and `10`), `TestHelloKeepsFeatures` (`Status().Hello.Features == ["expansion"]`, `Has("expansion")` true, a hello without the field gives `Has` false), `TestMessageStyleRConIsRefusedByTheHub`.
- [ ] **Step 2: Run** `go test ./internal/admin -run 'TestMessage|TestHello' -v`; expected FAIL (undefined fields).
- [ ] **Step 3: Implement** the fields above; in `validate` extend the `KindMessage` case (defaults first, then checks); store `req.Features` into the `Hello` where `h.hello` is built (it is rebuilt on every `req.Hello`).
- [ ] **Step 4: Run** `go test ./internal/admin -v`; expected PASS.
- [ ] **Step 5: Commit** `admin: message fields for notifications and the mod's feature list`.

---

### Task 2: API: new fields, the `rcon` style, client

**Files:**
- Modify: `internal/api/server.go` (`messageBody`, `message`, `Server` struct), `internal/apiclient/client.go:188` (`Message`), callers `cmd/dzo/player.go`, `internal/web/handlers.go:415`
- Test: `internal/api/api_test.go`, `internal/apiclient/*_test.go`

**Interfaces:**
- Consumes: Task 1 fields.
- Produces: `api.MessageBody{Text, Style, NotifyType, Title string; Seconds int}` (json `text,style,notify_type,title,seconds`; replaces `messageBody`, exported so the client shares it); `apiclient.(*Client).Message(ctx, name, steamID string, m api.MessageBody) (api.ActionResult, error)`; `Server.Say func(ctx context.Context, instance, steamID, text string) error` (nil = style `rcon` answers 503 `rcon is not available`; empty steamID = everyone).

- [ ] **Step 1: Write the failing tests:** `TestMessagePassesNotificationFieldsToTheMod` (POST with `style: expansion, notify_type: baguette, title: T, seconds: 5`; the command polled by a fake mod carries them), `TestMessageRConStyleUsesSay` (stub `Say` records `(instance, "", "hello")`, response 200, audit entry written with target `all`), `TestMessageRConStyleWithoutHook503`, `TestClientMessageSendsTheBody` (httptest server decodes `api.MessageBody`).
- [ ] **Step 2: Run** `go test ./internal/api ./internal/apiclient -run Message -v`; expected FAIL.
- [ ] **Step 3: Implement.** `message` handler: when `Style == "rcon"` call `s.Say` (same permissions, rate limit and audit as the mod path, no hub needed); otherwise build the `admin.Command` from the body. Update both callers to the new `Message` signature; `actMessage` reads `notify_type`, `title`, `seconds` from the form.
- [ ] **Step 4: Run** `go test ./internal/... ./cmd/... 2>&1 | tail`; expected PASS.
- [ ] **Step 5: Commit** `api: message fields for notifications, the rcon style`.

---

### Task 3: `dzo serve` provides the RCon hook

**Files:**
- Create: `internal/serve/rconsay.go`
- Modify: `internal/serve/serve.go` (where `api.New` is built)
- Test: `internal/serve/rconsay_test.go`

**Interfaces:**
- Produces: `func newRConSay(cfg *config.Config, tree *site.Tree, dial func(ctx context.Context, addr, password string) (broadcast.Session, error)) func(ctx context.Context, instance, steamID, text string) error`. A non-empty steamID is refused with `rcon says to everyone only` (BattlEye addresses slots, and the API has no id-to-slot map); empty steamID runs `say -1 <text>`.

- [ ] **Step 1: Write the failing test** `TestRConSayBroadcastsWithSayMinusOne` (fake session records `say -1 hi`), `TestRConSayToOnePlayerIsRefused`, `TestRConSayUnknownInstance`.
- [ ] **Step 2: Run** `go test ./internal/serve -run RConSay -v`; expected FAIL.
- [ ] **Step 3: Implement** using the instance's RCon address and `runfiles.RConPassword` exactly as `startMessengers` does (`internal/serve/messages.go:78`); one short-lived `battleye.Session` per call, closed after, with the 5 s login timeout of `rconRestarter`.
- [ ] **Step 4: Run** `go test ./internal/serve -v`; expected PASS.
- [ ] **Step 5: Commit** `serve: RCon say for the message API`.

---

### Task 4: dzo-admin: Expansion branch, title/seconds, features

**Files:**
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Proto.c` (`DZOAdminCommand`, `DZOAdminSync`), `.../5_Mission/DZOAdmin/Service.c:164-176` (hello), `.../4_World/DZOAdmin/World.c:216` (`Message`), `Version.c`
- Create (commit): `testmods/dzo-notify-probe/` (already written, uncommitted)

**Interfaces:**
- Consumes: Task 1 JSON names.
- Produces: `DZOAdminCommand.notify_type`, `.title`, `.seconds`; `DZOAdminSync.features` (`ref array<string>`, sent with `hello`, contains `"expansion"` under `#ifdef EXPANSIONMODCORE`); `World.Message` behaviour: `notification` uses `c.title` (default `Server`) and `c.seconds` (default 10); `expansion` calls `ExpansionNotification(title, text, "", ARGB(255, 52, 152, 219), seconds, type).Create(identity)` with the type mapped as in `testmods/dzo-notify-probe/.../DZONotifyProbe.c`; without Expansion compiled in it returns `expansion notifications are not available`.

- [ ] **Step 1: Write the failing live check** in `scripts/test-client.sh` (next to the existing message checks, `grep -n 'messages.png'`): `dzo msg t1 all "x" --style expansion --type toast` must be refused with `expansion notifications are not available` (the local run has no Expansion), `--style notification --title Restart --seconds 5` must succeed, and `dzo status`'s hello must carry no `expansion` feature. Run `scripts/test-client.sh`; expected the new checks FAIL.
- [ ] **Step 2: Implement** the mod changes above; bump the mod version the way the last mod change did (`git log -S'0.2.0' -- servermods/dzo-admin`), rebuild the PBO per `servermods.lock`/the packaging, nothing else.
- [ ] **Step 3: Run** `scripts/test-client.sh`; expected all checks PASS. Then rerun `/tmp`-style Expansion check with the probe: build `dzo-notify-probe` and boot with CF, Dabs, Licensed, Bundle as in the 2026-10-19 session, and confirm the mod's hello contains `expansion` in `dzo status` JSON (keep the screenshots out of the repo).
- [ ] **Step 4: Commit** `admin: Expansion notifications, title and seconds, feature list in the hello` (including the probe mod).

---

### Task 5: `dzo msg` flags

**Files:**
- Modify: `cmd/dzo/player.go:90-110`
- Test: `cmd/dzo/notify_test.go` (existing message tests live there) or a new `msg_test.go`

**Interfaces:**
- Consumes: `apiclient.Message` (Task 2).
- Produces: flags `--style` (now also `rcon`, `expansion`; help text lists every value), `--type` (expansion type, default `toast`), `--title`, `--seconds`.

- [ ] **Step 1: Write the failing tests:** `TestMsgExpansionSendsTypeTitleSeconds` (httptest API; assert the decoded `api.MessageBody`), `TestMsgTypeWithoutExpansionStyleIsAnError`, `TestMsgUnknownTypeListsTheTypes`, `TestMsgRConStyleAll`.
- [ ] **Step 2: Run** `go test ./cmd/dzo -run TestMsg -v`; expected FAIL.
- [ ] **Step 3: Implement** the flags; `--type` is rejected unless `--style expansion`; validate against `admin.NotifyTypes` before the call.
- [ ] **Step 4: Run** `go test ./cmd/dzo -v`; expected PASS.
- [ ] **Step 5: Commit** `cli: dzo msg ways (rcon, notification, expansion) with type, title, seconds`.

---

### Task 6: Web interface

**Files:**
- Modify: `internal/web/handlers.go` (`instView`, `actMessage`, player panel message form), `internal/web/templates/instance.html:15-20`, `internal/web/templates/fragments.html` (per-player message form, find with `grep -n message internal/web/templates/fragments.html`)
- Test: `internal/web/web_test.go`

**Interfaces:**
- Consumes: `instView.Status.Hello.Has("expansion")` (Task 1).
- Produces: broadcast form with a **way** select (`chat (yellow)`, `important (red)`, `friendly (green)`, `on screen (notification)`, `RCon say`, and `Expansion` only when the feature is reported), an Expansion **type** select shown only when `expansion` is chosen (small inline script in `static/`, or `hx-trigger`; no new dependency), a title input (placeholder `Server`) and seconds. The same controls on the per-player message form. "Send to all" is the broadcast form; the per-player form posts to `/players/{steamid}/message`.

- [ ] **Step 1: Write the failing tests:** `TestInstancePageOffersExpansionOnlyWithTheFeature` (backend status with and without `Hello.Features`; assert the `<option value="expansion">` and the six type options appear only with it), `TestBroadcastFormPostsNotifyFields` (POST `/b/x/i/y/message` with `style=expansion&notify_type=baguette&title=T&seconds=5` -> the fake API got them), `TestForcedExpansionWithoutFeatureShowsAFlash` (API 422 -> flash with the refusal, status 200).
- [ ] **Step 2: Run** `go test ./internal/web -run 'Expansion|Broadcast' -v`; expected FAIL.
- [ ] **Step 3: Implement.** Escape via `html/template` only; CSRF handling as the neighbouring forms do.
- [ ] **Step 4: Run** `go test ./internal/web -v`; expected PASS.
- [ ] **Step 5: Commit** `web: choose how a message reaches the players`.

---

### Task 7: Config: `announce.ways`

**Files:**
- Create: `internal/site/announceways.go`
- Modify: `internal/site/schema.go:122` (`Announce.Ways []AnnounceWay yaml:"ways,omitempty"`), `internal/site/restarttime.go:144` and the updates validation (`restart_announce`) to call `Ways` validation
- Test: `internal/site/announceways_test.go`

**Interfaces:**
- Produces:
  `type AnnounceWay struct { Within Duration; Way, Type string; Every Duration; At []Duration; Now Duration }` (yaml `within,way,type,every,at,now`);
  `func (a Announce) EffectiveWays() []AnnounceWay` (the defaults from Global Constraints when `Ways` is empty);
  `func (a Announce) ValidateWays() error`.
  Validation: `way` is one of the four; `type` only with `expansion` and one of the six (empty = toast); a rule needs `every` or `at`, not both (`now` may be added to either); `notification`/`expansion` need `every >= 1m` and `at` marks >= 1m apart; `rcon`/`chat` `every >= 1s`; no two rules with the same `within` (0 counts once); at most one rule sets `now`; `now > 0` and, when the rule has `within`, `now < within`.

- [ ] **Step 1: Write the failing tests:** `TestEffectiveWaysDefault` (exact two default rules), `TestWaysYAMLRoundTrip` (the example `{within: 10m, way: expansion, type: baguette, every: 1m, now: 10s}` parses), and one table test `TestValidateWaysRejects` with the Review Focus cases (notification every 30s, type on `chat`, duplicate `within`, two `now`, unknown way, `now >= within`), each asserting the error names the rule index (`announce.ways[1]: ...`).
- [ ] **Step 2: Run** `go test ./internal/site -run 'Ways' -v`; expected FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/site ./internal/resolve -v`; expected PASS (resolve golden files must not change: `Ways` is omitted when empty; if a golden changes, fix the tag, not the golden).
- [ ] **Step 5: Commit** `site: announce.ways, how the restart countdown reaches the players`.

---

### Task 8: Countdown engine

**Files:**
- Create: `internal/instance/announcerules.go`
- Modify: `internal/instance/restart.go` (`Announcement`, `timeline`, `countdown`, `Restarter`, `step`)
- Test: `internal/instance/restart_test.go` (existing timeline tests must be updated: the default timeline changed on purpose)

**Interfaces:**
- Produces:
  `type AnnounceRule struct { Within time.Duration; Way, Type string; Every time.Duration; At []time.Duration; Now time.Duration }`;
  `Announcement.Rules []AnnounceRule` (empty = the built-in defaults of Global Constraints, so `instance` stays free of `site`);
  `step` gains `way, typ string` and `now bool`;
  `Restarter.Notify func(ctx context.Context, way, typ, text string, seconds int) error` (nil = every way is RCon).
- Timeline rule (pin it in the tests): sort rules by `Within` ascending with 0 last; the rule with the smallest `Within >= left` covers `left`. Marks of a rule: its `At` values, or `Within - k*Every` (k >= 0, for `Within == 0` start at the countdown start) while `left` is above the next inner rule's `Within` and `>= Every`. The first step at the countdown start is always announced, by the rule that covers it. A rule with `Now` adds a step at `left == Now` with text `"<text> NOW"` by that rule's way, only if `Now <= countdown length`. Lock and kick steps are unchanged. A step's text keeps the existing `"<text> in <duration>"` form.
- Delivery: a step with way `rcon` calls `Announce` (`say -1`); any other way calls `Notify` with `seconds = 10` (NOW: 8); on error or nil `Notify`, log once and fall back to `Announce`. The cancel notice and the lock notice use the way of the covering rule, with the same fallback.

- [ ] **Step 1: Write the failing tests** (fake `Commander` + fake `Notify`, the existing fake clock via `Restarter.Sleep`): `TestDefaultTimelineNotifiesTheLastTenMinutes` (30m total, kick 3: rcon marks at the start, 20m, 15m; notification marks 10m..1m; NOW at 10s; lock via the covering rule), `TestTimelineEveryAndWithin` (two custom rules, exact `(left, way, type)` list), `TestTimelineAtMarks`, `TestNowSkippedWhenLongerThanTheCountdown`, `TestShortCountdownStartsWithTheCoveringRule` (`Minutes: 3, Kick: 0` -> first step is a notification), `TestNowZeroMinutesAnnouncesNothing`, `TestNotifyFailureFallsBackToSay`, `TestNilNotifyUsesSay`, `TestNotificationStepsAreAtLeastAMinuteApart`.
- [ ] **Step 2: Run** `go test ./internal/instance -run 'Timeline|Notify|Now' -v`; expected FAIL.
- [ ] **Step 3: Implement** `Announcement.timeline()` against the rule above (replace `announceMarks`; keep `durationText`); route `st.say` delivery through a `deliver(ctx, c, st)` helper in `countdown`.
- [ ] **Step 4: Run** `go test -race ./internal/instance -v`; expected PASS. Update `TestRestartAnnouncesLocksKicksStopsAndStaysStopped...` expectations to the new default.
- [ ] **Step 5: Commit** `instance: countdown rules, announcements through the notification system`.

---

### Task 9: Wire the CLI, the update engine and the notifier

**Files:**
- Create: `internal/resolve/announce.go` (`func AnnouncementOf(a site.Announce, text string) instance.Announcement`; if `resolve` importing `instance` makes a cycle, put it in `internal/updates` and use it from `cmd`)
- Modify: `cmd/dzo/updates.go:77` (`rconRestarter` sets `Notify`), `cmd/dzo/shortcuts.go:64`, `cmd/dzo/lifecycle.go:194`, `cmd/dzo/updates.go:437`, `internal/updates/updates.go:211` (all four build an `Announcement` by hand today)
- Test: `cmd/dzo/updates_test.go` (or new `notifier_test.go`), `internal/resolve/announce_test.go`

**Interfaces:**
- Consumes: Tasks 2, 7, 8.
- Produces: `func apiNotifier(cmd *cobra.Command, cfg *config.Config, inst *resolve.Instance) func(ctx context.Context, way, typ, text string, seconds int) error`. It builds an `apiclient` like `apiTarget.client()` with the token from `DZO_TOKEN`, `--token-file`, else `${paths.secrets}/announce.token` if that file exists (create with `dzo token create`, role operator; documented). No token -> returns an error `no API token` (logged once, countdown falls back to `say`). Way mapping: `chat` -> style `important`, `notification` -> `notification`, `expansion` -> `expansion` + `notify_type`; title `Server`; the text is what the step carries. If the instance's mod reports no `expansion` feature (`c.Status`), `expansion` is sent as `notification`.

- [ ] **Step 1: Write the failing tests:** `TestAnnouncementOfCopiesRules` (YAML rules -> `instance.AnnounceRule`, defaults when empty), `TestAPINotifierMapsWays` (httptest API records bodies for chat/notification/expansion; without the feature expansion arrives as notification), `TestAPINotifierWithoutTokenErrors`, `TestRestartFallsBackToRConWhenTheAPIIsDown` (API 503, assert `say -1` was sent for the notification step and the restart completed).
- [ ] **Step 2: Run** `go test ./cmd/dzo ./internal/resolve ./internal/updates -run 'Announce|Notifier|Falls' -v`; expected FAIL.
- [ ] **Step 3: Implement**; replace the four hand-built `instance.Announcement{...}` literals (they keep their flag overrides: `--minutes/--lock/--kick/--text`, `--now`).
- [ ] **Step 4: Run** `go test ./... 2>&1 | tail -20`; expected PASS.
- [ ] **Step 5: Commit** `restart: countdown through the notification system, RCon as the fallback`.

---

### Task 10: Docs, packaging, final gate

**Files:**
- Modify: `docs/cli.rst` (`dzo msg` row line ~219), `docs/api.rst:95-100`, `docs/web.rst`, `docs/operations.rst` (Graceful restarts, line ~53-80: the new default and `announce.ways`), `docs/configuration.rst` (`restarts.announce`, `updates.restart_announce`, the `announce.token` file), `docs/admin-map.rst` if it lists the mod's features, `debian/changelog`
- Test: `make docs`

- [ ] **Step 1:** Document the ways, the styles, the Expansion types, the default rules with the YAML example from Task 7, the "times count down to the kick" rule, the fallback chain, and the token file. State plainly that the default countdown changed (the per-second chat countdown is gone).
- [ ] **Step 2: Run** `make docs`; expected no warnings.
- [ ] **Step 3: Add** `debian/changelog` entry: message ways, `announce.ways`, dzo-admin changed (PBO rebuilt).
- [ ] **Step 4: Run** `make check`; expected PASS including `coverage-check` >= 85%. Also run `scripts/test-client.sh` once more.
- [ ] **Step 5: Commit** `docs: message ways and announce.ways`.

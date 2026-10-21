<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Event feed, killfeed and events page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** dzo-admin reports what happens in the game (kills including Expansion AI, deaths, joins and leaves, base building and raiding, flags, stashes), dzo adds in-game chat and admin actions, posts a Discord killfeed with an optional per-instance delay, the web UI gets a live "tail -f" events page with a chat input line, player links and map links, and `dzo events [-f]` shows the same feed on the console.

**Architecture:** A new optional part `feed` in the existing `/mod/v1/sync` (protocol stays 1, the change is additive): the mod buffers events and sends them with the next sync, numbered by `seq` so a resend is not counted twice. Chat comes from BattlEye RCon (the moderation service already holds a session per instance and parses `EventChat`; the 1.30 server scripts get no chat event, `MissionServer.OnEvent` has no `ChatMessageEventTypeID` case), admin actions from the audit log. Every source lands in one per-instance ring buffer in `admin.Hub` with dzo-assigned increasing ids; the API serves it (`GET …/feed?after=`) and streams it (SSE kind `feed`), the web page and the killfeed both read "everything after id N", so a slow or reconnecting reader never loses events while they are in the ring. The killfeed is a new package `internal/killfeed`: filter, delay queue, Discord batches.

**Tech Stack:** Enforce Script (dzo-admin, DayZ 1.30, optional Expansion AI / base building), Go (`internal/admin`, `internal/api`, `internal/apiclient`, `internal/moderation`, `internal/notify`, `internal/killfeed`, `internal/site`, `internal/serve`, `internal/web`), htmx + plain JS (CSP: no inline scripts), Leaflet, Sphinx.

**Spec:** the request, verbatim:

> - a killfeed for discord (including kills by expansion AI)
> - configurable delay of kill messages for PvP servers
> - have an "chat" style events page on the web ui that lists all events happening (tail -f style). events as in kills, base building and raiding actions, stashes, leaving/joining, flag raising/lowering + all chat messages from in game. If you think more fits, add it.
> - the chat page as an input line to send text to all players
> - players in the list -> link to the player in the players tab.
> - location -> link to the map, open the map with the location marked
>
> (follow-up) killfeed should contain killer and target, distance, weapon
>
> (follow-up) add something like `dzo events` for the console, no chat input, journalctl -f style

Additions chosen under "if you think more fits": non-PvP deaths with their cause (infected, animal, vehicle, fall/self), combination locks cut, BattlEye kicks, and dzo's own admin actions (kick, ban, teleport, broadcast, …) from the audit log.

## Global Constraints

- Work on a new branch `feature/event-feed` in a worktree (`superpowers:using-git-worktrees`). The main checkout has uncommitted user edits in `docs/*.rst`; never stage or commit them.
- `servermods/dzo-admin` is its own repo (submodule): commit there first, then the pointer in dzo. Mod version `0.5.0` (`Version.c`), protocol stays `1`. The PBO is rebuilt once, at the end of Task 2, then `make servermods-lock`.
- Mod rules (dayz-dev skill): server-only code paths (`GetGame().IsServer()`), null-check every `Cast`, `GetIdentity()`, `GetHierarchyParent()`; Expansion AI code only under `#ifdef EXPANSIONMODAI`; verify every class/method with `dayz-api.py` before use (the hooks below were checked against the 1.30 scripts; the Expansion AI faction getter was not, see Task 1).
- Kill entries carry killer, target, distance and weapon (the follow-up). Killfeed messages carry no positions.
- Untrusted text (player names, chat, class names) reaches the DOM only through `textContent`, HTML only through `html/template`, Discord only escaped and with `allowed_mentions: {"parse": []}`.
- The killfeed is best effort: a Discord post that fails, is rate-limited (429) or takes longer than 5 s is dropped and logged, never retried; at most 500 kills wait per instance (oldest dropped).
- The feed survives a restart of `dzo serve`: the newest `FeedKeep` events per instance are kept in `<paths.db>/feed/<instance>.jsonl` (0600, dir 0750), so the events page and `dzo events` never start empty.
- Feed ring size `admin.FeedKeep = 1000` per instance; mod buffer cap `256` events between syncs (oldest dropped, a `dropped` count is sent as one event of kind `dropped`); web page keeps at most 1000 rows.
- The events page needs `view`; the input line is shown and accepted only with `broadcast` (existing `POST …/message`, unchanged permission).
- Changes to `servermods/`, `internal/admin` and player actions: run `make test-client` (stable and `ARGS=--exp`) and say so in the commit message (AGENTS.md).
- `make check` passes, coverage ≥ 85 %; SPDX headers on new files; docs and `debian/changelog` in the same commits as the behaviour.

## Review Focus

1. **Player names that are Discord markup or mentions** (`@everyone`, `**x**`, `` ` ``, `<@123>`, `||`): the killfeed shows them literally and pings nobody. Test in Task 6.
2. **Kills waiting in the delay queue during `dzo serve` reload**: a site reload with the same instance keeps the queue and posts them on time; a changed delay applies to new kills only. (A full restart of `dzo serve` loses the queue; documented.) Test in Task 6.
3. **Lost or repeated syncs**: the mod resends a feed batch whose reply it did not get; dzo counts each `seq` once, and accepts a lower `seq` again after a hello (mod restart). Tests in Task 3.
4. **Bursts and a slow Discord**: a grenade kills five, a raid makes 60 build events in one sync, Discord answers 429 or hangs: the killfeed splits into messages under 2000 characters in order, and a post that fails or times out is dropped (best effort, no retry) while later kills still go out on time. Tests in Task 6.
5. **Player names and chat with terminal escape sequences** (`\x1b[2J`, `\r`, C1 `\x9b`) in `dzo events`: printed harmlessly as `?`, never interpreted by the terminal. Test in Task 9. (A web page left open for hours or reconnecting is covered by Task 7's e2e.)

---

### Task 1: dzo-admin — feed transport, kills (with Expansion AI), deaths, joins and leaves

**Files:**
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Proto.c` (class `DZOAdminFeedEvent`, field `feed` in `DZOAdminSync`)
- Create: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/Feed.c` (`DZOAdminFeed`: buffer, helpers, kill classification, `modded class PlayerBase` `EEKilled`)
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/5_Mission/DZOAdmin/Service.c` (send the feed every tick; `MissionServer` connect/disconnect hooks)
- Modify: `servermods/dzo-admin/README.md` (the feed part, the event kinds)

**Interfaces:**
- Produces (wire, mirrored by Task 3): `DZOAdminFeedEvent { int seq; string kind; string steam_id; string name; bool ai; string target_steam_id; string target_name; bool target_ai; bool has_pos; float x; float y; float z; string weapon; float distance; string object; string cause; string text; }` and `has` entry `"feed"`. Kinds from this task: `kill`, `death`, `connect`, `disconnect`, `dropped`.
  - `kill`: actor = killer (`steam_id`, `name`, `ai`), target = victim, `weapon` = display name of the weapon (vehicle display name for a road kill, `"fists"` for fists), `distance` in metres (0 for melee/vehicle), position = victim's.
  - `death`: no actor; target = victim; `cause` one of `self` (starved, bled, drowned, suicide: killer == victim), `infected`, `animal`, `explosion`, `vehicle` (no driver), `other`; `object` = class of the killer object for `infected`/`animal`/`other`.
  - `connect` / `disconnect`: actor = the player, position = the player's.
  - `dropped`: `text` = number of events dropped since the last sync.
- Produces (mod API): `static void DZOAdminFeed.Add(DZOAdminFeedEvent e)` (assigns `seq`), `static void DZOAdminFeed.Actor(DZOAdminFeedEvent e, PlayerBase p)` / `Target(...)` (fill steam id, name, ai from identity or the cached id/name, like vanilla `PluginAdminLog.GetPlayerPrefix`), `static void DZOAdminFeed.Pos(DZOAdminFeedEvent e, vector p)`. Task 2 uses these.

- [ ] **Step 1: Proto.** Add the class and `ref array<ref DZOAdminFeedEvent> feed;` to `DZOAdminSync`, comment that `has` lists `"feed"` when included.
- [ ] **Step 2: Buffer in `Feed.c`.** Static `m_Pending` (cap 256, drop oldest, count drops), `m_InFlight`, `m_Seq`. `Take()` moves pending into in-flight and returns it (prepending a `dropped` event if any), `Ack()` clears in-flight, `Retry()` puts in-flight back in front of pending. A new `seq` per event, starting at 1 per mod start.
- [ ] **Step 3: Service.** In `Tick`, before `Send`: if `m_Transport.m_LastOk` call `DZOAdminFeed.Ack()` else `DZOAdminFeed.Retry()`; then `s.feed = DZOAdminFeed.Take()` and `s.has.Insert("feed")` only when it is not empty. The feed goes with every tick, not on an interval.
- [ ] **Step 4: Kills.** `modded class PlayerBase { override void EEKilled(Object killer) }`: server only, classify **before** `super` (identity is still set), the same way vanilla `PluginAdminLog.PlayerKilled` does: killer root = `PlayerBase.Cast(EntityAI.Cast(killer).GetHierarchyParent())` else `PlayerBase.Cast(killer)`; `killer == this` → `death/self`; weapon or melee weapon with a root player → `kill`, distance `vector.Distance` for firearms; explosive (`ExplosivesBase`) → `death/explosion` unless a root player exists; `IsTransport()` → `kill` by `Transport.CrewDriver()` with the vehicle as weapon, else `death/vehicle`; `IsZombie()` → `death/infected`; `IsAnimal()` → `death/animal`; a root player without weapon → `kill` with weapon `fists`; anything else `death/other`. AI victims (eAIBase) are reported too, with `target_ai`.
- [ ] **Step 5: Expansion AI.** Under `#ifdef EXPANSIONMODAI`, `eAIBase.Cast(p)` sets `ai = true` and the name to `"AI"` plus the faction's display name in parentheses when it can be read. Look up the getter chain with `python3 ~/.claude/skills/dayz-dev/scripts/dayz-api.py -m expansion class eAIBase --inherited` (group → `eAIFaction.GetDisplayName()`); if there is none, the name is `"AI"`. Steam id stays empty for AI.
- [ ] **Step 6: Joins and leaves.** `modded class MissionServer`: `override void InvokeOnConnect(PlayerBase player, PlayerIdentity identity)` → `connect`; `override void PlayerDisconnected(PlayerBase player, PlayerIdentity identity, string uid)` → `disconnect` (steam id from `uid` when the identity is gone). Both call `super` first.
- [ ] **Step 7: Verify on a real server** (dayz-dev `testing/local-server.md`: isolated tree, `free-ports.sh`, `dzo serve` from this branch, dzo-admin as `-servermod`). With `scripts/test-client.sh` join, die (`player_kill` via the API → `death/self`), disconnect; check `/mod/v1/sync` bodies (dzo debug log or a `tcpdump`-free test endpoint) contain `connect`, `death`, `disconnect` with increasing `seq`. With Expansion (`-mod` CF, Dabs, Expansion bundle) spawn an AI next to the client and let it kill the client: `kill` with `ai: true`. No script errors in the RPT. Evidence into the commit message.
- [ ] **Step 8: Commit** in the submodule: `feed: report kills (Expansion AI too), deaths, joins and leaves to dzo`.

### Task 2: dzo-admin — base building, raids, flags, stashes; version 0.5.0

**Files:**
- Create: `servermods/dzo-admin/src/DZOAdmin/scripts/4_World/DZOAdmin/FeedHooks.c`
- Modify: `servermods/dzo-admin/src/DZOAdmin/scripts/3_Game/DZOAdmin/Version.c` (`"0.5.0"`), `servermods/dzo-admin/README.md`
- Modify (dzo repo): `servermods.lock`, the rebuilt PBO artefacts the earlier bumps committed (`git log -3 -- servermods servermods.lock`), `debian/changelog`

**Interfaces:**
- Consumes: `DZOAdminFeed.Add/Actor/Target/Pos` from Task 1.
- Produces kinds (actor = the player, position = the object's, `object` = part name or item class, `text` = class of the base object or flag): `build`, `dismantle`, `destroy` (a part destroyed; actor empty when no player did it), `lock_cut` (combination lock destroyed, `weapon` = tool display name), `flag_raised`, `flag_lowered`, `stash_buried`, `stash_dug_up` (`object` = the stashed item's class).

- [ ] **Step 1: Base building.** `modded class BaseBuildingBase`: override `OnPartBuiltServer(Man player, string part_name, int action_id)`, `OnPartDismantledServer(Man player, string part_name, int action_id)`, `OnPartDestroyedServer(Man player, string part_name, int action_id, bool destroyed_by_connected_part = false)`; call `super` first; skip `destroy` when `destroyed_by_connected_part` (one event per raid action, not per dependent part).
- [ ] **Step 2: Lock cut.** `modded class ActionDestroyCombinationLock`, `override void OnFinishProgressServer(ActionData action_data)`: read the fence's `GetCombinationLock()` before `super`, report `lock_cut` when it `IsDamageDestroyed()` after `super`.
- [ ] **Step 3: Flags.** `modded class TerritoryFlag`, `override protected void LogAnimateFlag(float newPhase, notnull PlayerBase player)` (vanilla calls it from `AnimateFlagEx` only with a player): `super`, then phase `0` → `flag_raised`, `1` → `flag_lowered` (the mast phase is inverted). `text` = flag class (`FindAttachmentBySlotName("Material_FPole_Flag")`, may be null → empty). Admin raises from dzo (`flag_raise`) pass no player and are reported through the audit log instead (Task 4).
- [ ] **Step 4: Stashes.** `modded class ActionDigInStash` / `ActionDigOutStash`, `override void OnFinishProgressServer(ActionData action_data)`: read the target's class (dig in) or `UndergroundStash.GetStashedItem()` class (dig out) **before** `super`, report after.
- [ ] **Step 5: Version 0.5.0**, README lists all feed kinds and the hooks.
- [ ] **Step 6: Verify on a real server** as in Task 1, with the client: build a fence part, dismantle it, raise and lower a flag, bury and dig up a stash; check each kind appears once. Vanilla tree without Expansion: no script errors. Evidence into the commit message.
- [ ] **Step 7: Build the PBO** (`dayz-mod-pack.sh build`, as the previous releases), boot it, compare script module counts with vanilla; `make servermods-lock`.
- [ ] **Step 8: Commit** submodule `feed: base building, raids, flags and stashes; version 0.5.0`, then dzo `admin: dzo-admin 0.5.0 reports game events (feed)` with the pointer, lock, PBO, changelog entry.

### Task 3: Hub — feed ring buffer with ids

**Files:**
- Create: `internal/admin/feed.go`, `internal/admin/feed_test.go`, `internal/admin/feedlog.go`, `internal/admin/feedlog_test.go`
- Modify: `internal/admin/proto.go` (`SyncRequest.Feed`, `Normalise`), `internal/admin/hub.go` (store, emit), `internal/admin/registry.go` (`Add` keeps `FeedLog` of an existing hub), `internal/admin/fuzz_test.go` (a sync body with `feed` in the seeds)
- Modify: `internal/serve/serve.go` (`FeedLogPath`, set in `AddHubs`), `internal/serve/serve_test.go`

**Interfaces:**
- Produces:
  ```go
  const FeedKeep = 1000
  // Feed kinds: the mod's (Tasks 1-2) plus the ones dzo adds.
  const (FeedKill = "kill"; FeedDeath = "death"; FeedConnect = "connect"; FeedDisconnect = "disconnect"
         FeedBuild = "build"; FeedDismantle = "dismantle"; FeedDestroy = "destroy"; FeedLockCut = "lock_cut"
         FeedFlagRaised = "flag_raised"; FeedFlagLowered = "flag_lowered"; FeedStashBuried = "stash_buried"; FeedStashDugUp = "stash_dug_up"
         FeedDropped = "dropped"; FeedChat = "chat"; FeedKick = "kick"; FeedAdmin = "admin")
  type FeedEvent struct {
      ID   int64     `json:"id"`   // dzo's, increasing per hub
      Time time.Time `json:"time"` // when dzo got it
      Seq  int       `json:"seq,omitempty"` // the mod's
      Kind string    `json:"kind"`
      SteamID, Name string; AI Flag            // json steam_id, name, ai
      TargetSteamID, TargetName string; TargetAI Flag // target_steam_id, target_name, target_ai
      HasPos Flag; X, Y, Z float64             // has_pos, x, y, z
      Weapon string; Distance float64; Object, Cause, Channel, Text string // all omitempty
  }
  func (h *Hub) AddFeed(ev FeedEvent) FeedEvent          // sets ID and Time, stores, emits StreamEvent{Kind: "feed", Data: ev}
  func (h *Hub) Feed(after int64, limit int) []FeedEvent  // ascending; limit <= 0 or > FeedKeep means FeedKeep; after 0 = the newest `limit`
  func (h *Hub) PlayerByName(name string) (Player, bool)  // exactly one online player with that name

  // FeedLog is the on-disk copy of a hub's feed: append-only JSON lines, compacted to the newest
  // FeedKeep lines when the file holds 2*FeedKeep (rewritten to a temp file and renamed).
  type FeedLog struct { Path string }
  func (l *FeedLog) Load() ([]FeedEvent, error)   // newest FeedKeep, oldest first; missing file = none; bad lines skipped
  func (l *FeedLog) Append(ev FeedEvent) error
  func (h *Hub) UseFeedLog(l *FeedLog) error       // loads it into the ring (ids continue after the highest loaded id), then every AddFeed appends
  func FeedLogPath(cfg *config.Config, instance string) string // in internal/serve: <paths.db>/feed/<instance>.jsonl
  ```
  A failed append is logged through the registry's `Logf` and does not stop the feed.
  `SyncRequest` gains `Feed []FeedEvent `json:"feed"``; `Normalise` drops it unless `has` lists `"feed"`.

- [ ] **Step 1: Failing tests** in `feed_test.go` (testify, fake clock as in `hub_test.go`):
  - `TestSyncFeedAssignsIDs`: one sync with three events → `Feed(0, 0)` has ids 1,2,3, `Time` = fake now.
  - `TestSyncFeedResendCountedOnce`: sync seq 1,2 then a resend of 1,2 plus 3 → ids 1..3 only (Review Focus 3).
  - `TestSyncFeedHelloResetsSeq`: after seq 5, a sync with `Hello: true` and seq 1 is accepted.
  - `TestFeedRingKeepsNewest`: `FeedKeep+10` events → `Feed(0, 0)` has `FeedKeep` entries, first id 11; `Feed(5, 0)` starts at 11 (gap; caller sees the jump).
  - `TestFeedAfterAndLimit`: `Feed(2, 1)` returns only id 3; `Feed(0, 2)` returns the newest two.
  - `TestAddFeedEmits`: a subscriber gets `StreamEvent{Kind: "feed"}` with the stored event.
  - `TestSyncFeedWithoutHasIgnored`: `Has: ["players"]` and a non-empty `Feed` → nothing stored.
  - `TestPlayerByName`: unique name found; two players with one name → false; unknown → false.
  - `feedlog_test.go` (under `t.TempDir()`): `TestFeedLogRoundTrip` (append 3, a new `FeedLog` on the same path loads them in order); `TestFeedLogMissingFile` (no error, empty); `TestFeedLogSkipsBadLines` (a truncated last line from a crash is skipped); `TestFeedLogCompacts` (`2*FeedKeep` appends → file holds `FeedKeep` lines, mode 0600); `TestUseFeedLogContinuesIDs` (log with ids 1..5 → next `AddFeed` gets id 6, `Feed(0, 0)` returns 1..6); `TestRegistryAddKeepsFeed` (a reload's `Add` of the same name keeps ring and log).
  - `serve_test.go`: `TestAddHubsFeedLog` (after `AddHubs` with `paths.db` in a temp dir, an `AddFeed` lands in `<db>/feed/<instance>.jsonl`; a second `AddHubs` on a fresh registry loads it).
- [ ] **Step 2: Run** `go test ./internal/admin/ -run 'Feed|PlayerByName'` → FAIL (undefined).
- [ ] **Step 3: Implement.** Append under the hub's lock, after the ID is assigned (one small write per event; the audit log does the same). Ring as a slice trimmed to `FeedKeep`; `lastSeq` per hub, reset when `req.Hello`; events with `Seq <= lastSeq` skipped; `Seq == 0` (Go callers) never skipped.
- [ ] **Step 4: Run** `go test -race ./internal/admin/` → PASS; `make fuzz` short run passes.
- [ ] **Step 5: Docs**: `operations.rst` (or wherever `paths.db` contents are listed): `feed/<instance>.jsonl`, what it holds (chat and positions: personal data, kept for at most the newest 1000 events, mode 0600), safe to delete. `make docs`.
- [ ] **Step 6: Commit** `admin: hub keeps the game event feed (ids, resend-safe ring, kept on disk)`.

### Task 4: Chat, BattlEye kicks and admin actions into the feed

**Files:**
- Modify: `internal/moderation/service.go` (callback), `internal/moderation/service_test.go`
- Modify: `internal/admin/audit.go` (callback), `internal/admin/audit_test.go` (create if missing)
- Modify: `internal/serve/serve.go` (wiring), `internal/serve/serve_test.go`

**Interfaces:**
- Produces:
  - `moderation.Service.OnEvent func(instance string, ev battleye.Event)` — called from `handle` for every event, after the ban logic, synchronously (callers must not block).
  - `admin.Audit.OnLog func(AuditEntry)` — called after a successful write.
  - `serve.feedFromRCon(reg *admin.Registry) func(string, battleye.Event)`: `EventChat` → `FeedChat` (`Name`, `Channel`, `Text`, `SteamID` from `hub.PlayerByName` when unique); `EventKick` → `FeedKick` (`Name`, `Text` = reason). Others ignored (the mod reports joins and leaves with steam ids).
  - `serve.feedFromAudit(reg *admin.Registry) func(admin.AuditEntry)`: entries with an `Instance` → `FeedAdmin` with `Name` = actor, `Text` = `action target detail`, `Object` = result when not OK, `TargetSteamID` = `Target` when it is a 17-digit Steam id.

- [ ] **Step 1: Failing tests:** `TestServiceOnEventChat` (fake RCon from the existing tests emits a chat line → callback gets instance and `EventChat`); `TestAuditOnLog`; in `serve_test.go` `TestFeedFromRConChat` (hub with one online player "Bob" → chat by "Bob" has his steam id; chat by "Ghost" has none), `TestFeedFromRConIgnoresConnect`, `TestFeedFromAudit`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** and wire in `serve.Run`: `svc.OnEvent = feedFromRCon(reg)`, `audit.OnLog = feedFromAudit(reg)` (set before `svc.Start`).
- [ ] **Step 4: Run** `go test -race ./internal/moderation/ ./internal/admin/ ./internal/serve/` → PASS.
- [ ] **Step 5: Commit** `serve: chat, BattlEye kicks and admin actions go into the event feed`.

### Task 5: API and client — read and stream the feed

**Files:**
- Modify: `internal/api/server.go` (route, `stream` filter), `internal/api/api_test.go`
- Modify: `internal/apiclient/client.go`, its test
- Modify: `docs/api.rst`

**Interfaces:**
- Produces:
  - `GET /api/v1/instances/{instance}/feed?after=<id>&limit=<n>` (PermView) → `[]admin.FeedEvent`; bad `after`/`limit` → 400.
  - `GET …/stream?only=feed`: only `feed` events (no initial snapshot of the other kinds); without `only` the stream is unchanged and also carries `feed`.
  - `func (c *Client) Feed(ctx context.Context, name string, after int64, limit int) ([]admin.FeedEvent, error)`
  - `func (c *Client) StreamOnly(ctx context.Context, name, only string, fn func(kind string, data json.RawMessage)) error` (`Stream` calls it with `""`).

- [ ] **Step 1: Failing tests:** `TestFeedEndpoint` (after/limit, 400 on `after=x`, 404 for an instance the token may not see), `TestStreamOnlyFeed` (first frame is a `feed` event added after connect, no `players` frame), apiclient round trip.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/api/ ./internal/apiclient/` → PASS.
- [ ] **Step 5: Docs** `api.rst`: the endpoint, the event kinds and fields (table), `only=feed`. `make docs`.
- [ ] **Step 6: Commit** `api: GET /feed and stream?only=feed`.

### Task 6: Discord killfeed with delay

**Files:**
- Create: `internal/killfeed/killfeed.go`, `internal/killfeed/killfeed_test.go`, `internal/killfeed/main_test.go` (goleak, as the other packages)
- Modify: `internal/notify/event.go` (`KindKill`), `internal/notify/template.go` (template, `md` func), `internal/notify/discord.go` (`allowed_mentions`), their tests
- Modify: `internal/site/schema.go` (`Instance.Killfeed`, validation), its test; `internal/serve/serve.go` (+ a `killfeeds.go` if it reads better)
- Modify: `docs/configuration.rst` (killfeed section), `docs/monitoring.rst` (Discord: the killfeed is separate from operator notifications)

**Interfaces:**
- Produces:
  ```go
  // site: instances/<name>/instance.yaml
  type KillfeedConfig struct {
      Discord   []string `yaml:"discord,omitempty"`    // webhook names from config.yaml notify.discord; empty = killfeed off
      Delay     Duration `yaml:"delay,omitempty"`      // 0 = at once; max 24h
      AIKillers *bool    `yaml:"ai_killers,omitempty"` // kills by Expansion AI, default true
      AIVictims bool     `yaml:"ai_victims,omitempty"` // players killing AI, default false
      Deaths    bool     `yaml:"deaths,omitempty"`     // also deaths without a killer (infected, fall, …), default false
  }
  // Instance gains: Killfeed KillfeedConfig `yaml:"killfeed,omitempty"`

  package killfeed
  type Config struct { Instance string; Targets []string; Delay time.Duration; AIKillers, AIVictims, Deaths bool }
  func Wanted(c Config, ev admin.FeedEvent) bool       // the filter
  func Line(ev admin.FeedEvent) string                  // one Discord line, escaped
  type Manager struct { Notify func(context.Context, notify.Event) error; Now func() time.Time; Tick <-chan time.Time; Logf func(string, ...any) }
  func (m *Manager) Set(ctx context.Context, reg *admin.Registry, cfgs []Config) // start/stop/update per instance, keeps queues of instances that stay
  func (m *Manager) Wait()                                                       // after ctx is done
  ```
  `notify.KindKill` events carry `Data["Lines"]` (already escaped, joined by `\n`); its template is `{{.Data.Lines}}`.

- [ ] **Step 1: Failing tests** in `killfeed_test.go`:
  - `TestLine`: `kill` Alice → Bob, `M4-A1`, 123.4 m → `💀 **Alice** killed **Bob** with M4-A1 (123 m)`; AI killer `AI (East)` → `🤖 **AI (East)** killed **Bob** with AKM (54 m)`; melee (distance 0) → no `(… m)`; vehicle → `with Ada 4x4`; `death/infected` with `Deaths` → `☠️ **Bob** was killed by infected`.
  - `TestLineEscapes` (Review Focus 1): names `@everyone`, `**x**`, `` `y` ``, `<@123>`, `||z||` → each markdown char backslash-escaped, `@` followed by a zero-width space; output contains no unescaped `*`, `` ` ``, `|`, `<@`.
  - `TestWanted`: player kills player yes; AI kills player yes / no with `AIKillers=false`; player kills AI only with `AIVictims`; `death` only with `Deaths`; `chat` never.
  - `TestDelay`: `Delay` 5 min, fake clock; a kill at t0 is not sent at t0+4m59s, is sent at t0+5m (one `Notify` with that line, targets from config).
  - `TestBatchSplit` (Review Focus 4): 60 due kills → several `Notify` calls, each `Lines` ≤ 2000 bytes, order kept.
  - `TestFailedPostDropped`: the fake notifier fails the first batch → it is logged and never sent again; the next due kill is sent on time.
  - `TestQueueCap`: 501 queued kills → the oldest is dropped, one log line.
  - `TestSetKeepsQueue` (Review Focus 2): kill queued, `Set` again with the same instance and a new delay → the queued kill is sent at its old due time.
  - `TestSetStopsRemoved`: instance removed from `cfgs` → its goroutine ends (goleak).
  - `TestHungDiscordDoesNotBlock`: the fake notifier blocks until its ctx ends → `hub.AddFeed` and a second instance's killfeed keep working; the stuck post is dropped after the 5 s post timeout (fake clock / short timeout seam), and cancelling the Manager's ctx returns `Wait` promptly.
  - `TestStartsAfterNewest`: events already in the hub before `Set` (also the ones loaded from the feed log after a restart) are not posted.
  In `notify` tests: payload has `"allowed_mentions":{"parse":[]}`; a 429 is an error like any other status ≥ 300. In `site` tests: unknown webhook name in `killfeed.discord` → validation error naming it; negative or >24h delay → error.
- [ ] **Step 2: Run** `go test ./internal/killfeed/ ./internal/notify/ ./internal/site/` → FAIL.
- [ ] **Step 3: Implement.** Nothing on the game side waits for Discord: the hub only stores and emits (non-blocking), the killfeed pulls. Each post runs with its own `context.WithTimeout(ctx, 5*time.Second)`; a post that fails or times out is dropped and logged (no retry), so a slow Discord delays that instance's killfeed by at most 5 s per batch, and cancelling `ctx` ends a post at once. Per instance one goroutine: wakes on the hub's `feed` stream events and on `Tick` (default 1 s ticker), reads `hub.Feed(last, 0)` (so a dropped stream event loses nothing), queues wanted kills with `due = ev.Time + Delay` (FIFO, the delay is the same for all), sends what is due in one batch split at 2000 bytes, one `notify.Event{Kind: KindKill, Instance, Targets: &targets}` per piece. Escaping: backslash before `\ * _ ~ | > # - ` and `[` `]`, `@` followed by U+200B (zero-width space).
- [ ] **Step 4: Wire.** `serve.Options` gains `Notify func(context.Context, notify.Event) error`; `cmd/dzo`'s serve command sets it from the existing `newNotifier(cfg)` (`cmd/dzo/updates.go`, which returns that func type). `newNotifier` also gets `HTTPClient: &http.Client{Timeout: 10 * time.Second}` (today it falls back to `http.DefaultClient`, which has no timeout; AGENTS.md wants one on every client). In `serve.Run`: `killfeed.Manager{Notify: o.Notify, Logf: logf}`, `Set` after `AddHubs` and again on reload, `Wait` on shutdown. A nil notifier with a configured killfeed logs once per instance and posts nothing. Test `TestRunKillfeedPosts` in `serve_test.go` with a fake notifier.
- [ ] **Step 5: Run** `go test -race ./internal/killfeed/ ./internal/notify/ ./internal/site/ ./internal/serve/` → PASS.
- [ ] **Step 6: Docs**: `configuration.rst` killfeed block with an example (`delay: 10m` for PvP), what a line looks like, AI options, that queued kills are lost when `dzo serve` restarts, Discord's rate limit. `make docs`.
- [ ] **Step 7: Commit** `killfeed: Discord killfeed with killer, victim, weapon, distance and an optional delay`.

### Task 7: Web events page with chat input

**Files:**
- Create: `internal/web/templates/events.html`, `internal/web/static/events.js`, `internal/web/e2e_events_test.go`
- Modify: `internal/web/server.go` (routes), `internal/web/handlers.go` (`eventsPage`, `feedFragment`, stream `only`), `internal/web/templates/instance.html` (link "Events"), `internal/web/templates/map.html` (link back), `internal/web/static/app.css`, `internal/web/web_test.go`
- Modify: `docs/web.rst`

**Interfaces:**
- Consumes: `apiclient.Feed`, `apiclient.StreamOnly` (Task 5), existing `POST /b/{backend}/i/{instance}/message`.
- Produces:
  - `GET /b/{backend}/i/{instance}/events` — the page: header with kind filters, the log (newest at the bottom), the input line.
  - `GET /b/{backend}/i/{instance}/feed?after=<id>` — server-rendered rows (`{{define "feedrows"}}`), used for the backlog (newest 200, so the page never opens empty, also right after a restart) and for gap fill after reconnect.
  - `GET /b/{backend}/i/{instance}/stream?only=feed` — the existing relay passes `only` through.
  - Row markup (events.js builds the same with `textContent`): `<li class="ev ev-<kind>" data-id="<id>" data-kind="<kind>"><time>HH:MM:SS</time> <icon> … </li>`; a player name is `<a href="/b/{b}/i/{i}#player-<steamid>">name</a>` when the steam id is known, plain text otherwise (AI, unknown chat names); a position is `<a href="/b/{b}/i/{i}/map?x=<x>&z=<z>&label=<url-escaped short text>">x / z</a>`.
  - Kind groups for the filter checkboxes: Kills & deaths (`kill`, `death`), Chat (`chat`), Players (`connect`, `disconnect`, `kick`), Base building (`build`, `dismantle`), Raids (`destroy`, `lock_cut`), Flags, Stashes, Admin (`admin`, `dropped`). Choice remembered in `localStorage` (try/catch).

- [ ] **Step 1: Failing tests** in `web_test.go` (fake API backend as the other tests): `TestEventsPage` (200, contains the backlog rows from the fake feed, a chat `<script>` text is escaped, player link `#player-765…`, map link with `x=` and `z=`); `TestEventsPageInputNeedsBroadcast` (viewer token: no input form; operator: form posting to `/message` with `style=chat`); `TestFeedFragmentAfter` (passes `after` to the API, 400 on bad value); `TestStreamOnlyPassed`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** handlers and template; the events view carries `CanBroadcast bool` = `info.Role.Can(api.PermBroadcast)` (from `c.Info`, as `instance` reads the role).
- [ ] **Step 4: `events.js`**: EventSource on `stream?only=feed`; append rows built with `textContent`; skip ids ≤ last shown; on `error`/reopen fetch `feed?after=<last>` and append (Review Focus 5); keep ≤ 1000 rows; "tail -f": auto-scroll only while the view is at the bottom, otherwise show a "new events ↓" button; filters hide by `data-kind`. After the input line's request succeeds, `data-reset` clears it (rows.js) and the focus stays in the field; Enter sends.
- [ ] **Step 5: e2e** `e2e_events_test.go` (chromedp, the existing harness): the page shows the backlog; an event added to the fake backend's stream appears without reload; a 1001st event removes the oldest; after the stream drops and reconnects, events added in between appear once; typing in the input line and Enter posts `text` and `style=chat`.
- [ ] **Step 6: Run** `go test -race ./internal/web/` and `make e2e` → PASS.
- [ ] **Step 7: Docs** `web.rst`: the events page, filters, who sees the input line, that chat comes from BattlEye RCon (needs RCon, as bans do). `make docs`.
- [ ] **Step 8: Commit** `web: live events page with chat input`.

### Task 8: Player and map links land on the right place

**Files:**
- Modify: `internal/web/templates/fragments.html` (`players` rows), `internal/web/static/rows.js`, `internal/web/static/map.js`, `internal/web/static/app.css`, `internal/web/e2e_map_test.go`, `internal/web/e2e_pages_test.go`, `internal/web/web_test.go`
- Modify: `docs/web.rst`, `docs/admin-map.rst`

**Interfaces:**
- Produces: player rows `<tr id="player-{{.SteamID}}">`; map page query `x`, `z` (world metres, numbers), `label` (optional text, ≤ 80 chars).

- [ ] **Step 1: Failing tests:** `TestPlayersRowsHaveAnchors` (fragment row has `id="player-765…"`); e2e: open `/b/…/i/…#player-<id>` → after the first poll that row is scrolled into view and has class `hl` (cleared after a few seconds or on click); a hash for a player who is not online shows "This player is not online." above the table instead; open `/map?x=7500&z=7600&label=Kill` → a focus marker at that position with the label as tooltip, map centred on it at zoom ≥ the vehicle zoom used by `zoomTo`; `/map?x=abc&z=1` and `x=-5000` (outside the map size) → no marker, normal fit, no console error.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.** rows.js: on `htmx:afterSettle` of the players poll, once per hash change, find `#player-…` and highlight it; offline note text via `textContent`. map.js: after tiles and bounds are set, read `URLSearchParams(location.search)`, accept only finite numbers inside `[0, size]`, add an `L.marker` with an `elIcon('focus', …)` and `bindTip(layer, label)`, `zoomTo`. The marker goes away with a "×" in its tooltip menu or when the page is reloaded without the query.
- [ ] **Step 4: Run** `go test -race ./internal/web/` and `make e2e` → PASS.
- [ ] **Step 5: Docs**: link targets in `web.rst`, the `?x=&z=&label=` form in `admin-map.rst` (shareable links). `make docs`.
- [ ] **Step 6: Commit** `web: event links open the player row and the map at the place`.

### Task 9: `dzo events` — the feed on the console

**Files:**
- Create: `cmd/dzo/events.go`, `cmd/dzo/events_test.go`
- Modify: `cmd/dzo/root.go` (`newEventsCmd()` in `root.AddCommand`), `internal/admin/feed.go` (`Summary`), `internal/admin/feed_test.go`
- Modify: `docs/cli.rst` (command table), `docs/cheatsheet/template.html` (one line next to `dzo status`), `debian/changelog`

**Interfaces:**
- Consumes: `apiclient.Feed`, `apiclient.StreamOnly` (Task 5); `apiTarget` (`cmd/dzo/player.go`) for `--api`, `--token-file`, `--config`.
- Produces:
  - `func (e FeedEvent) Summary() string` in `internal/admin`: one plain-text line without time and kind, e.g. `Alice killed Bob with M4-A1 (123 m)`, `AI (East) killed Bob with AKM (54 m)`, `Bob died (infected)`, `[Global] Alice: hi`, `Alice built wall_base_down on Fence`, `Alice raised Flag_Chernarus`, `web:bernd player.kick 765… (spam)`. Control characters (C0, DEL, C1, which includes ESC) in untrusted fields become `?`, newlines a space.
  - `dzo events <instance> [-f|--follow] [-n|--lines <n>] [--kind <kind,...>] [--json] [--no-color]`, journalctl-style: without `-f` print the newest `n` (default 20; the backlog is kept on disk, so this works right after a restart) and exit; with `-f` print them, then follow until Ctrl-C. `--kind` takes kinds or the group names of the web page (`kills`, `chat`, `players`, `building`, `raids`, `flags`, `stashes`, `admin`); an unknown name is an error listing the valid ones. `--json`: one `admin.FeedEvent` JSON object per line (as `journalctl -o json`).
  - Line format: `2026-10-21 14:03:12 kill        Alice killed Bob with M4-A1 (123 m)  @ 7512 / 7633` (local time, kind padded to 11, position only when `has_pos`). Colour by group only when stdout is a terminal (`term.IsTerminal`), `NO_COLOR` is unset and `--no-color` is not given: kills red, raids and lock cuts magenta, chat cyan, players green, admin yellow, the rest default.

- [ ] **Step 1: Failing tests.** `feed_test.go`: `TestSummary` (one case per kind above), `TestSummaryStripsControls` (a name with `\x1b[2J`, `\r`, `\x9b` and a chat text with `\n` → no byte < 0x20, no 0x7f, no U+0080–U+009F in the output). `events_test.go`, against an `httptest` API (as `admin_test.go` / `ban_test.go` do): `TestEventsLastN` (`-n 2` asks `feed?limit=2`, prints two lines oldest first, exits 0); `TestEventsKindFilter` (`--kind kills,chat` prints only those; `--kind nope` errors and names `kills`); `TestEventsJSON` (each line parses as `admin.FeedEvent`); `TestEventsNoColorWhenNotTerminal` (output to a buffer has no `\x1b`); `TestEventsFollowReconnects` (the fake stream sends id 3, closes; on reconnect the command asks `feed?after=3`, gets 4 and 5, then the stream sends 5 again and 6 → printed 3,4,5,6 once each; cancelling the context ends the command with exit 0); `TestEventsUnknownInstance` (404 → error message with the instance name).
- [ ] **Step 2: Run** `go test ./internal/admin/ ./cmd/dzo/ -run 'Summary|Events'` → FAIL.
- [ ] **Step 3: Implement.** Follow loop: `StreamOnly(ctx, inst, "feed", …)`; on its end (error or EOF) wait 1 s doubling to 30 s (reset after a successful frame), then `Feed(after=last, 0)` before reconnecting; skip ids ≤ last printed. A gap (first id after reconnect > last+1 and the ring had moved on) prints one line `-- events missed --`, as journalctl marks a reboot. Kind filter is applied client-side (the API returns everything).
- [ ] **Step 4: Run** `go test -race ./internal/admin/ ./cmd/dzo/` → PASS; `go run ./cmd/dzo gen-man "$(mktemp -d /tmp/dzo-man.XXXX)"` writes `dzo-events.1`.
- [ ] **Step 5: Docs** `cli.rst` row and cheatsheet line; `make docs`.
- [ ] **Step 6: Commit** `cli: dzo events shows the event feed, -f follows it`.

### Task 10: End-to-end against a real server, release notes

**Files:**
- Modify: `scripts/test-client.sh` (assert the feed), `debian/changelog`, `docs/overview.rst` (feature list) if it lists features

- [ ] **Step 1:** Extend `scripts/test-client.sh`: run `dzo events <instance> -f --json` in the background for the whole test and check its output at the end; after the client joins, `GET …/feed` contains `connect` for its steam id; after the existing `player_kill`, a `death` with `cause` `self`; after the client leaves, `disconnect`; a chat line the client sends (if the script can make it chat; otherwise skip with a note) appears as `chat` with its steam id. The Discord side and the delay are covered by Task 6's unit tests, not here.
- [ ] **Step 2: Run** `make test-client` and `make test-client ARGS=--exp` → PASS; note it for the commit message.
- [ ] **Step 3: Run** `make check` → PASS (coverage ≥ 85 %); `dpkg-buildpackage -us -uc -b` + lintian as the `deb` CI job, because of the changelog and the PBO.
- [ ] **Step 4: Commit** `test: the event feed and the killfeed against a real server` (message: test-client passed on stable and experimental).

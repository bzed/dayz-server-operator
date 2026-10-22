<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Player database Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Use frontend-design for Task 6. Build on the navigation plan (2026-10-22-navigation-overview); it must be merged first.

**Goal:** One page per installation lists every player who ever connected, and one page per player shows where and when they played, every name they used, and three verdicts side by side: BattlEye's global ban status (asked from BattlEye's master server), Steam's VAC, game and community bans with a link to the profile, and our own bans.

**Architecture:** The history lives in the moderation database (`moderation.Store`, SQLite, migration `0003_players.sql`), keyed by Steam ID. The BattlEye GUID is derived from it (`DeriveGUID`), so the existing sightings join to it. A **presence recorder** in `dzo serve` turns each hub's `players` stream into sessions and names. A **lookup refresher** in `dzo serve` asks BattlEye's master and Steam about players who joined recently and caches the answers for 24 h. An API call can also ask for a fresh lookup for one player. The API exposes it all under the new permission `player.history`, and the web interface renders a list and a player page whose top row holds the three verdicts.

**Tech Stack:** Go stdlib (`net` UDP, `net/http`, `encoding/xml`, `encoding/json`), SQLite via the existing store, `html/template`/htmx, Sphinx. No new module.

**Spec:** the request: "we do not have a player database that shows which player ever connected to us on which server, which names he used. maybe additional info from steam, battleye bans (from battleye direct, not our rcon bans), our bans, and so on." Revised: "do not store battleye kicks (they are from us), but connect to battleye and retrieve ban information for a guid. investigate if there is an api to do that. also investigate what we can retrieve from steam (steam profile link and vac bans should at least be possible). investigate and design a way to present the information."

## Research (2026-10-22)

### BattlEye: global ban status of a GUID

- BattlEye publishes no API for this. Its FAQ and documentation describe no lookup ([FAQ](https://www.battleye.com/support/faq/), [documentation](https://www.battleye.com/support/documentation/)).
- Its **master server answers an undocumented UDP query**, the same one game servers use for the ban check on join. Two independent open-source checkers implement it: [battleyephp/global-ban](https://github.com/battleyephp/global-ban) (MIT, last commit 2026-03-20; `arma31.battleye.com:2344`) and [Qteb/Battleye-Ban-Checker](https://github.com/Qteb/Battleye-Ban-Checker) (`arma2oa1.battleye.com:2324`; its comment says "all domains lead to the same server", and names `dayz1.battleye.com` for DayZ).
- **Protocol:** one UDP datagram with 5 ASCII bytes (`.....`; the C# tool sends `..6..`), then the 32 hex digits of the BattlEye GUID. The answer is 4 header bytes (the first 4 bytes of the request echoed back), then the ban reason in ASCII. An empty reason, or `0`, means not banned. A port with no game behind it does not answer.
- **The port selects the game.** `dayz1`, `arma31` and `arma2oa1.battleye.com` all resolve to 57.129.90.115, so the host name does not matter. With the known-banned GUID `7604904247cf01f911670144f088664a` (given by the user), a one-time sweep of ports 2300–2400 found exactly three that answer:

  | Port | Game | Answer for the banned GUID |
  |---|---|---|
  | 2324 | Arma 2: Operation Arrowhead (and the DayZ mod) | not banned |
  | 2344 | Arma 3 | not banned |
  | 2354 | **DayZ** | `Global Ban #760490` |

  Arma Reforger has no port here. 2334, 2364 and 2374 answered neither a Steam-style GUID nor a UUID-style ID, and `reforger1`, `reforger`, `armareforger1` and `ar1.battleye.com` do not resolve. Reforger identifies players by Bohemia account, not by a GUID derived from the Steam ID, so its bans cannot be looked up for a DayZ player this way.
  The reason is `Global Ban #` plus the first 6 hex digits of the GUID. The two open-source tools only knew 2324 and 2344, which is why the first test (with ports 2324/2344 and a clean GUID) said nothing about DayZ. The public example GUID `a0d1158281d8639495a1908b5a802470` is clean on all three.
- **Purpose:** information on the player page only. The DayZ server enforces BattlEye's global bans itself on join, and dzo neither enforces nor duplicates that. The Arma answers say whether the same Steam account is banned in BattlEye's other games, which helps judge a player.
- **Privacy:** the game server already sends every joining player's GUID to this master, so asking again discloses nothing new.
- **Load:** the master is not ours, and there is no documented limit. dzo asks rarely (see Global Constraints: about 3 packets per player per week at most) and never more than one packet per second.
- **Risk:** undocumented, so it can change or be blocked. dzo treats "no answer" as "unknown", never as "not banned", and backs off.

### Steam

| Source | Key | What it gives | Verified |
|---|---|---|---|
| Profile link `https://steamcommunity.com/profiles/<steamid64>` | no | always valid for a 17-digit ID | yes |
| Community XML `https://steamcommunity.com/profiles/<id>?xml=1` | no | `steamID` (persona name), `privacyState`/`visibilityState`, `vacBanned` (0/1), `tradeBanState`, `isLimitedAccount`, `memberSince`, `location`, `avatarIcon/Medium/Full`, `hoursPlayed2Wk`, `mostPlayedGames` (name, hours) | fetched here for the example ID |
| Web API `ISteamUser/GetPlayerBans/v1/?key=…&steamids=…` (≤100 IDs) | yes | `SteamId`, `CommunityBanned`, `VACBanned`, `NumberOfVACBans`, `DaysSinceLastBan`, `NumberOfGameBans`, `EconomyBan` (`"none"` …); works for private profiles too | **yes, with the user's key** (keyless call → HTTP 400) |
| Web API `ISteamUser/GetPlayerSummaries/v0002/` (≤100 IDs) | yes | `personaname`, `profileurl`, `communityvisibilitystate` (3 = public), `profilestate`, `avatar*`, `personastate`, `timecreated` (Unix), `loccountrycode`, `primaryclanid` | **yes** (public profile) |
| Web API `IPlayerService/GetOwnedGames/v1/?steamid=…&appids_filter[0]=221100&include_played_free_games=1` | yes | `game_count`, `games[].playtime_forever` (minutes) for DayZ, when the game list is public | **yes** (31 944 min for the example) |
| Web API `IPlayerService/GetSteamLevel/v1/` | yes | `player_level` | **yes** |
| Web API `ISteamUser/GetFriendList/v0001/?relationship=friend` | yes | `friends[].steamid`, `friend_since`, when the friend list is public (an error status otherwise) | **yes** (30 friends for the example) |

So **without a key** dzo shows the profile link, Steam name, VAC yes/no, trade ban, limited account, member since and DayZ hours when public. **With a key** it adds the number of VAC and game bans, days since the last ban, community and economy bans, account age, Steam level, DayZ playtime, and the player's Steam friends who also played on our servers (the friend IDs are kept in `steam_friends` and joined against `players`; a list that turns private is cleared on the next refresh). The community XML is not an official API: it is fetched politely (one request every 2 s at most), and dzo falls back to the link alone when it fails. With a key the XML is not fetched at all.

The user's key for manual checks lives in `~/.config/dzo/secrets/steam-web-api.key` (mode 0600, directory 0700), outside every git repository. Tests never read it; they use `httptest` fixtures. Manual verification scripts read it from there and never print it or a URL that contains it.

## Decisions (tell the user at review)

- BattlEye kicks are **not stored**: they are ours. `internal/moderation/service.go` stays as it is.
- The BattlEye lookup has **no configuration option**. It is information, not enforcement (the game server bans), and it is always on. The master address and the game ports are constants in the code, and the docs describe what is sent.
- `steam.profile_lookup` defaults to **on** (community XML, no key). `steam.web_api_key_file` is optional; with it, the Web API replaces the XML.
- BattlEye answers are cached **7 days**, Steam answers 24 h. Both are refreshed only for players seen in the last 30 days. Older players are looked up when someone presses "Check now" on their page, which works at most once per 10 minutes per player.
- No avatar image: the CSP allows images from `self` only, and relaying Steam's CDN is not worth it. The link opens the profile.

## Global Constraints

- AGENTS.md "Documenting configuration" for `steam.profile_lookup`, `steam.web_api_key_file` and `serve.session_retention_days`: the YAML example in `docs/configuration.rst` with default, prose (values, default, errors, what it sends where, when it takes effect: when `dzo serve` starts), and `make docs`. The BattlEye lookup has no option, but `docs/moderation.rst` says what it sends (one UDP packet per game with the GUID to 57.129.90.115 ports 2324/2344/2354, via `dayz1.battleye.com`) and how often.
- BattlEye load: one player round = 3 packets (one per game). At most 1 packet per second overall, a player re-asked after 7 days at the earliest, and only players seen in the last 30 days. After 3 rounds in a row with no answer at all, pause BattlEye lookups for 1 h.
- The Steam key never enters the repository: the dev copy is `~/.config/dzo/secrets/steam-web-api.key`, and the production default in the docs is `/var/lib/dzo/secrets/steam-web-api.key`. `.gitignore` additionally lists `*.key` and `secrets/` as a second guard (Task 4).
- The Steam key is a secret. Never log it or put it in an error (`*url.Error` contains the URL: wrap HTTP errors with the endpoint name only). Read it from a file with mode ≤ 0600; a group- or world-readable file is an error naming the path.
- Timeouts: BattlEye UDP 2 s per try, 2 tries. Steam HTTP 10 s. Rate limits: BattlEye ≤ 1 packet/s, Steam XML ≤ 1 request/2 s, Steam API ≤ 1 request/s (bans and summaries batched by 100; owned games, level and friends are per player, so only for players due a refresh). Every goroutine stops with its context and is waited for on shutdown.
- An unanswered or failed lookup is stored as `unknown` with its time and never overwrites a known answer. Back off for 1 h after 5 consecutive failures per source, and log once per source per hour.
- SQL with parameters only. Searches use `likeEscape`. Names, reasons and Steam fields are untrusted, truncated (names 128 bytes, reasons 256 bytes, XML body read through `io.LimitReader` of 256 KiB), and rendered only through `html/template`. Parse the XML with `encoding/xml` (no entity expansion).
- `player.history` goes to moderator, operator and admin, and is documented in `docs/moderation.rst` (roles table) and `docs/api.rst`.
- Coverage ≥ 85 %. Tests use fake clocks, `httptest` for Steam, and a local UDP listener for BattlEye. No test talks to the internet.

## Review Focus

- `dzo serve` restarts while players are online: leftover open sessions are closed at start, and after the restart the first `players` event opens new ones, with no duplicates.
- The mod disconnects without a final `players` event: the recorder closes the instance's sessions at the hub's `LastSeen`.
- BattlEye's master does not answer (UDP dropped by a firewall, as on the dev host): the page says "BattlEye did not answer (checked …)", never "clean". The old answer is kept if there was one.
- A Steam profile that is private or deleted: the XML has `privacyState` private, or an `<error>` element. The page shows the link and "private profile". A key-based ban lookup still works for private profiles (bans are public), so it is the better source when a key exists.
- A BattlEye reason with control characters or 1 KB of text: stored truncated, shown escaped, and no layout break (`overflow-wrap: anywhere`).

---

### Task 1: Store: players, names, sessions, lookup cache

**Files:**
- Create: `internal/moderation/migrations/sqlite/0003_players.sql`, `internal/moderation/players.go`, `internal/moderation/players_test.go`
- Modify: `internal/moderation/service.go:219` (`maintain` purges sessions), `internal/moderation/ids.go` (`ValidSteamID` if missing)

**Interfaces:**
- Produces:

```go
type PlayerName struct{ Name string; FirstSeen, LastSeen time.Time }
type PlayerInstance struct{ Instance string; FirstSeen, LastSeen time.Time; Sessions int; Played time.Duration }
type Session struct{ Instance, Name string; Start time.Time; End *time.Time }
type LookupState string // "ok", "unknown" (asked, no usable answer), "" (never asked)
type BEGame string // "dayz", "arma3", "arma2oa"
type BEStatus struct{ Game BEGame; State LookupState; Banned bool; Reason string; CheckedAt time.Time }
type SteamInfo struct {
	State LookupState; CheckedAt time.Time; Source string // "community" or "api"
	Persona, Privacy, Location, MemberSince, TradeBan string
	Limited, VACBanned, CommunityBanned bool
	VACBans, GameBans, DaysSinceLastBan *int // nil without a key
	EconomyBan string; DayZHours *float64
	Created *time.Time; Country string; Level *int // key only
	FriendIDs []string                           // key only, public friend lists; SaveSteamInfo replaces the player's rows in steam_friends
}
type PlayerSummary struct {
	SteamID, GUID, Name string; FirstSeen, LastSeen time.Time; Instances []string; Online bool
	BE []BEStatus // one per game, in the order dayz, arma3, arma2oa; BEBanned reports any of them banned
	BEBanned, SteamBanned bool // SteamBanned: VACBanned || CommunityBanned || GameBans > 0
}
type PlayerRecord struct{ PlayerSummary; Names []PlayerName; PerInstance []PlayerInstance; Sessions []Session; Steam SteamInfo }
type PlayerFilter struct{ Query, Instance string; Flagged bool; Limit, Offset int } // Limit default 50, max 200

func (s *Store) PlayerJoined(ctx context.Context, instance, steamID, name string, at time.Time) error
func (s *Store) PlayerLeft(ctx context.Context, instance, steamID string, at time.Time) error
func (s *Store) CloseSessions(ctx context.Context, instance string, at time.Time) error
func (s *Store) Players(ctx context.Context, f PlayerFilter) ([]PlayerSummary, int, error)
func (s *Store) Player(ctx context.Context, steamID string) (PlayerRecord, error) // ErrNotFound when never seen
func (s *Store) SaveBEStatus(ctx context.Context, guid string, st BEStatus) error // one row per (guid, game)
func (s *Store) FriendsSeen(ctx context.Context, friendIDs []string) ([]PlayerSummary, error) // the friends that are in players
func (s *Store) SaveSteamInfo(ctx context.Context, steamID string, in SteamInfo) error
func (s *Store) DueLookups(ctx context.Context, kind string, checkedBefore, seenAfter time.Time, limit int) ([]string, error) // kind "be" → GUIDs, "steam" → Steam IDs
func (s *Store) PurgeSessions(ctx context.Context, cutoff time.Time) (int64, error)
```

  `Save*` with `State == "unknown"` updates only `checked_at` and the state when a known answer exists, so the known answer is kept. `Flagged` filters to players with `BE.Banned`, `SteamBanned`, or an active ban of ours.
- Schema (Unix seconds as in 0001):

```sql
CREATE TABLE players (steam_id TEXT PRIMARY KEY, guid TEXT NOT NULL, name TEXT NOT NULL,
	first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL);
CREATE INDEX players_guid ON players (guid);
CREATE INDEX players_last_seen ON players (last_seen);
CREATE TABLE player_names (steam_id TEXT NOT NULL REFERENCES players (steam_id) ON DELETE CASCADE,
	name TEXT NOT NULL, first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, PRIMARY KEY (steam_id, name));
CREATE TABLE player_sessions (id INTEGER PRIMARY KEY AUTOINCREMENT, steam_id TEXT NOT NULL REFERENCES players (steam_id) ON DELETE CASCADE,
	instance TEXT NOT NULL, name TEXT NOT NULL, started_at INTEGER NOT NULL, ended_at INTEGER);
CREATE INDEX player_sessions_player ON player_sessions (steam_id, started_at);
CREATE INDEX player_sessions_open ON player_sessions (instance) WHERE ended_at IS NULL;
CREATE TABLE be_status (guid TEXT NOT NULL, game TEXT NOT NULL, state TEXT NOT NULL, banned INTEGER NOT NULL DEFAULT 0,
	reason TEXT NOT NULL DEFAULT '', checked_at INTEGER NOT NULL, PRIMARY KEY (guid, game));
CREATE TABLE steam_info (steam_id TEXT PRIMARY KEY, state TEXT NOT NULL, data TEXT NOT NULL, checked_at INTEGER NOT NULL);
CREATE TABLE steam_friends (steam_id TEXT NOT NULL, friend_id TEXT NOT NULL, PRIMARY KEY (steam_id, friend_id));
CREATE INDEX steam_friends_friend ON steam_friends (friend_id);
```

- [ ] **Step 1: Write the failing tests:** `TestJoinLeaveMakesASession` (90 min played, `Online` false after the leave), `TestRenameKeepsBothNames`, `TestJoinTwiceClosesTheOpenSession`, `TestCloseSessionsClosesOnlyThatInstance`, `TestPlayersSearch` (name, old name, Steam ID, GUID, `%` literal, instance filter, `total` with paging), `TestFlaggedFilter` (one player per flag kind and one clean), `TestUnknownKeepsTheKnownAnswer` (save banned, then unknown: still banned, `CheckedAt` updated, `State` unknown), `TestDueLookups` (never asked first, then stale, nothing fresh, nothing not seen within `seenAfter`), `TestPurgeSessionsKeepsNamesAndFirstSeen`.
- [ ] **Step 2: Run them.** `go test ./internal/moderation -run 'TestJoin|TestRename|TestClose|TestPlayersSearch|TestFlagged|TestUnknown|TestDue|TestPurgeSessions'`. Expected: FAIL.
- [ ] **Step 3: Implement** the migration and `players.go`. In `maintain`, purge sessions with `Service.SessionRetention` (`cmp.Or(…, DefaultSessionRetention)`, 365 days, ≤ 0 never).
- [ ] **Step 4: Run** `go test ./internal/moderation` and `make fuzz`. Expected: PASS.
- [ ] **Step 5: Commit** `moderation: player history and a cache for BattlEye and Steam lookups`.

### Task 2: Presence recorder

**Files:**
- Create: `internal/presence/presence.go`, `internal/presence/presence_test.go`
- Modify: `internal/serve/serve.go` (start next to the killfeed manager, `Set` again on reload), `internal/config/config.go` (`Serve.SessionRetentionDays *int`, `SessionRetention()`, the negative check at :353)
- Modify: `docs/configuration.rst` (`serve.session_retention_days`), `docs/moderation.rst` ("Player history": what is recorded, for how long)

**Interfaces:**
- Consumes: `Store.PlayerJoined/PlayerLeft/CloseSessions` (Task 1), `admin.Registry.Hub`, `Hub.Subscribe`, `Hub.Status`.
- Produces:

```go
type Store interface {
	PlayerJoined(ctx context.Context, instance, steamID, name string, at time.Time) error
	PlayerLeft(ctx context.Context, instance, steamID string, at time.Time) error
	CloseSessions(ctx context.Context, instance string, at time.Time) error
}
type Recorder struct{ Store Store; Now func() time.Time; Logf func(string, ...any); OnJoin func(steamID string) }
func (r *Recorder) Set(ctx context.Context, reg *admin.Registry, instances []string)
func (r *Recorder) Wait()
```

  One goroutine per instance. At start it closes leftovers at `Now()`. It diffs every `players` event against the previous set; a `status` event with `Connected == false` closes everything at `LastSeen`; on context end it closes at `Now()`. `OnJoin` (non-blocking) tells Task 5's refresher to look the player up soon.

- [ ] **Step 1: Write the failing tests** (real `admin.Hub`, fake store, `require.Eventually`): `TestRecorderJoinsAndLeaves`, `TestRecorderClosesOnDisconnect`, `TestRecorderClosesLeftoversAtStart`, `TestRecorderClosesOnShutdown`, `TestRecorderSetReplacesInstances`, `TestRecorderCallsOnJoin`; and in config `TestSessionRetentionDefaultAndZero`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement and wire it** in `serve.Run` with the instance names from `BanInstances`; `defer rec.Wait()` before the store closes.
- [ ] **Step 4: Run** `make test-race`. Expected: PASS.
- [ ] **Step 5: Docs** per AGENTS.md (`session_retention_days: 365   # default (unset = 365)` under `serve:`, plus prose). Run `make docs`.
- [ ] **Step 6: Commit** `serve: record player sessions and names from the live player list`.

### Task 3: BattlEye master query

**Files:**
- Create: `internal/battleye/master.go`, `internal/battleye/master_test.go`

**Interfaces:**
- Produces:

```go
type Game struct{ ID moderation.BEGame; Name string; Port int }
// Games are the games BattlEye's master answers for (found 2026-10-22 by a port sweep, see the plan's research).
var Games = []Game{{"dayz", "DayZ", 2354}, {"arma3", "Arma 3", 2344}, {"arma2oa", "Arma 2: OA", 2324}}
const MasterHost = "dayz1.battleye.com"
type GlobalBan struct{ Banned bool; Reason string }
var ErrNoAnswer = errors.New("battleye: master did not answer")
type Master struct {
	Host    string        // default MasterHost; tests point it at 127.0.0.1
	Port    func(Game) int // default g.Port; tests map every game to their fake listener
	Timeout time.Duration; Tries int
}
func (m *Master) Check(ctx context.Context, g Game, guid string) (GlobalBan, error)
```

  `Check` validates `guid` (32 lowercase hex digits; use `moderation.ValidGUID`, or move it to a leaf package if importing would make a cycle) and resolves the host once per call. It sends `"....." + guid` from a fresh UDP socket and reads until the deadline. The answer must be at least 4 bytes, and its first 4 bytes must equal the request's first 4; otherwise ignore it and keep reading. Reason = bytes 4…, trimmed, cut at 256 bytes, control characters removed. `""` or `"0"` means not banned. Defaults: `Timeout` 2 s, `Tries` 2. There is no configuration option (see Decisions); the docs say what is sent.

- [ ] **Step 1: Write the failing tests** against a local `net.ListenPacket("udp", "127.0.0.1:0")` fake master: `TestCheckClean` (answers `....`), `TestCheckBanned` (`....Global Ban #760490` → banned, reason `Global Ban #760490`), `TestCheckZeroIsClean`, `TestCheckIgnoresAStrayAnswer` (wrong header first, then the right one), `TestCheckNoAnswer` (silent listener → `ErrNoAnswer` after `Tries` × a 50 ms test timeout), `TestCheckRefusesABadGUID` (no packet is sent), `TestCheckStripsControlCharacters`, `TestGamesHaveDistinctPorts`.
- [ ] **Step 2: Run them.** `go test ./internal/battleye -run 'TestCheck|TestGames'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Manual check against the real master** (not a test; 6 packets, 1 s apart). A throwaway `go run` in a `mktemp -d /tmp/...` directory checks the banned GUID `7604904247cf01f911670144f088664a` and the clean `a0d1158281d8639495a1908b5a802470` for every game. Expected: banned on DayZ with `Global Ban #760490`, clean on Arma 3 and Arma 2: OA; the clean GUID is clean everywhere. Put the output in the commit message.
- [ ] **Step 5: Commit** `battleye: ask BattlEye's master about a GUID's global bans in DayZ, Arma 3 and Arma 2`.

### Task 4: Steam lookups

**Files:**
- Create: `internal/steamweb/steamweb.go`, `internal/steamweb/steamweb_test.go`, `internal/steamweb/testdata/` (a public profile, a private profile, a not-found `<error>` XML, and GetPlayerBans and GetPlayerSummaries JSON; `REUSE.toml` entry for the directory)
- Modify: `internal/config/config.go` (`Steam.ProfileLookup *bool \`yaml:"profile_lookup"\`` defaulting to true, `Steam.WebAPIKeyFile string \`yaml:"web_api_key_file"\``), `docs/configuration.rst`, `.gitignore` (add `*.key` and `secrets/` as a second guard next to `.private/*`)

**Interfaces:**
- Consumes: `moderation.SteamInfo` (Task 1).
- Produces:

```go
func ProfileURL(steamID string) string // "https://steamcommunity.com/profiles/" + id; "" for an invalid ID
func ReadKey(path string) (string, error)
type Client struct{ Key string; Community, API string; HTTP *http.Client } // bases default to the real hosts
func (c *Client) Community(ctx context.Context, steamID string) (moderation.SteamInfo, error)      // ?xml=1
func (c *Client) Bans(ctx context.Context, ids []string) (map[string]moderation.SteamInfo, error) // key only, ≤100 ids, GetPlayerBans + GetPlayerSummaries merged
func (c *Client) Details(ctx context.Context, steamID string, in *moderation.SteamInfo) error     // key only: GetOwnedGames (DayZ 221100), GetSteamLevel, GetFriendList; a private part leaves its field nil/empty, not an error
```

  `Community` maps the XML: `steamID`→Persona, `privacyState`→Privacy, `vacBanned`→VACBanned, `tradeBanState`→TradeBan, `isLimitedAccount`→Limited, `memberSince`, `location`, and the DayZ hours from `mostPlayedGames` (`gameLink` ending in `/app/221100`). It sets `Source "community"`. `Bans` sets the counts, `Created` (`timecreated`), `Country` (`loccountrycode`), the persona and visibility, and `Source "api"`. `Details` sets `DayZHours` (`playtime_forever` / 60), `Level` and `FriendIDs` (capped at 2000). The field names are the ones verified with the user's key (see Research). Errors name the endpoint and status only: never the URL, which holds the key.

- [ ] **Step 1: Write the failing tests** (`httptest`): `TestCommunityPublicProfile`, `TestCommunityPrivateProfile` (Privacy `private`, VAC still parsed when present), `TestCommunityNotFoundIsUnknown`, `TestCommunityBodyIsLimited` (a 1 MiB body → error, no OOM), `TestBansMergesBothEndpoints`, `TestBansErrorHidesTheKey` (403; `err.Error()` lacks the key), `TestDetailsPrivateGamesAndFriends` (owned games `{}` and friend list 401 → nil hours, no friends, no error), `TestDetailsFixtures` (fixtures shaped like the verified answers: 31944 min → 532.4 h, level 55, friends), `TestReadKeyRefusesAGroupReadableFile`, `TestProfileURL`. Config: `TestSteamProfileLookupDefaultsOn`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** `http.Client{Timeout: 10 * time.Second}`. The XML request sends a `User-Agent` of `dzo/<version>`.
- [ ] **Step 4: Verify against Steam with the user's key** (manual, not a test): a throwaway `go run` in a `mktemp -d /tmp/...` directory reads `~/.config/dzo/secrets/steam-web-api.key` with `ReadKey` and prints `Bans` and `Details` for 76561198066209976. Expected: no bans, created 2012-06-29, country UA, level 55, about 532 h of DayZ, about 30 friends. Check the output contains no key (`grep -c` for the key file's content is 0) before pasting it into the commit message. Then `git status --short` must not list any key file. Run `make test`, `make licenses`, `make vulncheck`.
- [ ] **Step 5: Docs** per AGENTS.md: under `steam:` add `profile_lookup: true   # default` and `web_api_key_file: /var/lib/dzo/secrets/steam-web-api.key   # default: empty (no key)`, plus prose: what each fetches, from where, how often, that the community page is not an official API, where a key comes from (https://steamcommunity.com/dev/apikey), the file mode, and when it takes effect. Run `make docs`.
- [ ] **Step 6: Commit** `steamweb: Steam profile and ban lookups, keyless or with a Web API key`.

### Task 5: Lookup refresher and API

**Files:**
- Create: `internal/lookups/lookups.go`, `internal/lookups/lookups_test.go`, `internal/api/players.go`, `internal/api/players_test.go`, `internal/apiclient/players.go`
- Modify: `internal/serve/serve.go` (start the refresher, connect `Recorder.OnJoin`), `internal/api/tokens.go` (`PermPlayerHistory = "player.history"` for moderator, operator, admin), `internal/api/server.go:167` (routes), `docs/api.rst`, `docs/moderation.rst`

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces:

```go
type Refresher struct {
	Store interface {
		DueLookups(ctx context.Context, kind string, checkedBefore, seenAfter time.Time, limit int) ([]string, error)
		SaveBEStatus(ctx context.Context, guid string, st moderation.BEStatus) error
		SaveSteamInfo(ctx context.Context, steamID string, in moderation.SteamInfo) error
	}
	BE *battleye.Master // nil: off
	Steam *steamweb.Client; SteamOn bool
	Now func() time.Time; Logf func(string, ...any)
}
func (r *Refresher) Run(ctx context.Context)                         // every minute: due lookups, rate-limited, backoff
func (r *Refresher) Nudge(steamID string)                           // non-blocking: look this player up next round
func (r *Refresher) LookupNow(ctx context.Context, steamID string) error // both sources, synchronously, ≤ 5 s
```

  API: `GET /api/v1/players?q=&instance=&flagged=&limit=&offset=` → `{"players": [...], "total": n}`; `GET /api/v1/players/{steamid}` → `PlayerRecord` plus `"bans"` (ours, `ListBans` with `Query: steamID`), `"identities"` (addresses only with `ip.history`, as `evidence.go:170` does) and `"profile_url"`; `POST /api/v1/players/{steamid}/lookup` → runs `LookupNow`, audited as `player.lookup`, answers the fresh record. All three use `PermPlayerHistory`. Client: `Players`, `Player`, `Lookup`, returning `api.PlayerDetail{moderation.PlayerRecord; Bans []moderation.Ban; Identities []moderation.Sighting; ProfileURL string}`.

- [ ] **Step 1: Write the failing tests:** `TestRefresherLooksUpDuePlayers` (fake store, BattlEye and Steam fakes), `TestRefresherBacksOffAfterFailures`, `TestRefresherKeepsTheRateLimit` (fake clock: 10 due GUIDs need ≥ 5 s of fake time), `TestNudgeComesFirst`, `TestLookupNowTimesOut`. API: viewer 403 on all three, moderator 200, addresses only for admin, invalid ID 400, unknown 404, `limit=500` capped at 200, `POST …/lookup` writes an audit entry and needs CSRF-free API auth as the other POSTs do.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement and wire it.** BattlEye is always on: a round asks the three games for one GUID, 1 packet/s, recheck after 7 days, and pauses 1 h after 3 rounds without any answer. Steam is on per `ProfileLookup`, with the key from `ReadKey` when the file is set; a bad key file is a startup error. With a key, use `Bans` (batched) plus `Details` per due player; without one, `Community`. Recheck Steam after 24 h. `seenAfter` = now − 30 days for both. `LookupNow` refuses (HTTP 429 from the API) a player looked up in the last 10 minutes.
- [ ] **Step 4: Run** `make test-race`. Expected: PASS.
- [ ] **Step 5: Docs:** endpoints in `docs/api.rst`, `player.history` in the roles table, "Player history" in `docs/moderation.rst` (where the verdicts come from, how fresh they are, "unknown"). Run `make docs`.
- [ ] **Step 6: Commit** `api: player history with BattlEye and Steam verdicts`.

### Task 6: Presentation

**Files:**
- Create: `internal/web/players.go`, `internal/web/templates/playerdb.html` (templates `"playerdb"`, `"player"`, `"verdicts"`), `internal/web/players_test.go`, `internal/web/e2e_players_test.go`
- Modify: `internal/web/server.go` (routes `GET /b/{backend}/players`, `GET /b/{backend}/players/{steamid}`, `POST /b/{backend}/players/{steamid}/lookup`; `page.HasPlayers = true`), `internal/web/templates/fragments.html` (panel: `Profile` link plus a compact `"verdicts"`; player rows: a marker for flagged players), `bans.html` (Steam ID targets link to the player page), `events.html` (`who` links to the player page), `internal/web/static/app.css`
- Modify: `docs/web.rst` ("Players" entry; drop the player database from "Not available yet"), `internal/web/web_test.go` (`TestPlayerPanelHasAProfileButtonThatDoesNothingYet` → `TestPlayerPanelLinksTheProfile`)

**Design.** It stays inside the existing identity: survey-paper palette, Barlow Condensed names, flare red only for what is actually bad. The one bold element is the **verdict row**: three equal cells under the name. Each answers one question in a sentence and says how old the answer is. Everything below is quiet reference material.

```
Fritz the Cat                                   76561198000000000  [copy]
● online on alpha · also known as fritz, xX_fritz_Xx and 3 more
[Ban…]  [Steam profile ↗]  [Check now]

┌ BattlEye ───────────────┬ Steam ──────────────────────┬ Here ────────────────────────┐
│ DayZ: Global Ban #760490 │ 2 VAC bans, last 214 days   │ Banned until 2026-11-01      │
│ Arma 3: not banned       │ ago · 1 game ban            │ #12 · all instances          │
│ Arma 2: not banned       │                             │                              │
│ asked 2 d ago            │ checked 5 h ago             │                              │
└──────────────────────────┴─────────────────────────────┴──────────────────────────────┘
  Steam: “fritz” · public · since June 2012 · DE · level 55 · 532 h DayZ
  Steam friends seen here: Kater Karlo (alpha, last 2026-10-20), …

Played on     alpha  first 2026-03-02  last today        41 sessions  96 h
              beta   first 2026-05-11  last 2026-09-30    3 sessions   4 h
Names         2026-03-02  Fritz the Cat   (current)
              2026-04-18  xX_fritz_Xx
Sessions      the newest 50, one line each
Addresses     (only with ip.history) as in the ban form
```

- Cell states, in plain words: **BattlEye** one line per game, DayZ first: "<game>: not banned" / "<game>: " plus the reason / "<game>: no answer"; "Not asked yet" when no game was asked. The cell is a ban cell when any game reports a ban; a ban in Arma only is still shown in red, with the game name, because it is the same Steam account. **Steam** "No bans" / "VAC banned" (keyless) or the counts with a key / "Private profile, no ban data" / "Lookups are off". **Here** "No bans" / "Banned until …" or "Banned permanently" with the ban number linked / "Ban expired …" (muted). Red (`--flare`) text and a 3 px left rule only on a cell that reports a ban. "Not banned" is muted ink, not green: green would claim more than the sources can promise.
- The Steam facts line shows only what the source gave (keyless: name, privacy, since, location, DayZ hours when public). "Steam friends seen here" (key only, public friend lists) links each friend's player page, newest first, at most 10 with "and N more"; it is omitted when there are none.
- Below 700 px the three cells stack in the same order. "asked 2 h ago" uses the dashboard's `span`.
- The list page has a search, an instance filter and "Only flagged players". Columns: Name (+ online dot), Last seen, First seen, Instances, then three narrow verdict columns with a glyph and text for screen readers (`fa-shield` BattlEye, `fa-steam` Steam, `fa-ban` Here), filled only when flagged.
- In the live player list and the action panel, a flagged player gets a small flare dot after the name with `title` text ("BattlEye global ban"). The panel shows the compact `"verdicts"` template (one line per source), so a moderator sees it before acting.

- [ ] **Step 1: Write the failing tests:** names and reasons escaped; each cell state renders its sentence from a fixture `PlayerDetail` (table test over the states above); only ban cells carry the class `bad`; "Check now" posts with CSRF and swaps the verdict row (htmx `hx-target`); viewer → readable 403 message; the panel's Profile link points at `/b/main/players/7656…`; the flagged dot appears in the player row for a BattlEye-banned player. E2E: `TestE2EPlayerPageFromThePanel` and `TestE2EVerdictsStackOnAPhone` (360 px: no horizontal scroll, cells in order).
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `make test`, `make e2e`, and screenshots at 1280 and 360 px, light and dark, into a `mktemp -d /tmp/…` directory. Look at them and fix what is off.
- [ ] **Step 5: Docs:** `docs/web.rst` "Players" entry: the verdict row and what each cell means, freshness and "Check now", the permission. Run `make docs`.
- [ ] **Step 6: `make test-client`** (the player panel in `internal/web` changed), stable and `ARGS=--exp`.
- [ ] **Step 7: Commit** `web: player database with BattlEye, Steam and ban verdicts`, with the test-client result in the message.

<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# dayz-server-operator — Implementation Plan

Status: **planning** (no code yet) · Last update: 2026-09-26

This document has three jobs:

1. **Part A** — inventory of *everything* the legacy `dayzdockerserver` does (main + the five
   production branches), including quirks and bugs, so nothing gets lost.
2. **Part B** — the consolidated requirement set distilled from A.
3. **Part C–F** — the design of the replacement, project layout, phased roadmap and open items.

Legacy source analysed: `../dayzdockerserver` (branches `main`, `chernarus`, `livonia`, `hashima`,
`onlyup-prisonbreak`, `deerisle`) plus the `dayz_restart` binary copied next to it.

---

## 0. Decisions taken so far

| # | Topic | Decision |
|---|-------|----------|
| D1 | Container runtime | **Podman, rootless**. Hard requirement. |
| D2 | Unit management | **Quadlets** (`.container`, `.image`/`.build`, `.volume`, `.timer`), no `podman generate systemd`. |
| D3 | Tenancy | **One service user, many instances.** DayZ server files and the workshop cache are downloaded once and shared read-only. |
| D4 | Configuration | **Tool repo (this one) + separate "site" config repo** holding shared mod integrations and `instances/<name>/`. No more branch-per-server. |
| D5 | Mission preparation | **Runs on the host in the operator**, which renders the mission/profile into the instance directory. The runtime image stays dumb (it only execs `DayZServer`). |
| D6 | Networking | **`Network=host` by default**, per-instance override to published ports (pasta). |
| D7 | Updates | **Mod updates: fully automatic, policy per instance** (`auto` / `notify` / `manual`), triggered by systemd timers. **Server (product) builds: never automatic.** dzo only detects and reports a new build. Downloading it and switching instances to it is always a manual step, because a new game version often needs a wipe, new or updated mods, or other changes. **No rollback of downloads:** a correctly downloaded broken mod or build needs a fix, not an older version. What dzo provides is a **forced re-download** of a mod (`dzo mod refresh`), also for a corrupted steamcmd cache (§C7). |
| D8 | RCon / restarts | Reimplemented natively. **Never rely on `#shutdown`** (it hangs DayZ, deerisle). Restart = countdown → lock → kick (with retries) → `systemctl --user restart`. |
| D9 | Low-level hacks | LD_PRELOAD (`fix_dayz.so`) no longer needed, but the instance model must keep **generic hooks for extra env / mounts / preload** in case such a workaround is needed again. |
| D10 | Node.js | **Excluded.** The legacy Vue/Express web code is ignored; only its *ideas* (mod search, XML editor, status) inform later phases. |
| D11 | Web stack (later) | Server-rendered + htmx, no JS build chain. The user's suggestion was FastAPI + Jinja/HTMX. See D12 for the language this becomes. |
| D12 | Implementation language | **Go** (see §C1). The user suggested Python and left the choice open once Go libraries were raised. |
| D13 | Mission directory | **The live mission dir (`mpmissions/<map>`) is persistent game data and is NEVER wiped or recreated.** It is initialised once from the instance's pristine mission (`servermpmissions`, per-instance, usually from a git repo). Afterwards the operator only *updates files in place*: it copies from pristine, merges and writes generated files. See §C6. |
| D14 | Server products | Several server "products" side by side: **DayZ stable (223350) and DayZ experimental (1042420)** now, extensible to future products (e.g. a DayZ successor). Pristine missions are per instance anyway. |
| D15 | Licence | **AGPL-3.0-or-later** if every dependency is compatible (checked so far: Apache-2.0, MIT, BSD are compatible; RCon is our own code). Otherwise GPL-3.0; MIT as the last resort. A licence check is enforced in CI. Copyright **Bernd Zeimetz <bernd@bzed.de>**. The repository follows the **REUSE** specification: every file carries `SPDX-FileCopyrightText` and `SPDX-License-Identifier` headers (files that cannot hold comments, such as images or fixtures, are covered by a `.license` file next to them or by `REUSE.toml`), licence texts live in `LICENSES/`, and `reuse lint` in CI fails on any file without licence information. |
| D16 | Site repo | Repo name/location free. The **git remote is configurable by URL**, and pull/commit/push behaviour is configurable. |
| D17 | Naming and packaging | Binary **`dzo`**, service user **`dayz`**. Delivered as a **Debian package**. |
| D18 | Monitoring | **Discord** notifications, plus **Icinga**-compatible checks for every instance. **Container health checks are mandatory and must work** (startup + liveness). |
| D19 | Scheduling | **Never cron.** Periodic work (restarts, update checks, image refresh, status export) uses **systemd timers**, unless the `dzo serve` daemon schedules it itself. |
| D20 | Quality gate | **Minimum 85 % test coverage** (Go statement coverage), enforced in CI **from the first commit, no ramp**. Excluded only: generated code (sqlc output, `*_gen.go`, embedded assets), listed file by file in `.coverage-exclude`. Hard-to-test areas (systemd D-Bus, git push, UDP) are covered via fakes (§C15). |
| D21 | CI | **GitHub Actions and GitLab CI**, both thin wrappers over the same `make` targets. Builds the `.deb` as an artifact. **No apt repo** for now. |
| D22 | Metrics | **Prometheus `/metrics`** export (plain HTTP by default, optional hot-reloadable TLS). Icinga runs **remotely**, so monitoring is designed for network access (metrics/status endpoint + A2S). Logs go to the existing Loki (gigapipe) stack. |
| D23 | Own servermod | **Server-side admin mod `dzo-admin`**, a separate mod fully independent of LogZ. **All in-game changes** (messages, teleport, item spawns, vehicle repair/delete, …) and the live state for the map (players, vehicles) go through it. Observation data (logs, metrics) stays in LogZ/MetricZ, which dzo can optionally read (D38, §C23). Provides a **map marker API** (config-only class watch rules, soft-dependency script API, file drop, icons shipped in mod PBOs) so other mods can easily add their content to the map. Developed with the `dayz-dev` skill. |
| D24 | Hostnames | **No real hostnames** in plans, docs, defaults or tests. |
| D25 | Map tiles | Generated by dzo from the map's data PBO (satellite `layers/S_*_lco.paa` + rvmat georeferencing). Source: vanilla maps as a single file from the client depot (the server install only has stripped placeholders), manual upload/configured path as fallback, and a **configured path required for modded maps**; no PBO scanning (§C17). Pipeline: PAA → PNG, resized into an XYZ pyramid, served by dzo and exportable to our own tile server. No third-party tiles. |
| D26 | Database | **SQLite by default, PostgreSQL optional** (same schema, sqlc + goose; CI tests both). Holds players, sessions, bans, schedules, audit, users and jobs. |
| D27 | Player identity | Players are **always tracked by SteamID64**, never by name. BE-GUID-only sessions (RCon without `dzo-admin`) are linked to the Steam ID once known. |
| D28 | Analytics | **Optional ClickHouse** for high-volume append-only history (positions, sessions, chat, population, admin events) using MergeTree/TTL/ReplacingMergeTree/AggregatingMergeTree + materialized views. SQLite/PostgreSQL remain the source of truth for mutable state (§C19). |
| D29 | Platform | **Debian trixie** everywhere (host, images, `.deb`); plan for **podman 5.4.2** / systemd 257 / **kernel ≥ 6.12** (trixie-backports kernel acceptable if needed); runtime tools **only from Debian packages** (Python only with packaged modules); **Go built with the latest upstream toolchain** (go1.27.x), not trixie's golang-go (§C0). |
| D30 | RCon | **Built-in BattlEye RCon implementation in dzo**, with no external tools (`bercon-cli`, `dayz_restart`) and no external RCon library (§C8). |
| D31 | Backups | **Built into dzo via btrfs snapshots**: instances must live on btrfs subvolumes (created unprivileged), read-only snapshots before changes (incl. automatic mod updates, with the server stopped), configurable retention with safe cleanup, full/partial restore, and an optional `post_backup` hook for offsite copies (§C20). |
| D32 | Migration | **Fresh start, no data import.** Legacy servers are only a config source: a converter builds an **example site config** from the legacy git branches (never from volumes), which is reviewed by hand. New worlds, profiles and player data start empty (§C21). |
| D33 | Boot test | A render is only proven good once a real `DayZServer` has booted it. **`dzo test boot`** starts the server headless on the rendered output (mission, serverDZ.cfg, keys, mods) in a disposable tree, waits for readiness, stops it and checks the logs (script compile errors, script modules not loaded, CE/mission errors). The method follows the `dayz-dev` skill (`testing/local-server.md`). **Development tool only:** it runs on developer machines (or a dedicated test runner), **never on a host with live servers**, and is not part of the update or restart path (§C22). |
| D34 | Web authentication | **Password + TOTP MFA required by default** for every web user (enrolment is forced at first login, no skip). Local users with argon2id, one-time recovery codes, server-side sessions, step-up re-authentication for sensitive actions, lockout protection. OIDC is optional and must prove MFA, or local TOTP is required on top. No default passwords: the first admin is created with the CLI via a one-time enrolment link (§C12). |
| D35 | Engineering style | **Lazy engineering** (in the spirit of the `ponytail` skill): build only what is needed now, reuse before writing (existing dzo code → Go stdlib → platform features such as systemd/podman/btrfs/the DB → existing well-maintained libraries → new code), no speculative abstractions, options or scaffolding, boring over clever. Never lazy about validation at trust boundaries, data-loss prevention, security or tests. Deliberate shortcuts with a known ceiling are marked `ponytail:` in the code. **This plan describes the target picture. Each phase implements only what it needs, and anything not needed yet is deferred rather than pre-built.** |
| D36 | Documentation | User/admin documentation as a **static website built with Sphinx** (reStructuredText, built-in theme, no extensions; `python3-sphinx` from trixie) in `docs/`. It is **independent of this plan** (describes dzo, never references the plan), is built in CI with warnings as errors, is shipped in the package under `/usr/share/doc/dzo/html/`, and is **served by the web UI under `/docs/`** as static files, without login. |
| D37 | Local servermods | Server-side mods can come from **outside the Steam Workshop** (`{local: <name>, server: true}`): a directory in the site repo, a directory on the host, or a pinned release archive (URL + sha256). They get the same **immutable, content-hashed generations**, update policies, dependency validation and boot tests as workshop mods. Local mods are **servermods only** (clients cannot fetch them). **`dzo-admin` is distributed this way** (§C7, §C16). |
| D38 | MetricZ, LogZ, cheat findings | **Optional data sources, never required.** MetricZ (our fork) writes a small metrics file into the profile dir, and dzo re-exports it without any extra exporter. **LogZ (our fork) data is only used through Loki, which is a hard requirement for any LogZ feature.** On a busy server LogZ writes well over 1 GB/day (several lines per shot, 60–100 shots/s on deathmatch), so dzo never parses or keeps LogZ files. It queries Loki, and so does logz-analyzer. **Division of work:** `dzo-admin` *acts* and supplies live state; LogZ/MetricZ *observe*. `dzo-admin` gets no metrics collector and no combat/kill logging. **logz-analyzer** stays the only implementation of the cheat detectors. dzo runs it and tracks its findings per player (SteamID64) for review. It never kicks or bans automatically (§C23). |
| D39 | Servermod distribution | **Own servermods (`dzo-admin`, our MetricZ/LogZ forks) are git submodules under `servermods/`**, pinned to tested commits (MetricZ/LogZ: MIT; analyzer and `dzo-admin`: AGPL-3.0-or-later, same copyright holder). `make servermods` packs them into PBOs (`pbo` library, no DayZ Tools); the `.deb` ships the boot-tested PBOs under `/usr/share/dzo/servermods/<name>/` as `{local: <name>}` sources (D37). `compat.yaml` records the tested set (server build id, servermod commits + PBO sha256, workshop mod versions); `dzo test boot` against it is the release gate. `debian/copyright` carries a stanza per submodule licence. |

---

# Part A — Analysis of the legacy system

## A1. Repository and branch topology

* `main` = upstream (Daniel Ceregatti, last commit 2024-10-18). It uses docker-compose with a `web`
  container (Node/Vue, **dropped**) and a `server` container.
* All five production branches fork from `main`'s tip `cda9de8` and never merge back:

| Branch | Ahead of main | Last tooling change | Map (template) | Ports game/rcon/query | Notes |
|---|---|---|---|---|---|
| chernarus | 68 | 2026-09-25 | `dayzOffline.chernarusplus` | 5302/5303/57016 | PvP, `-limitFPS=200 -cpuCount=2`, many custom map additions |
| livonia | 104 | 2026-09-25 | `dayzOffline.enoch` | 2302/2303/27016 | PvE+AI, Expansion traders, `-cpuCount=10`, **host network**, weather + traderstocks pre-start, `nominal_modifier.py` |
| hashima | 76 | 2026-04-08 | `main.hashima` | 3302/3303/37016 | `-cpuCount=4`, dz-common default ports patched to 33xx, fallback mission = hashima, mod-update scripts |
| onlyup-prisonbreak | 69 | 2026-06-02 | `dayzOffline.chernarusplus` | 4312/4313/47116 | **host network**, uses external `dayz_restart`, oldest merge logic |
| deerisle | 127 | 2026-04-08 | `empty.deerisle` | 8302/8303/8716 | Richest `dz` (EditorFiles, zombie territories, effect areas, underground triggers, custom keys, log rotate), `-cpuCount=$(nproc)`, healthcheck disabled, GeoIP mount, `forceupdate` and `modupdate_by_id` |

Every production server runs as its **own Linux user** (e.g. `dayz-livonia`) with its
own checkout, its own podman volumes and its own user systemd units. A `volumes` symlink in the
checkout points into the podman volume storage, and scripts reference `volumes/*_profiles/_data/...`.

## A2. Runtime architecture (production branches)

```
host user dayz-<name>
 ├─ dzpodman                    (bash) builds images, creates containers, generates systemd units
 ├─ ~/.config/systemd/user/container-<prefix>-{manager,server}.service  (Restart=always)
 ├─ cron (not in repo)          mod freshness check → dz mi → in-game countdown restart
 └─ /var/log/dayz/<name>        LogZ NDJSON output (bind-mounted)

container <prefix>-manager  (debian:trixie-slim + steamcmd + bercon)      idles: `while sleep 10`
   used via `dzpodman exec manager dz ...` for login/install/update/mods
container <prefix>-server   (debian:trixie-slim + xmlstarlet/jq/gwenhywfar/python3)
   entry: dz start → render mission → exec DayZServer, loop on exit unless dont_restart

named volumes (prefix_*): homedir_server(/root, steam session), serverfiles, mods(workshop content),
servermpmissions (pristine missions from git), mpmissions (live missions incl. storage_1), profiles
bind mounts: ./files → /files (config), ./server → /server (scripts), /usr/local/dayz, /var/lib/GeoIP
```

`dzpodman` (host script):

| Command | Behaviour |
|---|---|
| `build` | `podman pull debian:trixie-slim`, then builds each `*/Dockerfile` as `<prefix>-<dir>:latest` + `:<timestamp>`, then runs `cleanup` |
| `start` | For each image: `podman create` with ports/volumes/env from `config/containers/<name>.json`, healthcheck (`healthcheck`, interval 50–60 s, retries 3–5, start period 600–800 s, on-failure=restart), stop-timeout 30–60 s, optional `--network=host`. Then `podman generate systemd --new`, patches in `Restart=always` and an optional custom `ExecStop` wrapper (`stop_wrapper.sh`: `podman exec <c> <stop_command>` before the default stop), then `enable --now`. |
| `stop` | stop + disable + **delete** the unit files |
| `restart` / `rebuild` | stop+start / stop+build+start |
| `cleanup` | remove `localhost/<prefix>*` images older than 7 days (except `latest`) |
| `logs` | tmux session with one tiled pane per container `podman logs -f` |
| `exec <c> cmd` / `run <c> cmd` | exec into the running container / one-off `podman run --rm -it` with the same volumes |

Volume names get a prefix from the checkout directory name, and relative host paths are `realpath`ed.

## A3. Function inventory — `manager` (`manager/bin/dz`, formerly `web/bin/dz`)

| Cmd | Function | Details |
|---|---|---|
| `g\|login` | Steam login | Prompts for a user name (default `anonymous`) and writes `~/steamlogin` (`steamlogin=<user>`). Runs `steamcmd +login` interactively (password + Steam Guard); the session is cached in `~/Steam`. |
| `i\|install [force]` | Install server | `steamcmd +force_install_dir /serverfiles +app_update 223350 validate`, then `map default` (clones BI `DayZ-Central-Economy` for `dayzOffline.*`, because Steam no longer ships mpmissions). |
| `u\|update [force]` | Update server | Compares `buildid` in `appmanifest_223350.acf` with `app_info_print` (after deleting `appinfo.vdf` to defeat caching). If they differ: `app_update validate` + `modupdate`. |
| `f\|forceupdate` | Force update | `app_update validate` + `modupdate` unconditionally (deerisle, chernarus, hashima, onlyup). |
| `m\|modupdate` | Update all mods | One steamcmd call with `+workshop_download_item 221100 <id>` for every `@*` symlink in /serverfiles. |
| `mi <ids…>` | Update given mods | One steamcmd per id. Success is detected by grepping `Success. Downloaded item <id>`. Re-runs `x <id>` and returns non-zero if any failed (not on chernarus). |
| `a\|add <id>` | Add mod | Downloads the item, reads the name from `meta.cpp` (`name = "…"`, stripped to alnum), symlinks `/serverfiles/@<Name>` → workshop dir, then runs `xml` + `map`. |
| `r\|remove <id>` | Remove mod | `rm -rf` the workshop dir and the symlink. |
| `p\|map <id>` | Install map mission | Sources `/files/mods/<id>/map.env` (`MAP, DIR, REPO, MPDIR`), git clones/pulls into /tmp and copies `MPDIR` (glob) into pristine mpmissions. `map.sh <id> uninstall` backs up and deletes. |
| `x\|xml <id>` | Fetch/normalise integration XML | `xml.sh`: for each supported file, the `xml.env` variable decides the source: `http…` → curl download; `local` → copy from `/files/mods/<id>/`; `./path` → copy from inside the mod. The file is linted (xmllint/jq) and `start.sh` is copied into the mod dir. Then `installxml`: fixes quirks (`<events>`→`<eventposdef>` in cfgeventspawns, missing root element, missing XML declaration), lints, and **overwrites the file inside the workshop dir**. |
| `l\|s\|status` | Status | Steam login state (anonymous or not), installed yes/no, version (`strings DayZServer \| grep "DayZ x.y.z"`), mod table (id, name, URL, size). |

Supported integration files by stage (union over branches):
`types, cfgspawnabletypes, events` (→ CE folder), `cfgeventgroups, cfgeventspawns, cfgenvironment,
cfgrandompresets, mapgrouppos, mapgroupproto` (→ merged into mission file), `cfggameplay.json`
(deep merge), `cfgweather.xml` (replace), `init.c` (patch), `start.sh` (script).

## A4. Function inventory — `server` (`server/bin/dz`)

| Cmd | Function | Details |
|---|---|---|
| `start` | Render + run (container CMD) | See the pipeline in A5. Runs `./DayZServer "-mod=…" "-servermod=…" <params>` in the foreground. On exit it prints the error.log / script*.log / *.RPT tails, and **re-execs itself** unless `/serverfiles/dont_restart` exists. `DEVELOPMENT=1` blocks instead of starting, and `DONT_START` renders only. |
| `stop` | Stop | touch `dont_restart`, then `kill -TERM DayZServer`. |
| `r\|restart` | In-container restart | `kill -TERM` (the loop restarts it). |
| `norestart` | | touch `dont_restart` (deerisle). |
| `f\|force` | Kill | `kill -KILL`. |
| `c\|config` | Adopt serverDZ.cfg | Diff `/files/serverDZ.cfg` vs `/profiles/serverDZ.cfg.save`, interactive y/N. On first start the `.save` is created automatically. |
| `a\|activate <id\|idx\|all>` / `d\|deactivate` | (De)activate mods | Symlink `/profiles/@Name` → workshop dir. **The set of `@*` in /profiles is the active mod list.** |
| `l\|list` | List installed mods | |
| `s\|status` | Status | Running, uptime (mtime of /tmp/parameters), running params, working params, map, version, active mods. |
| `b\|backup` | Backup | `cp -a` of every mpmissions dir + /profiles into `~/backup/<timestamp>/`. |
| `w\|wipe` | Wipe | Interactive `rm -rf mpmissions/<map>/storage_1`. |
| `n\|rcon`, `login`, `install`, `add` | *dead* | Listed in the usage text / case statement but not implemented in the server container. |

Other server-side scripts:

| Script | Function |
|---|---|
| `server/bin/bercon` | Wrapper around WoozyMasta `bercon-cli`. Finds `beserver_x64_active_*.cfg` or `beserver_x64.cfg` (inside the container, or on the host via `volumes/*_profiles`), exports port/password and the GeoIP DB if present. deerisle/onlyup: any command containing `kick` first sends `kick -1`. |
| `server/bin/healthcheck` | `bercon players` (on deerisle it is disabled with `exit 0`, because a failing RCon caused restart loops). |
| `server/bin/restart [min lock delay text…]` | In-container countdown: announcement schedule >15 min every 10 min, >5 min every 5 min, >1 min every minute, then 60/30/15/10/5/4/3/2/1 s. `#lock` at the lock offset, kick every player id, wait for the delay, then `#shutdown` (**hangs DayZ** → superseded). |
| `server/bin/restart_extern` | Same logic run on the host. It talks to RCon directly, kicks 4 passes, and finishes with `systemctl --user restart container-<name>-server.service`. onlyup: a thin wrapper that derives the unit name from the Linux user name and calls `dayz_restart`. |
| `dayz_restart` (Rust binary, source lost) | Own BattlEye UDP client: auth, `version` connectivity check, keep-alive, reconnect with retries, OK/ERROR response classification, the same countdown schedule, `#lock` with retries, `players` → kick each id, up to 3 iterations then `#kick -1`, wait, then run `RESTART_COMMAND` via `sh -c`. Config via flags or env `RCON_IP/RCON_PORT/RCON_PASSWORD/RESTART_COMMAND`. **This is the behaviour to port (D8).** |
| `manager/bin/mod_update_check <moddir> [--exec cmd] [--debug]` | Python. Scans `meta.cpp` (publishedid, name), compares **file mtime** with Steam `ISteamRemoteStorage/GetPublishedFileDetails` `time_updated` (no API key), and runs `--exec` with the outdated ids appended. |
| `manager/bin/mod_update_run <ids…>` | `dzpodman exec manager dz mi <ids>` then `dzpodman exec server restart 10–12 5 2 'MOD UPDATE!'`. |
| cron (host, not in repo) | Periodically runs `mod_update_check … --exec mod_update_run`, plus scheduled maintenance restarts. |
| `files/bin/pre_start.sh` | Per-server hook executed before DayZServer starts (livonia: `traderstocks`, weather hook; hashima/deerisle: effectively disabled). |
| `files/bin/traderstocks <dir>` | Python. Refills Expansion trader JSON `Stock` entries that reached 0 (random ranges; livonia has special cases for BlackMarket, ammo and lumber). |
| `files/bin/update_dayz_weather` | Python. Open-Meteo forecast for lat/lon → generates `cfgweather.xml` + a `cfggameplay.json` weather/snowfall fragment written atomically (livonia: 60 h window, deerisle: 1 day). |
| `nominal_modifier.py` (livonia) | Authoring helper: builds a `types.xml` override with the nominal/min of regex-matched types scaled. |
| `Makefile` (deerisle) | rsync of the pristine enoch mission into the live mission (manual "reset mission from pristine"). |

## A5. The mission render pipeline (`dz start`, deerisle = superset)

Order matters; this is the behaviour to reproduce (and to golden-test, see §E).

1. `rotate`: move `*.log *.RPT *.mdmp *.ADM` from /profiles to `/profiles/logs/<timestamp>/` (other branches do this after exit in `report`).
2. Remove `dont_restart`.
3. If `mpmissions/<MAP>` does not exist: **one-time** copy from pristine `/mpmissions/<MAP>`.
4. Compute the mod lists. Every `/profiles/@*` symlink → name. If the name appears in `files/servermods` (**substring grep**) it goes into `-servermod=`, otherwise into `-mod=`.
5. **Refresh base files from pristine** (only this list, not `db/*`): `env/zombie_territories.xml, cfgrandompresets.xml, mapgrouppos.xml, mapgroupproto.xml, cfgeconomycore.xml, cfgenvironment.xml, cfgeventgroups.xml, cfgeventspawns.xml, cfggameplay.json, cfgundergroundtriggers.json, cfgeffectarea.json, cfgweather.xml, init.c`. A missing file is copied from `dayzOffline.chernarusplus` (hashima: from `main.hashima`).
6. `db/messages.xml` ← `files/messages.xml` (replace).
7. `EditorFiles`: `/profiles/custom/EditorFiles` → `mission/EditorFiles` (for DayZEditorLoader).
8. Keys: delete `serverfiles/keys/*` except `dayz.bikey`, then copy every `*.bikey` of each active mod.
9. Remove `mission/mod_*` and `mission/custom_*`.
10. For each active mod (in `find` order — **nondeterministic**; main used activation-time order):
    * `types / cfgspawnabletypes / events .xml` → `mod_<id>/`, plus a `<ce folder="mod_<id>">` entry appended to `cfgeconomycore.xml` (xmlstarlet).
    * `cfgrandompresets, mapgrouppos, mapgroupproto, cfgeventgroups, cfgeventspawns, cfgenvironment` → `xmlmerge` (gwenhywfar) of mod file + mission file, format, lint, **abort start on invalid XML**.
    * `cfggameplay.json` → `jq '.[0] * .[1]'` deep merge (mod wins).
    * `init.c.<MAP>` → `patch` (the code is buggy: it patches `init.c.<MAP>` using `init.c` as the diff, so it effectively never works as intended).
    * `cfgweather.xml` → replace.
    * `start.sh` → `bash -x` inside the mission dir (e.g. HypeTrain removes static trains via xmlstarlet).
11. Custom integrations: every dir under `/profiles/custom/<dir>` (in practice the repo's `files/custom`):
    * all `*.bikey` (follow symlinks) → keys;
    * `types, cfgspawnabletypes, events, globals` (+ `cfgeventgroups` on some branches) → `custom_<dir>/` + a `<ce folder>` entry;
    * `mapgrouppos, mapgroupproto, cfgeventgroups, cfgeventspawns, cfgenvironment` → xmlmerge into the mission;
    * `zombie_territories.xml` → xmlmerge into `env/zombie_territories.xml`;
    * `cfgundergroundtriggers.json` → concat `.Triggers`;
    * `cfgeffectarea.json` → concat `.Areas` and `.SafePositions`;
    * `cfggameplay.json` → deep merge;
    * if anything matched: copy all remaining files of the dir into `custom_<dir>/` (this is how `objectSpawnersArr` JSONs such as `custom_lost_bmps/LOSTBMPS.json` get into the mission).
12. `pre_start.sh` hook.
13. `serverDZ.cfg.save` → `/profiles/serverDZ.cfg`, with `steamQueryPort` overridden from `STEAM_PORT` env.
14. BattlEye: create `beserver_x64.cfg` (random `RConPassword`, `RestrictRCon 0`, `RConPort`) unless a cfg or `_active_` file exists.
15. `DayZServer -mod=… -servermod=… -cpuCount=N [-limitFPS=200] -config=/profiles/serverDZ.cfg -port=P -freezecheck -BEpath=/profiles/battleye -profiles=/profiles -netlog -adminlog` (main also had `-nologs`).

## A6. Configuration data model

```
files/
  serverDZ.cfg            raw server config (hostname, maxPlayers, template=<mission>, steamQueryPort, …)
  servermods              newline list of mod *names* loaded via -servermod
  messages.xml            replaces db/messages.xml
  bin/                    pre_start.sh + helper scripts
  mods/
    @<Name> -> <id>       name→id index symlinks (documentation only)
    <id>/xml.env          per-file source: local | http(s)://… | ./path/in/mod
    <id>/map.env          MAP DIR REPO MPDIR  (map mods; `default` = BI CE repo)
    <id>/*.xml|*.json     "local" integration files (some map-specific, e.g. cfgeventspawns_deerisle.xml)
    <id>/init.c.<map>     unified diff for init.c
    <id>/start.sh         post-merge script executed in the mission dir
    <alias-id> -> <id>    alias symlinks (e.g. Expansion sub-mods)
  custom/<name>/…         overlay dirs (see A5 §11)
  custom-disabled/, disabled/, editor_files_disabled/   parking lots for inactive overlays
config/containers/{server,manager}.json   ports, volumes, env for dzpodman
```

Divergence of shared data: 172 distinct files under `files/mods` across branches, of which **19
differ between servers** (mostly map-specific Expansion/weather/types tweaks). Custom overlays are
fully per-server. So the new model needs **shared integrations + per-instance overrides**.

## A7. Per-branch differences (tooling only)

| Aspect | chernarus | livonia | hashima | onlyup | deerisle |
|---|---|---|---|---|---|
| server params | `-limitFPS=200 -cpuCount=2` | `-cpuCount=10` | `-cpuCount=4 -limitFPS=200` | `-cpuCount=2 -limitFPS=200` | `-cpuCount=$(nproc)` |
| network | ports | **host** | ports | **host** (+ports) | ports |
| healthcheck | bercon players | bercon players | bercon players | bercon players | disabled |
| stop-timeout / health | 60 s / 60 s×5, 600 s | same | same | same | 30 s / 50 s×3, 800 s |
| base file list | no randompresets/zombie/triggers/effectarea | full | no zombie/triggers/effectarea | minimal (main) | full |
| custom merges | basic | full (bikey without -L) | basic, fallback = hashima | minimal | full |
| EditorFiles | – | ✓ | – | – (DayZEditorLoader as servermod) | ✓ |
| log rotation | after exit | after exit | after exit | after exit | before start |
| manager | no `mi` | no `forceupdate` | ✓ both + mod_update_* | ✓ both + mod_update_* | ✓ both |
| restart mechanism | restart_extern via dzpodman exec | same, `\|\| true` | same | `dayz_restart` | restart_extern with direct RCon, 4 kick passes |
| extras | – | weather, traderstocks, nominal_modifier, bercon-cli binary | weather (off) | python3 in image | GeoIP mount, strace in image, Makefile |
| host mounts | /usr/local/dayz | /usr/local/dayz | (LD_PRELOAD fix removed) | /usr/local/dayz | /usr/local/dayz, GeoIP |
| LogZ mount | /var/log/dayz/chernarus | …/livonia | …/hashima | …/onlyup | …/deerisle |

## A8. Quirks and bugs — do NOT carry over

1. **Mod iteration order is nondeterministic** in all branches (`find` instead of `ls -tdr`). For the game itself this is mostly irrelevant: DayZ does not
   (yet) give the `-mod=` order a reliable meaning, since script/config load order is decided by `CfgPatches` `requiredAddons` dependencies, and from a
   mod's point of view the load order is effectively chaotic anyway. It **does** matter for the *operator's own* processing: the order of `<ce folder>`
   entries (later CE definitions of the same type override earlier ones), XML/JSON merge conflicts, and reproducible renders. That order must be deterministic.
2. `servermods` matched by substring `grep` (e.g. a name contained in another name).
3. `installxml` **mutates workshop content** → breaks the mtime-based freshness check and `validate`.
4. `init.c` patch logic is inverted/broken.
5. Only a hard-coded file list (different per branch) is refreshed from pristine. **Keeping the live mission is intentional** (D13), because it holds game and mod data. But the refresh list must be explicit and configurable per instance, and the operator must know which files it manages. Today `db/types.xml` and others silently never pick up upstream fixes.
6. `xml.env` parsing via `source` (shell injection; a missing newline in one file silently merged two assignments: `msp_types.xmlTYPES=local`).
7. Global `/tmp/x` `/tmp/y` scratch files; `set -e` interplay with subshells hides failures.
8. The RCon password is generated once and never rotated; the `_active_` file is detected by glob.
9. `#shutdown` via RCon hangs the server (deerisle), and the in-container `restart` still uses it.
10. The healthcheck via RCon causes restart loops when RCon is flaky (disabled on deerisle).
11. Every server downloads its own ~3 GB server + mods; `serverfiles/keys` is shared state mutated at start.
12. `podman generate systemd` is deprecated; `dzpodman stop` deletes the unit files.
13. Server update and mod update are coupled (`update` also runs `modupdate`), so a server update is never a separate, deliberate step.
14. Dead commands (`rcon`, `add` in server), the hard-coded unit name `container-deerisle-server` in restart_extern.
15. Log rotation moves a fixed glob set (`*.log *.RPT *.mdmp *.ADM`) from the profile root, at a different time per branch: it misses mod logs in subdirectories and server-rotated files, catches files that are still needed, and archives into `profiles/logs/` inside the dir it is cleaning (§C9 replaces it).

---

# Part B — Consolidated requirements

### Functional

| ID | Requirement | Legacy source |
|---|---|---|
| FR-01 | Manage N DayZ server **instances** on one host under one service user | D3 |
| FR-02 | Steam account login with **user input**: username, password, Steam Guard (e-mail code / mobile authenticator code / app confirmation), via CLI and web; cached session; detection of expired sessions with pause of updates, notification and resume after re-login; anonymous where it works | login |
| FR-03 | Install/update several server **products** side by side (DayZ stable 223350, DayZ experimental 1042420, future products), build-id detection, `validate`, force; each instance selects a product | install/update/forceupdate |
| FR-04 | Workshop mods (app 221100): add/remove/update all/update selected, name/metadata from `meta.cpp`, size, URL | add/remove/modupdate/mi |
| FR-04a | **Local servermods** without the Steam Workshop (site repo dir, host dir, or URL archive + sha256), incl. our own `dzo-admin`; content-hash generations, changes rolled out under the instance update policy | (new) |
| FR-05 | Mod freshness check against the Steam Web API (`GetPublishedFileDetails`, no key) | mod_update_check |
| FR-06 | Per-instance mod list (deterministic order, used for dzo's merge/CE precedence, not as a game load order), split into client mods and server-side mods (explicit flag) | activate, servermods |
| FR-06a | Mod dependency validation from `CfgPatches` `requiredAddons` (block start on missing dependencies), dependency graph for diagnostics, optional dependency-sorted `-mod=` | load order |
| FR-07 | **Per-instance pristine mission** (`servermpmissions`) from git repos (BI CE repo, map repos, own forks; repo + ref + subdir), install/update; one-time initialisation of the live mission from it | map.sh/map.env |
| FR-07a | **Live mission is never wiped**. Updates happen in place and only touch operator-managed files (pristine-managed + generated, tracked in a manifest). Files from elsewhere are never modified or deleted. Changed managed files: warn, backup, overwrite. New files in the mission repo are picked up. Backup/snapshot before every change; an explicit, confirmed "re-initialise" command is the only destructive path | D13, Q11 |
| FR-08 | Mod integrations: per-file source (local / URL / path-in-mod), normalisation of broken XML, validation | xml.sh/installxml |
| FR-09 | Mission render: CE folder registration, XML merge for mapgroup*/events*/env/randompresets/zombie territories, JSON merge strategies (deep merge, array concat for triggers/effect areas), replace (weather, messages), init.c patching, EditorFiles, extra files, scripts | A5 |
| FR-09a | CE data from mods/overlays only via generated `<ce folder>` entries in `cfgeconomycore.xml`; cfggameplay list keys (object spawners, gear presets, restricted areas) are appended, not replaced | Q10 |
| FR-10 | Per-instance custom overlays (same capabilities as mods, plus globals.xml, bikeys) | files/custom |
| FR-11 | Keys: `dayz.bikey` + active mod keys + custom keys, per instance | A5 §8 |
| FR-12 | serverDZ.cfg managed per instance, with operator-enforced keys (ports, template, instanceId) and a diff before adoption | config |
| FR-13 | BattlEye config generation, RCon password management | loadconfig |
| FR-14 | Start/stop/restart/kill, auto-restart on crash, "stop and stay stopped" | start/stop/force/norestart |
| FR-15 | **Graceful restart**: announcements (schedule A4), lock, kick with retries + `#kick -1`, post-kick delay, then restart via systemd; custom message prefix (e.g. "MOD UPDATE!") | dayz_restart |
| FR-16 | Scheduled maintenance restarts per instance (typically every 3–4 h, times vary per server) | cron |
| FR-16a | Scheduled in-game messages via RCon (later phase; `messages.xml` covers the basics) | cron |
| FR-17 | Automatic **mod** update pipeline: detect (hourly by default) → download → restart affected instances gracefully, per-instance policy. **Server builds are only detected and reported, never downloaded or applied automatically** | cron + mod_update_run |
| FR-17a | **Forced mod refresh**: discard the cached copy and steamcmd's state for one, several or all mods and download them again, even if Steam reports no change (broken downloads, a bugged Steam/steamcmd cache); then apply like a normal mod update | forceupdate / mi |
| FR-17b | **Manual server upgrade** per instance: download the new product build on request, then switch selected instances with an explicit command that can include a storage wipe and mod/config changes, after a snapshot | install/update |
| FR-18 | **Working container health checks** (startup + liveness) with restart-on-failure that does not flap | healthcheck |
| FR-18a | **Icinga** checks (Nagios plugin API) for each instance and for global state (updates, steam session, disk) | – |
| FR-18b | **Discord** notifications (webhook) for updates, restarts, crashes, health changes, failed renders/jobs | – |
| FR-19 | Status: installed build/version, running, uptime, effective command line, active mods, players | status |
| FR-20 | Logs: journald for console; rotation of profile logs by a **regex list** (all but the newest match of each rule are moved into a log archive outside the profile dir, with retention); crash summary (tail of error/script/RPT); LogZ output dir per instance | report/rotate |
| FR-21 | Backup, restore, wipe storage with confirmation (backups via FR-21a) | backup/wipe |
| FR-21a | **Backups before changes** (server update, mod updates incl. automatic ones, mission/config changes, destructive operations, optionally every start or on a schedule) as btrfs snapshots; configurable retention (keep N, per trigger, max age, free space, pinning) with guaranteed cleanup; full and partial restore | D31 |
| FR-22 | **Built-in** BattlEye RCon client (no external tools): console/commands (players, say, kick, lock/unlock, bans), event stream (connect/GUID/chat), GeoIP enrichment | bercon |
| FR-23 | Pre-start / post-render **hooks** per instance (traderstocks, live weather, …) | pre_start.sh |
| FR-24 | Extra per-instance container options: env (e.g. LD_PRELOAD), extra ro mounts (GeoIP, fix libs), CPU pinning/limits, params | D9 |
| FR-25 | Development mode: render only / container idle for debugging | DEVELOPMENT/DONT_START |
| FR-25a | **Boot test**: boot a real DayZ server headless on a render result (fresh world, test ports), check that it becomes ready and that the logs show no script, CE or mission errors, and report per check; **development only, never on a host with live servers** (§C22) | – |
| FR-26 | **No data migration.** A config converter produces an **example site config** from the legacy git repositories (branches only); servers start fresh (§C21) | – |
| FR-27 | Web admin map: live player/vehicle positions, **extensible marker layers from other mods** (§C16); direct messages, teleport, spawn items for players, repair/delete vehicles (via `dzo-admin`), with roles and an audit log | Q17 |
| FR-28 | Integration with the existing Prometheus + Loki (gigapipe) stack: `/metrics`, collectable logs, optional OTLP traces | Q16 |
| FR-29 | Map tile generation from the map's data PBO: **vanilla maps downloaded as a single file from the client depot** (`sDepotDownloadFileFilter`), with **manual upload / configured path as fallback**, and a **configured path required for modded maps** (no PBO scanning), **cached per source hash (extracted once)**, auto-rebuilt on map updates and **rebuildable on request from the current game files**, served and exportable as XYZ PNG tiles | Q18 |
| FR-29a | **Active in-game events** (CE events such as heli crashes, convoys, contaminated areas, plus modded events) detected by `dzo-admin` and shown on the map with location, type and lifetime | map |
| FR-30 | Player list per server + global, player pages, tracking (first/last login, playtime per server, name history, sessions) | web admin |
| FR-31 | Steam profile + VAC/game ban info (Steam Web API, cached), optional join policies | web admin |
| FR-32 | Country from IP (GeoIP), stored per session | web admin |
| FR-33 | Kick, and ban by Steam ID/GUID/IP/country/ASN, temporary or permanent, for one/several/all servers, enforced on connect + native ban lists | web admin |
| FR-34 | Remote restarts (now/graceful), one-off restart timers, restart-when-empty, cancel/postpone | web admin |
| FR-35 | Item spawning with the full type list (from `dzo-admin` + parsed CE XML), kits/presets | web admin |
| FR-36 | Scheduled and one-off broadcast messages, direct messages | web admin |
| FR-37 | Per-server RBAC, audit log, API tokens, privacy (IP retention, export/erasure) | web admin |
| FR-37a | **Optional MetricZ/LogZ data** (D38): MetricZ metrics re-exported on `dzo-exporter` `/metrics` without a separate exporter; LogZ data queried from **Loki (required for LogZ features)** for kill feed, combat stats and session details; dzo sets the keys it needs in both mods' configs (file sink, telemetry off) | (new) |
| FR-37b | **Optional cheat-detection tracking** (D38): run logz-analyzer periodically, store its findings **in the DB, long-term, per player (SteamID64 / BE GUID) and searchable** (full text, player, server, detector, time, position), suppress findings explained by dzo's own admin actions, review queue (confirmed / false positive), threshold alerts, no automatic sanctions | cheat-detector |
| FR-38 | **Web authentication with MFA**: password + TOTP by default (forced enrolment), recovery codes, optional WebAuthn/passkeys, optional OIDC with an MFA requirement, session management, step-up for sensitive actions, lockout protection, CLI user bootstrap/reset (§C12) | – |

### Non-functional

* NFR-01 Rootless podman, quadlets only, `loginctl enable-linger` for the service user.
* NFR-02 Deterministic, idempotent rendering; dry-run and diff against the live mission; per-file atomic writes; never delete files the operator does not own.
* NFR-03 Shared downloads with **immutable generations** so an update or a forced re-download never changes files under a running server.
* NFR-04 No Node.js. Single-binary deployment, **packaged as a Debian package**.
* NFR-05 Library-first architecture: CLI, timers and the later web UI/API share one core.
* NFR-06 Secrets (Steam session, RCon passwords, web users) are never in git and have 0600 permissions.
* NFR-07 Everything observable: structured logs, job history, exit codes usable by systemd.
* NFR-08 Testable: golden tests against the legacy renderer output, unit tests for merges and RCon, and **boot tests with a real DayZ server** on developer machines for every render change that is meant to reach a server (§C22).
* NFR-09 Licence AGPL-3.0-or-later, copyright Bernd Zeimetz, with an automated dependency licence check (`go-licenses`) and REUSE compliance of our own files (`reuse lint`): every new file gets SPDX headers when it is created.
* NFR-10 No cron. Only systemd timers or daemon-internal scheduling (D19).
* NFR-11 **≥ 85 % test coverage**, enforced in CI on GitHub and GitLab (D20, D21).
* NFR-12 Remote-monitorable: Prometheus metrics + a machine-readable status endpoint; no local agent required (D22).
* NFR-13 **Lazy engineering (D35):** existing libraries and platform features first, no over-engineering, implement only what is needed; security, data safety and tests are never cut.
* NFR-14 **Documentation (D36):** a Sphinx website in `docs/`, kept in step with the code (behaviour changes update the docs in the same change), served by the web UI under `/docs/`.

---

# Part C — Design

## C0. Platform baseline: Debian trixie (D29)

Everything targets **Debian 13 "trixie"** (stable): host, container base images, the `.deb`. Versions shipped in trixie (checked 2026-09-26):

| Component | trixie version | Consequence |
|---|---|---|
| podman | **5.4.2** | Quadlet/podman features are limited to what 5.4 supports (see below). Newer features only behind runtime version detection. |
| systemd | 257 | `systemd-creds`, transient timers (`systemd-run --on-calendar`), `sysusers.d`/`tmpfiles.d` all available |
| passt/pasta | 2025-05 snapshot | default rootless network backend for the `publish` network mode |
| python3 | 3.13 (+ `python3-requests` 2.32) | only for hooks/legacy helpers; **packaged modules only, no pip/venv** |
| steamcmd | `steamcmd` (non-free, i386) | used inside the `dzo-steamcmd` image (as in legacy) |
| geoipupdate | 7.1 (contrib) | GeoIP DB updates via its own systemd timer |
| libjs-htmx / libjs-leaflet | 2.0.4 / 1.7.1 | web UI assets are taken **from the Debian packages** (§C12) |
| postgresql | 17 | optional DB backend (§C18) |
| monitoring-plugins-basic, btrfs-progs, git, uidmap | packaged | Recommends/Depends of the `.deb` |
| linux kernel | 6.12 (trixie stock) | **pinned in D29.** Unprivileged btrfs subvolume creation and **snapshots of own subvolumes** are allowed on 6.12 (the snapshot ioctl only checks `inode_owner_or_capable` on the source; verified in the v6.12 and v4.19 sources and on 7.1). `SNAP_DESTROY` needs `user_subvol_rm_allowed`. If spike S7 on the target kernel shows a gap, a **trixie-backports kernel is an accepted requirement** (no userspace workaround) |
| btrfs | – | **required** filesystem for the configured `paths.instances` + `paths.snapshots` (same filesystem; any location, e.g. `/srv/dayz`; §C2, §C20). Recommended mount option `user_subvol_rm_allowed` |
| golang-go | 1.24 (1.26 in backports) | **not used for building** (see below) |

**Rules:**
* **Runtime dependencies come from Debian packages only** (trixie main/contrib/non-free, trixie-backports only when unavoidable,
  documented per package). Helper tools and hooks may use Python, but only with Debian-packaged modules (e.g. `python3-requests`), never pip.
  External binaries are not downloaded at build or runtime (unlike legacy `bercon-cli`/`bercon` via curl). Functionality comes from dzo itself or from Debian packages.
* **Go builds use the latest upstream Go toolchain** (currently **go1.27.1**, from go.dev), not trixie's golang-go. `go.mod` declares
  `go 1.27` + a pinned `toolchain` line, and CI/builders install the official tarball with a verified checksum (or use the official `golang:1.27` image
  for pure Go jobs). Renovate bumps the toolchain. The resulting binary is **static** (`CGO_ENABLED=0`) and therefore independent of trixie's libraries.
  Go module dependencies are vendored (`go mod vendor`); they are compiled into the binary and are not "tools on the host".
* **Container images** are based on `debian:trixie-slim` and install only Debian packages.
* **Podman 5.4.2 feature baseline for quadlets.** Keys used by the plan (`HealthStartupCmd`/`Interval`/`Retries`/`Success`, `HealthOnFailure`,
  `Notify=healthy`, `.build` units, `Volume=`, `Network=host`, `[Service]` passthrough) are all expected to exist in 5.4. This is **verified on a trixie VM
  in spike S6**, and anything missing gets a fallback (R7). Features newer than 5.4 (e.g. quadlet `ReloadCmd`/`ReloadSignal`, the `podman quadlet` management
  CLI) are **not used**. dzo generates quadlet files itself and does not rely on them. A startup check refuses to run on podman < 5.4 with a clear message.
* CI integration and packaging tests run in **trixie** containers/VMs, so the podman and systemd behaviour matches production.

## C1. Language choice: Go (with Python remaining possible for hooks)

The user suggested Python and left Go open. **Recommendation: Go.**

| Criterion | Go | Python |
|---|---|---|
| Deployment on host | single static binary, no venv, trivially run from quadlets/timers and inside containers (healthcheck) | venv/pipx on host; the interpreter must also be in the runtime image for health |
| DayZ ecosystem | own built-in BattlEye RCon implementation (§C8); WoozyMasta libs: `a2s` (server query incl. DayZ A3SB), `dzce` (typed CE models + economycore merge, v0.x), `steam/filedetails` (workshop API), `dzid` (Steam64→BE GUID); ecosystem around LogZ/MetricZ (already used as servermods), `metricz-exporter`, `dayz-exporter` | few maintained libs; RCon client and CE parsing would be self-written |
| systemd / podman | `go-systemd/dbus`, podman CLI (or bindings) | `dbus-next` / subprocess |
| Web (later) | stdlib `net/http` + `html/template` + htmx, assets embedded in the binary | FastAPI + Jinja + htmx |
| XML fidelity | `encoding/xml` is weak at round-tripping comments/order → use `beevik/etree` for merge, `dzce` for typed validation | `lxml` excellent |

Consequences:

* The web layer follows D11 in Go form: server-rendered templates + htmx, no Node.
* Hooks are plain executables with a defined environment. Existing Python helpers (`update_dayz_weather`, `traderstocks`) keep working as hooks (Debian `python3` + `python3-requests`, no pip) and can be ported to built-in Go plugins later.
* `dzce` is pre-1.0: wrap it behind our own interfaces and pin versions. Fall back to an etree-based merge where it does not fit (e.g. `xmlmerge` semantics, see §E spike S3).
* Licence (D15): the project is **AGPL-3.0-or-later**. Planned dependencies and their licences: `a2s`, `dzce`, `dzid`, `steam`, `pbo`, `paa`, `lzo`, `lzss`, `rvmat` (WoozyMasta, all MIT), `creack/pty` (MIT), `beevik/etree` (BSD-2), `go-systemd` (Apache-2.0), `cobra` (Apache-2.0), `modernc.org/sqlite` (BSD-3), `pgx` (MIT), `goose` (MIT), `clickhouse-go` (Apache-2.0), `maxminddb-golang` (ISC); web assets from Debian: htmx (0BSD/BSD-2), Leaflet (BSD-2). All are compatible with AGPLv3. CI runs `go-licenses check` with an allow-list, so an incompatible dependency fails the build. If one is ever unavoidable, fall back to GPL-3.0, and to MIT as the last resort.

Working name for the binary: **`dzo`** (dayz-server-operator). Cheap to rename.

## C2. Host layout

Service user **`dayz`** (created by the Debian package, see §C14), lingering enabled, subuid/subgid ranges
assigned. Home = `/var/lib/dzo` (package default).

**All data locations are configurable** in `/etc/dzo/config.yaml`. Nothing in dzo hard-codes `/var/lib/dzo`, and the defaults are only defaults:

```yaml
paths:
  data: /var/lib/dzo              # e.g. /srv/dayz
  instances: ${data}/instances    # btrfs required (§C20), may be a different mount than data
  snapshots: ${data}/snapshots    # must be on the SAME btrfs filesystem as `instances` (snapshots cannot cross filesystems)
  cache: ${data}/cache            # download generations (reflink-friendly if on btrfs)
  logs: ${data}/logs              # archive of rotated profile logs (§C9), outside the instance subvolumes
  secrets: ${data}/secrets
  db: ${data}/db                  # SQLite file(s)
```

* `dzo setup` creates the directories with the right ownership (`dayz`, 0750/0700) and validates the constraints (btrfs for `instances`,
  same filesystem for `snapshots`, and enough free space). The same validation runs at every daemon/CLI start.
* The service user's home (quadlets in `~/.config/containers/systemd/`, the rootless podman storage in `~/.local/share/containers/`) stays
  at the package default. Rootless podman's image storage can be moved via the user's `storage.conf` if wanted (`dzo setup --podman-storage <dir>`).
  Generated quadlets always reference the configured absolute paths.
* Relocating an existing installation: `dzo setup relocate --to <dir>` (stop instances → move data → create new subvolumes on the target and
  copy into them (`cp -a --reflink=auto`, instant within the same btrfs filesystem) → update config → regenerate quadlets → start).

Default layout (with `paths.data = /var/lib/dzo`):

```
/etc/dzo/config.yaml                      operator config: paths, site repo URL/branch, products, notification targets
/var/lib/dzo/                             ($HOME of user dayz)
  .config/containers/systemd/             generated quadlets (owned by dzo, marked "generated")
  site/                                   checkout of the site config repo (remote configurable, D16)
  secrets/                                steam session (steamcmd home), rcon passwords, webhook URLs, web users (0600)
  cache/
    products/<product>/<buildid>/         immutable server build generations per product (reflink on btrfs)
    products/<product>/current -> <buildid>
    workshop/<appid>/<modid>/<time_updated>/  immutable mod generations
    local/<name>/<content-hash>/          immutable generations of local servermods (§C7)
    steamcmd/<product>/                   steamcmd working install dirs (mutable, never mounted into servers)
    git/<repo-hash>/                      bare mirrors of mission repos (shared fetch cache)
  instances/<name>/                       btrfs SUBVOLUME per instance (required, §C20)
    servermpmissions/                     PRISTINE mission(s) for this instance (git worktree of its mission repo @ ref)
    mpmissions/                           LIVE missions, persistent, never wiped (D13), mounted rw
      <map>/                              incl. storage_1/, mod data, operator-managed files
      .dzo-manifest.json                  files the operator owns + their last written hashes (lives outside <map>)
    profiles/                             DayZ -profiles dir (logs, BattlEye, mod configs e.g. VPPAdminTools)
    runtime/                              generated per start: keys/, serverDZ.cfg, beserver cfg, args (mounted ro)
    filehistory/                          copies of changed/drifted managed mission files (last N, §C6)
  snapshots/<name>/<ts>-<reason>/         read-only btrfs snapshots of the instance subvolume (§C20)
  snapshots/_db/                          database backups
  jobs/                                   job logs + state (update runs, restarts)
  logs/<name>/<YYYY-MM-DD>/…              archived profile logs, relative paths kept, gzip (§C9)
/run/dzo/status/<name>.json               local status snapshot (fallback/debug; remote monitoring uses dzo-exporter, §C9)
/var/log/dayz/<name>/                     optional LogZ bind (as today; setting it up needs root, default is the profile dir, §C23)
```

The host filesystem is btrfs (to be confirmed): `cp --reflink=auto` makes generations nearly free.
On other filesystems we fall back to hardlink trees for unchanged files.

## C3. The site config repo

```
site/
  site.yaml                         defaults (image tag, update policy, restart schedule, announce texts)
  integrations/
    mods/<modid>/integration.yaml   replaces xml.env/map.env (typed, no shell)
    mods/<modid>/files/…            local integration files (+ map-specific variants)
    mods/<modid>/hooks/…            scripts (replaces start.sh)
    maps/<name>.yaml                reusable mission source presets (replaces map.env; `vanilla` = BI CE repo)
  overlays/<name>/…                 reusable custom overlays (login-times, stamina, …)
  localmods/<name>/                 local servermods kept in git: addons/*.pbo (default source of {local: <name>}, §C7)
  instances/<name>/
    instance.yaml
    serverDZ.cfg
    messages.xml
    overlays/<name>/…               instance-only overlays (same format as files/custom)
    integrations/<modid>/…          per-instance override of a shared integration
    hooks/…
```

`integration.yaml` (example):

```yaml
mod: 2291785308            # DayZ-Expansion-Core
name: DayZExpansionCore
files:
  types.xml:          {source: local, path: files/types.xml}
  cfgspawnabletypes.xml: {source: url, url: https://…, sha256: …}   # pinned optional
  cfgeventspawns.xml: {source: local, path: files/cfgeventspawns.xml, maps: [dayzOffline.chernarusplus]}
  events.xml:         {source: mod,   path: ./info/events.xml}
normalize: [eventposdef-root, wrap-root, xml-decl]
hooks:
  post_merge: [hooks/remove-static-trains.sh]
aliases: [2291785546]
```

`instance.yaml` (example):

```yaml
name: deerisle
product: dayz-stable           # dayz-stable | dayz-experimental | … (see §C7)
map: empty.deerisle            # mission dir name; must match serverDZ.cfg template (enforced)
mission_source:                # pristine servermpmissions for THIS instance
  git: https://github.com/…/DeerIsle-mission.git   # or: preset: deerisle (site/integrations/maps/)
  ref: main                    # branch/tag/commit; updates are explicit (`dzo mission update`)
  path: "empty.deerisle"       # dir (glob) inside the repo, copied to servermpmissions/
fallback_mission: dayzOffline.chernarusplus   # source for base files the map lacks
mission:                       # see §C6: all pristine files are managed, storage_* is always excluded
  unmanaged: ["expansion/**"]  # globs that stay foreign even if the mission repo ships them
  drift: warn-backup-overwrite # Q11 default
ports: {game: 8302, rcon: 8303, query: 8716}
network: host                  # or: publish
params: {cpuCount: auto, limitFPS: null, extra: ["-netlog", "-adminlog"]}
mods:                          # order = dzo merge/CE precedence (later wins); DayZ itself does not rely on -mod= order
  - {id: 1559212036}                         # CF
  - {id: 1828439124, server: true}           # VPPAdminTools as -servermod
  - {id: 1602372402}
  - {local: dzo-admin, server: true}         # local servermod, not from the workshop (§C7, D37)
overlays: [login-times, stamina, loadout, idle_settings]
updates:
  policy: auto                 # auto | notify | manual
  check_interval: 1h
  restart_announce: {minutes: 12, lock: 5, delay: 2, text: "MOD UPDATE!"}
restarts:
  schedule: ["*-*-* 00/4:00"]  # systemd OnCalendar list, free per instance (typically every 3–4 h)
  announce: {minutes: 30, lock: 3, delay: 3}
health: {startup_timeout: 45m, interval: 60s, retries: 5}   # startup_timeout → HealthStartupRetries; default = 2× the slowest measured load (S1)
restart_limit: {burst: 5, interval: 30min, cooldown: null}
notify: {discord: [default, deerisle-admins]}   # omitted = default webhook; [] = none
container:
  env: {}                      # e.g. LD_PRELOAD (D9)
  mounts: ["/var/lib/GeoIP:/var/lib/GeoIP:ro"]
  logz_dir: /var/log/dayz/deerisle
  cpus: null
  memory: null                 # e.g. 24G → quadlet Memory=
hooks:
  pre_start: ["hooks/traderstocks.sh"]
```

The site repo is configured in `/etc/dzo/config.yaml` by **remote URL** + branch (+ SSH deploy key
in `secrets/`). `dzo site pull` updates it (also available as a timer). When configured to, the operator commits
changes it makes itself (e.g. `dzo mod add`, web edits) with the acting user as author, and optionally
pushes them. That gives git a paper trail, which was the stated goal of legacy `dz c`. Local
uncommitted edits block automatic pulls, and the operator reports them instead of overwriting.

## C4. Container images

1. **`dzo-runtime`** (game server): `debian:trixie-slim` + CA certs + the libs `DayZServer` needs.
   No shell tooling needed at runtime. Built from a `.build` quadlet (the `Containerfile` ships in the Debian
   package under `/usr/share/dzo/images/`) and rebuilt on a timer (weekly) for security updates. Replaces
   `dzpodman build/cleanup`. The health check binary is **not** baked into the image: the static
   `/usr/bin/dzo` from the package is bind-mounted read-only (`/usr/local/bin/dzo`), so the check always
   matches the installed operator version.
2. **`dzo-steamcmd`**: `debian:trixie-slim` + Debian's `steamcmd` package (non-free, i386 multiarch). The legacy `steamservice.so` blob
   copied from the repo is dropped, because the warning it silenced is cosmetic and only packaged software goes into images.
   Debian's package only ships the 2018 bootstrap: its `/usr/games/steamcmd` wrapper copies it to `$XDG_DATA_HOME/Steam/steamcmd` and steamcmd **self-updates
   there**, i.e. inside the persistent steamcmd home (`secrets/steamcmd-home`), so the update happens once and persists. This Valve self-update is the
   one accepted exception to "no downloaded binaries" (it is how steamcmd works, and it runs only inside the steamcmd container). Used
   only by short-lived `podman run` jobs started by the operator. Its mounts are the steamcmd home (secrets) and
   `cache/steamcmd/<product>`. **Never** mounts live instance data.

## C5. Quadlets (generated per instance)

`dzo-<name>.container` (sketch; exact keys verified in spike S6):

```ini
[Unit]
Description=DayZ server <name> (dzo)
# no network-online deps: quadlet adds podman-user-wait-network-online.service for rootless units itself
StartLimitIntervalSec=30min                          # crash/render-loop brake (F3), from instance.yaml restart_limit
StartLimitBurst=5
[Container]
Image=localhost/dzo-runtime:latest
ContainerName=dzo-<name>
Network=host
WorkingDir=/dayz
Exec=<args from runtime/args>                        # rendered at quadlet generation time
Volume=/usr/bin/dzo:/usr/local/bin/dzo:ro
Volume=<cache>/products/<product>/<buildid>:/dayz:ro
Volume=<inst>/runtime/keys:/dayz/keys:ro
Volume=<inst>/mpmissions:/dayz/mpmissions:rw        # LIVE mission, persistent (D13)
Volume=<cache>/workshop/221100/<id>/<gen>:/dayz/@<Name>:ro   # one per mod
Volume=<cache>/local/<name>/<hash>:/dayz/@<name>:ro          # one per local servermod
Volume=<inst>/profiles:/profiles:rw
Volume=<inst>/runtime/serverDZ.cfg:/profiles/serverDZ.cfg:ro
# --- health (D18): startup probe until the server answers, then liveness probe
HealthStartupCmd=/usr/local/bin/dzo health startup --query 127.0.0.1:<query>
HealthStartupInterval=30s
HealthStartupRetries=<ceil(health.startup_timeout / 30s)>   # generated from instance.yaml, default 45m → 90
HealthStartupSuccess=1
HealthCmd=/usr/local/bin/dzo health live --query 127.0.0.1:<query> --pid-name DayZServer
HealthInterval=60s
HealthRetries=5
HealthTimeout=10s                                    # A2S incl. challenge handshake = 2 round trips
HealthOnFailure=kill                                 # systemd Restart= restarts → one place for restart policy
Notify=healthy                                       # unit becomes "active" only once the server answers
StopTimeout=120
Memory=<container.memory>                            # optional hard limit (OOM → kill → counted by the start limit)
[Service]
ExecStartPre=/usr/bin/dzo render <name> --apply      # in-place mission update on every (re)start, like legacy
Restart=always
RestartSec=15
TimeoutStartSec=<startup_timeout + 15min>
[Install]
WantedBy=default.target
```

Other units (all generated by `dzo`). **No cron anywhere.**

**Single scheduling rule (no double-firing):** every scheduled action is a **systemd timer**. Static schedules from the site repo are
generated `.timer` units, and schedules created in the UI/API (one-off restarts, broadcasts) are rows in the DB that `dzo` **materialises as transient
timers** (`systemd-run --user --on-calendar=… --unit dzo-sched-<id>`) and re-materialises idempotently on every `dzo` start and on changes.
The daemon (`dzo serve`) **only creates, changes and cancels timers. It never fires actions itself.** A timer runs `dzo schedule run <id>`, which
takes a per-schedule lock and records the run, so a daemon restart, a double materialisation or a missed tick can never execute an action twice.

| Unit | Purpose |
|---|---|
| `dzo-runtime.build`, `dzo-steamcmd.build` | image builds |
| `dzo-image-refresh.timer/.service` | weekly `podman build --pull` + prune of old images |
| `dzo-update-check.timer/.service` | runs at the **smallest** `updates.check_interval` of all instances (default hourly). `dzo update check --apply` gates each instance by its own interval, last-check time and update window (§C7) |
| `dzo-restart-<name>.timer/.service` | maintenance restarts per instance, `OnCalendar=` from `restarts.schedule` (list, free per instance), `Persistent=false`, runs `dzo restart <name> --graceful` |
| `dzo-exporter.service` | long-running: `/metrics` (Prometheus) + `/status` (JSON for remote Icinga), §C9 |
| `dzo-status.timer/.service` | every minute: `dzo status --write /run/dzo/status/` (local snapshot + watchdog) |
| `dzo-site-pull.timer/.service` | optional periodic `dzo site pull` |
| `dzo-logs.timer/.service` | hourly: profile log rotation for all instances + archive retention (§C9) |
| `dzo-backup-prune.timer/.service` | daily: backup retention/cleanup, interrupted snapshots, orphan report, DB backup (§C20) |
| `dzo-backup-<name>.timer/.service` | optional periodic snapshots per instance (`backup.schedule`) |
| `dzo-web.service` (phase 3) | `dzo serve` (web UI + API), bound to localhost, behind a reverse proxy |

Design notes:

* **No restart storms (F3).** Three layers stop a broken config or a crash loop from flapping forever:
  1. **Pre-flight render:** every planned restart (update, config change, scheduled, manual) first runs `dzo render --dry-run` *while the
     server is still running*. If it fails, the restart is **not** performed, the server keeps running on the old state, and an alert is sent.
     So a bad site-repo change or integration never takes a server down. (Real boot tests are a development tool and do not run on the host, §C22.)
  2. **Failure gate for unplanned starts:** if `ExecStartPre` render fails anyway (crash restart after a bad change), dzo records the instance as
     `failed-render` with the input hash. Further start attempts with **unchanged inputs** fail immediately without work, and the unit
     reaches `failed` via the start limit. A change of inputs (a fix pushed to the site repo) or `dzo instance ack-failure <name>`
     (= `systemctl --user reset-failed` + clear the gate) re-enables starts.
  3. **systemd start limit** (`StartLimitIntervalSec`/`StartLimitBurst`, configurable as `restart_limit` per instance, default 5 starts per 30 min):
     covers crash loops and repeated health kills as well. The unit lands in `failed` and stays down. Icinga goes CRITICAL and Discord reports
     "crash loop, server stopped". An optional `restart_limit.cooldown` (e.g. 1 h) lets a timer retry once after the cool-down,
     **only for crash/health loops**, never for render failures with unchanged inputs.
* **Every (re)start runs `dzo render --apply`** in `ExecStartPre` (equals legacy `dz start` semantics: custom
  changes apply on restart). A render failure aborts the start *before* any file in the live mission is touched,
  because the render is computed and validated in staging first (§C6).
* `Restart=always` replaces the legacy in-container loop. "Stop and stay stopped" = `systemctl --user stop`.
* **Composed server root.** The shared build is mounted read-only, and per-instance `keys/`,
  `mpmissions/` and `@Mod` dirs are nested mounts on top of it. This removes the shared-mutable
  `serverfiles/keys` problem (A8.11). Whether DayZServer needs to write inside its install dir
  must be verified (spike S1; legacy had `strace` in the image for exactly this).
* Mods are mounted as `/dayz/@<Name>` so the `-mod=@A;@B` paths stay relative and short
  (the command-line length limit matters with 40+ mods).
* Port conflicts between instances are validated by `dzo` before generating the quadlets (host network).

## C6. Mission handling and the render pipeline (replaces A5)

### What the legacy start script really does (basis for Q10)

The recent `dz start` (deerisle/livonia) follows the BI "Central Economy mission files modding" model:

* **CE data files are never edited.** `types`, `spawnabletypes`, `events` and `globals` from mods and overlays are placed in
  their own folders (`mod_<id>/`, `custom_<dir>/`). The operator then **generates** matching
  `<ce folder="…"><file name="…" type="…"/></ce>` entries and inserts them into the mission's `cfgeconomycore.xml`.
  The `db/*` files of the mission stay as delivered.
* **Merge targets are refreshed from pristine before merging:** `cfgeconomycore.xml` (then the `<ce>` entries are added),
  `mapgrouppos.xml`, `mapgroupproto.xml`, `cfgeventgroups.xml`, `cfgeventspawns.xml`, `cfgenvironment.xml`,
  `cfgrandompresets.xml`, `env/zombie_territories.xml`, `cfggameplay.json`, `cfgundergroundtriggers.json`,
  `cfgeffectarea.json`, `cfgweather.xml`, `init.c`, `db/messages.xml`. That is why the refresh list exists: it is
  exactly the set of files that get merged into, so every start begins from a clean base, and removing a mod removes its contribution.
* **Referenced side files:** overlays ship extra files (object spawner JSONs, spawn gear presets, …) copied to
  `custom_<dir>/`, and a `cfggameplay.json` fragment referencing them by path (`custom_loadout/vanilla_loadout.json`).
* Keys, EditorFiles and `start.sh` scripts, as listed in A5.

Findings that the new implementation fixes:

* `jq '.[0] * .[1]'` **replaces arrays**. Two overlays that both set `WorldsData.objectSpawnersArr` or
  `PlayerData.spawnGearPresetFiles` overwrite each other. (chernarus works around this with one central
  `objectSpawnersArr` overlay listing every other overlay's files.)
* Some branches register `cfgeventgroups.xml` as a CE folder file with type `eventgroupdef`. That is not a CE folder
  type (it is a merge target), so it has no effect.
* The refresh list is hard-coded and differs per branch.

### Model (D13)

```
instances/<name>/servermpmissions/<map>/   PRISTINE, from the instance's mission git repo (replaceable any time)
instances/<name>/mpmissions/<map>/         LIVE, owned by the game + mods; NEVER wiped or recreated
instances/<name>/mpmissions/.dzo-manifest.json   every live path dzo owns: origin, source hash, hash last written
```

**File classes in the live mission:**

| Class | Examples | Owner | What dzo does |
|---|---|---|---|
| **pristine-managed** | every file that exists in pristine: `db/*.xml`, `cfg*.xml/json`, `init.c`, `env/*`, map-specific files | dzo | Kept identical to pristine. A **merge target** is rewritten as pristine + contributions. A live file that differs from what dzo last wrote → **warn, backup, overwrite** (Q11). |
| **generated** | `mod_<id>/…`, `custom_<dir>/…`, `EditorFiles/…` | dzo | Written from integrations/overlays. Obsolete generated files are removed only if listed in the manifest *and* unchanged since dzo wrote them (otherwise warn + backup + remove). |
| **foreign** | `storage_*/…`, mod data (e.g. `expansion/…`), files created by the game, admins or tools | not dzo | **Never modified, never deleted.** Not in the manifest. |

Rules:

* **Initialisation (once):** if `mpmissions/<map>` does not exist, copy pristine (+ fallback-mission fill for missing base
  files), and record everything copied as pristine-managed. This is the only time the tree is created.
* **New files in pristine** (the mission repo gained a file): absent in live → copied and added to the manifest as
  pristine-managed. Already present in live (created by something else) → treated as a managed file from now on:
  warn, backup, overwrite, add to the manifest. A per-instance `mission.unmanaged` glob list lets specific paths
  stay foreign instead (e.g. a mod that ships its config in the mission repo but rewrites it at runtime).
* **New integration/overlay files** in the site repo simply become new generated files on the next render.
* **Files removed from pristine** are *not* deleted from live. They are dropped from the manifest (become foreign) and reported.
  Deleting them is an explicit `dzo mission prune <name>` (after a `destructive` snapshot).
* **Never delete anything that is not in the manifest.** Generated dirs are not `rm -rf`ed (unlike legacy). Only
  manifest-listed files inside them are removed; unknown files found there are reported and left alone.
* `storage_*` and the per-instance `mission.unmanaged` globs are hard-excluded from every write, even when pristine contains matching paths.
* **Drift handling (Q11):** before overwriting a managed file whose live hash differs from the last written hash, copy it to
  `filehistory/<ts>/drift/<path>`, log a warning with a diff summary, and notify (Discord) once per file and change.
* **Safety net:** before every apply, all managed files that will change are copied to `filehistory/<ts>/` (only these files; the last
  `mission.keep_file_history` sets are kept, older ones are deleted). `dzo mission rollback <name> [<ts>]` restores them. Full backups are btrfs snapshots (§C20).
* **Re-initialise** (`dzo mission reinit <name>`) is the only destructive operation: explicit confirmation, and a `destructive` snapshot is taken first.
* **Updating pristine** (`dzo mission update <name>` = git fetch + checkout of `ref`) only changes `servermpmissions/`.
  The live mission follows on the next render. `--dry-run` shows the resulting apply plan.
* Consequence: customisations never go into the live `db/*.xml`. They go into overlays (CE folder or merge fragments).
  (No legacy live missions are imported; every server starts with a fresh mission from pristine, §C21.)

### Pipeline

Input: instance.yaml + site repo + pristine mission + active product build + active mod generations + live mission + manifest.
Output: an **apply plan** (writes/deletes, limited to manifest paths and new generated/pristine paths) + `runtime/` (keys, serverDZ.cfg, BE cfg, args).

Steps (each a pure function over a staging tree, unit-testable):

1. Staging = pristine tree (fallback fill), empty generated dirs. Merge targets start from their pristine copy, never from the live file.
2. Apply `messages.xml`, EditorFiles and keys (`dayz.bikey` from the build, client mod keys, overlay keys → `runtime/keys`). Servermods
   (workshop or local) contribute no keys: they are never sent to clients, so nothing verifies their signatures.
3. For each mod **in list order (= dzo merge/CE precedence, later wins; conflicts are reported)**: resolve integration files (local/url/mod path; URL content cached by hash;
   never write into the workshop cache), normalise, validate, and apply using the *merge strategy registry*:
   * `ce-folder`: `types`, `spawnabletypes`, `events`, `globals` (and other CE file types the product driver allows) →
     `mod_<id>/` + a **generated `<ce folder>` entry in `cfgeconomycore.xml`** (one per folder, idempotent, in mod-list order = CE precedence)
   * `xml-append-root`: mapgrouppos, mapgroupproto, cfgeventgroups, cfgeventspawns, cfgenvironment, cfgrandompresets, zombie_territories (semantics to match `xmlmerge`, spike S3; duplicate keys by `name` reported)
   * `json-merge` for cfggameplay.json: deep merge for objects, and **append + dedupe for known list keys**
     (`WorldsData.objectSpawnersArr`, `PlayerData.spawnGearPresetFiles`, `WorldsData.playerRestrictedAreaFiles`, …, list in the product driver),
     replace for other arrays; conflicts on scalar keys between two contributors are reported
   * `json-array-concat(keys)`: cfgundergroundtriggers (`Triggers`), cfgeffectarea (`Areas`, `SafePositions`)
   * `replace`: cfgweather.xml, messages.xml
   * `patch`: init.c (a correct implementation of the intended behaviour)
   * `copy-extra`: remaining overlay files → `custom_<dir>/`
   * `hook`: scripts run in the staging dir with a defined env (`DZO_STAGING`, `DZO_LIVE_MISSION` (read-only use), `DZO_INSTANCE`, `DZO_MAP`, …)
4. Overlays (shared, then instance-local) with the same strategies. Overlays can **declare** referenced files
   (`overlay.yaml`: `object_spawners: ["*.json"]`, `spawn_gear_presets: [...]`, `restricted_areas: [...]`), and dzo generates the
   `cfggameplay.json` references with the correct `custom_<dir>/` prefix, so hand-written path fragments are no longer needed (they still work).
5. Validate everything in staging (well-formed XML, `dzce` typed parse where available, JSON parse, every referenced
   file exists, every `<ce folder>` exists). **Any error → abort; the live mission is untouched.**
6. serverDZ.cfg: parse (tolerant parser for the `key = value;` + `class` syntax), enforce `template`,
   `steamQueryPort`, `instanceId` uniqueness, write `runtime/serverDZ.cfg`.
7. BattlEye cfg with the password from `secrets/` → `profiles/battleye/beserver_x64.cfg`; delete stale `beserver_x64_active_*`.
   Profile mod configs for active mods dzo knows: `$profile:dzo-admin/config.json` (§C16), and in `$profile:metricz/config.json` /
   `$profile:logz/config.json` **only the keys dzo needs** (§C23), set by a key-level JSON merge. All other keys stay as the admin left them.
8. Args: `-config -port -profiles -BEpath -mod -servermod -cpuCount … + extra`.
9. **Apply**: compare staging with live + manifest → classify (unchanged / update / drift / new / obsolete / foreign-conflict)
   → snapshot → write atomically (temp + rename) → update the manifest → emit the report (journal, job record, Discord for drift).
10. `pre_start` hooks run **after apply, against the live mission** (they deliberately modify game data, e.g.
    `traderstocks` on `expansion/traderzones`, weather writing cfgweather/cfggameplay fragments), and are logged as such.

`dzo render <name> --dry-run --diff` prints the apply plan with classifications and unified diffs (future web feature: diff view).
Property tests assert that no apply ever writes or deletes a path outside (manifest ∪ new pristine ∪ new generated) and never touches `storage_*`.

## C7. Products, download cache and update engine

**Products (D14)** are declared in `/etc/dzo/config.yaml`, with built-in defaults:

```yaml
products:
  dayz-stable:       {server_appid: 223350,  branch: public, workshop_appid: 221100, binary: DayZServer, keys_dir: keys}
  dayz-experimental: {server_appid: 1042420, branch: public, workshop_appid: 221100, binary: DayZServer, keys_dir: keys}
  # optional per product: branch_password (password-protected beta branches), stored in secrets/
```

That experimental uses appid 1042420 (branch public) is confirmed. That it consumes workshop content via 221100 is assumed and checked once in spike S1.

A product defines how to download (steamcmd app/branch/beta password), which workshop app mods come from, the server
binary, and the launch-parameter/ports profile. A future product (e.g. a DayZ successor) gets a new product entry and,
if its launch or config model differs, a new **product driver** in code (an interface: `Download`, `BuildArgs`,
`RenderConfig`, `HealthProbe`, `RCon`). Each instance references exactly one product. Pristine missions are per instance anyway.

* **Steam authentication**, see below. After a successful login the session is reused by all download jobs.
* Server build per product: `app_info_print` build id vs the newest downloaded build. A new build is **only detected and reported**
  (Discord, `dzo_product_update_available{product,buildid}`, `dzo check remote --updates` → WARNING, status/web). **Nothing is downloaded or
  switched automatically, ever** (D7). See "Manual server upgrade" below.
* Mods: freshness via `steam/filedetails` (`time_updated`, **not** local mtime), download with
  `workshop_download_item` (batched, with retry, success parsing per item), then snapshot into `<modid>/<time_updated>/`.
  Mods are shared between products if the workshop app is the same.
* Name resolution from `meta.cpp` in the generation. The name is stored in a lock file for a stable `@Name`.
* **Update job** (`dzo update check --apply`, hourly timer by default):
  1. Determine new mod generations for the instances whose `check_interval` is due, and new server builds (report only, see above).
  2. Download + snapshot the mods (servers keep running; nothing mounted changes).
  3. Map affected instances (mod X affects the instances using X).
  4. Per affected instance and policy: `auto` → graceful restart with the update announcement, running **stop → btrfs snapshot (§C20)
     → switch generation (re-render + regenerated quadlet) → start**; if the snapshot fails with policy `abort`, the instance restarts on the old generation and an alert is sent; `notify` → record + notify; `manual` → record.
  5. Garbage-collect generations not referenced by any instance and older than N days.
* **Update windows, batching, minimum restart interval** (per instance, avoids a restart per single mod push):
  ```yaml
  updates:
    policy: auto
    check_interval: 1h
    window: ["06:00-10:00", "14:00-16:00"]   # auto-apply only inside these windows (default: always)
    quiet_hours: ["18:00-24:00"]            # never auto-restart for updates here
    min_restart_interval: 4h                # at most one update restart per 4 h; everything found meanwhile is batched
    batch_delay: 20m                        # after the first detection wait for more updates (mods often publish in bursts)
    apply_with_scheduled_restart: true      # if a maintenance restart is due within N h, just use it instead of an extra restart
    max_delay: 12h                          # upper bound: then apply even outside the window (announced)
  ```
  All pending mod updates of an instance are applied in **one** stop → snapshot → switch → start cycle.
* **No rollback of downloads.** If a mod was downloaded correctly and is broken, the mod needs a fix (by its author, or by removing it from the
  instance). An older generation is not offered as a remedy. Generations only exist so that running servers never see files change underneath them. Unreferenced generations are garbage-collected.
* **Forced mod refresh** (`dzo mod refresh <id…> | --all [--instance <name>] [--no-restart]`): for broken or incomplete downloads and a
  bugged Steam/steamcmd cache (it has happened that Steam kept serving a bad copy). dzo:
  1. deletes steamcmd's state for the item (`steamapps/workshop/content/221100/<id>`, its `downloads/` and `temp/` leftovers, and the item's entry
     in `appworkshop_221100.acf`) in the steamcmd work dir, never in a cache generation;
  2. downloads it again with `workshop_download_item … validate`, even if `time_updated` is unchanged;
  3. verifies the result (`meta.cpp` present, every PBO parses, size plausible vs. `filedetails.file_size`);
  4. stores it as a **new generation** (`<modid>/<time_updated>-r<n>/`, since the old one may be referenced by a running server);
  5. applies it to the affected instances like a normal mod update (policy, announcement, snapshot), or only at the next restart with `--no-restart`.
  The web UI offers the same as a button per mod. More elaborate recovery (e.g. a full steamcmd reset) is not automated. `dzo steam reset-cache`
  just wipes the steamcmd work dir after confirmation, and the next download fills it again.
* **Manual server upgrade** (never automatic, D7):
  1. `dzo product update <product>` downloads the new build (`app_update validate` into `cache/steamcmd/<product>`, then a reflink snapshot into
     `<buildid>/`). Running servers are not affected. `--force` re-validates and re-snapshots the current build (the equivalent of the forced mod refresh for the server).
  2. The admin prepares what the new version needs: mod updates or new mods, config changes in the site repo, and a wipe decision.
  3. `dzo instance upgrade <name> --build <buildid> [--wipe] [--dry-run]` runs stop → snapshot (`update` trigger, plus `destructive` if `--wipe`)
     → optional wipe (§C11) → switch to the build → render → start, announced like any restart. `--dry-run` shows the render plan and
     the dependency check against the new build first. Each instance is upgraded separately; nothing forces all instances of a product to switch at once.
* **Mod dependencies instead of load order:** DayZ does not (yet) honour the `-mod=` order in a reliable way. What actually decides loading is each
  addon's `CfgPatches` `requiredAddons`, and for mod developers the effective order is chaotic anyway. dzo therefore reads the (rapified)
  `config.bin`/`config.cpp` `CfgPatches` from each mod generation's PBOs (with the `pbo` lib plus a config parser) and:
  * **validates dependencies** before a render/start: every `requiredAddons` entry must be provided by the product build or an active mod. A missing
    dependency (a classic crash-on-start cause) blocks the start with a clear message instead of a crash loop;
  * shows the **dependency graph** and the resulting addon order in `dzo mod deps <instance>` and the web UI, as diagnostics for admins and mod developers;
  * optionally sorts the `-mod=` list topologically by dependencies (`mods_order: dependencies|as_listed`, default `as_listed`), so if DayZ ever starts to
    honour the order, dzo already produces a sensible one.
  This is independent of the dzo merge precedence (the list order), which only affects CE/XML/JSON merging.
* A global lock (flock) ensures a single steamcmd job at a time.

### Local servermods (D37)

Servermods that are not on the Steam Workshop: our own `dzo-admin`, private server-only tooling, or a mod under development.
An instance lists them as `{local: <name>, server: true}`, in the same mod list as workshop mods (same merge precedence and integrations).

* **Sources.** By default `site/localmods/<name>/` (the mod is versioned in the site repo). Other sources are declared once in `site.yaml`:
  ```yaml
  local_mods:
    dzo-admin: {url: https://…/dzo-admin-1.4.0.tar.gz, sha256: …}   # release archive, sha256 required
    mytools:   {path: /srv/dayz-build/@MyTools}                     # absolute host dir, e.g. a build output
  ```
  A URL archive (tar.gz or zip) is verified against its sha256 before unpacking. Unpacking rejects absolute paths, `..`, links and device files.
  A `path` or site dir is copied regular files only. Symlinks are rejected, so a host path cannot pull in files from outside the mod.
  The mod root is the dir holding `addons/`, either at the top level or in a single top-level `@<name>/` dir.
* **Generations.** dzo imports the source into `cache/local/<name>/<hash>/`, where `<hash>` is a sha256 over sorted relative paths,
  modes and contents. Identical content gives the same generation, and a running server never sees files change. The generation an instance uses is
  pinned in the lock file, like the workshop `@Name`.
* **Updates.** Nothing is imported at render or start time (a crash restart must not pick up a half-finished build). `dzo update check`
  re-hashes every local source; a new hash counts like a new workshop `time_updated`: import, then the instance's update policy
  (`auto` / `notify` / `manual`, windows, snapshot) applies. So an edit to `site/localmods/…` or a bumped `url`/`sha256` in the site repo rolls out
  the same way as a workshop update, with the git history as the paper trail. `dzo mod refresh --local <name>` re-imports on demand.
  GC and "no rollback" are the same as for workshop mods.
* **Validation (at import, blocks the new generation):** `addons/` holds at least one `.pbo`, every PBO parses and has a `prefix` header (a
  PBO without it loads without error, but none of its scripts are loaded, §C22), and `CfgPatches` is read for the dependency check above. `meta.cpp`/`mod.cpp`
  are optional, because the name comes from the instance config. A `keys/` dir is ignored.
* **Name and mount.** `<name>` matches `[A-Za-z0-9][A-Za-z0-9_.-]*` and is not purely numeric, so it cannot collide with a workshop id in
  `integrations/mods/<name>/`. It is mounted as `/dayz/@<name>` and passed as the relative `-servermod=@<name>`. A clash with a workshop mod's
  `@Name` in the same instance is a validation error.
* **Servermods only.** `server: false` on a local mod is rejected: clients cannot download it, so it would only ever cause a signature or
  missing-mod kick. Private client mods would need key handling and client distribution, which are out of scope.

### Steam authentication (interactive, expect user input)

steamcmd needs a real Steam account for workshop content (anonymous only works for some server apps). A login needs
**username + password + a Steam Guard factor**: an e-mail code, a mobile authenticator (TOTP) code, or a **confirmation in the Steam
mobile app**. Steam caches a login token in the steamcmd home (`config/config.vdf`, `ssfn*`). It usually survives for a long time, but it
**expires or is invalidated** (password change, Steam-side revocation, new device detection, long inactivity). A re-login then needs a human.
dzo must therefore treat Steam auth as an **interactive, recurring operator task**, not a one-time setup.

* **Account:** a dedicated Steam account that owns DayZ is recommended (not a personal main account), with Steam Guard enabled.
  The username is in `/etc/dzo/config.yaml` per product/workshop app (usually one account for everything).
* **Interactive login flow:** dzo runs steamcmd in the `dzo-steamcmd` container **attached to a pseudo-terminal** (`creack/pty`, MIT)
  and drives it as a small state machine. It recognises the prompts (password, Steam Guard e-mail code, two-factor code, "confirm in the
  mobile app", success, failure types) and relays each one to the user:
  * **CLI:** `dzo steam login [--user name]` asks for the password (no echo) and then the code, or shows "confirm in the Steam app…" and
    waits with a timeout. It also works over SSH, and `dzo steam login --passthrough` is a raw pty fallback if Steam changes its prompts.
  * **Web UI (phase 3):** a "Steam login required" banner and a form wizard (password → code / waiting for app confirmation), streamed via SSE,
    with the permission `steam.auth`, CSRF protection, and a short-lived server-side session that holds the pty. The same state machine is used.
* **Secrets handling:**
  * By default the password is **not stored**. It is used once for the pty session and wiped from memory. Only the Steam
    session files live in `secrets/steamcmd-home/` (0700, owned by `dayz`, never in git or backups unless encrypted).
  * Optional `steam.store_password: true` for unattended re-logins when only the session expired and Steam accepts the cached device
    without a new Guard code. It is stored **encrypted with `systemd-creds`** (host key / TPM2 if available), never in plain text in the config.
    A Guard code or app confirmation can still be required at any time. That is the expected case, not an error.
  * TOTP shared secrets (for fully automatic mobile-authenticator codes) are **not** supported by default. Holding them on the server would
    defeat the second factor. If you ever want it, it is an explicit opt-in (Q21).
  * steamcmd output is **scrubbed** before logging (password echo, tokens) and never stored raw in job logs.
* **Detecting that auth is needed:** every steamcmd job classifies failures (`Cached credentials not found`, `Invalid Password`,
  `Two-factor code mismatch`, `Login Failure`, `Rate Limit Exceeded`, `No subscription`, …). Auth-related failures set the global
  state **`steam_auth_required`**:
  * The update engine pauses downloads. **No restarts are triggered** for updates it cannot download. Running servers keep running on
    their current generations, and pending update jobs are kept and resume automatically after a successful login.
  * Notifications: Discord (admin target, once, with a reminder after N hours), `dzo_steam_session_valid 0` + `dzo_steam_auth_required 1`
    in `/metrics`, and `dzo check remote --steam` → WARNING, then CRITICAL after a configurable time. Icinga shows it.
  * `dzo steam status` shows account, last successful login, last successful download, and current state.
* **Lockout protection:** a failed password/code is never retried automatically. Steam rate limits are respected with exponential backoff and
  a clear "rate limited until …" state, so automatic retries can never lock the account.
* **Proactive check:** a cheap periodic probe (part of the hourly update check: `+login` with cached credentials + `+quit`) detects expiry before the
  next download needs it, so an admin can re-authenticate at a convenient time.
* **Tests:** a scripted fake steamcmd (pty) reproduces all prompt/failure variants, so the state machine, the CLI and web flows and the pause/resume logic
  are covered without real credentials (and count toward the 85 %).

## C8. RCon and graceful restart (port of `dayz_restart`)

* **Built-in BattlEye RCon client** (`internal/battleye/rcon`, our own implementation, no external tools and no external RCon library):
  * Protocol (BattlEye RCon over UDP): `BE` header + CRC32, login packet, command packets with a sequence number (0–255 wrap),
    **multi-packet responses** (reassembly by index/count), **server messages** (type 0x02) that must be acknowledged within the timeout
    (otherwise BattlEye drops the client), and keep-alive (an empty command) at least every 45 s.
  * A connection manager per instance: one long-lived authenticated session shared by all users (restart jobs, web console, player tracking,
    broadcasts), a command queue with per-command timeouts, automatic reconnect with backoff, and a `version` probe as a liveness check.
  * **Event stream** from server messages: player connect (slot, name, IP:port), "Verified GUID", disconnect, kicks, chat (`(Global)`, `(Side)`, `(Direct)`, …),
    and RCon admin logins. These are parsed into typed events for player tracking (§C18), chat log, ban enforcement and Discord.
  * Typed helpers over the raw commands: `players` (parsed: slot, IP, port, ping, GUID, verified, name, lobby), `say`, `kick`, `#lock`/`#unlock`,
    `bans`/`addBan`/`removeBan`/`loadBans`/`writeBans`, `#kick -1`, `missions`, `version`.
  * GeoIP enrichment is done by dzo's own GeoIP layer (§C18), not in the RCon client.
  * Fully unit-tested against an **in-process fake BattlEye server** (packet loss, reordering, multi-packet responses, missed acks, auth failures, CRC errors),
    plus fuzzing of the packet parser. It is part of the 85 % coverage. Legacy `bercon-cli` and `dayz_restart` behaviour serves only as a reference for tests.
  * The RCon endpoint is reached from the host (`127.0.0.1:<rcon>` with host networking, or the published port). `RConIP` is set to `127.0.0.1` where possible (S5).
* `dzo restart <name> [--minutes 30 --lock 3 --delay 3 --text "…"] [--now]`:
  announcement schedule as in A4 → `#lock` at the offset (retry) → up to 3 passes of `players` + `kick <id> <reason>`,
  then `#kick -1` → wait the delay → restart. A plain restart is `systemctl --user restart dzo-<name>.service`. If a pre-change snapshot is due
  (§C20), the job runs `stop` → snapshot → `start`, and the start renders/applies (`ExecStartPre`).
  **No `#shutdown`.** If RCon is unreachable: log it and restart via systemd immediately (configurable).
* It runs as a transient systemd unit (`systemd-run --user --unit dzo-restart-<name>`), or as the service of the
  restart timer, so it survives CLI disconnects, is visible, and is cancellable (`dzo restart <name> --cancel` sends `#unlock` + an announcement).
  Concurrent requests (maintenance timer and update restart at the same moment) are merged: the earliest deadline wins.
* `dzo rcon <name> [cmd…]` (interactive console or one-shot), with players output incl. GeoIP.
* Later (FR-16a): scheduled broadcast messages per instance, run by the daemon or by a timer.

## C9. Health, monitoring (Prometheus/Icinga), notifications (Discord), logs

### Container health checks (D18: must work)

`dzo health` runs **inside** the container (static binary, bind-mounted). Both network modes work, because it
probes `127.0.0.1` inside the container's network namespace:

| Probe | Used as | Logic | Exit |
|---|---|---|---|
| `dzo health startup` | `HealthStartupCmd` | DayZServer process exists **and** A2S `A2S_INFO` on the query port answers (incl. the challenge handshake = 2 round trips; the query port only answers once the mission is loaded, which is exactly the readiness signal) | 0 once up, 1 while starting |
| `dzo health live` | `HealthCmd` | process exists (not zombie) **and** A2S answers within timeout (2 tries). Optional `--rcon` adds an RCon `version` probe (off by default, A8.10) | 0 healthy, 1 unhealthy |

* Startup and liveness are separate, so long mission loads (many mods) do not count as failures, while a hang after startup
  (e.g. the `#shutdown` freeze) is detected within `interval × retries` (default 5 min) → `kill` → systemd restart.
* `Notify=healthy` makes `systemctl start/restart` block until the server really answers. The restart job and
  Icinga therefore see real state, not just "container running".
* Health transitions are recorded (podman events / journal) → Discord notification + status file.
* Spike S6 verifies the podman version on the host supports `HealthStartup*`, `HealthOnFailure=kill` and `Notify=healthy` in rootless quadlets.

### Status service, Prometheus and remote Icinga (FR-18a, D22)

Icinga runs on another host, so everything is reachable over the network. No local plugin execution is required.

* **`dzo-exporter.service`** (small long-running user service, available from Phase 2 before the web UI; later the same
  handlers are mounted in `dzo serve`):
  * `GET /metrics` in Prometheus exposition format:
    * per instance: `dzo_instance_up`, `dzo_instance_health{state=starting|healthy|unhealthy}`, `dzo_instance_players`,
      `dzo_instance_max_players`, `dzo_instance_uptime_seconds`, `dzo_instance_restarts_total{reason=crash|health|scheduled|update|manual}`,
      `dzo_instance_last_render_success`, `dzo_instance_last_render_timestamp`, `dzo_instance_mission_drift_files`,
      `dzo_instance_a2s_rtt_seconds`, `dzo_instance_info{product,build,map}`, `dzo_instance_mods_pending_update`
    * global: `dzo_product_build_info{product,buildid}`, `dzo_updates_pending`, `dzo_update_last_check_timestamp`,
      `dzo_steam_session_valid`, `dzo_jobs_failed_total{type}`, `dzo_cache_bytes`, `dzo_disk_free_bytes`
    * `dzo_build_info{version}` + standard Go process metrics
  * `GET /status` and `/status/<instance>`: JSON with the same data plus human-readable messages (for Icinga).
  * Data comes from the systemd/podman state (it runs as `dayz`, so it can query its own user session), A2S probes, and job/state files.
    Cached per scrape interval so scrapes never block on game servers.
  * **Plain HTTP by default** on a configurable listen address (default `:9464`), so Prometheus can scrape it directly.
    **Optional TLS**: `tls.cert_file` + `tls.key_file` (+ optional `tls.client_ca_file` for mTLS) in `/etc/dzo/config.yaml`.
    Optional basic auth / bearer token and an IP allow-list. No secrets or player IPs are exposed.
  * **Certificate rotation without restarts:** TLS is only handled by `dzo-exporter` (and later `dzo serve`), never by the
    game containers, so a certificate change can never require a DayZ server restart. The certificate is loaded via
    `tls.Config.GetCertificate` and re-read when the files change (mtime/inotify check) or on `systemctl --user reload dzo-exporter`
    (SIGHUP). Existing connections keep running and the process is not restarted. This works with ACME tools that just replace
    the files (e.g. certbot/lego deploy hooks can call the reload). An invalid new certificate is rejected and logged, and the old one stays active.
* **Icinga on the remote host**, in order of preference:
  1. `dzo check remote --url https://<host>:9464/status --instance deerisle` (the same Debian package installed on the Icinga
     host or satellite). It implements the Monitoring Plugins API (OK/WARNING/CRITICAL/UNKNOWN, perfdata: players, uptime, restarts, rtt).
     Thresholds are passed as flags. Other subcommands: `check remote … --updates`, `--steam`, `--jobs`, `--disk`.
  2. `dzo check a2s host:queryport`: a pure network check of the game server itself (independent of the operator, catches host/network outages).
  3. Alternatively, Icinga reads from Prometheus (e.g. `check_prometheus` style queries) if a Prometheus server scrapes `/metrics`.
* `contrib/icinga2/` ships `CheckCommand` definitions and example `apply Service` rules for 1 + 2.
* `/run/dzo/status/*.json` (§C2) remains as a local fallback / debugging aid.
* Optional: in-game metrics from MetricZ, re-exported by `dzo-exporter` itself (§C23). No `metricz-exporter` quadlet and no textfile collector are needed.

### Logs, metrics, traces into the existing stack (Prometheus + Loki, gigapipe)

* **Metrics:** Prometheus scrapes `dzo-exporter` `/metrics` (above), which also carries the MetricZ series of every instance that runs
  MetricZ (§C23), all with consistent labels (`instance`, `product`, `map`).
* **Logs → Loki:** dzo does not ship logs itself by default. It guarantees a stable, documented layout that an agent
  (Grafana Alloy / promtail / vector, whatever the stack uses) collects:
  * journald: units `dzo-<name>.service` (server console) and `dzo-*.service` (operator jobs). dzo logs are structured
    (`log/slog` JSON with `instance`, `job`, `event` fields).
  * LogZ NDJSON: `profiles/logz/logs/` of the instance (default), or `/var/log/dayz/<name>/` with the optional `container.logz_dir` bind.
    **Required** for every LogZ feature in dzo: dzo reads LogZ data only from Loki.
  * `dzo loki-config` prints a ready-made agent config snippet with the right paths and labels.
  * Optional later: a built-in Loki push client, if running an agent is not wanted.
* **Traces (optional):** operator jobs (update check → download → render → graceful restart) are natural traces.
  They are instrumented with OpenTelemetry and exported via OTLP if an endpoint is configured (gigapipe accepts traces). Off by default.

### Discord (FR-18b, Q14)

* `/etc/dzo/config.yaml` defines a **default webhook** and optional **named webhooks**. URLs are stored in `secrets/`.
* Each instance uses the default unless `notify.discord` lists other targets (it can list several; `[]` disables).
  Per target, an event filter (e.g. restarts → public channel; drift, render and job failures → admin channel).
* Events: mod update detected/downloaded/applied, **server build available** (manual action needed), mod refresh result, restart scheduled/started/finished, health unhealthy/recovered, crash loop,
  render/validation failed, drift detected, **steam login required / session expired** (with reminders), job failed.
* Rate limiting + coalescing (e.g. one message for "12 mods updated, 3 servers restarting"). Templated messages (Go templates) per event.
* A `Notifier` interface, so mail/Matrix/etc. can be added later. `dzo notify test [--target x]`.

### Logs

* Console output → journald (`journalctl --user -u dzo-<name>`, `dzo logs <name> -f`).
* After an unclean exit: a crash summary (tails of error.log, script*.log, *.RPT) in the journal, the job record and Discord. It is taken
  before any rotation, from the files still in place.
* LogZ: writes into the profile dir (optional `container.logz_dir` bind), shipped to Loki by the agent. dzo never reads the files (§C23).

### Profile log rotation

The profile dir collects logs from many writers: the server (`*.RPT`, `script_*.log`, `crash_*.log`, `*.ADM`, `*.mdmp`, some of them rotated by the
server itself), BattlEye, and mods, which put logs wherever they like. Legacy moved a fixed glob set into `profiles/logs/<ts>/` on every start, which
did not work well (A8). dzo instead uses a **regex list** and moves everything **except the newest file of each rule** into a log archive.

```yaml
# site.yaml (defaults for all instances); instance.yaml `logs:` adds rules and overrides settings
logs:
  rotate:                                   # RE2, matched against the slash path relative to profiles/
    - {match: '^DayZServer_x64_.*\.RPT$'}
    - {match: '^script_.*\.log$'}
    - {match: '^crash_.*\.log$'}
    - {match: '^DayZServer_x64_.*\.ADM$'}
    - {match: '\.mdmp$', max_age: 14d}      # crash dumps are big: shorter archive retention
    - {match: '^VPPAdminTools/Logs/.*\.txt$'}   # mod logs are added per site
    - {match: '^battleye/.*\.log$', keep_newest: 2}
  keep_newest: 1          # per rule: this many newest matches (by mtime) stay in place
  min_age: 10m            # never touch a file modified more recently
  archive: {compress: gzip, max_age: 90d, max_size: 50G}   # per instance; oldest deleted first
```

* **One rule = one series.** Per rule, the matching files are sorted by mtime, and the newest `keep_newest` stay in place. The newest is the file the running
  server (or the mod) may still be writing, and it is the one an admin looks at first. All older matches are archived.
  The built-in default list for the DayZ product driver contains only the server's own files (the first five lines above, exact names confirmed in S9). BattlEye and mod logs are site config.
* **Safety:** only regular files strictly below `profiles/` are touched. dzo does not follow symlinks and never matches a file modified within `min_age`. A hard exclusion
  list covers what dzo or the game need (`serverDZ.cfg`, the BattlEye config, `$profile:dzo-admin/`, mod `config.json` files the render manages, `*.json`/`*.xml`
  mod configs unless a rule explicitly names them). A rule that matches an excluded file is a validation error, not a silent skip.
* **Move:** the archive is outside the instance subvolume, so a plain rename would cross a btrfs subvolume boundary (EXDEV). dzo streams each file through gzip into
  `${paths.logs}/<name>/<YYYY-MM-DD>/<relative path>.gz` (or a reflink copy with `compress: none`), fsyncs, then unlinks the original. Name clashes get a numeric suffix.
  Moved files also stop growing the instance's btrfs snapshots from then on (older snapshots keep them until pruned, §C20).
* **When:** in `ExecStartPre` before the render (the server is down, so nothing is being written; the previous run's newest files stay in place for inspection),
  and **hourly** via `dzo-logs.timer` for all instances while they run. The timer also applies archive retention (`max_age`, then `max_size`, oldest first).
* `dzo logs rotate <name> [--dry-run]` shows or runs the plan (each file with its rule). `dzo logs archive <name> [--since …] [--match …]` lists archived files, and the web UI
  offers the same with download (operator role). Metrics: `dzo_logs_rotated_files_total{instance}`, `dzo_logs_archive_bytes{instance}`, and
  `dzo_profile_unmatched_bytes{instance}` with a list in `dzo logs rotate --dry-run`: large files no rule matches, so new mod logs get noticed and a rule added.

## C10. CLI (legacy → new)

| Legacy | New |
|---|---|
| `dzpodman build` / `cleanup` | `dzo images build` / automatic via timer |
| `dzpodman start/stop/restart` | `dzo instance apply <name>` (render quadlets + daemon-reload + enable), `dzo start/stop/restart <name>` |
| `dzpodman logs` | `dzo logs <name> [-f]` (journal) |
| `dzpodman exec/run` | `dzo shell <name>` (debug container with the same mounts), `dzo exec <name> …` |
| `dz login` | `dzo steam login [--user] [--passthrough]`, `dzo steam status` (interactive: password + Steam Guard code or app confirmation) |
| `dz install / update / forceupdate` | `dzo product install/update <product> [--force]` (manual only), `dzo instance upgrade <name> --build <id> [--wipe]`, `dzo update check [--apply]` (mods; server builds report only) |
| `dz mi <ids…>` (force) | `dzo mod refresh <id…> \| --all [--instance x] [--no-restart]`, `dzo steam reset-cache` |
| `dz add / remove / m / mi` | `dzo mod add <id> [--instance x --server]`, `dzo mod add --local <name> [--path <dir> \| --url <u> --sha256 <h>] [--instance x]`, `dzo mod remove`, `dzo mod update [ids…]`, `dzo mod refresh --local <name>` |
| `dz map` | `dzo mission init/update/status/rollback/reinit <name>` (pristine fetch, one-time init, in-place update, snapshots) |
| `dz xml` | `dzo integration check <modid>` (fetch, normalise, validate, show diffs) |
| `dz a / d / l` | `dzo instance mods <name> add/remove/move/list` |
| `dz c` | `dzo config diff/apply <name>` (serverDZ.cfg) |
| `dz s` | `dzo status [<name>]` (build, generations, running, uptime, players via A2S, args, pending updates) |
| `dz b / w` | `dzo backup create/list/diff/pin/unpin/prune <name>`, `dzo restore <name> <id> [--path …]`, `dzo wipe <name>` |
| `restart`, `restart_extern`, `dayz_restart`, `mod_update_*`, cron | `dzo restart <name>`, `dzo-update-check.timer`, `dzo-restart-<name>.timer` |
| `healthcheck` | `dzo health startup/live` (inside the container), `dzo check …` (Icinga) |
| `DEVELOPMENT` / `DONT_START` | `dzo render <name> --dry-run --diff`, `dzo shell <name>` |
| (new) | `dzo site pull/status/commit/validate`, `dzo notify test`, `dzo legacy convert-config --repo <checkout> --ref <branch>…` (example site config, §C21) |
| (new) | `dzo test boot <name> [--server steam\|<dir>] [--mods-from steam-client\|cache\|<dir>] [--native\|--container] [--keep-tree] [--vanilla]` (headless boot test, development only, §C22) |
| (new, phase 3) | `dzo user list\|add\|disable\|enable\|reset-password\|reset-mfa\|revoke-sessions <name>` (web user bootstrap and recovery; `add` prints a one-time enrolment link, §C12) |

## C11. Hooks and extensibility

* **Instance lifecycle:** `instance create`, `remove` (stops, removes quadlets and generated timers incl. transient schedule timers,
  exporter targets, map markers/state, asks about snapshots; Icinga then sees the instance as unknown, which is documented so the service can be removed).
  **Renaming is not supported in place** (the unit names, `instanceId`, paths and DB keys depend on it). The documented path is: create a new instance +
  `dzo instance clone <old> <new>` (subvolume snapshot copy of the data) + remove the old one.
* Hook points: `post_backup` (§C20, offsite copies), `post_download(mod)`, `post_merge(mod)`, `post_render`, `pre_start`, `post_stop`, `pre_update`, `post_update`.
* A hook is an executable with the env contract `DZO_*` and a JSON context on stdin. Non-zero exit aborts (configurable).
* Built-in Go plugins over time: `traderstocks`, `weather` (Open-Meteo), `nominal-scale` (authoring helper).
* Core as a Go library (`internal/…` → a `pkg/…` API later); CLI and web are thin adapters over one service layer with
  **jobs** (long-running operations with logs, progress and cancellation). That is the basis for the web UI.

## C12. Web platform (phase 3+)

* `dzo serve`: `net/http` + `html/template` + htmx (served from Debian's `libjs-htmx`, `/usr/share/javascript/htmx/`; an embedded copy only for non-deb dev builds and tests), SSE for live logs, job progress, player lists and the map.
* Pages: dashboard (instances, players online, build, pending updates), instance detail (mods with order for merge precedence,
  overlays, serverDZ.cfg editor with diff, render diff, RCon console), **players and moderation (§C18)**, **admin map (§C16/§C17)**,
  **schedules (restarts, broadcasts)**, mod search (Steam Web API), jobs, **backups (list, diff, browse, pin, restore)**, audit log, and later an XML/JSON editor with `dzce`
  validation (the legacy XmlTree idea).
* JSON API `/api/v1/…` over the same service layer (Discord bots, scripts). Every UI action is an API call.
* **Documentation** (D36): `/docs/` serves the installed Sphinx HTML (`/usr/share/doc/dzo/html/`, overridable with `web.docs_dir` for dev builds) with a plain `http.FileServer`, public (no login; it contains nothing secret), linked from the UI header and the login page (so account recovery instructions are reachable when locked out).
* Auth: see **Authentication** below (password + TOTP by default). **RBAC scoped per server**: roles
  `admin` / `moderator` / `operator` / `viewer`, assignable globally or per instance (e.g. a moderator may kick/ban on deerisle only).
  Fine-grained permissions under the roles (`player.kick`, `player.ban`, `player.ban.global`, `player.spawn_item`, `player.teleport`,
  `server.restart`, `broadcast.manage`, `pii.view` for IPs, …). CSRF protection, session timeouts, and an audit entry for every mutating call.
* **Database (§C18):** SQLite by default, PostgreSQL optional, used for users, audit, jobs, players, bans, schedules and caches. Configuration of
  servers stays in the site repo (git commit per change, with author). Operational data (players, bans, schedules created in the UI)
  lives in the database.

### Authentication (D34)

**Default: password + TOTP for every user.** `auth.mfa: required` is the default. `optional` exists only as an explicit config choice (e.g. a
single-admin setup behind a VPN) and is shown as a warning on the dashboard and in `dzo check remote`.

* **Bootstrap, no default passwords.** The first admin is created on the host: `dzo user add <name> --role admin` prints a **one-time enrolment
  link** (random token, valid 24 h, single use). The link leads to: set password → scan the TOTP QR code → confirm with a code → get recovery codes.
  The account is only usable after that. Admins invite further users the same way from the UI. The CLI stays the recovery path:
  `dzo user list|add|disable|enable|reset-password|reset-mfa|revoke-sessions <name>`. It runs as `dayz` on the host, so the unix account is the
  trust boundary, and every CLI action is audited with source `cli`.
* **Passwords:** argon2id (`golang.org/x/crypto`), minimum length 12, no composition rules (NIST SP 800-63B), a check against a small built-in list of
  common passwords, and the username is not allowed as the password. Changing the password needs the current password and a TOTP code.
* **TOTP** (RFC 6238, SHA-1, 6 digits, 30 s, so every authenticator app works): ±1 step tolerance, **replay protection** (the last accepted time step
  per user is stored, so a code works only once), and the secret is **encrypted at rest** in the DB with a key from `secrets/` (created by `dzo setup`,
  optionally sealed with `systemd-creds`). The QR code is rendered server-side as an inline PNG, and the secret is also shown as text. Libraries: `pquerna/otp`
  (Apache-2.0) and a QR encoder (e.g. `skip2/go-qrcode`, MIT), both covered by the licence check.
* **Recovery codes:** 10 one-time codes, stored as argon2id hashes, shown once, regenerable (needs a TOTP code). Using one is audited and notified.
* **WebAuthn / passkeys (optional, later phase):** `go-webauthn/webauthn` (BSD-3) as an additional second factor next to TOTP. TOTP stays the default and
  the one factor every user has.
* **Login flow:** username + password → a short-lived pre-auth state (5 min, bound to the browser) → TOTP or recovery code → session. Error messages
  are generic ("invalid credentials") and do not reveal whether a user exists. Password and code attempts are verified in constant time.
* **Brute-force and lockout protection:** rate limits per IP and per account, with exponential backoff. After N failed attempts (default 10) the account is
  locked temporarily (default 15 min), never permanently, so an attacker cannot lock admins out forever; the CLI can unlock. Lockouts, recovery-code use, MFA resets
  and (optionally) logins from a new country (GeoIP) are sent to the admin Discord target.
* **Sessions:** server-side in the DB (random 256-bit id; only its hash is stored). Cookie `__Host-dzo_session` with `Secure`, `HttpOnly` and
  `SameSite=Strict`. Idle timeout (default 30 min) and absolute lifetime (default 12 h). Users can see and revoke their sessions (browser, IP, country, last use), admins
  can revoke anyone's. All sessions of a user are revoked on a password change, MFA reset or when the user is disabled. The session id is rotated at login.
* **Step-up re-authentication:** sensitive actions need a TOTP code entered in the last 5 minutes. These are user/role management, global or country/ASN bans, restart or broadcast to
  all servers, wipe/restore/reinit, secret changes (webhooks, RCon rotation), and API token creation.
* **CSRF and browser hardening:** `SameSite=Strict` plus a per-session CSRF token sent by htmx as a header (`hx-headers`) and checked on every
  mutating request, an `Origin`/`Referer` check, a strict CSP (no inline scripts; htmx/Leaflet from local static paths), `X-Frame-Options: DENY`, and HSTS when served over HTTPS.
* **Transport:** `Secure` cookies need HTTPS. `dzo serve` binds to localhost by default and sits behind a reverse proxy, or uses its own TLS listener
  with hot-reloaded certificates (as the exporter, §C9). Plain HTTP is refused for non-loopback listeners unless `auth.allow_insecure_http` is set explicitly (dev only).
* **OIDC (optional):** `coreos/go-oidc` (Apache-2.0), authorisation code flow with PKCE. MFA is delegated to the IdP only if the ID token proves
  it (`amr` contains `mfa`/`otp`/`hwk`, or a configured `acr` value). Otherwise dzo requires its own TOTP on top (`oidc.require_mfa: true`, default).
  Roles can be mapped from a groups claim. Local accounts stay available as a break-glass path (can be disabled).
* **Trusted reverse-proxy header** (e.g. an authenticating proxy): off by default. It is only honoured when the request comes from a configured proxy
  address, dzo listens on loopback or a unix socket, and the header name is configured. The proxy must then enforce MFA, which the docs state clearly.
* **API tokens** (bots, scripts): not interactive, so there's no TOTP. Instead they are scoped (permissions + instances), have an expiry, are shown once and stored hashed,
  and belong to a named service account. They can never manage users, roles or tokens. Creating a token needs step-up.
* **Audit:** login success/failure, lockout, MFA enrolment/reset, recovery-code use, session revocation, token create/revoke, role changes.
* **Tests:** RFC 6238 test vectors, a fake clock for step tolerance and replay, lockout/backoff, CSRF rejection, session expiry/rotation, the OIDC flow against
  a fake IdP, and the enrolment link lifecycle. All count toward the 85 % gate.

## C13. Security

* Rootless podman, no privileged containers; the runtime container gets no host secrets except its rendered BE cfg.
* Steam credentials: only the steamcmd session in `secrets/` (the password is never stored).
* RCon password per instance, generated, rotatable (`dzo rcon rotate`), bound to localhost when on host network where possible (`RConIP`, spike S5).
* Hooks run with the operator user's rights. They come from the site repo, so the git repo is the trust boundary.
* The status files for monitoring contain no secrets (no RCon passwords, no player IPs).
* **Web UI:** password + TOTP MFA required by default, no default credentials, CLI-only bootstrap and recovery, server-side sessions, step-up for
  sensitive actions (§C12 Authentication). TOTP secrets are encrypted at rest; passwords, recovery codes, session ids and API tokens are stored only as hashes.
* **`dzo-admin` endpoint:** with `network: host` the game server shares the host network namespace, so any local process can reach the endpoint.
  The **per-instance token is the only guard** (constant-time compare, rotated on every render, 0600 file in the profile dir, one endpoint port per instance,
  bound to loopback / the pasta gateway only, rate-limited). Any other local user on the host is therefore trusted only as far as they cannot read `profiles/`.

## C14. Debian packaging (D17)

* Source package `dayz-server-operator` with a proper `debian/` directory, targeting **trixie**: plain `dh` with `override_dh_auto_build`/`_test`
  calling the Makefile (no `dh-golang`, because that ties the build to Debian's Go). The build uses the **upstream Go toolchain** (§C0) found via `GO`/`PATH`
  in the builder, Go modules are **vendored** (`go mod vendor`, so builds work offline), and the result is a `CGO_ENABLED=0` static binary. `debian/copyright` (machine-readable DEP-5) is generated/checked
  against the vendored licences (feeds into the AGPL compatibility check); our own files are `Copyright: 2026 Bernd Zeimetz <bernd@bzed.de>`, `License: AGPL-3.0-or-later`, matching the SPDX headers.
* Binary package `dzo` ships:
  * `/usr/bin/dzo` (static; also bind-mounted into containers for health checks)
  * `/usr/share/dzo/images/{runtime,steamcmd}/Containerfile`, quadlet templates, default presets
  * `/etc/dzo/config.yaml` (conffile, commented defaults)
  * `sysusers.d/dzo.conf` → system user `dayz` with home `/var/lib/dzo`; `tmpfiles.d/dzo.conf` → `/var/lib/dzo` (home + default data dir), `/run/dzo/status`.
    Custom data paths (e.g. `/srv/dayz`) are created and validated by `dzo setup`, not by the package.
  * Icinga 2 CheckCommand definitions + example services, bash/zsh completion, man pages (generated from the CLI)
* `postinst`: allocate **subuid/subgid** ranges for `dayz` (sysusers does not do this; needed for rootless podman),
  `loginctl enable-linger dayz`, and **no** automatic start of game servers. `dzo setup` (run as `dayz`) does the
  first-time steps: image builds, steam login, site repo clone.
* Documentation: `Build-Depends: python3-sphinx`, `make docs` in `override_dh_auto_build`, installed to `/usr/share/doc/dzo/html/`, and `dh_sphinxdoc` links Sphinx's JS/CSS to the Debian packages (`libjs-sphinxdoc`) instead of duplicating it.
* Depends: `podman (>= 5.4)`, `systemd (>= 257)`, `git`, `uidmap`, `passt`, `libjs-htmx`, `libjs-leaflet`. Recommends: `geoipupdate`, `btrfs-progs` (admin tooling; dzo uses the ioctls directly),
  `python3`, `python3-requests` (for hooks). Suggests: `postgresql`, `monitoring-plugins-basic`. All are available in trixie.
* Upgrades: `postinst` triggers `dzo quadlet regenerate` for the `dayz` user (via `systemctl --user -M dayz@`) so
  generated units follow template changes. Running servers are not restarted automatically.
* The `.deb` is built in CI (`dpkg-buildpackage` in a `debian:trixie` container with debhelper from trixie + the upstream Go tarball) and published as a **CI artifact / release asset**. No apt repo for now (D21).

## C15. Testing strategy and CI (D20, D21)

**Coverage gate: ≥ 85 %** total statement coverage (`go test -coverpkg=./... -coverprofile`), measured over all
non-generated packages; `cmd/` wiring is kept thin so it does not dilute the number. CI fails below the threshold,
and per-package coverage is reported so weak spots are visible. Coverage is measured on unit + golden + fake-backed
integration tests; the real-podman tests below add to it where they run.

Test layers:

| Layer | What | Where it runs |
|---|---|---|
| Unit | merge strategies, normalisers, serverDZ.cfg parser, manifest/apply classification, restart schedule, RCon protocol (fake UDP server), A2S probe (fake server), policies, notifier formatting, metrics | everywhere |
| Property / fuzz | apply never touches paths outside the manifest set or `storage_*`; parsers do not panic (Go fuzzing on cfg/XML/JSON inputs) | everywhere (fuzz time-boxed in CI) |
| Golden | legacy render outputs (S0) for all five servers + synthetic fixtures; `-update` flag to regenerate on intended changes | everywhere |
| Fake-backed integration | full CLI flows against fake `systemctl`/`podman`/`steamcmd` binaries (recorded behaviour) and a temp dir tree | everywhere |
| Real podman | quadlet generation + `systemctl --user` + health checks with a tiny dummy "server" image that answers A2S | GitHub Actions (Ubuntu runner has podman) / GitLab runner with podman+systemd (tagged, optional) |
| Packaging | build the `.deb`, `lintian`, install into a clean trixie container, `dzo version`, `dzo setup --dry-run` | both CIs |
| Boot test (§C22) | real `DayZServer` boots of rendered output: the five example site configs, integration changes, `dzo-admin` builds | developer machines; optional tagged GitLab runner with a pre-installed server; **not** counted in the coverage gate |

The unit, golden, integration and packaging layers never need Steam credentials or the real DayZ server. steamcmd and Steam Web API interactions are replayed from recorded fixtures.
The boot test is the only layer that needs the real server files. The boot-test code itself is unit-tested against a **fake `DayZServer`** (a small
program that answers A2S and writes recorded log fixtures from S9), so its own coverage does not depend on the game.

CI (identical logic, two front-ends):

* `Makefile` targets: `lint` (golangci-lint, `go vet`, `reuse lint` from the trixie `reuse` package), `test` (race, coverage, gate), `fuzz-short`, `golden`, `licenses`, `docs` (`sphinx-build -W`, warnings fail the build)
  (`go-licenses check` against the AGPL-compatible allow-list), `build` (static, `-trimpath`, version stamping), `deb`, `integration`.
* **`.github/workflows/ci.yml`**: jobs lint → test(+coverage gate, upload coverage report) → licenses → build → deb (artifact)
  → integration-podman. Tags create a GitHub release with the binary + `.deb`.
* **`.gitlab-ci.yml`**: same stages, official `golang:1.27` image for pure Go jobs + `debian:trixie` (+ upstream Go) for packaging and integration, coverage regex + Cobertura report for MR widgets,
  `.deb` as job artifact / release asset, podman integration job on a tagged runner.
* Renovate/Dependabot for Go modules, the Go toolchain version and base images (the licence check runs on every bump).

## C16. `dzo-admin` servermod and live admin map (D23, Q17, Q19, Q20)

**Goal (Q17):** an HTML map in the web UI that shows **live player and vehicle positions** and lets admins act on
them: **spawn items for a player**, **teleport players** (click on the map), **repair or delete vehicles**, and
**send direct messages to players**.

**Principles (Q19, Q20):**
* **Everything that changes the game world goes through our own admin mod `dzo-admin`**: teleports, item spawns,
  vehicle repair/delete, messages and any future action. The live state the map needs (player list with positions, vehicle
  inventory) also comes from this mod.
* **`dzo-admin` is a separate mod, fully independent of LogZ.** No shared code, DTOs or runtime dependency. It must work
  with or without LogZ loaded. LogZ stays a pure logging mod: its NDJSON lines go to Loki (§C9), and dzo can optionally query them there for
  extra features, never for anything the map or the admin actions need (§C23). (Grafana/Loki is where history and analytics live.)
* Server-side only (`-servermod`), no client mod. Developed in its own repository with the `dayz-dev` skill
  (`/home/bzed/workspace/skills/dayz-dev-plugin`), which verifies every engine/script API against the real sources and the
  1.29 notes before it is used.
* **Distribution: as a local servermod (D37, §C7), not through the Steam Workshop.** The `dzo-admin` repository's CI packs the PBO
  (with the `prefix` header; no signing needed for a servermod) and publishes a release archive. Sites reference it in `site.yaml` as
  `local_mods: {dzo-admin: {url: …, sha256: …}}` and enable it per instance with `{local: dzo-admin, server: true}`. An upgrade is a reviewed
  commit that bumps URL + hash, rolled out under the update policy. Developers point `path:` at their build output. dzo recognises the mod by its
  `CfgPatches` class, not by its name or source, and then writes `$profile:dzo-admin/config.json` in the render. Version skew between `dzo` and the mod
  is handled by the `hello` protocol version.
* Every `dzo-admin` build is **boot-tested** (§C22) before release, as the skill demands for any mod change: the script module count
  rises over the vanilla baseline, there are no `SCRIPT (E)` lines, the mod's own "loaded" line is printed, and the mod registers with the
  **fake dzo endpoint** started by the boot test (this proves the `RestApi` path and the token).

**`dzo-admin` protocol (versioned, JSON):**

| Kind | Name | Payload | Result |
|---|---|---|---|
| state | `players` | – (also pushed periodically, interval configurable, e.g. 5 s) | steam id, name, position, orientation, health/blood, alive/unconscious, vehicle (if any) |
| state | `vehicles` | – (pushed at a low interval, e.g. 60 s, plus on demand) | persistent id, class, position, health/ruined, fuel, occupants |
| action | `message` | player (steam id) or all, text, style (chat / on-screen notification where reachable server-side) | delivered y/n |
| action | `teleport` | player, target x/z (y from surface) or player-to-player, optional orientation | new position |
| action | `spawn_item` | player, class name, quantity/health, target (inventory → hands → ground at the player's feet) | spawned entity id(s) |
| action | `vehicle_repair` | persistent id, scope (engine/parts/wheels/fluids/all) | state after repair |
| action | `vehicle_delete` | persistent id | deleted y/n |
| meta | `hello` / `ping` | – | mod version, protocol version, capabilities (so dzo can adapt per server) |

* **Transport (to be confirmed in spike S8):** the mod talks **outbound** to a small endpoint of `dzo` (network-mode aware: `127.0.0.1:<port>`
  with `network: host`; the host-mapped gateway address pasta provides, i.e. `host.containers.internal`, with `network: publish`; the render step writes
  the right URL into the mod config, verified in S5)
  via the engine's `RestApi`/`RestContext`. It pushes state and polls or long-polls pending actions, then posts the results.
  The per-instance token and endpoint are in `$profile:dzo-admin/config.json`, written by the render step (not in git).
  Fallback transport: a file spool in `$profile:dzo-admin/{in,out}/`. Inbound connections into the game process are never needed.
### Map marker API for other mods

`dzo-admin` is a **platform for map content**, not only for its own data. Other mods (UFO crashes, AI convoys, helicopter
crashes, camp fires, trader zones, events, …) must be able to put markers on the admin map with very little effort and
**without a hard dependency** on `dzo-admin`. They must still load and work when it is not installed.

**Marker model** (identical in the script API, the protocol and the web UI):

| Field | Meaning |
|---|---|
| `layer` | category, e.g. `ufo_crash`, `ai_convoy`, `campfire`. Each layer is a toggle in the map UI with its own default icon/colour |
| `id` | unique within the layer (the mod's own id, or the entity's persistent id) |
| `shape` | `point` (default), `circle` (radius m), `polygon`/`polyline` (zones, routes, convoy paths) |
| `position` / `points` | world coordinates (x, z; y optional) |
| `entity` | optional entity reference: `dzo-admin` then **follows the entity** automatically (position updates, auto-removal on delete), so a moving convoy needs a single call |
| `icon`, `color`, `label` | presentation; `icon` is a name from the icon registry (below) |
| `props` | free key/value map shown in the tooltip (e.g. loot tier, AI count, remaining time) |
| `ttl` | optional lifetime; markers expire automatically (crash sites that despawn) |
| `visibility` | `admin` (default) / `operator` / `viewer` role on the web UI |
| `actions` | optional list of operator-side actions offered on the marker (e.g. `teleport_here`, `delete_entity`), mapped to existing `dzo-admin` actions with the usual permission checks |

**Three integration paths, from easiest to most flexible:**

1. **Zero code: class watch rules** (configured per instance in the site repo, pushed to the mod via its config):
   ```yaml
   admin_map:
     watch:
       - {layer: campfire,  classes: ["FireplaceBase"], icon: fire, only_if: burning}
       - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: ufo, label: "UFO"}
       - {layer: ai_convoy, classes: ["eAIBase"], icon: ai, cluster: 50}
   ```
   `dzo-admin` tracks entities of these classes (incl. subclasses, glob on class names) and publishes them as markers.
   Which hooking technique is cheap enough without scanning the world (e.g. registration on entity init/delete of the relevant base
   classes vs. periodic scans with a budget) is part of spike S8. Rules carry a per-layer update interval and a max count.
2. **Script API with a soft dependency.** `dzo-admin` sets a global preprocessor define (e.g. `DZO_ADMIN`, via `CfgMods`
   `defines[]`), and a mod wraps its calls:
   ```c
   #ifdef DZO_ADMIN
   DZOAdmin_Map.Upsert("ufo_crash", m_CrashId, GetPosition(), "ufo", "UFO crash", props, 3600);
   DZOAdmin_Map.Track("ai_convoy", convoyLeader, "truck", "Convoy " + m_Name);   // follows the entity
   DZOAdmin_Map.Remove("ufo_crash", m_CrashId);
   #endif
   ```
   API: `Upsert`, `Track(entity)`, `Untrack`, `Remove`, `ClearLayer`, `DefineLayer(name, defaultIcon, color, visibility)`.
   Calls are cheap (they update an in-memory table; publishing is batched by `dzo-admin` at its push interval). The exact
   mechanism for the soft dependency is verified with the `dayz-dev` skill before the API is frozen. `#ifdef` on another mod's define is
   doubtful across independent PBOs without a config-level dependency, so the planned fallback is a **runtime lookup** (resolve the `DZOAdmin_Map`
   type by name and call it only if present, verified in S8). The **file drop** (3.) always works as the lowest common denominator, and the API is versioned (`DZOAdmin_Map.API_VERSION`).
3. **File drop (no script coupling at all):** a mod or external tool writes `$profile:dzo-admin/markers/<layer>.json`
   (same marker schema). `dzo-admin` (or `dzo` directly, since it can read the profile dir) picks it up. This suits tools and scripts outside the game,
   and mods that already write JSON.

**Icons:**
* Built-in icon set in `dzo` (SVG, embedded): player, vehicle types, fire, ufo, ai, loot, zone, event, generic pins.
* **Mods can ship their own icons:** SVG/PNG files at a convention path inside their PBO (e.g. `<prefix>/dzo_icons/<name>.svg`).
  `dzo` already reads PBOs (§C17), so it extracts them from the active mod generations and registers them as `<modname>:<name>`.
  There is no web asset deployment and no second download for players.
* Instance/site overrides: `site/instances/<name>/map_icons/` (and shared `site/map_icons/`) can add or replace icons and layer styles.

**Web UI and API:** layers appear automatically as toggleable overlays (grouped by the providing mod), with clustering for dense
layers, tooltips from `props`, and marker actions subject to roles. `GET /api/v1/instances/<name>/map/markers?layer=…`
(+ SSE stream) exposes the same data for other consumers. Marker counts per layer are exported to `/metrics`.

**Documentation deliverable:** `dzo-admin` ships a short integration guide + an example mod (a "marker demo" that places
a few static and tracked markers), so third-party mod authors can copy it.

### Active in-game events on the map

`dzo-admin` reports **which Central Economy events are currently active and where**, so they can be shown as a map layer
(and optionally sent to Discord, e.g. "Heli crash spawned near Novodmitrovsk").

**What counts as an event:** dynamic CE events from `events.xml` / `cfgeventspawns.xml`, e.g. helicopter crashes (`StaticHeliCrash`),
military/police convoys and wrecks (`StaticMilitaryConvoy`, `StaticPoliceCar`), contaminated areas (`StaticContaminatedArea`, dynamic toxic zones),
trains (HypeTrain), infected/animal hordes, plus modded events registered by mod integrations (Expansion, …).

**How it is detected (server-side only, verified in spike S8 with the `dayz-dev` skill before implementation):**
1. **Event → class mapping generated by dzo:** dzo already renders the effective CE files (vanilla + `mod_*`/`custom_*` folders, §C6). From
   `events.xml` it derives, per event, the **child types** (`<child type="Wreck_UH1Y" …>`) and event metadata (name, category, lifetime, `active`
   flag, position source), and from `cfgeventspawns.xml` the configured spawn positions. This mapping is pushed to `dzo-admin` in its config
   (`$profile:dzo-admin/events.json`), so no hand-written rules are needed and new mods' events work automatically.
2. **In the game:** `dzo-admin` registers the relevant classes (same efficient hooking as the class-watch rules of the marker API, §C16) and
   groups spawned entities into **event instances**: an entity of an event's child type near a configured event spawn position = one active event
   occurrence (id, event name, position, radius/extent, spawned_at, child entity count). Occurrences end when their entities are deleted or despawn
   (CE lifetime/cleanup), or for vehicles when a player has taken one over (moved beyond the spawn radius, then it is a normal vehicle again).
3. **Contaminated areas and other effect areas:** the mod reports `EffectArea` entities (dynamic and static) with position, radius and type directly, shown as circles.
4. **Where the engine exposes more** (e.g. script-accessible CE API calls that list active events, if they exist in 1.29), those are used and preferred.
   Otherwise the class/position correlation above applies. The S8 spike decides this, and nothing is assumed without checking the script sources.
5. **Modded events** that do not go through CE (Expansion missions, airdrops, AI patrols, …) publish themselves via the **marker API** (§C16) and
   appear in the same "Events" layer group.

**Protocol addition:**

| Kind | Name | Payload |
|---|---|---|
| state | `events` | pushed at a low interval (e.g. 30 s) + immediately on start/end: occurrence id, event name, category, position, shape (point/circle/polygon), child entity ids/classes, spawned_at, source (`ce` / `effect_area` / `marker_api`) |
| event | `event_started` / `event_ended` | occurrence id, event name, position, reason (despawn, looted/taken over, cleanup) |

**Operator side:**
* Map layer group **"Events"** with a toggle per event type, icons per category (built-in icons for vanilla events, mod-shipped icons for modded ones, §C16),
  tooltips (event name, age, remaining lifetime if known, child count), and actions like "teleport here" (permission-checked).
* `GET /api/v1/instances/<name>/events` (+ SSE), metrics `dzo_events_active{instance,event}`, and optional Discord notifications per event type
  (`notify.events: [StaticHeliCrash, StaticContaminatedArea]`, rate-limited).
* With ClickHouse: an `event_occurrences` table (`MergeTree`) → statistics (frequency and location heatmaps per event type, average lifetime, how fast
  crashes get looted), useful to tune `events.xml`/`cfgeventspawns.xml` in overlays.

### Operator side, security, metrics

* **Operator side:** the `admin` package (transport-agnostic): endpoint, command queue with timeouts and idempotency keys,
  an in-memory world state for the map (pushed to the browser via SSE), and results in the job/audit log. Every action is also available via
  API/CLI (`dzo player msg|tp|give`, `dzo vehicle list|repair|delete`).
* **Security:** these are admin powers. They need the `operator`/`admin` web roles, CSRF protection, and an audit log entry per action
  (who, what, which player/vehicle, result), plus optional Discord mirroring to an admin channel. Class names are validated against a
  per-instance allow/deny list, with rate limits per admin and per target. The endpoint accepts only the instance token, only on localhost.
* **Metrics:** state pushed by the mod also feeds `/metrics` (players online per instance, vehicles per class), because that data is there anyway.
  `dzo-admin` gets **no metrics collector of its own**: server/world metrics are MetricZ's job (§C23).
* **Map rendering:** Leaflet (from Debian's `libjs-leaflet`, no build chain) on tiles generated from the map's own `data.pbo`, see §C17.
* `dzo` works fully without the mod (RCon + systemd, no live map). Capabilities are discovered via `hello` and the UI hides what is unavailable.

## C17. Map tile pipeline (Q18)

The admin map (§C16) uses **tiles generated from the map's own terrain data PBO**. There are no third-party
map sites or tile sources.

**Source: exactly one configured PBO per map, no scanning.** The satellite layer of a terrain ships in a data PBO
(`layers/S_*_lco.paa`). dzo never searches mod or game directories for it (a modded server has gigabytes of PBOs, and guessing is fragile).
Where the file comes from depends on the map:

* **The dedicated server install is not a source.** Verified on 1.29: the server's `addons/worlds_chernarusplus_data.pbo` and
  `worlds_enoch_data.pbo` contain 1024 satellite tiles of **172 bytes each** (stripped placeholders, the whole PBO is 16–24 MB), and the
  server has no `worlds_sakhal_data.pbo` at all. The real files are in the **DayZ client** (app 221100): `Addons/worlds_chernarusplus_data.pbo`
  (282 MB), `Addons/worlds_enoch_data.pbo` (297 MB), `sakhal/Addons/worlds_sakhal_data.pbo` (199 MB), each with ~95–150 MB of real satellite tiles.
* **Vanilla maps** (built-in table, by mission world name: `chernarusplus`, `enoch` (Livonia), `sakhal`; the upcoming map is added once its file
  name is known): dzo **downloads only that one file from the client depot** with steamcmd, using the logged-in Steam account (§C7; the account owns DayZ anyway):
  ```
  +@sSteamCmdForcePlatformType windows
  +sDepotDownloadFileFilter "*worlds_chernarusplus_data.pbo"
  +download_depot 221100 <client depot id> [<manifest id>]
  ```
  (`sDepotDownloadFileFilter` is present in current `steamclient.so` builds and needs a self-updated steamcmd; anonymous `download_depot` is
  refused.) The file is stored in the cache as `${paths.cache}/mapsources/<map>/<sha256>.pbo`. That takes about 200–300 MB per map instead of ~20 GB for the whole client.
  Details (depot id, Sakhal DLC depot, one filter or one run per file, output path, latest manifest) are verified in spike S10.
* **If the automatic download fails** (Steam changes, no session, missing depot access, filter not supported): the admin **uploads the file manually**.
  This works in the web UI (upload form, permission `map.update`) or with `dzo map source set <map> <file>`, which copies the file into
  `${paths.data}/mapsources/<map>/<sha256>.pbo` and points `map_tiles.maps.<map>.source` at it. A plain path in the config
  (`source: /srv/dayz/mapsources/chernarusplus_data.pbo`) works too. A configured source always wins over the automatic download.
* **Modded maps (e.g. DeerIsle, Namalsk) require the configured path.** There is no automatic lookup. The admin provides the map mod's data PBO (e.g.
  DeerIsle's `data.pbo`, prefix `deerisle\data`) by upload or path. An instance whose map has no source simply has no map tiles; the map UI shows a
  hint instead of failing.
* **Validation on every source:** the PBO must contain `layers/S_*_lco.paa` tiles and matching `P_*.rvmat` files. Stripped placeholders
  (all S tiles tiny, e.g. the 172-byte server stubs) are rejected with "this is the stripped dedicated-server copy, use the client's PBO".

```yaml
map_tiles:
  auto_rebuild: true
  maps:
    chernarusplus: {}                                   # vanilla: automatic client-depot download
    enoch: {source: /srv/dayz/mapsources/enoch.pbo}     # vanilla, download failed → uploaded file
    deerisle: {source: /srv/dayz/mapsources/deerisle-data.pbo}   # modded: required
```

Structure, from the DeerIsle sample:

| Path in PBO | Content | Use |
|---|---|---|
| `layers/S_<x>_<y>_lco.paa` | satellite colour tiles, 512×512, DXT1, LZO-compressed (43×43 for DeerIsle) | **map imagery** |
| `layers/M_*_lca.paa`, `N_*_nohq.paa` | surface masks, normal maps | ignored |
| `layers/P_<x>-<y>_*.rvmat` | terrain materials. `Stage0` references the S tile, and `TexGen3.uvTransform` gives world→UV (`aside` = 1/512 per metre, `pos` = tile origin) | **exact georeferencing** of every tile (resolution, origin, overlap) |
| `map/*_co.paa` | in-game paper map images (e.g. 2048² `karta_co`) | optional low-zoom overview layer |

From the sample: scale 1/512 per m (1 m/px). Tile 21 has its origin at 8000 m, so the step is 384 px and the overlap
128 px (64 per side), and 43 × 384 m ≈ 16.5 km. The generator **derives these values from the rvmats per map** (no hard-coded
overlap). It validates them for consistency across all tiles and fails loudly on unexpected layouts.

**Pipeline** (`dzo map tiles build <instance|map>`, pure Go, no external tools):

1. Read the PBO directly (WoozyMasta `pbo`, MIT), without unpacking to disk. Collect the S tiles + rvmats.
2. Decode PAA (WoozyMasta `paa`, MIT: DXT1/DXT5, LZO/LZSS) → RGBA.
3. Crop each tile's overlap according to its UV transform and place it in a *virtual* mosaic (world metres ↔ pixels).
   The mosaic is never materialised: a 16.5k² RGBA image would be ~1 GB.
4. Generate the **max-zoom output tiles** (256×256) directly from the (at most 4) overlapping source tiles. Build the lower
   zooms by 2×2 downsampling of the level above (streaming, bounded memory, parallel per tile row).
5. Encode as **PNG** (default; WebP optional) with the layout `{z}/{x}/{y}.png`. Also write `metadata.json`: map name, world
   size (m), metres per pixel at max zoom, min/max zoom, tile size, axis orientation (DayZ `x`/`z` → image x/y, north up),
   source PBO + hash, and generator version.
6. Optional overview layer from `map/*_co.paa` (resized) for the lowest zooms, or as an alternative "paper map" style.

**Cache, invalidation and on-request updates:**
* Output goes to `${paths.cache}/maptiles/<map>/<source-hash>/`. The hash covers the source PBO content (+ generator version), so the same map is
  **extracted only once** and shared by all instances using it. Unchanged maps are never re-extracted, not even after a dzo restart or reinstall of an instance.
* An **index** (DB) per map: source (client-depot download with manifest id, or uploaded/configured path), hash, generator version, built_at, size, state
  (`building` / `ready` / `failed` / `stale`), and which tile set is currently served.
* **Automatic rebuild** (`map_tiles.auto_rebuild: true`, default): when the source hash changes, a background build is queued. For vanilla maps,
  the client depot file is re-checked after every manual `dzo product update` (a new game version may change the map) and downloaded again if its manifest
  changed. For uploaded or configured files, a new upload or a changed file is picked up. The old tile set keeps being served until the new one is `ready`, then it switches atomically.
* **On request:** `dzo map tiles status [<map>]` shows the source (download or path), hash, age and whether the source differs (`stale`).
  `dzo map source update <map>` retries the client-depot download. `dzo map tiles update <map|instance>` rebuilds from the **current source file**, and `--force` rebuilds even with
  an identical hash (e.g. after a generator fix). The web UI offers the same as an "Update map from game files" button (permission `map.update`).
  With `auto_rebuild: false`, stale maps are only reported.
* Builds run as jobs with low CPU/IO priority (`nice`/`ionice`, systemd `CPUWeight`), one at a time, with progress in the UI, so running game servers are not affected.
* Old tile sets are garbage-collected when no longer referenced (keep the previous one for quick rollback, `dzo map tiles use <map> <hash>`).

**Serving and sharing:**
* `dzo serve` serves `/tiles/<map>/<source-hash>/{z}/{x}/{y}.png` with `Cache-Control: immutable` (the hash is in the path), and
  `/tiles/<map>/metadata.json` with a short max-age + `ETag`, pointing to the current hash. A map update is simply a new hash in `metadata.json`.
  `tiles.public_base_url` uses the same `<map>/<source-hash>/…` layout, and `dzo map tiles export` writes it that way.
* The tile set is a plain XYZ directory, so **our own map tile server** (or any static web server/CDN) can serve it
  directly. `dzo map tiles export <map> <dir|rsync-target>` publishes it, and `tiles.public_base_url` in the config makes the
  web UI use that server instead of the built-in route.
* Access control: tiles are derived from the map authors' assets, so they are served only to authenticated users by
  default. Publishing them publicly is an explicit config decision per map.

**Map UI:** Leaflet with `CRS.Simple`, bounds and zoom from `metadata.json`. DayZ world coordinates (as reported by `dzo-admin`, `x`/`z` in metres)
map linearly to pixels, and clicking the map returns world coordinates for teleports (`y` is resolved server-side from the surface).

## C18. Players, moderation and scheduling (web admin)

### Identity: always the Steam ID

* **The primary key of a player is the SteamID64.** Names are attributes with history, never identifiers.
* Sources of identity:
  * **`dzo-admin`** (preferred): on connect it reports SteamID64, name, BE GUID, IP and the server slot. This is the only reliable
    Steam ID source.
  * **BattlEye RCon** (fallback, no mod): connect/disconnect/GUID-verified messages and `players` only contain
    **BE GUID** + IP + name + slot. The BE GUID is an MD5 hash of the SteamID64 and cannot be reversed. dzo stores such sessions under the GUID and **links
    them to the Steam ID** as soon as it is known (the mod reports it, or an admin enters it: dzo computes GUID(steamid) with `dzid` and matches it).
  * So on servers without `dzo-admin`, per-player features degrade gracefully (GUID-based), and the UI shows it.

### Data model (relational, SQLite/PostgreSQL)

| Table | Content |
|---|---|
| `players` | steam_id (PK), be_guid (derived, indexed), first_seen, last_seen, total_playtime, current display name, notes count, flags (watchlist, trusted) |
| `player_names` | steam_id, name, first_seen, last_seen (alias history) |
| `player_ips` | steam_id, ip, country, asn/org (optional), first_seen, last_seen (**PII**, retention-limited, `pii.view` permission) |
| `player_server_stats` | steam_id, instance, first_login, last_login, playtime_seconds, sessions_count |
| `sessions` | id, steam_id (nullable until linked), be_guid, instance, connected_at, disconnected_at, ip, country, name, disconnect reason (quit/kick/ban/crash/restart) |
| `steam_profiles` | steam_id, persona name, avatar, profile URL, visibility, account created, country (profile), level, **VAC banned, number of VAC bans, game bans, community banned, economy ban, days since last ban**, fetched_at |
| `bans` | id, target (steam_id / be_guid / ip / **country** / **asn**), scope (**global** or list of instances), reason, public message, created_by, created_at, **expires_at** (temp bans), revoked_by/at, evidence/notes |
| `kicks` | steam_id, instance, by, reason, at (history) |
| `player_notes` | steam_id, author, text, created_at (moderator notes) |
| `findings`, `finding_players` | logz-analyzer cheat-detection findings per player (steam_id + be_guid), long-term, full-text searchable (optional, §C23) |
| `watchlist` | steam_id, reason, notify targets (alert when a player joins any server) |
| `schedules` | id, kind (`restart_once`, `restart_recurring`, `broadcast`), instances, spec (time / interval / calendar), payload (countdown params, message text), enabled, created_by, next_run, last_run |
| `item_types` | instance, class name, display name, category, source mod, scope, from (`game` / `types.xml`), CE data (nominal, lifetime, usage/value tags) |
| `item_presets` | name, list of (class, qty, attachments) "kits", scope per instance |
| `audit_log` | who, when, action, target, instance, parameters, result, source (web/api/cli/timer) |
| `jobs`, `users`, `roles`, `role_bindings`, `api_tokens` | as in §C11/§C12 |

* Access layer: `database/sql` with **pgx** (PostgreSQL) and **modernc.org/sqlite** (pure Go, no CGO). SQL is kept to the common
  subset, with thin dialect helpers (upsert, time functions). Queries are generated type-safe with **sqlc** for both engines.
  Migrations with **goose** (versioned, embedded, run on startup with a lock). The **CI test matrix runs the DB test suite against both**
  SQLite and PostgreSQL (a service container in GitHub Actions and GitLab), and this counts toward the 85 % coverage.
* High-volume history (positions, full session log, chat, population) goes to **ClickHouse if configured (§C19)**. Otherwise the relational DB keeps only a short retention of it.
* SQLite runs in WAL mode with a single writer goroutine. `dzo db migrate-to-postgres` copies everything for a later switch.
* DB backups: `VACUUM INTO` (SQLite) / `pg_dump` into `${paths.snapshots}/_db/`, before migrations and daily, with retention (§C20).

### Player list and player page

* **Per server**: online players (live from `dzo-admin` state / RCon `players`) with name, Steam ID, country flag, ping, playtime
  on this server, session length, VAC/game-ban badge, watchlist/notes indicators, and actions (message, kick, ban, teleport, spawn items,
  show on map). **Global view**: all online players across servers + search over all known players (steam id, name history, GUID, IP for `pii.view`).
* **Player page**: Steam profile (avatar, link, account age, visibility, VAC/game bans with days since last ban), first/last seen,
  per-server stats (first login, last login, playtime), name history, session history, country history (IPs only with `pii.view`),
  kicks/bans history, notes, audit trail of admin actions against this player, current position on the map (if online), a link to Grafana/Loki
  filtered by steam id (LogZ events), **with LogZ active:** kill feed and combat stats, **with logz-analyzer:** a behaviour tab with the cheat-detection findings (§C23),
  and possible alt accounts (shared IPs, shown as a hint only).

### Steam player info

* Steam Web API (key in `secrets/`): `ISteamUser/GetPlayerSummaries` (profile) and `ISteamUser/GetPlayerBans` (VAC, game,
  community, economy bans, days since last ban), batched (100 ids per call), cached in `steam_profiles` (refresh on join if older than N hours,
  plus a daily refresh for recently active players), within the rate limits.
* Optional **join policies** per instance (evaluated on connect, action = kick with message + audit + optional Discord):
  VAC/game ban within the last N days, private profile, account younger than N days. Each can be off, log only, or enforced.
* A new VAC ban on a known player → Discord notification.

### GeoIP

* A MaxMind GeoLite2 Country (and optionally ASN) database, kept up to date by Debian's `geoipupdate` (contrib) on its own systemd timer
  (license key in config; legacy servers already mount `/var/lib/GeoIP`), or DB-IP lite as a licence alternative.
  dzo reads the `.mmdb` directly (Go `maxminddb` reader) and reloads it on file change without restarting.
* The country is shown everywhere a player appears and is stored per session. The IP address itself is PII (see privacy below).

### Moderation: kick and ban

* **Kick** (per server): RCon `kick <slot> <reason>` (works without the mod), or via `dzo-admin` with a nicer message.
* **Ban** targets: Steam ID (primary), BE GUID, IP / CIDR, **country (GeoIP)**, ASN (e.g. VPN/hosting providers).
  **Scope: one, several or all servers.** Permanent or **temporary** (expires automatically), with reason, public message and notes.
* **Enforcement (defence in depth):**
  1. **On connect**, dzo checks every joining player (from `dzo-admin` or RCon connect/GUID messages) against the ban table for
     that instance → immediate kick with the ban message (+ reason/expiry). This covers all target types incl. country/ASN and temp bans.
  2. For Steam ID/GUID bans, dzo also **renders** them into the native lists at every render/ban change: DayZ `ban.txt` in the profile
     dir (format, Steam64 vs. other ids, to be verified in spike S4 before implementing) and BattlEye `bans.txt` (GUIDs; reloaded via RCon `loadBans`), so bans hold even if dzo is down.
     These files are fully generated; manual entries are imported once and then managed in the DB.
  3. The country/ASN bans have an **allow-list** override (specific Steam IDs may still join).
* Unban, ban edit and ban history are all audited. A ban list per server shows active/expired bans and filters.
* A **whitelist mode** per server (only listed Steam IDs / groups may join) uses the same mechanism (useful for events and test servers).

### Remote restarts and one-off restart timers

* "Restart now" (graceful countdown with configurable minutes/lock/delay/message, or immediate) from the UI/API → the same
  `dzo restart` job as §C8, with audit.
* **One-off restart timer**: "restart deerisle at 18:30 with 15 min countdown and message 'Update'" → a row in `schedules`.
  Following the single scheduling rule (§C5), `dzo` materialises it as a **transient systemd timer**; the daemon never fires it itself.
  No cron (D19). Regular restart timers from the site config (§C5) are shown in the same UI, read-only (or edited via site repo commits).
* Option "**restart when empty**": if no player is online, restart immediately; otherwise fall back to the countdown or the next slot.
* Cancel/postpone a pending restart (sends `#unlock` + an announcement if the countdown already started).

### Broadcast messages

* **Scheduled broadcasts** per server or global: text (with placeholders like `{next_restart_in}`, `{players}`, `{server}`,
  `{discord}`), schedule (every N minutes, at times, during a window, before restarts), channel (RCon `say -1` = global chat; with
  `dzo-admin`: on-screen notification style), enabled flag. Rotation of several messages in a group.
* One-off broadcasts from the UI ("send now" to one/several/all servers) and **direct messages** to single players.
* Persisted in `schedules` and executed via systemd timers under the single scheduling rule (§C5), so they also keep working without the daemon. Complements the static `messages.xml`.

### Item spawning: the list of available types

* The **authoritative list comes from the game**: `dzo-admin` enumerates spawnable classes on startup (config classes with public
  scope from `CfgVehicles`/`CfgWeapons`/`CfgMagazines`/`CfgAmmo` items, incl. all loaded mods) and sends it with a hash. dzo stores it per instance and product build.
* **Enriched from the CE files** dzo already renders: the effective `types.xml` set (vanilla + every `mod_*`/`custom_*` CE folder, parsed with
  `dzce`) gives category, usage/value tags and nominal/lifetime, so the picker can filter "weapons/tier 4/military".
* Without `dzo-admin`: the list comes from the parsed CE files only (spawning itself needs the mod anyway).
* UI: searchable picker (class name, display name, category, source mod), quantity/health, "with attachments" (from `cfgspawnabletypes`
  presets where available), **item kits/presets** (e.g. "starter kit", "event reward"), target (player inventory / hands / ground / map position).
  Permissions + an allow/deny list per instance (§C16), and every spawn is audited.

### Also included (suggested additions)

* **Chat log** per server (from `dzo-admin` or RCon chat messages) with search. Reply to a player (direct message) from the chat view.
* **Watchlist**: flag a player and get a Discord alert when they join any server.
* **Moderator notes** and **evidence links** on players and bans; ban templates (predefined reasons/durations).
* **Server population statistics**: players over time and peak hours (from sessions; Grafana dashboards for the details via Prometheus).
* **Priority/reserved slots** (list of Steam IDs that bypass the queue, if supported by the server config) managed like the whitelist.
* **Privacy/GDPR**: IPs stored only with a configurable retention (e.g. 90 days, then only country is kept), `pii.view` permission,
  per-player data export and erasure (`dzo player forget <steamid>`, except active bans), documented in a privacy note for your community.
* **API tokens** with scoped permissions for bots (e.g. a Discord bot with `!players` or `!restart deerisle`).
* **Rate limits and safety confirmations** for destructive/global actions (global ban, restart all servers, country ban).
* **Multi-server actions**: kick/ban/message across all servers at once, "broadcast to all".

## C19. Optional analytics store: ClickHouse

**Split of responsibilities:**
* **SQLite/PostgreSQL** (§C18) stays the **source of truth for mutable, transactional state**: users, roles, bans, schedules, notes,
  watchlist, item presets, jobs, and the *current* player aggregates. dzo must run fully without ClickHouse.
* **ClickHouse** (optional, `analytics.clickhouse` in `/etc/dzo/config.yaml`) stores **high-volume, append-only history** and the
  analytics on top of it. With it enabled, features become available that are too heavy for SQLite. Without it, those features are hidden or reduced
  to short retention in the relational DB.

**Tables and engines:**

| Table | Engine | Content / purpose |
|---|---|---|
| `positions` | `MergeTree`, `PARTITION BY toYYYYMM(ts)`, `ORDER BY (instance, steam_id, ts)`, **TTL** (e.g. 90 d) | player positions from `dzo-admin` state pushes (every ~5 s): movement **replay on the map**, **heatmaps**, "who was near X at time T" for moderation |
| `vehicle_positions` | `MergeTree` + TTL | vehicle tracks (lost/stolen vehicle investigations) |
| `sessions_log` | `MergeTree` | every connect/disconnect with GUID, steam_id, country, ASN; **column TTL on `ip`** (e.g. `ip … TTL ts + INTERVAL 90 DAY`, reset to default): IPs are removed automatically while country/ASN stay (privacy by the engine) |
| `player_state_latest` | `ReplacingMergeTree(version)` `ORDER BY (instance, steam_id)` | last known state per player (position, health, online) for fast lookups and "last seen at" on the map |
| `playtime_daily` | `AggregatingMergeTree` fed by a **materialized view** from `sessions_log` | playtime per player/server/day, first/last login (`minState`/`maxState`), unique players per day |
| `population` | `MergeTree` + MV into `AggregatingMergeTree` (`avgState`, `maxState` per 5 min/hour) | players online over time, peak hours per server |
| `chat_log` | `MergeTree` + TTL, token/ngram bloom-filter skip index on text | chat history search |
| `admin_events` | `MergeTree` (no TTL, or long) | append-only copy of the audit log (spawns, teleports, kicks, bans, restarts) for long-term analysis |
| `item_spawns` | `MergeTree` | who spawned what for whom (abuse detection, event statistics) |
| `event_occurrences` | `MergeTree` + TTL | active in-game events (start/end, position, type) → frequency/location heatmaps, lifetime statistics (§C16) |
| `alt_accounts` (view) | query over `sessions_log` | Steam IDs sharing IPs/ASNs in a time window (hints only, `pii.view`) |

* Other engines where they help: `Dictionary` (GeoIP/ASN lookups inside ClickHouse, if desired), `Buffer` or **async inserts**
  for the 5 s position stream, and `Distributed`/`ReplicatedMergeTree` if the ClickHouse side is ever clustered (that is the stack's business, dzo only needs a DSN).
* Retention is expressed as **table/column TTLs in the migrations**, configurable per table (`analytics.retention.positions: 90d`).
  Player erasure (§C18, GDPR) runs `ALTER TABLE … DELETE WHERE steam_id = …` (lightweight deletes) across all tables.
* Writer: `clickhouse-go` v2 (Apache-2.0), with batched inserts from an in-process buffer (flush every N rows / N seconds). On outage it buffers
  on disk (bounded) and retries. ClickHouse problems never block game or admin actions.
* Migrations: separate goose migration set for ClickHouse (`internal/db/clickhouse/`), with the database/cluster name configurable.
* **Relation to gigapipe:** that stack already runs on ClickHouse (Loki/Prometheus-compatible data incl. LogZ events). dzo
  can use the **same ClickHouse server with its own database**. dzo does not depend on LogZ data (Q19/Q20). dzo stores nothing from LogZ except the
  analyzer findings (§C23). LogZ events live in Loki/gigapipe only. Cross-joins with them (e.g. kills near a position) are an optional analysis feature via Grafana/SQL.
* Web features unlocked with ClickHouse: movement replay and heatmap layers on the admin map, population and playtime charts, "players near
  position/time" search, chat search over long periods, alt-account hints, spawn/admin-action statistics.
* Tests: the ClickHouse integration test job uses the official ClickHouse service container in both CIs. The analytics writer and query code count toward
  the 85 % coverage (unit tests use an interface fake, and integration tests cover the SQL).

## C20. Backups: btrfs snapshots (D31)

Backups are part of dzo and are built on **one mechanism: btrfs snapshots of per-instance subvolumes**. There are no alternative backends, and
that is what keeps it simple.

### Requirement: instances on btrfs subvolumes

* The configured `paths.instances` and `paths.snapshots` (§C2; any location, e.g. `/srv/dayz/instances`) **must be on the same btrfs filesystem**. `dzo setup` and every daemon/CLI start check it
  (`statfs` → `BTRFS_SUPER_MAGIC`) and refuse to manage instances otherwise, with a clear message. For hosts without a btrfs data volume,
  the docs describe adding one (a dedicated partition/LV, or a loopback image file as a stop-gap).
* **Every instance directory is a subvolume**, created by dzo as the unprivileged `dayz` user via the `BTRFS_IOC_SUBVOL_CREATE` ioctl (no root, no
  helper; verified on kernel 7.1, re-checked on trixie's 6.12 in S7).
* Files written by the game container are owned by `dayz` (the rootless container runs as root in the user namespace = the `dayz` uid on the host), so
  snapshotting, restoring and deleting never need `podman unshare` or root.
* Recommended mount option: **`user_subvol_rm_allowed`** (instant snapshot deletion by the owner). It is not required, see "Deletion" below. `dzo setup`
  reports whether it is set.

### What gets snapshotted

* A snapshot is a **read-only snapshot of the whole instance subvolume** (`servermpmissions/`, live `mpmissions/` incl. `storage_*` and mod data,
  `profiles/`, `runtime/`, manifest, `filehistory/`), stored at `${paths.snapshots}/<instance>/<timestamp>-<reason>`.
  It is instant and space-efficient (copy-on-write).
* The **database** is backed up separately: SQLite via `VACUUM INTO` (consistent online copy) into `${paths.snapshots}/_db/<timestamp>.sqlite`,
  PostgreSQL via `pg_dump` (if the local server is used). This runs before DB migrations and daily, with the same retention mechanism.
  ClickHouse is not backed up by dzo (it is analytics data, and the stack owner handles it).
* Download cache generations are immutable and not snapshotted (they can be downloaded again; §C7 forced refresh).

### When snapshots are taken

| Trigger | When | Default |
|---|---|---|
| `update` | before `dzo instance upgrade` switches an instance to a new product build (manual) | on |
| `mod_update` | before new mod generations are applied, **incl. the automatic hourly update pipeline** | on |
| `mission_update` | before pristine mission changes are applied | on |
| `config_change` | before site repo changes (instance.yaml, overlays, serverDZ.cfg) are applied | on |
| `render` | before every render/apply, i.e. every start incl. crash restarts | off |
| `scheduled` | periodic snapshots of a running server (`dzo-backup-<name>.timer`, e.g. every 6 h; crash-consistent, like a power cut) | off |
| `manual` | `dzo backup create <name>` / web UI | – |
| `destructive` | before `mission reinit`, `mission prune`, `wipe`, restore | **always, cannot be disabled** |

For triggers that come with a restart, the restart job runs **stop → snapshot → apply → start**. The snapshot sees a stopped server and takes
milliseconds, so it adds no noticeable downtime. If a snapshot fails, the policy is `abort` by default: nothing is applied, the server starts on the old generation,
and an alert is sent. `warn` is available per trigger. The update pipeline continues with other instances.

### Retention and cleanup

* **Settings** (defaults in `site.yaml`, override per instance):
  ```yaml
  backup:
    before: [update, mod_update, mission_update, config_change]
    schedule: null                    # e.g. "*-*-* 00/6:00" for periodic snapshots
    keep: 20                          # total per instance
    keep_by_reason: {mod_update: 8, update: 10, scheduled: 12}
    max_age: 30d                      # optional
    min_keep: 3                       # never go below, regardless of age
    min_free_bytes: 50GiB             # before snapshotting, prune the oldest to keep this free; never below min_keep/pinned
  ```
* **Pinned** snapshots (`dzo backup pin <id>`) and the snapshots referenced by an in-progress restore are never pruned automatically.
* **Index as source of truth:** each snapshot is recorded in the DB (id, instance, reason, created_at, path, state
  `creating` → `complete` | `failed` → `deleting` → `deleted`, pinned). Pruning only acts on indexed entries and verifies that the path is a direct child of
  `${paths.snapshots}/<instance>/`. It never globs, never follows symlinks, and never touches instance subvolumes.
* **When cleanup runs:** after every successful snapshot (only then, so a failure never reduces the number of good snapshots), via the daily
  `dzo-backup-prune.timer`, and on demand with `dzo backup prune [<name>] [--dry-run]`.
* **Deletion (unprivileged-safe):** try `BTRFS_IOC_SNAP_DESTROY` (instant, allowed with `user_subvol_rm_allowed`). On EPERM, clear the read-only flag
  (allowed for the owner) and remove the tree recursively, then `rmdir` the empty subvolume (allowed for the owner). This path was verified
  unprivileged; it is slower (proportional to the file count) but needs no rights. Deletions are idempotent and resumable (`deleting` state). Failures are
  alerted (metrics, Discord, Icinga).
* **Leftovers:** interrupted `creating` entries are cleaned up. Directories in the snapshot root that are not in the index are **reported, not deleted**
  (`--adopt-orphans` / `--delete-orphans` to act). Missing snapshots are marked `missing`.
* Removing an instance asks whether its snapshots are kept (default) or deleted.

### Restore

* **Full restore** `dzo restore <name> <id>`: stop the instance → take a safety snapshot (`pre_restore`) → rename the live subvolume aside
  (`.<name>.replaced-<ts>`, same filesystem, allowed for the owner) → create a **writable snapshot** of the backup at the instance path (instant) →
  start. The replaced subvolume is kept as a snapshot entry (reason `replaced`) and follows retention.
* **Partial restore** `dzo restore <name> <id> --path mpmissions/<map>/storage_1` (or a mod's data dir): `cp -a --reflink=always` from the
  read-only snapshot into the stopped instance, after a safety snapshot. Useful to roll back only player data or only one mod's files.
* **Browse:** snapshots are plain read-only directories, so `dzo backup diff <id> [<id2>|live]` shows changed files, and the web UI offers a file browser/download.
* Every restore is audited and notified.

### Offsite copies (optional)

* dzo does not ship data offsite itself. An optional **`post_backup` hook** receives the path of the new read-only snapshot (a consistent
  source), e.g. to run `restic backup` or `borg create` (Debian packages) on it. Its failure is reported but does not affect the local snapshot.

### Visibility and tests

* `dzo backup list [<name>]` (size estimate, reason, pinned, expiry per retention), metrics `dzo_backup_count{instance}`,
  `dzo_backup_last_success_timestamp{instance,reason}`, `dzo_backup_failures_total`, `dzo_backup_prune_failures_total`, and a web UI page.
* Retention selection is a pure function covered by property tests: never below `min_keep`, never pinned, only inside the root, newest always kept.
  The snapshot/delete/restore paths are integration-tested on a **loopback btrfs** filesystem in CI (mounted once with root in the job setup, then
  tests run unprivileged, both with and without `user_subvol_rm_allowed`).

The per-render copies of changed/drifted managed mission files (§C6) remain as a fine-grained complement ("warn, backup, overwrite" per file).

## C21. Fresh start: example site config from the legacy repositories (D32)

**No data is imported from running servers.** The new system starts fresh: new live missions initialised from pristine (a full world
wipe), empty profiles, no player/ban/session history, and no copies of legacy volumes. The legacy servers are only used as **configuration
input**, and only from their **git repositories** (branches), never from podman volumes or a host.

**Deliverable: an example site config** (`examples/site/` in this repo, later copied into the real site repo), generated once by a converter
and then **reviewed and edited by hand**. It is not kept in sync with the legacy branches.

**Converter:** `dzo legacy convert-config --repo <dayzdockerserver checkout> --ref <branch>… --out <site dir>`
* Reads everything via `git show <ref>:<path>` (no checkout, no volumes, no network except optional URL fetches). It is idempotent and has `--dry-run`.
* Produces per branch/instance, plus shared parts:

| Legacy source (per branch) | Site config result |
|---|---|
| `files/serverDZ.cfg` | `instances/<name>/serverDZ.cfg` (dzo-enforced keys such as ports/`template`/`instanceId` are moved to `instance.yaml` and marked) |
| `config/containers/server.json` | `ports`, `container.mounts` (GeoIP, LogZ dir), `container.env` in `instance.yaml` |
| `dzpodman` (`--network=host`, stop timeout, health settings) | `network`, `health`, `restart_limit` defaults |
| `server/bin/dz` `parameters=` (`-cpuCount`, `-limitFPS`, `-netlog`, …) | `params` |
| `template=` in serverDZ.cfg + `files/mods/<id>/map.env` (or `default`) | `map`, `mission_source` (git repo + path) |
| `files/mods/@<Name>` → `<id>` symlinks | **candidate** `mods:` list with ids and names (the repo has no record of which mods were *active*, which was only in the volumes, so every entry is marked `# review: active?`) |
| `files/servermods` | `server: true` on matching mods (exact name match, reported when ambiguous) |
| `files/mods/<id>/xml.env`, `map.env`, `*.xml/json`, `init.c.<map>`, `start.sh` | `integrations/mods/<id>/integration.yaml` + files + hooks. Identical files across branches become **shared** integrations, and the ~19 divergent ones become **per-instance overrides** |
| `files/custom/<dir>` | `instances/<name>/overlays/<dir>`. Overlays identical across branches (e.g. `login-times`) are offered as shared `overlays/` |
| `files/custom-disabled`, `files/disabled`, `editor_files_disabled` | **not converted**, listed in the report |
| `files/messages.xml` | `instances/<name>/messages.xml` |
| `files/bin/pre_start.sh` (traderstocks, weather) | `hooks.pre_start` entries + the helper scripts under `hooks/` (Debian python3 only) |
| legacy quirks (buggy `init.c` patching, substring servermod matching, the missing newline in `xml.env`, …) | fixed in the output and listed in the report |

* **Not converted** (no reliable source in the repos, and intentionally not carried over): active mod set, mod load/activation order,
  restart times and update checks (cron on the host), RCon passwords, Steam credentials, live state of any kind. The instance gets the site
  defaults (`restarts.schedule`, `updates`) with `# review` markers.
* Output: the site tree + `CONVERSION_REPORT.md` (per instance: what was converted, what needs a decision, what was dropped and why, detected
  conflicts between branches). The generated `instance.yaml` files validate against the schema (`dzo site validate`), so the example is usable as-is after review.
* Ports are kept from the legacy branches by default, so the new servers can run in parallel on other ports (`--port-offset`) during testing.

**Rollout per server (fresh start):** announce the wipe to the community → create the instance from the reviewed example config → `dzo product install` /
`mod add` → `dzo mission init` (fresh world from pristine) → run in parallel on test ports → switch to the production ports and stop the legacy
server → retire the legacy user and units once stable. The legacy volumes are left untouched; the new system never reads them.

## C22. Boot test: does the server start with what we rendered? (D33)

Golden tests prove that the renderer produces the expected bytes. Only the game can say whether it **accepts** them. Static checks
(XML/JSON parse, `dzce`, dependency validation from `requiredAddons`) miss a whole class of errors: script compile errors, `modded` classes the
engine rejects, overrides of `proto native` methods, wrong override signatures, mods whose scripts are silently not loaded, and CE files the
engine refuses at runtime. The `dayz-dev` skill (`testing/local-server.md`, verified with the native Linux `DayZServer` 1.29) shows that a
headless dedicated server answers these questions in under a minute, without a client or players. `dzo test boot` builds that procedure
into dzo.

**Development only.** A real DayZ server is only started for development: on a developer machine, or on a dedicated test runner
without game servers. It **never runs next to live servers** and is not part of the update, restart or render path on a production host.
dzo enforces this: `dzo test boot` refuses to run on a host where dzo manages instances (instance directories configured under `paths.instances`
or any `dzo-*` instance unit present for the user), and there is no override flag. Production safety stays with the static checks, the
pre-flight render and the failure gates of §C5.

### What is booted

The boot test feeds the **exact output of the render pipeline** (§C6 staging, before apply) into a **disposable server tree**. The live mission is never used or touched.

| In the tree | Source |
|---|---|
| `DayZServer`, `addons/`, `dta/`, `sakhal/`, … | one symlink per top-level entry of the server install (read-only) |
| `serverDZ.cfg` | the rendered `runtime/serverDZ.cfg`, rewritten for the test: test ports, `hostname` prefixed with `[dzo boot test]`, random `password` so nobody can join |
| `mpmissions/<template>/` | a **real copy** of the staging mission (the rendered files, not symlinks); empty storage = fresh world, which also exercises first-start init |
| `keys/`, `battleye/` | real dirs: the rendered keys; BE cfg with a random RCon password and a test RCon port |
| `@<Name>` | symlinks to the mod generations of the render (dzo's workshop cache and local servermod generations) |
| `profiles/` | empty, used as `-profiles=profiles` |

Where the server files and mods come from:

* `--server steam` (default): the **"DayZ Server" tool installed by the Steam client** (app 223350;
  experimental 1042420). dzo locates it like the skill's `find-dayzserver.sh`: read `steamapps/libraryfolders.vdf` of every known Steam
  root (native, Debian, Flatpak, Snap, `$STEAM_ROOT`), find `appmanifest_<appid>.acf`, and trust the manifest (`installdir`, `buildid`),
  not the `"apps"` list. `StateFlags` ≠ 4 (still updating) aborts with a clear message. If the server is not installed, dzo does not install it; it prints
  the install instructions from the skill (Steam Library → Tools → "DayZ Server", or `steam steam://install/223350`).
* `--server <dir>`: an existing server install, e.g. a separate steamcmd install the developer may break freely, or the pre-installed server of a test runner.
* `--mods-from steam-client` (default with `--server steam`): mods from the client's workshop dir (`steamapps/workshop/content/221100/<id>`). A missing mod is reported with its id
  and name ("subscribe in the Steam Workshop and start the launcher once"). dzo never downloads into Steam directories.
  `--mods-from cache` uses a local dzo development cache, `--mods-from <dir>` a directory of `@Mod` folders (e.g. a fresh `dzo-admin` build).
* **Nothing is ever written into a Steam directory or a cache generation.** The tree is the only thing written to. It is created under
  `$XDG_CACHE_HOME/dzo/boottest/<run>/`, and removed afterwards unless `--keep-tree` is set.

### How it runs (rules from the skill)

* **Mod paths are relative to the tree** (`-mod=@A;@B`, `-servermod=@dzo-admin`). Absolute paths are **silently ignored** by the engine: it
  probes the folder but loads no PBO and prints no error. This rule applies to production too: the quadlet mounts mods as `/dayz/@<Name>` and passes
  relative paths (§C5). The args builder has a unit test that rejects absolute mod paths.
* Working directory = the tree (the engine resolves `addons/` and `mpmissions/` from it). `-config` and `-profiles` may be absolute.
* **Core dumps off** (`RLIMIT_CORE=0` for the child; `--ulimit core=0` in the container). A crashing server writes up to ~5 GB of core otherwise.
* **Free ports** are picked for game, query (`steamQueryPort`) and RCon, never 2302/27016 or any port of a configured instance.
* **Stop:** SIGTERM, then SIGKILL after 15 s. After a clean shutdown (`Termination successfully completed` in the RPT) the process can hang
  forever in Steam API threads. dzo tracks the child by PID and process group. It never matches by name: the process appears as `enfMain`, not `DayZServer`.
* Two execution modes:
  * `--native` (default): runs the Linux `DayZServer` directly, fastest.
  * `--container`: the **same runtime image and mount layout as the instance quadlet** (ro server root, nested mission/keys/mod mounts),
    via `podman run --rm` with an isolated network namespace (pasta, nothing published). This also tests the production layout (S1/S2) on the
    developer machine. The A2S probe runs inside the container (`podman exec … dzo health startup`).

### Readiness and checks

1. **Ready:** the A2S probe (same code as `HealthStartupCmd`, §C9) answers on the test query port, with a timeout of `boot_test.timeout`
   (default 180 s; tuned with the load times from S1). Then dzo waits `boot_test.settle` (default 15 s) for init scripts and the `dzo-admin`
   first contact, and stops the server.
2. The logs in `profiles/` are evaluated (`script_*.log`, `*.RPT`, `error.log`, `*.mdmp`):

| Check | Result |
|---|---|
| Process exited or crashed before ready, `.mdmp` present, ready timeout | **fail** (tails of RPT/script log/error.log in the report) |
| `SCRIPT\s+\(E\)` lines in `script_*.log`, i.e. compile errors (the tag is space-padded, so the literal `SCRIPT (E)` never matches) | **fail**, with file:line and the owning mod |
| Script modules not loaded: `Module: <Game\|World\|Mission>; loaded N files` compared to the **vanilla baseline** of the same product build. If a mod ships scripts for a module but the count did not rise, its scripts were not loaded. Common silent causes are an absolute mod path or a PBO without the `prefix` header that `config.cpp` script paths expect. | **fail** for "did not rise"; a count that differs from baseline + the mod's `.c` files (counted from its PBOs with the pbo library) is a **warning** until S9 proves the formula |
| Engine/CE/mission error patterns in RPT/`error.log` (XML parse errors in CE files, unknown class names in `types.xml`, broken `cfggameplay.json`, missing files referenced by `cfggameplay.json`/object spawners, missing `<ce folder>`) | **fail** or **warn** per pattern. The patterns live in one catalogue file with log fixtures captured in S9. Unknown `error.log` content is a **warning** and is shown verbatim |
| `expect` lines: per instance/integration `boot_test.expect: ["<regex>"]` (e.g. a mod's "loaded" `Print`) | **fail** if missing |
| `dzo-admin` (if active): registration at the **fake dzo endpoint** the boot test starts (right token, protocol version) | **fail** if missing |
| Clean shutdown line in the RPT | info only (the SIGKILL fallback is expected) |

The **vanilla baseline** (the same build with no mods and the pristine vanilla mission) is booted on demand and cached as
`cache/boottest/baseline/<product>/<buildid>.json`. `dzo test boot --vanilla` refreshes it.

Output: a report per check (text + JSON, exit code 0 pass / 1 fail / 2 infrastructure problem such as a missing server install or no free port),
and the complete `profiles/` logs copied to `--out <dir>` (default `./boottest-<instance>-<timestamp>/`).

### Where it is used

* **Development:** `dzo test boot <name>` against a local site checkout, typically with `--server steam --native`, after every
  change to integrations, overlays, merge strategies or the renderer that is meant to reach a server. `dzo integration check <modid> --boot`
  boots a single integration on top of the vanilla mission. `make boottest SITE=<dir>` boots all instances of a site config.
* **Acceptance:** the five example site configs (§C21) must boot green in Phase 1 (together with the golden tests: golden = same bytes, boot = the game accepts them).
* **CI:** public CI has no server files and must never hold Steam credentials, so it runs no boot tests. An optional GitLab job on a tagged
  self-hosted runner with a pre-installed server (`DZO_BOOTTEST_SERVER_DIR`) boots the example configs. The runner is a dedicated test machine
  with no game servers. The job is skipped when the runner is absent.
* **Not on production hosts:** no boot test before updates or restarts. A mod update that breaks scripts is caught by the health checks and the
  restart-storm protection (§C5), and fixed with a forced refresh or by removing the mod (§C7). Changes made by hand (integrations, overlays) are boot-tested on a developer machine before they are pushed to the site repo.

## C23. Optional game data: MetricZ, LogZ and cheat-detection findings (D38)

**What we have.** Our forks, both GPL-3.0-or-later (upstream WoozyMasta), server-side only (`-servermod`), both writing into the profile dir:

* **MetricZ** (`bzed/metricz`): upstream plus our Expansion fixes (NPE in network metrics, vehicle metric leak on delete, `ScriptLog` counting that
  broke Expansion AI). Prometheus text format, file sink `$profile:metricz/export/<name>.prom` (atomic) or HTTP push, 15 s default interval.
  Server FPS, players (vitals, position, ping; per-SteamID series), vehicles, AI, weather, effect areas, crash sites, territories, RPC/event counters.
* **LogZ** (`bzed/logz`): about 50 own commits on top of upstream, developed together with **logz-analyzer** (the "logz and logz-analyzer change together" rule).
  NDJSON, schema `logz-v1beta` (beta, can change), file sink `$profile:logz/logs/logz_<instance_id>.ndjson`, rotated on start (`rotation_keep`).
  **Volume:** several lines per shot. A deathmatch server with 60–100 shots/s plus all other world changes writes **well over 1 GB/day**.
  Sessions (incl. `kick_reason`), chat, hits/kills with hit forensics, line of sight and damage anatomy, `weapon.fire`, player and vehicle snapshots, inventory,
  actions, triggers/gas zones (with their origin), juncture/RPC telemetry, and a second addon with Expansion hooks (code locks, territories, teleports).
* **logz-analyzer** (`cheat-detector/logz-analyzer`): our Python CLI, Debian-packaged (Debian python3 libs only). It reads LogZ events **from Loki only**,
  has about 30 detectors (teleport, speed, godmode, aimbot, range shots, fire rate, through-wall, combat log, code-lock brute force, juncture abuse, …),
  a weapon DB, and a `report` mode that posts fingerprinted findings to Discord from a systemd timer.

**Principles.**
* All three are **optional**. dzo sees MetricZ/LogZ as active when their `CfgPatches` class (`MetricZ`, `LogZ`) is in the instance's active mods, and the
  analyzer when it is installed/configured. Nothing else in this plan depends on them. Features appear when the data is there, like `dzo-admin`'s capabilities.
* **MetricZ needs no external stack.** Its export is one small file in the profile dir (on the host, §C2), which dzo reads directly. Prometheus stays an optional consumer.
* **LogZ needs Loki (hard requirement).** Because of the volume, LogZ files on disk are only a transit buffer for the log agent (Alloy/promtail) that ships them to Loki.
  dzo never tails, parses, stores or keeps them for analysis. Every LogZ feature in dzo and in logz-analyzer is a **Loki query** (LogQL does the filtering and
  aggregation server-side). Without Loki configured, dzo treats LogZ as absent. dzo never rotates, truncates or deletes the mods' output.
* **Telemetry off by default.** Both mods phone home once per start (`settings.disable_telemetry`). dzo sets it to `true` unless the instance says otherwise.

### Division of work: `dzo-admin` vs. the forks

| Capability | `dzo-admin` | MetricZ | LogZ | Decision |
|---|---|---|---|---|
| Game actions (message, teleport, spawn, vehicle repair/delete) | ✓ | – | – | `dzo-admin` only |
| Live positions for the map (≈5 s) | ✓ | 15 s gauges | throttled snapshots | `dzo-admin`. The map never depends on MetricZ/LogZ. |
| Identity on connect (SteamID64, GUID, IP) | ✓ | – | SteamID, name | `dzo-admin` / RCon. LogZ adds session details (`kick_reason`). |
| Server and world metrics (FPS, entities, AI, weather, RPC) | – | ✓ | – | MetricZ. `dzo-admin` gets no metrics collector. |
| Hits, kills, weapon use, damage | – | counters | ✓ detailed | LogZ. `dzo-admin` does not log combat. |
| Chat | ✓ | counter | ✓ | `dzo-admin` / RCon (works without LogZ) |
| Active CE events on the map (§C16) | ✓ | counts | `event.in/out`, gas-zone origin | `dzo-admin` (needs live positions and lifetimes), reusing LogZ's gas-zone-origin findings |
| Base building, code locks, territories | – | territory gauges | ✓ incl. Expansion | LogZ |
| Cheat indicators | – | – | raw data | logz-analyzer |

Rule for new work: **observation** (a new log event, a new metric) goes into LogZ/MetricZ, and for LogZ together with the matching analyzer change.
**Acting**, or anything the web UI needs live, goes into `dzo-admin`. So `dzo-admin` stays small. It copies no code from the forks (GPLv3 §13 would allow
combining it with AGPL code, with per-file SPDX headers). What it does take over are the techniques the forks have already verified on real servers:
* time-sliced work spread over frames, registries kept by `EEInit`/`EEDelete` instead of world scans, cached class-name/label strings (MetricZ);
* the **second-addon pattern** for hooks into optional mods (LogZ `expansion/`): a separate PBO in the same `@dzo-admin` folder whose `requiredAddons`
  names the other mod, every file wrapped in that mod's define. A missing required addon is only a warning, and the guarded code compiles to nothing
  (verified with Expansion 1.9.73). `dzo-admin` uses it for its own Expansion support. It is also input for S8: for the marker API, the other direction is a third-party
  mod's optional addon that requires `dzo-admin`;
* checking in the game sources that every hooked method actually runs server-side, plus the boot-test recipe (§C22).

### MetricZ in dzo

* Render (step 7) sets these keys in `$profile:metricz/config.json`: file export on, atomic write on, fixed `file_name` (`metricz`), `instance_id` = instance name,
  `settings.disable_telemetry`, and `disabled_metrics.players` (default `true`: dzo has the players from `dzo-admin`/its DB, and per-SteamID series are
  unbounded cardinality and personal data). Instance knobs: `gamedata.metricz: {telemetry: false, player_series: false}`.
* `dzo-exporter` reads `profiles/metricz/export/metricz.prom`, parses it with `expfmt` (Prometheus client libs, already a dependency), adds
  `instance`/`product`/`map` and serves the series under their original `dayz_metricz_*` names on `/metrics`. A file older than 3× the collect interval
  counts as stale: its series are dropped and `dzo_metricz_up{instance}` goes to 0. **This replaces the planned `metricz-exporter` quadlets.** There is one scrape target per host.
* `dzo status` and the web dashboard show server FPS and entity counts when available. `dzo check remote --fps-warn/--fps-crit` is an optional Icinga threshold.

### LogZ in dzo

* **Placement:** LogZ writes into the profile dir (`profiles/logz/logs/`), like the many other logs mods leave in random places there. A bind mount
  elsewhere (`container.logz_dir`) stays optional, because setting it up needs root. Accepted consequence: log data lives in the instance subvolume and is
  kept by btrfs snapshots until they are pruned, so snapshot retention sizing (§C20) has to account for it. The agent collects the files (`dzo loki-config` prints
  the paths and labels logz-analyzer expects: `service_name="dayz_logz"`, `instance="<name>"`).
* Render (step 7) sets these keys in `$profile:logz/config.json`: file sink on, `events_mask: "-1"` (all events, including the numeric-only ones the analyzer needs),
  `settings.disable_telemetry`, `instance_id` (= the Loki `instance` label). `rotation_keep` stays the admin's (disk is a transit buffer, not storage).
  Instance knob: `gamedata.logz: {telemetry: false}`.
* **Loki config** in `/etc/dzo/config.yaml`: `loki: {url, tenant, auth in secrets/, service_name: dayz_logz}`, the same values logz-analyzer uses.
* **Queries, not ingestion** (`internal/loki`, phase 3): the player page and the server views run LogQL on demand (short in-memory cache), e.g. the kill feed of a server,
  a player's kills/deaths/longest hit/weapons over a chosen range (metric queries like `count_over_time`/`max_over_time` with `| json` filters, computed in Loki),
  session details (`kick_reason`), and base/lock events of a player. dzo stores none of it. Query cost is bounded by limits (time range, line limit, timeout).
* Lines are parsed only when shown, and a line with an unknown `schema` is shown raw with a warning instead of being interpreted. The contract fixtures come from the logz repo.
* Without Loki (or with Loki down) these views are hidden or show "Loki unavailable". Nothing else in dzo is affected.

### Cheat-detection findings (logz-analyzer)

* logz-analyzer stays a **separate Python project and the only implementation of the detectors**. It evolves together with LogZ, and porting about 30
  detectors to Go would give us two copies to maintain. dzo runs it and keeps the results. dzo's Debian package `Suggests: logz-analyzer`.
* **Changes needed in logz-analyzer** (done in its repo):
  1. **machine-readable findings**: `findings --json`, one JSON object per finding with `detector`, `instance`, `fingerprint`, `ts`, `steam_id` (subject)
     plus involved steam ids, `pos`, `grade` (`verdict` / `audit` / `assumption`, from each detector's documented caveat), `title`, `detail`, the **evidence lines** themselves (the few raw LogZ lines the
     finding cites, not only their timestamps: findings are kept far longer than Loki keeps logs), plus analyzer version and LogZ schema.
     The current `Finding` has no steam id, position, grade or evidence;
  2. excludes per run (`--exclude`), supplied by dzo.

  It keeps reading from **Loki** as it does today (its per-instance event cache fetches only the delta per run). There is no file source.
* **Run:** `dzo-analyze.timer` (host-wide, default every 15 min, `analysis.interval` in `/etc/dzo/config.yaml`) → `dzo analyze`. For each instance with LogZ
  active and `analysis.enabled`, it runs the analyzer as a subprocess (as `dayz`, with a timeout, the Loki settings from dzo's config, state/event cache in `cache/analyzer/<instance>/`) and
  ingests the findings idempotently by fingerprint. A failure is a job failure with a notification, and it never touches a server.
* **Suppression from dzo's own knowledge:** staff steam ids (web users linked to a steam id, plus `analysis.exclude`) are passed as excludes. Findings that dzo's
  audit log explains are marked `explained` automatically: an admin teleport of that player within ±N s explains a teleport/speed finding, an admin spawn explains item
  findings, an admin heal or godmode explains godmode/vitals findings.
* **Stored in dzo's database, long-term, per player** (relational DB: low volume, mutable review state, so no ClickHouse):
  * `findings`: id, fingerprint (unique), instance, detector, grade, **steam_id** (FK `players`, a player row is created if unknown) and **be_guid** (derived
    with `dzid`, indexed), involved steam ids, ts, pos x/z, title, detail, status `new|confirmed|false_positive|explained`, reviewed_by/at, review note,
    analyzer version, LogZ schema, and the **full finding JSON incl. its evidence lines**. So a finding stays self-contained and understandable years later,
    after Loki has expired the underlying logs.
  * `finding_players`: finding ↔ every involved steam id (victims, other shooters), so a player's page also lists findings they were part of.
  * Per-player aggregates for **every player** across servers: counts per detector and grade (24 h / 7 d / all time), first/last finding, confirmed count.
* **Retention:** findings are kept **long-term**. `analysis.retention` defaults to **3 years** (configurable, `0` = unlimited). Findings referenced by a ban are never
  expired while the ban exists. Player erasure (§C18) removes them unless an active ban references them (same rule as for bans).
* **Search** (web + `dzo findings search`): by **SteamID64 or BE GUID** (either resolves the player, including sessions only known by GUID, §C18), by name
  (through the name history), by server, detector, grade, status, time range, by **position** (radius around x/z, indexed on x/z), and **full text** over
  title, detail and evidence lines. Full text uses the DB's own engine: SQLite **FTS5**, PostgreSQL `tsvector` + GIN, and there is no extra search service.
  Results export as CSV/JSON (e.g. as evidence for a ban appeal).
* **Web (moderator role):** a findings queue per server and globally, a behaviour tab on the player page (timeline, counts per detector, evidence positions on the map),
  review actions, and a ban dialog pre-filled with the finding references.
* **Alerts:** `analysis.alerts: [{detector: teleport, grades: [verdict], count: 3, window: 24h, action: notify|watchlist}]`. Notifications go through
  dzo's notifier (`analysis.finding`, rate-limited, coalesced). **Findings never trigger kicks or bans automatically.** The analyzer's own Discord `report` is for
  setups without dzo. Don't enable it for an instance dzo analyses, or findings are reported twice.
* Metrics `dzo_findings_total{instance,detector,grade}`. Findings are personal data: they are part of the per-player data export (§C18). DB backups (§C20) cover them.

---

# Part D — Project structure (Go)

```
cmd/dzo/                    main, CLI wiring (cobra)
internal/
  config/                   operator config, products, site repo loader, schemas (instance.yaml, integration.yaml), validation
  site/                     git handling of the site repo (remote URL, pull/commit/push)
  product/                  product drivers (dayz: stable/experimental; interface for future products)
  steam/                    steamcmd runner (podman), app info/build ids, workshop API client
  cache/                    generations, reflink/hardlink snapshots, GC, locks
  maptiles/                 map sources (client-depot single-file download, upload, validation), PAA decode, rvmat georeferencing, tile pyramid, export
  mission/                  pristine missions (git), one-time init, manifest, apply plan, drift, snapshots/rollback
  render/                   render pipeline, merge strategy registry
  ce/                       XML/JSON merge + normalisation (etree, dzce adapters)
  servercfg/                serverDZ.cfg parser/writer
  battleye/                 BE cfg, built-in rcon protocol client + event parser, restart sequence
  backup/                   btrfs subvolume/snapshot ioctls, triggers, retention + cleanup, restore, DB backup, post_backup hook
  quadlet/                  unit + timer templates, generation, systemd (dbus) control
  instance/                 lifecycle, status, wipe
  update/                   update orchestration, policies
  health/                   in-container probes (startup/live), A2S
  monitor/                  status model, Prometheus metrics, /status endpoint, Icinga checks (plugin API, remote)
  admin/                    dzo-admin servermod endpoint, command queue, world state (phase 3+)
  loki/                     LogQL client for LogZ views (kill feed, combat stats, sessions); queries only, no ingestion (optional, §C23)
  analysis/                 logz-analyzer runner, findings ingest, audit-log suppression, alerts (optional, §C23)
  notify/                   notifier interface, Discord webhook
  hooks/                    hook runner
  jobs/                     job model (log, progress, cancel)
  db/                       schema, goose migrations, sqlc queries (sqlite + postgres); clickhouse/ analytics schema + writer
  players/                  identity linking, sessions, stats, steam web api client, geoip
  moderation/               bans (all target types/scopes), enforcement, native ban list rendering, join policies
  schedule/                 one-off/recurring restarts and broadcasts, transient systemd timers
  auth/ (phase 3)           users, argon2id, TOTP + recovery codes, enrolment links, sessions, step-up, lockout, OIDC, API tokens
  web/ (phase 3)            handlers, templates, static (embedded)
  legacy/                   config converter: legacy git branches → example site config + report (no volumes)
  boottest/                 (development only) disposable server tree, Steam install locator (libraryfolders.vdf/appmanifest), process/container runner, log checks + pattern catalogue, baseline cache, refuses to run on hosts with instances
images/runtime/Containerfile, images/steamcmd/Containerfile
debian/                     Debian packaging (§C14)
contrib/icinga2/            CheckCommand definitions + examples
contrib/backup-hooks/       example post_backup hooks: restic, borg
.github/workflows/, .gitlab-ci.yml   CI (§C15)
testdata/golden/…           legacy renderer outputs (see E)
docs/                       Sphinx documentation website (reST, `make docs`), served under /docs/ (D36)
LICENSE                     copy of LICENSES/AGPL-3.0-or-later.txt (pkg.go.dev cannot read a symlink)
LICENSES/                   licence texts (REUSE); every file has SPDX headers
```

Tooling: latest upstream Go (go1.27.x, pinned via the `toolchain` directive; not trixie's golang-go), `golangci-lint`, `go-licenses`, `reuse`,
`make test` (coverage gate 85 %), golden tests, `make deb`. CI on GitHub Actions and GitLab from the first commit (§C15).
The `dzo-admin` servermod lives in a separate repository.

---

# Part E — Phased roadmap

Each phase ends with a working, deployable state.

### Phase 0 — Spikes and golden master (before real code)
* **S0 golden master**: run the legacy `server` image with `DONT_START=1` for each of the five branches
  against **fresh volumes** (pristine missions + downloaded mods; no production data), and capture the rendered managed files + args → `testdata/golden/<branch>/`.
  The new renderer must reproduce these (modulo formatting and documented fixes from A8).
* **S1** read-only shared server root with nested mounts: does DayZServer (1.29, stable + experimental) run with `/dayz` ro? Which paths must be writable?
  Also: measure mission load times of all five servers (default `health.startup_timeout`), and confirm experimental consumes workshop items via 221100.
* **S2** live mission as a nested rw mount (`/dayz/mpmissions`) under a ro root. Also make an inventory of what the game and the mods on our five servers write into the mission dir (storage, Expansion, Editor exports, …), to validate the managed/unmanaged split of §C6.
* **S3** exact `xmlmerge` (gwenhywfar) semantics on our files (duplicates, attribute handling) vs etree/`dzce` merge.
* **S4** DayZ native `ban.txt` format (Steam64 vs. other ids) for ban rendering, and the built-in RCon client against real DayZ servers (stable + experimental): multi-packet `players` with 60+ players, server-message ack timing,
  keep-alive, behaviour on server restart, and the event message formats (connect/GUID/chat/kick) captured as fixtures.
* **S5** host networking with several instances: port layout, RCon bound to localhost, A2S probe from inside the container with both network modes.
* **S6** quadlet/podman features on **trixie's podman 5.4.2** (VM): `HealthStartup*`, `HealthOnFailure=kill` + `Restart=always`, `Notify=healthy`, nested `Volume=` ordering, `ExecStartPre` time limits, `.build` units in rootless mode, timers generated for the `dayz` user.
* **S7** on the **target kernel** (trixie 6.12, or the chosen backports kernel), not the dev box: confirm the host filesystem (btrfs?), `cp --reflink` behaviour for the download cache generations, and **unprivileged subvolume create/snapshot +
  `ro false` + `rm -rf` deletion on the trixie kernel (6.12)** as the `dayz` user, incl. files written by the rootless game container.
* **S8** (end of Phase 2) `dzo-admin` feasibility with the `dayz-dev` skill: `RestApi` polling of `127.0.0.1` from a servermod (latency, stability), and server-side-only implementation of message/teleport/spawn/vehicle repair+delete; state push of all players/vehicles (payload size, server FPS impact); detection of active CE events (script-accessible CE API vs. class/spawn-position correlation, effect areas); identity events on connect (SteamID64, BE GUID, IP) and the enumeration of spawnable item classes; class-watch tracking technique for the marker API and the soft-dependency mechanism (`#ifdef` define) for third-party mods.
* **S9** boot test groundwork (§C22), done manually with the `dayz-dev` skill and the Steam-installed server (stable + experimental):
  vanilla script module counts per build; time to A2S readiness; whether the server boots with no reachable network (container mode) and
  whether a test boot shows up in the master server list; the formula for expected module counts with mods. Also a **catalogue of the log lines** for
  deliberately broken inputs, captured as fixtures: bad XML in `types.xml`/`events.xml`/`cfgeconomycore.xml`, unknown class names, broken
  `cfggameplay.json`, a missing object spawner file, a missing `<ce folder>`, a script compile error, an absolute mod path and a PBO without a prefix.
* **S10** client-depot single-file download for map tiles (§C17): the Windows client depot id(s) of 221100 (and whether Sakhal is a separate DLC depot),
  `sDepotDownloadFileFilter` syntax (several patterns or one run per file), output path, `download_depot` without a manifest id = latest, and that the result has
  real satellite tiles (not the 172-byte server placeholders). If it doesn't work, vanilla maps also use manual upload.

### Phase 1 — Core + CLI, single instance parity
* Repo bootstrap: licence (REUSE-compliant from the first commit), Makefile, CI on GitHub + GitLab with the 85 % coverage gate and licence check active from day one. The documentation site (`docs/`, D36) is built in CI from day one, and every feature lands together with its documentation page.
* Config/products/site repo (remote URL), `dzo steam login`, product install/update into generations,
  mod add/update into generations (workshop and local servermods from the site repo or a host path, §C7), pristine missions from git + one-time init, render pipeline with all merge strategies
  and the **in-place apply with manifest/drift/snapshots**, serverDZ.cfg and BE handling, quadlet generation with
  **working health checks**, start/stop/restart/status/logs, **built-in RCon client** (§C8) + graceful restart.
* Minimal `dzo-exporter` (`/metrics`, `/status`) + `dzo check remote`, so the instance can be monitored from day one.
* `dzo test boot` (§C22, development only: native + container mode, vanilla baseline, log checks) so every render change is boot-tested on a developer machine from the start.
* Acceptance: golden tests **and boot tests** are green for all five configs; one instance (suggest **hashima**, smallest) runs
  in parallel to production on other ports with a **copy** of its mission; kill/hang tests prove health → restart works.

### Phase 2 — Automation, monitoring, packaging, migration
* Mod update engine with policies and timers (hourly check), forced mod refresh, server build detection + notification and the manual `product update`/`instance upgrade` flow, per-instance maintenance restart timers, GC,
  log rotation/crash summary, **btrfs snapshot backups incl. before automatic mod updates, retention/cleanup, restore (§C20)**, hooks (+ traderstocks/weather as hooks).
* `dzo-exporter` complete (`/metrics`, `/status`, auth/TLS, MetricZ re-export, §C23), MetricZ/LogZ config keys in render, remote Icinga checks + shipped CheckCommands; Discord notifications (default + per-server webhooks).
* Debian package (§C14) built in both CIs as an artifact, and `dzo setup`.
* Example site config for all five servers via `dzo legacy convert-config` (§C21) + manual review; `CONVERSION_REPORT.md` decisions resolved.
* Fresh-start rollout per server (§C21): announce wipe → instance from example config → install → fresh mission → parallel test → switch ports → retire legacy.

### Phase 3 — Web platform
* `dzo serve` + **authentication with TOTP MFA by default** (§C12, D34; user bootstrap via CLI) + dashboard + instance/mod management + RCon console + jobs + SSE logs; optionally takes over scheduling.
* Database layer (SQLite + PostgreSQL), users/RBAC/audit; optional ClickHouse analytics store (§C19) with replay/heatmap/statistics features.
* Players & moderation (§C18): identity/sessions, player list + pages, Steam info, GeoIP, kick/ban (all scopes/targets), restarts & one-off timers, broadcasts, item spawning with type list.
* Optional game data (§C23, requires Loki): LogZ views via LogQL (kill feed, combat stats), then cheat-detection findings: `findings --json` lands in the
  analyzer repo first, then `dzo analyze`, the review queue, the behaviour tab and alerts.
* Map tile pipeline (§C17) + tile serving/export.
* Live admin map: `dzo-admin` state push (players, vehicles) → world state → Leaflet map over SSE.
* `dzo-admin` servermod, if S8 is positive (developed with the `dayz-dev` skill): direct messages → teleport → spawn items →
  vehicle repair/delete, each with roles, audit log and rate limits. Every build is boot-tested (§C22) and released as an archive for the `url` local-mod source (§C7).
* Map marker API (class watch rules → script API → file drop, mod-shipped icons) + integration guide + example mod.
* Active in-game events layer (event→class mapping generated from the rendered CE files, detection in `dzo-admin`, Discord notifications).
* Then: config editors with validation and diffs, Steam mod search.

### Phase 4 — Nice-to-have
* Scheduled in-game broadcast messages (FR-16a), built-in weather/trader plugins in Go, a types.xml authoring toolkit
  (nominal scaling, category filters à la "no food" from the legacy README), Discord bot, additional products,
  multi-host (agent per host) if ever needed.

---

# Part F — Open questions, risks, assumptions

**Answered (2026-09-26)**

* Q1 Mission dir holds all game and mod data → never wiped; in-place updates of managed files only (D13, §C6).
* Q2 `files/custom` is mounted into `/profiles/custom` → the config converter turns it into instance overlays (§C21).
* Q3 Schedules vary per server: mod checks usually hourly, restarts every 3–4 h. Plus regular in-game messages (later, FR-16a). Implemented with systemd timers, never cron (D19).
* Q4/Q5 DayZ stable **and** experimental side by side, and future products possible (D14). Experimental uses its own per-instance servermpmissions.
* Q6 AGPL-3.0-or-later if the dependencies allow it, else GPL-3.0, else MIT (D15).
* Q7 Site repo name free; the remote must be configurable by URL (D16).
* Q8 `dzo`, user `dayz`, Debian package (D17, §C14).
* Q9 Discord + Icinga; working container health checks (D18, §C9).
* Q10 Following the legacy start script: merge targets are refreshed from pristine and rebuilt; CE data only via generated `<ce folder>` entries; all pristine files are managed (§C6).
* Q11 Managed files: warn, backup, overwrite. Never delete files that come from elsewhere; new files in the mission repo are adopted (§C6).
* Q12 GitHub Actions + GitLab CI, `.deb` as artifact, no apt repo (D21, §C15).
* Q13 Icinga is remote → `/metrics` for Prometheus + `/status` + `dzo check remote`/`a2s` (D22, §C9).
* Q14 Default Discord webhook + optional per-server webhooks with event filters (§C9).
* Q15 No hostnames in the plan. The exporter defaults to plain HTTP; TLS is optional and hot-reloadable, so replacing a certificate never restarts a game server (§C9).
* Q16 A Prometheus + Loki stack (gigapipe, tracing available) exists → metrics scraped from `/metrics`, logs via journald + LogZ files collected by an agent, optional OTLP traces (§C9).
* Q17 Admin-mod priorities: live player map, spawn items for players, teleport via the HTML map, repair/delete vehicles, direct messages (§C16).
* Q18 Map tiles come from the map's data PBO: vanilla maps as a single file from the client depot (the dedicated server only ships stripped placeholders), uploaded/configured file as fallback, and a configured file for modded maps: `layers/*.paa` → PNG, resized, served by dzo and shared with our own tile server (§C17).
* Q19 All game-changing actions (teleport, vehicle repair/delete, item spawns, messages, …) and the live map state go into our own admin mod. LogZ only delivers log lines to Loki (§C16).
* Q20 `dzo-admin` is a separate mod, independent of LogZ (§C16).
* Added: 85 % coverage requirement (D20); optional `dzo-admin` servermod (D23, §C16).
* Review (REVIEW.md, 2026-09-26): findings resolved or refuted, see §G.
* Added (2026-09-29): local servermods without the Steam Workshop, incl. `dzo-admin` (D37, §C7).
* Added (2026-09-29): MetricZ/LogZ (our forks) and logz-analyzer findings as optional data sources, and the division of work with `dzo-admin` (D38, §C23).
  This refines Q19: game-changing actions stay in `dzo-admin`, and dzo may now optionally *query* LogZ data, **only through Loki** (hard requirement, since the volume is over 1 GB/day).

**Still open**

* **Q21** (prior art if the TOTP opt-in is ever built: `WoozyMasta/steamcmd-2fa`, MIT) Steam Guard type of the dedicated account (e-mail code or mobile authenticator/app confirmation)? Should dzo ever hold a TOTP secret for fully unattended logins (not recommended), or should the password be stored encrypted (`steam.store_password`)?

**Risks**

* R1 DayZServer may refuse a read-only install dir or nested mounts (S1/S2). Fallback: a per-instance reflink copy of the build (cheap on btrfs) instead of an ro mount.
* R2 `xmlmerge` semantics are subtly different from ours, which would cause gameplay differences. Mitigated by the golden tests (S0/S3).
* R3 `dzce` is v0.x with breaking changes. Mitigation: wrapper, pinning, own fallback.
* R4 steamcmd flakiness (timeouts on large mods, "Success" parsing). Mitigation: retries, per-item verification via `meta.cpp` + size, jobs are resumable.
* R4a **Steam authentication needs a human**: password + Steam Guard (e-mail/mobile code or app confirmation), and cached sessions expire unpredictably. Updates stall until someone logs in again. Mitigations: interactive pty-driven login in the CLI and web, early detection by a proactive probe, pause (not fail) of the update pipeline, Discord/Icinga/metrics alerts, no automatic password retries (lockout protection), optional encrypted password storage (§C7).
* R4b Steam changes steamcmd prompts or the login flow. Mitigation: prompt patterns in one place with fixtures, a `--passthrough` raw terminal fallback, and an alert on unknown output.
* R5 Merge/CE precedence differs from legacy (legacy iteration order was effectively random, so conflicting CE/XML definitions may resolve differently).
  The game's own load order is not affected (DayZ resolves it via `requiredAddons`). Mitigation: the render report lists every definition overridden by more than one mod,
  so conflicts become visible and are settled explicitly in the example config review, instead of being order-dependent.
* R6 **Mission data loss** is the worst case. Mitigations: never delete unmanaged paths, copies of changed managed files before every apply, validation in staging before touching live, mandatory btrfs snapshots before updates and destructive operations, and golden + property tests ("render never touches paths outside the manifest").
* R7 Older podman on the host lacks `HealthStartup*`/`Notify=healthy` (S6). Fallback: a single `HealthCmd` with a long `HealthStartPeriod` (legacy style), plus the host-side watchdog in `dzo-status`.
* R8 A mod or the game rewrites a file that ships in the mission repo, so every render produces "drift" and overwrites runtime data. Mitigations: drift is always backed up + notified, recurring drift on the same file suggests adding it to `mission.unmanaged`, and the example config seeds `unmanaged` from the S2 inventory.
* R9 **btrfs is required** for instance data. Hosts without btrfs need a btrfs data volume (partition/LV, or loopback image as a stop-gap). Unprivileged snapshot deletion without `user_subvol_rm_allowed` is slow for big trees. Mitigation: `dzo setup` checks and recommends the mount option, and the deletion fallback is tested in CI.
* R10 **Boot tests give false confidence or cost too much.** A headless boot cannot cover player-driven code (actions, inventory, damage), real
  clients, networking or BattlEye, and some errors only appear under load or after hours. Reports say what was *not* covered. Automatic mod updates
  on production are not boot-tested, because a real server never runs next to live servers. There, the health checks and restart-storm protection remain the safety net, and a fix means a forced mod refresh or removing the mod (§C7).
* R11 **LogZ schema drift** (`logz-v1beta`, beta). A changed log line breaks dzo's LogQL views or the analyzer. Mitigations: dzo checks the schema of every line it shows and falls back to raw display instead of
  guessing, contract fixtures shared with the logz repo, and the logz/logz-analyzer change-together rule. All of it is optional, so breakage never affects servers or admin actions.

**Assumptions**

* One host for now, x86_64, Debian trixie (podman 5.4.2, systemd 257, kernel ≥ 6.12; backports kernel allowed), systemd user session for `dayz`, the configured instance/snapshot paths on btrfs.
* The five servers keep their current ports.

---

# Part G — Review resolution (REVIEW.md, 2026-09-26)

| # | Finding | Resolution |
|---|---|---|
| F1 | Unprivileged btrfs snapshots only from Linux 6.15 | **Refuted.** In v6.12 (and already v4.19) `fs/btrfs/ioctl.c` has no `CAP_SYS_ADMIN` gate on `SNAP_CREATE`/`SNAP_CREATE_V2`; snapshots only require `inode_owner_or_capable` on the source subvolume ("snapshots are limited to own subvolumes only"). Only `SNAP_DESTROY` needs `user_subvol_rm_allowed`, which the plan already handles. Still adopted: the kernel is pinned in D29/§C0 (≥ 6.12, **trixie-backports kernel accepted** if S7 finds a gap, no userspace workaround), and S7 runs on the target kernel. |
| F2 | Experimental appid | Resolved (1042420 confirmed). Workshop 221100 for experimental → check in S1; optional `branch_password` per product added (§C7). |
| F3 | Restart storm on persistent render failure / crash loops | **Fixed** (§C5): pre-flight dry-run render before every planned restart (server keeps running on failure), `failed-render` gate keyed on the input hash + `dzo instance ack-failure`, systemd `StartLimitIntervalSec`/`StartLimitBurst` (instance `restart_limit`), optional cool-down retry only for crash/health loops. |
| 3.1 | Global timer vs per-instance `check_interval` | **Fixed:** the timer runs at the minimum interval, and `dzo update check` gates per instance (§C5/§C7). |
| 3.2 | Scheduling ownership | **Fixed:** single rule, everything is a systemd timer; the daemon only materialises and cancels timers, and runs are locked and recorded (§C5, §C18). |
| 3.3 | `dzo-admin` endpoint with `publish` networking; host-mode exposure | **Fixed:** network-mode-aware endpoint URL (§C16), and the token-as-only-guard model stated explicitly (§C13). |
| 3.4 | `network-online` deps in a user quadlet | **Fixed:** removed from the sketch (quadlet injects the rootless wait unit). |
| 3.5 | A2S challenge handshake / readiness | **Fixed:** noted in §C9 and the probe timeout. |
| 3.6 | `ban.txt` format | **Adopted:** to be verified in S4 before implementation (§C18). |
| 3.7 | Update debouncing/windows | **Fixed:** windows, quiet hours, `min_restart_interval`, `batch_delay`, piggy-backing on maintenance restarts, `max_delay` (§C7). Server builds are no longer applied automatically at all (D7). |
| 3.8 | "snapshots" naming collision | **Fixed:** per-file copies renamed to `filehistory/` (`mission.keep_file_history`). |
| 3.9 | Tile URL hashing | **Fixed:** hash in the tile path, `metadata.json` with ETag points to the current hash (§C17). |
| 3.10 | Migration diff→overlay | **Obsolete:** no data migration at all; servers start fresh, and only the config is converted into a reviewed example (§C21, D32). |
| 3.11 | `#ifdef` soft dependency | **Adopted:** runtime type lookup as the planned fallback; file drop always works (§C16). |
| 3.12 | Memory limit | **Fixed:** `container.memory` → quadlet `Memory=`. |
| 3.13 | Startup budget | **Fixed:** `health.startup_timeout` (default 45 m) generates `HealthStartupRetries` and `TimeoutStartSec`; S1 measures real load times. |
| 3.14 | steamcmd self-update location | **Fixed:** verified in Debian's wrapper; it self-updates into `$XDG_DATA_HOME/Steam/steamcmd` inside the persistent steamcmd home. This is documented as the one accepted exception (§C4). |
| 3.15 | Coverage ramp/exclusions | **Partly refuted:** no ramp, 85 % from the first commit (user requirement). Only generated code may be excluded, file by file; hard areas are covered via fakes (D20). |
| 3.16 | Q21 prior art | Noted (`WoozyMasta/steamcmd-2fa`). |
| 3.17 | Instance rename/removal | **Fixed:** removal cleans up units, timers, exporter targets and markers; renaming = clone + remove (§C11). |
| §4 | Ordering nits, licence list | **Fixed:** FR/NFR/R/answered-Q lists sorted; licence list completed (pbo, paa, lzo, lzss, rvmat, dzid, steam, creack/pty, pgx, goose, clickhouse-go, maxminddb). |

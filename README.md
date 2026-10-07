<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

<div align="center">
  <img src="assets/logo.svg" alt="dzo logo" width="160" height="160">

  <h1>dzo</h1>
  <p><strong>A DayZ dedicated server operator for rootless podman.</strong></p>

  [![CI](https://github.com/bzed/dayz-server-operator/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/bzed/dayz-server-operator/actions/workflows/ci.yml)
  [![Codecov](https://codecov.io/gh/bzed/dayz-server-operator/branch/main/graph/badge.svg)](https://codecov.io/gh/bzed/dayz-server-operator)
  [![Go Report Card](https://goreportcard.com/badge/github.com/bzed/dayz-server-operator)](https://goreportcard.com/report/github.com/bzed/dayz-server-operator)
  [![Go Reference](https://pkg.go.dev/badge/github.com/bzed/dayz-server-operator.svg)](https://pkg.go.dev/github.com/bzed/dayz-server-operator)
  [![REUSE status](https://api.reuse.software/badge/github.com/bzed/dayz-server-operator)](https://api.reuse.software/info/github.com/bzed/dayz-server-operator)
  [![License: AGPL v3+](https://img.shields.io/badge/license-AGPL--3.0--or--later-blue.svg)](LICENSE)
</div>

---

`dzo` manages DayZ dedicated server instances on top of **rootless podman**
and **systemd quadlets** — no Node.js, no `podman generate systemd`, and no
external RCon tools. It's a single static Go binary that installs and
updates server builds and workshop mods into immutable, shareable
generations, renders each instance's mission and configuration from a
site-config repo, and talks to the game server directly over a built-in
BattlEye RCon client.

This replaces a fleet of ad-hoc bash scripts (`dzpodman`, `bercon-cli`,
`dayz_restart`, …) that grew organically across several production DayZ
servers, with one tool, one config model, and a test suite. The full
design — decisions, legacy analysis, architecture and the phased roadmap —
lives in [`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md); user-facing
documentation is a [Sphinx site](docs/) under `docs/`. For a quick start there is a
[two-page cheat sheet (PDF)](docs/cheatsheet/dzo-cheatsheet.pdf) with the setup steps
and the everyday commands.

> **Status:** Phase 1 (core + CLI) is substantially built: every package
> below is implemented and tested, up to and including instance lifecycle
> (quadlet/timer materialization, systemd start/stop/restart, the F3
> restart-storm brake, the BattlEye graceful-restart sequence), resolving a
> site-config instance, mission render, mod/product install, and the first
> part of the web platform: the `dzo-admin` servermod, the JSON API and a web
> interface for players, vehicles and a live map. Not built yet: web users
> with passwords and TOTP, the player/ban database, one-off restart timers and
> scheduled broadcasts, and golden-master tests for the maps whose mission is not
> Bohemia's (spike S0 compared three branches), see
> [MILESTONES.md](MILESTONES.md) and the [spikes page](docs/spikes.rst). See
> [Needs live verification](#needs-live-verification) below for what
> hasn't been checked against a real Steam account or DayZ server, and
> the plan's [phased roadmap](IMPLEMENTATION_PLAN.md#part-e--phased-roadmap)
> for what's tracked as still pending.

## What's here today

| Package | What it does |
|---|---|
| `internal/config` | Loads and validates `/etc/dzo/config.yaml` (data paths, products, notification targets) |
| `internal/servercfg` | Parses/writes `serverDZ.cfg` and diffs two versions before adoption |
| `internal/battleye` | A native BattlEye RCon client — login, commands, multi-packet responses, the connect/GUID/chat/kick event stream — no `bercon-cli`, no external RCon library |
| `internal/ce` | JSON deep-merge and XML child-merge primitives for combining mission/mod/overlay data, plus `cfgeconomycore.xml` `<ce folder>` registration |
| `internal/quadlet` | Renders podman quadlet `.container` units (health checks, host/publish networking, mounts, F3's start-rate limit) without relying on deprecated or newer-than-baseline podman tooling |
| `internal/cache` | An immutable, generation-based download cache with atomic activation and safe garbage collection |
| `internal/site` | The site-config repo's schema types (`site.yaml`/`instance.yaml`/overlays) and git plumbing (clone/pull/commit/push) |
| `internal/mission` | The live mission render/apply pipeline (`dzo instance render`: git pristine fetch, fallback fill, staging, apply): manifest tracking, apply-plan classification, atomic writes with a filehistory safety net, and the CE/XML/JSON merge dispatcher |
| `internal/a2s` | The Valve A2S server-query protocol client (challenge handshake + `A2S_INFO`) |
| `internal/health` | `dzo health startup`/`live` — process + A2S (+ optional RCon) probes for `HealthStartupCmd`/`HealthCmd` |
| `internal/hooks` | Runs the `pre_start`/`post_stop`/... extension-point scripts with a `DZO_*` env contract and a JSON context |
| `internal/notify` | Discord webhook notifications, per-event templates, and event coalescing |
| `internal/monitor` | Prometheus `/metrics`, JSON `/status`, and Icinga/Nagios-compatible checks |
| `internal/steam` | The interactive steamcmd login state machine (pty-driven prompt classifier) and a non-interactive mode for scheduled jobs |
| `internal/product` | The update-window/batching scheduling decision, a Steam Web API mod-freshness client, the steamcmd job runner for `+app_update`/`+workshop_download_item`, and the install flow that turns downloads and local servermods (dir, host path, URL+sha256) into validated immutable generations (`dzo product install\|update`, `dzo mod add\|update\|list\|refresh`) |
| `internal/moddeps` | CfgPatches `requiredAddons` reader for diagnostics (`dzo mod deps\|cfgpatches`), from a PBO (rapified `config.bin`, LZSS entries) or a plain-text `config.cpp`. Not used for ordering: mods load in the order of the `mods` list |
| `internal/resolve` | Combines `config.yaml`, the site checkout (`site.yaml` defaults + `instance.yaml`) and the cache into one resolved instance, including its quadlet spec; behind `dzo instance show` |
| `internal/admin` | The operator side of the `dzo-admin` servermod: the versioned JSON protocol, a hub per instance (live world state, a command queue with timeouts and idempotent ids), the token-guarded endpoint the mod calls, marker file drop, the audit log (JSON lines) and spawn limits |
| `internal/api` | The `/api/v1` JSON API of one installation: scoped bearer tokens with roles, reads, actions (message, teleport, spawn, vehicle repair/delete), server-sent events, audit |
| `internal/apiclient` | The Go client of that API, used by the web interface and `dzo player\|vehicle`, so one web interface can talk to several installations |
| `internal/serve` | `dzo serve`: the API and mod listeners, the mod's `config.json` written at render time |
| `internal/web` | The web interface (`dzo web`): server-rendered pages, htmx, a Leaflet map; talks to installations only through their API |
| `internal/servermods` | `make servermods`: packs `servermods/*/src/*` into reproducible PBOs with a prefix header, using [WoozyMasta/pbo](https://github.com/WoozyMasta/pbo) (MIT), and writes `compat.yaml` (dzo version, servermod commits, PBO hashes), which `dzo mod add\|update` checks shipped servermods against |
| `internal/boottest` | `dzo test boot`: boots a real DayZServer from a disposable tree on a render of an instance and checks the logs (development machines only); `internal/runfiles` renders the keys, `serverDZ.cfg` and BattlEye config an instance needs besides its mission |
| `internal/btrfs`, `internal/backup` | btrfs subvolumes and snapshots by ioctl as the unprivileged user; `dzo backup create\|list\|prune\|pin\|unpin\|diff` and `dzo restore`: an index of read-only snapshots, a retention policy (pure, property-tested), pre_restore safety snapshots, full and partial restore |
| `internal/exporter` | `dzo exporter` and `dzo status`: the collector (systemd, podman, the Steam query, state files, the snapshot index) behind `/metrics` and `/status`, served over HTTP or TLS (certificate reloaded without a restart, mutual TLS, bearer token, IP allow-list) |
| `internal/setup` | `dzo setup`: data directories and their checks, the two container images (`images/`), the Steam login hand-off, the site clone and the weekly image refresh timer |
| `servermods/dzo-admin` | The Enforce Script servermod (submodule): state push, admin actions, marker API. Booted headless on 1.29 |
| `internal/instance` | Ties the above into one instance's lifecycle: the F3 failed-render gate, quadlet+timer materialization, systemd start/stop/restart, and the BattlEye lock/kick graceful-restart sequence |
| `cmd/dzo` | The CLI wiring all of the above together |

## Installing

**From the apt repository** (Debian trixie, amd64; signed, rebuilt from `main`):

```console
$ sudo install -d -m 0755 /etc/apt/keyrings
$ sudo curl -fsSLo /etc/apt/keyrings/dzo-archive-keyring.asc https://bzed.github.io/dayz-server-operator/apt/dzo-archive-keyring.asc
$ sudo tee /etc/apt/sources.list.d/dzo.sources <<'EOF'
Types: deb
URIs: https://bzed.github.io/dayz-server-operator/apt
Suites: trixie
Components: main
Signed-By: /etc/apt/keyrings/dzo-archive-keyring.asc
EOF
$ sudo apt update && sudo apt install dzo
```

The key's fingerprint is `184C DDC4 96A9 C739 D10F  67B0 FE4E E034 4431 4050`.

**From a release build:** grab the `.deb` or a static binary from the
[latest CI run's artifacts](https://github.com/bzed/dayz-server-operator/actions/workflows/ci.yml)
(tagged releases will publish these automatically once cut).

**From source** (needs a recent Go toolchain — see `go.mod`):

```console
$ git clone https://github.com/bzed/dayz-server-operator.git
$ cd dayz-server-operator
$ make build
$ ./bin/dzo version
```

**As a Debian package** (targets Debian trixie):

```console
$ sudo apt install debhelper build-essential
$ dpkg-buildpackage -us -uc -b
$ sudo apt install ../dzo_*.deb
```

## Usage

The [cheat sheet](docs/cheatsheet/dzo-cheatsheet.pdf) covers the setup and the everyday
commands on two A4 pages; the examples below are the basics.

```console
$ dzo version
0.1.0 (a1b2c3d, 2026-09-26T15:00:00Z)

$ dzo config validate --config /etc/dzo/config.yaml
config /etc/dzo/config.yaml is valid
  data:      /var/lib/dzo
  instances: /var/lib/dzo/instances
  products:  2 configured

$ dzo servercfg diff /profiles/serverDZ.cfg.save /files/serverDZ.cfg
~ hostname: "old name" -> "new name"
+ steamQueryPort

$ dzo rcon exec --addr 127.0.0.1:2303 --password s3cret players

$ dzo cache list /var/lib/dzo/cache/products/dayz-stable
* 1234567
  1234600

$ dzo instance show myserver --quadlet > dzo-myserver.container

$ dzo steam login --user mysteamaccount
steamcmd password for mysteamaccount: ****
login OK

$ dzo mod deps mods/@MyMod/config.cpp --provides DZ_Data,DZ_Scripts --order
all requiredAddons entries are satisfied

$ dzo token create web --role operator --delegate > /var/lib/dzo/secrets/web.token
$ dzo serve &                      # /api/v1 and the dzo-admin endpoint
$ dzo web                          # http://127.0.0.1:8081
$ dzo player msg myserver all "restart in 5 minutes"

$ dzo instance restart myserver --graceful --rcon-addr 127.0.0.1:2303 --rcon-password s3cret
```

Run `dzo --help` (or `<command> --help`) for the full flag reference.

## Developing

```console
$ make lint      # gofmt, go vet, golangci-lint
$ make reuse     # SPDX/REUSE licence header check
$ make test      # unit tests
$ make cover     # tests + the 85% coverage gate (D20)
$ make licenses  # dependency licence allow-list check (D15)
```

`.github/workflows/ci.yml` runs all of the above on every push, plus
cross-compiled binary and `.deb` builds, and uploads both coverage and
JUnit test results to Codecov. See
[`CONTRIBUTING` notes in the plan](IMPLEMENTATION_PLAN.md#c15-testing-strategy-and-ci-d20-d21)
for the full testing strategy.

## Needs live verification

This environment has no Steam account, no real DayZ install, and no
podman/systemd user session to develop against, so everything above is
built and tested against real, documented file/wire formats and
fake-server/fake-script test doubles — not against the real thing. These
are the specific spots that need checking against a live setup before
relying on them, roughly in the order they'd bite:

- **steamcmd's interactive prompts and job output** (`internal/steam`,
  `internal/product`): the password/Steam-Guard/mobile-confirmation
  prompt wording and the `+app_update`/`+workshop_download_item`
  success/failure lines are reverse-engineered from community reports,
  not exercised against a real account.
- **Rapified `config.bin` and `Cprs` (LZSS) PBO entries** (`internal/moddeps`):
  decoders are implemented from published format descriptions and tested
  only against synthetic fixtures. Verify against a few real workshop mod
  PBOs (compressed and uncompressed entries, nested arrays, `+=` arrays)
  with `dzo mod cfgpatches`; the LZSS ring-offset convention is the least
  certain part.
- **The install flow against a real steamcmd** (`internal/product`):
  where steamcmd puts workshop items (`<force_install_dir>/steamapps/workshop/
  content/<app>/<id>`), the `appmanifest_<app>.acf` build id, the
  `appworkshop_<app>.acf` layout the forced refresh edits, and `+force_install_dir`
  being honoured by `workshop_download_item` are all from documentation and
  memory. Not implemented: the "size plausible vs. `file_size`" check. Real
  workshop PBOs are only checked for parsing, not for a prefix.
- **BattlEye's event message patterns** (`internal/battleye`'s
  connect/GUID/chat/kick regexes, spike S4) are not confirmed against a
  real server, because no player was connected when it was checked. The
  `players` output, an unknown command, `#lock`/`#unlock`/`#kick`/
  `#shutdown`, the keep-alive answers and the behaviour on a server restart
  are (see the [spikes page](docs/spikes.rst)); `battleye.Session` redials with
  backoff.
- **The resolved quadlet unit** (`internal/resolve`): the `DayZServer`
  command line, the nested mounts over the read-only build (`/dayz/keys`,
  `/dayz/mpmissions`, `@<id>` mods) and the single `Exec=` line with
  `;`-quoting are only checked against podman's documented quadlet syntax.
  It assumes the runtime image has no `ENTRYPOINT`, and that mods can live in
  `@<workshop id>` directories instead of `@<Name>` (DayZ does not care about
  the directory name; unverified).
- **Pristine mission fetch** (`internal/mission`, `dzo instance render`):
  it shallow-fetches `mission_source.ref` with plain `git`; tested against
  local repos only. Fetching a bare commit id needs server support
  (GitHub allows it); branches and tags always work.
- **The `dzo-admin` mod** (`servermods/dzo-admin`): booted headless on a real
  1.29 `DayZServer` (build 24570360) from a PBO packed with dayz-dev-tools,
  against the real `dzo serve`: it compiles without `SCRIPT (E)`, the script
  module counts rise over vanilla (Game 416 to 422, World 2123 to 2125, Mission
  209 to 210), `DZO_ADMIN` is defined, the `RestApi` POST with the token in the
  body works, state arrives (43 vehicles, 9 effect areas, 2030 spawnable
  classes in chunks, file-drop and watch-rule markers), and `vehicle repair`
  (all five scopes), `vehicle delete`, the refusal paths and the deny list
  work. Not verified, because they need a connected player: message delivery
  (`PlayerBase.Message`, the notification call and its icon string), teleport,
  `spawn_item`, the `players` state and vehicle crew; the `ItemBase` watch hook
  was only exercised through cars. Also unverified: that the container can read
  the 0600 `config.json` and reach `serve.mod_listen` (`127.0.0.1` with host
  networking, `host.containers.internal` with pasta), and `compat.yaml` plus the
  boot-test gate of M6 do not exist yet (`make servermods` does; its PBO, with a
  text `config.cpp` and no rapify step, boots like the hand-packed one). Bugs the boot found, now fixed: `proto` and `out` are
  reserved words, `EntityAI` cannot be modded (watch rules hook `ItemBase` and
  `CarScript`), `array<ref>.Copy` types, `Substring` throws past the end, and
  the JSON writer turns bools into 0/1, null arrays into `[]` and null objects
  into `{}`.
- **`PlayerIdentity` has no IP address**, so `dzo-admin` cannot report one; IPs
  stay a RCon-side topic.
- **Web interface assets and map**: htmx and Leaflet are served from
  `web.assets` (`htmx/htmx.min.js`, `leaflet/leaflet.{js,css}` under
  `/usr/share/javascript` is assumed for Debian's `libjs-*`), the CSP allows
  Leaflet's inline styles only, and the world sizes of `sakhal` and unknown
  maps are guesses (maps with tiles use the size derived from the tiles).
- **Map tiles** (`internal/maptiles`) are verified against the client
  `worlds_*_data.pbo` of Chernarus, Livonia and Sakhal and DeerIsle's
  `data.pbo` on a development machine (world positions checked against the
  mission's `mapgrouppos.xml`), not against the automatic client-depot
  download of the plan, which is not built: the PBO is configured or passed on
  the command line.
- **`dzo test boot`** was run against a real `DayZServer` 1.29 (build 24570360)
  installed by Steam, with the Central Economy mission and dzo-admin: it passes on a
  good instance and fails on a mission script error, an XML file that does not
  parse and a missing `<ce folder>`; the logs of those runs are the test fixtures.
  Not covered: booting inside the runtime container, mods from the Workshop
  (only dzo-admin was used), the experimental server, Windows, and
  `cfggameplay.json` (the server only reads it with `enableCfgGameplayFile = 1`).
  The Steam query answers long before the mission has loaded, so readiness waits
  for the mission and the mod's first contact.
- **Backups** (`internal/btrfs`, `internal/backup`): the unprivileged snapshot,
  read-only, writable-restore and delete operations were run on kernel 7.x (a
  development machine) and on Debian 13's kernel 6.12 (a VM), with and without
  `user_subvol_rm_allowed`. Destroying a read-only snapshot with that option
  fails with EROFS until the flag is cleared; dzo does. The snapshot index is a
  JSON file per instance, not the database the plan calls for, because there is no
  database layer yet. Snapshots are taken before upgrades, mod updates, mission
  updates and wipes, and a timer prunes them daily. Not built: the database backup,
  metrics, the web page. The tests
  skip without a btrfs filesystem (`DZO_BTRFS_TESTDIR`); CI mounts a loopback one.
- **The exporter** (`internal/exporter`) was run against fakes for systemd, podman
  and the Steam query, and against real files (a snapshot index, a render manifest,
  the Steam status file); not against a real user systemd or podman. It reads
  `podman inspect --format {{.State.Health.Status}}` of the container `dzo-<name>`,
  and `systemctl --user show` of `dzo-<name>.service`. The restart counter is
  systemd's, without reasons. `dzo units sync` writes the unit that runs it.
- **Container images and `dzo setup`** (`images/`, `internal/setup`): both
  images were built with podman 5.8 on a development machine. Every shared
  library of the real 1.29 `DayZServer` resolves inside `dzo-runtime` (`ldd`),
  and `steamcmd +quit` ran in `dzo-steamcmd` and updated itself into a mounted
  home. A game server has not been run in the runtime image, `podman build
  --pull=newer` and the weekly timer have not been tried on podman 5.4.2 or a
  real systemd user session, and `dzo setup` itself ran only against fakes and,
  as `--dry-run`, in a clean trixie container. `.build` quadlets are not used:
  setup calls `podman build`. Nothing runs through the steamcmd image yet: the
  install commands call the `steamcmd` in `PATH`.
- **Debian's web libraries**: the package depends on `libjs-leaflet` (1.7.1 in
  trixie) and `libjs-htmx` (4.0.0-beta6). The map was checked on Leaflet 1.7.1.
  The pages were written against htmx 2 and were not run against htmx 4.
- **Packaging**: the Sphinx documentation (`/usr/share/doc/dzo/html`, which the
  web interface serves under `/docs/`) and one man page per command
  (`dzo gen-man`) are built into the package; a `dpkg-buildpackage` of that was
  only run in CI. `compat.yaml`'s `server_build` stays empty until the boot test
  (M7) fills it.
- **A2S `AppID` truncation** (`internal/a2s`): the wire field is a signed
  16-bit int; real DayZ app ids may wrap. Documented on `InfoResponse`.
- **The `enfMain` process name** (`internal/health`'s process-existence
  check) is an assumption from plan notes, not confirmed against a
  running server binary.
- **Podman quadlet keys** (`HealthStartup*`, `HealthOnFailure=kill`,
  `Notify=healthy`, `StopTimeout`, `Volume=…:O`) were confirmed on podman 5.4.2
  (spike S6, [spikes page](docs/spikes.rst)).
- **DayZ Experimental's app id/workshop app pairing** (1042420 consuming
  workshop content via 221100) is assumed, not confirmed (spike S1): no workshop
  mod was installed on the experimental instance.

## Licence

AGPL-3.0-or-later. This repository follows the [REUSE](https://reuse.software/)
specification — every file carries an SPDX header, and licence texts live
under [`LICENSES/`](LICENSES/). See [`LICENSE`](LICENSE) and
[`debian/copyright`](debian/copyright) for details.

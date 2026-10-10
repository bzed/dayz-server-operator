<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

<div align="center">
  <img src="assets/logo.svg" alt="dzo logo" width="160" height="160">

  <h1>dzo</h1>
  <p><strong>Run DayZ dedicated servers on Debian with rootless podman.</strong></p>

  [![CI](https://github.com/bzed/dayz-server-operator/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/bzed/dayz-server-operator/actions/workflows/ci.yml)
  [![Codecov](https://codecov.io/gh/bzed/dayz-server-operator/branch/main/graph/badge.svg)](https://codecov.io/gh/bzed/dayz-server-operator)
  [![Go Report Card](https://goreportcard.com/badge/github.com/bzed/dayz-server-operator)](https://goreportcard.com/report/github.com/bzed/dayz-server-operator)
  [![Go Reference](https://pkg.go.dev/badge/github.com/bzed/dayz-server-operator.svg)](https://pkg.go.dev/github.com/bzed/dayz-server-operator)
  [![REUSE status](https://api.reuse.software/badge/github.com/bzed/dayz-server-operator)](https://api.reuse.software/info/github.com/bzed/dayz-server-operator)
  [![License: AGPL v3+](https://img.shields.io/badge/license-AGPL--3.0--or--later-blue.svg)](LICENSE)
</div>

---

`dzo` installs, configures, updates and watches one or more DayZ dedicated
servers on a single Debian machine. It is one static binary: no Node.js, no
shell-script fleet, no external RCon tool. Every server runs in a rootless
podman container, started by systemd, and everything about your servers lives
in a git repository you own.

## What you get

- **Servers as configuration.** One `instance.yaml` per server (map, ports, mods,
  restart schedule) in a git "site" repository. `dzo` renders the mission and
  `serverDZ.cfg` from it, so a server can be rebuilt from git at any time, and
  changes to the Central Economy files are applied in place without wiping
  your live mission.
- **Server builds and workshop mods that never change under a running server.**
  Steam downloads go into immutable, shared "generations". A server switches to
  a new one only when it restarts, and old ones are cleaned up automatically.
  Server builds are updated by hand, never behind your back.
- **Restarts players notice.** Scheduled or manual restarts announce
  themselves in game, lock the server, kick the rest and start again with the
  new build or mods. A brake stops a server that fails to start from restarting
  in a loop.
- **Snapshots and restores.** On btrfs, `dzo` snapshots an instance before
  upgrades, mod updates and wipes, prunes old ones by a retention policy, and
  restores the whole instance or a single path.
- **Monitoring and notifications.** Prometheus `/metrics`, an Icinga/Nagios
  check, and Discord notifications for restarts, failed renders and failed
  downloads.
- **An admin API, a web interface and a live map.** See who is online, send a
  message, teleport a player, give an item, repair or delete a vehicle, and
  watch players and vehicles on the map. The in-game part is the `dzo-admin`
  servermod, which only connects out to `dzo`.
- **A built-in BattlEye RCon client.** Used for the graceful restart, `dzo rcon`
  and the player events.

The full documentation, including the quick start, the configuration reference
and the operations guide, is at
**<https://bzed.github.io/dayz-server-operator/>**. For the first setup there is
also a [two-page cheat sheet (PDF)](docs/cheatsheet/dzo-cheatsheet.pdf).

## Install

**From the apt repository** (Debian 13 "trixie", amd64; signed, rebuilt from `main`):

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

Other ways: a `.deb` or a static binary from the
[CI artifacts](https://github.com/bzed/dayz-server-operator/actions/workflows/ci.yml),
or build it yourself (a recent Go toolchain, see `go.mod`):

```console
$ git clone https://github.com/bzed/dayz-server-operator.git && cd dayz-server-operator
$ make build && ./bin/dzo version                  # a static binary in ./bin
$ sudo apt install debhelper build-essential       # or the Debian package:
$ dpkg-buildpackage -us -uc -b && sudo apt install ../dzo_*.deb
```

## Quick start

Run these as the service user (`sudo -iu dayz`). The
[quick start](https://bzed.github.io/dayz-server-operator/quickstart.html) has
every step with its explanation.

```console
$ dzo setup                                      # directories, container images, the site checkout
$ dzo steam login --user <steam account>         # password and Steam Guard, once
$ dzo product install dayz-stable                # the server build, about 3 GB
$ # write instances/chernarus/instance.yaml in your site repository, push it, then:
$ dzo site pull && dzo site validate
$ dzo instance create chernarus                  # data volume and first mission
$ dzo instance apply chernarus                   # container units and timers
$ dzo start chernarus
$ dzo mod add 1559212036 --instance chernarus    # a workshop mod (Community Framework)
$ dzo restart chernarus --minutes 5              # announce, lock, kick, restart
```

## Everyday commands

```console
$ dzo status                                     # every instance: state, players, health, restarts
$ dzo logs chernarus -f                          # the server console
$ dzo mod update chernarus                       # new mod versions, applied at the next restart
$ dzo backup create chernarus && dzo backup list chernarus
$ dzo restore chernarus <snapshot>               # or one path of it

$ dzo token create web --role operator --delegate > /var/lib/dzo/secrets/web.token
$ dzo serve &                                    # /api/v1 and the endpoint of dzo-admin
$ dzo web                                        # http://127.0.0.1:8081
$ dzo player list chernarus
$ dzo player msg chernarus all "restart in 5 minutes"
$ dzo rcon exec --instance chernarus players

$ dzo servercfg diff old/serverDZ.cfg new/serverDZ.cfg
$ dzo mod cfgpatches mods/@MyMod/addons/*.pbo    # CfgPatches and requiredAddons of a mod
```

`dzo --help` and `dzo <command> --help` list every flag.

## Status

`dzo` runs real servers today, but it is young. The sections below say plainly
what has been run against the real thing, what is not built, and what has not
been checked yet.

### Run for real

On a Debian 13 development host (rootless podman 5.4.2, a user systemd, btrfs)
with a real Steam account, and on a workstation with the DayZ client:

- **Servers.** DayZ 1.29 (stable) and 1.30 (experimental) run in the runtime
  image from the generated quadlets, side by side, with workshop mods (CF-Test
  on experimental) and `dzo-admin`; builds and mods come from steamcmd by `dzo`.
  `@<workshop id>` directories work as mod folders, the Steam login and the
  workshop path layout match what the code expects, and the process name the
  health check looks for is `enfMain`.
- **dzo-admin with a real client**, on both 1.29 and 1.30
  (`scripts/test-client.sh`, a headless DayZ client against a local server and
  `dzo serve`): the player list, messages in all three styles, teleport (and its
  refusal in a vehicle), spawning an item into the inventory, the hands and the
  ground, an unknown class, the crew of a vehicle, class watch rules on items,
  vehicle repair and delete.
- **The web interface and the map** against a live server with a player, using
  Debian's own `libjs-htmx` 2.0.4 and `libjs-leaflet` 1.7.1 (trixie).
- **The Central Economy mission** is fetched from Bohemia's repository by branch or
  by bare commit id, rendered, applied in place, and booted by `dzo test boot`
  (also with CF-Test on the experimental server).
- **Reading mod configs.** `dzo mod cfgpatches` reads the `CfgPatches` of about
  4,900 PBOs of real workshop mods (rapified `config.bin`, LZSS-compressed
  entries, per-folder configs, plain `config.cpp`) and agrees with armake2 on a
  random sample. Running it is what found and fixed the decoder bugs.
- **Backups** on btrfs, as the unprivileged user, on kernel 7.x and Debian 13's 6.12.
- **Monitoring**: the exporter runs on the development host against real systemd
  and podman, Prometheus metrics and `/status` included.
- **Container images and setup**: both images build on podman 5.4.2 and 5.8, the
  game server runs in them, and `dzo setup` ran on the development host.

### Not built yet

- Web users with passwords and TOTP (the web interface expects an
  authenticating reverse proxy today).
- The player and ban database, and the database backup that goes with it.
- One-off restart timers and scheduled broadcasts.
- A web page for backups.
- Downloading map tiles from the client depot automatically (you point `dzo` at
  the client's world PBO).
- Golden-master tests for maps whose mission is not Bohemia's (spike S0 compared
  three branches; see [MILESTONES.md](MILESTONES.md) and the
  [spikes page](docs/spikes.rst)).
- A size-plausibility check of downloaded workshop items, and a check of real
  workshop PBOs for their prefix.
- The `compat.yaml` boot-test gate for the servermods (`make servermods` writes
  the file; `server_build` stays empty until the boot test fills it).

### Limits worth knowing

- `PlayerIdentity` has no IP address, so `dzo-admin` cannot report one; IPs
  come from RCon.
- The restart counter in the metrics is systemd's and has no reasons.
- A few workshop mods ship an unreadable `config.cpp` (obfuscated, or built from
  macros): `dzo mod cfgpatches` reports an error for 10 of the ~4,900 PBOs. It is
  only a diagnostic and does not decide the load order, which is the order of the
  `mods` list.
- The package depends on Debian's `libjs-htmx` and `libjs-leaflet`. On trixie
  that is htmx 2.0.4, which the pages were written for; the htmx 4 beta of
  forky and sid has not been tried.
- The world sizes of `sakhal` and of unknown maps without tiles are guesses.

### Needs live verification

Things that could not be checked yet, roughly in the order they would bite:

- **BattlEye's player events** (`internal/battleye`'s connect, GUID, chat, kick
  and disconnect patterns, spike S4). They need a client with BattlEye, which the
  headless test client is not. The `players` output, unknown commands, `#lock`,
  `#unlock`, `#kick`, `#shutdown`, the keep-alive answers and the behaviour on a
  server restart are confirmed (see the [spikes page](docs/spikes.rst)).
- **steamcmd's failure paths.** A real login, server install, workshop
  download and forced refresh have worked. The wording of the other prompts
  (wrong password, Steam Guard by mail or by mobile confirmation, rate
  limiting) and of failed jobs is from community reports.
- **The weekly image refresh.** Its command rebuilds both images on podman
  5.4.2, but the timer has not fired by itself yet, and the unit on the
  development host predates a fix: the generated unit lacked `--images-dir` and
  `--config`, which broke it on any host with a non-default layout.
- **`dzo test boot` in some modes**: `--container`, `cfggameplay.json` (the
  server only reads it with `enableCfgGameplayFile = 1`) and Windows. It has
  passed on 1.29 and 1.30, on a good instance, and fails on a mission script
  error, an XML file that does not parse and a missing `<ce folder>`.
- **The web interface's buttons in a browser.** The pages, the map and the API
  actions behind the buttons work; the htmx forms were not clicked.
- **Other podman versions** than 5.4.2 and 5.8 for the quadlet keys
  (`HealthStartup*`, `HealthOnFailure=kill`, `Notify=healthy`, `StopTimeout`,
  `Volume=…:O`), which spike S6 confirmed on 5.4.2.

## Developing

```console
$ make lint      # gofmt, go vet, golangci-lint
$ make reuse     # SPDX/REUSE licence header check
$ make test      # unit tests
$ make cover     # tests + the 85% coverage gate (D20)
$ make licenses  # dependency licence allow-list check (D15)
```

`.github/workflows/ci.yml` runs all of the above on every push, plus
cross-compiled binary and `.deb` builds, and uploads coverage and JUnit test
results to Codecov. Tests that need a real DayZ server and client
(`make test-findfile`, `make test-client`) run on a development machine only; see
the [development guide](docs/development.rst) and the
[testing strategy](IMPLEMENTATION_PLAN.md#c15-testing-strategy-and-ci-d20-d21).
The full design, with decisions, legacy analysis and the phased roadmap, is in
[`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md).

<details>
<summary>The packages</summary>

| Package | What it does |
|---|---|
| `cmd/dzo` | The CLI that wires everything together |
| `internal/config` | Loads and validates `/etc/dzo/config.yaml` (data paths, products, notification targets) |
| `internal/site` | The site repository's schema types (`site.yaml`, `instance.yaml`, overlays) and its git plumbing |
| `internal/resolve` | Combines `config.yaml`, the site checkout and the cache into one resolved instance, including its quadlet spec (`dzo instance show`) |
| `internal/instance` | One instance's lifecycle: the failed-render gate, quadlet and timer materialization, systemd start/stop/restart, the BattlEye lock/kick graceful restart |
| `internal/quadlet` | Renders podman quadlet `.container` units (health checks, networking, mounts, the start-rate limit) |
| `internal/mission` | The mission render/apply pipeline: pristine fetch, staging, apply plan, atomic writes with a file history |
| `internal/ce` | JSON and XML merge primitives for mission, mod and overlay data, and `<ce folder>` registration |
| `internal/servercfg` | Parses and writes `serverDZ.cfg` and diffs two versions |
| `internal/runfiles` | The keys, `serverDZ.cfg` and BattlEye config an instance needs besides its mission |
| `internal/cache` | The immutable, generation-based download cache with atomic activation and safe garbage collection |
| `internal/steam` | The steamcmd login state machine (pty-driven) and a non-interactive mode for scheduled jobs |
| `internal/product` | Update scheduling, the Steam Web API freshness client, the steamcmd job runner and the install flow for server builds, workshop mods and local servermods |
| `internal/moddeps` | Reads `CfgPatches` and `requiredAddons` from a PBO or a `config.cpp` (diagnostics) |
| `internal/battleye` | The native BattlEye RCon client: login, commands, multi-packet answers, the event stream |
| `internal/a2s` | The Valve A2S server-query client |
| `internal/health` | `dzo health startup` and `live`: process, A2S and optional RCon probes |
| `internal/hooks` | Runs the extension-point scripts (`pre_start`, `post_stop`, …) with a `DZO_*` environment |
| `internal/notify` | Discord notifications, per-event templates and event coalescing |
| `internal/monitor`, `internal/exporter` | Prometheus `/metrics`, JSON `/status`, Icinga/Nagios checks, served over HTTP or TLS with mutual TLS, a bearer token or an IP allow-list |
| `internal/btrfs`, `internal/backup` | Snapshots by ioctl as the unprivileged user; the snapshot index, retention policy and full or partial restore |
| `internal/admin`, `internal/api`, `internal/apiclient`, `internal/serve` | The operator side of `dzo-admin` (protocol, hub, audit log, spawn limits), the `/api/v1` JSON API with scoped tokens, its Go client, and `dzo serve` |
| `internal/web`, `internal/maptiles` | The web interface (server-rendered pages, htmx, a Leaflet map) and the map tiles built from the game's world PBOs |
| `internal/servermods` | `make servermods`: packs `servermods/*/src/*` into reproducible PBOs with [WoozyMasta/pbo](https://github.com/WoozyMasta/pbo) (MIT) and writes `compat.yaml` |
| `internal/boottest` | `dzo test boot`: boots a real DayZServer from a disposable tree and checks the logs |
| `internal/setup` | `dzo setup`: data directories, the two container images (`images/`), the Steam login hand-off, the site clone and the weekly image refresh timer |
| `servermods/dzo-admin` | The Enforce Script servermod (a submodule): state push, admin actions, marker API |

</details>

## Licence

AGPL-3.0-or-later. This repository follows the [REUSE](https://reuse.software/)
specification — every file carries an SPDX header, and licence texts live
under [`LICENSES/`](LICENSES/). See [`LICENSE`](LICENSE) and
[`debian/copyright`](debian/copyright) for details.

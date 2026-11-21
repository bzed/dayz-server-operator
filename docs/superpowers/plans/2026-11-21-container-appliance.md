<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Container Appliance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** dzo runs in a Debian trixie container with systemd as init and manages the game servers as containers of the host user's podman, through the host's podman user socket mounted into it.

**Architecture:** A new config key `podman.mode: remote` makes `dzo units sync` write each instance as a plain user unit (`Type=notify`) whose `ExecStart` is a new hidden command, `dzo instance supervise <name>`. The supervisor runs or re-attaches to the container through the socket, reports readiness when the container is healthy, and stops it on SIGTERM. Its `podman run` arguments come from the same `quadlet.ContainerSpec` the quadlet is rendered from, and a parity test keeps the two in step. Everything else (timers, serve, web, exporter, `podman inspect` calls) works unchanged once the image switches podman to remote.

**Tech Stack:** Go, systemd user units, podman remote API via the `podman` client, Debian trixie image, GitHub Actions + ghcr.io.

**Spec:** `docs/superpowers/specs/2026-11-21-container-appliance-design.md`

**Depends on:** `docs/superpowers/plans/2026-11-21-linux-distributions.md` Part 1 (embedded web assets: the image installs no `libjs-*`) and Tasks 4-5 (the `stable` apt suite the image's source names, and `checkPodmanVersion`). Tasks 1-6 here can start before that plan is done; Task 7 cannot.

## Global Constraints

- `podman.mode: local` (the default) must render byte-identical units to today: `go test ./internal/resolve/` golden files do not change in any task.
- Host podman **5.4** or newer; the client in the image is trixie's **5.4.2**.
- Paths dzo mounts into game containers must exist under the **same absolute path** on the host and in the appliance.
- No new Go module dependency (sd_notify is a few lines over a unix datagram socket).
- No `panic` in the supervisor; every goroutine stops with the context; tests use fake clocks or `require.Eventually`, never `time.Sleep`.
- A command that is not a game-server start (`dzo units sync`, the package postinst) never starts or stops an instance.
- Every new config key: in the `config.yaml` example of `docs/configuration.rst` with its default, and in prose (values, default, error, when it takes effect) in the same commit.
- `debian/changelog` entry for packaging changes; `make check` before every push.

## Review Focus

- **Switching `podman.mode` with an instance running.** Expected: `dzo units sync` swaps the `.container` for the `.service` (or back) without stopping the server; the running server keeps its old unit until its next restart, like a pending update (the `Hold` logic). Test in Task 3.
- **The socket is down when a supervised unit starts** (host `podman.socket` not enabled). Expected: the unit fails fast with `podman socket … unreachable` in the journal, not a hang until `TimeoutStartSec`. Test in Task 5.
- **The container exits during the startup health phase** (bad mission, crash at boot). Expected: the supervisor exits with the container's exit code before readiness; systemd counts a failed start, and the crash summary runs. Test in Task 5.
- **A container of the same name left over from a crashed appliance, but stopped** (`Exited`). Expected: the supervisor starts a fresh one (`--replace`), it does not attach to a dead container. Test in Task 5.
- **`apt upgrade` of dzo inside the appliance with servers running.** Expected: running servers keep their health-check binary (the old inode stays mounted), the next restart uses the new one, and no server is restarted by the upgrade. Test in Task 4 (atomic replace) and on the checklist in Task 9.

---

### Task 1: Spike — a long-lived attach through the socket (gate)

Manual, on the dev host, before any code. The design's biggest risk.

- [ ] **Step 1:** As in the spike in the spec: start `podman.socket` for your user, run a throwaway trixie container with systemd as init, the socket mounted at `/run/host-podman.sock`, and a stand-in server (`nc -u -l -k <port>`) started by the 20-line prototype supervisor from the spec's spike (`podman run --replace --rm` in the foreground). Leave it for **24 hours**.
- [ ] **Step 2:** During that time: `systemctl --user restart podman.service` on the host once; check whether the attach survives, whether the supervisor sees EOF, and what `podman ps` shows.
- [ ] **Step 3:** Also check that `podman.service` on the host is not stopped for idleness while an attach is open (`systemctl --user status podman.service` after 1 h).
- [ ] **Step 4: Record** the results in the spec's "Risks" section and commit `docs: long-lived attach findings for the appliance`. If the attach does not survive a `podman.service` restart, Task 5's re-attach covers it; if the server container itself is stopped by a `podman.service` restart, stop and bring it to the user.

### Task 2: The `podman.mode` config key

**Files:**
- Modify: `internal/config/config.go` (new `Podman` struct on `Config`, default and `Validate`)
- Test: `internal/config/config_test.go`
- Modify: `docs/configuration.rst` (example and prose)

**Interfaces:**
- Produces: `type PodmanMode string` with `PodmanLocal PodmanMode = "local"`, `PodmanRemote PodmanMode = "remote"`; `Config.Podman.Mode PodmanMode` (`yaml:"podman"` → `mode`), default `local`; `func (c *Config) Remote() bool`.

- [ ] **Step 1: Failing tests** `TestPodmanModeDefault` (empty config → `local`, `Remote()` false), `TestPodmanModeRemote`, `TestPodmanModeInvalid` (`mode: docker` → error containing `podman.mode: must be "local" or "remote", got "docker"`).
- [ ] **Step 2: Run** `go test ./internal/config/ -run PodmanMode -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Docs.** Example: `podman:` / `  mode: local   # default`. Prose: `local` runs instances as quadlets of this host's podman; `remote` (the container appliance, :doc:`container`) runs them as plain units supervised by `dzo instance supervise`, through the podman of `CONTAINER_HOST`; a change takes effect at the next `dzo units sync` and, for a running instance, at its next restart; nothing is lost. `docs/container.rst` does not exist yet: link it in Task 9 and keep plain text here for now so `make docs` passes.
- [ ] **Step 6: Commit** `config: podman.mode selects quadlets or supervised units`.

### Task 3: Render the run arguments and the supervised unit

**Files:**
- Create: `internal/quadlet/run.go`, `internal/quadlet/run_test.go`
- Modify: `internal/quadlet/container.go` (extract the `[Service]` writer so both renderers share it)
- Modify: `internal/units/units.go:241-262` (`Desired`), `:340-420` (`Sync`: hold, removal, enable)
- Modify: `internal/instance/lifecycle.go` (add `Enable`)
- Test: `internal/units/units_test.go`, `internal/instance/lifecycle_test.go`

**Interfaces:**
- Consumes: `config.Config.Remote()` (Task 2).
- Produces:
  - `func RunArgs(spec ContainerSpec) ([]string, error)` — the arguments after `podman` for `run`: `run --name <Name> --replace --rm` then, mirroring `RenderContainer` key by key: `--network host` | `-p` per port; `-v <src:dst[:O|:ro]>` per volume in order; `-e K=V` sorted; `--workdir`; `--health-cmd`, `--health-interval`, `--health-retries`, `--health-on-failure`, `--health-startup-cmd`, `--health-startup-interval`, `--health-startup-retries`, `--health-startup-success 1`; `--ulimit core=0` unless `CoreDumps`; `--memory`; `--cpus`; `--stop-timeout`; then `Image` and `Exec...`. No `--cgroups`, no `--sdnotify`, no `-d`.
  - `func RenderSupervised(spec ContainerSpec, execStart string) (string, error)` — `[Unit]` as `RenderContainer`'s; `[Service]` with `Type=notify`, `NotifyAccess=main`, `ExecStart=<execStart>`, `KillMode=mixed`, and every line `RenderContainer` writes in `[Service]` (pre-start, `ExecStop`, `ExecStopPost`, `Restart=always`, `SuccessExitStatus=255`, timeouts, restart delay); `[Install] WantedBy=default.target`.
  - `func (l Lifecycle) Enable(ctx context.Context, units ...string) error` — `systemctl --user enable <units>` (no `--now`).
  - In remote mode `Desired` returns `File{Dir: Systemd, Name: "dzo-<name>.service", Content: …, Install: true}` with `execStart = o.cmd("instance", "supervise", name)`; new field `File.Install bool` makes `Sync` call `lc.Enable` for it.

- [ ] **Step 1: Failing tests** in `internal/quadlet/run_test.go`:
  - `TestRunArgsGolden`: for a spec with every field set, the exact `[]string` (write it out in the test).
  - `TestRunArgsMatchesQuadlet`: parse the `[Container]` section of `RenderContainer(spec)` for the specs behind `internal/resolve/testdata/alpha.golden` and `beta.golden` (build them via `resolve.Resolve` in the test, like `resolve_test.go:85`) and assert, for each key, the matching argument: every `Volume=` value appears as a `-v` value in the same order; `Environment=`, `Network=`, `PublishPort=`, `Health*=`, `Ulimit=`, `PodmanArgs=` values, `StopTimeout=`, `WorkingDir=`, `Image=`, `Exec=` (unquoted) all present. A `[Container]` key the test does not know fails the test, so a new quadlet key forces a decision.
  - `TestRenderSupervisedService`: the `[Service]` section of `RenderSupervised` contains every `[Service]` line of `RenderContainer` for the same spec, plus `Type=notify`, `NotifyAccess=main`, `ExecStart=/usr/bin/dzo instance supervise alpha`.
- [ ] **Step 2: Failing tests** in `internal/units/units_test.go`:
  - `TestDesiredRemoteWritesService`: remote config → no `.container` file, one `dzo-alpha.service` in the systemd dir with `Install: true`.
  - `TestSyncModeSwitchKeepsRunningInstance`: start with `dzo-alpha.container` in the quadlet dir, sync in remote mode → the `.container` is removed, the `.service` written, the fake systemctl log contains `enable dzo-alpha.service` and **no** `disable` or `stop` of `dzo-alpha.service`; and the reverse switch (remove the `.service`, write the `.container`) also never calls `disable dzo-alpha.service`.
  - `TestSyncRemoteHoldsPendingUpdate`: as the existing hold test, in remote mode (the hold looks in the systemd dir).
- [ ] **Step 3: Run** `go test ./internal/quadlet/ ./internal/units/ -v`. Expected: FAIL.
- [ ] **Step 4: Implement.** In `Sync`: the `held` key trims `.service` as well as `.container`; the `gone` list must never contain an instance's `dzo-<name>.service` (instance names from `tree.InstanceNames()`); call `lc.Enable` for `Install` files after `daemon-reload`.
- [ ] **Step 5: Run** the tests and `go test ./internal/resolve/`. Expected: PASS, golden files unchanged.
- [ ] **Step 6: Commit** `units: in podman.mode remote, instances are supervised units with the quadlet's podman arguments`.

### Task 4: The health-check binary on the host's side

**Files:**
- Modify: `internal/resolve/resolve.go:52,474-480` (mount source), `internal/units/units.go` (`Sync` copies the binary)
- Test: `internal/resolve/resolve_test.go`, `internal/units/units_test.go`
- Modify: the spec, section "The health-check binary" (record the deviation below)

**Interfaces:**
- Produces: `func HealthBinary(cfg *config.Config) string` in `internal/resolve` — `cfg.Binary` in local mode, `<paths.data>/bin/dzo` in remote mode; `buildQuadlet` mounts it at `/usr/local/bin/dzo`. `units.Sync` in remote mode copies `cfg.Binary` to `HealthBinary(cfg)` when the content differs, by writing a temp file in the same directory and renaming it (mode `0o755`).

Deviation from the spec, decided here: not a versioned file per release. A file bind mount pins the inode, so a running container keeps the old binary after the rename, exactly as with `/usr/bin/dzo` on a native install, and nothing needs cleaning up.

- [ ] **Step 1: Failing tests** `TestHealthBinaryRemote` (remote config with `paths.data: /srv/d` → the alpha spec's first volume source is `/srv/d/bin/dzo`), `TestSyncCopiesHealthBinary` (remote, `cfg.Binary` a temp file → `<data>/bin/dzo` has its content and mode 0755; a second sync with the same content does not rewrite it, compare `os.SameFile` of the stat before and after), `TestSyncReplacesHealthBinaryAtomically` (open the old file, sync a changed binary, the open handle still reads the old content).
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Local mode is untouched (golden files unchanged).
- [ ] **Step 4: Run** the tests and `go test ./internal/resolve/`. Expected: PASS.
- [ ] **Step 5: Commit** `units: in remote mode, the game containers mount a copy of dzo below paths.data`.

### Task 5: The supervisor

**Files:**
- Create: `internal/supervise/supervise.go`, `internal/supervise/notify.go`, `internal/supervise/supervise_test.go`
- Modify: `cmd/dzo/instance.go:48` (register `newInstanceSuperviseCmd()`, `Hidden: true`)
- Create: `cmd/dzo/supervise.go`

**Interfaces:**
- Consumes: `quadlet.RunArgs` (Task 3), `resolve.Resolve`.
- Produces:
  - `type Options struct { Podman string; Name string; RunArgs []string; StopTimeout time.Duration; Poll time.Duration; Notify func(state string) error; Stdout, Stderr io.Writer; Clock func() time.Time }`
  - `func Run(ctx context.Context, o Options) (exitCode int, err error)`:
    1. `podman version --format {{.Server.Version}}` must answer within 10 s, else `err` `podman socket unreachable: …` (exit code 1).
    2. `podman inspect -f {{.State.Running}} <Name>`: `true` → `podman attach --no-stdin <Name>`; otherwise `podman <RunArgs...>` (foreground). Its stdout/stderr go to `o.Stdout`/`o.Stderr`.
    3. A goroutine polls `podman inspect -f {{.State.Health.Status}} <Name>` every `Poll` (default 2 s) until `healthy`, then `Notify("READY=1")` once.
    4. When ctx is cancelled (SIGTERM): `podman stop -t <StopTimeout seconds> <Name>`, then wait for the child.
    5. When the attached/run child exits while `podman inspect` still says `Running=true` (the connection broke, not the server), attach again; at most once per `Poll`, and give up with an error after 10 consecutive failures.
    6. Otherwise return the child's exit code.
  - `func SdNotify(state string) error` in `notify.go`: writes `state` to `$NOTIFY_SOCKET` (unix datagram; `@` prefix = abstract), no-op when unset.
  - `dzo instance supervise <name>`: resolves the instance, `RunArgs(inst.Quadlet)`, runs `supervise.Run` with signal-cancelled context, exits with the code.

- [ ] **Step 1: Failing tests** with a fake `podman` shell script (pattern: `internal/instance/lifecycle_test.go:25`) whose answers come from files in a temp dir, and `Notify` recording calls:
  - `TestSuperviseStartsAndNotifiesWhenHealthy`: inspect says not running → the fake logs a `run --name dzo-a …` call; health answers `starting`, then `healthy` → exactly one `READY=1`.
  - `TestSuperviseAttachesToRunning`: inspect says `true` → `attach --no-stdin dzo-a`, no `run`.
  - `TestSuperviseExitedContainerIsReplaced`: inspect says `false` (an `Exited` leftover) → `run … --replace`.
  - `TestSuperviseStopsOnCancel`: cancel ctx → `stop -t 90 dzo-a` with `StopTimeout: 90s`, and `Run` returns the child's code.
  - `TestSuperviseExitBeforeHealthy`: the run child exits 3 while health is `starting` → returns 3, no `READY=1`.
  - `TestSuperviseReattachOnBrokenConnection`: the child exits while inspect still says running → a second `attach`; after the server stops, the code of the last child is returned.
  - `TestSuperviseSocketDown`: `version` fails → error contains `podman socket unreachable`, returns within the test's deadline (fake clock, no sleep).
  - `TestSdNotify`: a listening unixgram socket in `t.TempDir()`, `NOTIFY_SOCKET` set → it receives `READY=1`.
- [ ] **Step 2: Run** `go test ./internal/supervise/ -race -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** with `-race`. Expected: PASS.
- [ ] **Step 5: Integration test against real podman** `TestSuperviseRealPodman`, build tag `appliance`, skipped unless `DZO_APPLIANCE_PODMAN=1`: a `debian:trixie-slim` container `sleep infinity` with `--health-cmd true`, supervise it through `CONTAINER_HOST`, check `READY=1`, cancel, container gone. Add `make test-appliance` running it. Run it on this host with the user socket. Expected: PASS.
- [ ] **Step 6: Commit** `instance: dzo instance supervise runs a game container through a remote podman`.

### Task 6: `dzo setup` and the boot test in remote mode

**Files:**
- Modify: `internal/setup/setup.go` (`r.remote(ctx)` step when `Cfg.Remote()`), `cmd/dzo/testboot.go:106-109` (run directory)
- Test: `internal/setup/setup_test.go`, `cmd/dzo/testboot_test.go` (or where testboot is tested)
- Modify: the spec, "The boot test and the debug shell" (correct it: the tree is mounted, the token directory is not)

**Interfaces:**
- Produces, in remote mode, three setup checks, each `ok` or `error`:
  - `podman version --format {{.Server.Version}}` ≥ 5.4 (reuse `checkPodmanVersion` from the distributions plan's Task 5 if it exists; otherwise define it here with the same signature `func checkPodmanVersion(out string) error`).
  - Same path: write `<paths.data>/.dzo-setup-<random>` with random content, then `podman run --rm -v <paths.data>:<paths.data>:ro localhost/dzo-runtime:latest cat <file>` must print that content; error `paths.data <dir> is not mounted at the same path on the host: …`.
  - Host systemd: `podman info --format {{.Host.CgroupManager}}` is `systemd`; error `the host's podman does not use systemd: container health checks never run there, and no instance would become ready`.
- The boot test's run directory is `<paths.data>/boottest/<stamp>` in remote mode (today `~/.cache/dzo/boottest/…`, which the host cannot see).

- [ ] **Step 1: Failing tests**: `TestSetupRemoteChecks` with a fake `Exec` for the ok case and each of the three errors (exact messages above); `TestBootTestRunDirRemote` (remote config → run dir below `paths.data`).
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `setup: check the remote podman, the shared data path and the host's systemd`.

### Task 7: The appliance image

**Files:**
- Create: `images/appliance/Containerfile`, `images/appliance/containers-remote.conf`, `images/appliance/environment.d-podman.conf`, `images/appliance/config.yaml`
- Modify: `.github/workflows/ci.yml` (job `appliance-image` after `package-install`; push on `main` and tags; a weekly `schedule` rebuild), `debian/changelog`

**Interfaces:**
- Consumes: the `dzo-deb` artifact.
- Produces: `ghcr.io/bzed/dzo:<version>` and `:latest`. Inside: user `dayz` UID/GID **1000** created before the package is installed (sysusers leaves it), lingering on, `/etc/containers/containers.conf.d/50-dzo-remote.conf` (`[engine] remote = true`), `/etc/environment.d/50-dzo-podman.conf` (`CONTAINER_HOST=unix:///run/host-podman.sock`, read by the user manager), dzo's apt source and keyring under `/etc/apt/sources.list.d/dzo.sources` (`Suites: stable`), `/etc/dzo/config.yaml` with `podman.mode: remote` and `paths.data: /srv/dzo`, `STOPSIGNAL SIGRTMIN+3`, `CMD ["/sbin/init"]`.

- [ ] **Step 1: Write the Containerfile** (`debian:trixie`; `systemd-sysv dbus-user-session libpam-systemd`; contrib/non-free/i386 and the steamcmd licence pre-seed as in `images/steamcmd/Containerfile`; `apt install ./dzo_*.deb`).
- [ ] **Step 2: Build and boot** locally with the run options from Task 8's quadlet. Expected: `podman exec dzo systemctl is-system-running` → `running`; `podman exec -u dayz dzo env` under the user session shows `CONTAINER_HOST`; `systemctl --user show-environment` (as dayz) contains it too; `podman exec -u dayz dzo podman ps` lists the host's containers.
- [ ] **Step 3: CI job** builds from the run's `.deb`, boots the image, runs `dzo version` and `dzo setup --dry-run` as dayz inside, pushes on `main` (with `packages: write`). Weekly rebuild via `schedule:`.
- [ ] **Step 4: Commit** `images: the dzo appliance (trixie, systemd, remote podman)`.

### Task 8: Running it from the host

**Files:**
- Create: `contrib/appliance/dzo.container` (host quadlet), `contrib/appliance/dzo` (host wrapper script)
- Modify: `debian/dzo.install` is **not** changed (these are for the host, not the image); add them to `REUSE.toml` if they cannot carry headers

**Interfaces:**
- Produces:
  - `dzo.container` for the host user: `Image=ghcr.io/bzed/dzo:latest`, `ContainerName=dzo`, `Network=host`, `UserNS=keep-id:uid=1000,gid=1000`, `PodmanArgs=--systemd=always`, `SecurityLabelDisable=true`, `Volume=%t/podman/podman.sock:/run/host-podman.sock`, `Volume=/srv/dzo:/srv/dzo` (the data path, same on both sides, the example uses `/srv/dzo`), `Volume=dzo-etc:/etc/dzo`, `Volume=dzo-home:/var/lib/dzo`, `Volume=dzo-journal:/var/log/journal`, `StopTimeout=` long enough for the longest instance stop (document: sum of `stop.timeout` + 60 s), `[Unit] Requires=podman.socket After=podman.socket`.
  - `dzo` wrapper: `exec podman exec $tty -u dayz -e XDG_RUNTIME_DIR=/run/user/1000 -w /var/lib/dzo dzo dzo "$@"`, `$tty` = `-it` when stdin is a terminal.

- [ ] **Step 1: Write both.**
- [ ] **Step 2: End-to-end on this host:** install the quadlet for your user, `systemctl --user enable --now podman.socket`, start `dzo.service`, then via the wrapper: `dzo setup` (all three remote checks `ok`), a site with one instance, `dzo instance create`, `apply`, `start`. With a real DayZ server build (see the `dayz-dev` skill for the local server, mind the dev-host port range 40000-45000 and `/var/tmp` for large files): the unit becomes active when healthy, `dzo status` shows it, `podman ps` **on the host** shows `dzo-<name>`, a client can join.
- [ ] **Step 3: The update path:** inside, `apt install --reinstall` of a newer `.deb` (or the same with a bumped version) with the server running. Expected: server not restarted (same `StartedAt`), `dzo-web`/`dzo-serve` restarted, `dzo restart <name>` afterwards uses the new binary.
- [ ] **Step 4: Unclean cases:** `podman kill dzo` (the appliance) with a server running, start it again. Expected: the supervisor re-attaches, the server's `StartedAt` is unchanged. `podman stop dzo`: servers stop cleanly first (RCon shutdown in the journal).
- [ ] **Step 5: Commit** `contrib: host quadlet and wrapper for the dzo appliance` with the results of Steps 2-4.

### Task 9: Documentation

**Files:**
- Create: `docs/container.rst`
- Modify: `docs/index.rst` (toctree), `docs/installation.rst` (link), `README.md` (a "In a container" install line), `docs/configuration.rst` (link `podman.mode` to :doc:`container`), `docs/operations.rst` (the unit of an instance: supervised in remote mode), `docs/cli.rst` if hidden commands are listed there (they are not shown by `gen-man`; check), `docs/development.rst` (`make test-appliance`), `debian/changelog`

- [ ] **Step 1: Write `container.rst`**: requirements (host podman ≥ 5.4 with systemd, `podman.socket`, lingering, cgroup delegation, btrfs for backups), the security note (the socket is full control over that user's containers and files: use a dedicated host user), running (the quadlet and the wrapper from Task 8), the same-path rule, updating with `apt` inside, what differs from a native install (servers are host containers; hooks run inside the appliance with its tools; output while no supervisor is attached is only in the host's `podman logs`), the three setup checks and what to do for each.
- [ ] **Step 2: Run** `make docs`. Expected: no warnings. Search the docs for `podman.mode`: it turns up in the example and in prose.
- [ ] **Step 3: Commit** `docs: running dzo as a container appliance`.

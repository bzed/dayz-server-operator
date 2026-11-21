<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# dzo in a podman container (the appliance) — Design

Date: 2026-11-21
Status: draft, waiting for approval
Companion: `2026-11-21-linux-distributions-design.md` (native packages)

## Intent and why

A user wants to run dzo on a host where only containers are run and the base system does
not matter. The appliance is the tested Debian system in a container: Debian trixie,
systemd as init, the `dzo` package, steamcmd. It manages the game servers through the
host user's podman socket, mounted into it, so the game servers are ordinary containers
of the host's podman, next to the appliance and not inside it.

## Goals

- One image that contains everything dzo needs. On the host: rootless podman, the user's
  podman socket, and a data directory.
- Inside the container dzo behaves as on Debian: the same commands, timers, services,
  `systemctl --user`, `dzo logs`, web UI, exporter.
- dzo is updated inside the running container with `apt`, exactly as on a Debian host.
  The container does not have to be restarted for an update, and game servers keep
  running through it.
- Game servers are visible to and controllable by the host's podman.

## Non-goals

- Docker as the host engine, Kubernetes, podman machine on macOS or Windows.
- Rootful podman on the host.
- A host without systemd. Podman runs container health checks from systemd timers on the
  host, and dzo's start, restart-on-unhealthy and readiness all depend on them.
- Game servers surviving a stop of the appliance (see Stopping).
- Nested podman (see Approaches).

## Anti-goals

- A change to how instances start on a native install. Quadlets stay the only start path
  there; nothing in this design may alter a rendered quadlet.
- An appliance that looks healthy while servers run unsupervised.

## Constraints

- The host podman must be 5.4 or newer (the features dzo's containers use live on the
  server side). The client in the image is trixie's 5.4.2.
- The appliance holds the host user's podman socket, which is full control over that
  user's containers and, through bind mounts, that user's files. Run it as a dedicated
  host user. The docs say this plainly.
- Bind mounts are resolved by the host. Every path dzo mounts into a game server must
  exist under the same absolute path on the host and in the appliance.

## What the spikes showed (2026-11-20, one host: Debian, kernel 7.3, podman 5.8.6)

Stand-in containers were used (a UDP listener with dzo-style health checks, mounts and
limits), not a real DayZ server and not a real dzo installation.

**Host socket (chosen)**, the user's `podman.socket` mounted with `-v`, systemd as init
in a trixie container, the host user mapped to the inner service user with
`--userns=keep-id:uid=…,gid=…`:

- `podman ps`, `run` and an image build work through the socket (client 5.4.2, server
  5.8.6). No extra capabilities are needed on the appliance container.
- An unchanged quadlet does not start:
  `Error: the option --cgroups="split" is not supported in remote mode`. Quadlet always
  adds that flag. With the flag removed by hand the container starts but the unit fails
  with `protocol`, and the container keeps running with nothing supervising it.
- A plain user unit running a 20-line shell supervisor worked: start attached through the
  socket, unit `active` only once the container is healthy, container output in the
  inner user journal, restart after a kill, container removed on stop.
- After a hard kill of the supervisor, the restarted supervisor attached to the still
  running container; the server was not restarted.
- dzo's btrfs tests (subvolume, snapshot, read-only, delete) pass inside such a container
  on a bind-mounted btrfs directory.

**Nested podman (not chosen)**: an unchanged quadlet works, but the outer container needs
`--cap-add SYS_ADMIN`, `--security-opt unmask=ALL` and `--device /dev/net/tun`, an inner
id range (`1:999` plus `1001:64535`; a single id cannot unpack the Debian image), and a
reachable `network-online.target`.

## Approaches

1. **Host socket and a supervisor in dzo** (chosen by the user). Costs a second way to
   start an instance, used only in the appliance. Servers are host containers, the
   appliance needs no extra privileges, and dzo can be updated or even crash without
   touching a running server.
2. **Nested podman.** No dzo code change and the exact tested start path. But the host
   sees one opaque container, the outer container needs `SYS_ADMIN`, and everything that
   stops the appliance stops every server.
3. **Host socket, no systemd inside**, dzo supervising its own timers and services.
   Rejected: it replaces the tested system instead of keeping it, which is the opposite
   of the intent.

## Design

### The image

- `debian:trixie`, plus systemd as init (`systemd-sysv`, `dbus-user-session`,
  `libpam-systemd`), plus the `dzo` package with its dependencies (podman as the client,
  steamcmd, git, patch). CI builds it from the `.deb` of the same run and pushes it to
  `ghcr.io/bzed/dzo` with the version and `latest` tags, after CI has passed on main.
  A weekly rebuild picks up Debian security updates.
- dzo's apt repository is configured in the image, so `apt update && apt upgrade` inside
  the running container updates dzo. The package's postinst already rewrites the units
  and restarts dzo's own services and never a game server; that holds here too.
- The `dayz` user exists with a fixed, documented UID/GID and lingering enabled, so its
  user systemd starts at boot of the container.
- Podman is switched to remote for that user: `containers.conf` with `remote = true` and
  `CONTAINER_HOST` pointing at the mounted socket, in the user's environment and in the
  user manager's environment. Every existing `podman` call in dzo (image build and prune
  in `dzo setup`, `inspect` in the exporter and the CLI, the debug shell, the boot test)
  then reaches the host without a change.
- `config.yaml` in the image sets the appliance mode (below).

### Running it

Documented and shipped in `contrib/`:

- A quadlet for the **host** user (`dzo.container`) and the equivalent `podman run` line.
  What it sets: systemd mode, host networking, the user mapping so that the host user is
  `dayz` inside, the socket mount, the data directory mounted **at the same path** on
  both sides, volumes for `/etc/dzo`, for the service user's home (unit files, podman
  client config) and for `/var/log/journal` (so `dzo logs` history survives a recreated
  container), SELinux label separation off for the socket, and a stop timeout long enough
  for the servers to stop (see Stopping).
- A `dzo` wrapper script for the host that runs `dzo …` inside as the service user with
  the user session's environment and a terminal when there is one, so `dzo status`,
  `dzo steam login` and the rest are typed on the host as on a native install.
- Host prerequisites the page lists: podman ≥ 5.4, `systemctl --user enable --now
  podman.socket`, lingering for the host user, cgroup delegation for memory and CPU
  limits (as on any rootless host), btrfs for the data directory if backups are wanted.

Host networking is required: `dzo-serve`, the web UI and the exporter listen on the
host's loopback as on a native install, RCON reaches the servers on `127.0.0.1`, and the
game servers (host network themselves) reach `serve.mod_listen`.

### Appliance mode in dzo

A new key in `config.yaml` selects how instances are started. *My call on the name:*
`podman.mode`, `local` (default: quadlets, today's behaviour, unchanged) or `remote`.
A bad value is a config error. A change takes effect at the next `dzo units sync`, and
for a running instance at its next restart. It is documented in the `config.yaml`
example and in prose, as AGENTS.md requires.

In `remote` mode:

- **Units.** `dzo units sync` writes `dzo-<name>.service` as a plain unit into the
  systemd user directory instead of `dzo-<name>.container` into the quadlet directory.
  The unit name is the same, so `dzo start/stop/restart`, `dzo logs`, the exporter, the
  web UI and the timers do not change. The `[Unit]` and `[Service]` settings are the ones
  the quadlet carries today (pre-start render and log rotation, hooks, the crash summary
  in `ExecStopPost`, `Restart=always`, `SuccessExitStatus=255`, restart delay, start
  limits, timeouts), with `Type=notify` and `ExecStart=dzo instance supervise <name>`.
  A leftover quadlet of the same instance is removed, and the other way round, so a mode
  switch never leaves two definitions of one unit.
- **One description, two renderers.** The supervisor builds its `podman run` arguments
  from the same `quadlet.ContainerSpec` the quadlet is rendered from. A test renders both
  for the golden instances and compares them: image, name, network, every volume with
  its `:ro`/`:O` option, environment, working directory, command, health settings,
  ulimit, memory and CPU limits and stop timeout must match, so the two paths cannot
  drift apart silently.
- **The supervisor** (`dzo instance supervise <name>`, hidden, written in Go):
  - If the instance's container is already running, it attaches to it. Otherwise it
    starts it with `--replace --rm`, in the foreground, not detached.
  - The container's output is its own output, so it lands in the user journal under the
    unit, where `dzo logs` reads it.
  - It polls the container's health and tells systemd the unit is ready when the
    container is healthy, which is what `Notify=healthy` does on a native install. The
    unit's start timeout applies as it does today.
  - On SIGTERM it stops the container with the instance's stop timeout and waits.
  - It exits with the container's exit code, so `SuccessExitStatus=255` and the crash
    summary's `$SERVICE_RESULT` keep their meaning.
  - If the connection to the socket breaks while the container still runs (the host's
    podman service is socket-activated and may restart), it attaches again instead of
    exiting. Exiting would count as a crash against the unit's start limit.
- **The health-check binary.** Game containers mount the dzo binary for their health
  checks. `/usr/bin/dzo` does not exist on the host, so in remote mode dzo copies its own
  binary (`binary` in `config.yaml`) to a fixed path below the data directory,
  `<paths.data>/bin/dzo`, and mounts that. `dzo units sync` writes the copy before the
  units that name it and only when the content differs: a temporary file in the same
  directory, fsynced, mode `0755` (the host's podman and the container process must be
  able to read and execute it), renamed over the old path. A bind mount pins the inode,
  so a container that is running keeps the binary it started with after `apt upgrade`
  replaced the copy, exactly as a native install keeps `/usr/bin/dzo`; nothing needs
  cleaning up, and no versioned copies are kept.
- **`dzo setup` checks**, each with a clear message: the socket answers and the server is
  podman ≥ 5.4; a marker file written below the data directory is visible at the same
  path in a container the host starts (this catches a wrong mount); the host uses
  systemd (without it health checks never run and no instance would ever become ready).
- **The boot test and the debug shell** run their own containers with mounts. Anything
  they mount must lie below the data directory in remote mode. The boot test does not
  mount its token directory (`os.MkdirTemp` in the system temp directory,
  `internal/boottest/run.go`); dzo reads the tokens and writes the dzo-admin
  `config.json` that uses them into the profiles directory. What its container does mount
  is the run tree (`serverCommand` in `internal/boottest/run.go`), today below
  `~/.cache/dzo/boottest` (`cmd/dzo/testboot.go`), which the host cannot see; in remote
  mode it goes to `<paths.data>/boottest/<stamp>`. The server install and the mod
  directories it mounts too have to be visible on the host at the same paths; the
  builder checks those the same way.

steamcmd runs inside the appliance from the Debian package, with its home under the
secrets directory in the data directory, as on Debian. Hooks from the site repository run
inside the appliance too, so they can only use tools the image has; the page says so.

### Stopping

Stopping the appliance stops the game servers cleanly, like logging out the service user
on a native host would: systemd stops the units, each supervisor stops its container.
*My call:* this, rather than leaving servers running without anything to restart, back
up or announce for them. Updates do not need it (apt inside), and a host reboot ends the
servers anyway.

Attaching to a running container is therefore for the unclean cases: the supervisor or
the whole appliance was killed, the socket connection broke. The spike showed it works
without restarting the server.

### Documentation

A new `docs/container.rst` (requirements, the security note on the socket, running,
updating with apt, what differs from a native install, troubleshooting the three setup
checks), linked from the installation page and the README; `podman.mode` in
`docs/configuration.rst`; the supervisor in `docs/operations.rst` where the unit of an
instance is explained.

### Testing

- Unit tests for the argument renderer (the comparison above) and for the supervisor
  against a fake `podman` script, in the style of the existing fake `systemctl`: start,
  ready on healthy, stop, exit code, attach when running, re-attach on a broken
  connection.
- An end-to-end target in the style of `make e2e-container` that starts the image with a
  real user socket and a stand-in server and replays the spike: start, ready, kill,
  restart, stop, supervisor kill and re-attach, `apt`-style binary replacement with a
  server running. It runs in CI if the runner's podman is new enough, otherwise locally,
  and is on the release checklist either way.
- Manual, with a real DayZ server (CI cannot run one): the checklist of the native
  packages, run in the appliance.
- `make test-client` is not needed by this change itself: it does not touch the mod
  protocol or the player and vehicle actions.

## Left to the builder

- The exact unit template and where the supervisor lives in the packages.
- How the supervisor talks to podman: the `podman` client as dzo does everywhere today is
  the default; the HTTP API directly only if the client gets in the way.
- The UID in the image, the names of the volumes, the wrapper's details.
- How the user manager gets `CONTAINER_HOST` (environment.d was enough in the spike only
  together with `systemctl --user set-environment`; find the one reliable place).

## Risks and open points

- **Long-lived attach.** The spike held an attached connection for seconds, not days.
  The host's `podman.socket` service normally exits when idle; whether a days-long attach
  survives, and what a restart of that service does to it, must be tested before the
  rest is built on it. This is the first task of the plan.
- **Version skew.** Client 5.4.2 against server 5.8.6 worked. A podman 6 host is
  untested.
- **SELinux hosts.** The spike host has none. The socket mount is expected to need label
  separation off; the game containers have the same open question as in the companion
  design.
- **Output while nothing is attached** (between a supervisor death and its restart) is
  not in the inner journal. It is still in the host podman's log of the container.
- **Real DayZ, real dzo.** Neither was part of the spikes.

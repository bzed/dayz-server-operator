<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Native packages for Ubuntu, Fedora/RHEL and Arch — Design

Date: 2026-11-21
Status: draft, waiting for approval
Companion: `2026-11-21-container-appliance-design.md` (dzo in a podman container, for hosts
none of these packages cover)

## Intent and why

dzo is installed and tested on Debian 13 (trixie) only. People run game servers on other
distributions, and nothing in the dzo binary itself needs Debian: it is one static Go binary
that drives rootless podman quadlets through the user's systemd. What ties it to Debian is
the packaging and a handful of files it reads from Debian packages. This design removes
those ties and ships native packages for the distributions that already have a new enough
podman and systemd.

## Goals

- The existing `.deb` installs and works on Ubuntu 26.04 LTS and later, and on Debian
  releases after trixie, without a rebuild.
- A dnf repository with an rpm for Fedora 43+ and the RHEL 10 family (RHEL, AlmaLinux,
  Rocky, CentOS Stream 10).
- A pacman repository with a package for Arch Linux, best effort.
- README and docs say for every distribution what is supported, how it is installed and
  how well it is tested.

## Non-goals

- Hosts with podman older than 5.4 or systemd older than 257 (Ubuntu 24.04 LTS, Debian 12,
  RHEL 9). They are what the container appliance is for.
- Hosts without systemd.
- arm64. DayZServer is x86_64 only.
- An AUR package, a Flatpak, a Snap, a Nix package.
- Building dzo from source inside the rpm or the Arch package (see Approaches).

## Anti-goals

- A second behaviour on Debian. Debian 13 stays the reference system; what changes there
  must be a strict simplification (fewer dependencies), not a new code path.
- "Supported" without evidence. A distribution is only called supported at the level it
  was actually tested at (see Support levels).

## What was checked (2026-11-20, in the distributions' official container images)

| Distribution | podman | systemd | Of dzo's `.deb` dependencies |
|---|---|---|---|
| Debian 13 (reference) | 5.4.2 | 257 | all |
| Ubuntu 25.10 | 5.4.2 | 257.9 | all, incl. `steamcmd:i386` (multiverse); release is EOL |
| Ubuntu 26.04 LTS | 5.7.0 | 259 | all; `libjs-htmx` 2.0.8 |
| Ubuntu 26.10 | 5.8.6 | 261 | all, but `libjs-htmx` is **4.0.0-beta6** |
| Fedora 43 / 44 | 5.8.9 | 258 / 259 | no steamcmd, htmx or leaflet package found |
| CentOS Stream 10 | 6.1.0 | 257 | no steamcmd, htmx or leaflet package found |
| Arch | 6.2.0 | 262 | steamcmd not in the repositories |

Not checked, and therefore open (see Risks): podman 6 with dzo's quadlets, SELinux on
Fedora/RHEL, btrfs on RHEL kernels.

## Approaches

1. **One `.deb` for Debian and Ubuntu, an rpm, an Arch package; web assets inside dzo**
   (chosen). Every distribution gets its native package manager, upgrades and dependency
   handling. The packages differ only in metadata; the payload is the same.
2. **One tarball and an install script for everything that is not Debian.** Least
   packaging work, but no upgrades through the package manager, no dependency resolution,
   and an install script that runs as root and has to know every distribution anyway.
   Rejected: the user asked for repositories, and upgrades are where a hand install rots.
3. **Only the container appliance for everything that is not Debian.** Rejected as the
   only answer: it adds a socket, a second start path and a container to debug for hosts
   that could run dzo natively. It stays the answer for hosts the packages do not cover.

The user picked 1: the `.deb` for Ubuntu, a yum/dnf repository for Fedora/RHEL,
"something" for Arch.

## Design

### 1. Web assets move into dzo (all packages, including the `.deb`)

Today `web.assets` (`/usr/share/javascript`) and `web.flags_dir`
(`/usr/share/iso-flags-svg/country-4x3`) point at files of `libjs-htmx`, `libjs-leaflet`,
`fonts-fork-awesome` and `iso-flags-svg`. Those packages do not exist on Fedora, RHEL and
Arch, and Ubuntu 26.10 already ships htmx 4, which dzo's web UI is not written for.
Decided by the user: ship them with dzo, in the `.deb` too.

- Pinned copies of htmx (2.0.x), Leaflet, Fork Awesome and the 4:3 country flags are
  vendored in the repository and compiled into the binary (`go:embed`). *My call:* in the
  binary rather than as files under `/usr/share/dzo`, because then the tarball build, the
  rpm, the Arch package, the appliance and the e2e suite all need nothing extra.
- The versions are the ones the web UI is tested with today (Debian trixie's: htmx 2.0.4,
  Leaflet 1.7.1, Fork Awesome 1.2.0), unless the builder finds a reason to take the latest
  patch release of the same major version. A script (`make vendor-web` or similar)
  downloads the pinned versions and verifies checksums that are committed; nothing is
  fetched at build time.
- `web.assets` and `web.flags_dir` stay as overrides: empty (the new default) serves the
  embedded copies, a directory serves that directory as today. An existing config that
  sets them keeps working. The URL layout (`/vendor/htmx/htmx.min.js`,
  `/vendor/leaflet/...`, `/vendor/fork-awesome/...`, `/flags/<code>`) does not change.
- `debian/control` drops `libjs-htmx`, `libjs-leaflet`, `fonts-fork-awesome` and
  `iso-flags-svg`. CI and `make e2e` no longer install them.
- Each vendored project gets its licence file under `LICENSES/`, a `REUSE.toml` entry and
  a `debian/copyright` stanza. The builder verifies each licence against the
  AGPL-compatible allow-list before vendoring (expected: htmx 0BSD, Leaflet BSD-2-Clause,
  Fork Awesome OFL-1.1 for the fonts and MIT for the CSS; the flags collection's licence
  must be looked up, and if it does not fit, flags stay an optional directory).
- Docs (required by AGENTS.md): both keys in the `config.yaml` example with the new
  default, and the prose on what empty means.

GeoIP stays outside: `geoip-database` remains a `.deb` dependency, and is optional
elsewhere (without it the player list shows no country, which dzo already handles).

### 2. steamcmd

- `.deb`: unchanged, `Depends: steamcmd:i386` (Debian non-free, Ubuntu multiverse).
- rpm and Arch: there is no steamcmd package. dzo already builds the
  `localhost/dzo-steamcmd` image in `dzo setup`. All packages ship a small wrapper
  (`/usr/libexec/dzo/steamcmd-container` or the distribution's libexec path) that runs
  steamcmd in that image with the steamcmd home under the secrets directory mounted, as
  the image's Containerfile describes.
- Commands that take `--steamcmd` default to `steamcmd` from `PATH` and fall back to the
  wrapper when there is none. `dzo setup` says which one will be used. The fallback and
  the wrapper go into `docs/cli.rst` and the installation page.
- The interactive login (`dzo steam login`, a pty with password and Steam Guard prompts)
  has to work through the wrapper. That is only testable with a real Steam account, so it
  is on the manual checklist.

### 3. One post-install script for all packages

The `.deb` postinst does four things that every package needs: create the `dayz` user as
a regular user (UID 1000+, for the per-user journal), give it subordinate uid/gid ranges,
enable lingering, and create `/run/dzo` through tmpfiles.d. On upgrade it runs
`dzo units sync` and restarts dzo's own services, never a game server.

- That logic moves into one shell script in the repository that the `.deb` postinst, the
  rpm `%post` and the Arch `.install` file all call, so the three cannot drift. The
  sysusers.d and tmpfiles.d snippets are shared files too.
- The subuid range is picked free instead of fixed at 100000-165535, which may be taken on
  a host that already has rootless podman users. (*My call*; the `.deb` keeps a range it
  already assigned.)

### 4. Ubuntu

- Minimum **26.04 LTS**. It is the first LTS after trixie, and 25.10 is already out of
  support. Later releases work because of section 1.
- The apt repository gains the suite name `stable` for the same package; `trixie` stays
  valid so existing installations keep working. Docs use `stable`.
- The install page gets an Ubuntu section: enable `multiverse` and `i386` (for steamcmd),
  add the repository, `apt install dzo`.

### 5. Fedora and the RHEL 10 family

- One rpm for both, built in CI in a Fedora container from the artifacts CI already
  builds (the static binary, the servermods, the HTML docs, the man pages). *My call:*
  repackaging those instead of compiling in the spec file, because dzo needs the current
  upstream Go toolchain and the binary is static, so one build serves every distribution.
- `Requires: podman >= 5.4, systemd >= 257, git, patch, shadow-utils, passt`.
  `Recommends: btrfs-progs, geoipupdate`.
- Signed with the repository key, published with `createrepo_c` on GitHub Pages under
  `/rpm/`, next to `/apt/`, with a `dzo.repo` file to drop into `/etc/yum.repos.d/`.
- **SELinux** is the unknown. Both distributions enforce it, and dzo's containers bind
  mount the build, the mods, the profiles and the dzo binary, some as `:O` overlays.
  Before the spec file is written, a spike on a Fedora VM answers: does a rendered
  instance quadlet start as it is, and if not, is `SecurityLabelDisable=true` in the
  quadlet enough. The intended fix is that one key, emitted always (it is a no-op without
  SELinux), not relabelling multi-GB shared build directories with `:z`. If the spike
  shows more is needed (the service user's home under `/var/lib`, the user systemd
  running `/usr/bin/dzo`), the findings come back before more is built.
- **btrfs**: Fedora has it. RHEL-family kernels are believed not to ship it; there dzo
  runs without snapshots and backups, which it already reports per instance. The install
  page says so.

### 6. Arch

- A `PKGBUILD` in the repository (same repackaging as the rpm), built in CI in an
  `archlinux` container, published as a signed pacman repository under `/arch/`.
- Level: best effort (see below). No AUR upload.

### 7. Podman 6

Stream 10 and Arch ship podman 6, which dzo has never run on. The plan's check that
refuses podman < 5.4 was never written (the code has no version check); `dzo setup` gets
it now, as an error below 5.4 with no upper bound, because the packages will meet hosts
nobody tested. Whether the quadlet keys dzo uses still exist there is
checked two ways: in CI, each distribution's own quadlet generator runs in dry-run mode
over the rendered golden instance units and must accept them; and the manual checklist
boots a real server.

### Support levels

| Level | Distributions | What stands behind it |
|---|---|---|
| Reference | Debian 13 | everything: CI, e2e, `make test-client`, the author's servers |
| Supported | Ubuntu 26.04 LTS, Fedora (current two), RHEL 10 family | CI smoke per distribution; the manual checklist once per dzo release line and when a distribution release is added |
| Best effort | Arch, Ubuntu interim releases, Debian testing | CI smoke only |

- **CI smoke**, per distribution, in its container image: install the package from the
  freshly built repository, check the user, the files and `dzo version`, run
  `dzo setup --dry-run` as the service user, run the quadlet generator dry-run from
  section 7. The best-effort jobs do not block a merge.
- **Manual checklist** (in `docs/development.rst`), on a VM: install, `dzo setup`, Steam
  login, one instance up with a real DayZ server and a client join, restart, backup and
  restore where btrfs exists, web UI, upgrade of the package with a server running. CI
  cannot run DayZ, so this is the only real check.

### Documentation

`docs/installation.rst` is split by distribution (requirements, repository, install, what
differs); `README.md` drops "on Debian" from the headline claims and shows the matrix;
`docs/dev-host.rst` stays Debian-specific but links the matrix; `docs/configuration.rst`
for the two changed defaults; `docs/cli.rst` for the steamcmd fallback.

### Order

1. Embedded web assets and the slimmer `.deb` dependencies (unblocks everything, and
   fixes Ubuntu 26.10 and future Debian on its own).
2. Ubuntu: suite name, CI smoke, docs.
3. Shared post-install script, steamcmd wrapper, the SELinux spike, the rpm and its
   repository.
4. Arch.

Each step is its own implementation plan and can ship alone.

## Left to the builder

- Where the vendored files live in the tree and the exact form of the update script.
- The spec file, the PKGBUILD and the CI job layout, following how the `deb` and `apt`
  jobs are built today.
- Whether the rpm and pacman repositories reuse the apt signing key or get their own
  (reusing is simpler for users; the builder checks that both tools accept the key type).

## Risks and open points

- SELinux may need more than one quadlet key; this is the largest unknown and is spiked
  first.
- Podman 6 may have dropped or changed a quadlet key dzo uses.
- The flags collection's licence may not allow vendoring.
- steamcmd through the container wrapper has never done an interactive login.
- "RHEL kernels have no btrfs" is from memory, not checked.

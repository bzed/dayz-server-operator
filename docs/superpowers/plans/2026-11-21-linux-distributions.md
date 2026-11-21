<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Native Packages for Other Distributions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** dzo installs from native packages on Debian 13+, Ubuntu 26.04 LTS+, Fedora 43+, the RHEL 10 family and Arch, and its web UI no longer depends on distribution JavaScript packages.

**Architecture:** The web assets (htmx, Leaflet, Fork Awesome, country flags) are vendored and compiled into the binary, so every package carries the same payload. The `.deb` keeps its build; the rpm and the Arch package repackage the CI-built static binary and the same files, and all three run one shared post-install script. Each package format gets a signed repository on GitHub Pages next to `/apt/`.

**Tech Stack:** Go (`embed`), debhelper, rpmbuild + createrepo_c, makepkg + repo-add, GitHub Actions, Sphinx.

**Spec:** `docs/superpowers/specs/2026-11-21-linux-distributions-design.md`

## Global Constraints

- Debian 13 (trixie) stays the reference; changes there may only remove dependencies, never add a code path.
- Floors: podman **5.4**, systemd **257**, amd64 only. Ubuntu minimum **26.04 LTS**; Fedora **43+**; RHEL family **10**.
- No new Go module dependency. Vendored web files are data, fetched by a script with committed SHA-256 sums; nothing is downloaded during a build.
- Vendored versions: htmx **2.0.4**, Leaflet **1.7.1**, Fork Awesome **1.2.0** (Debian trixie's), unless a task records a reason for a later patch release of the same major version.
- URL layout stays: `/vendor/htmx/htmx.min.js`, `/vendor/leaflet/{leaflet.js,leaflet.css,images/*}`, `/vendor/fork-awesome/{css/fork-awesome.css,fonts/*}`, `/flags/<code>`.
- Every changed config key is documented in the `config.yaml` example and in prose in `docs/configuration.rst` in the same commit (AGENTS.md).
- Every new file has an SPDX header or a `REUSE.toml` entry; every vendored project has its licence under `LICENSES/`; `make reuse` and `make licenses` pass.
- Packaging changes add a `debian/changelog` entry. `make check` passes before every push.
- A distribution is only documented at the support level the CI job or the manual checklist actually covers (Reference / Supported / Best effort, spec table).

## Review Focus

- **An existing config that still sets `web.assets: /usr/share/javascript`** after the `.deb` dropped `libjs-*`: the directory may be gone after autoremove. Expected: the web UI still works, using the embedded copies, and `dzo web` logs one warning naming the key. Test in Task 2.
- **A config that points `web.assets` at a directory lacking one file** (e.g. no Fork Awesome): expected: the missing file falls back to the embedded copy, not a 404 that silently breaks icons. Test in Task 2.
- **Flag codes that do not exist** (`/flags/xx`) and path tricks (`/flags/..`, upper case): expected 404 from the embedded set exactly as from the directory today. Test in Task 2.
- **A host where `/etc/subuid` already holds the range 100000-165535 for another user** (an existing rootless podman user): expected: the shared post-install script assigns a free, non-overlapping range and never changes an existing `dayz` range. Test in Task 6.
- **`steamcmd` neither in `PATH` nor the image built yet** (fresh rpm install before `dzo setup`): expected: a clear error telling the operator to run `dzo setup`, not a podman "image not known". Test in Task 7.

---

## Part 1 — Embedded web assets (ships alone; fixes Ubuntu 26.10 and later Debian by itself)

### Task 1: Vendor the web files

**Files:**
- Create: `scripts/vendor-web.sh`, `internal/web/vendor/SHA256SUMS`, `internal/web/vendor/htmx/htmx.min.js`, `internal/web/vendor/leaflet/…`, `internal/web/vendor/fork-awesome/…`, `internal/web/vendor/flags/<code>.svg`
- Create: `LICENSES/0BSD.txt`, `LICENSES/BSD-2-Clause.txt`, `LICENSES/OFL-1.1.txt`, `LICENSES/MIT.txt` (those not already present), plus the flags' licence
- Modify: `REUSE.toml`, `debian/copyright`, `Makefile` (target `vendor-web`)

**Interfaces:**
- Produces: the directory `internal/web/vendor/` with exactly this layout: `htmx/htmx.min.js`, `leaflet/leaflet.js`, `leaflet/leaflet.css`, `leaflet/images/*`, `fork-awesome/css/fork-awesome.css`, `fork-awesome/fonts/*`, `flags/<two lower-case letters>.svg` (4:3).

- [ ] **Step 1: Check the licences before downloading anything.** htmx (expected 0BSD), Leaflet (BSD-2-Clause), Fork Awesome (fonts OFL-1.1, CSS MIT), and the flag set Debian's `iso-flags-svg` 1.0.2 is built from. Look the flag licence up at its upstream; if it is not on the allow-list in `Makefile`'s `licenses` target or not REUSE-expressible, stop vendoring flags: `flags/` stays out, `web.flags_dir` keeps its current default in Task 2, and the task notes record why.
- [ ] **Step 2: Write `scripts/vendor-web.sh`.** It downloads the pinned release archives (versions from Global Constraints) into a `mktemp -d /var/tmp/...` directory, extracts only the files of the layout above into `internal/web/vendor/`, and runs `sha256sum -c internal/web/vendor/SHA256SUMS`. `--update` rewrites `SHA256SUMS` instead of checking. `make vendor-web` runs it.
- [ ] **Step 3: Run it with `--update` once, then without.** Expected: the second run prints every file `OK`.
- [ ] **Step 4: Compare with Debian's copies** (on this trixie host): `cmp internal/web/vendor/htmx/htmx.min.js /usr/share/javascript/htmx/htmx.min.js` and the same for `leaflet.js`, `leaflet.css` and `fork-awesome.css`. Expected: identical. If a file differs only because Debian rebuilt it from source, keep upstream's and note it.
- [ ] **Step 5: Licences and REUSE.** Add one `[[annotations]]` block per project in `REUSE.toml` (path globs under `internal/web/vendor/`) and one `Files:` stanza per project in `debian/copyright`. Run `make reuse licenses`. Expected: both pass.
- [ ] **Step 6: Commit** `web: vendor htmx, Leaflet, Fork Awesome and the country flags`.

### Task 2: Serve the embedded assets

**Files:**
- Modify: `internal/web/server.go:46-55,103-105`, `internal/web/handlers.go:574-583`, `internal/config/config.go:220-226,294`, `cmd/dzo/` (`cmd/dzo/web.go:80`, where `web.Options` is filled from config)
- Create: `internal/web/vendor.go`, `internal/web/vendor_test.go`
- Modify: `internal/web/e2e_test.go:33-60` (`useRealAssets`)
- Modify: `docs/configuration.rst:93-95,299-306`, `docs/geoip.rst:103,118`, `docs/development.rst` (the `DZO_E2E_ASSETS` paragraph)

**Interfaces:**
- Consumes: `internal/web/vendor/` from Task 1.
- Produces: `func vendorFS(dir string) fs.FS` in `internal/web/vendor.go` — the embedded tree, overlaid by `dir` when `dir != ""` (a file present in `dir` wins, a missing one falls back to the embedded copy); `func flagsFS(dir string) fs.FS` likewise over `vendor/flags`. Config defaults `Web.Assets = ""`, `Web.FlagsDir = ""`.

- [ ] **Step 1: Write the failing tests** in `internal/web/vendor_test.go`, through `Server.Handler()` with `httptest`:
  - `TestVendorEmbeddedDefault`: `Options{}` → `GET /vendor/htmx/htmx.min.js` is 200 and its body equals the embedded file; same for `/vendor/leaflet/leaflet.css` and `/vendor/fork-awesome/css/fork-awesome.css`.
  - `TestVendorDirOverridesFile`: `Options{Assets: d}` where `d` holds only `htmx/htmx.min.js` with body `"local"` → that URL returns `"local"`, and `/vendor/leaflet/leaflet.js` still returns the embedded file.
  - `TestVendorMissingDirFallsBack`: `Options{Assets: "/nonexistent"}` → `/vendor/htmx/htmx.min.js` is 200 (embedded).
  - `TestFlagsEmbedded`: `GET /flags/de` is 200 `image/svg+xml`; `/flags/xx`, `/flags/DE`, `/flags/..`, `/flags/d` are 404.
  - `TestVendorNoListing`: `GET /vendor/` and `GET /vendor/htmx/` are 404, not a directory listing.
- [ ] **Step 2: Run** `go test ./internal/web/ -run 'TestVendor|TestFlags' -v`. Expected: FAIL (no embedded files served with empty `Assets`).
- [ ] **Step 3: Implement `vendor.go`** with `//go:embed vendor` and the two overlay functions; switch the `/vendor/` handler to `http.FileServerFS(vendorFS(s.opts.Assets))` behind a wrapper that 404s any path ending in `/`, and `flag` to `http.ServeFileFS(w, r, flagsFS(s.opts.FlagsDir), code+".svg")`. Change the defaults in `config.go:294` to empty and update the doc comments on `Assets`/`FlagsDir` (no more "Debian's libjs-*"). Where `cmd/dzo` builds `web.Options`, log one warning at start when `web.assets` or `web.flags_dir` is set but not a directory: `web.assets <dir> is not a directory; serving the built-in copies`.
- [ ] **Step 4: Run** the tests from Step 1 and `go test ./internal/config/ ./internal/web/`. Expected: PASS (fix config tests that asserted the old defaults).
- [ ] **Step 5: e2e uses the embedded files.** `useRealAssets` copies only when `DZO_E2E_ASSETS` is set; unset, the server runs with `Assets: ""`. Run `make e2e` (or `make e2e-container`). Expected: PASS.
- [ ] **Step 6: Docs.** In the `config.yaml` example: `assets: ""   # default: none (the built-in copies)` and `flags_dir: ""   # default: none (the built-in flags)`. Prose for each: what is built in and its versions, that a directory overrides file by file, that a missing directory logs a warning and falls back, that both are read at start of `dzo web`. Fix `geoip.rst` and the `DZO_E2E_ASSETS` paragraph in `development.rst` (now optional: tests against another htmx). Run `make docs`. Expected: no warnings.
- [ ] **Step 7: Commit** `web: serve the built-in htmx, Leaflet, Fork Awesome and flags; web.assets and web.flags_dir override them`.

### Task 3: Drop the JavaScript packages from the `.deb`, CI and the e2e image

**Files:**
- Modify: `debian/control:17-25`, `debian/changelog`, `.github/workflows/ci.yml:146-160` (e2e job), `images/e2e/Containerfile:27-33`, `Makefile:93-96` (e2e comment), `README.md:170,209`, `docs/installation.rst` (package dependency list)

- [ ] **Step 1:** Remove `libjs-htmx`, `libjs-leaflet`, `iso-flags-svg`, `fonts-fork-awesome` from `Depends:` (and the matching `Recommends`/`Suggests` if any). Remove them from the e2e job's `apt-get install` and from `images/e2e/Containerfile`. Drop `continue-on-error` for `forky` in the e2e matrix: the htmx 4 problem it guarded against is gone.
- [ ] **Step 2: Build the package** `dpkg-buildpackage -us -uc -b && lintian --fail-on error ../dzo_*.deb`. Expected: success; `dpkg-deb -f ../dzo_*.deb Depends` lists none of the four.
- [ ] **Step 3: changelog entry** ("ship htmx, Leaflet, Fork Awesome and the flags; no longer depend on the libjs-* packages") and fix the README/installation sentences that name the libjs packages.
- [ ] **Step 4: Run** `make check`. Expected: PASS.
- [ ] **Step 5: Commit** `debian: no longer depend on libjs-htmx, libjs-leaflet, fonts-fork-awesome, iso-flags-svg`.

---

## Part 2 — Ubuntu and a podman version check

### Task 4: `stable` suite in the apt repository, Ubuntu CI smoke

**Files:**
- Modify: `scripts/build-apt-repo.sh` (after the `includedeb` loop), `.github/workflows/ci.yml:250-275` (`package-install` job), `docs/installation.rst`, `README.md:69-90`

**Interfaces:**
- Produces: the published repository answers both `Suites: trixie` and `Suites: stable` (`dists/stable` → `dists/trixie`).

- [ ] **Step 1:** Add `reprepro --basedir "$base" --outdir "$out" createsymlinks` after the loop (`apt/conf/distributions` already has `Suite: stable`). Run the script locally with a throwaway `GNUPGHOME` and a built `.deb`. Expected: `ls -l apt-repo/dists/` shows `stable -> trixie`.
- [ ] **Step 2: Matrix the `package-install` job** over `debian:trixie`, `ubuntu:26.04` (blocking), and `ubuntu:26.10`, `debian:forky` (`continue-on-error: true`). The steamcmd step differs per family: Debian enables `contrib non-free`, Ubuntu enables `multiverse` (in `/etc/apt/sources.list.d/ubuntu.sources`); both add i386 and pre-seed the licence questions. Add a step after `dzo setup --dry-run`: `getent passwd dayz | cut -d: -f3` ≥ 1000 and `grep -q '^dayz:' /etc/subuid`.
- [ ] **Step 3: Run** the job locally for Ubuntu: `podman run --rm -v $PWD/..:/w docker.io/library/ubuntu:26.04 sh -c '<the job's install steps> && dzo version && su dayz -s /bin/sh -c "dzo setup --dry-run"'`. Expected: exit 0.
- [ ] **Step 4: Docs.** Installation page: an "Ubuntu 26.04 LTS and later" section (multiverse, i386, the same `dzo.sources` with `Suites: stable`), and switch the Debian section's example to `Suites: stable` with a note that `trixie` keeps working. README: same.
- [ ] **Step 5: Commit** `apt: publish the stable suite; install-test the package on Ubuntu 26.04`.

### Task 5: `dzo setup` refuses podman older than 5.4

**Files:**
- Modify: `internal/setup/setup.go` (new step `r.podmanVersion(ctx)`, called first in `Run` after the root check, also with `ImagesOnly`)
- Test: `internal/setup/setup_test.go`

**Interfaces:**
- Produces: `func checkPodmanVersion(out string) error` — parses `podman version --format {{.Client.Version}}` output (`5.4.2`, `6.1.0`, `5.8.6-dev`), error below 5.4.0, no upper bound.

- [ ] **Step 1: Failing test** `TestCheckPodmanVersion`, table: `"5.4.2"` ok, `"5.4.0"` ok, `"6.2.0"` ok, `"5.8.6-dev"` ok, `"5.3.9"` error containing `podman 5.4 or newer`, `"4.9.3"` error, `"garbage"` error containing `cannot parse`. And `TestSetupReportsOldPodman`: `Run` with a fake `Exec` returning `4.9.3` for `podman version` → returned error, output line `error podman 4.9.3 is too old: dzo needs podman 5.4 or newer`.
- [ ] **Step 2: Run** `go test ./internal/setup/ -run 'Podman' -v`. Expected: FAIL.
- [ ] **Step 3: Implement**; the step reports `ok podman <version>` on success.
- [ ] **Step 4: Run** the tests. Expected: PASS. Update `docs/installation.rst` (the setup output section) with the new line.
- [ ] **Step 5: Commit** `setup: refuse podman older than 5.4`.

---

## Part 3 — Fedora and the RHEL 10 family

### Task 6: One post-install script for every package

**Files:**
- Create: `packaging/common/dzo-postinst.sh`, `packaging/common/dzo-postinst_test.sh` (or a Go test driving it in a temp root, see Step 1)
- Move: `debian/dzo.sysusers` → `packaging/common/dzo.sysusers`, `debian/dzo.tmpfiles` → `packaging/common/dzo.tmpfiles` (debian/ keeps them via `dh_installsysusers --name` or symlinks; pick what lintian accepts)
- Modify: `debian/postinst`, `debian/dzo.install`, `debian/changelog`

**Interfaces:**
- Produces: `/usr/libexec/dzo/postinst configure|upgrade` installed by every package. It: creates `/var/lib/dzo` (0750 dayz:dayz, never recursive chown); assigns a subuid/subgid range of 65536 when `dayz` has none, choosing the lowest start ≥ 100000 that overlaps no line of `/etc/subuid` resp. `/etc/subgid`; `loginctl enable-linger dayz`; on `upgrade` runs `dzo units sync` as dayz and restarts `dzo-exporter`, `dzo-serve`, `dzo-web` when active — exactly the logic of today's `debian/postinst:19-55`. The root for file paths is `$DZO_ROOT` (default empty) so tests run it against a temp directory.

- [ ] **Step 1: Failing test** (shell test run from a Go test in `internal/setup` or a `make test-packaging` target; it must run in `make test`): with `DZO_ROOT=$tmp` and fake `usermod`, `loginctl`, `runuser` on `PATH` that log their arguments:
  - empty `etc/subuid` → `usermod --add-subuids 100000-165535 …`;
  - `etc/subuid` containing `alice:100000:65536` → `--add-subuids 165536-231071`;
  - `etc/subuid` containing `dayz:300000:65536` → no `usermod` call;
  - `upgrade` without `/run/user/<uid>/bus` → no `runuser` call.
- [ ] **Step 2: Run it.** Expected: FAIL (script missing).
- [ ] **Step 3: Write the script**; `debian/postinst` becomes `#DEBHELPER#` plus `/usr/libexec/dzo/postinst "$1" "$2"` mapped to configure/upgrade.
- [ ] **Step 4: Run** the test, then `dpkg-buildpackage -us -uc -b` and the `package-install` job locally for trixie. Expected: PASS; the user, range and linger exist as before.
- [ ] **Step 5: Commit** `packaging: one post-install script for all packages; pick a free subuid range`.

### Task 7: steamcmd in the container when the host has none

**Files:**
- Create: `packaging/common/steamcmd-container` (shell), installed as `/usr/libexec/dzo/steamcmd-container`
- Modify: `cmd/dzo/mod.go:35`, `cmd/dzo/steam.go:54` (the two `--steamcmd`/`--command` defaults), `internal/setup/setup.go` (`r.steam()` reports which steamcmd is used)
- Create: `cmd/dzo/steamcmd.go` with `func defaultSteamcmd(lookPath func(string) (string, error), wrapper string) string`
- Test: `cmd/dzo/steamcmd_test.go`, `internal/setup/setup_test.go`
- Modify: `debian/dzo.install`, `docs/cli.rst`, `docs/installation.rst`

**Interfaces:**
- Produces: `defaultSteamcmd` returns `"steamcmd"` when `lookPath("steamcmd")` succeeds, else `wrapper` (`/usr/libexec/dzo/steamcmd-container`). The wrapper runs `podman run --rm -i [-t when stdin is a terminal] --network host --userns keep-id -v "$secrets/steamcmd-home:/home/steam" -v "$cache:$cache" -e HOME=/home/steam localhost/dzo-steamcmd:latest steamcmd "$@"`, reading `secrets` and `cache` from the dzo config (`dzo config` has subcommands, see `cmd/dzo/config.go`; add a `get <key>` subcommand there if none prints a single path). Without the image it exits 1 with `dzo-steamcmd image missing: run dzo setup`.

- [ ] **Step 1: Failing tests** `TestDefaultSteamcmd` (both branches) and `TestSetupSaysWhichSteamcmd` (fake lookPath without steamcmd → output line `info steamcmd: not installed, using /usr/libexec/dzo/steamcmd-container (the dzo-steamcmd image)`).
- [ ] **Step 2: Run** `go test ./cmd/dzo/ ./internal/setup/ -run Steamcmd -v`. Expected: FAIL.
- [ ] **Step 3: Implement** and wire both flag defaults to `defaultSteamcmd(exec.LookPath, …)`; flag help text names the fallback.
- [ ] **Step 4: Run** the tests. Expected: PASS. Then on this host, with the image built: `PATH=/usr/bin:/bin /usr/libexec/dzo/steamcmd-container +quit` (from the checkout path). Expected: steamcmd starts and quits.
- [ ] **Step 5: Docs**: `docs/cli.rst` (both flags: default and fallback), installation page (Fedora/RHEL/Arch get steamcmd from the image). `make docs`.
- [ ] **Step 6: Commit** `steam: run steamcmd from the dzo-steamcmd image when the host has none`.
- [ ] **Step 7 (manual, record the result in the commit message of Task 9):** `dzo steam login` through the wrapper with a real account and Steam Guard on a Fedora VM.

### Task 8: SELinux spike (gate for Task 9)

Manual, on a Fedora 44 VM with SELinux enforcing, dzo installed by hand as in `docs/dev-host.rst` (binary, images, user, linger).

- [ ] **Step 1:** Render one instance (`dzo instance create`, `dzo instance apply`) and `dzo start` it. Record `journalctl --user -u dzo-<name>` and `ausearch -m avc -ts recent`.
- [ ] **Step 2:** If it fails on AVC denials, add `SecurityLabelDisable=true` to the rendered `.container` by hand, `systemctl --user daemon-reload`, start again. Record.
- [ ] **Step 3:** Check what else is denied: the user systemd running `/usr/bin/dzo` from `/var/lib/dzo` home, podman storage below `/var/lib/dzo/.local/share/containers`.
- [ ] **Step 4: Write the findings into the spec** (section 5, replacing "the unknown") and commit `docs: SELinux findings for Fedora`. If more than `SecurityLabelDisable=true` is needed, stop and bring the findings to the user before Task 9.

### Task 9: The rpm and the dnf repository

**Files:**
- Create: `packaging/rpm/dzo.spec`, `scripts/build-rpm.sh`, `scripts/build-rpm-repo.sh`, `rpm/dzo.repo`
- Modify: `internal/quadlet/container.go` (emit `SecurityLabelDisable=true`, if Task 8 confirmed it), `internal/quadlet/*_test.go`, `internal/resolve/testdata/*.golden`
- Modify: `.github/workflows/ci.yml` (jobs `rpm`, `rpm-install`), `.github/workflows/docs.yml` (job `rpm-repo`, publish under `site/rpm`), `docs/installation.rst`

**Interfaces:**
- Consumes: the `dzo-binaries` artifact (`dist/dzo-linux-amd64`) and a tarball of `dist/servermods`, `images/`, `contrib/`, `docs/_build/html`, `debian/man` that the `deb` job uploads as `dzo-payload` (add that upload to the `deb` job); `packaging/common/*` from Tasks 6-7.
- Produces: `dzo-<version>-1.x86_64.rpm` with the same file list as `debian/dzo.install`, `Requires: podman >= 5.4, systemd >= 257, git, patch, shadow-utils, passt`, `Recommends: btrfs-progs, geoipupdate`; `%post` calls `/usr/libexec/dzo/postinst`; sysusers/tmpfiles via the `%sysusers_create_compat`-style macros of Fedora.

- [ ] **Step 1 (only if Task 8 said so): failing test** in `internal/quadlet` asserting `SecurityLabelDisable=true` in `[Container]`; run, implement, `go test ./internal/resolve/ -update`, review the golden diff (one line per file), commit `quadlet: disable SELinux label separation for the game containers` with the reason from Task 8.
- [ ] **Step 2: Write `dzo.spec` and `build-rpm.sh`** (`rpmbuild -bb` with `_topdir` in `mktemp -d /var/tmp/…`, version from `scripts/debian-snapshot.sh`'s scheme with `~` for the snapshot part so rpm sorts it like dpkg).
- [ ] **Step 3: Build locally** in `registry.fedoraproject.org/fedora:44`. Expected: `rpmlint` shows no errors; `rpm -qpl` matches the `.deb`'s `dpkg -c` file list (ignoring `/usr/share/doc` layout).
- [ ] **Step 4: Install-test** in `fedora:43`, `fedora:44` and `quay.io/centos/centos:stream10`: `dnf install ./dzo-*.rpm`, `dzo version`, `su dayz -s /bin/sh -c 'dzo setup --dry-run'`, the dayz UID ≥ 1000 and subuid checks from Task 4. Expected: exit 0. Add the same as CI job `rpm-install` (Fedora blocking, Stream 10 blocking).
- [ ] **Step 5: Repository.** `build-rpm-repo.sh <rpm-dir> <out-dir>`: `rpmsign --addsign` with the key in `GNUPGHOME` (same rules as `build-apt-repo.sh`), `createrepo_c`, `gpg --detach-sign --armor repodata/repomd.xml`, export the public key. `rpm/dzo.repo` points at `https://bzed.github.io/dayz-server-operator/rpm/` with `gpgcheck=1`, `repo_gpgcheck=1`. First check that `rpm --import` accepts the existing apt key; if not, generate a separate key and say so in the commit. Wire the `rpm-repo` job in `docs.yml` like `apt`.
- [ ] **Step 6: Docs**: installation page section "Fedora 43+ and RHEL 10 family" (repo file, `dnf install dzo`, no btrfs on RHEL-family kernels → no backups, SELinux note from Task 8). `make docs`.
- [ ] **Step 7: Commit** `rpm: package dzo for Fedora and the RHEL 10 family; dnf repository` (+ result of Task 7 Step 7).

### Task 10: Quadlet compatibility check per distribution

**Files:**
- Create: `scripts/check-quadlet.sh`
- Modify: `.github/workflows/ci.yml` (step in each `package-install`/`rpm-install`/`arch-install` job)

**Interfaces:**
- Produces: `scripts/check-quadlet.sh <dir-with-.container-files>` runs the distribution's own generator (`/usr/lib/systemd/user-generators/podman-user-generator --user --dryrun` with `QUADLET_UNIT_DIRS=<dir>`) and fails if it exits non-zero or prints `unsupported key`.

- [ ] **Step 1:** Write it; the inputs are the golden files of `internal/resolve/testdata/*.golden` copied to `<name>.container` (they are the rendered units).
- [ ] **Step 2: Run** in `debian:trixie`, `fedora:44`, `centos:stream10`, `archlinux` containers with podman installed. Expected: exit 0 on all; any failure is a podman-6 incompatibility to fix in `internal/quadlet` before this task is done (with a test pinning the fix).
- [ ] **Step 3: Commit** `ci: check the rendered quadlets with each distribution's generator`.

---

## Part 4 — Arch (best effort)

### Task 11: PKGBUILD and the pacman repository

**Files:**
- Create: `packaging/arch/PKGBUILD`, `packaging/arch/dzo.install`, `scripts/build-arch-repo.sh`
- Modify: `.github/workflows/ci.yml` (jobs `arch`, `arch-install`, both `continue-on-error: true`), `.github/workflows/docs.yml` (publish under `site/arch`), `docs/installation.rst`

**Interfaces:**
- Consumes: `dzo-binaries`, `dzo-payload`, `packaging/common/*`.
- Produces: `dzo-<ver>-1-x86_64.pkg.tar.zst`, `depends=(podman systemd git patch shadow passt)`, `optdepends=(btrfs-progs geoipupdate)`; `post_install`/`post_upgrade` call `/usr/libexec/dzo/postinst`; a signed repository `dzo` (`repo-add --sign`).

- [ ] **Step 1: Write the PKGBUILD** (version uses `.` instead of `~`/`+`, as `vercmp` sorts snapshots).
- [ ] **Step 2: Build and install-test** in `archlinux:latest` as a non-root build user: `makepkg -f`, `pacman -U`, `dzo version`, `dzo setup --dry-run` as dayz, `scripts/check-quadlet.sh`. Expected: exit 0.
- [ ] **Step 3: Repository and docs** (an "Arch Linux (best effort)" section: add `[dzo]` to `pacman.conf`, import the key with `pacman-key`). `make docs`.
- [ ] **Step 4: Commit** `arch: package dzo and publish a pacman repository (best effort)`.

### Task 12: Support matrix and the manual checklist

**Files:**
- Modify: `README.md` (headline without "on Debian", the support table), `docs/installation.rst` (requirements per distribution, the support table at the top), `docs/development.rst` (section "Release checklist for other distributions"), `docs/dev-host.rst` (one line: Debian-specific, link the matrix), `debian/changelog`

- [ ] **Step 1:** Write the table from the spec ("Support levels"), with the CI job names behind each row.
- [ ] **Step 2:** Write the manual checklist exactly as the spec lists it (install, `dzo setup`, Steam login, a real server up and a client joining, restart, backup and restore where btrfs exists, web UI, package upgrade with a server running).
- [ ] **Step 3: Run** `make docs` and grep the docs for `Debian 13 (trixie), x86_64` claims that now say too little. Expected: none left outside the Debian section.
- [ ] **Step 4: Commit** `docs: supported distributions and how each is tested`.

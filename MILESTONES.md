<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Milestones: first live test of dzo

Work order for a coding agent. Design background is in
`IMPLEMENTATION_PLAN.md` (section refs in brackets); do not copy it into `docs/`.

## Ground rules (apply to every milestone)

- Read `README.md` ("What's here today", "Needs live verification") first.
- Minimal code, stdlib first, no new dependency without a reason (ponytail).
- Every new file starts with the SPDX header (AGPL-3.0-or-later, Bernd Zeimetz).
- Every feature ships with tests (`make test` race-clean, coverage gate >= 85 %),
  a Sphinx page in `docs/` (`sphinx-build -W`), and `make lint reuse` green.
- Periodic jobs: systemd timers only, never cron. Never wipe `mpmissions`;
  managed files are updated in place. No real host names anywhere; use
  placeholders. Server builds are never auto-updated, no rollback logic.
- No real Steam account or DayZ server here: use fakes/fixtures, and list what
  stays unverified in the README section "Needs live verification".
- One commit per milestone (or smaller), messages in the repo's existing style.
- Stop at the end of each milestone; do not start the next unasked.

## M1: Site config to resolved instance  [C3, C6, C7]

The biggest gap: nothing turns a real `site.yaml` + `instance.yaml` into the
already-resolved inputs the `mission`, `product`, `quadlet` and `instance`
packages take.

- New `internal/resolve`: load site repo (`internal/site`) and merge site
  defaults with instance config; output one resolved `Instance` value
  (product generation, mod generations in load order, mission source, render
  inputs, quadlet spec, ports, paths).
- Cross-check against the existing consumers' input types; adapt the
  resolver, not the consumers, unless a consumer is wrong.
- CLI: `dzo instance show <name>` (prints resolved config as YAML) and
  `dzo config validate` extended to resolve every instance.
- Acceptance: golden test resolving a synthetic site repo (two instances,
  one with a workshop mod, one with `{local: x, server: true}`).

## M2: Mission render/apply wired to an instance  [C6]

- `dzo instance render <name>`: pristine mission (git) -> staging -> apply
  in place using `internal/mission` (manifest, drift, filehistory net).
  `--dry-run` prints the apply plan.
- First-time init from the pristine mission; later runs only touch managed files.
- Acceptance: integration test in a temp tree; property test that nothing
  outside the manifest set and `storage_*` is touched.

## M3: Mod and product install flow  [C7]

- `dzo mod add|update|list <instance-or-site>`: workshop download into
  immutable content-hashed generations (reuse `internal/product` + `cache`),
  validation (`meta.cpp`, PBOs parse, prefix header present), local servermods
  from site repo dir / host path / URL+sha256.
- `dzo product install|update` for the server build (manual only, no auto).
- Forced mod refresh command (`dzo mod refresh --force`) instead of rollback.
- Acceptance: fake-steamcmd integration tests; generations survive re-runs.

## M4: Rapified config.bin and compressed PBO entries  [C7, moddeps]

Independent of M1-M3; can run in parallel.

- `internal/moddeps`: decode rapified `config.bin` (CfgPatches
  `requiredAddons` only is enough) and LZSS (`Cprs`) PBO entries.
- Fixtures: build tiny rapified files in test code or check in a small
  synthetic PBO; document the format source in a comment.
- Acceptance: `dzo mod deps` / `cfgpatches` work on a PBO with `config.bin`.

## M5: Container images, `dzo setup`, package contents  [C4, C14]

- `images/{runtime,steamcmd}/Containerfile` (trixie base), shipped to
  `/usr/share/dzo/images/`; quadlet templates and default presets shipped too.
  Update `debian/dzo.install`; lintian clean.
- `dzo setup` (run as `dayz`): validate data path, build images, `steam login`
  hand-off, clone site repo. `--dry-run` supported. `postinst` handles
  subuid/subgid and linger (verify what is already there first).
- Acceptance: packaging CI job installs the `.deb` in a clean trixie
  container, runs `dzo version` and `dzo setup --dry-run`.

Status: done. Deviations: images are built with `podman build` (no `.build`
quadlets), the "default presets" are example map presets, and the install
commands do not run steamcmd through its image yet. Details in the README.

## M6: Servermods as submodules, `compat.yaml`  [D39, C14, C16]

- `servermods/dzo-admin` is already a submodule (skeleton). Add
  `make servermods`: pack `src/DZOAdmin` into `dist/servermods/dzo-admin/
  addons/dzoadmin.pbo` with the `pbo` library (prefix from `$PBOPREFIX$`),
  plus `meta.cpp`. Deterministic output (fixed timestamps, sorted entries).
- MetricZ/LogZ forks (MIT) and the analyzer are later submodules; wire the
  packing generically over `servermods/*` so adding one is one line.
- `compat.yaml` format + writer: dzo version, server build id, each servermod
  commit + PBO sha256. `debian/rules` runs `make servermods`; `dzo.install`
  ships `servermods/` to `/usr/share/dzo/servermods/`. `debian/copyright`
  gets a stanza per submodule licence (MetricZ/LogZ MIT).
- `dzo` refuses to activate a shipped servermod whose hash differs from
  `compat.yaml` unless told to.
- Acceptance: `make deb` from a fresh recursive clone; PBO hash reproducible
  across two builds.

Status: done for dzo-admin, the only servermod there is. MetricZ, LogZ and the
analyzer are not submodules yet; adding one is a `.gitmodules` entry and a
`debian/copyright` stanza. `server_build` in `compat.yaml` is filled by M7.

## M7: `dzo test boot`  [C22]  (dev machines only)

- Native mode first: render, boot a real `DayZServer`, wait for A2S, check
  logs (no `SCRIPT (E)`, module count above baseline, the mod's "loaded"
  line). Fake-`DayZServer` program for the unit tests.
- Never run next to live servers; refuse if a live instance is detected.
- Acceptance: green against the fake; document the manual procedure for the
  real server.

Status: done, and also run against a real 1.29 server on a development machine.
Native mode only (no `--container`). `dzo instance render` now also writes the
runtime files the boot needs (keys, `serverDZ.cfg`, BattlEye config).

## M8: Example site config and first instance  [C21]

- `dzo legacy convert-config` from `../dayzdockerserver` for hashima first,
  then the other four; `CONVERSION_REPORT.md` with unresolved decisions.
- Golden tests: same bytes as the legacy render modulo documented fixes.
- Acceptance: hashima config resolves (M1), renders (M2), and boot-tests (M7).

## M9: Hardening for real traffic

- RCon client reconnect with backoff (see README). Status: done, `battleye.Session`
  (`internal/battleye/session.go`), checked against a real server restart.
- Anything the M1-M8 review flags; update "Needs live verification".

## Live test checklist (human, after M1-M8)

Hashima on other ports with a copy of its mission, parallel to production:
steam login, product install, mod install, render, boot test, start, health
(kill/hang -> restart), BattlEye `players` output capture, then fill in the
README "Needs live verification" items (S1, S4, S6, `enfMain`).

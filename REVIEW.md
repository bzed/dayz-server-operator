<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Review of IMPLEMENTATION_PLAN.md

Date: 2026-09-26 · Reviewer: Crush (GLM) · Reviewed revision: `main`, working tree

Method: read the full plan, spot-checked Part A claims against the legacy checkout at
`../dayzdockerserver` (all six branches), and externally verified platform/ecosystem claims
(Debian package versions via `rmadison`, quadlet keys via podman docs, library availability and
licences via GitHub, btrfs kernel behaviour via kernel sources/docs). Verification results are in
§1; everything else is design review.

**Verdict: strong plan, approve with changes.** The analysis (Part A) is accurate (spot-checked
against the real scripts), the requirement set is complete and traceable, and the design resolves
the legacy system's worst problems (data loss, nondeterminism, broken restarts, shared mutable
state). Two findings should be resolved **before code starts** (§2: F1, F3); the rest are refinements.

---

## 1. External facts — verification results

| Claim in plan | Result |
|---|---|
| trixie: podman 5.4.2, systemd 257, passt 2025-05 snapshot | ✅ (`podman 5.4.2+ds1-2`, `systemd 257.13-1~deb13u1`, `passt 0.0~git20250503…`) |
| trixie: `libjs-htmx` 2.0.4 | ✅ (`2.0.4-1`, stable) — good, this was the least obvious one |
| trixie: `libjs-leaflet` 1.7.1, `postgresql` 17, `geoipupdate` 7.1 (contrib), `python3-requests` 2.32, `golang-go` 1.24 | ✅ all confirmed |
| trixie: `steamcmd` packaged (non-free, i386) | ✅ `0~20180105-5` in non-free. Note: it repacks the **2018 bootstrap tarball** — steamcmd self-updates on first run into its *home*, so the mounted `cache/steamcmd/<product>` home is what matters; the image package just provides the bootstrap. Fine, but state it (see 3.14). |
| Quadlet keys `HealthStartupCmd/Interval/Retries/Success`, `HealthOnFailure=kill`, `Notify=healthy`, `StopTimeout`, `.build` units | ✅ all present in podman 5.x quadlet docs. S6 on 5.4.2 stays worthwhile but risk is low. |
| WoozyMasta libs: `a2s`, `dzce`, `dzid`, `pbo`, `paa`, `steam` | ✅ all exist, all MIT. `dzce` actively maintained (last push 2026-04). Bonus: separate `lzo`, `lzss` and `rvmat` packages exist, and `paa` explicitly advertises LZO/LZSS — the C17 tile pipeline is on much solider ground than the plan implies. |
| DayZ experimental server = appid **1042420, branch public** (D14/§C7) | ✅ **confirmed by the operator** (2026-09-26). Remaining minor note: §C7 assumes both products download workshop items via appid 221100 — worth a one-time confirmation that the experimental server consumes workshop content via 221100 (and consider keeping an optional `branch_password` field in the product model for future password-protected branches). |
| Unprivileged btrfs snapshots as user `dayz` (D31/§C20), "verified on kernel 7.1, re-checked on trixie's 6.12 in S7" | ❌ **mismatch with the stated platform baseline.** Unprivileged *snapshot creation* (`BTRFS_IOC_SNAP_CREATE*`) landed in mainline **Linux 6.15**; before that it requires `CAP_SYS_ADMIN`. **trixie ships 6.12.** On 6.12 the following all work unprivileged: subvolume *creation*, flipping the ro flag on a subvolume you own, `rm -rf` + `rmdir` of an empty subvolume, and `SNAP_DESTROY` only with `user_subvol_rm_allowed`. But **snapshot creation gets EPERM** — the plan's deletion fallback does not help for *creating* snapshots. The dev box apparently runs a ≥6.15 kernel ("kernel 7.1"), which would mask this. See finding F1. |
| Legacy behaviour in Part A (spot checks) | ✅ `find`-based mod iteration, `xmlmerge -o /tmp/x`, `instanceId` in serverDZ.cfg, deerisle ports 8302/8303/8716, `/tmp/parameters` + `/tmp/mod_command_line` (basis of R5) — all present in the legacy source. |

---

## 2. Critical findings (resolve before Phase 1)

### F1 — D31 backup design does not work on the declared baseline kernel
§C20/D31 makes unprivileged snapshot creation the *only* backup mechanism, but trixie's stock
kernel 6.12 cannot do that (see §1). The plan must pick one and state it explicitly:

1. **Raise the baseline:** require kernel **≥ 6.15** (trixie-backports kernel) in D29/Assumptions,
   enforced by the `dzo setup` statfs/version check, **or**
2. **Design the fallback now:** unprivileged full-tree copies via `cp -a --reflink=always`
   (per-file reflinks — CoW-cheap, works on any btrfs, no kernel dependency; slower, not atomic),
   or a tiny root-owned helper/systemd service that performs `SNAP_CREATE` for the `dayz` user.

Either way: D29 must pin a **kernel** requirement next to podman/systemd (it currently doesn't
mention the kernel at all outside §C20/S7), and S7 must run on the *target* kernel, not the dev
box's. The read-only-snapshot + ro-flag-clear + delete paths you describe are correct for 6.12.

### F2 — ~~Experimental product appid unverified~~ (resolved)
2026-09-26: the operator confirmed the experimental server **is** appid 1042420, branch public —
the shipped defaults in §C7 are correct. Only the side note remains: confirm once that workshop
content for experimental is consumed via appid 221100 as assumed, and keep an optional
`branch_password` field in the product model for future password-protected branches (cheap,
generic, useful for any product). No spike needed otherwise.

### F3 — Restart storm on persistent render failure
§C5 pairs `ExecStartPre=/usr/bin/dzo render --apply` with `Restart=always` + `RestartSec=15`.
A broken site-repo change (bad XML, invalid integration) makes every start abort in ExecStartPre,
and systemd will then flap the unit every ~15 s forever — the exact A8.10-style failure mode the
plan criticises, now triggered by config instead of RCon. The render abort-before-touching-live
design (§C6) is right; the *reaction policy* is missing. Specify one of:

- `StartLimitIntervalSec`/`StartLimitBurst` + `RestartForceExitStatus` tuning so the unit lands in
  `failed` after N attempts and stays down (Icinga/Discord already cover it), or
- render failure moves the instance into a `failed-render` state that suppresses auto-restart
  until an operator intervenes (`dzo instance ack-failure`).

Same reasoning applies to containers killed repeatedly by `HealthOnFailure=kill` (crash loop
detection exists as a Discord event — define the automatic de-escalation, not just the notification).

---

## 3. Design gaps and open points (address in the plan, non-blocking)

1. **Global update timer vs per-instance `check_interval`** (§C5 + §C7): the timer is host-global
   and hourly "by default", but `instance.yaml` promises a per-instance interval. Define the
   reconciliation: e.g. the timer runs at the minimum configured interval and `dzo update check`
   gates per instance by its own last-check time — or drop per-instance intervals until `dzo serve`.
2. **Scheduling ownership** (§C5/§C12/§C18): `dzo serve` "may take over the timer schedules, or
   keep driving the same timers" is a race waiting to happen (double restarts, divergent
   next-run state). Define a single rule, e.g.: schedules live in the DB and are *always*
   materialised as transient timers; the daemon only creates/cancels timers, never fires them
   itself. Then a daemon restart can never double-fire.
3. **`dzo-admin` endpoint under `publish` networking** (§C16): the mod talks to `127.0.0.1` — true
   only in `host` mode. Under pasta the host is reachable via the gateway address pasta provides;
   the rendered `$profile:dzo/admin/config.json` must be network-mode aware. Also note: in host
   mode every process on the host shares that endpoint's network namespace — the instance token is
   the only guard; say so explicitly in §C13.
4. **Quadlet sketch network deps** (§C5): `After=network-online.target`/`Wants=…` in a **user**
   unit are ignored by the user manager; quadlet rootless units get `podman-user-wait-network-online.service`
   injected automatically. Drop those two lines from the sketch (it is the reference for codegen).
5. **A2S specifics** (§C9): DayZ A2S requires the challenge handshake (A3SB variant); the
   WoozyMasta `a2s` lib handles it, but budget two round trips per probe in `HealthTimeout`/probe
   code, and note that the query port only answers once the mission is loaded (that's exactly what
   makes it a good readiness signal).
6. **`ban.txt` format** (§C18): verify whether DayZ's native ban.txt wants SteamID64 or BE GUID
   before implementing the enforcement rendering (BattlEye `bans.txt` = GUIDs is correct). Add to
   an early spike or the importer work.
7. **Update debouncing/windows** (§C7): with hourly checks and `auto` policy, each single stale mod
   triggers stop→snapshot→switch→start. DayZ mod authors push frequently; add per-instance update
   window/quiet-hours and a minimum-restart-interval/batching option (e.g. "restart for updates at
   most every N hours, batch everything found"). Legacy had the same behaviour; this is the chance
   to improve it, and `keep_by_reason` retention otherwise churns on mod_update snapshots.
8. **Naming collision** (§C2/§C6 vs §C20): `instances/<name>/snapshots/` (per-file drift copies)
   vs `${paths.snapshots}/<name>/` (btrfs snapshots). Rename one (e.g. file copies →
   `instances/<name>/filehistory/`) before any code or docs settle on it.
9. **Tile URL hashing** (§C17): "immutable cache headers (the URL includes the hash)" but the
   route is `/tiles/<map>/{z}/{x}/{y}.png`. Either put `<source-hash>` in the path or use
   `ETag`/`Cache-Control: max-age` + explicit invalidation; pick one and align `tiles.public_base_url`.
10. **Migration diff→overlay** (Phase 2, FR-26): converting hand-edits of managed *merge-target*
    files into overlays automatically is the fuzziest step of the whole migration (the hand-edit
    was made against an unknown pristine base). Make it explicitly **human-reviewed**: importer
    produces a proposed overlay + diff report as commits on a branch; operator merges. The plan
    reads as if it happens automatically ("so the first render does not 'revert' them").
11. **Soft-dependency define** (§C16): `#ifdef DZO_ADMIN` across *independent* PBOs is dubious in
    Enforce — preprocessor defines of another mod generally only apply when there is a config-level
    dependency. S8 already plans to verify; note the likely fallback (runtime capability check,
    e.g. a `DZOAdmin_Map` global probed via `TypeString`/`CallLater` safely, or the file-drop path)
    so third-party authors get a working answer even if the define mechanism fails.
12. **Memory limits** (§C5/instance.yaml): `container.cpus` exists but no memory option. DayZ
    servers OOM more often than they CPU-throttle; add `container.memory` (quadlet `Memory=`)
    to the instance schema.
13. **`HealthStartupRetries` vs heavy loads** — sketch says `60 × 30s = 30 min`; deerisle with the
    full mod set has historically been slow to load. The values are configurable per instance —
    just make sure the *default* startup budget covers the worst current server, or the first
    deployment of the slowest instance flaps. (`health.startup_timeout` exists in instance.yaml —
    wire it visibly to `HealthStartupRetries×Interval`.)
14. **steamcmd self-update** (§C4/§C7): Debian's package ships the 2018 bootstrap; the real binary
    materialises via self-update into the steamcmd home. Since mounts are `secrets/steamcmd-home` +
    `cache/steamcmd/<product>`, confirm where the self-update writes (`linux32` next to the
    install dir vs `$HOME`) so a fresh container isn't forever re-updating, and so the "no external
    binaries" rule isn't accidentally violated in spirit. One sentence in §C4 suffices.
15. **Coverage gate pragmatics** (D20): 85 % from day one with golden + fake-backed tests is
    achievable, but `internal/quadlet` (systemd-dbus), `internal/site` (git push flows) and
    `internal/battleye` networking edges will be the permanent weak spots. Consider a documented
    allow-list of excluded files (not packages), or a ramp (80 % at first green CI, 85 % by end of
    Phase 1) so the gate doesn't get deleted in frustration during week one.
16. **Q21 (open)**: when you decide it, note there is prior art in the ecosystem:
    `WoozyMasta/steamcmd-2fa` (MIT) generates Steam Guard codes from a TOTP secret for steamcmd.
    The plan's default (don't hold TOTP secrets) remains the right call; the tool is a good
    reference if the opt-in is ever built.
17. **Instance lifecycle minor gaps**: no rename/move procedure (`instanceId` and quadlet/unit names
    make it non-trivial — even a "documented as create-new + import" answer is fine), and instance
    deletion should also remove the exporter/Icinga expectations and transient timers. Both are
    one-liners in §C11/§C12 docs.

---

## 4. Internal consistency nits (cosmetic)

- FR list order is jumpy: FR-07a and FR-09a are inserted *before* FR-08/FR-09, FR-28 trails after
  FR-37, FR-29a after FR-29. IDs are stable so renumbering is optional, but at least sort the
  table rows by ID — cross-referencing currently requires search.
- NFR block is ordered 01–04, then 09–12, then 05–08. Same fix.
- Risks R7 appears after R8/R9. Same fix.
- "Answered" Q-block is unordered (Q18/Q17 answered before Q15, interleaved groups). Fine, but a
  sort pass would help.
- §C1 dependency licence list: add `creack/pty` (mentioned in §C7, MIT — fine) and the C17 deps
  (`pbo`, `paa`, `lzo`, `lzss`, `rvmat`, all MIT per GitHub) so the `go-licenses` allow-list and
  the plan stay in sync.
- §C2 shows `snaphots` typo-free but note `instances` and `snapshots` are configurable — the
  `Exec`/`Volume=` examples elsewhere hard-code `/var/lib/dzo`; fine as illustration, just keep
  codegen on configured paths (the plan already says so).

---

## 5. What is notably good (keep as-is)

- **Part A accuracy**: every claim I spot-checked against the legacy branches holds (find-order,
  `xmlmerge` scratch files, `/tmp/mod_command_line` for R5, `instanceId` semantics). The
  quirks list (A8) directly seeding test design (golden, property tests) is exactly the right loop.
- **Manifest-based apply model** (§C6) with pristine-managed/generated/foreign classes and
  "never touch what you don't own" — this is the correct core invariant, and property-testing it
  is the right enforcement.
- **Immutable download generations** (§C7) kill the shared-mutable `serverfiles/keys` problem and
  give rollback for free.
- **Steam auth treated as a recurring human task** (pause-not-fail, lockout protection, proactive
  probe, scrubbed logs) — unusually mature for this domain; the fake-pty test plan closes the loop.
- **Graceful restart port** of `dayz_restart` (countdown → lock → kick passes → `#kick -1` →
  systemd restart, no `#shutdown`) matches the one mechanism that demonstrably works on deerisle.
- **Split of mutable state (SQLite/PG) vs append-only history (optional ClickHouse)** with dzo
  fully functional without ClickHouse.
- **Map tiles from rvmat georeferencing** rather than guessed layouts, with per-map derivation and
  loud failure on unexpected layouts — and the library situation is better than the plan fears (§1).

---

## 6. Suggested plan edits (summary)

| # | Section | Edit |
|---|---|---|
| F1 | D29, §C20, S7, Assumptions | Pin kernel requirement (≥ 6.15) **or** document reflink-copy/root-helper fallback; S7 must run on the target kernel |
| F2 | D14, §C7 | ~~verify appid~~ resolved (1042420 confirmed by operator); only confirm workshop appid 221100 for experimental + optional `branch_password` field |
| F3 | §C5 | Define restart policy on persistent ExecStartPre/health failure (start limits or failed-state gate) |
| 3.1 | §C5/§C7 | Define timer vs per-instance `check_interval` reconciliation |
| 3.2 | §C5/§C12/§C18 | Single-owner scheduling rule (daemon materialises timers, never fires) |
| 3.3 | §C16, §C13 | Network-mode-aware `dzo-admin` endpoint; document token-as-only-guard in host mode |
| 3.4 | §C5 | Remove `network-online.target` lines from the quadlet sketch |
| 3.6 | §C18 | Verify ban.txt format; state it |
| 3.7 | §C7 | Update windows / batching / min-restart-interval per instance |
| 3.8 | §C2/§C6/§C20 | Rename per-file snapshot dir to avoid "snapshots" collision |
| 3.10 | Phase 2 | Importer produces *proposed* overlays for human review |
| 3.11 | §C16 | Note fallback for the `#ifdef` soft-dependency mechanism |
| 3.12 | §C5/instance.yaml | Add `container.memory` |
| 3.14 | §C4 | One sentence on steamcmd self-update location |
| 3.15 | D20 | Exclusion list or coverage ramp |
| 4 | Part B/F | Sort FR/NFR/R rows; complete the §C1 licence list with pbo/paa/lzo/lzss/rvmat/creack-pty |

None of these change the architecture; all of them are cheap now and expensive after Phase 1.

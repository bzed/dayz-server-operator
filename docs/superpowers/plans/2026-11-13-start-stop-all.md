<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# `dzo start --all` / `dzo stop --all` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `dzo start --all` starts every instance of the site and `dzo stop --all` stops every instance, with the same options as the one-instance commands — including the graceful countdown (`--graceful`, `--minutes`, `--lock`, `--kick`, `--text`) and `--cancel`.

**Architecture:** A `--all` flag on the existing top-level `dzo start` and `dzo stop` (the operator confirmed this over separate `start-all`/`stop-all` commands), following the `dzo logs rotate [<name> | --all]` precedent: `cobra.MaximumNArgs(1)` plus an XOR check, `tree.InstanceNames()` for the list, per-instance failure lines to stderr and a final `N instance(s) failed` error. The one-instance graceful-stop body of `newStopCmd` moves into a shared `stopWithOptions` helper so `--all` reuses it unchanged; the per-instance stops run concurrently (the operator confirmed concurrent countdowns, so every server stops at the same time instead of one after another). `dzo instance start/stop` stay single-instance systemctl wrappers.

**Tech Stack:** Go 1.27, cobra v1.10.2, std `sync` only — no new dependencies.

**Spec:** the operator's request in this session (start-all/stop-all with the same options as start/stop, especially the graceful stop), with two decisions confirmed by the operator: the `--all` flag over new commands, and concurrent per-instance countdowns.

## Global Constraints

- `make check` must pass: fmt, vet, golangci-lint v2.14.0, reuse, licences, vulncheck, `test-race`, fuzz, e2e (needs Chrome and the JS assets of docs/development.rst), coverage at or above 85%.
- The graceful-stop flags keep their meaning: with `--all` each instance counts down with its **own** `restarts.announce` values, and `--minutes`/`--lock`/`--kick`/`--text` override them for every instance; `--cancel` cancels every countdown.
- `--all` exists only on the top-level `dzo start` and `dzo stop`. `dzo instance start` / `dzo instance stop` keep `cobra.ExactArgs(1)`.
- `dzo stop --all` runs the per-instance stops **concurrently** (plain and countdown); `dzo start --all` starts them **one after another** (staggered starts are gentler on disk and IO, and `systemctl start` returns quickly).
- A plain `dzo stop --all` (no countdown flags, no `--cancel`) does not resolve the instances: stopping must not depend on everything resolving, and the unit name is all `systemctl` needs.
- All changes are in existing files (SPDX headers already in place); no new files outside this plan.
- Tests write only under `t.TempDir()`; no real systemctl, RCon, `$HOME` or `/srv`; goroutines are joined before the command returns (`TestMain` runs goleak).
- Docs change in the same commit as the behaviour: the `docs/cli.rst` rows and, for the countdown, prose in `docs/operations.rst` next to `_graceful-stop`; `make docs` passes (Sphinx, warnings are errors).
- One logical change per commit with the area prefixes of `git log`; CLI changes use `cli:`.

## Review Focus

1. **`--all` together with a name, or neither:** the command refuses and does nothing, like `dzo logs rotate`. Pinned by: Task 2 (`TestStartAllNeedsNameOrAll`), Task 3 (`TestStopAllNeedsNameOrAll`).
2. **One instance failing mid-run** (its systemctl exits non-zero, or it does not resolve): the others still start/stop, and the command fails with `N instance(s) failed`. Pinned by: Task 2 (`TestStartAllContinuesAfterAFailure`), Task 3 (`TestStopAllContinuesAfterAResolveFailure`).
3. **Concurrent writes to the command's output:** the `--all` goroutines all write through `cmd.OutOrStdout()`/`cmd.ErrOrStderr()`; in tests that is one shared buffer, so `runCmd` must write through the mutex-guarded `syncBuf` or `make test-race` fails. Pinned by: Task 3 (the `runCmd` change plus `go test -race`).
4. **A site with no instances:** `--all` succeeds and calls nothing, like the other all-instance commands. Pinned by: Task 3 (`TestStopAllWithNoInstances`).
5. **Ctrl-C during a countdown:** every countdown must end and unlock its server. This is owned by `Restarter.wait` (`internal/instance/restart.go`), already pinned by `internal/instance/restart_test.go`; the `--all` layer shares the command's context (one Ctrl-C cancels all countdowns, each unlocking its server) and adds no cancellation code of its own, so no new test.

---

### Task 1: Name the instance in `dzo stop`'s countdown and cancel messages

The plumbing `--all` needs: the one-instance graceful-stop body becomes a reusable helper, and every message the graceful path prints gains the instance's name (mandatory once several countdowns interleave; it also matches `dzo instance shutdown`'s `dzo shutdown <name>: …` style).

**Files:**
- Modify: `cmd/dzo/shortcuts.go` (`newStopCmd`, lines 30–89, plus new helpers)
- Modify: `cmd/dzo/updates.go:78-114` (`rconRestarter`), `cmd/dzo/updates.go:399-415` (`cancelCountdown`)
- Test: `cmd/dzo/shortcuts_test.go`

**Interfaces:**
- Consumes: `rconRestarter` (`cmd/dzo/updates.go:78`), `cancelCountdown` (`cmd/dzo/updates.go:400`), `resolve.AnnouncementOf` (`internal/resolve/announce.go:14`), `instance.ErrCancelled`.
- Produces (Task 3 relies on these exact names):
  - `func lifecycleOf(cmd *cobra.Command) instance.Lifecycle` — builds the systemctl runner from the `--command`/`--user` flags (replaces the inline copy in `newStopCmd` lines 53–59).
  - `type stopOptions struct { counting bool; cancel bool; minutes, lock, kick int; minutesSet, lockSet, kickSet bool; text string }` — the countdown flags of `dzo stop`; `counting` is true when any of `--graceful`/`--minutes`/`--lock`/`--kick`/`--text` was given.
  - `func stopWithOptions(cmd *cobra.Command, cfg *config.Config, inst *resolve.Instance, lc instance.Lifecycle, o stopOptions) error` — cancel or count down and stop one resolved instance.

- [ ] **Step 1: Write the failing tests** (append to `cmd/dzo/shortcuts_test.go`; both paths are proven by the existing `TestStopWithCountdownOptions`, only the messages change)

```go
func TestStopCancelNamesTheInstance(t *testing.T) {
	cfg := exporterSite(t, "")
	script := writeFakeSystemctlCmd(t, 0)
	out, err := runCmd(t, "stop", "x", "--cancel", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --cancel: %s", out)
	assert.Contains(t, out, "x: cancelled", "the cancel message names the instance")
}

func TestStopCountdownLogsNameTheInstance(t *testing.T) {
	cfg := exporterSite(t, "")
	script := writeFakeSystemctlCmd(t, 0) // is-active answers "active", but the fixture has no RCon password
	out, err := runCmd(t, "stop", "x", "--graceful", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --graceful: %s", out)
	assert.Contains(t, out, "x: RCon unreachable", "the countdown log names the instance")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/dzo/ -run 'TestStopCancelNamesTheInstance|TestStopCountdownLogsNameTheInstance' -v`
Expected: FAIL — the output has `cancelled` and `RCon unreachable` without the `x: ` prefix.

- [ ] **Step 3: Prefix the graceful-stop messages with the instance name**

In `cmd/dzo/updates.go`:

- `rconRestarter` (lines 78–114): both `Log` closures — the battleye `Session`'s (line 95) and the `Restarter`'s (line 105) — become `func(f string, a ...any) { _, _ = fmt.Fprintf(cmd.ErrOrStderr(), inst.Name+": "+f+"\n", a...) }`.
- `cancelCountdown` (line 413): `_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: cancelled\n", inst.Name)`.

- [ ] **Step 4: Extract the one-instance stop into `stopWithOptions`**

In `cmd/dzo/shortcuts.go`: add `lifecycleOf`, `stopOptions` and `stopWithOptions` (Interfaces block above) next to `newStopCmd`. `stopWithOptions` is the moved body of `newStopCmd`'s counting/cancel branch: build `r := rconRestarter(cmd, cfg, inst, lc)`; `o.cancel` → `cancelCountdown`; otherwise `resolve.AnnouncementOf(inst.Restarts.Announce, o.text)` with the `minutesSet`/`lockSet`/`kickSet` overrides, then `r.Stop(cmd.Context(), inst.Name, a)`; `instance.ErrCancelled` prints `"%s: the stop was cancelled\n"` (with the name) to stdout and returns nil. Rewire `newStopCmd`'s `RunE` to build one `stopOptions` from its locals and `f.Changed(...)` and call `stopWithOptions(cmd, cfg, inst, lifecycleOf(cmd), o)`; the plain path (`!counting && !cancel` → `plain(cmd, args)`) is untouched.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/dzo/ -v`
Expected: PASS — the two new tests and every existing test (`TestStopWithCountdownOptions`, `TestStartAndStopShortForms`, …).

- [ ] **Step 6: Commit**

```bash
git add cmd/dzo/shortcuts.go cmd/dzo/updates.go cmd/dzo/shortcuts_test.go
git commit -m "cli: name the instance in dzo stop's countdown and cancel messages"
```

### Task 2: `dzo start --all`

`newStartCmd` wraps `newInstanceStartCmd` and gains `--all`: load the site, start every instance's unit one after another in name order, count failures, keep going.

**Files:**
- Modify: `cmd/dzo/shortcuts.go:22-26` (`newStartCmd`)
- Modify: `docs/cli.rst:80-81` (the `dzo start` row)
- Test: `cmd/dzo/shortcuts_test.go`

**Interfaces:**
- Consumes: `lifecycleOf` (Task 1), `loadSite` (`cmd/dzo/site.go:14`), `(*site.Tree).InstanceNames()` (`internal/site/tree.go:228`), `configFlag` (`cmd/dzo/site.go:36`).
- Produces (test helpers Task 3 reuses):
  - `func allSite(t testing.TB) string` — `exporterSite` plus a second, resolvable instance `y`.
  - `func writeRecordingSystemctlCmd(t testing.TB, argsFile string) string` — a systemctl stub that appends its arguments to `argsFile` and exits 0.

- [ ] **Step 1: Write the failing tests and helpers** (append to `cmd/dzo/shortcuts_test.go`)

```go
// allSite is exporterSite with a second instance, y, for the --all commands.
func allSite(t testing.TB) string {
	t.Helper()
	cfg := exporterSite(t, "")
	writeFile(t, filepath.Join(filepath.Dir(cfg), "site", "instances", "y", "instance.yaml"), `name: y
product: dayz-stable
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2402, rcon: 2406, query: 27116}
network: host
`)
	return cfg
}

// writeRecordingSystemctlCmd writes a systemctl stub that appends its arguments to argsFile and succeeds.
func writeRecordingSystemctlCmd(t testing.TB, argsFile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-systemctl.sh")
	writeFile(t, path, "#!/bin/sh\necho \"$@\" >> "+argsFile+"\nexit 0\n")
	require.NoError(t, os.Chmod(path, 0o755)) //nolint:gosec // test fixture, needs to run
	return path
}

func TestStartAll(t *testing.T) {
	cfg := allSite(t)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeRecordingSystemctlCmd(t, args)
	out, err := runCmd(t, "start", "--all", "--config", cfg, "--command", script)
	require.NoError(t, err, "start --all: %s", out)
	b, err := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user start dzo-x.service", "x was started")
	assert.Contains(t, string(b), "--user start dzo-y.service", "y was started")
}

func TestStartAllNeedsNameOrAll(t *testing.T) {
	_, err := runCmd(t, "start")
	assert.Error(t, err, "start without a name and without --all must fail")
	_, err = runCmd(t, "start", "--all", "x")
	assert.Error(t, err, "start --all together with a name must fail")
}

func TestStartAllContinuesAfterAFailure(t *testing.T) {
	cfg := allSite(t)
	dir := t.TempDir()
	args := filepath.Join(dir, "systemctl-args")
	script := filepath.Join(dir, "systemctl")
	writeFile(t, script, "#!/bin/sh\necho \"$@\" >> "+args+"\nfor a in \"$@\"; do [ \"$a\" = \"dzo-y.service\" ] && exit 1; done\nexit 0\n")
	require.NoError(t, os.Chmod(script, 0o755)) //nolint:gosec // test fixture, needs to run
	_, err := runCmd(t, "start", "--all", "--config", cfg, "--command", script)
	require.Error(t, err, "one failing instance must fail the run")
	assert.Contains(t, err.Error(), "1 instance(s) failed")
	b, rerr := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, rerr, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user start dzo-x.service", "x was started although y failed")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/dzo/ -run 'TestStartAll' -v`
Expected: FAIL — `unknown flag: --all` / wrong number of args errors.

- [ ] **Step 3: Implement `--all` in `newStartCmd`**

Override the wrapped command like `newStopCmd` does: keep `plain := c.RunE`, then set `c.Use = "start [<name> | --all]"`, `c.Args = cobra.MaximumNArgs(1)`, a `Short`/`Long` that mention `--all`, and a `RunE` that: XOR-checks `all == (len(args) == 1)` → `fmt.Errorf("give an instance name or --all")`; `!all` → `plain(cmd, args)`; otherwise `loadSite(configPath)`, then **sequentially** over `tree.InstanceNames()` call `lifecycleOf(cmd).Start(cmd.Context(), n)`, printing each failure as `"%s: %v\n"` to `cmd.ErrOrStderr()` and counting it, and return `fmt.Errorf("%d instance(s) failed", failed)` if any failed. Register `configFlag(c, &configPath)` and `c.Flags().BoolVar(&all, "all", false, "every instance")`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/dzo/ -v`
Expected: PASS.

- [ ] **Step 5: Document the row**

In `docs/cli.rst` (lines 80–81) the row becomes:

```rst
* - ``dzo start <name> | --all``
  - start one instance, or every instance one after another
```

- [ ] **Step 6: Verify the docs build**

Run: `make docs`
Expected: exits 0 (Sphinx, warnings are errors).

- [ ] **Step 7: Commit**

```bash
git add cmd/dzo/shortcuts.go cmd/dzo/shortcuts_test.go docs/cli.rst
git commit -m "cli: dzo start --all starts every instance"
```

### Task 3: `dzo stop --all`

`newStopCmd` gains `--all`: plain stops run concurrently (a `systemctl stop` blocks through the unit's `ExecStop`, so serialising them would multiply the wait), and each countdown/cancel runs per instance in its own goroutine so every server stops at the same time. Each instance counts down with its own `restarts.announce` values.

**Files:**
- Modify: `cmd/dzo/shortcuts.go` (`newStopCmd`, plus `stopAll` and `stopOne`)
- Modify: `cmd/dzo/root_test.go:19-28` (`runCmd`)
- Modify: `docs/cli.rst:82-83` (the `dzo stop` row), `docs/operations.rst:123-146` (the graceful-stop section)
- Test: `cmd/dzo/shortcuts_test.go`

**Interfaces:**
- Consumes: `stopWithOptions`, `stopOptions`, `lifecycleOf` (Task 1); `resolve.Resolve(cfg, tree, name)` (`internal/resolve/resolve.go:220`); `loadSite`; `(*site.Tree).InstanceNames()`.
- Produces:
  - `func stopAll(cmd *cobra.Command, cfg *config.Config, tree *site.Tree, lc instance.Lifecycle, o stopOptions) error`
  - `func stopOne(cmd *cobra.Command, cfg *config.Config, tree *site.Tree, name string, lc instance.Lifecycle, o stopOptions) error`

- [ ] **Step 1: Make `runCmd` safe for concurrent writers**

In `cmd/dzo/root_test.go`, `runCmd` writes into `var out syncBuf` (the mutex-guarded buffer from `admin_test.go:35`) instead of `bytes.Buffer`, because the `--all` goroutines write through the command's writers concurrently; drop the then-unused `bytes` import. No behaviour change for existing tests.

- [ ] **Step 2: Write the failing tests** (append to `cmd/dzo/shortcuts_test.go`)

```go
func TestStopAll(t *testing.T) {
	cfg := allSite(t)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeRecordingSystemctlCmd(t, args)
	out, err := runCmd(t, "stop", "--all", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all: %s", out)
	b, err := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user stop dzo-x.service", "x was stopped")
	assert.Contains(t, string(b), "--user stop dzo-y.service", "y was stopped")
}

func TestStopAllNeedsNameOrAll(t *testing.T) {
	_, err := runCmd(t, "stop")
	assert.Error(t, err, "stop without a name and without --all must fail")
	_, err = runCmd(t, "stop", "--all", "x")
	assert.Error(t, err, "stop --all together with a name must fail")
}

func TestStopAllGraceful(t *testing.T) {
	cfg := allSite(t)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeRecordingSystemctlCmd(t, args)
	out, err := runCmd(t, "stop", "--all", "--graceful", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all --graceful: %s", out)
	assert.Contains(t, out, "x: RCon unreachable", "x was counted down")
	assert.Contains(t, out, "y: RCon unreachable", "y was counted down")
	b, err := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user stop dzo-x.service", "x was stopped")
	assert.Contains(t, string(b), "--user stop dzo-y.service", "y was stopped")
}

func TestStopAllCancel(t *testing.T) {
	cfg := allSite(t)
	script := writeFakeSystemctlCmd(t, 0)
	out, err := runCmd(t, "stop", "--all", "--cancel", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all --cancel: %s", out)
	assert.Contains(t, out, "x: cancelled", "x's countdown was cancelled")
	assert.Contains(t, out, "y: cancelled", "y's countdown was cancelled")
	dir := filepath.Dir(cfg)
	for _, n := range []string{"x", "y"} {
		_, err := os.Stat(filepath.Join(dir, "instances", n, "runtime", "restart-cancel")) //nolint:gosec // test fixture path under t.TempDir()
		assert.NoError(t, err, "the cancel file of %s exists", n)
	}
}

func TestStopAllContinuesAfterAResolveFailure(t *testing.T) {
	cfg := allSite(t)
	writeFile(t, filepath.Join(filepath.Dir(cfg), "site", "instances", "y", "instance.yaml"), `name: y
product: nonexistent
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2402, rcon: 2406, query: 27116}
network: host
`)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeRecordingSystemctlCmd(t, args)
	_, err := runCmd(t, "stop", "--all", "--graceful", "--config", cfg, "--command", script)
	require.Error(t, err, "one instance failing to resolve must fail the run")
	assert.Contains(t, err.Error(), "1 instance(s) failed")
	b, rerr := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, rerr, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user stop dzo-x.service", "x was stopped although y does not resolve")
}

func TestStopAllWithNoInstances(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	writeFile(t, cfg, "paths:\n  data: "+dir+"\n")
	script := writeFakeSystemctlCmd(t, 0)
	out, err := runCmd(t, "stop", "--all", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all on an empty site: %s", out)
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./cmd/dzo/ -run 'TestStopAll' -v`
Expected: FAIL — `unknown flag: --all` / wrong number of args errors.

- [ ] **Step 4: Implement `--all` in `newStopCmd`**

In `cmd/dzo/shortcuts.go`:

- `stopOne` (signature in the Interfaces block): `!o.counting && !o.cancel` → `lc.Stop(cmd.Context(), name)` (no resolution — a plain stop must work even when an instance does not resolve); otherwise `resolve.Resolve(cfg, tree, name)`, then `stopWithOptions(cmd, cfg, inst, lc, o)`.
- `stopAll`: one goroutine per name of `tree.InstanceNames()` (Go 1.22+ loop-variable semantics; capture `n` directly), each calling `stopOne`; failures append `"%s: %v\n"` to `cmd.ErrOrStderr()` and increment a `sync.Mutex`-guarded counter; `sync.WaitGroup` join, then `fmt.Errorf("%d instance(s) failed", failed)` if any. All goroutines share `cmd.Context()`, so one Ctrl-C cancels every countdown (and `Restarter.wait` unlocks each server on the way out).
- `newStopCmd`: `c.Args = cobra.MaximumNArgs(1)`; `c.Use = "stop [<name> | --all] [--graceful | --minutes N --lock N --kick N --text T] [--cancel]"`; the `RunE` XOR-checks `all == (len(args) == 1)`, builds the `stopOptions` (as in Task 1), keeps the one-instance branch (`!all`: plain → `plain(cmd, args)`, else `loadInstance` + `stopWithOptions`) and adds: `loadSite(configPath)` then `stopAll(cmd, cfg, tree, lifecycleOf(cmd), o)`. Register `c.Flags().BoolVar(&all, "all", false, "every instance")` and extend `Long` with the `--all` semantics (concurrent stops, per-instance announce values, one failure does not stop the others).

- [ ] **Step 5: Run the tests to verify they pass, also under the race detector**

Run: `go test ./cmd/dzo/ -v` then `go test -race ./cmd/dzo/ -run 'TestStopAll|TestStartAll' -v`
Expected: PASS, race-clean (the goroutines are joined before the command returns, so goleak stays happy too).

- [ ] **Step 6: Document the row and the prose**

In `docs/cli.rst` (lines 82–83) the row becomes:

```rst
* - ``dzo stop <name> | --all [--graceful | --minutes N --lock N --kick N --text T] [--cancel]``
  - stop and stay stopped; with a countdown option, announce, lock and kick the players first (:ref:`graceful-stop`); ``--all`` takes every instance, their countdowns running together
```

In `docs/operations.rst`, at the end of the "Stopping with a countdown" section (after the `tmux`/`systemd-run` paragraph, before "Scheduled restarts"), add:

```rst
``dzo stop --all`` (and ``dzo start --all``) take every instance of the site. The stops run at
the same time, so every server's graceful stop waits in parallel; with a countdown each instance
counts down with its own ``restarts.announce`` numbers — the flags override them for every
instance — and all servers stop together. The log lines and the cancel notice carry the
instance's name. An instance that fails (its unit cannot be stopped, or it does not resolve)
does not stop the others; the command fails with ``N instance(s) failed``. ``dzo stop --all
--cancel`` ends the countdown of every instance. ``dzo start --all`` starts the instances one
after another.
```

- [ ] **Step 7: Verify the docs build**

Run: `make docs`
Expected: exits 0.

- [ ] **Step 8: Commit**

```bash
git add cmd/dzo/shortcuts.go cmd/dzo/root_test.go cmd/dzo/shortcuts_test.go docs/cli.rst docs/operations.rst
git commit -m "cli: dzo stop --all stops every instance, the countdowns running together"
```

### Task 4: The full gate

**Files:** none (verification only; fix and fold any finding into the owning task's files, then commit with `fix:`).

**Interfaces:** none.

- [ ] **Step 1: Format and vet**

Run: `make fmt vet`
Expected: clean (or no changes from `make fmt`).

- [ ] **Step 2: Run the whole gate**

Run: `make check`
Expected: passes — fmt-check, vet, lint (golangci-lint v2.14.0), reuse, licenses, vulncheck, `test-race`, fuzz, e2e (needs Chrome/Chromium and libjs-htmx, libjs-leaflet, fonts-fork-awesome, see docs/development.rst), coverage-check at or above 85%.

- [ ] **Step 3: Fix anything it finds, in the owning task's files**

Each fix is its own `fix:` commit (or folded into the task's commit if the branch has not been pushed yet).

- [ ] **Step 4: Commit any fixes**

```bash
git add -A && git commit -m "fix: whatever the gate found"
```

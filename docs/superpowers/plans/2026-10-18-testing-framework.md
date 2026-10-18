<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Testing Framework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the test suite stricter and easier to read: testify assertions everywhere, lint rules that keep the test conventions from AGENTS.md enforced instead of hoped for, goroutine-leak checks, fuzzing of the untrusted-input parsers, and browser end-to-end tests of the web UI that run locally as part of `make check`.

**Architecture:** testify (`require`/`assert`) replaces raw `t.Fatal*`/`t.Error*` package by package; once all packages are migrated, golangci-lint rules (`testifylint`, `thelper`, `usetesting`, `forbidigo`) lock the conventions in, and `go test -shuffle=on` flushes out order dependencies. `go.uber.org/goleak` checks that every package with background goroutines stops them. Native Go fuzz targets cover the BattlEye, A2S, `serverDZ.cfg` and quantity parsers; their seeds run in every `go test`, the fuzzing itself in `make fuzz`. Browser tests use chromedp against the installed Chrome/Chromium, live in `internal/web` behind the `e2e` build tag, reuse the existing `newEnv` fake backends, and run through `make e2e`. Everything runs in GitHub CI too: a `fuzz` job, and an `e2e` job in a `debian:trixie` container with Debian's `chromium`, `libjs-htmx` and `libjs-leaflet` (the versions a dzo installation actually serves), plus a non-blocking `debian:forky` run that warns early about the next htmx (4.0 beta in forky/sid). `make check` keeps mirroring CI, so it runs fuzz and e2e as well.

**Tech Stack:** Go 1.27, `github.com/stretchr/testify` (latest v1), `go.uber.org/goleak` (latest v1), `github.com/chromedp/chromedp` (latest), golangci-lint v2.14.0, Chrome/Chromium, Debian `libjs-htmx` and `libjs-leaflet`.

**Spec:** the request of 2026-10-18 (no separate spec): "a better testing framework, including selenium or better options for e2e testing; consider switching to testify; keeping our test requirements stricter is good", amended the same day: "we can run everything in the GitHub CI if it's supported to run GUI tests there". It is: the runners need no display for headless Chrome, and the job brings its own Debian browser.

### Decision: chromedp, not Selenium or Playwright

| Option | Why not / why |
|---|---|
| Selenium (`tebeka/selenium`) | needs a WebDriver binary matched to the browser version plus a client library that is unmaintained; a second moving part per test run |
| Playwright for Go | downloads a Node driver and its own browser builds at test time (network, hundreds of MB, not Debian-packaged) |
| go-rod | close second; downloads its own Chromium unless configured not to |
| **chromedp** | pure Go (MIT), speaks the DevTools protocol to the Chrome/Chromium already installed (the cheatsheet build already needs Chrome), no driver process, no downloads. Cost: no Firefox coverage, accepted |

## Global Constraints

- `make check` must pass after every task (AGENTS.md); total coverage stays ≥ 85 % (`COVERAGE_MIN`), never lowered, no code excluded.
- golangci-lint stays at **v2.14.0**; no version bump in this work.
- New dependencies: `github.com/stretchr/testify`, `go.uber.org/goleak`, `github.com/chromedp/chromedp` only. Each commit that adds one names it and why in the message, and runs `make licenses` (MIT/BSD/Apache/ISC are on the allow-list).
- Behaviour-preserving migration: a migrated package has the same tests (same names, same subtests) and no weaker checks. A check that was `t.Fatal` becomes `require`, a check that was `t.Error` becomes `assert`, unless the test cannot continue after it failing (then `require`).
- No `time.Sleep` in tests (AGENTS.md): wait with `require.Eventually`, a channel, or a fake clock; a pacing loop uses `time.NewTicker` in a `select` with `ctx.Done()`.
- Tests write only under `t.TempDir()` — this includes the browser's user-data directory.
- e2e tests: build tag `e2e`, file names `e2e*_test.go`, test names `TestE2E*`, package `web`. Under the tag a missing browser or missing JS assets fail the test (never skip); without the tag they do not compile into `go test ./...`.
- e2e environment: `DZO_E2E_CHROME` (browser binary; default: chromedp's lookup, which finds `google-chrome` and `chromium`), `DZO_E2E_ASSETS` (default `/usr/share/javascript`, must hold `htmx/htmx.min.js`, `leaflet/leaflet.js`, `leaflet/leaflet.css`).
- CI (`.github/workflows/ci.yml`) gains two jobs, `fuzz` (Task 7) and `e2e` (Task 8), both required for the `deb` job (`needs:`), except the `debian:forky` e2e matrix entry, which is `continue-on-error: true`. New third-party actions: none; reuse the SHA-pinned `checkout`, `setup-go` and `upload-artifact` already in the file. `make check` stays the local mirror of CI.
- The e2e job runs Chromium with its sandbox on, as an unprivileged user; `--no-sandbox` is not an option the harness offers.
- **A real DayZ server never runs in CI**, and nothing in CI or `make check` may need one (Steam, the DayZ server/client, sway, one game at a time). CI and `make check` use the fakes (`newEnv`, `boottest`'s `DZO_FAKE_SERVER`). The real-server tier is `make test-client` (`scripts/test-client.sh`), run by hand; Task 12 adds the browser to it and the AGENTS.md rule for when it is required.
- Commit prefixes: `test:` for test-only changes, `build:` for Makefile/lint config, `web:` / `fix:` for product code fixed because a new check found a bug. Docs change in the same commit as the behaviour they describe.

## Review Focus

1. **htmx inline handlers under the CSP.** `instance.html` and the player panel in `fragments.html` use `hx-on::after-request="this.reset()"` (3 places); htmx compiles `hx-on` with `Function()`, which the CSP (`default-src 'self'`, no `'unsafe-eval'`) blocks. A real browser reports a CSP violation and the message forms do not reset. Found by `TestE2EPagesLoadWithoutErrors` and fixed in Task 8; the reset behaviour is pinned by `TestE2EMessageFormResets` (Task 9).
2. **`hx-confirm` dialogs.** Kill, delete, revoke and unlink go through `window.confirm`. A headless browser that never answers hangs the test until the timeout. The harness answers JavaScript dialogs; `TestE2EKillAsksForConfirmation` (Task 9) pins both "dismiss sends nothing" and "accept sends the command".
3. **Polling throws away an open panel.** `rows.js` pauses `data-poll` refreshes while a `.rowpanel` is open. Pinned by `TestE2EPollingWaitsWhileAPanelIsOpen` (Task 9).
4. **Player names are untrusted.** `<b>alice</b>` must reach the DOM as text in the table, the info popup and the map marker. Pinned by `TestE2EPlayerNameIsText` (Task 8) and the map assertion in Task 10.
5. **Shuffle and goleak exposing hidden coupling.** Tests that pass only in file order or that leave goroutines (e.g. `answerCommands` in `web_test.go` is never waited for) fail once Tasks 5 and 6 land. Fix the test or the leak; an `IgnoreTopFunction` is allowed only for a third-party goroutine, with a comment naming it.

---

## File Structure

| File | Responsibility |
|---|---|
| `docs/development.rst` | new "Testing" section: conventions, `make e2e`, `make fuzz`, environment variables |
| `AGENTS.md` | `make check` table gains `fuzz` and `e2e` rows; coverage section points at the conventions |
| `Makefile` | `-shuffle=on` on `test`/`test-race`; `vet` with `-tags e2e`; new `fuzz`, `e2e` targets; `check` runs both |
| `.github/workflows/ci.yml` | new `fuzz` and `e2e` jobs; `deb` needs them |
| `.golangci.yml` | `run.build-tags: [e2e]`; enables `testifylint`, `thelper`, `usetesting`, `forbidigo` with test-only scope |
| `REUSE.toml` | entry for `**/testdata/fuzz/**` (generated corpus files cannot carry headers) |
| `**/*_test.go` | migrated to testify, `t.Context()`, no sleeps |
| `*/main_test.go` (per package listed in Task 6) | `TestMain` with `goleak.VerifyTestMain` |
| `*/fuzz_test.go` (battleye, a2s, servercfg, admin) | fuzz targets |
| `internal/web/e2e_test.go` | harness: browser, real assets, console/CSP collector, dialog handling |
| `internal/web/e2e_pages_test.go`, `e2e_actions_test.go`, `e2e_map_test.go`, `e2e_bans_test.go` | scenarios |
| `internal/web/live_test.go` | `//go:build e2e && live`: the browser against a real `dzo serve` started by `scripts/test-client.sh` |
| `scripts/test-client.sh` | runs `live_test.go` as one of its checks |

---

### Task 1: testify, the conventions, and `internal/web` as the reference package

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/web/web_test.go`, `internal/web/bans_test.go`
- Modify: `docs/development.rst` (new section "Testing" after "Building")

**Interfaces:**
- Produces: the conventions every later task follows; `env` helpers keep their names (`newEnv`, `get`, `post`, `postForm`, `postMultipart`, `answerCommands`) but `answerCommands` now returns a `func()` that cancels **and waits** for its goroutine.

- [ ] **Step 1: Record the baseline**

Run: `go test ./internal/web/ -v -count=1 2>&1 | grep -E '^\s*--- (PASS|FAIL|SKIP)' | sort > /tmp/web-before.txt; wc -l /tmp/web-before.txt`
Expected: all PASS; keep the file for Step 4.

- [ ] **Step 2: Add testify and migrate the two files**

`go get github.com/stretchr/testify@latest`. Rewrite each `if cond { t.Fatalf(...) }` into `require.*`, each `t.Errorf` check into `assert.*`, choosing the most specific assertion (`require.Equal`, `require.Contains`, `assert.NotContains`, `require.NoError`, `require.Len`, …; `testifylint` will insist on this in Task 5). Keep a failure message only where it adds meaning the assertion does not carry (e.g. which page/route). Replace `context.Background()` with `t.Context()` in tests and helpers that have a `t`. Helpers take `t testing.TB` (or keep `e.t`) and call `t.Helper()` first. `answerCommands` paces with a `time.NewTicker(time.Millisecond)` in a `select` on `ctx.Done()`, and its returned func cancels and waits on a `sync.WaitGroup`/done channel.

- [ ] **Step 3: Write the Testing section of `docs/development.rst`**

Content, as rules: `require` by default, `assert` only for independent checks after which the test still makes sense; no `t.Fatal*`/`t.Error*` (`t.Skip*`, `t.Log*` are fine); `t.Context()`, `t.TempDir()`, `t.Setenv()`, `t.Chdir()` instead of their global counterparts; helpers start with `t.Helper()`; no `time.Sleep`, use `require.Eventually`, channels or a fake clock; one sentence each on goleak, fuzzing and e2e with "see below" (filled by Tasks 6–8).

- [ ] **Step 4: Verify the migration preserved the tests**

Run: `go test ./internal/web/ -v -count=1 -race 2>&1 | grep -E '^\s*--- (PASS|FAIL|SKIP)' | sort | diff /tmp/web-before.txt -`
Expected: no diff. Then `make docs` and `make check` pass.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/web/web_test.go internal/web/bans_test.go docs/development.rst
git commit -m "test: testify for internal/web and the testing conventions

testify (MIT; deps go-spew ISC, go-difflib BSD-3, yaml.v3 MIT/Apache)
replaces hand-written if/t.Fatalf checks: shorter tests, diffs on failure."
```

---

### Task 2: migrate `cmd/dzo`

**Files:** Modify: `cmd/dzo/*_test.go` (24 files; `root_test.go` keeps its `TestMain`)

**Interfaces:** Consumes: conventions from Task 1.

- [ ] **Step 1: Baseline** — as Task 1 Step 1 with `./cmd/dzo/` into `/tmp/cmd-before.txt`.
- [ ] **Step 2: Migrate** — as Task 1 Step 2, including the `time.Sleep` in `admin_test.go` and `exporter_test.go` (replace by `require.Eventually` on the condition the sleep waited for).
- [ ] **Step 3: Verify** — the Task 1 Step 4 diff against `/tmp/cmd-before.txt` is empty; `grep -nE '\bt\.(Fatal|Fatalf|Error|Errorf)\(|time\.Sleep|context\.Background\(\)' cmd/dzo/*_test.go` prints nothing; `make check` passes.
- [ ] **Step 4: Commit** — `git commit -m "test: testify for cmd/dzo"`

---

### Task 3: migrate the large internal packages

**Files:** Modify: `*_test.go` in `internal/{moderation,mission,site,product,battleye,api,instance}`

Same four steps as Task 2, per package list above, one commit `test: testify for moderation, mission, site, product, battleye, api, instance`. The sleeps to replace: `battleye/{client,livebans,live,session}_test.go`, `api/api_test.go`, `instance/graceful_test.go`, `moderation/testutil_test.go`. A sleep that stands in for "the server is slow" becomes a handler that blocks on a channel the test closes.

---

### Task 4: migrate the remaining packages

**Files:** Modify: every remaining `*_test.go` under `internal/` (admin, moddeps, steam, backup, monitor, notify, servermods, a2s, apiclient, ce, health, integrate, legacy, runfiles, serve, and the single-file packages).

Same four steps as Task 2, one commit `test: testify for the remaining internal packages`. Sleeps: `admin/hub_test.go`, `apiclient/client_test.go`, `cache/store_test.go`, `exporter/exporter_test.go` (4), `notify/coalesce_test.go`. Final verification over the whole tree:

Run: `grep -rnE '\bt\.(Fatal|Fatalf|Error|Errorf)\(|time\.Sleep\(|context\.Background\(\)' --include='*_test.go' . | grep -v '^./.claude/'`
Expected: no output.

---

### Task 5: lock the conventions in with lint rules and shuffled tests

**Files:**
- Modify: `.golangci.yml`, `Makefile` (`test`, `test-race`), `AGENTS.md` ("Coverage" section), `docs/development.rst`

- [ ] **Step 1: Prove the rules are missing** — create a scratch file `internal/version/zz_lint_probe_test.go` containing a test that calls `t.Fatalf`, `time.Sleep(time.Millisecond)`, `context.Background()`, `assert.Equal(t, len(s), 2)` and a helper without `t.Helper()`. Run `make lint`. Expected: passes (nothing catches them yet).

- [ ] **Step 2: Enable the linters** in `.golangci.yml`:
  - `run.build-tags: [e2e]` (so Tasks 8–11 are linted).
  - `linters.enable`: add `testifylint`, `thelper`, `usetesting`, `forbidigo`.
  - `linters.settings.testifylint.enable-all: true`.
  - `linters.settings.forbidigo.forbid`: `pattern: ^time\.Sleep$` (msg: "tests wait with require.Eventually, a channel or a fake clock") and `pattern: ^(.+\.)?t\.(Error|Errorf|Fatal|Fatalf)$` (msg: "use testify require/assert").
  - `linters.exclusions.rules`: `path-except: _test\.go`, `linters: [forbidigo, thelper, testifylint, usetesting]` so the rules apply to tests only.

- [ ] **Step 3: Verify every probe line is reported** — `make lint` now reports each of the five probe violations. If the `t.Fatalf` pattern misses `e.t.Fatalf` or `tb.Fatalf`, widen the regex until it catches all three spellings. Delete the probe file; `make lint` passes on the tree (fix any findings in tests, not with `//nolint`).

- [ ] **Step 4: Shuffle** — add `-shuffle=on` to the `go test` arguments of `test` and `test-race`. Run `make test-race` three times. Expected: passes each time; a failure that names a seed is an order dependency — reproduce with `-shuffle=<seed>` and fix the test.

- [ ] **Step 5: Docs** — AGENTS.md "Coverage" gets one line: "The test conventions are in `docs/development.rst` (Testing) and enforced by the linters in `.golangci.yml`." `docs/development.rst` lists the four linters and `-shuffle=on`. `make docs`, `make check` pass.

- [ ] **Step 6: Commit** — `git commit -m "build: lint the test conventions and shuffle the test order"`

---

### Task 6: goroutine-leak checks

**Files:**
- Create: `main_test.go` in `internal/{admin,api,apiclient,battleye,exporter,instance,monitor,moderation,notify,serve,web}`
- Modify: `cmd/dzo/root_test.go` (existing `TestMain`)
- Modify: `go.mod`, `go.sum`, `docs/development.rst`
- Modify: whatever leaks (test or product code)

**Interfaces:** Produces: `func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }` in each listed package; in `cmd/dzo/root_test.go` the existing `os.Exit(m.Run())` becomes `goleak.VerifyTestMain(m)`, after the `os.Setenv(allowRootEnv, "1")` it already does.

- [ ] **Step 1: Add goleak and the `TestMain`s** — `go get go.uber.org/goleak@latest`.
- [ ] **Step 2: Run and list the leaks** — `go test -count=1 ./cmd/dzo/ ./internal/...`; collect every "found unexpected goroutines" report. Expected: some (at least `answerCommands` if Task 1 missed it, and idle HTTP keep-alive connections from `http.DefaultClient`).
- [ ] **Step 3: Fix each leak** — test-side leaks: wait for the goroutine, close idle connections (`client.CloseIdleConnections()` in `t.Cleanup`) or use a per-test `http.Client`. Product-side leaks (a goroutine that ignores its context): fix in product code with a separate `fix:` commit whose test is the goleak check that failed. `goleak.IgnoreTopFunction` only for a third-party goroutine, with a comment naming the library and why it cannot be stopped.
- [ ] **Step 4: Verify** — `make check` passes; run `make test-race` twice more (goleak plus shuffle).
- [ ] **Step 5: Docs and commit** — `docs/development.rst`: "a package that starts goroutines has a `TestMain` with `goleak.VerifyTestMain`". `git commit -m "test: goleak checks for every package with background goroutines"` with the dependency justification (MIT, no transitive deps).

---

### Task 7: fuzz the untrusted-input parsers

**Files:**
- Create: `internal/battleye/fuzz_test.go`, `internal/a2s/fuzz_test.go`, `internal/servercfg/fuzz_test.go`, `internal/admin/fuzz_test.go`
- Modify: `Makefile` (new `fuzz` target, `check` depends on it), `.github/workflows/ci.yml` (new `fuzz` job), `REUSE.toml`, `AGENTS.md`, `docs/development.rst`

**Interfaces:**
- Consumes: `battleye.DecodeLoginResponse(packet []byte) (bool, error)`, `battleye.DecodeCommandResponse(packet []byte) (seq byte, multipart *MultipartInfo, payload []byte, err error)`, `battleye.DecodeServerMessage(packet []byte) (seq byte, message []byte, err error)`, `battleye.ParsePlayers(raw string) []PlayerInfo`, `battleye.ParseEvent(payload string) Event`, `a2s.ParseInfoResponse(body []byte) (InfoResponse, error)`, `servercfg.Parse(data []byte) (*File, error)` with `(*File).Bytes() []byte`, `admin.ParseQuantity(s string) (float64, error)`.
- Produces: `make fuzz` (variable `FUZZTIME ?= 10s`).

- [ ] **Step 1: Write the fuzz targets** — seeds are the vectors the existing tests already use (`f.Add(...)` for each). Properties:

| Target | Property beyond "does not panic" |
|---|---|
| `FuzzDecodeLoginResponse`, `FuzzDecodeServerMessage` | `err == nil` ⇒ the returned slice is no longer than the packet |
| `FuzzDecodeCommandResponse` | `err == nil` ⇒ `len(payload) <= len(packet)`; `multipart != nil` ⇒ its index is below its count |
| `FuzzParsePlayers`, `FuzzParseEvent` | none (total functions) |
| `FuzzParseInfoResponse` | none |
| `FuzzServercfgParse` | `Parse` ok ⇒ `Parse(f.Bytes())` ok and its `Bytes()` equal `f.Bytes()` (rendering is a fixpoint) |
| `FuzzParseQuantity` | `err == nil` ⇒ result is not NaN/Inf and is `>= 0` or one of `QuantityDefault`, `QuantityMax` |

- [ ] **Step 2: Run each briefly** — `go test ./internal/servercfg -run '^$' -fuzz '^FuzzServercfgParse$' -fuzztime 30s` and likewise per target. A crash is a real bug: keep the generated `testdata/fuzz/<Target>/<file>` as the regression test and fix the parser in a separate `fix:` commit.
- [ ] **Step 3: `make fuzz`** — a target that runs every fuzz target for `$(FUZZTIME)`, one `go test -run '^$$' -fuzz '^<Name>$$' -fuzztime $(FUZZTIME) <pkg>` per target (Go fuzzes one target per invocation); `check` gains `fuzz`. Add the `REUSE.toml` annotation for `**/testdata/fuzz/**` (AGPL-3.0-or-later, same copyright line). AGENTS.md table: row `| fuzzing | make fuzz | 10 s per target; the seeds also run in make test |`. docs: how to run one target longer and what to commit after a crash.
- [ ] **Step 3a: CI job** — `fuzz` on `ubuntu-latest`: checkout, setup-go (same pinned SHAs as the `test` job), `make fuzz`; on failure upload `**/testdata/fuzz/**` with the pinned `upload-artifact` so the crashing input can be committed. Add `fuzz` to the `needs:` of `deb`.
- [ ] **Step 4: Verify** — `make fuzz` passes; `make reuse`; `make check`; push the branch and the `fuzz` job is green.
- [ ] **Step 5: Commit** — `git commit -m "test: fuzz the BattlEye, A2S, serverDZ.cfg and quantity parsers"`

---

### Task 8: e2e harness and the page checks

**Files:**
- Create: `internal/web/e2e_test.go`, `internal/web/e2e_pages_test.go`
- Modify: `go.mod`, `go.sum`, `Makefile` (`e2e`, `vet`, `check`), `.github/workflows/ci.yml` (new `e2e` job), `AGENTS.md`, `docs/development.rst`
- Modify (fix): `internal/web/templates/instance.html`, `internal/web/templates/fragments.html`, `internal/web/static/rows.js`, and `internal/web/web_test.go` if it asserts on `hx-on`

**Interfaces:**
- Consumes: `newEnv(t *testing.T, role api.Role) *env` and its fields `web *httptest.Server`, `hub *admin.Hub`, `assets string`.
- Produces (all in `e2e_test.go`, `//go:build e2e`):
  - `func useRealAssets(t *testing.T, dst string)` — copies `htmx/htmx.min.js`, `leaflet/leaflet.js`, `leaflet/leaflet.css` (and `leaflet/images/*` if present) from `$DZO_E2E_ASSETS` (default `/usr/share/javascript`) into `dst` (callers pass `e.assets`, overwriting the stub; Task 12 passes a `t.TempDir()`); `require.FileExists` with the message "install libjs-htmx and libjs-leaflet or set DZO_E2E_ASSETS".
  - `type browser struct { ctx context.Context; dialogs chan<- bool }` — `dialogs` receives the answer for the next `window.confirm` (true = accept).
  - `func newBrowser(t *testing.T) *browser` — no `*env`, so Task 12 can use it against a real backend; headless exec allocator from `chromedp.DefaultExecAllocatorOptions` plus `chromedp.UserDataDir(t.TempDir())` and `chromedp.ExecPath($DZO_E2E_CHROME)` when set; context bounded by `context.WithTimeout(t.Context(), 30*time.Second)`, cancelled in `t.Cleanup`. Enables the `log` domain and listens for `runtime.EventExceptionThrown`, `runtime.EventConsoleAPICalled` of type `error`, and `log.EventEntryAdded` of level `error` (this is where CSP violations arrive), collected under a mutex; a `t.Cleanup` asserts the collection is empty, listing each entry. `page.EventJavascriptDialogOpening` is answered from `dialogs` (no pending answer = dismiss, and the test fails with "unexpected dialog: <message>"). A failing first `chromedp.Run` fails with "no Chrome/Chromium found; install one or set DZO_E2E_CHROME".
  - `func (b *browser) run(t *testing.T, actions ...chromedp.Action)` — `require.NoError(t, chromedp.Run(b.ctx, actions...))`.
  - `func (e *env) url(path string) string` — `e.web.URL + path`.

- [ ] **Step 1: Write the failing page tests** in `e2e_pages_test.go`:

```go
func TestE2EPagesLoadWithoutErrors(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	for _, p := range []string{"/", "/b/main/i/alpha", "/b/main/i/alpha/map", "/b/main/audit", "/b/main/bans", "/b/main/bans/new"} {
		b.run(t, chromedp.Navigate(e.url(p)), chromedp.WaitReady("body"))
	}
	// the collector's cleanup fails the test on any console error, exception or CSP report
}

func TestE2EPlayerNameIsText(t *testing.T) {
	// navigate to /b/main/i/alpha, chromedp.WaitVisible(`td.who`) (filled by the htmx "load" poll),
	// then: require.Contains(text of td.who, "<b>alice</b>");
	// require.Zero(evaluate `document.querySelectorAll("td.who b").length`)
}
```

- [ ] **Step 2: Run and see them fail** — before the harness exists: `go test -tags e2e -run '^TestE2E' ./internal/web/` fails to compile (`newBrowser` undefined).
- [ ] **Step 3: Implement the harness** as specified in Interfaces (`go get github.com/chromedp/chromedp@latest`).
- [ ] **Step 4: Make targets** — `e2e: ; $(GO) test -tags e2e -count=1 -race -run '^TestE2E' ./internal/web/`; `vet: ; $(GO) vet ./... && $(GO) vet -tags e2e ./internal/web/`; `check` gains `e2e` after `test-race`. Run `make e2e`. Expected: `TestE2EPagesLoadWithoutErrors` fails on `/b/main/i/alpha` with CSP reports for `hx-on` (Review Focus 1); any other console/CSP failure is a real bug too.
- [ ] **Step 4a: Fix the `hx-on` handlers** — remove all three `hx-on::after-request="this.reset()"`, mark those forms `data-reset`, and in `static/rows.js` add one `htmx:afterRequest` listener that calls `reset()` on `e.detail.elt` when it is a `form[data-reset]` and `e.detail.successful`. `make e2e` passes. Own commit, before the harness commit: `web: reset the message forms from rows.js; the CSP blocks hx-on`.
- [ ] **Step 5: Verify the harness fails loudly** — `DZO_E2E_CHROME=/nonexistent make e2e` fails with the Chrome message; `DZO_E2E_ASSETS=/nonexistent make e2e` fails with the assets message; `make test` still does not compile the e2e files (`go list -f '{{.TestGoFiles}}' ./internal/web` has no `e2e_*`).
- [ ] **Step 5a: CI job** — `e2e` on `ubuntu-latest` with `container: image: debian:${{ matrix.suite }}`, matrix `suite: [trixie, forky]`, `continue-on-error: ${{ matrix.suite == 'forky' }}`, container `options: --security-opt seccomp=unconfined` (Docker's default seccomp profile blocks the user namespaces Chromium's sandbox needs). Steps, following the `deb` job: apt-get `git ca-certificates make chromium libjs-htmx libjs-leaflet`; checkout; setup-go; `useradd -m tester`, `chown -R tester` the workspace, and run `runuser -u tester -- env PATH="$PATH" HOME=/home/tester make e2e` (Chromium refuses to sandbox as root). Add `e2e` to the `needs:` of `deb` (the forky entry cannot block it because of `continue-on-error`). If Chromium still reports "No usable sandbox" in the container, stop and report back instead of adding `--no-sandbox`.
- [ ] **Step 6: Docs** — AGENTS.md table: row `| browser e2e | make e2e | needs Chrome/Chromium and libjs-htmx + libjs-leaflet locally (CI brings its own in a trixie container), see docs/development.rst |`. `docs/development.rst`: the e2e section (why chromedp, the two env vars, the CI job and the non-blocking forky run, how to watch a run with `chromedp.Flag("headless", false)` when debugging, never committed).
- [ ] **Step 6a: Verify in CI** — push the branch; the trixie `e2e` job is green. A red forky job is expected information, not a blocker; note its failure in the task report.
- [ ] **Step 7: Commit** — `git commit -m "test: browser e2e tests for the web UI with chromedp"`, justifying chromedp (MIT; deps cdproto, sysutil, gobwas/ws MIT, go-json-experiment/json BSD-3; uses the installed browser, no driver or download).

---

### Task 9: e2e — player actions

**Files:** Create: `internal/web/e2e_actions_test.go`

**Interfaces:** Consumes: Task 8 harness; `(*env).answerCommands(ok bool, msg string) func()`; `e.hub` to observe sent commands via the audit page or the `answerCommands` result.

- [ ] **Step 1: Write the failing tests**
  - `TestE2EKickDialogSendsTheKick` — open the instance page, click the `Actions` button (`button[data-rowpanel]`) of alice's row, wait for `tr.rowpanel`, open the kick `<dialog id="kick-7656">`, fill a reason, submit; with `answerCommands(true, "kicked")` running, `#panel-flash` shows "kicked".
  - `TestE2EKillAsksForConfirmation` — send `false` on `b.dialogs`, click Kill: no command reaches the hub (assert with `require.Never` over 300 ms on a counter incremented by a wrapper around `answerCommands`); send `true`, click again: `require.Eventually` sees exactly one kill.
  - `TestE2EPollingWaitsWhileAPanelIsOpen` — open a panel, then `hub.Sync` a second player; for 6 s (> the 5 s poll) the panel is still in the DOM and the new player is not shown (`require.Never`); click the panel's `button[data-close]`, the new player appears (`require.Eventually`, 10 s).
  - `TestE2EMessageFormResets` — with `answerCommands(true, "sent")` running, type into the instance message form and submit; `require.Eventually` the input value is `""`. Same for the player panel's "Direct message" form.
- [ ] **Step 2: Run** — `make e2e`. Expected: PASS (the `hx-on` fix landed in Task 8); a failure is a real bug, fixed in a separate `web:` commit with the failing test as proof.
- [ ] **Step 3: Verify** — `make check` passes.
- [ ] **Step 4: Commit** — `git commit -m "test: e2e for the player actions"`

---

### Task 10: e2e — the live map

**Files:** Create: `internal/web/e2e_map_test.go`

**Interfaces:** Consumes: Task 8 harness; `e.hub.Sync(admin.SyncRequest{...})` to move a player.

- [ ] **Step 1: Write the failing tests**
  - `TestE2EMapShowsPlayersAndVehicles` — navigate to `/b/main/i/alpha/map`; wait for `.leaflet-container`; the number of player markers is 1 and vehicle markers is 4 (read `map.js` for the marker class names); the player marker's text contains `<b>alice</b>` as text and the marker contains no `<b>` element.
  - `TestE2EMapFollowsTheStream` — read alice's marker `style.transform`; `hub.Sync` alice to `X: 5000, Z: 5000`; `require.Eventually` (10 s) the transform differs.
- [ ] **Step 2: Run** — `make e2e`. Expected: PASS once selectors are right; a failure here is a real map/SSE bug, fixed in a separate `web:` commit with this test as its proof.
- [ ] **Step 3: Commit** — `git commit -m "test: e2e for the live map"`

---

### Task 11: e2e — the ban form

**Files:** Create: `internal/web/e2e_bans_test.go`

**Interfaces:** Consumes: Task 8 harness; the two moderation stores behind backends `main` and `other` (as `TestBanFormCreatesAndFansOut` in `bans_test.go` reads them).

- [ ] **Step 1: Write the failing test** `TestE2EBanFormFansOut` — open `/b/main/bans/new`, fill Steam ID `76561198000000001`, a reason, duration, tick the `other` backend, submit; the browser lands on the new ban's page (`/b/main/bans/<id>`) and both stores hold the ban (same assertions as `TestBanFormCreatesAndFansOut`). Then `TestE2ERevokeAsksForConfirmation` — on that page, dismissing the confirm leaves the ban active, accepting revokes it and `#flash` says so.
- [ ] **Step 2: Run** — `make e2e`. Expected: PASS, or a real bug fixed in a separate `web:` commit.
- [ ] **Step 3: Final check** — `make check` passes; `make docs` passes.
- [ ] **Step 4: Commit** — `git commit -m "test: e2e for the ban form and revoking"`

---

### Task 12: the browser against a real DayZ server (local only)

CI cannot start DayZ, so everything above runs against fakes. This task closes the gap where the fakes could lie: `scripts/test-client.sh` already runs a real server with dzo-admin, a real `dzo serve` and (unless `--no-client`) the headless DayZ client. It now also drives the web UI over that stack with the Task 8 harness.

**Files:**
- Create: `internal/web/live_test.go` (`//go:build e2e && live`)
- Modify: `scripts/test-client.sh`, `AGENTS.md`, `docs/development.rst`

**Interfaces:**
- Consumes: `useRealAssets(t *testing.T, dst string)`, `newBrowser(t *testing.T) *browser`, `(*browser).run` (Task 8); `web.New`, `web.Backend`, `apiclient.New(url, token string)`.
- Produces: tests `TestLive*`, configured by `DZO_LIVE_API` (base URL of the running `dzo serve` API), `DZO_LIVE_TOKEN_FILE` (an admin API token), `DZO_LIVE_INSTANCE` (`t1`), `DZO_LIVE_PLAYER` (the client's player name, `dzotester`; empty with `--no-client`). Without `DZO_LIVE_API` the tests fail (the `live` tag is an explicit opt-in, as with `e2e`).

- [ ] **Step 1: Write the tests** — each builds `web.New([]*web.Backend{{Name: "live", Client: apiclient.New(api, token)}}, web.Options{Assets: <TempDir with useRealAssets>})` behind an `httptest.Server`, then:
  - `TestLiveVehiclesAndMap` — the instance page lists the four vehicles `server_tests` spawned near x 7400 (types `Offroad_02|Hatchback_02|CivilianSedan|OffroadHatchback`); the map page shows the `Police cars` layer markers the watch rule produces; repairing the first of those vehicles from its panel shows a success flash with the real mod's result text. No console/CSP errors (the harness collector).
  - `TestLivePlayer` (skipped with a clear message when `DZO_LIVE_PLAYER` is empty, i.e. `--no-client`) — the player row for `DZO_LIVE_PLAYER` appears via htmx polling; the info popup shows a country or "unknown" without an error; a direct message from the player panel shows the delivered flash; the player's map marker exists.
- [ ] **Step 2: Hook it into `test-client.sh`** — a function `web_tests` at the end of `server_tests` (so it runs with and without the client):
  `DZO_LIVE_API="http://127.0.0.1:$api" DZO_LIVE_TOKEN_FILE="$work/api.token" DZO_LIVE_INSTANCE=t1 DZO_LIVE_PLAYER="$player" go test -tags 'e2e live' -count=1 -run '^TestLive' ./internal/web/`, recorded with `verdict "web UI against the real server" $?`, with `player=dzotester` in client mode and empty with `--no-client`.
- [ ] **Step 3: Run it** — `make test-client ARGS=--no-client` (no game needed), then `make test-client` and `make test-client ARGS=--exp` on a machine with Steam, the server and the client. Expected: the new verdict passes in all three, along with the existing 15 checks.
- [ ] **Step 4: Feed findings back into the fakes** — anything the live run catches that the CI tests did not (a field the real mod sends differently, an error text, timing) gets a fake-backed regression test in `web_test.go` / `e2e_*_test.go` or the `admin` tests, in the same commit as the fix. The live tier finds the bug; CI keeps it fixed.
- [ ] **Step 5: AGENTS.md** — under "Before pushing", after the conditional list: "Changes to `servermods/`, `internal/admin` (the mod protocol), or the player/vehicle actions in `internal/api` and `internal/web`: also run `make test-client` (stable, and `ARGS=--exp` for the next DayZ build) and say in the commit message that it passed. CI cannot run DayZ, so this is the only check against the real game." `docs/development.rst`: the live tier and its environment variables.
- [ ] **Step 6: Verify and commit** — `make check` still passes without any DayZ (the `live` files need both tags, so neither `make test` nor `make e2e` compiles them; check with `go list -tags e2e -f '{{.TestGoFiles}}' ./internal/web` showing no `live_test.go`). `git commit -m "test: the web UI against a real DayZ server in test-client.sh"`

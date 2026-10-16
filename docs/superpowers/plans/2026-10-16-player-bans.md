<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Player Bans and Ban Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ban players by Steam ID/GUID (and optionally IP) for a time or forever, per instance or for every instance of one installation, with editing, revoking, history and per-player evidence. Enforcement goes through BattlEye's `bans.txt` and a live RCon enforcer in `dzo serve`.

**Architecture:** A new package `internal/moderation` holds the SQLite store (bans, targets, history, evidence, sightings), the `bans.txt` renderer and parser, and a `Service` that `dzo serve` runs. Per instance, the `Service` keeps a BattlEye session that renders and reloads `bans.txt`, kicks banned players on connect and records GUID/IP sightings. The API (`internal/api`) exposes bans and evidence with role checks. The CLI and the web UI use them through `internal/apiclient`. Before the store exists, a verification phase captures BattlEye's real behaviour from a local server, and the captured files become golden test data.

**Tech Stack:** Go 1.27, `database/sql` with `modernc.org/sqlite` (pure Go, no cgo), the existing `internal/battleye` client, `html/template` + htmx for the web UI, bash for the live test (`scripts/test-client.sh`).

**Spec:** `docs/superpowers/specs/2026-10-16-player-bans-design.md`

## Deviations from the spec (decided while planning, also written into the spec in Task 0)

1. **No goose.** The store applies numbered, embedded SQL files (`internal/moderation/migrations/sqlite/NNNN_name.sql`) through a small runner with a `schema_version` table. That is about 40 lines against goose's dependency tree (AGENTS.md: keep dependencies few).
2. **`players` and `identities` become one `sightings` table keyed by the BattlEye GUID** (`guid, ip, name, instance, first_seen, last_seen`). A Steam ID maps to its GUID through the derivation that V2 verifies, so no Steam ID has to be guessed from names or slots.
3. **"Changed outside dzo" is detected by a hash sidecar** (`profiles/battleye/.dzo-bans.sha256`), not by a header comment, so it does not depend on whether BattlEye keeps comments. When `bans.txt` differs from what dzo last wrote (hand edits, or another RCon tool's `addBan`), serve imports the new active entries and then renders again. That replaces "import once".
4. **Editing is `PUT /api/v1/bans/{id}` with the full ban**, because the client always sends every editable field.

## Global Constraints

- `make check` passes before every push (fmt-check, vet, golangci-lint v2.14.0, reuse, vulncheck, test-race, coverage ≥ 85%).
- Every new file has an SPDX header (`// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>` + `// SPDX-License-Identifier: AGPL-3.0-or-later`; `{{/* … */}}` in templates; `#` in shell and SQL comments use `--`).
- Wrap every error (`fmt.Errorf("moderation: …: %w", err)`). Ignoring one needs `_ =` and a comment.
- Functions doing I/O take `context.Context` first. Goroutines stop on cancel and are waited for on shutdown.
- Files are `0o600`, directories `0o750`. Secrets are written atomically.
- SQL only with `?` parameters, never by concatenating values. Only clause *shapes* may be concatenated, from constants.
- Tests write only under `t.TempDir()` and never sleep to wait: use the `eventually` helper (Task 10) or channels.
- `//nolint:<linter> // <reason>` only, never bare.
- One logical change per commit, with a conventional prefix (`moderation:`, `api:`, `web:`, `docs:`, `test:`). Every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Never write the RCon password, API tokens or evidence file paths into an error that reaches the API or the web UI.

## Review Focus

1. **Several bans on one target.** One GUID can have a permanent instance ban and a timed global ban. The rendered `bans.txt` must have one line for it with the *longest* expiry, and revoking one ban must leave the other in force. Pinned in Task 6 (`TestEntriesMergeKeepsLongest`) and Task 11 (`TestRevokeOneOfTwoKeepsTheOther`).
2. **Player names and messages with control characters, newlines, `%`, quotes or 1 000 characters.** A message with a newline would split a `bans.txt` line or an RCon `kick` command. Pinned in Task 6 (`TestBEReasonSanitises`) and Task 12 (`TestKickMessageIsSanitised`).
3. **A restricted token (instances: [alpha]) must not see or touch bans that apply only to beta, and must not create global bans.** Pinned in Task 8 (`TestRestrictedTokenSeesOnlyItsBans`).
4. **The server is down or RCon fails when a ban is made.** The ban must be stored, the answer must say `pending` for that instance, and the next sweep must sync it. Pinned in Task 11 (`TestPendingIsRetriedOnTick`).
5. **An evidence upload that claims to be an image but is HTML or SVG, or is larger than the cap.** It must be refused, and a stored file must never be served as `text/html`. Pinned in Task 5 (`TestAddEvidenceRefusesNonImages`) and Task 9 (`TestEvidenceFileHeaders`).

---

## File Structure

```
internal/moderation/                 new package
  doc.go                             package comment
  ids.go           ids_test.go       Steam ID / GUID / IP validation, DeriveGUID
  duration.go      duration_test.go  "30m|12h|7d|2w|permanent" -> *time.Time (CLI and web share it)
  store.go         store_test.go     Open/OpenReadOnly/migrations, bans CRUD, history, list, FindActive
  evidence.go      evidence_test.go  evidence rows + image files
  sightings.go     sightings_test.go GUID/IP sightings, Identities
  bansfile.go      bansfile_test.go  bans.txt Entries/Format/Parse, sidecar write, ChangedOutside, ImportEntries, RenderAtStart
  service.go       service_test.go   per-instance RCon, sync, sweep, enforcer
  testutil_test.go                   eventually(), newStore()
  migrations/sqlite/0001_init.sql
  testdata/v1/  (captured: bans.txt, *.out, NOTES.md)
  testdata/v2/  (captured: pair.txt, players.out)
internal/battleye/livebans_test.go   V1 live capture test (gated)
internal/battleye/players.go         ParsePlayers (+ players_test.go)
internal/api/tokens.go               detector role, ban permissions
internal/api/bans.go   bans_test.go  ban routes
internal/api/evidence.go evidence_test.go  evidence + identities routes
internal/api/server.go               Moderation field, routes, now
internal/apiclient/bans.go bans_test.go
internal/apiclient/client.go         newReq refactor (multipart)
cmd/dzo/ban.go   ban_test.go         dzo ban …, dzo evidence …
cmd/dzo/root.go                      register commands
cmd/dzo/instance.go                  render bans.txt at start
cmd/dzo/serve.go                     token --role help text
internal/serve/serve.go              BanDB, BanInstances, Service wiring
internal/web/bans.go web bans tests in internal/web/bans_test.go
internal/web/templates/bans.html     ban list/detail/form/result
internal/web/templates/fragments.html  "Ban…" link in the player panel
internal/web/templates/layout.html   nav link
internal/web/server.go               routes
scripts/test-client.sh               --bans stage (V1–V4)
docs/faq.rst, docs/moderation.rst (new), docs/index.rst, docs/api.rst, docs/cli.rst, docs/development.rst
debian/changelog                     the new dependency
```

---

### Task 0: Record the deviations in the spec

**Files:**
- Modify: `docs/superpowers/specs/2026-10-16-player-bans-design.md`

- [ ] **Step 1: Edit the spec**

In the Decisions table change B2's last sentence from "Migrations with goose." to "Migrations are embedded numbered SQL files applied by a small runner (no goose)." Replace the `players` and `identities` lines of the data model block with:

```
sightings      guid, ip, name, instance, first_seen, last_seen   (PK guid, ip, instance)
```

and add a sentence below the block: "A Steam ID maps to its GUID by the derivation that V2 verifies, so sightings need no Steam ID column." In "Rendering `bans.txt`", replace the paragraph starting "Existing hand-written bans are imported once" with:

"dzo records the SHA-256 of what it wrote in `profiles/battleye/.dzo-bans.sha256`. When `bans.txt` differs from that (hand edits, or another RCon tool's `addBan`), serve imports its active entries that are not banned yet as instance bans `created_by = import:bans.txt` and renders again. The start path never overwrites a file changed outside dzo, and leaves it to serve. Removing a ban has to go through dzo."

In the API table, change `PATCH /bans/{id}` to `PUT /bans/{id}` (the full ban).

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-10-16-player-bans-design.md
git commit -m "docs: ban design follows the plan (sightings, hash sidecar, PUT, no goose)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 1: V1, capture BattlEye's ban file behaviour

No product code. A gated live test and a `--bans` mode for `scripts/test-client.sh` that runs the server **with BattlEye**, then the captured files are committed with notes.

**Files:**
- Create: `internal/battleye/livebans_test.go`
- Modify: `scripts/test-client.sh`
- Create (captured): `internal/moderation/testdata/v1/{bans-after-add.out,bans-file-after-add.txt,bans.txt,bans-after-load.out,bans-after-expiry.out,bans.txt.after-expiry,remove.out,NOTES.md}`

**Interfaces:**
- Produces: `testdata/v1/NOTES.md` answering Q1–Q8 below. Tasks 2, 6 and 12 read it.

- [ ] **Step 1: Write the gated capture test**

`internal/battleye/livebans_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestLiveBans captures how a real DayZ server's BattlEye stores bans (spike V1 of the ban design). It
// only runs with DZO_LIVE_BE_ADDR, DZO_LIVE_BE_PASSWORD, DZO_LIVE_BE_DIR (the server's BattlEye
// directory) and DZO_LIVE_CAPTURE (where the captures go) set, against a server nobody plays on: it
// adds bans and removes every ban at the end.
func TestLiveBans(t *testing.T) {
	addr, pw := os.Getenv("DZO_LIVE_BE_ADDR"), os.Getenv("DZO_LIVE_BE_PASSWORD")
	dir, out := os.Getenv("DZO_LIVE_BE_DIR"), os.Getenv("DZO_LIVE_CAPTURE")
	if addr == "" || pw == "" || dir == "" || out == "" {
		t.Skip("set DZO_LIVE_BE_ADDR, DZO_LIVE_BE_PASSWORD, DZO_LIVE_BE_DIR and DZO_LIVE_CAPTURE")
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	c, err := Dial(addr, pw)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	run := func(cmd string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		res, err := c.Command(ctx, cmd)
		t.Logf("%q -> err=%v\n%s", cmd, err, res)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return res
	}
	save := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(out, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	readFile := func() string {
		b, err := os.ReadFile(filepath.Join(dir, "bans.txt")) //nolint:gosec // the test server's own file
		if err != nil {
			return "<" + err.Error() + ">"
		}
		return string(b)
	}

	const g1, g2, g3, g4 = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210",
		"11111111111111111111111111111111", "22222222222222222222222222222222"
	long := strings.Repeat("x", 300)
	// Q1-Q5: what the server writes for each kind of ban and reason.
	for _, cmd := range []string{
		"addBan " + g1 + " 0 permanent reason with spaces",
		"addBan " + g2 + " 5 timed äöü \"quoted\" 100%",
		"addBan 192.0.2.7 0 ip ban",
		"addBan 2001:db8::7 0 ipv6 ban",
		"addBan " + g3 + " 0 " + long,
	} {
		run(cmd)
	}
	save("bans-after-add.out", run("bans"))
	save("bans-file-after-add.txt", readFile()) // Q8: written without writeBans?
	run("writeBans")
	save("bans.txt", readFile())

	// Q6: a file in our format with a comment line, loaded with loadBans.
	expiry := time.Now().Add(90 * time.Second).Unix()
	own := fmt.Sprintf("// written by the dzo V1 test\n%s -1 own permanent\n%s %d own timed\n198.51.100.9 -1 own ip\n", g1, g4, expiry)
	if err := os.WriteFile(filepath.Join(dir, "bans.txt"), []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	run("loadBans")
	save("bans-after-load.out", run("bans"))

	// Q7: is the timed ban listed after its expiry, and does writeBans drop it?
	time.Sleep(time.Until(time.Unix(expiry+5, 0))) // a live capture: the server's clock decides, nothing to poll
	save("bans-after-expiry.out", run("bans"))
	run("writeBans")
	save("bans.txt.after-expiry", readFile())

	// Clean up: removeBan 0 until the list is empty. The numbers shift on every removal.
	num := regexp.MustCompile(`(?m)^\s*(\d+)\s`)
	var removed strings.Builder
	for i := 0; i < 50 && num.MatchString(run("bans")); i++ {
		removed.WriteString(run("removeBan 0") + "\n")
	}
	save("remove.out", removed.String())
	run("writeBans")
}
```

- [ ] **Step 2: Check that it skips and lints without the environment**

Run: `go test ./internal/battleye -run TestLiveBans -v`
Expected: `--- SKIP: TestLiveBans`. Then `make lint` reports nothing new.

- [ ] **Step 3: Add the `--bans` mode to `scripts/test-client.sh`**

1. Usage and the argument loop: add `[--bans]` to the usage line and comment, with `--bans) bans=1 ;;` in the `case`, and initialise `bans=0` beside `keep=0`.
2. Directly after the two lines that switch `BattlEye = 0;` on, add:

```bash
rcon=$((query + 1))
if [ "$bans" = 1 ]; then
	# The ban checks need BattlEye: RCon on localhost, an absolute -BEpath, and a client that runs
	# BattlEye (PROTON_BATTLEYE_RUNTIME, see docs/development.rst).
	sed -i 's/^BattlEye = .*/BattlEye = 1;/' "$cfg"
	rconpw=$(head -c12 /dev/urandom | od -An -tx1 | tr -d ' \n')
	mkdir -p "$tree/profiles/battleye" "$work/secrets/rcon" "$work/data/instances/t1"
	printf 'RConPassword %s\nRConPort %d\nRConIP 127.0.0.1\n' "$rconpw" "$rcon" >"$tree/profiles/battleye/beserver_x64.cfg"
	echo "$rconpw" >"$work/secrets/rcon/t1"
	chmod 600 "$tree/profiles/battleye/beserver_x64.cfg" "$work/secrets/rcon/t1"
	# dzo serve finds the instance's profiles (and so its bans.txt) under paths.instances.
	ln -sfn "$tree/profiles" "$work/data/instances/t1/profiles"
	export PROTON_BATTLEYE_RUNTIME=${PROTON_BATTLEYE_RUNTIME:-$HOME/.local/share/Steam/steamapps/common/Proton BattlEye Runtime}
fi
```

   In the `instance.yaml` heredoc replace `rcon: $((query + 1))` with `rcon: $rcon`.
3. Move `"$work/dzo" serve … &` and its `pids+=($!)` into a function `start_serve() { …; }`, defined right before the server start. Call `[ "$bans" = 1 ] || start_serve` where serve used to start, so that in ban mode V1 runs before serve owns `bans.txt`.
4. Start the server with an extra argument when `bans=1`: build `beargs=()` and use `[ "$bans" = 1 ] && beargs=("-BEpath=$tree/profiles/battleye")`, then pass `"${beargs[@]}"` after `-adminlog`.
5. Right after the `scripts compile` verdict, add:

```bash
if [ "$bans" = 1 ]; then
	waitfor 60 "'$work/dzo' rcon exec --addr 127.0.0.1:$rcon --password '$rconpw' version >/dev/null 2>&1" || { bad "rcon: BattlEye answers"; exit 1; }
	ok "rcon: BattlEye answers"
	s=0
	DZO_LIVE_BE_ADDR=127.0.0.1:$rcon DZO_LIVE_BE_PASSWORD=$rconpw DZO_LIVE_BE_DIR=$tree/profiles/battleye \
		DZO_LIVE_CAPTURE=$work/capture/v1 go test -count=1 -run TestLiveBans -v ./internal/battleye >"$work/v1.log" 2>&1 || s=1
	[ "$s" = 0 ] || tail -40 "$work/v1.log" >&2
	verdict "V1: BattlEye ban file capture" $s
	echo "== V1 captures in $work/capture/v1 (use --keep)"
	start_serve
	if [ "$noclient" = 1 ]; then
		echo "== $pass passed, $fail failed"
		[ "$fail" = 0 ]
		exit
	fi
fi
```

- [ ] **Step 4: Run V1 against the stable server**

Run: `scripts/test-client.sh --bans --no-client --keep`
Expected: `PASS rcon: BattlEye answers`, `PASS V1: BattlEye ban file capture`, then the kept work dir. If the server refuses RCon, check `"$work/server.log"` for the BattlEye line and that `-BEpath` is absolute.

- [ ] **Step 5: Copy the captures and write the notes**

```bash
mkdir -p internal/moderation/testdata/v1
cp /tmp/dzcl.*/capture/v1/* internal/moderation/testdata/v1/   # the kept work dir printed above
```

Write `internal/moderation/testdata/v1/NOTES.md` (with the SPDX HTML comment header) answering each question with a quote from the captures:

- Q1 The line format of `bans.txt` (fields, separator, field order).
- Q2 How a permanent ban's expiry is written.
- Q3 How a timed ban's expiry is written (absolute Unix time or minutes).
- Q4 Whether spaces, UTF-8, quotes and `%` in the reason survive, and the longest reason kept (count the `x` of the 300-character reason).
- Q5 Whether `addBan` takes an IPv6 address.
- Q6 Whether a `//` comment line in a loaded file is ignored (`bans-after-load.out` lists exactly 3 bans), and whether `writeBans` keeps it.
- Q7 Whether `bans` lists an expired ban before `writeBans`, and whether `writeBans` drops it.
- Q8 Whether `addBan` writes `bans.txt` by itself (`bans-file-after-add.txt`).

The expected answers, from Arma's BattlEye, are: Q1 `<guid|ip> <expiry> <reason>` separated by single spaces; Q2 `-1`; Q3 an absolute Unix time; Q6 comments ignored; Q7 expired entries are dropped. **Where a capture differs, the capture wins.** Tasks 2, 6 and 12 say exactly what to change in that case.

Delete any line that names the RCon password from the captures before committing (there should be none).

- [ ] **Step 6: Record the findings in the FAQ**

In `docs/faq.rst`, in the "Why is a player's GUID shown as unverified?" answer, replace "and the format of the native ``ban.txt``" with "and the native DayZ ``ban.txt`` (see :doc:`moderation`)". Add a new question after it:

```rst
What does BattlEye's bans.txt look like?
   Captured from a real server (``internal/moderation/testdata/v1``): one ban per line,
   ``<GUID or IP> <expiry> <reason>``, where the expiry is <Q2/Q3 answers>. <Q6 answer>. <Q7 answer>.
   dzo writes this file itself; see :doc:`moderation`.
```

Fill the `<…>` parts from NOTES.md in plain sentences. `docs/moderation.rst` is created in Task 17. Until then `make docs` will warn about the reference, so build the docs only after Task 17, or write the reference as plain text now and turn it into `:doc:` in Task 17.

- [ ] **Step 7: Commit**

```bash
git add internal/battleye/livebans_test.go scripts/test-client.sh internal/moderation/testdata/v1 docs/faq.rst
git commit -m "test: capture BattlEye's ban file behaviour (ban spike V1)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Identifiers and the GUID derivation (V2)

**Files:**
- Create: `internal/moderation/doc.go`, `internal/moderation/ids.go`, `internal/moderation/ids_test.go`
- Modify: `scripts/test-client.sh` (V2 capture in `--bans` mode with the client)
- Create (captured): `internal/moderation/testdata/v2/pair.txt`, `internal/moderation/testdata/v2/players.out`

**Interfaces:**
- Produces:
  - `func ValidSteamID(s string) bool`
  - `func ValidGUID(s string) bool`
  - `func ParseIP(s string) (string, error)` (canonical IPv4 text)
  - `func DeriveGUID(steamID string) (string, error)`

- [ ] **Step 1: Capture a real Steam ID / GUID pair**

In `scripts/test-client.sh`, right after `ok "players: state lists the player"` and the `id=…` line, add:

```bash
if [ "$bans" = 1 ]; then
	mkdir -p "$work/capture/v2"
	"$work/dzo" rcon exec --addr "127.0.0.1:$rcon" --password "$rconpw" players >"$work/capture/v2/players.out"
	guid=$(grep dzotester "$work/capture/v2/players.out" | grep -oE '[0-9a-f]{32}' | head -1)
	echo "$id $guid" >"$work/capture/v2/pair.txt"
	want=$(python3 -I -c 'import hashlib,struct,sys;print(hashlib.md5(b"BE"+struct.pack("<Q",int(sys.argv[1]))).hexdigest())' "$id")
	s=1
	[ -n "$guid" ] && [ "$guid" = "$want" ] && s=0
	verdict "V2: GUID = md5(BE + little-endian Steam ID) ($id -> $guid)" $s
fi
```

Run: `scripts/test-client.sh --bans --keep` (needs the BattlEye client, see `docs/development.rst`).
Expected: `PASS V2: GUID = md5(…)`. **If it FAILS, stop the plan here and report to the user.** The design depends on the derivation (spec V2).

Copy `capture/v2/pair.txt` and `capture/v2/players.out` to `internal/moderation/testdata/v2/`. Steam IDs of test accounts are fine to commit. If the account is personal, ask the user first.

- [ ] **Step 2: Write the failing tests**

`internal/moderation/ids_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"strings"
	"testing"
)

func TestDeriveGUIDMatchesTheLiveCapture(t *testing.T) {
	b, err := os.ReadFile("testdata/v2/pair.txt")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		t.Fatalf("pair.txt: want \"<steamid> <guid>\", got %q", b)
	}
	got, err := DeriveGUID(f[0])
	if err != nil || got != f[1] {
		t.Fatalf("DeriveGUID(%s) = %q, %v; the server said %s", f[0], got, err, f[1])
	}
}

func TestDeriveGUIDRefusesBadIDs(t *testing.T) {
	for _, id := range []string{"", "123", "76561198000000001x", "86561198000000001", "765611980000000011"} {
		if _, err := DeriveGUID(id); err == nil {
			t.Errorf("DeriveGUID(%q) accepted", id)
		}
	}
}

func TestValidators(t *testing.T) {
	if !ValidSteamID("76561198000000001") || ValidSteamID("7656119800000000") {
		t.Error("ValidSteamID")
	}
	if !ValidGUID("907725ff524e1026154b5ac3cccb0258") || ValidGUID("907725FF524E1026154B5AC3CCCB0258") || ValidGUID("907725ff") {
		t.Error("ValidGUID: lowercase hex, 32 digits")
	}
	for in, want := range map[string]string{"192.0.2.7": "192.0.2.7", " 10.0.0.1 ": "10.0.0.1", "::ffff:192.0.2.7": "192.0.2.7"} {
		if got, err := ParseIP(in); err != nil || got != want {
			t.Errorf("ParseIP(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0.0.0.0", "2001:db8::7", "192.0.2.7:2304", "10.0.0.0/8", "example.org"} {
		if _, err := ParseIP(in); err == nil {
			t.Errorf("ParseIP(%q) accepted", in)
		}
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/moderation/`
Expected: FAIL, `undefined: DeriveGUID`.

- [ ] **Step 4: Implement**

`internal/moderation/doc.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package moderation keeps bans, ban evidence and player sightings in a SQLite database, renders
// BattlEye's bans.txt from them, and enforces bans on running servers through RCon
// (docs/superpowers/specs/2026-10-16-player-bans-design.md). dzo serve is the only writer; the start
// path of an instance opens the database read-only to render bans.txt.
package moderation
```

`internal/moderation/ids.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"crypto/md5" //nolint:gosec // BattlEye defines the GUID as an MD5; nothing here relies on it for security
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// ValidSteamID reports whether s is a Steam64 ID of an individual account (17 digits, 7656119…).
func ValidSteamID(s string) bool {
	if len(s) != 17 || !strings.HasPrefix(s, "7656119") {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// ValidGUID reports whether s is a BattlEye GUID as the server prints it: 32 lowercase hex digits.
func ValidGUID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ParseIP returns the canonical text of an IPv4 address. BattlEye bans single addresses; ranges,
// ports, IPv6 and the unspecified address are refused.
func ParseIP(s string) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", invalid("invalid IP address %q", s)
	}
	a = a.Unmap()
	if !a.Is4() || a.IsUnspecified() {
		return "", invalid("IP bans take a single IPv4 address, not %q", s)
	}
	return a.String(), nil
}

// DeriveGUID returns the BattlEye GUID of a Steam ID: md5("BE" + the ID as little-endian uint64),
// verified against a real server (testdata/v2).
func DeriveGUID(steamID string) (string, error) {
	if !ValidSteamID(steamID) {
		return "", invalid("invalid Steam ID %q", steamID)
	}
	id, err := strconv.ParseUint(steamID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("moderation: steam id: %w", err)
	}
	var b [10]byte
	b[0], b[1] = 'B', 'E'
	binary.LittleEndian.PutUint64(b[2:], id)
	sum := md5.Sum(b[:]) //nolint:gosec // see the import
	return hex.EncodeToString(sum[:]), nil
}
```

Add the `invalid` helper in the same file (Task 4 uses it too):

```go
// InvalidError is a request the store refuses: the API answers 400 with its text.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }
```

If V1's NOTES.md Q5 says BattlEye *takes* IPv6, keep the refusal anyway (IPv4 only is the decision) but change the message to "dzo bans IPv4 addresses only".

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/moderation/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/moderation scripts/test-client.sh
git commit -m "moderation: Steam ID, GUID and IP checks, GUID derivation verified live (V2)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Ban durations

**Files:**
- Create: `internal/moderation/duration.go`, `internal/moderation/duration_test.go`

**Interfaces:**
- Produces: `func ExpiryFrom(s string, now time.Time) (*time.Time, error)`. `""` is an error, `"permanent"` gives nil, and `Nm`, `Nh`, `Nd`, `Nw` give `now + N units`.
- Produces: `func FormatExpiry(t *time.Time) string` ("permanent" or RFC 3339 UTC).

- [ ] **Step 1: Write the failing test**

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"
)

func TestExpiryFrom(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{"30m": 30 * time.Minute, "12h": 12 * time.Hour, "7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour} {
		got, err := ExpiryFrom(in, now)
		if err != nil || got == nil || !got.Equal(now.Add(want)) {
			t.Errorf("ExpiryFrom(%q) = %v, %v", in, got, err)
		}
	}
	if got, err := ExpiryFrom("permanent", now); err != nil || got != nil {
		t.Errorf("permanent = %v, %v", got, err)
	}
	for _, bad := range []string{"", "0d", "-1d", "7", "7y", "d", "99999999w"} {
		if _, err := ExpiryFrom(bad, now); err == nil {
			t.Errorf("ExpiryFrom(%q) accepted", bad)
		}
	}
	if FormatExpiry(nil) != "permanent" || FormatExpiry(&now) != "2026-10-16T12:00:00Z" {
		t.Error("FormatExpiry")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/moderation/ -run TestExpiryFrom`
Expected: FAIL, `undefined: ExpiryFrom`.

- [ ] **Step 3: Implement**

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"strconv"
	"time"
)

// maxBanDays bounds a timed ban; longer means permanent.
const maxBanDays = 10 * 365

// ExpiryFrom turns a ban duration ("30m", "12h", "7d", "2w" or "permanent") into the expiry, nil
// meaning permanent.
func ExpiryFrom(s string, now time.Time) (*time.Time, error) {
	if s == "permanent" {
		return nil, nil
	}
	if len(s) < 2 {
		return nil, invalid("invalid ban duration %q (e.g. 30m, 12h, 7d, 2w or permanent)", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 1 {
		return nil, invalid("invalid ban duration %q (e.g. 30m, 12h, 7d, 2w or permanent)", s)
	}
	var unit time.Duration
	switch s[len(s)-1] {
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return nil, invalid("invalid ban duration %q (e.g. 30m, 12h, 7d, 2w or permanent)", s)
	}
	if time.Duration(n) > maxBanDays*24*time.Hour/unit {
		return nil, invalid("ban duration %q is longer than %d days; use permanent", s, maxBanDays)
	}
	t := now.Add(time.Duration(n) * unit)
	return &t, nil
}

// FormatExpiry prints an expiry for people and the CLI.
func FormatExpiry(t *time.Time) string {
	if t == nil {
		return "permanent"
	}
	return t.UTC().Format(time.RFC3339)
}
```

- [ ] **Step 4: Run it**

Run: `go test ./internal/moderation/ -run TestExpiryFrom`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/moderation/duration.go internal/moderation/duration_test.go
git commit -m "moderation: ban durations

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The store: database, migrations and bans

**Files:**
- Modify: `go.mod`, `go.sum` (`modernc.org/sqlite`)
- Create: `internal/moderation/migrations/sqlite/0001_init.sql`, `internal/moderation/store.go`, `internal/moderation/store_test.go`, `internal/moderation/testutil_test.go`

**Interfaces:**
- Consumes: `ValidSteamID`, `ValidGUID`, `ParseIP`, `DeriveGUID`, `invalid`, `InvalidError` (Task 2).
- Produces (used by every later task):

```go
type Scope string            // ScopeGlobal "global", ScopeInstances "instances"
type TargetKind string       // TargetSteam "steam", TargetGUID "guid", TargetIP "ip"
type Target struct { Kind TargetKind; Value string; Derived bool }
type Ban struct {
	ID int64; Reason, Message, CreatedBy string; CreatedAt time.Time; ExpiresAt *time.Time
	Scope Scope; Instances []string; Targets []Target; Evidence []int64
	RevokedBy string; RevokedAt *time.Time; RevokeReason string
}
func (b Ban) Active(now time.Time) bool
func (b Ban) AppliesTo(instance string) bool
func (b Ban) State(now time.Time) string   // "active" | "expired" | "revoked"
func (b Ban) Input() BanInput               // the editable fields, for PUT and rights checks
type BanInput struct {
	SteamID string; GUIDs, IPs []string; Reason, Message string; ExpiresAt *time.Time
	Scope Scope; Instances []string; Evidence []int64
}
type BanFilter struct { State, Instance, Query string; Limit int; Now time.Time }
type HistoryEntry struct { At time.Time; Actor, Action string; Change json.RawMessage }
var ErrNotFound error
const MaxReason = 1000; const MaxMessage = 200; const DefaultMessage = "You are banned from this server"
func Open(ctx context.Context, path string) (*Store, error)
func OpenReadOnly(ctx context.Context, path string) (*Store, error)
func (s *Store) Close() error
func (s *Store) CreateBan(ctx context.Context, actor string, in BanInput, now time.Time) (Ban, error)
func (s *Store) UpdateBan(ctx context.Context, id int64, actor string, in BanInput, now time.Time) (Ban, error)
func (s *Store) RevokeBan(ctx context.Context, id int64, actor, reason string, now time.Time) (Ban, error)
func (s *Store) GetBan(ctx context.Context, id int64) (Ban, error)
func (s *Store) ListBans(ctx context.Context, f BanFilter) ([]Ban, error)
func (s *Store) ActiveBans(ctx context.Context, instance string, now time.Time) ([]Ban, error)
func (s *Store) FindActive(ctx context.Context, instance, guid, ip string, now time.Time) (*Ban, error)
func (s *Store) History(ctx context.Context, id int64) ([]HistoryEntry, error)
```

- [ ] **Step 1: Add the dependency**

Run: `go get modernc.org/sqlite@v1.60.1 && go mod tidy && make licenses`
Expected: `go.mod` lists `modernc.org/sqlite v1.60.1`, and `make licenses` shows only BSD-3-Clause/MIT-style licences for the new modules. Note the result for the commit message.

- [ ] **Step 2: Write the schema**

`internal/moderation/migrations/sqlite/0001_init.sql`:

```sql
-- SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
-- SPDX-License-Identifier: AGPL-3.0-or-later

-- Bans, their targets, scope and history; evidence; GUID/IP sightings. Times are Unix seconds, so the
-- same statements work on PostgreSQL later (with BIGSERIAL ids in its own migration set).
CREATE TABLE bans (
	id            INTEGER PRIMARY KEY,
	reason        TEXT    NOT NULL,
	message       TEXT    NOT NULL,
	created_by    TEXT    NOT NULL,
	created_at    INTEGER NOT NULL,
	expires_at    INTEGER,
	scope         TEXT    NOT NULL CHECK (scope IN ('global', 'instances')),
	revoked_by    TEXT,
	revoked_at    INTEGER,
	revoke_reason TEXT
);
CREATE TABLE ban_instances (
	ban_id   INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	instance TEXT    NOT NULL,
	PRIMARY KEY (ban_id, instance)
);
CREATE TABLE ban_targets (
	ban_id  INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	kind    TEXT    NOT NULL CHECK (kind IN ('steam', 'guid', 'ip')),
	value   TEXT    NOT NULL,
	derived INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (ban_id, kind, value)
);
CREATE INDEX ban_targets_value ON ban_targets (value);
CREATE TABLE ban_history (
	id     INTEGER PRIMARY KEY,
	ban_id INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	at     INTEGER NOT NULL,
	actor  TEXT    NOT NULL,
	action TEXT    NOT NULL,
	change TEXT    NOT NULL
);
CREATE INDEX ban_history_ban ON ban_history (ban_id);
CREATE TABLE evidence (
	id          INTEGER PRIMARY KEY,
	steam_id    TEXT,
	guid        TEXT,
	ip          TEXT,
	instance    TEXT    NOT NULL,
	source      TEXT    NOT NULL,
	kind        TEXT    NOT NULL CHECK (kind IN ('log', 'note', 'image')),
	body        TEXT    NOT NULL,
	file_sha256 TEXT,
	file_type   TEXT,
	created_at  INTEGER NOT NULL,
	CHECK (steam_id IS NOT NULL OR guid IS NOT NULL OR ip IS NOT NULL)
);
CREATE INDEX evidence_guid ON evidence (guid);
CREATE INDEX evidence_ip ON evidence (ip);
CREATE TABLE ban_evidence (
	ban_id      INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	evidence_id INTEGER NOT NULL REFERENCES evidence (id),
	PRIMARY KEY (ban_id, evidence_id)
);
CREATE TABLE sightings (
	guid       TEXT    NOT NULL,
	ip         TEXT    NOT NULL,
	name       TEXT    NOT NULL,
	instance   TEXT    NOT NULL,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	PRIMARY KEY (guid, ip, instance)
);
CREATE INDEX sightings_ip ON sightings (ip);
```

- [ ] **Step 3: Write the test helpers and the failing tests**

`internal/moderation/testutil_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const steamA, steamB = "76561198000000001", "76561198000000002"

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "dzo.sqlite")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func guidOf(t *testing.T, steam string) string {
	t.Helper()
	g, err := DeriveGUID(steam)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// eventually polls cond until it holds or 5 s pass. It waits on a condition, not for a fixed time.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func at(d time.Duration) *time.Time { x := t0.Add(d); return &x }
```

`internal/moderation/store_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func TestOpenCreatesAPrivateFileAndMigratesOnce(t *testing.T) {
	s, path := newStore(t)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", info.Mode().Perm(), err)
	}
	_ = s.Close()
	s2, err := Open(ctx, path) // the second open applies nothing again
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
}

func TestCreateBanAddsTheDerivedGUID(t *testing.T) {
	s, _ := newStore(t)
	b, err := s.CreateBan(ctx, "alice", BanInput{SteamID: steamA, Reason: "aimbot", Scope: ScopeGlobal, ExpiresAt: at(time.Hour)}, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetGUID, Value: guidOf(t, steamA), Derived: true}}
	if len(b.Targets) != 2 || b.Targets[0] != want[0] || b.Targets[1] != want[1] {
		t.Fatalf("targets %+v", b.Targets)
	}
	if b.Message != DefaultMessage || b.CreatedBy != "alice" || !b.CreatedAt.Equal(t0) || !b.Active(t0) || b.Active(t0.Add(2*time.Hour)) {
		t.Fatalf("ban %+v", b)
	}
	if b.Input().SteamID != steamA || len(b.Input().GUIDs) != 0 {
		t.Fatalf("Input %+v", b.Input())
	}
}

func TestCreateBanValidates(t *testing.T) {
	s, _ := newStore(t)
	for name, in := range map[string]BanInput{
		"no target":          {Reason: "x", Scope: ScopeGlobal},
		"no reason":          {SteamID: steamA, Scope: ScopeGlobal},
		"bad steam":          {SteamID: "123", Reason: "x", Scope: ScopeGlobal},
		"bad guid":           {GUIDs: []string{"XYZ"}, Reason: "x", Scope: ScopeGlobal},
		"bad ip":             {IPs: []string{"10.0.0.0/8"}, Reason: "x", Scope: ScopeGlobal},
		"past expiry":        {SteamID: steamA, Reason: "x", Scope: ScopeGlobal, ExpiresAt: at(-time.Minute)},
		"no scope":           {SteamID: steamA, Reason: "x"},
		"global + instances": {SteamID: steamA, Reason: "x", Scope: ScopeGlobal, Instances: []string{"a"}},
		"instances, none":    {SteamID: steamA, Reason: "x", Scope: ScopeInstances},
		"long reason":        {SteamID: steamA, Reason: strings.Repeat("r", MaxReason+1), Scope: ScopeGlobal},
		"long message":       {SteamID: steamA, Reason: "x", Message: strings.Repeat("m", MaxMessage+1), Scope: ScopeGlobal},
		"unknown evidence":   {SteamID: steamA, Reason: "x", Scope: ScopeGlobal, Evidence: []int64{42}},
	} {
		_, err := s.CreateBan(ctx, "a", in, t0)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
}

func TestUpdateRevokeAndHistory(t *testing.T) {
	s, _ := newStore(t)
	b, _ := s.CreateBan(ctx, "alice", BanInput{SteamID: steamA, Reason: "aimbot", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	in := b.Input()
	in.Reason, in.IPs, in.Instances = "aimbot, confirmed", []string{"192.0.2.7"}, []string{"alpha", "beta"}
	b2, err := s.UpdateBan(ctx, b.ID, "bob", in, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if b2.Reason != "aimbot, confirmed" || len(b2.Instances) != 2 || len(b2.Targets) != 3 {
		t.Fatalf("updated %+v", b2)
	}
	if _, err := s.RevokeBan(ctx, b.ID, "carol", "", t0.Add(2*time.Minute)); err == nil {
		t.Fatal("revoke without a reason accepted")
	}
	b3, err := s.RevokeBan(ctx, b.ID, "carol", "appeal accepted", t0.Add(2*time.Minute))
	if err != nil || b3.State(t0.Add(3*time.Minute)) != "revoked" || b3.RevokedBy != "carol" {
		t.Fatalf("revoked %+v, %v", b3, err)
	}
	if _, err := s.UpdateBan(ctx, b.ID, "bob", in, t0.Add(3*time.Minute)); err == nil {
		t.Fatal("a revoked ban was edited")
	}
	h, err := s.History(ctx, b.ID)
	if err != nil || len(h) != 3 || h[0].Action != "create" || h[1].Action != "edit" || h[1].Actor != "bob" || h[2].Action != "revoke" {
		t.Fatalf("history %+v, %v", h, err)
	}
	if !strings.Contains(string(h[1].Change), `"aimbot, confirmed"`) || !strings.Contains(string(h[1].Change), `"old"`) {
		t.Fatalf("edit change %s", h[1].Change)
	}
	if _, err := s.GetBan(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBan(999) = %v", err)
	}
}

func TestListAndFindActive(t *testing.T) {
	s, _ := newStore(t)
	g := guidOf(t, steamA)
	perm, _ := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "cheat", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	timed, _ := s.CreateBan(ctx, "a", BanInput{IPs: []string{"192.0.2.7"}, Reason: "50% toxic_chat", Scope: ScopeGlobal, ExpiresAt: at(time.Hour)}, t0)
	gone, _ := s.CreateBan(ctx, "a", BanInput{SteamID: steamB, Reason: "x", Scope: ScopeGlobal}, t0)
	_, _ = s.RevokeBan(ctx, gone.ID, "a", "mistake", t0)
	later := t0.Add(2 * time.Hour)

	ids := func(f BanFilter) []int64 {
		t.Helper()
		bs, err := s.ListBans(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, b := range bs {
			out = append(out, b.ID)
		}
		return out
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", name, got, want)
			}
		}
	}
	eq("active now", ids(BanFilter{State: "active", Now: t0}), timed.ID, perm.ID)
	eq("active later", ids(BanFilter{State: "active", Now: later}), perm.ID)
	eq("expired later", ids(BanFilter{State: "expired", Now: later}), timed.ID)
	eq("revoked", ids(BanFilter{State: "revoked", Now: t0}), gone.ID)
	eq("beta sees global only", ids(BanFilter{State: "active", Instance: "beta", Now: t0}), timed.ID)
	eq("query by steam finds the guid target", ids(BanFilter{Query: steamA, Now: t0}), perm.ID)
	eq("query by guid", ids(BanFilter{Query: g, Now: t0}), perm.ID)
	eq("LIKE wildcards are literal", ids(BanFilter{Query: "50%", Now: t0}), timed.ID)
	eq("underscore is literal", ids(BanFilter{Query: "c_eat", Now: t0}))
	eq("limit", ids(BanFilter{Limit: 1, Now: t0}), gone.ID)

	if b, err := s.FindActive(ctx, "alpha", g, "", t0); err != nil || b == nil || b.ID != perm.ID {
		t.Fatalf("FindActive guid alpha = %+v, %v", b, err)
	}
	if b, _ := s.FindActive(ctx, "beta", g, "", t0); b != nil {
		t.Fatalf("an alpha ban applied on beta: %+v", b)
	}
	if b, _ := s.FindActive(ctx, "beta", "", "192.0.2.7", t0); b == nil || b.ID != timed.ID {
		t.Fatalf("FindActive ip = %+v", b)
	}
	if b, _ := s.FindActive(ctx, "beta", "", "192.0.2.7", later); b != nil {
		t.Fatal("an expired ban was found")
	}
	if b, _ := s.FindActive(ctx, "alpha", guidOf(t, steamB), "", t0); b != nil {
		t.Fatal("a revoked ban was found")
	}
}

func TestOpenReadOnly(t *testing.T) {
	s, path := newStore(t)
	_, _ = s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	ro, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if bs, err := ro.ActiveBans(ctx, "alpha", t0); err != nil || len(bs) != 1 {
		t.Fatalf("ActiveBans = %v, %v", bs, err)
	}
	if _, err := ro.CreateBan(ctx, "a", BanInput{SteamID: steamB, Reason: "x", Scope: ScopeGlobal}, t0); err == nil {
		t.Fatal("a read-only store wrote")
	}
	if _, err := OpenReadOnly(ctx, path+".missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database: %v", err)
	}
}
```

- [ ] **Step 4: Run them to see them fail**

Run: `go test ./internal/moderation/`
Expected: FAIL, `undefined: Open`.

- [ ] **Step 5: Implement `store.go`**

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite" // the database/sql driver "sqlite"
)

//go:embed migrations/sqlite/*.sql
var migrationsFS embed.FS

// Scope says where a ban applies.
type Scope string

const (
	ScopeGlobal    Scope = "global"    // every instance of this installation
	ScopeInstances Scope = "instances" // the instances listed in Ban.Instances
)

// TargetKind is what a ban target identifies.
type TargetKind string

const (
	TargetSteam TargetKind = "steam"
	TargetGUID  TargetKind = "guid"
	TargetIP    TargetKind = "ip"
)

// Target is one identifier a ban covers. Derived marks the GUID computed from the Steam ID.
type Target struct {
	Kind    TargetKind `json:"kind"`
	Value   string     `json:"value"`
	Derived bool       `json:"derived,omitempty"`
}

// Limits of the free-text fields.
const (
	MaxReason      = 1000
	MaxMessage     = 200
	DefaultMessage = "You are banned from this server"
	maxInstanceLen = 64
)

// Ban is one ban with its targets, scope and evidence links.
type Ban struct {
	ID           int64      `json:"id"`
	Reason       string     `json:"reason"`
	Message      string     `json:"message"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Scope        Scope      `json:"scope"`
	Instances    []string   `json:"instances,omitempty"`
	Targets      []Target   `json:"targets"`
	Evidence     []int64    `json:"evidence,omitempty"`
	RevokedBy    string     `json:"revoked_by,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokeReason string     `json:"revoke_reason,omitempty"`
}

// Active reports whether the ban is in force at now.
func (b Ban) Active(now time.Time) bool {
	return b.RevokedAt == nil && (b.ExpiresAt == nil || b.ExpiresAt.After(now))
}

// AppliesTo reports whether the ban's scope covers an instance.
func (b Ban) AppliesTo(instance string) bool {
	return b.Scope == ScopeGlobal || slices.Contains(b.Instances, instance)
}

// State is "active", "expired" or "revoked".
func (b Ban) State(now time.Time) string {
	switch {
	case b.RevokedAt != nil:
		return "revoked"
	case b.Active(now):
		return "active"
	}
	return "expired"
}

// Input returns the editable fields of the ban.
func (b Ban) Input() BanInput {
	in := BanInput{Reason: b.Reason, Message: b.Message, ExpiresAt: b.ExpiresAt, Scope: b.Scope,
		Instances: slices.Clone(b.Instances), Evidence: slices.Clone(b.Evidence)}
	for _, t := range b.Targets {
		switch {
		case t.Kind == TargetSteam:
			in.SteamID = t.Value
		case t.Kind == TargetGUID && !t.Derived:
			in.GUIDs = append(in.GUIDs, t.Value)
		case t.Kind == TargetIP:
			in.IPs = append(in.IPs, t.Value)
		}
	}
	return in
}

// BanInput is what a caller sets when creating or editing a ban.
type BanInput struct {
	SteamID   string     `json:"steam_id,omitempty"`
	GUIDs     []string   `json:"guids,omitempty"`
	IPs       []string   `json:"ips,omitempty"`
	Reason    string     `json:"reason"`
	Message   string     `json:"message,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Scope     Scope      `json:"scope"`
	Instances []string   `json:"instances,omitempty"`
	Evidence  []int64    `json:"evidence,omitempty"`
}

// normalise validates the input and returns it cleaned, with its targets.
func (in BanInput) normalise(now time.Time) (BanInput, []Target, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	in.Message = strings.TrimSpace(in.Message)
	switch {
	case in.Reason == "":
		return in, nil, invalid("a ban needs a reason")
	case utf8.RuneCountInString(in.Reason) > MaxReason:
		return in, nil, invalid("the reason is longer than %d characters", MaxReason)
	case utf8.RuneCountInString(in.Message) > MaxMessage:
		return in, nil, invalid("the message is longer than %d characters", MaxMessage)
	case in.ExpiresAt != nil && !in.ExpiresAt.After(now):
		return in, nil, invalid("the expiry %s is not in the future", FormatExpiry(in.ExpiresAt))
	}
	if in.Message == "" {
		in.Message = DefaultMessage
	}
	if in.ExpiresAt != nil {
		t := in.ExpiresAt.UTC().Truncate(time.Second)
		in.ExpiresAt = &t
	}
	switch in.Scope {
	case ScopeGlobal:
		if len(in.Instances) > 0 {
			return in, nil, invalid("a global ban lists no instances")
		}
	case ScopeInstances:
		if len(in.Instances) == 0 {
			return in, nil, invalid("an instance ban needs at least one instance")
		}
		for _, i := range in.Instances {
			if i == "" || len(i) > maxInstanceLen || strings.ContainsFunc(i, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				return in, nil, invalid("invalid instance name %q", i)
			}
		}
		slices.Sort(in.Instances)
		in.Instances = slices.Compact(in.Instances)
	default:
		return in, nil, invalid("scope must be global or instances, not %q", in.Scope)
	}
	var ts []Target
	add := func(t Target) {
		for _, have := range ts {
			if have.Kind == t.Kind && have.Value == t.Value {
				return
			}
		}
		ts = append(ts, t)
	}
	if in.SteamID != "" {
		g, err := DeriveGUID(in.SteamID)
		if err != nil {
			return in, nil, err
		}
		add(Target{Kind: TargetSteam, Value: in.SteamID})
		add(Target{Kind: TargetGUID, Value: g, Derived: true})
	}
	for i, g := range in.GUIDs {
		g = strings.ToLower(strings.TrimSpace(g))
		if !ValidGUID(g) {
			return in, nil, invalid("invalid BattlEye GUID %q", in.GUIDs[i])
		}
		in.GUIDs[i] = g
		add(Target{Kind: TargetGUID, Value: g})
	}
	for i, ip := range in.IPs {
		v, err := ParseIP(ip)
		if err != nil {
			return in, nil, err
		}
		in.IPs[i] = v
		add(Target{Kind: TargetIP, Value: v})
	}
	if len(ts) == 0 {
		return in, nil, invalid("a ban needs a Steam ID, a GUID or an IP address")
	}
	return in, ts, nil
}

// ErrNotFound is returned for an unknown ban or evidence id.
var ErrNotFound = errors.New("moderation: not found")

// Store is the moderation database.
type Store struct {
	db  *sql.DB
	dir string // evidence files
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func dsn(path string, readOnly bool) string {
	q := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if readOnly {
		q += "&mode=ro"
	} else {
		q += "&_pragma=journal_mode(WAL)"
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q}).String()
}

// Open opens the database at path, creating it (mode 0600) and applying new migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("moderation: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // dzo's own database path
	if err != nil {
		return nil, fmt.Errorf("moderation: %w", err)
	}
	_ = f.Close() // only created here, so that SQLite does not create it with the umask's mode
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("moderation: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // one writer; transactions queue in the process instead of failing with SQLITE_BUSY
	s := &Store{db: db, dir: filepath.Join(filepath.Dir(path), "evidence")}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenReadOnly opens an existing database without writing to it (the instance start path). A missing
// database gives an error that wraps os.ErrNotExist.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("moderation: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, fmt.Errorf("moderation: open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("moderation: open %s: %w", path, err)
	}
	return &Store{db: db, dir: filepath.Join(filepath.Dir(path), "evidence")}, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("moderation: schema_version: %w", err)
	}
	var cur int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&cur); err != nil {
		return fmt.Errorf("moderation: schema_version: %w", err)
	}
	files, err := fs.Glob(migrationsFS, "migrations/sqlite/*.sql")
	if err != nil {
		return fmt.Errorf("moderation: migrations: %w", err)
	}
	sort.Strings(files)
	for _, f := range files {
		n, err := strconv.Atoi(strings.SplitN(path.Base(f), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("moderation: migration %s: name must start with its number", f)
		}
		if n <= cur {
			continue
		}
		body, err := migrationsFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("moderation: migration %s: %w", f, err)
		}
		err = s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, n)
			return err
		})
		if err != nil {
			return fmt.Errorf("moderation: migration %s: %w", f, err)
		}
	}
	return nil
}

// tx runs fn in a transaction, committing when it returns nil.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback() // the error of fn is the one that matters
		return err
	}
	return tx.Commit()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func unixOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func timeOrNil(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

// CreateBan stores a new ban and its history entry.
func (s *Store) CreateBan(ctx context.Context, actor string, in BanInput, now time.Time) (Ban, error) {
	in, targets, err := in.normalise(now)
	if err != nil {
		return Ban{}, err
	}
	var b Ban
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO bans (reason, message, created_by, created_at, expires_at, scope) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
			in.Reason, in.Message, actor, now.Unix(), unixOrNil(in.ExpiresAt), string(in.Scope)).Scan(&id); err != nil {
			return err
		}
		if err := writeChildren(ctx, tx, id, in, targets); err != nil {
			return err
		}
		var err error
		if b, err = getBan(ctx, tx, id); err != nil {
			return err
		}
		return addHistory(ctx, tx, id, now, actor, "create", map[string]any{"new": b})
	})
	if err != nil {
		return Ban{}, wrap("create ban", err)
	}
	return b, nil
}

// UpdateBan replaces the editable fields of a ban that is not revoked.
func (s *Store) UpdateBan(ctx context.Context, id int64, actor string, in BanInput, now time.Time) (Ban, error) {
	var b Ban
	err := s.tx(ctx, func(tx *sql.Tx) error {
		old, err := getBan(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.RevokedAt != nil {
			return invalid("ban %d is revoked and cannot be edited", id)
		}
		in, targets, err := in.normalise(now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bans SET reason = ?, message = ?, expires_at = ?, scope = ? WHERE id = ?`,
			in.Reason, in.Message, unixOrNil(in.ExpiresAt), string(in.Scope), id); err != nil {
			return err
		}
		if err := writeChildren(ctx, tx, id, in, targets); err != nil {
			return err
		}
		if b, err = getBan(ctx, tx, id); err != nil {
			return err
		}
		return addHistory(ctx, tx, id, now, actor, "edit", map[string]any{"old": old, "new": b})
	})
	if err != nil {
		return Ban{}, wrap("update ban", err)
	}
	return b, nil
}

// RevokeBan ends a ban now. It stays in the database as history.
func (s *Store) RevokeBan(ctx context.Context, id int64, actor, reason string, now time.Time) (Ban, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Ban{}, invalid("revoking a ban needs a reason")
	}
	if utf8.RuneCountInString(reason) > MaxReason {
		return Ban{}, invalid("the reason is longer than %d characters", MaxReason)
	}
	var b Ban
	err := s.tx(ctx, func(tx *sql.Tx) error {
		old, err := getBan(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.RevokedAt != nil {
			return invalid("ban %d is already revoked", id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bans SET revoked_by = ?, revoked_at = ?, revoke_reason = ? WHERE id = ?`,
			actor, now.Unix(), reason, id); err != nil {
			return err
		}
		if b, err = getBan(ctx, tx, id); err != nil {
			return err
		}
		return addHistory(ctx, tx, id, now, actor, "revoke", map[string]any{"reason": reason})
	})
	if err != nil {
		return Ban{}, wrap("revoke ban", err)
	}
	return b, nil
}

// wrap keeps InvalidError and ErrNotFound recognisable for the API.
func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	var inv *InvalidError
	if errors.As(err, &inv) || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("moderation: %s: %w", what, err)
}

func writeChildren(ctx context.Context, tx *sql.Tx, id int64, in BanInput, targets []Target) error {
	for _, q := range []string{`DELETE FROM ban_instances WHERE ban_id = ?`, `DELETE FROM ban_targets WHERE ban_id = ?`, `DELETE FROM ban_evidence WHERE ban_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	for _, i := range in.Instances {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ban_instances (ban_id, instance) VALUES (?, ?)`, id, i); err != nil {
			return err
		}
	}
	for _, t := range targets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ban_targets (ban_id, kind, value, derived) VALUES (?, ?, ?, ?)`, id, string(t.Kind), t.Value, t.Derived); err != nil {
			return err
		}
	}
	for _, e := range in.Evidence {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM evidence WHERE id = ?`, e).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return invalid("unknown evidence id %d", e)
		} else if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ban_evidence (ban_id, evidence_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, e); err != nil {
			return err
		}
	}
	return nil
}

func addHistory(ctx context.Context, tx *sql.Tx, id int64, now time.Time, actor, action string, change any) error {
	b, err := json.Marshal(change)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ban_history (ban_id, at, actor, action, change) VALUES (?, ?, ?, ?, ?)`, id, now.Unix(), actor, action, string(b))
	return err
}

func getBan(ctx context.Context, q querier, id int64) (Ban, error) {
	var b Ban
	var created int64
	var expires, revokedAt sql.NullInt64
	var revokedBy, revokeReason sql.NullString
	var scope string
	err := q.QueryRowContext(ctx, `SELECT id, reason, message, created_by, created_at, expires_at, scope, revoked_by, revoked_at, revoke_reason FROM bans WHERE id = ?`, id).
		Scan(&b.ID, &b.Reason, &b.Message, &b.CreatedBy, &created, &expires, &scope, &revokedBy, &revokedAt, &revokeReason)
	if errors.Is(err, sql.ErrNoRows) {
		return Ban{}, ErrNotFound
	}
	if err != nil {
		return Ban{}, err
	}
	b.CreatedAt, b.ExpiresAt, b.Scope = time.Unix(created, 0).UTC(), timeOrNil(expires), Scope(scope)
	b.RevokedBy, b.RevokedAt, b.RevokeReason = revokedBy.String, timeOrNil(revokedAt), revokeReason.String
	rows, err := q.QueryContext(ctx, `SELECT instance FROM ban_instances WHERE ban_id = ? ORDER BY instance`, id)
	if err != nil {
		return Ban{}, err
	}
	for rows.Next() {
		var i string
		if err := rows.Scan(&i); err != nil {
			_ = rows.Close()
			return Ban{}, err
		}
		b.Instances = append(b.Instances, i)
	}
	if err := closeRows(rows); err != nil {
		return Ban{}, err
	}
	// steam first, then guid, then ip: the order people read them in.
	rows, err = q.QueryContext(ctx, `SELECT kind, value, derived FROM ban_targets WHERE ban_id = ?
		ORDER BY CASE kind WHEN 'steam' THEN 0 WHEN 'guid' THEN 1 ELSE 2 END, derived DESC, value`, id)
	if err != nil {
		return Ban{}, err
	}
	for rows.Next() {
		var t Target
		var kind string
		if err := rows.Scan(&kind, &t.Value, &t.Derived); err != nil {
			_ = rows.Close()
			return Ban{}, err
		}
		t.Kind = TargetKind(kind)
		b.Targets = append(b.Targets, t)
	}
	if err := closeRows(rows); err != nil {
		return Ban{}, err
	}
	rows, err = q.QueryContext(ctx, `SELECT evidence_id FROM ban_evidence WHERE ban_id = ? ORDER BY evidence_id`, id)
	if err != nil {
		return Ban{}, err
	}
	for rows.Next() {
		var e int64
		if err := rows.Scan(&e); err != nil {
			_ = rows.Close()
			return Ban{}, err
		}
		b.Evidence = append(b.Evidence, e)
	}
	return b, closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

// GetBan returns one ban.
func (s *Store) GetBan(ctx context.Context, id int64) (Ban, error) {
	b, err := getBan(ctx, s.db, id)
	return b, wrap("get ban", err)
}

// BanFilter selects bans for ListBans. Empty fields do not filter; Limit 0 means no limit.
type BanFilter struct {
	State    string // "", "active", "expired" or "revoked"
	Instance string // bans that apply to this instance
	Query    string // a Steam ID, GUID or IP among the targets, or text in the reason or a sighted name
	Limit    int
	Now      time.Time
}

// likeEscape makes s a literal for LIKE ... ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListBans returns the bans matching f, newest first.
func (s *Store) ListBans(ctx context.Context, f BanFilter) ([]Ban, error) {
	q := `SELECT b.id FROM bans b WHERE 1 = 1`
	var args []any
	now := f.Now.Unix()
	switch f.State {
	case "":
	case "active":
		q += ` AND b.revoked_at IS NULL AND (b.expires_at IS NULL OR b.expires_at > ?)`
		args = append(args, now)
	case "expired":
		q += ` AND b.revoked_at IS NULL AND b.expires_at IS NOT NULL AND b.expires_at <= ?`
		args = append(args, now)
	case "revoked":
		q += ` AND b.revoked_at IS NOT NULL`
	default:
		return nil, invalid("state must be active, expired or revoked, not %q", f.State)
	}
	if f.Instance != "" {
		q += ` AND (b.scope = 'global' OR EXISTS (SELECT 1 FROM ban_instances i WHERE i.ban_id = b.id AND i.instance = ?))`
		args = append(args, f.Instance)
	}
	if text := strings.TrimSpace(f.Query); text != "" {
		values := []any{text, strings.ToLower(text)}
		if g, err := DeriveGUID(text); err == nil {
			values = append(values, g)
		}
		like := "%" + likeEscape(text) + "%"
		q += ` AND (EXISTS (SELECT 1 FROM ban_targets t WHERE t.ban_id = b.id AND t.value IN (?` + strings.Repeat(", ?", len(values)-1) + `))
			OR b.reason LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM ban_targets t JOIN sightings g ON g.guid = t.value WHERE t.ban_id = b.id AND g.name LIKE ? ESCAPE '\'))`
		args = append(append(args, values...), like, like)
	}
	q += ` ORDER BY b.id DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("moderation: list bans: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: list bans: %w", err)
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("moderation: list bans: %w", err)
	}
	out := make([]Ban, 0, len(ids))
	for _, id := range ids {
		b, err := getBan(ctx, s.db, id)
		if err != nil {
			return nil, fmt.Errorf("moderation: list bans: %w", err)
		}
		out = append(out, b)
	}
	return out, nil
}

// ActiveBans returns every ban in force on an instance at now.
func (s *Store) ActiveBans(ctx context.Context, instance string, now time.Time) ([]Ban, error) {
	return s.ListBans(ctx, BanFilter{State: "active", Instance: instance, Now: now})
}

// FindActive returns the ban in force on an instance for a GUID or an IP (either may be empty),
// preferring a permanent one, then the longest; nil when there is none.
func (s *Store) FindActive(ctx context.Context, instance, guid, ip string, now time.Time) (*Ban, error) {
	if guid == "" && ip == "" {
		return nil, nil
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT b.id FROM bans b JOIN ban_targets t ON t.ban_id = b.id
		WHERE t.kind IN ('guid', 'ip') AND t.value IN (?, ?)
		  AND b.revoked_at IS NULL AND (b.expires_at IS NULL OR b.expires_at > ?)
		  AND (b.scope = 'global' OR EXISTS (SELECT 1 FROM ban_instances i WHERE i.ban_id = b.id AND i.instance = ?))
		ORDER BY b.expires_at IS NULL DESC, b.expires_at DESC, b.id LIMIT 1`, guid, ip, now.Unix(), instance).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("moderation: find ban: %w", err)
	}
	b, err := getBan(ctx, s.db, id)
	if err != nil {
		return nil, fmt.Errorf("moderation: find ban: %w", err)
	}
	return &b, nil
}

// HistoryEntry is one change of a ban.
type HistoryEntry struct {
	At     time.Time       `json:"at"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"` // create, edit or revoke
	Change json.RawMessage `json:"change"`
}

// History returns the changes of a ban, oldest first.
func (s *Store) History(ctx context.Context, id int64) ([]HistoryEntry, error) {
	if _, err := s.GetBan(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT at, actor, action, change FROM ban_history WHERE ban_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("moderation: history: %w", err)
	}
	var out []HistoryEntry
	for rows.Next() {
		var h HistoryEntry
		var at int64
		var change string
		if err := rows.Scan(&at, &h.Actor, &h.Action, &change); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: history: %w", err)
		}
		h.At, h.Change = time.Unix(at, 0).UTC(), json.RawMessage(change)
		out = append(out, h)
	}
	return out, closeRows(rows)
}
```

Note on `""` in `FindActive`'s `IN (?, ?)`: an empty GUID or IP never matches a stored target, because targets are validated non-empty.

- [ ] **Step 6: Run the tests**

Run: `go test -race ./internal/moderation/`
Expected: PASS. If `TestOpenReadOnly`'s write is *accepted*, the driver ignored `mode=ro`. Check `dsn(path, true)` produces `file:///…?…&mode=ro`.

- [ ] **Step 7: Lint and commit**

Run: `make fmt lint`
Expected: clean.

```bash
git add go.mod go.sum internal/moderation
git commit -m "moderation: SQLite ban store with history

Adds modernc.org/sqlite (BSD-3-Clause, pure Go, no cgo; packaged in Debian as
golang-modernc-sqlite-dev) for the database the plan's D26 calls for: bans need
transactions, history and queries that a JSON file cannot give safely.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Evidence and sightings

**Files:**
- Create: `internal/moderation/evidence.go`, `internal/moderation/evidence_test.go`, `internal/moderation/sightings.go`, `internal/moderation/sightings_test.go`

**Interfaces:**
- Consumes: `Store`, `invalid`, `DeriveGUID`, `ParseIP`, `closeRows` (Task 4).
- Produces:

```go
type EvidenceKind string  // EvidenceLog "log", EvidenceNote "note", EvidenceImage "image"
type Evidence struct { ID int64; SteamID, GUID, IP, Instance, Source string; Kind EvidenceKind; Body, FileType string; CreatedAt time.Time }
type EvidenceInput struct { SteamID, GUID, IP, Instance string; Kind EvidenceKind; Body string }
type Who struct { SteamID, GUID, IP string }
const MaxEvidenceBody = 64 << 10; const MaxImage = 5 << 20
func (s *Store) AddEvidence(ctx context.Context, source string, in EvidenceInput, image []byte, now time.Time) (Evidence, error)
func (s *Store) GetEvidence(ctx context.Context, id int64) (Evidence, error)
func (s *Store) EvidenceFor(ctx context.Context, who Who) ([]Evidence, error)
func (s *Store) EvidenceFile(ctx context.Context, id int64) (path, contentType string, err error)
type Sighting struct { GUID, IP, Name, Instance string; FirstSeen, LastSeen time.Time; SharedWith int }
func (s *Store) Seen(ctx context.Context, instance, guid, ip, name string, now time.Time) error
func (s *Store) Identities(ctx context.Context, steamID string) ([]Sighting, error)
```

- [ ] **Step 1: Write the failing tests**

`internal/moderation/evidence_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// png1x1 is a minimal valid PNG header followed by filler; http.DetectContentType only needs the header.
var png1x1 = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0}, 64)...)

func TestAddEvidenceLogDerivesTheGUID(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.AddEvidence(ctx, "detector:speed", EvidenceInput{SteamID: steamA, Instance: "alpha", Kind: EvidenceLog, Body: "speed 900 km/h\x00"}, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if e.GUID != guidOf(t, steamA) || e.Body != "speed 900 km/h" || e.Source != "detector:speed" {
		t.Fatalf("evidence %+v", e)
	}
	list, err := s.EvidenceFor(ctx, Who{GUID: guidOf(t, steamA)})
	if err != nil || len(list) != 1 || list[0].ID != e.ID {
		t.Fatalf("EvidenceFor = %+v, %v", list, err)
	}
	b, err := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "speedhack", Scope: ScopeGlobal, Evidence: []int64{e.ID}}, t0)
	if err != nil || len(b.Evidence) != 1 || b.Evidence[0] != e.ID {
		t.Fatalf("ban evidence %+v, %v", b.Evidence, err)
	}
}

func TestAddEvidenceValidates(t *testing.T) {
	s, _ := newStore(t)
	for name, in := range map[string]EvidenceInput{
		"nobody":        {Instance: "alpha", Kind: EvidenceNote, Body: "x"},
		"no instance":   {SteamID: steamA, Kind: EvidenceNote, Body: "x"},
		"empty body":    {SteamID: steamA, Instance: "alpha", Kind: EvidenceNote, Body: "  "},
		"bad kind":      {SteamID: steamA, Instance: "alpha", Kind: "video", Body: "x"},
		"image kind":    {SteamID: steamA, Instance: "alpha", Kind: EvidenceImage, Body: "x"},
		"guid mismatch": {SteamID: steamA, GUID: guidOf(t, steamB), Instance: "alpha", Kind: EvidenceNote, Body: "x"},
		"too long":      {SteamID: steamA, Instance: "alpha", Kind: EvidenceLog, Body: strings.Repeat("l", MaxEvidenceBody+1)},
	} {
		var inv *InvalidError
		if _, err := s.AddEvidence(ctx, "a", in, nil, t0); !errors.As(err, &inv) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAddEvidenceImageIsStoredByHash(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.AddEvidence(ctx, "alice", EvidenceInput{IP: "192.0.2.7", Instance: "alpha", Body: "screenshot"}, png1x1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != EvidenceImage || e.FileType != "image/png" {
		t.Fatalf("evidence %+v", e)
	}
	path, ct, err := s.EvidenceFile(ctx, e.ID)
	if err != nil || ct != "image/png" {
		t.Fatalf("EvidenceFile = %q, %q, %v", path, ct, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", info.Mode().Perm(), err)
	}
	e2, err := s.AddEvidence(ctx, "alice", EvidenceInput{IP: "192.0.2.7", Instance: "alpha"}, png1x1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if p2, _, _ := s.EvidenceFile(ctx, e2.ID); p2 != path {
		t.Fatalf("the same image is stored twice: %s, %s", path, p2)
	}
	if _, _, err := s.EvidenceFile(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing evidence file: %v", err)
	}
}

func TestAddEvidenceRefusesNonImages(t *testing.T) {
	s, _ := newStore(t)
	for name, data := range map[string][]byte{
		"html":     []byte("<!DOCTYPE html><script>alert(1)</script>"),
		"svg":      []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"too big":  append(png1x1, bytes.Repeat([]byte{0}, MaxImage)...),
		"empty":    {},
		"gif":      []byte("GIF89a......"),
	} {
		var inv *InvalidError
		if _, err := s.AddEvidence(ctx, "a", EvidenceInput{SteamID: steamA, Instance: "alpha"}, data, t0); !errors.As(err, &inv) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
```

`internal/moderation/sightings_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"
)

func TestSightingsAndSharedIPs(t *testing.T) {
	s, _ := newStore(t)
	ga, gb := guidOf(t, steamA), guidOf(t, steamB)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Seen(ctx, "alpha", ga, "192.0.2.7", "alice", t0))
	must(s.Seen(ctx, "alpha", ga, "192.0.2.7", "alice2", t0.Add(time.Hour))) // same row, newer name
	must(s.Seen(ctx, "beta", ga, "198.51.100.1", "alice", t0))
	must(s.Seen(ctx, "alpha", gb, "192.0.2.7", "bob", t0)) // shares the first IP
	ids, err := s.Identities(ctx, steamA)
	if err != nil || len(ids) != 2 {
		t.Fatalf("Identities = %+v, %v", ids, err)
	}
	byIP := map[string]Sighting{}
	for _, x := range ids {
		byIP[x.IP] = x
	}
	if x := byIP["192.0.2.7"]; x.SharedWith != 1 || x.Name != "alice2" || !x.FirstSeen.Equal(t0) || !x.LastSeen.Equal(t0.Add(time.Hour)) {
		t.Fatalf("shared IP sighting %+v", x)
	}
	if x := byIP["198.51.100.1"]; x.SharedWith != 0 {
		t.Fatalf("own IP sighting %+v", x)
	}
	if err := s.Seen(ctx, "alpha", "nope", "192.0.2.7", "x", t0); err == nil {
		t.Fatal("an invalid GUID was recorded")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/moderation/`
Expected: FAIL, `undefined: EvidenceInput`.

- [ ] **Step 3: Implement `evidence.go`**

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EvidenceKind is what an evidence item holds.
type EvidenceKind string

const (
	EvidenceLog   EvidenceKind = "log"   // log lines, e.g. from a cheat detector
	EvidenceNote  EvidenceKind = "note"  // an admin's note
	EvidenceImage EvidenceKind = "image" // a screenshot; Body is its caption
)

// Limits of evidence.
const (
	MaxEvidenceBody = 64 << 10
	MaxImage        = 5 << 20
)

// imageTypes are the accepted image types, by what http.DetectContentType says.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// Evidence is one item filed against a player.
type Evidence struct {
	ID        int64        `json:"id"`
	SteamID   string       `json:"steam_id,omitempty"`
	GUID      string       `json:"guid,omitempty"`
	IP        string       `json:"ip,omitempty"`
	Instance  string       `json:"instance"`
	Source    string       `json:"source"`
	Kind      EvidenceKind `json:"kind"`
	Body      string       `json:"body"`
	FileType  string       `json:"file_type,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// EvidenceInput is a new evidence item. Kind is ignored when an image is given.
type EvidenceInput struct {
	SteamID  string       `json:"steam_id,omitempty"`
	GUID     string       `json:"guid,omitempty"`
	IP       string       `json:"ip,omitempty"`
	Instance string       `json:"instance"`
	Kind     EvidenceKind `json:"kind"`
	Body     string       `json:"body"`
}

// Who names a player by any of their identifiers.
type Who struct {
	SteamID, GUID, IP string
}

// resolve validates the identifiers and fills the GUID from the Steam ID.
func (w Who) resolve() (Who, error) {
	if w.SteamID != "" {
		g, err := DeriveGUID(w.SteamID)
		if err != nil {
			return w, err
		}
		if w.GUID != "" && strings.ToLower(w.GUID) != g {
			return w, invalid("GUID %s does not belong to Steam ID %s", w.GUID, w.SteamID)
		}
		w.GUID = g
	}
	if w.GUID != "" {
		w.GUID = strings.ToLower(w.GUID)
		if !ValidGUID(w.GUID) {
			return w, invalid("invalid BattlEye GUID %q", w.GUID)
		}
	}
	if w.IP != "" {
		ip, err := ParseIP(w.IP)
		if err != nil {
			return w, err
		}
		w.IP = ip
	}
	if w.GUID == "" && w.IP == "" {
		return w, invalid("evidence needs a Steam ID, a GUID or an IP address")
	}
	return w, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// AddEvidence stores an evidence item, and the image (if any) as <evidence dir>/<aa>/<sha256>.
func (s *Store) AddEvidence(ctx context.Context, source string, in EvidenceInput, image []byte, now time.Time) (Evidence, error) {
	who, err := Who{SteamID: in.SteamID, GUID: in.GUID, IP: in.IP}.resolve()
	if err != nil {
		return Evidence{}, err
	}
	in.Instance = strings.TrimSpace(in.Instance)
	if in.Instance == "" || len(in.Instance) > maxInstanceLen {
		return Evidence{}, invalid("evidence needs the instance it is from")
	}
	in.Body = strings.TrimSpace(strings.ReplaceAll(in.Body, "\x00", ""))
	if len(in.Body) > MaxEvidenceBody {
		return Evidence{}, invalid("the evidence text is longer than %d bytes", MaxEvidenceBody)
	}
	var sum, ctype string
	if image != nil {
		if len(image) == 0 || len(image) > MaxImage {
			return Evidence{}, invalid("an image must be 1 byte to %d MiB", MaxImage>>20)
		}
		ctype = http.DetectContentType(image)
		if !imageTypes[ctype] {
			return Evidence{}, invalid("images must be PNG, JPEG or WebP, not %s", ctype)
		}
		h := sha256.Sum256(image)
		sum = hex.EncodeToString(h[:])
		if err := s.writeFile(sum, image); err != nil {
			return Evidence{}, err
		}
		in.Kind = EvidenceImage
	} else {
		if in.Kind != EvidenceLog && in.Kind != EvidenceNote {
			return Evidence{}, invalid("evidence kind must be log or note (images are uploaded as files), not %q", in.Kind)
		}
		if in.Body == "" {
			return Evidence{}, invalid("evidence needs some text")
		}
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `INSERT INTO evidence (steam_id, guid, ip, instance, source, kind, body, file_sha256, file_type, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		nullable(who.SteamID), nullable(who.GUID), nullable(who.IP), in.Instance, source, string(in.Kind), in.Body, nullable(sum), nullable(ctype), now.Unix()).Scan(&id)
	if err != nil {
		return Evidence{}, fmt.Errorf("moderation: add evidence: %w", err)
	}
	return s.GetEvidence(ctx, id)
}

func (s *Store) filePath(sum string) string { return filepath.Join(s.dir, sum[:2], sum) }

// writeFile stores data under its hash, atomically, unless it is there already.
func (s *Store) writeFile(sum string, data []byte) error {
	p := s.filePath(sum)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fmt.Errorf("moderation: evidence file: %w", err)
	}
	return writeAtomic(p, data)
}

// writeAtomic writes data to path through a temporary file in the same directory (mode 0600).
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("moderation: write %s: %w", filepath.Base(path), err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the write error is what matters
		return fmt.Errorf("moderation: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

const evidenceCols = `id, COALESCE(steam_id, ''), COALESCE(guid, ''), COALESCE(ip, ''), instance, source, kind, body, COALESCE(file_type, ''), created_at`

func scanEvidence(sc interface{ Scan(...any) error }) (Evidence, error) {
	var e Evidence
	var kind string
	var created int64
	if err := sc.Scan(&e.ID, &e.SteamID, &e.GUID, &e.IP, &e.Instance, &e.Source, &kind, &e.Body, &e.FileType, &created); err != nil {
		return Evidence{}, err
	}
	e.Kind, e.CreatedAt = EvidenceKind(kind), time.Unix(created, 0).UTC()
	return e, nil
}

// GetEvidence returns one evidence item.
func (s *Store) GetEvidence(ctx context.Context, id int64) (Evidence, error) {
	e, err := scanEvidence(s.db.QueryRowContext(ctx, `SELECT `+evidenceCols+` FROM evidence WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Evidence{}, ErrNotFound
	}
	if err != nil {
		return Evidence{}, fmt.Errorf("moderation: evidence: %w", err)
	}
	return e, nil
}

// EvidenceFor returns the evidence filed against a player (by GUID, which a Steam ID resolves to,
// or by IP), newest first, at most 500 items.
func (s *Store) EvidenceFor(ctx context.Context, who Who) ([]Evidence, error) {
	who, err := who.resolve()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+evidenceCols+` FROM evidence WHERE guid = ? OR ip = ? ORDER BY id DESC LIMIT 500`, who.GUID, who.IP)
	if err != nil {
		return nil, fmt.Errorf("moderation: evidence: %w", err)
	}
	var out []Evidence
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: evidence: %w", err)
		}
		out = append(out, e)
	}
	return out, closeRows(rows)
}

// EvidenceFile returns the stored file of an image evidence item and its content type.
func (s *Store) EvidenceFile(ctx context.Context, id int64) (path, contentType string, err error) {
	var sum, ctype sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT file_sha256, file_type FROM evidence WHERE id = ?`, id).Scan(&sum, &ctype)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !sum.Valid) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("moderation: evidence file: %w", err)
	}
	return s.filePath(sum.String), ctype.String, nil
}
```

- [ ] **Step 4: Implement `sightings.go`**

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"fmt"
	"time"
)

// Sighting is a GUID seen with an IP on an instance. SharedWith counts the other GUIDs seen behind the
// same IP (carrier-grade NAT puts many players behind one address).
type Sighting struct {
	GUID       string    `json:"guid"`
	IP         string    `json:"ip"`
	Name       string    `json:"name"`
	Instance   string    `json:"instance"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	SharedWith int       `json:"shared_with"`
}

const maxName = 128

// Seen records that a GUID connected from an IP under a name.
func (s *Store) Seen(ctx context.Context, instance, guid, ip, name string, now time.Time) error {
	if !ValidGUID(guid) {
		return invalid("invalid BattlEye GUID %q", guid)
	}
	ip, err := ParseIP(ip)
	if err != nil {
		return err
	}
	if len(name) > maxName {
		name = name[:maxName]
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sightings (guid, ip, name, instance, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (guid, ip, instance) DO UPDATE SET name = excluded.name, last_seen = excluded.last_seen`,
		guid, ip, name, instance, now.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("moderation: sighting: %w", err)
	}
	return nil
}

// Identities returns where a Steam ID's GUID was seen, most recent first.
func (s *Store) Identities(ctx context.Context, steamID string) ([]Sighting, error) {
	g, err := DeriveGUID(steamID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.guid, s.ip, s.name, s.instance, s.first_seen, s.last_seen,
			(SELECT COUNT(DISTINCT o.guid) FROM sightings o WHERE o.ip = s.ip AND o.guid <> s.guid)
		FROM sightings s WHERE s.guid = ? ORDER BY s.last_seen DESC`, g)
	if err != nil {
		return nil, fmt.Errorf("moderation: identities: %w", err)
	}
	var out []Sighting
	for rows.Next() {
		var x Sighting
		var first, last int64
		if err := rows.Scan(&x.GUID, &x.IP, &x.Name, &x.Instance, &first, &last, &x.SharedWith); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: identities: %w", err)
		}
		x.FirstSeen, x.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out = append(out, x)
	}
	return out, closeRows(rows)
}
```

Note: `TestSightingsAndSharedIPs` expects two rows for steamA (alpha/192.0.2.7 and beta/198.51.100.1). The alpha row's `name` is the newer "alice2", and `first_seen` stays `t0` because the upsert does not touch it.

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/moderation/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/moderation
git commit -m "moderation: player evidence (log lines, notes, images) and GUID/IP sightings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: bans.txt: render, parse, write, import, start path

**Files:**
- Create: `internal/moderation/bansfile.go`, `internal/moderation/bansfile_test.go`
- Modify: `internal/serve/serve.go` (add `BanDB`), `cmd/dzo/instance.go` (render at start)

**Interfaces:**
- Consumes: `Ban`, `Store.ActiveBans`, `Store.CreateBan`, `writeAtomic`, `OpenReadOnly` (Tasks 4–5). Reads `testdata/v1`.
- Produces:

```go
type Entry struct { Value string; Expires int64; Reason string } // Expires: Unix seconds, -1 permanent
func Entries(bans []Ban, instance string) []Entry
func FormatBansTxt(es []Entry) []byte
func ParseBansTxt(b []byte) []Entry
func BEReason(s string) string
const BansTxt = "bans.txt"
func WriteBansFile(dir string, content []byte) (changed bool, err error)
func ChangedOutside(dir string) (bool, []byte, error)
func (s *Store) ImportEntries(ctx context.Context, instance string, es []Entry, now time.Time) (imported int, err error)
func RenderAtStart(ctx context.Context, dbPath, instance, beDir string, now time.Time) (string, error)
// internal/serve:
func BanDB(cfg *config.Config) string // <paths.db>/dzo.sqlite
```

- [ ] **Step 1: Write the failing tests**

`internal/moderation/bansfile_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The file BattlEye itself wrote (V1) must survive our parser and formatter unchanged: then what we
// write is what BattlEye writes.
func TestBansTxtRoundTripsTheCapture(t *testing.T) {
	golden, err := os.ReadFile("testdata/v1/bans.txt")
	if err != nil {
		t.Fatal(err)
	}
	es := ParseBansTxt(golden)
	if len(es) < 3 {
		t.Fatalf("parsed %d entries from the capture: %+v", len(es), es)
	}
	if got := string(FormatBansTxt(es)); got != string(golden) {
		t.Fatalf("round trip differs:\n--- got\n%s--- captured\n%s", got, golden)
	}
	byValue := map[string]Entry{}
	for _, e := range es {
		byValue[e.Value] = e
	}
	if e := byValue["0123456789abcdef0123456789abcdef"]; e.Expires != -1 || e.Reason != "permanent reason with spaces" {
		t.Errorf("permanent GUID ban %+v", e)
	}
	if e := byValue["fedcba9876543210fedcba9876543210"]; e.Expires <= 0 {
		t.Errorf("timed GUID ban %+v", e)
	}
	if e := byValue["192.0.2.7"]; e.Expires != -1 {
		t.Errorf("IP ban %+v", e)
	}
}

func TestEntriesMergeKeepsLongest(t *testing.T) {
	g := guidOf(t, steamA)
	bans := []Ban{
		{ID: 1, Message: "one hour", Scope: ScopeGlobal, ExpiresAt: at(time.Hour), Targets: []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetGUID, Value: g, Derived: true}}},
		{ID: 2, Message: "forever here", Scope: ScopeInstances, Instances: []string{"alpha"}, Targets: []Target{{Kind: TargetGUID, Value: g}, {Kind: TargetIP, Value: "192.0.2.7"}}},
		{ID: 3, Message: "beta only", Scope: ScopeInstances, Instances: []string{"beta"}, Targets: []Target{{Kind: TargetIP, Value: "198.51.100.1"}}},
	}
	es := Entries(bans, "alpha")
	want := []Entry{{Value: g, Expires: -1, Reason: "forever here"}, {Value: "192.0.2.7", Expires: -1, Reason: "forever here"}}
	if len(es) != 2 {
		t.Fatalf("entries %+v", es)
	}
	got := map[string]Entry{es[0].Value: es[0], es[1].Value: es[1]}
	for _, w := range want {
		if got[w.Value] != w {
			t.Errorf("entry %+v, want %+v", got[w.Value], w)
		}
	}
	// beta: the global one-hour GUID ban and beta's own permanent IP ban.
	beta := map[string]Entry{}
	for _, e := range Entries(bans, "beta") {
		beta[e.Value] = e
	}
	if len(beta) != 2 || beta[g].Expires != t0.Add(time.Hour).Unix() || beta["198.51.100.1"].Expires != -1 {
		t.Errorf("beta entries %+v", beta)
	}
}

func TestBEReasonSanitises(t *testing.T) {
	for in, want := range map[string]string{
		"line\nbreak\r\tand\x00nul": "line break and nul",
		"  spaced   out  ":         "spaced out",
		"":                         "banned",
	} {
		if got := BEReason(in); got != want {
			t.Errorf("BEReason(%q) = %q, want %q", in, got, want)
		}
	}
	long := BEReason(strings.Repeat("ä", 1000))
	if len(long) > beReasonMax || !utf8.ValidString(long) {
		t.Errorf("long reason: %d bytes, valid UTF-8 %t", len(long), utf8.ValidString(long))
	}
}

func TestWriteBansFileAndChangedOutside(t *testing.T) {
	dir := t.TempDir()
	if changed, _, err := ChangedOutside(dir); err != nil || changed {
		t.Fatalf("no file: %t, %v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, BansTxt), []byte("0123456789abcdef0123456789abcdef -1 by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, content, err := ChangedOutside(dir); err != nil || !changed || !strings.Contains(string(content), "by hand") {
		t.Fatalf("hand-written file: %t, %v", changed, err)
	}
	if w, err := WriteBansFile(dir, []byte("x -1 y\n")); err != nil || !w {
		t.Fatalf("WriteBansFile = %t, %v", w, err)
	}
	if w, _ := WriteBansFile(dir, []byte("x -1 y\n")); w {
		t.Fatal("unchanged content was written again")
	}
	if changed, _, _ := ChangedOutside(dir); changed {
		t.Fatal("our own file counts as changed outside")
	}
	info, _ := os.Stat(filepath.Join(dir, BansTxt))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestImportEntries(t *testing.T) {
	s, _ := newStore(t)
	g := guidOf(t, steamA)
	_, _ = s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	n, err := s.ImportEntries(ctx, "alpha", []Entry{
		{Value: g, Expires: -1, Reason: "already banned"},
		{Value: "fedcba9876543210fedcba9876543210", Expires: t0.Add(time.Hour).Unix(), Reason: "timed"},
		{Value: "192.0.2.7", Expires: -1, Reason: ""},
		{Value: "11111111111111111111111111111111", Expires: t0.Add(-time.Hour).Unix(), Reason: "expired"},
		{Value: "garbage", Expires: -1, Reason: "skipped"},
	}, t0)
	if err != nil || n != 2 {
		t.Fatalf("imported %d, %v", n, err)
	}
	bs, _ := s.ListBans(ctx, BanFilter{Query: "192.0.2.7", Now: t0})
	if len(bs) != 1 || bs[0].CreatedBy != "import:bans.txt" || bs[0].Scope != ScopeInstances || bs[0].Instances[0] != "alpha" || bs[0].Message != DefaultMessage {
		t.Fatalf("imported IP ban %+v", bs)
	}
}

func TestRenderAtStart(t *testing.T) {
	s, path := newStore(t)
	be := t.TempDir()
	if msg, err := RenderAtStart(ctx, path+".none", "alpha", be, t0); err != nil || !strings.Contains(msg, "no ban database") {
		t.Fatalf("no database: %q, %v", msg, err)
	}
	_, _ = s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeGlobal}, t0)
	msg, err := RenderAtStart(ctx, path, "alpha", be, t0)
	if err != nil || !strings.Contains(msg, "1 entr") {
		t.Fatalf("render: %q, %v", msg, err)
	}
	b, _ := os.ReadFile(filepath.Join(be, BansTxt))
	if !strings.Contains(string(b), guidOf(t, steamA)+" -1 cheater") {
		t.Fatalf("bans.txt %q", b)
	}
	_ = os.WriteFile(filepath.Join(be, BansTxt), []byte("edited by hand\n"), 0o600)
	if msg, err := RenderAtStart(ctx, path, "alpha", be, t0); err != nil || !strings.Contains(msg, "changed outside") {
		t.Fatalf("changed outside: %q, %v", msg, err)
	}
	if b, _ := os.ReadFile(filepath.Join(be, BansTxt)); string(b) != "edited by hand\n" {
		t.Fatal("the start path overwrote a hand-edited bans.txt")
	}
}
```

**If NOTES.md Q1–Q3 differ from `<value> <expiry|-1> <reason>`:** change `FormatBansTxt`/`ParseBansTxt` below to the captured format, and the expected strings `" -1 cheater"` and the `Expires` checks in these tests to match. The round-trip test against the capture is what proves compatibility.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/moderation/ -run 'BansTxt|Entries|BEReason|WriteBansFile|ImportEntries|RenderAtStart'`
Expected: FAIL, `undefined: ParseBansTxt`.

- [ ] **Step 3: Implement `bansfile.go`**

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// BansTxt is BattlEye's ban list in the instance's BattlEye directory; sidecar holds the SHA-256 of
// what dzo last wrote there.
const (
	BansTxt = "bans.txt"
	sidecar = ".dzo-bans.sha256"
)

// beReasonMax is the longest reason BattlEye keeps, in bytes (testdata/v1/NOTES.md Q4).
const beReasonMax = 200

// Entry is one line of bans.txt. Expires is a Unix time, or -1 for a permanent ban.
type Entry struct {
	Value   string // a GUID or an IP address
	Expires int64
	Reason  string
}

// BEReason makes text safe for one bans.txt line and one RCon kick command: no control characters or
// line breaks, single spaces, at most beReasonMax bytes of valid UTF-8.
func BEReason(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	for len(s) > beReasonMax {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "banned"
	}
	return s
}

// Entries returns the bans.txt lines of the bans in force on an instance (bans must be active ones):
// one per GUID and IP target, the longest ban winning when several cover the same value.
func Entries(bans []Ban, instance string) []Entry {
	by := map[string]Entry{}
	for _, b := range bans {
		if !b.AppliesTo(instance) {
			continue
		}
		exp := int64(-1)
		if b.ExpiresAt != nil {
			exp = b.ExpiresAt.Unix()
		}
		for _, t := range b.Targets {
			if t.Kind != TargetGUID && t.Kind != TargetIP {
				continue
			}
			old, ok := by[t.Value]
			if ok && (old.Expires == -1 || (exp != -1 && exp <= old.Expires)) {
				continue
			}
			by[t.Value] = Entry{Value: t.Value, Expires: exp, Reason: BEReason(b.Message)}
		}
	}
	out := make([]Entry, 0, len(by))
	for _, e := range by {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// FormatBansTxt renders entries the way BattlEye writes the file (testdata/v1).
func FormatBansTxt(es []Entry) []byte {
	var b bytes.Buffer
	for _, e := range es {
		fmt.Fprintf(&b, "%s %d %s\n", e.Value, e.Expires, BEReason(e.Reason))
	}
	return b.Bytes()
}

// ParseBansTxt reads bans.txt leniently: blank lines, comments and lines it does not understand are
// skipped.
func ParseBansTxt(b []byte) []Entry {
	var out []Entry
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		f := strings.SplitN(line, " ", 3)
		if len(f) < 2 {
			continue
		}
		exp, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		e := Entry{Value: f[0], Expires: exp}
		if len(f) == 3 {
			e.Reason = f[2]
		}
		out = append(out, e)
	}
	return out
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// WriteBansFile writes dir/bans.txt atomically (mode 0600) when its content differs, and records its
// hash. changed reports whether it wrote.
func WriteBansFile(dir string, content []byte) (changed bool, err error) {
	path := filepath.Join(dir, BansTxt)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) { //nolint:gosec // the instance's BattlEye directory
		return false, os.WriteFile(filepath.Join(dir, sidecar), []byte(hashOf(content)+"\n"), 0o600)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, fmt.Errorf("moderation: %w", err)
	}
	if err := writeAtomic(path, content); err != nil {
		return false, err
	}
	if err := writeAtomic(filepath.Join(dir, sidecar), []byte(hashOf(content)+"\n")); err != nil {
		return false, err
	}
	return true, nil
}

// ChangedOutside reports whether dir/bans.txt exists and is not what dzo last wrote, and returns its
// content.
func ChangedOutside(dir string) (bool, []byte, error) {
	content, err := os.ReadFile(filepath.Join(dir, BansTxt)) //nolint:gosec // the instance's BattlEye directory
	if errors.Is(err, os.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("moderation: %w", err)
	}
	want, err := os.ReadFile(filepath.Join(dir, sidecar)) //nolint:gosec // dzo's own sidecar
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, nil, fmt.Errorf("moderation: %w", err)
	}
	if len(content) == 0 && err != nil {
		return false, content, nil // an empty file nobody wrote holds nothing to import
	}
	return strings.TrimSpace(string(want)) != hashOf(content), content, nil
}

// ImportEntries stores the entries of a bans.txt that dzo did not write as instance bans, skipping
// expired ones, values that are banned already and lines that are neither a GUID nor an IP.
func (s *Store) ImportEntries(ctx context.Context, instance string, es []Entry, now time.Time) (imported int, err error) {
	for _, e := range es {
		if e.Expires != -1 && e.Expires <= now.Unix() {
			continue
		}
		in := BanInput{Scope: ScopeInstances, Instances: []string{instance}, Message: BEReason(e.Reason)}
		in.Reason = "imported from bans.txt"
		if e.Reason != "" {
			in.Reason += ": " + e.Reason
		}
		if in.Message == "banned" {
			in.Message = ""
		}
		switch {
		case ValidGUID(e.Value):
			in.GUIDs = []string{e.Value}
		default:
			ip, err := ParseIP(e.Value)
			if err != nil {
				continue
			}
			in.IPs = []string{ip}
		}
		if e.Expires != -1 {
			t := time.Unix(e.Expires, 0).UTC()
			in.ExpiresAt = &t
		}
		if b, err := s.FindActive(ctx, instance, e.Value, e.Value, now); err != nil {
			return imported, err
		} else if b != nil {
			continue
		}
		if _, err := s.CreateBan(ctx, "import:"+BansTxt, in, now); err != nil {
			return imported, fmt.Errorf("moderation: import %s: %w", e.Value, err)
		}
		imported++
	}
	return imported, nil
}

// RenderAtStart writes an instance's bans.txt from a read-only database before the server starts, so
// bans hold even when dzo serve is down. It leaves the file alone when there is no database yet or when
// the file was changed outside dzo (serve imports it). The message says what it did.
func RenderAtStart(ctx context.Context, dbPath, instance, beDir string, now time.Time) (string, error) {
	st, err := OpenReadOnly(ctx, dbPath)
	if errors.Is(err, os.ErrNotExist) {
		return "no ban database yet; bans.txt left as it is", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()
	if changed, _, err := ChangedOutside(beDir); err != nil {
		return "", err
	} else if changed {
		return "bans.txt was changed outside dzo; left as it is until dzo serve imports it", nil
	}
	bans, err := st.ActiveBans(ctx, instance, now)
	if err != nil {
		return "", err
	}
	es := Entries(bans, instance)
	if _, err := WriteBansFile(beDir, FormatBansTxt(es)); err != nil {
		return "", err
	}
	return fmt.Sprintf("bans.txt written (%d entries)", len(es)), nil
}
```

Set `beReasonMax` to the limit NOTES.md Q4 found (in bytes). If Q4 found no limit within 300 characters, keep 200. If Q6 says BattlEye does *not* ignore `//` lines, keep the comment skip in `ParseBansTxt` anyway: dzo never writes comments.

The `"1 entr"` substring in `TestRenderAtStart` matches "1 entries". Fine as is, or add singular handling if you like.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/moderation/`
Expected: PASS. If `TestBansTxtRoundTripsTheCapture` fails, the capture's format differs: adapt `FormatBansTxt`/`ParseBansTxt` (see Step 1), never the golden file.

- [ ] **Step 5: Render at instance start**

In `internal/serve/serve.go`, after `AuditLog`:

```go
// BanDB is the moderation database (bans, evidence, sightings).
func BanDB(cfg *config.Config) string { return filepath.Join(cfg.Paths.DB, "dzo.sqlite") }
```

In `cmd/dzo/instance.go`, right after `if err := runfiles.Write(rf); err != nil { return err }`:

```go
	// Bans hold even when dzo serve is down: bans.txt is rendered from the database at every start.
	msg, err := moderation.RenderAtStart(cmd.Context(), serve.BanDB(cfg), inst.Name, filepath.Join(inst.Paths.Profiles, "battleye"), time.Now())
	if err != nil {
		return fmt.Errorf("rendering bans.txt: %w", err)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg)
```

and import `github.com/bzed/dayz-server-operator/internal/moderation` (and `time` if missing). Run `go test ./cmd/dzo/ -run TestRender` (expect PASS, now with "no ban database yet" in the output), then `make test-race`.

- [ ] **Step 6: Commit**

```bash
git add internal/moderation internal/serve/serve.go cmd/dzo/instance.go
git commit -m "moderation: render BattlEye's bans.txt, import outside changes, render at start

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Roles and ban permissions

**Files:**
- Modify: `internal/api/tokens.go`, `cmd/dzo/serve.go:95` (role help), `internal/api/api_test.go` (only if a role test lists all roles)
- Test: `internal/api/tokens_test.go`

**Interfaces:**
- Produces: `RoleDetector Role = "detector"`, and the permissions `PermBanView = "ban.view"`, `PermBanInstance = "ban.instance"`, `PermBanLong = "ban.permanent"`, `PermBanIP = "ban.ip"`, `PermBanGlobal = "ban.global"`, `PermBanRevokeAny = "ban.revoke_any"`, `PermEvidenceAdd = "evidence.add"`.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/tokens_test.go`:

```go
func TestBanPermissions(t *testing.T) {
	cases := map[Role][]string{
		RoleDetector:  {PermEvidenceAdd},
		RoleViewer:    {PermView},
		RoleModerator: {PermBanView, PermBanInstance, PermEvidenceAdd},
		RoleOperator:  {PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermEvidenceAdd},
		RoleAdmin:     {PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermBanGlobal, PermBanRevokeAny, PermEvidenceAdd},
	}
	all := []string{PermView, PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermBanGlobal, PermBanRevokeAny, PermEvidenceAdd}
	for role, want := range cases {
		for _, p := range all {
			if got := role.Can(p); got != slices.Contains(want, p) && !(p == PermView && role != RoleDetector) {
				t.Errorf("%s.Can(%s) = %t", role, p, got)
			}
		}
	}
	if RoleDetector.Can(PermView) || !RoleDetector.Valid() || RoleDetector.Rank() >= RoleViewer.Rank() {
		t.Error("detector: valid, below viewer, sees nothing")
	}
}
```

(add `"slices"` to the imports).

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api/ -run TestBanPermissions`
Expected: FAIL, `undefined: RoleDetector`.

- [ ] **Step 3: Implement**

In `internal/api/tokens.go`, add `RoleDetector Role = "detector"` as the first role constant. Add the seven permission constants to the permission block. Then:

```go
var rolePerms = map[Role][]string{
	RoleDetector:  {PermEvidenceAdd},
	RoleViewer:    {PermView},
	RoleModerator: {PermView, PermMessage, PermBanView, PermBanInstance, PermEvidenceAdd},
	RoleOperator:  {PermView, PermMessage, PermBroadcast, PermTeleport, PermSpawn, PermRepair, PermDelete, PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermEvidenceAdd},
	RoleAdmin: {PermView, PermMessage, PermBroadcast, PermTeleport, PermSpawn, PermRepair, PermDelete, PermAuditView,
		PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermBanGlobal, PermBanRevokeAny, PermEvidenceAdd},
}
```

and `Rank`: `RoleDetector` 1, `RoleViewer` 2, `RoleModerator` 3, `RoleOperator` 4, `RoleAdmin` 5. `visible()` compares ranks only, so shifting them keeps marker visibility. A detector never reaches the marker routes, because it lacks `PermView`. In `cmd/dzo/serve.go`, change the `--role` help to `"detector, viewer, moderator, operator or admin"`.

- [ ] **Step 4: Run the API tests**

Run: `go test -race ./internal/api/ ./cmd/dzo/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/tokens.go internal/api/tokens_test.go cmd/dzo/serve.go
git commit -m "api: ban permissions and a detector role that can only file evidence

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: API ban routes

**Files:**
- Create: `internal/api/bans.go`, `internal/api/bans_test.go`
- Modify: `internal/api/server.go` (fields `Moderation`, `now`; routes)

**Interfaces:**
- Consumes: `moderation.Store`, `moderation.Ban`, `BanInput`, `BanFilter`, `InvalidError`, `ErrNotFound`, the permissions from Task 7.
- Produces:

```go
// Moderation is what the ban routes need from dzo serve.
type Moderation interface {
	Store() *moderation.Store
	// Changed syncs the instances a ban (and its previous version) applies to, and returns
	// "ok" or "pending: <why>" per instance.
	Changed(ctx context.Context, b moderation.Ban, prev *moderation.Ban) map[string]string
}
type BanResult struct { Ban moderation.Ban `json:"ban"`; Sync map[string]string `json:"sync,omitempty"` }
type RevokeBody struct { Reason string `json:"reason"` }
// Server gains: Moderation Moderation; now func() time.Time (set in New to time.Now)
```

- [ ] **Step 1: Write the failing tests**

`internal/api/bans_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const steamA = "76561198000000001"

var banNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

type fakeMod struct {
	st *moderation.Store
	mu sync.Mutex
	n  int
}

func (f *fakeMod) Store() *moderation.Store { return f.st }

func (f *fakeMod) Changed(_ context.Context, b moderation.Ban, _ *moderation.Ban) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return map[string]string{"alpha": "ok"}
}

func newBanEnv(t *testing.T) (*env, *fakeMod) {
	t.Helper()
	e := newEnv(t)
	st, err := moderation.Open(context.Background(), filepath.Join(t.TempDir(), "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := &fakeMod{st: st}
	e.srv.Moderation, e.srv.now = m, func() time.Time { return banNow }
	return e, m
}

func banBody(scope string, instances []string, dur time.Duration, ips ...string) string {
	in := moderation.BanInput{SteamID: steamA, Reason: "aimbot", Scope: moderation.Scope(scope), Instances: instances, IPs: ips}
	if dur > 0 {
		x := banNow.Add(dur)
		in.ExpiresAt = &x
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func TestBanRightsMatrix(t *testing.T) {
	e, _ := newBanEnv(t)
	day := 24 * time.Hour
	cases := []struct {
		role Role
		body string
		want int
	}{
		{RoleViewer, banBody("instances", []string{"alpha"}, day), 403},
		{RoleModerator, banBody("instances", []string{"alpha"}, day), 201},
		{RoleModerator, banBody("instances", []string{"alpha"}, 8*day), 403},
		{RoleModerator, banBody("instances", []string{"alpha"}, 0), 403},
		{RoleModerator, banBody("instances", []string{"alpha"}, day, "192.0.2.7"), 403},
		{RoleModerator, banBody("global", nil, day), 403},
		{RoleOperator, banBody("instances", []string{"alpha"}, 0, "192.0.2.7"), 201},
		{RoleOperator, banBody("global", nil, day), 403},
		{RoleOperator, banBody("instances", []string{"nosuch"}, day), 400},
		{RoleAdmin, banBody("global", nil, 0), 201},
		{RoleAdmin, `{"reason":"x","scope":"global"}`, 400},
		{RoleAdmin, `{"bogus":1}`, 400},
	}
	for i, c := range cases {
		code, b := e.do("POST", "/api/v1/bans", e.token(c.role, nil, false), c.body)
		if code != c.want {
			t.Errorf("case %d (%s): %d %s, want %d", i, c.role, code, b, c.want)
		}
	}
}

func TestBanLifecycleOverAPI(t *testing.T) {
	e, m := newBanEnv(t)
	mod := e.token(RoleModerator, nil, true)
	code, b := e.do("POST", "/api/v1/bans", mod, banBody("instances", []string{"alpha"}, time.Hour), "X-Dzo-Actor", "alice")
	if code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	var res BanResult
	_ = json.Unmarshal(b, &res)
	if res.Sync["alpha"] != "ok" || res.Ban.CreatedBy != "t-moderator:alice" || m.n != 1 {
		t.Fatalf("result %+v, changed %d", res, m.n)
	}
	id := res.Ban.ID
	if code, b := e.do("GET", fmt.Sprintf("/api/v1/bans?state=active&q=%s", steamA), mod, ""); code != 200 || !strings.Contains(string(b), `"aimbot"`) {
		t.Fatalf("list: %d %s", code, b)
	}
	in := res.Ban.Input()
	in.Reason = "aimbot (confirmed)"
	body, _ := json.Marshal(in)
	if code, b := e.do("PUT", fmt.Sprintf("/api/v1/bans/%d", id), mod, string(body), "X-Dzo-Actor", "alice"); code != 200 || !strings.Contains(string(b), "confirmed") {
		t.Fatalf("edit: %d %s", code, b)
	}
	// Another moderator may not revoke alice's ban; an admin may.
	if code, _ := e.do("POST", fmt.Sprintf("/api/v1/bans/%d/revoke", id), mod, `{"reason":"no"}`, "X-Dzo-Actor", "bob"); code != 403 {
		t.Fatalf("bob revoked alice's ban: %d", code)
	}
	if code, b := e.do("POST", fmt.Sprintf("/api/v1/bans/%d/revoke", id), e.token(RoleAdmin, nil, false), `{"reason":"appeal"}`); code != 200 {
		t.Fatalf("admin revoke: %d %s", code, b)
	}
	if code, b := e.do("GET", fmt.Sprintf("/api/v1/bans/%d/history", id), mod, ""); code != 200 || strings.Count(string(b), `"action"`) != 3 {
		t.Fatalf("history: %d %s", code, b)
	}
	if code, _ := e.do("GET", "/api/v1/bans/999", mod, ""); code != 404 {
		t.Fatalf("missing ban: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/bans/abc", mod, ""); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/bans?state=bogus", mod, ""); code != 400 {
		t.Fatalf("bad state: %d", code)
	}
}

func TestRestrictedTokenSeesOnlyItsBans(t *testing.T) {
	e, m := newBanEnv(t)
	st := m.st
	beta, _ := st.CreateBan(context.Background(), "x", moderation.BanInput{SteamID: steamA, Reason: "beta only", Scope: moderation.ScopeInstances, Instances: []string{"beta"}}, banNow)
	glob, _ := st.CreateBan(context.Background(), "x", moderation.BanInput{GUIDs: []string{"0123456789abcdef0123456789abcdef"}, Reason: "everywhere", Scope: moderation.ScopeGlobal}, banNow)
	tok := e.token(RoleAdmin, []string{"alpha"}, false)
	code, b := e.do("GET", "/api/v1/bans", tok, "")
	if code != 200 || strings.Contains(string(b), "beta only") || !strings.Contains(string(b), "everywhere") {
		t.Fatalf("list: %d %s", code, b)
	}
	if code, _ := e.do("GET", fmt.Sprintf("/api/v1/bans/%d", beta.ID), tok, ""); code != 404 {
		t.Fatalf("beta ban visible: %d", code)
	}
	if code, _ := e.do("GET", fmt.Sprintf("/api/v1/bans/%d", glob.ID), tok, ""); code != 200 {
		t.Fatalf("global ban hidden: %d", code)
	}
	if code, _ := e.do("POST", "/api/v1/bans", tok, banBody("global", nil, 0)); code != 403 {
		t.Fatalf("a restricted token made a global ban: %d", code)
	}
	if code, _ := e.do("POST", "/api/v1/bans", tok, banBody("instances", []string{"beta"}, time.Hour)); code != 400 {
		t.Fatalf("a restricted token banned on beta: %d", code)
	}
}

func TestBansWithoutDatabase(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do("GET", "/api/v1/bans", e.token(RoleAdmin, nil, false), ""); code != 503 {
		t.Fatalf("no moderation: %d", code)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api/ -run 'Ban|Restricted'`
Expected: FAIL to compile (`e.srv.Moderation undefined`).

- [ ] **Step 3: Implement**

In `internal/api/server.go`, add to `Server`:

```go
	// Moderation serves the ban and evidence routes; nil answers them 503.
	Moderation Moderation

	now func() time.Time
```

set `now: time.Now` in `New`, and add the routes at the end of `Handler()` (before `return mux`):

```go
	route("GET "+base+"/bans", PermBanView, false, s.listBans)
	route("POST "+base+"/bans", PermBanInstance, false, s.createBan)
	route("GET "+base+"/bans/{id}", PermBanView, false, s.getBan)
	route("PUT "+base+"/bans/{id}", PermBanInstance, false, s.updateBan)
	route("POST "+base+"/bans/{id}/revoke", PermBanInstance, false, s.revokeBan)
	route("GET "+base+"/bans/{id}/history", PermBanView, false, s.banHistory)
```

`internal/api/bans.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// shortBan is the longest ban a moderator may give.
const shortBan = 7 * 24 * time.Hour

// Moderation is what the ban routes need from dzo serve (internal/moderation.Service).
type Moderation interface {
	Store() *moderation.Store
	// Changed syncs the instances a ban (and its previous version) applies to and returns "ok" or
	// "pending: <why>" per instance.
	Changed(ctx context.Context, b moderation.Ban, prev *moderation.Ban) map[string]string
}

// BanResult answers a ban change: the ban and the sync state per instance.
type BanResult struct {
	Ban  moderation.Ban    `json:"ban"`
	Sync map[string]string `json:"sync,omitempty"`
}

// RevokeBody is the body of POST /bans/{id}/revoke.
type RevokeBody struct {
	Reason string `json:"reason"`
}

func (s *Server) store(w http.ResponseWriter) (*moderation.Store, bool) {
	if s.Moderation == nil {
		fail(w, http.StatusServiceUnavailable, "bans are not available: this dzo serve has no ban database")
		return nil, false
	}
	return s.Moderation.Store(), true
}

// storeFail answers an error of the store.
func storeFail(w http.ResponseWriter, err error) {
	var inv *moderation.InvalidError
	switch {
	case errors.As(err, &inv):
		fail(w, http.StatusBadRequest, "%s", inv.Msg)
	case errors.Is(err, moderation.ErrNotFound):
		fail(w, http.StatusNotFound, "not found")
	default:
		fail(w, http.StatusInternalServerError, "database error")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		fail(w, http.StatusBadRequest, "invalid id %q", r.PathValue("id"))
		return 0, false
	}
	return id, true
}

// canSee reports whether a principal may see a ban: global ones, and those on an instance it may touch.
func canSee(who Principal, b moderation.Ban) bool {
	if b.Scope == moderation.ScopeGlobal || len(who.Token.Instances) == 0 {
		return true
	}
	return slices.ContainsFunc(b.Instances, who.Allowed)
}

// banRights checks a ban's input against the caller's role and token. code is 403 for missing rights
// and 400 for instances it cannot use.
func (s *Server) banRights(c *call, in moderation.BanInput) (code int, err error) {
	role := c.who.Token.Role
	switch in.Scope {
	case moderation.ScopeGlobal:
		if !role.Can(PermBanGlobal) || len(c.who.Token.Instances) > 0 {
			return http.StatusForbidden, fmt.Errorf("global bans need the admin role and a token for every instance")
		}
	default:
		for _, i := range in.Instances {
			if _, ok := s.Hubs.Hub(i); !ok || !c.who.Allowed(i) {
				return http.StatusBadRequest, fmt.Errorf("unknown instance %q", i)
			}
		}
	}
	if (in.ExpiresAt == nil || in.ExpiresAt.Sub(s.now()) > shortBan) && !role.Can(PermBanLong) {
		return http.StatusForbidden, fmt.Errorf("bans longer than 7 days need the operator role")
	}
	if len(in.IPs) > 0 && !role.Can(PermBanIP) {
		return http.StatusForbidden, fmt.Errorf("IP bans need the operator role")
	}
	return 0, nil
}

func targetsOf(b moderation.Ban) string {
	var v []string
	for _, t := range b.Targets {
		if !t.Derived {
			v = append(v, t.Value)
		}
	}
	return strings.Join(v, ",")
}

func (s *Server) logBan(c *call, action string, b moderation.Ban, ok bool, detail string) {
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Instance: strings.Join(b.Instances, ","), Action: action,
		Target: targetsOf(b), Detail: detail, OK: ok}) // the ban itself is stored; a failed audit line must not undo it
}

func (s *Server) listBans(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	bans, err := st.ListBans(r.Context(), moderation.BanFilter{State: q.Get("state"), Instance: q.Get("instance"),
		Query: q.Get("q"), Limit: queryInt(r, "limit", 100, 1000), Now: s.now()})
	if err != nil {
		storeFail(w, err)
		return
	}
	out := []moderation.Ban{}
	for _, b := range bans {
		if canSee(c.who, b) {
			out = append(out, b)
		}
	}
	writeJSON(w, 200, out)
}

// loadBan reads the {id} ban, answering 404 for one the caller may not see.
func (s *Server) loadBan(w http.ResponseWriter, r *http.Request, c *call) (*moderation.Store, moderation.Ban, bool) {
	st, ok := s.store(w)
	if !ok {
		return nil, moderation.Ban{}, false
	}
	id, ok := pathID(w, r)
	if !ok {
		return nil, moderation.Ban{}, false
	}
	b, err := st.GetBan(r.Context(), id)
	if err == nil && !canSee(c.who, b) {
		err = moderation.ErrNotFound
	}
	if err != nil {
		storeFail(w, err)
		return nil, moderation.Ban{}, false
	}
	return st, b, true
}

func (s *Server) getBan(w http.ResponseWriter, r *http.Request, c *call) {
	if _, b, ok := s.loadBan(w, r, c); ok {
		writeJSON(w, 200, b)
	}
}

func (s *Server) banHistory(w http.ResponseWriter, r *http.Request, c *call) {
	st, b, ok := s.loadBan(w, r, c)
	if !ok {
		return
	}
	h, err := st.History(r.Context(), b.ID)
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, 200, h)
}

func (s *Server) createBan(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	var in moderation.BanInput
	if !decode(w, r, &in) {
		return
	}
	if code, err := s.banRights(c, in); err != nil {
		fail(w, code, "%v", err)
		return
	}
	b, err := st.CreateBan(r.Context(), c.actor, in, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	s.logBan(c, "ban.create", b, true, fmt.Sprintf("ban #%d %s until %s: %s", b.ID, b.Scope, moderation.FormatExpiry(b.ExpiresAt), b.Reason))
	writeJSON(w, http.StatusCreated, BanResult{Ban: b, Sync: s.Moderation.Changed(r.Context(), b, nil)})
}

// mayChange: the ban's creator (who must still be allowed to make it) or whoever may revoke any ban.
func (s *Server) mayChange(c *call, b moderation.Ban) (int, error) {
	if c.who.Token.Role.Can(PermBanRevokeAny) {
		return 0, nil
	}
	if b.CreatedBy != c.actor {
		return http.StatusForbidden, fmt.Errorf("only %s or an admin may change ban %d", b.CreatedBy, b.ID)
	}
	return s.banRights(c, b.Input())
}

func (s *Server) updateBan(w http.ResponseWriter, r *http.Request, c *call) {
	st, old, ok := s.loadBan(w, r, c)
	if !ok {
		return
	}
	var in moderation.BanInput
	if !decode(w, r, &in) {
		return
	}
	if code, err := s.mayChange(c, old); err != nil {
		fail(w, code, "%v", err)
		return
	}
	if code, err := s.banRights(c, in); err != nil {
		fail(w, code, "%v", err)
		return
	}
	b, err := st.UpdateBan(r.Context(), old.ID, c.actor, in, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	s.logBan(c, "ban.edit", b, true, fmt.Sprintf("ban #%d", b.ID))
	writeJSON(w, 200, BanResult{Ban: b, Sync: s.Moderation.Changed(r.Context(), b, &old)})
}

func (s *Server) revokeBan(w http.ResponseWriter, r *http.Request, c *call) {
	st, old, ok := s.loadBan(w, r, c)
	if !ok {
		return
	}
	var body RevokeBody
	if !decode(w, r, &body) {
		return
	}
	if code, err := s.mayChange(c, old); err != nil {
		fail(w, code, "%v", err)
		return
	}
	b, err := st.RevokeBan(r.Context(), old.ID, c.actor, body.Reason, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	s.logBan(c, "ban.revoke", b, true, fmt.Sprintf("ban #%d: %s", b.ID, body.Reason))
	writeJSON(w, 200, BanResult{Ban: b, Sync: s.Moderation.Changed(r.Context(), b, &old)})
}
```

`banRights` for scope `instances` with zero instances: the store refuses it as 400. A restricted token asking for a beta ban gets 400 "unknown instance", the same answer as for a non-existent instance, so a restricted token cannot probe instance names.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "api: ban routes with role and scope checks, history and audit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: API evidence and identities routes

**Files:**
- Create: `internal/api/evidence.go`, `internal/api/evidence_test.go`
- Modify: `internal/api/server.go` (routes)

**Interfaces:**
- Consumes: `Store.AddEvidence`, `GetEvidence`, `EvidenceFor`, `EvidenceFile`, `Identities` (Task 5), `newBanEnv` (Task 8 test).
- Produces the routes: `GET /api/v1/evidence?steam_id=&guid=&ip=`, `POST /api/v1/evidence` (JSON `moderation.EvidenceInput`, or multipart with the same field names plus `file`), `GET /api/v1/evidence/{id}`, `GET /api/v1/evidence/{id}/file`, `GET /api/v1/players/{steamid}/identities`.

- [ ] **Step 1: Write the failing tests**

`internal/api/evidence_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

var testPNG = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)

func (e *env) upload(token string, fields map[string]string, file []byte) (int, []byte) {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "shot.png")
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/evidence", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestDetectorFilesLogEvidence(t *testing.T) {
	e, _ := newBanEnv(t)
	det := e.token(RoleDetector, []string{"alpha"}, false)
	code, b := e.do("POST", "/api/v1/evidence", det, `{"steam_id":"`+steamA+`","instance":"alpha","kind":"log","body":"teleported 3 km"}`)
	if code != 201 {
		t.Fatalf("add: %d %s", code, b)
	}
	if code, _ := e.do("POST", "/api/v1/evidence", det, `{"steam_id":"`+steamA+`","instance":"beta","kind":"log","body":"x"}`); code != 400 {
		t.Fatalf("detector filed for an instance it may not touch: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/evidence?steam_id="+steamA, det, ""); code != 403 {
		t.Fatalf("detector read evidence: %d", code)
	}
	mod := e.token(RoleModerator, nil, false)
	code, b = e.do("GET", "/api/v1/evidence?steam_id="+steamA, mod, "")
	if code != 200 || !strings.Contains(string(b), "teleported 3 km") {
		t.Fatalf("list: %d %s", code, b)
	}
	if code, _ := e.do("GET", "/api/v1/evidence", mod, ""); code != 400 {
		t.Fatalf("list without a player: %d", code)
	}
}

func TestEvidenceFileHeaders(t *testing.T) {
	e, _ := newBanEnv(t)
	mod := e.token(RoleModerator, nil, false)
	code, b := e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha", "body": "screenshot"}, testPNG)
	if code != 201 {
		t.Fatalf("upload: %d %s", code, b)
	}
	var ev moderation.Evidence
	_ = json.Unmarshal(b, &ev)
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/evidence/%d/file", e.ts.URL, ev.ID), nil)
	req.Header.Set("Authorization", "Bearer "+mod)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	h := resp.Header
	if resp.StatusCode != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || !bytes.Equal(got, testPNG) {
		t.Fatalf("file: %d %v", resp.StatusCode, h)
	}
	if code, b := e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha"}, []byte("<html><script>x</script>")); code != 400 {
		t.Fatalf("html accepted as an image: %d %s", code, b)
	}
	if code, _ := e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha"}, append(testPNG, make([]byte, moderation.MaxImage)...)); code != 413 && code != 400 {
		t.Fatalf("oversized image: %d", code)
	}
	if code, _ := e.do("GET", fmt.Sprintf("/api/v1/evidence/%d/file", ev.ID+100), mod, ""); code != 404 {
		t.Fatalf("missing file: %d", code)
	}
}

func TestIdentitiesRoute(t *testing.T) {
	e, m := newBanEnv(t)
	g, _ := moderation.DeriveGUID(steamA)
	_ = m.st.Seen(context.Background(), "alpha", g, "192.0.2.7", "alice", banNow)
	code, b := e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleModerator, nil, false), "")
	if code != 200 || !strings.Contains(string(b), `"192.0.2.7"`) || !strings.Contains(string(b), `"shared_with":0`) {
		t.Fatalf("identities: %d %s", code, b)
	}
	if code, _ := e.do("GET", "/api/v1/players/123/identities", e.token(RoleModerator, nil, false), ""); code != 400 {
		t.Fatalf("bad steam id: %d", code)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api/ -run 'Evidence|Identities'`
Expected: FAIL, 404 or 405 answers (no routes).

- [ ] **Step 3: Implement**

Routes in `Handler()`:

```go
	route("GET "+base+"/evidence", PermBanView, false, s.listEvidence)
	route("POST "+base+"/evidence", PermEvidenceAdd, false, s.addEvidence)
	route("GET "+base+"/evidence/{id}", PermBanView, false, s.getEvidence)
	route("GET "+base+"/evidence/{id}/file", PermBanView, false, s.evidenceFile)
	route("GET "+base+"/players/{steamid}/identities", PermBanView, false, s.identities)
```

`internal/api/evidence.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// evidenceJSONMax bounds a JSON evidence body: the text plus room for the other fields.
const evidenceJSONMax = moderation.MaxEvidenceBody + 4<<10

func (s *Server) listEvidence(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	list, err := st.EvidenceFor(r.Context(), moderation.Who{SteamID: q.Get("steam_id"), GUID: q.Get("guid"), IP: q.Get("ip")})
	if err != nil {
		storeFail(w, err)
		return
	}
	out := []moderation.Evidence{}
	for _, e := range list {
		if c.who.Allowed(e.Instance) {
			out = append(out, e)
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) addEvidence(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	var in moderation.EvidenceInput
	var image []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, moderation.MaxImage+evidenceJSONMax)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				fail(w, http.StatusRequestEntityTooLarge, "the upload is larger than %d MiB", moderation.MaxImage>>20)
				return
			}
			fail(w, http.StatusBadRequest, "bad upload: %v", err)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		in = moderation.EvidenceInput{SteamID: r.FormValue("steam_id"), GUID: r.FormValue("guid"), IP: r.FormValue("ip"),
			Instance: r.FormValue("instance"), Kind: moderation.EvidenceKind(r.FormValue("kind")), Body: r.FormValue("body")}
		f, _, err := r.FormFile("file")
		if err != nil {
			fail(w, http.StatusBadRequest, "the upload has no file")
			return
		}
		defer func() { _ = f.Close() }()
		if image, err = io.ReadAll(io.LimitReader(f, moderation.MaxImage+1)); err != nil {
			fail(w, http.StatusBadRequest, "reading the upload: %v", err)
			return
		}
	} else {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, evidenceJSONMax))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			fail(w, http.StatusBadRequest, "bad request body: %v", err)
			return
		}
	}
	if _, ok := s.Hubs.Hub(in.Instance); !ok || !c.who.Allowed(in.Instance) {
		fail(w, http.StatusBadRequest, "unknown instance %q", in.Instance)
		return
	}
	e, err := st.AddEvidence(r.Context(), c.actor, in, image, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Instance: e.Instance, Action: "evidence.add",
		Target: firstNonEmpty(e.SteamID, e.GUID, e.IP), Detail: fmt.Sprintf("evidence #%d %s", e.ID, e.Kind), OK: true}) // stored either way
	writeJSON(w, http.StatusCreated, e)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// loadEvidence reads the {id} item, answering 404 for one from an instance the caller may not touch.
func (s *Server) loadEvidence(w http.ResponseWriter, r *http.Request, c *call) (*moderation.Store, moderation.Evidence, bool) {
	st, ok := s.store(w)
	if !ok {
		return nil, moderation.Evidence{}, false
	}
	id, ok := pathID(w, r)
	if !ok {
		return nil, moderation.Evidence{}, false
	}
	e, err := st.GetEvidence(r.Context(), id)
	if err == nil && !c.who.Allowed(e.Instance) {
		err = moderation.ErrNotFound
	}
	if err != nil {
		storeFail(w, err)
		return nil, moderation.Evidence{}, false
	}
	return st, e, true
}

func (s *Server) getEvidence(w http.ResponseWriter, r *http.Request, c *call) {
	if _, e, ok := s.loadEvidence(w, r, c); ok {
		writeJSON(w, 200, e)
	}
}

// evidenceFile serves an image with its stored type, never sniffed, in a sandbox.
func (s *Server) evidenceFile(w http.ResponseWriter, r *http.Request, c *call) {
	st, e, ok := s.loadEvidence(w, r, c)
	if !ok {
		return
	}
	path, ctype, err := st.EvidenceFile(r.Context(), e.ID)
	if err != nil {
		storeFail(w, err)
		return
	}
	f, err := os.Open(path) //nolint:gosec // a path the store built from a hash
	if err != nil {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f) // the client went away, or the disk failed mid-file: nothing left to answer
}

func (s *Server) identities(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	list, err := st.Identities(r.Context(), r.PathValue("steamid"))
	if err != nil {
		storeFail(w, err)
		return
	}
	out := []moderation.Sighting{}
	for _, x := range list {
		if c.who.Allowed(x.Instance) {
			out = append(out, x)
		}
	}
	writeJSON(w, 200, out)
}
```

If `firstNonEmpty` already exists in package `api`, reuse it instead of declaring it again. It exists in `admin`, not in `api`. Check with `grep -n "func firstNonEmpty" internal/api`.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "api: evidence (log lines, notes, images) and a player's GUID/IP sightings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: apiclient and the `dzo ban` / `dzo evidence` commands

**Files:**
- Modify: `internal/apiclient/client.go` (split `req` into `newReq`)
- Create: `internal/apiclient/bans.go`, `internal/apiclient/bans_test.go`, `cmd/dzo/ban.go`, `cmd/dzo/ban_test.go`
- Modify: `cmd/dzo/root.go` (register `newBanCmd()`, `newEvidenceCmd()`)

**Interfaces:**
- Consumes: the API routes of Tasks 8–9, `api.BanResult`, `api.RevokeBody`, `moderation.*`.
- Produces (apiclient):

```go
type BanFilter struct { State, Instance, Query string; Limit int }
func (c *Client) Bans(ctx context.Context, f BanFilter) ([]moderation.Ban, error)
func (c *Client) Ban(ctx context.Context, id int64) (moderation.Ban, error)
func (c *Client) BanHistory(ctx context.Context, id int64) ([]moderation.HistoryEntry, error)
func (c *Client) CreateBan(ctx context.Context, in moderation.BanInput) (api.BanResult, error)
func (c *Client) UpdateBan(ctx context.Context, id int64, in moderation.BanInput) (api.BanResult, error)
func (c *Client) RevokeBan(ctx context.Context, id int64, reason string) (api.BanResult, error)
func (c *Client) Evidence(ctx context.Context, who moderation.Who) ([]moderation.Evidence, error)
func (c *Client) EvidenceItem(ctx context.Context, id int64) (moderation.Evidence, error)
func (c *Client) AddEvidence(ctx context.Context, in moderation.EvidenceInput) (moderation.Evidence, error)
func (c *Client) AddEvidenceImage(ctx context.Context, in moderation.EvidenceInput, image []byte) (moderation.Evidence, error)
func (c *Client) EvidenceFile(ctx context.Context, id int64) (*http.Response, error) // caller closes Body
func (c *Client) Identities(ctx context.Context, steamID string) ([]moderation.Sighting, error)
```

- [ ] **Step 1: Write the failing apiclient test**

`internal/apiclient/bans_test.go` sets up an `api.Server` with a moderation store and a fake `Moderation`, the same way `bans_test.go` in `internal/api` does. Look at `internal/apiclient/client_test.go` for its existing server helper and reuse it, then add:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type okMod struct{ st *moderation.Store }

func (m okMod) Store() *moderation.Store { return m.st }
func (m okMod) Changed(context.Context, moderation.Ban, *moderation.Ban) map[string]string {
	return map[string]string{"alpha": "ok"}
}

func banClient(t *testing.T) *Client {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(context.Background(), filepath.Join(dir, "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv.Moderation = okMod{st}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	secret, _, _ := srv.Tokens.Create("admin", api.RoleAdmin, nil, true, time.Hour)
	return New(ts.URL, secret).WithActor("alice", "test")
}

func TestBanClientRoundTrip(t *testing.T) {
	c, ctx := banClient(t), context.Background()
	res, err := c.CreateBan(ctx, moderation.BanInput{SteamID: "76561198000000001", Reason: "aimbot", Scope: moderation.ScopeGlobal})
	if err != nil || res.Sync["alpha"] != "ok" {
		t.Fatalf("CreateBan = %+v, %v", res, err)
	}
	in := res.Ban.Input()
	in.Reason = "aimbot!"
	if r2, err := c.UpdateBan(ctx, res.Ban.ID, in); err != nil || r2.Ban.Reason != "aimbot!" {
		t.Fatalf("UpdateBan = %+v, %v", r2, err)
	}
	if bs, err := c.Bans(ctx, BanFilter{State: "active", Query: "76561198000000001"}); err != nil || len(bs) != 1 {
		t.Fatalf("Bans = %+v, %v", bs, err)
	}
	if _, err := c.RevokeBan(ctx, res.Ban.ID, "appeal"); err != nil {
		t.Fatal(err)
	}
	if h, err := c.BanHistory(ctx, res.Ban.ID); err != nil || len(h) != 3 {
		t.Fatalf("BanHistory = %+v, %v", h, err)
	}
	if _, err := c.Ban(ctx, 999); err == nil {
		t.Fatal("Ban(999) found")
	}
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	ev, err := c.AddEvidenceImage(ctx, moderation.EvidenceInput{SteamID: "76561198000000001", Instance: "alpha", Body: "shot"}, png)
	if err != nil || ev.Kind != moderation.EvidenceImage {
		t.Fatalf("AddEvidenceImage = %+v, %v", ev, err)
	}
	if _, err := c.AddEvidence(ctx, moderation.EvidenceInput{SteamID: "76561198000000001", Instance: "alpha", Kind: moderation.EvidenceLog, Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if list, err := c.Evidence(ctx, moderation.Who{SteamID: "76561198000000001"}); err != nil || len(list) != 2 {
		t.Fatalf("Evidence = %+v, %v", list, err)
	}
	resp, err := c.EvidenceFile(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Header.Get("Content-Type") != "image/png" || len(b) != len(png) {
		t.Fatalf("EvidenceFile: %v %d", resp.Header, len(b))
	}
	if _, err := c.Identities(ctx, "76561198000000001"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/apiclient/ -run TestBanClientRoundTrip`
Expected: FAIL, `c.CreateBan undefined`.

- [ ] **Step 3: Implement the client**

In `client.go`, replace `req` with a general `newReq`, and keep `req` as the JSON wrapper:

```go
func (c *Client) newReq(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if c.Actor != "" {
		r.Header.Set("X-Dzo-Actor", c.Actor)
	}
	if c.Source != "" {
		r.Header.Set("X-Dzo-Source", c.Source)
	}
	return r, nil
}

func (c *Client) req(ctx context.Context, method, path string, body any) (*http.Request, error) {
	if body == nil {
		return c.newReq(ctx, method, path, nil, "")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.newReq(ctx, method, path, bytes.NewReader(b), "application/json")
}
```

`internal/apiclient/bans.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// BanFilter selects bans; empty fields do not filter.
type BanFilter struct {
	State, Instance, Query string
	Limit                  int
}

// send runs a JSON request and decodes the answer when its status is want.
func (c *Client) send(ctx context.Context, method, path string, body any, want int, out any) error {
	status, b, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status != want {
		return apiErr(status, b)
	}
	return json.Unmarshal(b, out)
}

func banPath(id int64) string { return "/api/v1/bans/" + strconv.FormatInt(id, 10) }

// Bans lists bans, newest first.
func (c *Client) Bans(ctx context.Context, f BanFilter) (out []moderation.Ban, err error) {
	q := url.Values{}
	for k, v := range map[string]string{"state": f.State, "instance": f.Instance, "q": f.Query} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return out, c.get(ctx, "/api/v1/bans?"+q.Encode(), &out)
}

// Ban returns one ban.
func (c *Client) Ban(ctx context.Context, id int64) (out moderation.Ban, err error) {
	return out, c.get(ctx, banPath(id), &out)
}

// BanHistory returns the changes of a ban.
func (c *Client) BanHistory(ctx context.Context, id int64) (out []moderation.HistoryEntry, err error) {
	return out, c.get(ctx, banPath(id)+"/history", &out)
}

// CreateBan makes a ban.
func (c *Client) CreateBan(ctx context.Context, in moderation.BanInput) (out api.BanResult, err error) {
	return out, c.send(ctx, http.MethodPost, "/api/v1/bans", in, http.StatusCreated, &out)
}

// UpdateBan replaces a ban's editable fields.
func (c *Client) UpdateBan(ctx context.Context, id int64, in moderation.BanInput) (out api.BanResult, err error) {
	return out, c.send(ctx, http.MethodPut, banPath(id), in, http.StatusOK, &out)
}

// RevokeBan ends a ban.
func (c *Client) RevokeBan(ctx context.Context, id int64, reason string) (out api.BanResult, err error) {
	return out, c.send(ctx, http.MethodPost, banPath(id)+"/revoke", api.RevokeBody{Reason: reason}, http.StatusOK, &out)
}

// Evidence lists the evidence filed against a player.
func (c *Client) Evidence(ctx context.Context, who moderation.Who) (out []moderation.Evidence, err error) {
	q := url.Values{}
	for k, v := range map[string]string{"steam_id": who.SteamID, "guid": who.GUID, "ip": who.IP} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return out, c.get(ctx, "/api/v1/evidence?"+q.Encode(), &out)
}

// EvidenceItem returns one evidence item.
func (c *Client) EvidenceItem(ctx context.Context, id int64) (out moderation.Evidence, err error) {
	return out, c.get(ctx, "/api/v1/evidence/"+strconv.FormatInt(id, 10), &out)
}

// AddEvidence files log lines or a note.
func (c *Client) AddEvidence(ctx context.Context, in moderation.EvidenceInput) (out moderation.Evidence, err error) {
	return out, c.send(ctx, http.MethodPost, "/api/v1/evidence", in, http.StatusCreated, &out)
}

// AddEvidenceImage uploads a screenshot with a caption (in.Body).
func (c *Client) AddEvidenceImage(ctx context.Context, in moderation.EvidenceInput, image []byte) (out moderation.Evidence, err error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{"steam_id": in.SteamID, "guid": in.GUID, "ip": in.IP, "instance": in.Instance, "body": in.Body} {
		if err := mw.WriteField(k, v); err != nil {
			return out, err
		}
	}
	fw, err := mw.CreateFormFile("file", "evidence")
	if err != nil {
		return out, err
	}
	if _, err := fw.Write(image); err != nil {
		return out, err
	}
	if err := mw.Close(); err != nil {
		return out, err
	}
	r, err := c.newReq(ctx, http.MethodPost, "/api/v1/evidence", &body, mw.FormDataContentType())
	if err != nil {
		return out, err
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return out, err
	}
	if resp.StatusCode != http.StatusCreated {
		return out, apiErr(resp.StatusCode, b)
	}
	return out, json.Unmarshal(b, &out)
}

// EvidenceFile fetches an image; the caller closes the body.
func (c *Client) EvidenceFile(ctx context.Context, id int64) (*http.Response, error) {
	r, err := c.req(ctx, http.MethodGet, fmt.Sprintf("/api/v1/evidence/%d/file", id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, apiErr(resp.StatusCode, b)
	}
	return resp, nil
}

// Identities returns where a Steam ID's GUID was seen, with how many others share each IP.
func (c *Client) Identities(ctx context.Context, steamID string) (out []moderation.Sighting, err error) {
	return out, c.get(ctx, "/api/v1/players/"+url.PathEscape(steamID)+"/identities", &out)
}
```

Run: `go test -race ./internal/apiclient/`
Expected: PASS.

- [ ] **Step 4: Write the failing CLI test**

`cmd/dzo/ban_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type cliMod struct{ st *moderation.Store }

func (m cliMod) Store() *moderation.Store { return m.st }
func (m cliMod) Changed(context.Context, moderation.Ban, *moderation.Ban) map[string]string {
	return map[string]string{"alpha": "ok"}
}

func TestBanCommands(t *testing.T) {
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(context.Background(), filepath.Join(dir, "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	srv.Moderation = cliMod{st}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	secret, _, _ := srv.Tokens.Create("cli", api.RoleAdmin, nil, true, time.Hour)
	tokenFile := filepath.Join(dir, "token")
	writeFile(t, tokenFile, secret+"\n")
	common := []string{"--api", ts.URL, "--token-file", tokenFile}
	run := func(args ...string) string {
		t.Helper()
		out, err := runCmd(t, append(args, common...)...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	out := run("ban", "add", "--steam", "76561198000000001", "--reason", "aimbot", "--duration", "7d", "--instance", "alpha")
	if !strings.Contains(out, "ban #1") || !strings.Contains(out, "alpha: ok") {
		t.Fatalf("add: %s", out)
	}
	if out := run("ban", "list", "--state", "active"); !strings.Contains(out, "aimbot") || !strings.Contains(out, "76561198000000001") {
		t.Fatalf("list: %s", out)
	}
	run("ban", "edit", "1", "--reason", "aimbot, confirmed", "--add-ip", "192.0.2.7")
	out = run("ban", "show", "1")
	if !strings.Contains(out, "aimbot, confirmed") || !strings.Contains(out, "192.0.2.7") || !strings.Contains(out, "edit") {
		t.Fatalf("show: %s", out)
	}
	run("evidence", "add", "--steam", "76561198000000001", "--instance", "alpha", "--log", "speed 900 km/h")
	if out := run("evidence", "list", "--steam", "76561198000000001"); !strings.Contains(out, "speed 900 km/h") {
		t.Fatalf("evidence list: %s", out)
	}
	run("ban", "revoke", "1", "--reason", "appeal")
	if out := run("ban", "list", "--state", "revoked"); !strings.Contains(out, "revoked") {
		t.Fatalf("revoked list: %s", out)
	}
	if out, err := runCmd(t, append([]string{"ban", "add", "--steam", "76561198000000001", "--reason", "x"}, common...)...); err == nil {
		t.Fatalf("add without --duration succeeded: %s", out)
	}
}
```

Run: `go test ./cmd/dzo/ -run TestBanCommands`
Expected: FAIL (`unknown command "ban"`).

- [ ] **Step 5: Implement the commands**

`cmd/dzo/ban.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// banFlags are the fields shared by `ban add` and `ban edit`.
type banFlags struct {
	steam, reason, message, duration string
	guids, ips, instances            []string
	global                           bool
	evidence                         []int64
}

func (f *banFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.steam, "steam", "", "Steam64 ID (its BattlEye GUID is banned too)")
	cmd.Flags().StringSliceVar(&f.guids, "guid", nil, "BattlEye GUID (repeatable)")
	cmd.Flags().StringVar(&f.reason, "reason", "", "why (kept for admins)")
	cmd.Flags().StringVar(&f.message, "message", "", "what the player is shown (default: "+moderation.DefaultMessage+")")
	cmd.Flags().StringVar(&f.duration, "duration", "", "30m, 12h, 7d, 2w or permanent")
	cmd.Flags().StringSliceVar(&f.instances, "instance", nil, "instance the ban applies to (repeatable)")
	cmd.Flags().BoolVar(&f.global, "global", false, "apply to every instance of the installation")
	cmd.Flags().Int64SliceVar(&f.evidence, "evidence", nil, "evidence id to link (repeatable)")
}

func (f *banFlags) scope(in *moderation.BanInput) error {
	switch {
	case f.global && len(f.instances) > 0:
		return fmt.Errorf("give --global or --instance, not both")
	case f.global:
		in.Scope, in.Instances = moderation.ScopeGlobal, nil
	case len(f.instances) > 0:
		in.Scope, in.Instances = moderation.ScopeInstances, f.instances
	}
	return nil
}

func printSync(w io.Writer, sync map[string]string) {
	names := make([]string, 0, len(sync))
	for n := range sync {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		_, _ = fmt.Fprintf(w, "  %s: %s\n", n, sync[n])
	}
}

func targetList(b moderation.Ban) string {
	var v []string
	for _, t := range b.Targets {
		if !t.Derived {
			v = append(v, t.Value)
		}
	}
	return strings.Join(v, ",")
}

func scopeText(b moderation.Ban) string {
	if b.Scope == moderation.ScopeGlobal {
		return "global"
	}
	return strings.Join(b.Instances, ",")
}

func printBanResult(cmd *cobra.Command, verb string, r api.BanResult) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ban #%d %s\n", r.Ban.ID, verb)
	printSync(cmd.OutOrStdout(), r.Sync)
}

func idArg(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid ban id %q", s)
	}
	return id, nil
}

func newBanCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "ban", Short: "Ban players, list, edit and revoke bans (needs `dzo serve`)"}
	t.flags(cmd)

	var add banFlags
	addCmd := &cobra.Command{
		Use: "add", Short: "Ban a player by Steam ID, GUID or IP", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			if add.duration == "" {
				return fmt.Errorf("give --duration (e.g. 7d or permanent)")
			}
			exp, err := moderation.ExpiryFrom(add.duration, time.Now())
			if err != nil {
				return err
			}
			in := moderation.BanInput{SteamID: add.steam, GUIDs: add.guids, IPs: add.ips, Reason: add.reason, Message: add.message, ExpiresAt: exp, Evidence: add.evidence}
			if err := add.scope(&in); err != nil {
				return err
			}
			r, err := c.CreateBan(ctxOf(cmd), in)
			if err != nil {
				return err
			}
			printBanResult(cmd, "created", r)
			return nil
		},
	}
	add.register(addCmd)
	addCmd.Flags().StringSliceVar(&add.ips, "ip", nil, "IPv4 address (repeatable; mind shared addresses behind carrier NAT)")

	var f apiclient.BanFilter
	listCmd := &cobra.Command{
		Use: "list", Short: "List bans, newest first", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			bans, err := c.Bans(ctxOf(cmd), f)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tSTATE\tEXPIRES\tSCOPE\tTARGETS\tREASON")
			now := time.Now()
			for _, b := range bans {
				_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", b.ID, b.State(now), moderation.FormatExpiry(b.ExpiresAt), scopeText(b), targetList(b), b.Reason)
			}
			return tw.Flush()
		},
	}
	listCmd.Flags().StringVar(&f.State, "state", "", "active, expired or revoked")
	listCmd.Flags().StringVar(&f.Instance, "instance", "", "bans that apply to this instance")
	listCmd.Flags().StringVar(&f.Query, "query", "", "Steam ID, GUID, IP, or text in the reason or a player name")
	listCmd.Flags().IntVar(&f.Limit, "limit", 100, "at most this many")

	showCmd := &cobra.Command{
		Use: "show <id>", Short: "Show a ban with its history", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			id, err := idArg(args[0])
			if err != nil {
				return err
			}
			b, err := c.Ban(ctxOf(cmd), id)
			if err != nil {
				return err
			}
			h, err := c.BanHistory(ctxOf(cmd), id)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(w, "ban #%d (%s)\nreason:   %s\nmessage:  %s\nexpires:  %s\nscope:    %s\ncreated:  %s by %s\n",
				b.ID, b.State(time.Now()), b.Reason, b.Message, moderation.FormatExpiry(b.ExpiresAt), scopeText(b), b.CreatedAt.Format(time.RFC3339), b.CreatedBy)
			for _, tg := range b.Targets {
				d := ""
				if tg.Derived {
					d = " (from the Steam ID)"
				}
				_, _ = fmt.Fprintf(w, "target:   %s %s%s\n", tg.Kind, tg.Value, d)
			}
			for _, e := range b.Evidence {
				_, _ = fmt.Fprintf(w, "evidence: #%d\n", e)
			}
			if b.RevokedAt != nil {
				_, _ = fmt.Fprintf(w, "revoked:  %s by %s: %s\n", b.RevokedAt.Format(time.RFC3339), b.RevokedBy, b.RevokeReason)
			}
			for _, e := range h {
				_, _ = fmt.Fprintf(w, "history:  %s %s %s\n", e.At.Format(time.RFC3339), e.Actor, e.Action)
			}
			return nil
		},
	}

	var ed banFlags
	var addIPs, removeIPs []string
	editCmd := &cobra.Command{
		Use: "edit <id>", Short: "Change a ban (only the given fields)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			id, err := idArg(args[0])
			if err != nil {
				return err
			}
			b, err := c.Ban(ctxOf(cmd), id)
			if err != nil {
				return err
			}
			in := b.Input()
			fl := cmd.Flags()
			if fl.Changed("steam") {
				in.SteamID = ed.steam
			}
			if fl.Changed("guid") {
				in.GUIDs = ed.guids
			}
			if fl.Changed("reason") {
				in.Reason = ed.reason
			}
			if fl.Changed("message") {
				in.Message = ed.message
			}
			if fl.Changed("duration") {
				if in.ExpiresAt, err = moderation.ExpiryFrom(ed.duration, time.Now()); err != nil {
					return err
				}
			}
			if fl.Changed("evidence") {
				in.Evidence = ed.evidence
			}
			if err := ed.scope(&in); err != nil {
				return err
			}
			in.IPs = slices.DeleteFunc(append(in.IPs, addIPs...), func(ip string) bool { return slices.Contains(removeIPs, ip) })
			r, err := c.UpdateBan(ctxOf(cmd), id, in)
			if err != nil {
				return err
			}
			printBanResult(cmd, "changed", r)
			return nil
		},
	}
	ed.register(editCmd)
	editCmd.Flags().StringSliceVar(&addIPs, "add-ip", nil, "add an IPv4 address")
	editCmd.Flags().StringSliceVar(&removeIPs, "remove-ip", nil, "remove an IPv4 address")

	var why string
	revokeCmd := &cobra.Command{
		Use: "revoke <id>", Short: "Unban: end a ban now (it stays in the history)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			id, err := idArg(args[0])
			if err != nil {
				return err
			}
			r, err := c.RevokeBan(ctxOf(cmd), id, why)
			if err != nil {
				return err
			}
			printBanResult(cmd, "revoked", r)
			return nil
		},
	}
	revokeCmd.Flags().StringVar(&why, "reason", "", "why the ban ends (required)")

	cmd.AddCommand(addCmd, listCmd, showCmd, editCmd, revokeCmd)
	return cmd
}

func newEvidenceCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "evidence", Short: "File and list evidence against players (needs `dzo serve`)"}
	t.flags(cmd)
	var who moderation.Who
	whoFlags := func(c *cobra.Command) {
		c.Flags().StringVar(&who.SteamID, "steam", "", "Steam64 ID")
		c.Flags().StringVar(&who.GUID, "guid", "", "BattlEye GUID")
		c.Flags().StringVar(&who.IP, "ip", "", "IPv4 address")
	}

	var instance, logText, note, file, image, caption string
	addCmd := &cobra.Command{
		Use: "add", Short: "File log lines (--log, or --file with - for stdin), a note or an image", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			in := moderation.EvidenceInput{SteamID: who.SteamID, GUID: who.GUID, IP: who.IP, Instance: instance}
			var ev moderation.Evidence
			switch {
			case image != "":
				data, err := os.ReadFile(image) //nolint:gosec // a file the operator names
				if err != nil {
					return err
				}
				in.Body = caption
				ev, err = c.AddEvidenceImage(ctxOf(cmd), in, data)
				if err != nil {
					return err
				}
			default:
				switch {
				case logText != "":
					in.Kind, in.Body = moderation.EvidenceLog, logText
				case note != "":
					in.Kind, in.Body = moderation.EvidenceNote, note
				case file != "":
					var data []byte
					if file == "-" {
						data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), moderation.MaxEvidenceBody+1))
					} else {
						data, err = os.ReadFile(file) //nolint:gosec // a file the operator names
					}
					if err != nil {
						return err
					}
					in.Kind, in.Body = moderation.EvidenceLog, string(data)
				default:
					return fmt.Errorf("give --log, --note, --file or --image")
				}
				if ev, err = c.AddEvidence(ctxOf(cmd), in); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "evidence #%d filed\n", ev.ID)
			return nil
		},
	}
	whoFlags(addCmd)
	addCmd.Flags().StringVar(&instance, "instance", "", "instance the evidence is from (required)")
	addCmd.Flags().StringVar(&logText, "log", "", "log lines")
	addCmd.Flags().StringVar(&note, "note", "", "a note")
	addCmd.Flags().StringVar(&file, "file", "", "file with log lines, - for stdin")
	addCmd.Flags().StringVar(&image, "image", "", "PNG, JPEG or WebP screenshot")
	addCmd.Flags().StringVar(&caption, "caption", "", "caption of the image")

	listCmd := &cobra.Command{
		Use: "list", Short: "List the evidence filed against a player", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			list, err := c.Evidence(ctxOf(cmd), who)
			if err != nil {
				return err
			}
			for _, e := range list {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "#%d %s %s %s by %s\n", e.ID, e.CreatedAt.Format(time.RFC3339), e.Instance, e.Kind, e.Source)
				if e.Body != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", strings.ReplaceAll(e.Body, "\n", "\n    "))
				}
			}
			return nil
		},
	}
	whoFlags(listCmd)
	cmd.AddCommand(addCmd, listCmd)
	return cmd
}
```

`printBanResult` prints "ban #1 created", and the test looks for "ban #1". The list's STATE column says "revoked" after revoking, which the last test check relies on.

Register both in `cmd/dzo/root.go` beside `newPlayerCmd(), newVehicleCmd(),`: `newBanCmd(), newEvidenceCmd(),`.

- [ ] **Step 6: Run the tests**

Run: `go test -race ./internal/apiclient/ ./cmd/dzo/`
Expected: PASS. `cmd/dzo/man_test.go` may compare the command list with the docs. If it fails, it names what to add (Task 17 writes the CLI docs; add placeholder-free entries now if the test demands them).

- [ ] **Step 7: Commit**

```bash
git add internal/apiclient cmd/dzo
git commit -m "cli: dzo ban add|list|show|edit|revoke and dzo evidence add|list

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: BattlEye `players` parser and the moderation Service (sync and sweep)

**Files:**
- Create: `internal/battleye/players.go`, `internal/battleye/players_test.go`, `internal/moderation/service.go`, `internal/moderation/service_test.go`

**Interfaces:**
- Consumes: `testdata/v2/players.out`, `Store`, `Entries`, `FormatBansTxt`, `WriteBansFile`, `ChangedOutside`, `ParseBansTxt`, `ImportEntries`, `admin.Audit`.
- Produces:

```go
// battleye
type PlayerInfo struct { ID int; IP string; Port int; Ping int; GUID string; Verified bool; Name string }
func ParsePlayers(raw string) []PlayerInfo
// moderation
type Instance struct { Name, BEDir, RConAddr, RConPass string }
type RCon interface { Command(ctx context.Context, cmd string) (string, error); Events() <-chan battleye.Event; Close() error }
type Service struct { Dial func(Instance) RCon; Tick <-chan time.Time; Now func() time.Time /* … */ }
func NewService(store *Store, audit *admin.Audit, logf func(string, ...any)) *Service
func (s *Service) Store() *Store
func (s *Service) Start(ctx context.Context)
func (s *Service) SetInstances(insts []Instance)
func (s *Service) Changed(ctx context.Context, b Ban, prev *Ban) map[string]string
func (s *Service) Pending() map[string]bool
func (s *Service) Wait()
```

- [ ] **Step 1: The players parser, test first**

`internal/battleye/players_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"os"
	"strings"
	"testing"
)

func TestParsePlayersOfTheLiveCapture(t *testing.T) {
	b, err := os.ReadFile("../moderation/testdata/v2/players.out")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := os.ReadFile("../moderation/testdata/v2/pair.txt")
	if err != nil {
		t.Fatal(err)
	}
	guid := strings.Fields(string(pair))[1]
	ps := ParsePlayers(string(b))
	if len(ps) != 1 || ps[0].Name != "dzotester" || ps[0].GUID != guid || ps[0].IP == "" || ps[0].Port == 0 {
		t.Fatalf("ParsePlayers = %+v", ps)
	}
}

func TestParsePlayersVariants(t *testing.T) {
	raw := "Players on server:\n[#] [IP Address]:[Port] [Ping] [GUID] [Name]\n--------------------------------------------------\n" +
		"0   10.0.0.3:2304         15   0123456789abcdef0123456789abcdef(OK) alice (Lobby)\n" +
		"12  192.0.2.7:41939       -1   fedcba9876543210fedcba9876543210(?) bob with spaces\n" +
		"3   garbage line\n(2 players in total)\n"
	ps := ParsePlayers(raw)
	if len(ps) != 2 {
		t.Fatalf("%+v", ps)
	}
	if p := ps[0]; p.ID != 0 || p.IP != "10.0.0.3" || p.Port != 2304 || !p.Verified || p.Name != "alice" {
		t.Errorf("alice %+v", p)
	}
	if p := ps[1]; p.ID != 12 || p.Ping != -1 || p.Verified || p.Name != "bob with spaces" {
		t.Errorf("bob %+v", p)
	}
}
```

`internal/battleye/players.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"regexp"
	"strconv"
	"strings"
)

// PlayerInfo is one line of the RCon "players" answer.
type PlayerInfo struct {
	ID       int
	IP       string
	Port     int
	Ping     int
	GUID     string
	Verified bool // "(OK)"; "(?)" while the BattlEye master has not confirmed it
	Name     string
}

// rePlayer matches "<#> <ip>:<port> <ping> <guid>(OK|?) <name>[ (Lobby)]" (testdata/v2 of
// internal/moderation holds a real answer).
var rePlayer = regexp.MustCompile(`^(\d+)\s+([0-9.]+):(\d+)\s+(-?\d+)\s+([0-9a-f]{32})\((OK|\?)\)\s+(.*?)(?:\s+\(Lobby\))?$`)

// ParsePlayers reads the players of a "players" answer; lines it does not understand are skipped.
func ParsePlayers(raw string) []PlayerInfo {
	var out []PlayerInfo
	for _, line := range strings.Split(raw, "\n") {
		m := rePlayer.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		id, _ := strconv.Atoi(m[1]) // the pattern only matches digits
		port, _ := strconv.Atoi(m[3])
		ping, _ := strconv.Atoi(m[4])
		out = append(out, PlayerInfo{ID: id, IP: m[2], Port: port, Ping: ping, GUID: m[5], Verified: m[6] == "OK", Name: m[7]})
	}
	return out
}
```

Run: `go test ./internal/battleye/ -run ParsePlayers`
Expected: PASS. If the live-capture test fails, the real answer differs from the pattern. Adapt `rePlayer` to `testdata/v2/players.out`, keeping the variants test green.

- [ ] **Step 2: Write the failing Service tests (sync part)**

`internal/moderation/service_test.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
)

type fakeRCon struct {
	mu      sync.Mutex
	cmds    []string
	players string
	fail    error
	events  chan battleye.Event
}

func newFakeRCon() *fakeRCon { return &fakeRCon{events: make(chan battleye.Event, 8)} }

func (f *fakeRCon) Command(_ context.Context, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", f.fail
	}
	f.cmds = append(f.cmds, cmd)
	if cmd == "players" {
		return f.players, nil
	}
	return "", nil
}
func (f *fakeRCon) Events() <-chan battleye.Event { return f.events }
func (f *fakeRCon) Close() error                  { return nil }
func (f *fakeRCon) sent(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}
func (f *fakeRCon) setFail(err error) { f.mu.Lock(); f.fail = err; f.mu.Unlock() }

type svcEnv struct {
	svc   *Service
	st    *Store
	rcon  map[string]*fakeRCon
	dirs  map[string]string
	tick  chan time.Time
	now   time.Time
	mu    sync.Mutex
	audit string
}

func newSvc(t *testing.T, names ...string) *svcEnv {
	t.Helper()
	st, _ := newStore(t)
	e := &svcEnv{st: st, rcon: map[string]*fakeRCon{}, dirs: map[string]string{}, tick: make(chan time.Time), now: t0, audit: filepath.Join(t.TempDir(), "audit.jsonl")}
	e.svc = NewService(st, admin.NewAudit(e.audit), t.Logf)
	e.svc.Tick = e.tick
	e.svc.Now = func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
	var insts []Instance
	for _, n := range names {
		e.rcon[n], e.dirs[n] = newFakeRCon(), t.TempDir()
		insts = append(insts, Instance{Name: n, BEDir: e.dirs[n], RConAddr: "127.0.0.1:1", RConPass: "pw"})
	}
	e.svc.Dial = func(i Instance) RCon { return e.rcon[i.Name] }
	ctx, cancel := context.WithCancel(context.Background())
	e.svc.Start(ctx)
	e.svc.SetInstances(insts)
	t.Cleanup(func() { cancel(); e.svc.Wait() })
	return e
}

func (e *svcEnv) setNow(t time.Time) { e.mu.Lock(); e.now = t; e.mu.Unlock() }

func (e *svcEnv) file(name string) string {
	b, _ := os.ReadFile(filepath.Join(e.dirs[name], BansTxt))
	return string(b)
}

func TestChangedRendersAndReloads(t *testing.T) {
	e := newSvc(t, "alpha", "beta")
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	res := e.svc.Changed(ctx, b, nil)
	if len(res) != 1 || res["alpha"] != "ok" {
		t.Fatalf("Changed = %v", res)
	}
	if !strings.Contains(e.file("alpha"), guidOf(t, steamA)+" -1 cheater") {
		t.Fatalf("alpha bans.txt %q", e.file("alpha"))
	}
	if len(e.rcon["alpha"].sent("loadBans")) == 0 {
		t.Fatal("no loadBans on alpha")
	}
	if e.file("beta") != "" {
		t.Fatal("beta got an alpha ban")
	}
	// Moving the ban to beta syncs both: alpha loses it.
	in := b.Input()
	in.Instances = []string{"beta"}
	b2, _ := e.st.UpdateBan(ctx, b.ID, "a", in, t0)
	if res := e.svc.Changed(ctx, b2, &b); res["alpha"] != "ok" || res["beta"] != "ok" {
		t.Fatalf("Changed after the move = %v", res)
	}
	if strings.Contains(e.file("alpha"), "cheater") || !strings.Contains(e.file("beta"), "cheater") {
		t.Fatalf("after the move: alpha %q beta %q", e.file("alpha"), e.file("beta"))
	}
}

func TestRevokeOneOfTwoKeepsTheOther(t *testing.T) {
	e := newSvc(t, "alpha")
	b1, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "global", Scope: ScopeGlobal}, t0)
	b2, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "y", Message: "local", Scope: ScopeInstances, Instances: []string{"alpha"}, ExpiresAt: at(time.Hour)}, t0)
	e.svc.Changed(ctx, b2, nil)
	r1, _ := e.st.RevokeBan(ctx, b1.ID, "a", "appeal", t0)
	e.svc.Changed(ctx, r1, &b1)
	if f := e.file("alpha"); !strings.Contains(f, guidOf(t, steamA)) || !strings.Contains(f, "local") {
		t.Fatalf("the remaining ban is gone: %q", f)
	}
}

func TestPendingIsRetriedOnTick(t *testing.T) {
	e := newSvc(t, "alpha")
	e.rcon["alpha"].setFail(errors.New("server down"))
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	if res := e.svc.Changed(ctx, b, nil); !strings.HasPrefix(res["alpha"], "pending") {
		t.Fatalf("Changed = %v", res)
	}
	if !e.svc.Pending()["alpha"] {
		t.Fatal("alpha not pending")
	}
	if !strings.Contains(e.file("alpha"), guidOf(t, steamA)) {
		t.Fatal("the file is written even when RCon fails")
	}
	e.rcon["alpha"].setFail(nil)
	e.tick <- t0
	eventually(t, "alpha synced", func() bool { return !e.svc.Pending()["alpha"] })
	if len(e.rcon["alpha"].sent("loadBans")) == 0 {
		t.Fatal("the retry sent no loadBans")
	}
}

func TestSweepDropsExpiredBans(t *testing.T) {
	e := newSvc(t, "alpha")
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal, ExpiresAt: at(time.Minute)}, t0)
	e.svc.Changed(ctx, b, nil)
	e.setNow(t0.Add(2 * time.Minute))
	e.tick <- t0
	eventually(t, "the expired ban leaves bans.txt", func() bool { return !strings.Contains(e.file("alpha"), guidOf(t, steamA)) })
}

func TestOutsideChangesAreImported(t *testing.T) {
	st, _ := newStore(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, BansTxt), []byte("0123456789abcdef0123456789abcdef -1 added by another tool\n"), 0o600)
	svc := NewService(st, admin.NewAudit(filepath.Join(t.TempDir(), "a.jsonl")), t.Logf)
	r := newFakeRCon()
	svc.Dial = func(Instance) RCon { return r }
	svc.Tick = make(chan time.Time)
	svc.Now = func() time.Time { return t0 }
	cctx, cancel := context.WithCancel(context.Background())
	svc.Start(cctx)
	svc.SetInstances([]Instance{{Name: "alpha", BEDir: dir}})
	defer func() { cancel(); svc.Wait() }()
	eventually(t, "the import", func() bool {
		bs, _ := st.ListBans(ctx, BanFilter{Query: "0123456789abcdef0123456789abcdef", Now: t0})
		return len(bs) == 1 && bs[0].CreatedBy == "import:bans.txt"
	})
	eventually(t, "bans.txt rewritten as ours", func() bool { c, _, _ := ChangedOutside(dir); return !c })
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/moderation/ -run 'Changed|Revoke|Pending|Sweep|Outside'`
Expected: FAIL, `undefined: NewService`.

- [ ] **Step 4: Implement `service.go` (sync and sweep; the enforcer follows in Task 12)**

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
)

// syncTimeout bounds one instance's sync when a ban changes, so the API answers quickly when a server
// is down; the sweep retries.
const syncTimeout = 5 * time.Second

// Instance is what the Service needs to enforce bans on one instance.
type Instance struct {
	Name     string
	BEDir    string // the instance's profiles/battleye
	RConAddr string
	RConPass string
}

// RCon is the part of battleye.Session the Service uses.
type RCon interface {
	Command(ctx context.Context, cmd string) (string, error)
	Events() <-chan battleye.Event
	Close() error
}

// Service enforces bans on the running instances: it renders bans.txt, makes BattlEye reload it,
// kicks banned players and records sightings. dzo serve runs one.
type Service struct {
	// Dial makes an instance's RCon session (default: a battleye.Session); tests replace it.
	Dial func(Instance) RCon
	// Tick paces the sweep (default: every minute); tests replace it.
	Tick <-chan time.Time
	// Now is the clock (default time.Now).
	Now func() time.Time

	store *Store
	audit *admin.Audit
	logf  func(string, ...any)

	mu    sync.Mutex
	ctx   context.Context
	insts map[string]*running
	wg    sync.WaitGroup
}

type running struct {
	inst   Instance
	rcon   RCon
	cancel context.CancelFunc
	done   chan struct{}

	syncMu  sync.Mutex // one sync at a time per instance
	mu      sync.Mutex
	pending bool
}

// NewService returns a Service over a store; Start it before SetInstances.
func NewService(store *Store, audit *admin.Audit, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{store: store, audit: audit, logf: logf, Now: time.Now, insts: map[string]*running{},
		Dial: func(i Instance) RCon { return &battleye.Session{Addr: i.RConAddr, Password: i.RConPass, Log: logf} }}
}

// Store returns the database.
func (s *Service) Store() *Store { return s.store }

// Start runs the sweep until ctx ends. Call Wait after ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	tick := s.Tick
	var stop func()
	if tick == nil {
		t := time.NewTicker(time.Minute)
		tick, stop = t.C, t.Stop
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if stop != nil {
			defer stop()
		}
		for {
			select {
			case <-ctx.Done():
				s.SetInstances(nil)
				return
			case <-tick:
				s.sweep(ctx)
			}
		}
	}()
}

// Wait waits for every goroutine of the Service after its context ended.
func (s *Service) Wait() { s.wg.Wait() }

// SetInstances starts enforcement on new instances, stops it on removed ones and restarts it on
// changed ones.
func (s *Service) SetInstances(insts []Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]Instance{}
	for _, i := range insts {
		want[i.Name] = i
	}
	for name, r := range s.insts {
		if w, ok := want[name]; !ok || w != r.inst {
			r.cancel()
			<-r.done
			_ = r.rcon.Close() // the session ends either way
			delete(s.insts, name)
		}
	}
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	for name, i := range want {
		if _, ok := s.insts[name]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		r := &running{inst: i, rcon: s.Dial(i), cancel: cancel, done: make(chan struct{}), pending: true}
		s.insts[name] = r
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer close(r.done)
			s.syncOne(ctx, r) // the first render and import; the enforcer then follows the events
			s.enforce(ctx, r)
		}()
	}
}

func (s *Service) running() []*running {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*running, 0, len(s.insts))
	for _, r := range s.insts {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].inst.Name < out[j].inst.Name })
	return out
}

// Pending reports the instances whose last sync failed.
func (s *Service) Pending() map[string]bool {
	out := map[string]bool{}
	for _, r := range s.running() {
		r.mu.Lock()
		if r.pending {
			out[r.inst.Name] = true
		}
		r.mu.Unlock()
	}
	return out
}

// Changed syncs every running instance that b or prev applies to.
func (s *Service) Changed(ctx context.Context, b Ban, prev *Ban) map[string]string {
	var targets []*running
	for _, r := range s.running() {
		if b.AppliesTo(r.inst.Name) || (prev != nil && prev.AppliesTo(r.inst.Name)) {
			targets = append(targets, r)
		}
	}
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, syncTimeout)
			defer cancel()
			res := "ok"
			if err := s.syncOne(cctx, r); err != nil {
				res = "pending: " + err.Error()
			}
			mu.Lock()
			out[r.inst.Name] = res
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (s *Service) sweep(ctx context.Context) {
	for _, r := range s.running() {
		cctx, cancel := context.WithTimeout(ctx, 4*syncTimeout)
		if err := s.syncOne(cctx, r); err != nil {
			s.logf("bans: %s: %v", r.inst.Name, err)
		}
		cancel()
	}
}

// syncOne imports outside changes, renders bans.txt, makes BattlEye reload it when it changed or the
// last reload failed, and kicks banned players who are online.
func (s *Service) syncOne(ctx context.Context, r *running) error {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	now := s.Now()
	name, dir := r.inst.Name, r.inst.BEDir
	if changed, content, err := ChangedOutside(dir); err != nil {
		return r.fail(err)
	} else if changed {
		n, err := s.store.ImportEntries(ctx, name, ParseBansTxt(content), now)
		if err != nil {
			return r.fail(err)
		}
		if n > 0 {
			s.logf("bans: %s: imported %d bans from bans.txt", name, n)
			_ = s.audit.Log(admin.AuditEntry{Actor: "dzo", Source: "bans", Instance: name, Action: "ban.import", Detail: fmt.Sprintf("%d from bans.txt", n), OK: true}) // informational
		}
	}
	bans, err := s.store.ActiveBans(ctx, name, now)
	if err != nil {
		return r.fail(err)
	}
	written, err := WriteBansFile(dir, FormatBansTxt(Entries(bans, name)))
	if err != nil {
		return r.fail(err)
	}
	r.mu.Lock()
	reload := written || r.pending
	r.mu.Unlock()
	if reload {
		if _, err := r.rcon.Command(ctx, "loadBans"); err != nil {
			return r.fail(fmt.Errorf("loadBans: %w", err))
		}
		if err := s.kickBanned(ctx, r); err != nil {
			return r.fail(err)
		}
	}
	r.mu.Lock()
	r.pending = false
	r.mu.Unlock()
	return nil
}

func (r *running) fail(err error) error {
	r.mu.Lock()
	r.pending = true
	r.mu.Unlock()
	return err
}
```

Add temporary stubs at the end of `service.go`, so that this task compiles. Task 12 replaces them:

```go
func (s *Service) enforce(ctx context.Context, r *running) { <-ctx.Done() }

func (s *Service) kickBanned(context.Context, *running) error { return nil }
```

`TestChangedRendersAndReloads` checks `loadBans` only, so the stubs pass it.

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/moderation/ ./internal/battleye/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/battleye/players.go internal/battleye/players_test.go internal/moderation/service.go internal/moderation/service_test.go
git commit -m "moderation: sync bans.txt to running instances with loadBans, retried by a sweep

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: The enforcer: kick on connect and on a new ban, record sightings

**Files:**
- Modify: `internal/moderation/service.go` (replace the two stubs)
- Test: `internal/moderation/service_test.go`

**Interfaces:**
- Consumes: `battleye.Event` (`EventConnect` with `PlayerID`, `PlayerName`, `Address`; `EventGUID`/`EventVerifiedGUID` with `PlayerID`, `GUID`; `EventDisconnect`, `EventKick`), `battleye.ParsePlayers`, `Store.FindActive`, `Store.Seen`, `BEReason`.
- Produces: kicks over RCon as `kick <slot> <BEReason(message)>` (the form `docs/faq.rst` verified), plus audit entries `ban.kick`.

- [ ] **Step 1: Write the failing tests**

Append to `service_test.go`:

```go
func TestKickOnConnectByGUIDAndIP(t *testing.T) {
	e := newSvc(t, "alpha")
	g := guidOf(t, steamA)
	_, _ = e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeGlobal}, t0)
	_, _ = e.st.CreateBan(ctx, "a", BanInput{IPs: []string{"198.51.100.9"}, Reason: "y", Message: "ip banned", Scope: ScopeGlobal}, t0)
	r := e.rcon["alpha"]
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "alice", Address: "192.0.2.7:2304"}
	r.events <- battleye.Event{Kind: battleye.EventGUID, PlayerID: 3, PlayerName: "alice", GUID: g}
	eventually(t, "the GUID kick", func() bool { return len(r.sent("kick 3 ")) == 1 })
	if k := r.sent("kick 3 ")[0]; k != "kick 3 cheater" {
		t.Fatalf("kick %q", k)
	}
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 4, PlayerName: "mallory", Address: "198.51.100.9:2304"}
	eventually(t, "the IP kick", func() bool { return len(r.sent("kick 4 ")) == 1 })
	ids, _ := e.st.Identities(ctx, steamA)
	if len(ids) != 1 || ids[0].IP != "192.0.2.7" || ids[0].Name != "alice" {
		t.Fatalf("sighting %+v", ids)
	}
	if b, _ := os.ReadFile(e.audit); !strings.Contains(string(b), `"ban.kick"`) {
		t.Fatal("no audit entry for the kick")
	}
}

func TestNoKickWithoutBan(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	g := guidOf(t, steamB)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 1, PlayerName: "bob", Address: "192.0.2.8:2304"}
	r.events <- battleye.Event{Kind: battleye.EventGUID, PlayerID: 1, PlayerName: "bob", GUID: g}
	eventually(t, "the sighting", func() bool { ids, _ := e.st.Identities(ctx, steamB); return len(ids) == 1 })
	if k := r.sent("kick"); len(k) != 0 {
		t.Fatalf("kicked %v", k)
	}
}

func TestNewBanKicksOnlinePlayer(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	g := guidOf(t, steamA)
	r.mu.Lock()
	r.players = "0   192.0.2.7:2304   15   " + g + "(OK) alice\n1   192.0.2.8:2304   15   " + guidOf(t, steamB) + "(OK) bob\n"
	r.mu.Unlock()
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "bye", Scope: ScopeGlobal}, t0)
	e.svc.Changed(ctx, b, nil)
	if k := r.sent("kick"); len(k) != 1 || k[0] != "kick 0 bye" {
		t.Fatalf("kicks %v", k)
	}
}

func TestKickMessageIsSanitised(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	_, _ = e.st.CreateBan(ctx, "a", BanInput{IPs: []string{"192.0.2.7"}, Reason: "x", Message: "two\nlines; #kick -1", Scope: ScopeGlobal}, t0)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 2, PlayerName: "x", Address: "192.0.2.7:2304"}
	eventually(t, "the kick", func() bool { return len(r.sent("kick 2 ")) == 1 })
	if k := r.sent("kick 2 ")[0]; k != "kick 2 two lines; #kick -1" {
		t.Fatalf("kick %q", k)
	}
}
```

The last test shows that the message stays one argument of one command: BattlEye takes the rest of the line as the reason, and a newline cannot start a second command.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/moderation/ -run 'Kick|NoKick|NewBan'`
Expected: FAIL (timeouts in `eventually`, no kicks).

- [ ] **Step 3: Implement**

Replace the two stubs in `service.go` with:

```go
// slot is what the enforcer knows about a connected player.
type slot struct {
	name, ip, guid string
}

// enforce follows an instance's BattlEye events until ctx ends.
func (s *Service) enforce(ctx context.Context, r *running) {
	slots := map[int]*slot{}
	events := r.rcon.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			s.handle(ctx, r, slots, ev)
		}
	}
}

func (s *Service) handle(ctx context.Context, r *running, slots map[int]*slot, ev battleye.Event) {
	name := r.inst.Name
	switch ev.Kind {
	case battleye.EventConnect:
		host, _, err := net.SplitHostPort(ev.Address)
		if err != nil {
			s.logf("bans: %s: connect without an address: %q", name, ev.Raw)
			return
		}
		slots[ev.PlayerID] = &slot{name: ev.PlayerName, ip: host}
		s.check(ctx, r, ev.PlayerID, "", host)
	case battleye.EventGUID, battleye.EventVerifiedGUID:
		sl := slots[ev.PlayerID]
		if sl == nil {
			sl = &slot{name: ev.PlayerName}
			slots[ev.PlayerID] = sl
		}
		sl.guid = ev.GUID
		if sl.ip != "" {
			if err := s.store.Seen(ctx, name, ev.GUID, sl.ip, sl.name, s.Now()); err != nil {
				s.logf("bans: %s: sighting: %v", name, err)
			}
		}
		s.check(ctx, r, ev.PlayerID, ev.GUID, sl.ip)
	case battleye.EventDisconnect, battleye.EventKick:
		delete(slots, ev.PlayerID)
	}
}

// check kicks a player when a ban covers their GUID or IP.
func (s *Service) check(ctx context.Context, r *running, id int, guid, ip string) {
	b, err := s.store.FindActive(ctx, r.inst.Name, guid, ip, s.Now())
	if err != nil {
		s.logf("bans: %s: %v", r.inst.Name, err)
		return
	}
	if b != nil {
		s.kick(ctx, r, id, firstOf(guid, ip), *b)
	}
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *Service) kick(ctx context.Context, r *running, id int, target string, b Ban) {
	_, err := r.rcon.Command(ctx, fmt.Sprintf("kick %d %s", id, BEReason(b.Message)))
	e := admin.AuditEntry{Actor: "dzo", Source: "bans", Instance: r.inst.Name, Action: "ban.kick", Target: target,
		Detail: fmt.Sprintf("ban #%d", b.ID), OK: err == nil}
	if err != nil {
		e.Result = err.Error()
		s.logf("bans: %s: kick %d: %v", r.inst.Name, id, err)
	}
	_ = s.audit.Log(e) // the kick happened (or not) either way
}

// kickBanned kicks the online players a ban covers (after a ban change).
func (s *Service) kickBanned(ctx context.Context, r *running) error {
	raw, err := r.rcon.Command(ctx, "players")
	if err != nil {
		return fmt.Errorf("players: %w", err)
	}
	for _, p := range battleye.ParsePlayers(raw) {
		b, err := s.store.FindActive(ctx, r.inst.Name, p.GUID, p.IP, s.Now())
		if err != nil {
			return err
		}
		if b != nil {
			s.kick(ctx, r, p.ID, firstOf(p.GUID, p.IP), *b)
		}
	}
	return nil
}
```

Add `"net"` to the imports.

- [ ] **Step 4: Run all moderation tests with the race detector**

Run: `go test -race -count=3 ./internal/moderation/`
Expected: PASS, three times (flakiness here is a bug, not bad luck).

- [ ] **Step 5: Commit**

```bash
git add internal/moderation
git commit -m "moderation: kick banned players on connect and on a new ban, record sightings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Wire the Service into `dzo serve`

**Files:**
- Modify: `internal/serve/serve.go`
- Test: `internal/serve/serve_test.go` (existing file; add a test)

**Interfaces:**
- Consumes: `moderation.Open`, `NewService`, `Start`, `SetInstances`, `Wait`; `resolve.ResolveAll`; `inst.RConAddr()`; `inst.Paths.Profiles`.
- Produces: `func BanInstances(cfg *config.Config, tree *site.Tree, logf func(string, ...any)) ([]moderation.Instance, error)`.

- [ ] **Step 1: Write the failing test**

`internal/serve/serve_test.go` has `fixture(t) (*config.Config, *site.Tree)` with the instances `alpha` and `beta`. Add:

```go
func TestBanInstancesNeedTheRConPassword(t *testing.T) {
	cfg, tree := fixture(t)
	insts, err := BanInstances(cfg, tree, t.Logf)
	if err != nil || len(insts) != 0 {
		t.Fatalf("without a password file: %+v, %v", insts, err)
	}
	pwFile := filepath.Join(cfg.Paths.Secrets, "rcon", "alpha")
	if err := os.MkdirAll(filepath.Dir(pwFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pwFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	insts, err = BanInstances(cfg, tree, t.Logf)
	if err != nil || len(insts) != 1 || insts[0].Name != "alpha" || insts[0].RConPass != "secret" ||
		insts[0].BEDir != filepath.Join(cfg.Paths.Instances, "alpha", "profiles", "battleye") {
		t.Fatalf("with a password file: %+v, %v", insts, err)
	}
}
```

(add `"strings"` only if the file needs it elsewhere; this test does not).

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/serve/ -run TestBanInstances`
Expected: FAIL, `undefined: BanInstances`.

- [ ] **Step 3: Implement**

In `internal/serve/serve.go`:

```go
// BanInstances are the instances whose bans serve enforces. An instance without an RCon password yet
// (never rendered) is skipped: the start path renders its bans.txt, and serve picks it up after the
// next site reload.
func BanInstances(cfg *config.Config, tree *site.Tree, logf func(string, ...any)) ([]moderation.Instance, error) {
	all, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		return nil, err
	}
	var out []moderation.Instance
	for _, inst := range all {
		b, err := os.ReadFile(filepath.Join(cfg.Paths.Secrets, "rcon", inst.Name)) //nolint:gosec // dzo's own secrets directory
		if errors.Is(err, os.ErrNotExist) {
			logf("bans: %s has no RCon password yet; enforcing after its first render and a reload", inst.Name)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("bans: %s: reading the RCon password: %w", inst.Name, err)
		}
		out = append(out, moderation.Instance{Name: inst.Name, BEDir: filepath.Join(inst.Paths.Profiles, "battleye"),
			RConAddr: inst.RConAddr(), RConPass: strings.TrimSpace(string(b))})
	}
	return out, nil
}
```

The error message names the file's instance, never the password. In `Run`, after `AddHubs`:

```go
	store, err := moderation.Open(ctx, BanDB(cfg))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }() // after the Service has stopped (deferred below, runs first)
	mctx, mcancel := context.WithCancel(ctx)
	svc := moderation.NewService(store, admin.NewAudit(AuditLog(cfg)), logf)
	svc.Start(mctx)
	defer func() { mcancel(); svc.Wait() }()
	if insts, err := BanInstances(cfg, tree, logf); err != nil {
		logf("bans: %v", err)
	} else {
		svc.SetInstances(insts)
	}
```

Pass `svc` to the API: after `apiSrv := api.New(…)`, add `apiSrv.Moderation = svc`. In the reload branch, after `AddHubs` succeeded, add:

```go
			if insts, err := BanInstances(cfg, t, logf); err != nil {
				logf("bans: %v", err)
			} else {
				svc.SetInstances(insts)
			}
```

Use one shared `audit := admin.NewAudit(AuditLog(cfg))` for both the API and the Service, so that both write through the same mutex. Add the imports `errors`, `os`, `strings`, `moderation`.

- [ ] **Step 4: Run the serve and command tests**

Run: `go test -race ./internal/serve/ ./cmd/dzo/`
Expected: PASS (`TestServeCommand` now also creates `db/dzo.sqlite` under its temp dir).

- [ ] **Step 5: Commit**

```bash
git add internal/serve
git commit -m "serve: run the ban enforcer for every instance with an RCon password

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: V3, enforcement end to end with a real client

**Files:**
- Modify: `scripts/test-client.sh`

**Interfaces:**
- Consumes: `dzo ban add|revoke` (Task 10), serve's Service (Task 13), the `--bans` harness (Tasks 1–2).

- [ ] **Step 1: Add the ban checks**

In `scripts/test-client.sh`, in the ban branch after the V2 block (the player is online), add:

```bash
if [ "$bans" = 1 ]; then
	adm_kicks() { grep -c "dzotester.*kicked" "$adm" 2>/dev/null || true; }
	client() { "$dev/dayz-client-headless.sh" "${exp[@]}" "$@" "$work/client" -name=dzotester -connect=127.0.0.1 -port="$game" >/dev/null 2>&1; }
	joined() { waitfor "$1" "grep -c 'dzotester.*is connected' '$adm' | grep -qx '$2'"; }

	# V3b: a new ban kicks the player who is online, with the ban message.
	s=0
	dzo ban add --steam "$id" --reason "V3 test" --message "dzo V3 ban" --duration 10m --instance t1 | grep -q 't1: ok' || s=1
	waitfor 30 "'$work/dzo' rcon exec --addr 127.0.0.1:$rcon --password '$rconpw' players | grep -vq dzotester" || s=1
	grep -q 'dzo V3 ban' "$work/serve.log" "$adm" "$tree"/profiles/*.RPT 2>/dev/null || true # the message's place is recorded by V3a below
	verdict "V3b: a new ban kicks the online player" $s
	s=0
	grep -q "$(python3 -I -c 'import hashlib,struct,sys;print(hashlib.md5(b"BE"+struct.pack("<Q",int(sys.argv[1]))).hexdigest())' "$id") [0-9]* dzo V3 ban" "$tree/profiles/battleye/bans.txt" || s=1
	verdict "V3: bans.txt holds the derived GUID with the expiry and message" $s

	# V3a: a banned player who connects is refused.
	"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true
	before=$(grep -c 'dzotester.*is connected' "$adm" || true)
	client start
	s=0
	sleep 60 # the client needs this long to reach the server; a refused join leaves no trace to poll for in the ADM
	[ "$(grep -c 'dzotester.*is connected' "$adm" || true)" = "$before" ] || s=1
	verdict "V3a: a banned player cannot join" $s
	"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$work/v3a-refused.png" >/dev/null 2>&1 || true
	"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true

	# V3c: a revoked ban lets the player in again.
	banid=$(dzo ban list --state active --query "$id" | awk 'NR == 2 {print $1}')
	dzo ban revoke "$banid" --reason "V3 done" >/dev/null
	client start
	s=0
	joined 240 "$((before + 1))" || s=1
	verdict "V3c: after the revoke the player joins" $s
	"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true

	# V3d: an IP ban refuses the player's address (127.0.0.1 on a local test).
	dzo ban add --ip 127.0.0.1 --reason "V3 ip" --duration 10m --instance t1 >/dev/null
	before=$(grep -c 'dzotester.*is connected' "$adm" || true)
	client start
	s=0
	sleep 60 # as for V3a
	[ "$(grep -c 'dzotester.*is connected' "$adm" || true)" = "$before" ] || s=1
	verdict "V3d: an IP ban refuses the address" $s
	"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true
	echo "== $pass passed, $fail failed (ban stage)"
	[ "$fail" = 0 ]
	exit
fi
```

The two `sleep 60`s are the one exception to "never sleep to wait". This is a shell live test, and a refused join leaves no event to poll for. The comments say so, and the screenshot `v3a-refused.png` records what the player saw.

- [ ] **Step 2: Run it**

Run: `scripts/test-client.sh --bans --keep`
Expected: V1, V2, V3a–d and the bans.txt check all `PASS`. On a failure, read `"$work/serve.log"` (`bans:` lines) and `"$tree/profiles/battleye/bans.txt"`.

- [ ] **Step 3: Record what the player sees**

Look at `v3a-refused.png` in the kept work dir and note the text BattlEye shows (the reason or a generic "Banned"). Add it to `internal/moderation/testdata/v1/NOTES.md` as "Q9 (from V3a): …".

- [ ] **Step 4: Commit**

```bash
git add scripts/test-client.sh internal/moderation/testdata/v1/NOTES.md
git commit -m "test: ban enforcement end to end with a BattlEye client (V3)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Web UI: ban list, ban form with fan-out, ban detail

**Files:**
- Create: `internal/web/bans.go`, `internal/web/bans_test.go`, `internal/web/templates/bans.html`
- Modify: `internal/web/server.go` (routes), `internal/web/templates/layout.html` (nav), `internal/web/templates/fragments.html` (Ban link), `internal/web/web_test.go` (`newEnv` gets a moderation store)

**Interfaces:**
- Consumes: the apiclient methods of Task 10, `moderation.ExpiryFrom`, `FormatExpiry`, `Ban.State`, `Ban.Input`.
- Produces the routes:
  - `GET /b/{backend}/bans` (list; `?state=&q=`)
  - `GET /b/{backend}/bans/new?steam_id=&instance=` (form)
  - `POST /b/{backend}/bans` (create, fan-out with `also`)
  - `GET /b/{backend}/bans/{id}` (detail)
  - `POST /b/{backend}/bans/{id}` (edit)
  - `POST /b/{backend}/bans/{id}/revoke`
  - `GET /b/{backend}/evidence/{id}/file` (image proxy)

- [ ] **Step 1: Give the web test env a ban database**

In `internal/web/web_test.go`'s `newEnv`, after creating `apiSrv`:

```go
	st, err := moderation.Open(context.Background(), filepath.Join(dir, "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	apiSrv.Moderation = webMod{st}
```

and in `bans_test.go` define:

```go
type webMod struct{ st *moderation.Store }

func (m webMod) Store() *moderation.Store { return m.st }
func (m webMod) Changed(context.Context, moderation.Ban, *moderation.Ban) map[string]string {
	return map[string]string{"alpha": "ok"}
}
```

Also add a second working backend, `"other"`, to `newEnv` for the fan-out test. It is a second `api.Server` with its own store and hub `"alpha"`, appended as `{Name: "other", Client: …}` before `"down"`. Keep the existing tests passing; if one counts backends, update it.

- [ ] **Step 2: Write the failing tests**

`internal/web/bans_test.go` (with `webMod` from Step 1):

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const webSteam = "76561198000000001"

func (e *env) post(path string, form url.Values, csrf bool) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.web.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if csrf {
		req.Header.Set("X-CSRF-Token", e.srv.csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestBanFormCreatesAndFansOut(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	if code != 200 || !strings.Contains(body, webSteam) || !strings.Contains(body, `name="also" value="other"`) {
		t.Fatalf("form: %d %s", code, body)
	}
	form := url.Values{"steam_id": {webSteam}, "reason": {"aimbot <script>"}, "message": {"bye"}, "duration": {"7d"},
		"scope": {"instances"}, "instances": {"alpha"}, "also": {"other"}}
	if code, _ := e.post("/b/main/bans", form, false); code != 403 {
		t.Fatalf("no CSRF token: %d", code)
	}
	code, body = e.post("/b/main/bans", form, true)
	if code != 200 || strings.Count(body, "ban #1") != 2 || !strings.Contains(body, "alpha: ok") {
		t.Fatalf("create: %d %s", code, body)
	}
	_, list, _ := e.get("/b/main/bans?state=active")
	if !strings.Contains(list, "aimbot &lt;script&gt;") || strings.Contains(list, "<script>") {
		t.Fatalf("list escaping: %s", list)
	}
	_, other, _ := e.get("/b/other/bans")
	if !strings.Contains(other, "aimbot") {
		t.Fatalf("the other backend has no ban: %s", other)
	}
}

func TestBanDetailEditAndRevoke(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	form := url.Values{"steam_id": {webSteam}, "reason": {"first"}, "duration": {"permanent"}, "scope": {"global"}}
	if code, body := e.post("/b/main/bans", form, true); code != 200 || !strings.Contains(body, "ban #1") {
		t.Fatalf("create: %d %s", code, body)
	}
	code, body, _ := e.get("/b/main/bans/1")
	if code != 200 || !strings.Contains(body, "first") || !strings.Contains(body, "create") {
		t.Fatalf("detail: %d %s", code, body)
	}
	form.Set("reason", "second")
	form.Set("duration", "keep")
	if code, body := e.post("/b/main/bans/1", form, true); code != 200 || !strings.Contains(body, "changed") {
		t.Fatalf("edit: %d %s", code, body)
	}
	if code, body := e.post("/b/main/bans/1/revoke", url.Values{"reason": {"appeal"}}, true); code != 200 || !strings.Contains(body, "revoked") {
		t.Fatalf("revoke: %d %s", code, body)
	}
	_, body, _ = e.get("/b/main/bans/1")
	if !strings.Contains(body, "second") || !strings.Contains(body, "appeal") {
		t.Fatalf("detail after: %s", body)
	}
	if code, _, _ := e.get("/b/main/bans/abc"); code != 404 {
		t.Fatalf("bad id: %d", code)
	}
}

func TestEvidenceImageProxy(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	c := e.srv.backend("main").Client
	ev, err := c.AddEvidenceImage(context.Background(), moderation.EvidenceInput{SteamID: webSteam, Instance: "alpha", Body: "shot"}, png)
	if err != nil {
		t.Fatal(err)
	}
	code, body, hdr := e.get("/b/main/evidence/" + itoa(ev.ID) + "/file")
	if code != 200 || hdr.Get("Content-Type") != "image/png" || hdr.Get("X-Content-Type-Options") != "nosniff" || len(body) != len(png) {
		t.Fatalf("proxy: %d %v", code, hdr)
	}
	_, page, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	if !strings.Contains(page, `<img src="/b/main/evidence/`+itoa(ev.ID)+`/file"`) {
		t.Fatalf("the form does not show the player's evidence: %s", page)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
```

(add `"strconv"` to the imports).

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/web/ -run 'Ban|Evidence'`
Expected: FAIL (404 from the new routes).

- [ ] **Step 4: Implement the handlers**

Routes in `internal/web/server.go` `Handler()`:

```go
	mux.HandleFunc("GET /b/{backend}/bans", s.bansPage)
	mux.HandleFunc("GET /b/{backend}/bans/new", s.banForm)
	mux.HandleFunc("POST /b/{backend}/bans", s.createBan)
	mux.HandleFunc("GET /b/{backend}/bans/{id}", s.banPage)
	mux.HandleFunc("POST /b/{backend}/bans/{id}", s.updateBan)
	mux.HandleFunc("POST /b/{backend}/bans/{id}/revoke", s.revokeBan)
	mux.HandleFunc("GET /b/{backend}/evidence/{id}/file", s.evidenceFile)
```

`internal/web/bans.go`:

```go
// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type bansView struct {
	Backend, State, Query string
	Bans                  []moderation.Ban
	Now                   time.Time
	Err                   string
}

func (s *Server) backendOf(w http.ResponseWriter, r *http.Request) (*Backend, *apiclient.Client, bool) {
	b := s.backend(r.PathValue("backend"))
	if b == nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return b, s.client(b, r), true
}

func (s *Server) bansPage(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	v := bansView{Backend: b.Name, State: r.URL.Query().Get("state"), Query: r.URL.Query().Get("q"), Now: time.Now()}
	var err error
	if v.Bans, err = c.Bans(r.Context(), apiclient.BanFilter{State: v.State, Query: v.Query, Limit: 200}); err != nil {
		v.Err = err.Error()
	}
	s.render(w, "bans", s.page("Bans", b.Name, v))
}

type banFormView struct {
	Backend, SteamID, Instance string
	Instances                  []admin.Status
	Others                     []string
	Identities                 []moderation.Sighting
	Evidence                   []moderation.Evidence
	CanIP, CanGlobal           bool
	Err                        string
}

func (s *Server) banForm(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	v := banFormView{Backend: b.Name, SteamID: q.Get("steam_id"), Instance: q.Get("instance")}
	for _, o := range s.backends {
		if o.Name != b.Name {
			v.Others = append(v.Others, o.Name)
		}
	}
	var err error
	if v.Instances, err = c.Instances(r.Context()); err != nil {
		v.Err = err.Error()
	}
	if info, err := c.Info(r.Context()); err == nil {
		v.CanIP, v.CanGlobal = info.Role.Can(api.PermBanIP), info.Role.Can(api.PermBanGlobal)
	}
	if moderation.ValidSteamID(v.SteamID) {
		v.Identities, _ = c.Identities(r.Context(), v.SteamID) // optional extras; the form works without them
		v.Evidence, _ = c.Evidence(r.Context(), moderation.Who{SteamID: v.SteamID})
	}
	s.render(w, "banform", s.page("Ban a player", b.Name, v))
}

// formInput reads the ban fields of a form. duration "keep" leaves keep's expiry.
func formInput(r *http.Request, keep *time.Time) (moderation.BanInput, error) {
	f := r.PostForm
	in := moderation.BanInput{SteamID: strings.TrimSpace(f.Get("steam_id")), Reason: f.Get("reason"), Message: f.Get("message"),
		Scope: moderation.Scope(f.Get("scope")), IPs: f["ips"]}
	if g := strings.TrimSpace(f.Get("guid")); g != "" {
		in.GUIDs = []string{g}
	}
	if in.Scope == moderation.ScopeInstances {
		in.Instances = f["instances"]
	}
	for _, e := range f["evidence"] {
		id, err := strconv.ParseInt(e, 10, 64)
		if err != nil {
			return in, fmt.Errorf("invalid evidence id %q", e)
		}
		in.Evidence = append(in.Evidence, id)
	}
	d := f.Get("duration")
	if d == "custom" {
		d = strings.TrimSpace(f.Get("duration_custom"))
	}
	if d == "keep" {
		in.ExpiresAt = keep
		return in, nil
	}
	exp, err := moderation.ExpiryFrom(d, time.Now())
	in.ExpiresAt = exp
	return in, err
}

type banOutcome struct {
	Backend string
	ID      int64
	Sync    map[string]string
	Err     string
}

func (s *Server) createBan(w http.ResponseWriter, r *http.Request) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	b, _, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	in, err := formInput(r, nil)
	if err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	targets := []*Backend{b}
	for _, name := range r.PostForm["also"] {
		if o := s.backend(name); o != nil && o != b && !slices.Contains(targets, o) {
			targets = append(targets, o)
		}
	}
	var out []banOutcome
	for i, t := range targets {
		tin := in
		if i > 0 {
			tin.Evidence = nil // evidence ids belong to the first backend's database
		}
		o := banOutcome{Backend: t.Name}
		if res, err := s.client(t, r).CreateBan(r.Context(), tin); err != nil {
			o.Err = err.Error()
		} else {
			o.ID, o.Sync = res.Ban.ID, res.Sync
		}
		out = append(out, o)
	}
	s.render(w, "banresult", out)
}

type banView struct {
	Backend  string
	Ban      moderation.Ban
	History  []moderation.HistoryEntry
	Evidence []moderation.Evidence
	Now      time.Time
	Err      string
}

func banID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Server) banPage(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id, ok := banID(w, r)
	if !ok {
		return
	}
	v := banView{Backend: b.Name, Now: time.Now()}
	var err error
	if v.Ban, err = c.Ban(r.Context(), id); err != nil {
		v.Err = err.Error()
	} else {
		v.History, _ = c.BanHistory(r.Context(), id) // shown when it loads
		for _, e := range v.Ban.Evidence {
			if ev, err := c.EvidenceItem(r.Context(), e); err == nil {
				v.Evidence = append(v.Evidence, ev)
			}
		}
	}
	s.render(w, "ban", s.page(fmt.Sprintf("Ban #%d", id), b.Name, v))
}

func (s *Server) updateBan(w http.ResponseWriter, r *http.Request) {
	s.banAction(w, r, func(c *apiclient.Client, id int64) (string, error) {
		old, err := c.Ban(r.Context(), id)
		if err != nil {
			return "", err
		}
		in, err := formInput(r, old.ExpiresAt)
		if err != nil {
			return "", err
		}
		res, err := c.UpdateBan(r.Context(), id, in)
		return syncText("changed", res.Sync), err
	})
}

func (s *Server) revokeBan(w http.ResponseWriter, r *http.Request) {
	s.banAction(w, r, func(c *apiclient.Client, id int64) (string, error) {
		res, err := c.RevokeBan(r.Context(), id, r.PostForm.Get("reason"))
		return syncText("revoked", res.Sync), err
	})
}

func syncText(verb string, sync map[string]string) string {
	var parts []string
	for n, st := range sync {
		parts = append(parts, n+": "+st)
	}
	slices.Sort(parts)
	if len(parts) == 0 {
		return verb
	}
	return verb + " (" + strings.Join(parts, ", ") + ")"
}

func (s *Server) banAction(w http.ResponseWriter, r *http.Request, f func(*apiclient.Client, int64) (string, error)) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	_, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id, ok := banID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	text, err := f(c, id)
	if err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	s.render(w, "flash", flash{OK: true, Text: text})
}

// evidenceFile passes an evidence image through, with the API's type and never sniffed.
func (s *Server) evidenceFile(w http.ResponseWriter, r *http.Request) {
	_, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id, ok := banID(w, r)
	if !ok {
		return
	}
	resp, err := c.EvidenceFile(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	ct := resp.Header.Get("Content-Type")
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, moderation.MaxImage)) // a broken transfer only breaks this image
}
```

Import `internal/api` for the permission constants.

- [ ] **Step 5: Write the templates**

`internal/web/templates/bans.html`:

```html
{{/* SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de> */}}
{{/* SPDX-License-Identifier: AGPL-3.0-or-later */}}
{{define "bans"}}{{template "head" .}}
{{with .Data}}
<h1><i class="fa fa-ban" aria-hidden="true"></i>Bans <small>{{.Backend}}</small></h1>
<form method="get" class="filters">
  <select name="state">
    <option value="" {{if eq .State ""}}selected{{end}}>all</option>
    <option value="active" {{if eq .State "active"}}selected{{end}}>active</option>
    <option value="expired" {{if eq .State "expired"}}selected{{end}}>expired</option>
    <option value="revoked" {{if eq .State "revoked"}}selected{{end}}>revoked</option>
  </select>
  <input name="q" value="{{.Query}}" placeholder="Steam ID, GUID, IP, name or reason" size="34">
  <button><i class="fa fa-search" aria-hidden="true"></i>Filter</button>
  <a class="btn" href="/b/{{.Backend}}/bans/new"><i class="fa fa-plus" aria-hidden="true"></i>New ban</a>
</form>
{{if .Err}}<p class="flash err">{{.Err}}</p>{{else if not .Bans}}<p>No bans.</p>{{else}}
<table>
  <thead><tr><th>#</th><th>State</th><th>Expires</th><th>Scope</th><th>Targets</th><th>Reason</th><th>By</th></tr></thead>
  <tbody>
  {{$now := .Now}}{{$be := .Backend}}
  {{range .Bans}}
    <tr>
      <td><a href="/b/{{$be}}/bans/{{.ID}}">{{.ID}}</a></td>
      <td>{{.State $now}}</td>
      <td>{{expiry .ExpiresAt}}</td>
      <td>{{if eq .Scope "global"}}global{{else}}{{join .Instances ", "}}{{end}}</td>
      <td>{{range .Targets}}{{if not .Derived}}<code>{{.Value}}</code> {{end}}{{end}}</td>
      <td>{{.Reason}}</td>
      <td>{{.CreatedBy}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}
{{end}}
{{template "foot" .}}{{end}}

{{define "banform"}}{{template "head" .}}
{{with .Data}}
<h1><i class="fa fa-ban" aria-hidden="true"></i>Ban a player <small>{{.Backend}}</small></h1>
{{if .Err}}<p class="flash err">{{.Err}}</p>{{end}}
<div id="ban-result"></div>
<form hx-post="/b/{{.Backend}}/bans" hx-target="#ban-result" class="banform">
  <label>Steam ID <input name="steam_id" value="{{.SteamID}}" size="20" pattern="7656119[0-9]{10}"></label>
  <label>or BattlEye GUID <input name="guid" size="34" pattern="[0-9a-f]{32}"></label>
  <label>Reason (for admins) <input name="reason" required maxlength="1000" size="50"></label>
  <label>Message (shown to the player) <input name="message" maxlength="200" size="50" placeholder="You are banned from this server"></label>
  <label>Duration
    <select name="duration">
      <option value="1h">1 hour</option><option value="1d">1 day</option><option value="7d" selected>7 days</option>
      <option value="30d">30 days</option><option value="permanent">permanent</option><option value="custom">custom…</option>
    </select>
    <input name="duration_custom" placeholder="e.g. 3d or 12h" size="10">
  </label>
  <fieldset><legend>Scope</legend>
    {{if .CanGlobal}}<label><input type="radio" name="scope" value="global"> every instance</label>{{end}}
    <label><input type="radio" name="scope" value="instances" checked> these instances:</label>
    {{$sel := .Instance}}{{range .Instances}}<label><input type="checkbox" name="instances" value="{{.Instance}}" {{if eq .Instance $sel}}checked{{end}}> {{.Instance}}</label>{{end}}
  </fieldset>
  {{if .Identities}}<fieldset><legend>IP addresses (not banned unless ticked)</legend>
    {{range .Identities}}<label><input type="checkbox" name="ips" value="{{.IP}}" {{if not $.Data.CanIP}}disabled{{end}}> {{.IP}} ({{.Name}} on {{.Instance}}, last {{.LastSeen.Format "2006-01-02"}}){{if .SharedWith}} <strong class="warn">shared with {{.SharedWith}} other player{{if gt .SharedWith 1}}s{{end}}</strong>{{end}}</label>{{end}}
  </fieldset>{{end}}
  {{if .Evidence}}<fieldset><legend>Evidence to link</legend>
    {{range .Evidence}}<label class="evidence"><input type="checkbox" name="evidence" value="{{.ID}}" checked> #{{.ID}} {{.Kind}} from {{.Source}} on {{.Instance}}, {{.CreatedAt.Format "2006-01-02 15:04"}}
      {{if eq .Kind "image"}}<img src="/b/{{$.Data.Backend}}/evidence/{{.ID}}/file" alt="evidence {{.ID}}" loading="lazy">{{end}}
      {{if .Body}}<pre>{{.Body}}</pre>{{end}}</label>{{end}}
  </fieldset>{{end}}
  {{if .Others}}<fieldset><legend>Also send to (without the evidence links)</legend>
    {{range .Others}}<label><input type="checkbox" name="also" value="{{.}}"> {{.}}</label>{{end}}
  </fieldset>{{end}}
  <button class="danger"><i class="fa fa-ban" aria-hidden="true"></i>Ban</button>
</form>
{{end}}
{{template "foot" .}}{{end}}

{{define "banresult"}}{{range .}}<div class="flash {{if .Err}}err{{else}}ok{{end}}" role="status">{{.Backend}}: {{if .Err}}{{.Err}}{{else}}ban #{{.ID}} created{{range $i, $s := .Sync}} · {{$i}}: {{$s}}{{end}}{{end}}</div>{{end}}{{end}}

{{define "ban"}}{{template "head" .}}
{{with .Data}}
<h1><i class="fa fa-ban" aria-hidden="true"></i>Ban #{{.Ban.ID}} <small>{{.Backend}}</small></h1>
{{if .Err}}<p class="flash err">{{.Err}}</p>{{else}}
{{$b := .Ban}}
<dl>
  <dt>State</dt><dd>{{$b.State $.Data.Now}}</dd>
  <dt>Reason</dt><dd>{{$b.Reason}}</dd>
  <dt>Message</dt><dd>{{$b.Message}}</dd>
  <dt>Expires</dt><dd>{{expiry $b.ExpiresAt}}</dd>
  <dt>Scope</dt><dd>{{if eq $b.Scope "global"}}every instance{{else}}{{join $b.Instances ", "}}{{end}}</dd>
  <dt>Targets</dt><dd>{{range $b.Targets}}<code>{{.Kind}} {{.Value}}</code>{{if .Derived}} (from the Steam ID){{end}}<br>{{end}}</dd>
  <dt>Created</dt><dd>{{$b.CreatedAt.Format "2006-01-02 15:04"}} by {{$b.CreatedBy}}</dd>
  {{if $b.RevokedAt}}<dt>Revoked</dt><dd>{{$b.RevokedAt.Format "2006-01-02 15:04"}} by {{$b.RevokedBy}}: {{$b.RevokeReason}}</dd>{{end}}
</dl>
<div id="flash"></div>
{{if not $b.RevokedAt}}
<section><h2>Edit</h2>
<form hx-post="/b/{{.Backend}}/bans/{{$b.ID}}" hx-target="#flash">
  {{with $b.Input}}<input type="hidden" name="steam_id" value="{{.SteamID}}">{{range .GUIDs}}<input type="hidden" name="guid" value="{{.}}">{{end}}
  {{range .IPs}}<label><input type="checkbox" name="ips" value="{{.}}" checked> IP {{.}}</label>{{end}}
  {{range .Evidence}}<input type="hidden" name="evidence" value="{{.}}">{{end}}{{end}}
  <label>Reason <input name="reason" value="{{$b.Reason}}" required maxlength="1000" size="50"></label>
  <label>Message <input name="message" value="{{$b.Message}}" maxlength="200" size="50"></label>
  <label>Duration <select name="duration"><option value="keep" selected>keep ({{expiry $b.ExpiresAt}})</option><option value="1d">1 day from now</option><option value="7d">7 days from now</option><option value="30d">30 days from now</option><option value="permanent">permanent</option><option value="custom">custom…</option></select>
    <input name="duration_custom" placeholder="e.g. 3d" size="8"></label>
  <input type="hidden" name="scope" value="{{$b.Scope}}">{{range $b.Instances}}<input type="hidden" name="instances" value="{{.}}">{{end}}
  <button><i class="fa fa-save" aria-hidden="true"></i>Save</button>
</form>
<form hx-post="/b/{{.Backend}}/bans/{{$b.ID}}/revoke" hx-target="#flash" hx-confirm="Revoke ban #{{$b.ID}}?">
  <input name="reason" placeholder="Why the ban ends" required size="40">
  <button class="danger"><i class="fa fa-undo" aria-hidden="true"></i>Revoke (unban)</button>
</form>
</section>
{{end}}
{{if .Evidence}}<section><h2>Evidence</h2>
{{range .Evidence}}<div class="evidence"><strong>#{{.ID}} {{.Kind}}</strong> from {{.Source}} on {{.Instance}}, {{.CreatedAt.Format "2006-01-02 15:04"}}
  {{if eq .Kind "image"}}<img src="/b/{{$.Data.Backend}}/evidence/{{.ID}}/file" alt="evidence {{.ID}}" loading="lazy">{{end}}
  {{if .Body}}<pre>{{.Body}}</pre>{{end}}</div>{{end}}
</section>{{end}}
<section><h2>History</h2>
<table><thead><tr><th>When</th><th>Who</th><th>What</th></tr></thead><tbody>
{{range .History}}<tr><td>{{.At.Format "2006-01-02 15:04"}}</td><td>{{.Actor}}</td><td>{{.Action}}</td></tr>{{end}}
</tbody></table></section>
{{end}}
{{end}}
{{template "foot" .}}{{end}}
```

Register the `expiry` template function in `New`: `template.FuncMap{"pct": pct, "join": strings.Join, "expiry": moderation.FormatExpiry}`.

`FormatExpiry` takes `*time.Time`, and `.ExpiresAt` is one, so the call works as written.

In `layout.html`'s nav, inside the `range .Backends` loop, add `<a href="/b/{{.Name}}/bans"><i class="fa fa-ban" aria-hidden="true"></i>{{.Name}} bans</a>`.

In `fragments.html`'s `panel`, after the give form and before `</div>`, add:

```html
  <a class="btn danger" href="/b/{{.Backend}}/bans/new?steam_id={{.Player.SteamID}}&amp;instance={{.Instance}}"><i class="fa fa-ban" aria-hidden="true"></i>Ban…</a>
```

`html/template` escapes the query values, so it's safe for any Steam ID or instance string. Add minimal CSS to `static/app.css` for `.banform label { display:block }`, `.evidence img { max-width: 32rem; display:block }`, and `.warn { color: var(--err, #b00) }`, using the variable names that `app.css` already uses.

- [ ] **Step 6: Run the web tests**

Run: `go test -race ./internal/web/`
Expected: PASS. Then open the UI by hand: `go run ./cmd/dzo web …` with a local serve, as in `docs/web.rst`. Check the form, a list with 1 000 bans, and the image.

- [ ] **Step 7: Commit**

```bash
git add internal/web
git commit -m "web: ban list, ban form with known IPs and fan-out to other backends, ban page with evidence and history

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: V4, DayZ's own `ban.txt`

**Files:**
- Modify: `scripts/test-client.sh` (one more check in `--bans` mode)
- Modify (only if V4 passes): `internal/moderation/bansfile.go`, `internal/moderation/bansfile_test.go`, `internal/moderation/service.go`, `cmd/dzo/instance.go`

- [ ] **Step 1: Test it live**

In the ban branch of `test-client.sh`, before the final `echo` of Task 14, add a V4 block:

1. Stop the server: kill `$server`, wait for it, and remove it from `pids`.
2. Revoke all test bans (`dzo ban list --state active | awk 'NR>1{print $1}' | xargs -r -n1 …revoke --reason V4`).
3. Write `"$id // V4 test"` into `"$tree/ban.txt"`.
4. Start the server again with the same command line (refactor the server start into a `start_server` function to reuse it).
5. Start the client and wait 60 s, with the same sleep rationale as V3a.
6. Assert no new `is connected` line: `verdict "V4: DayZ ban.txt with a comment refuses the Steam ID" $s`.
7. Repeat with just `"$id"`: `verdict "V4: DayZ ban.txt, bare Steam ID"`.
8. Repeat with the server running: write the file *after* the server is up and check whether it is re-read: `verdict "V4: ban.txt is re-read while running"`. This one is informational: record it as PASS or FAIL.

Run: `scripts/test-client.sh --bans --keep`, and record the three results as Q10–Q12 in `testdata/v1/NOTES.md`.

- [ ] **Step 2: Decide**

- If neither of the first two checks passes: DayZ's `ban.txt` is not supported. Write that into `docs/moderation.rst` (Task 17), commit the script and notes, and skip Step 3.
- If either passes: do Step 3 with the format that worked.

- [ ] **Step 3 (only if V4 passed): Render `ban.txt` too**

Test first, in `bansfile_test.go`:

```go
func TestDayZBanTxt(t *testing.T) {
	bans := []Ban{
		{ID: 1, Scope: ScopeGlobal, Targets: []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetGUID, Value: guidOf(t, steamA), Derived: true}}},
		{ID: 2, Scope: ScopeGlobal, ExpiresAt: at(time.Hour), Targets: []Target{{Kind: TargetSteam, Value: steamB}}},
	}
	if got := string(DayZBanTxt(bans, "alpha")); got != steamA+"\n" {
		t.Fatalf("ban.txt %q: only permanent Steam ID bans (DayZ knows no expiry)", got)
	}
}
```

Implement it in `bansfile.go`:

```go
// DayZBanTxt renders the server's own ban.txt (V4): the Steam IDs of permanent bans, one per line.
// DayZ knows no expiry, so timed bans stay with BattlEye and the enforcer.
func DayZBanTxt(bans []Ban, instance string) []byte {
	var ids []string
	for _, b := range bans {
		if b.ExpiresAt != nil || !b.AppliesTo(instance) {
			continue
		}
		for _, t := range b.Targets {
			if t.Kind == TargetSteam && !slices.Contains(ids, t.Value) {
				ids = append(ids, t.Value)
			}
		}
	}
	sort.Strings(ids)
	var out bytes.Buffer
	for _, id := range ids {
		out.WriteString(id + "\n")
	}
	return out.Bytes()
}
```

Use the line format V4 confirmed: bare, or with `// <reason>` if only that worked. Write it to the server root, which inside the container is `runtime/` beside `priority.txt`. Check how `priority.txt` is mounted in `internal/quadlet` (`grep -n priority internal/quadlet/*.go`) and add `ban.txt` the same way, written in `runfiles.Write` from a new `Input.BanTxt []byte`. `RenderAtStart` gains a `runtimeDir` argument and writes `ban.txt` there with `writeAtomic`. If V4 showed the file is re-read at runtime, `syncOne` writes it too. Otherwise it is written at start only, and the docs say so. Add tests for each change, red first, as in earlier tasks.

- [ ] **Step 4: Commit**

```bash
git add scripts/test-client.sh internal/moderation/testdata/v1/NOTES.md   # plus the Step 3 files when done
git commit -m "test: DayZ's own ban.txt checked live (V4)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: Documentation

**Files:**
- Create: `docs/moderation.rst`
- Modify: `docs/index.rst` (toctree), `docs/api.rst`, `docs/cli.rst`, `docs/web.rst`, `docs/faq.rst` (`:doc:` links from Task 1), `docs/development.rst` (the `--bans` stage), `debian/changelog`

- [ ] **Step 1: Write `docs/moderation.rst`**

Sections, in the docs' existing plain style. Read `docs/web.rst` for tone, and fill every value from the code and NOTES.md, with no placeholders left:

1. **Bans**: what a ban covers (Steam ID plus its GUID, optional GUIDs and IPs), durations (`30m … permanent`), scope (instances or global = every instance of this installation), message and reason.
2. **Shared IP addresses**: why IPs are never banned automatically, and the "shared with N other players" warning.
3. **How bans are enforced**:
   - `bans.txt` rendered at every start and by `dzo serve`, then `loadBans`
   - the enforcer's kicks on connect and on a new ban
   - what holds while serve is down
   - the hash sidecar, importing outside changes, and "remove bans through dzo"
   - DayZ's `ban.txt` per V4
4. **Who may do what**: the role table from Task 7, the restricted-token rules, and the detector role.
5. **Evidence**: kinds, limits (64 KiB text, 5 MiB PNG/JPEG/WebP), filing from a detector with `curl`, a JSON example:

   ```sh
   curl -H "Authorization: Bearer $DZO_TOKEN" -H 'Content-Type: application/json' \
     -d '{"steam_id":"7656119…","instance":"chernarus","kind":"log","body":"…"}' http://127.0.0.1:8080/api/v1/evidence
   ```

   Use a concrete Steam ID such as `76561198000000001` in the real file, not an ellipsis.
6. **Several installations**: the web UI's "also send to", and that evidence links stay with the first backend.
7. **Where the data lives**: `<paths.db>/dzo.sqlite` and `<paths.db>/evidence/`, plus backing them up (point to `docs/backups.rst`).

Add `moderation` to the toctree in `docs/index.rst` after `web`. In `docs/api.rst`, document every route of Tasks 8 and 9 with method, permission, body and answer. Follow the existing route tables there. In `docs/cli.rst`, add `dzo ban` and `dzo evidence` the way `dzo player` is documented, and run `make docs` to regenerate any CLI help pages it builds. In `docs/development.rst`, after the BattlEye capture paragraph, describe `scripts/test-client.sh --bans [--no-client]`: what V1–V4 check and that it needs the BattlEye client. Turn the plain-text references from Task 1 Step 6 into `:doc:\`moderation\``.

- [ ] **Step 2: Changelog**

Add a `debian/changelog` entry at the top in the existing format (copy the header line of the newest entry, bump the version as the last entries did, and use `dch` if available): "Player bans with evidence (dzo ban, dzo evidence, web UI); new dependency modernc.org/sqlite (statically linked)."

- [ ] **Step 3: Build the docs and run everything**

Run: `make docs && make check`
Expected: both pass, with no Sphinx warnings and coverage ≥ 85%. If coverage dropped, the gaps are usually the error branches in `service.go` and `api/evidence.go`. Add tests that hit them, for example a store closed under the Service, or a multipart body without a file. Never lower `COVERAGE_MIN`.

Run: `dpkg-buildpackage -us -uc -b` in a `debian:trixie` container as the `deb` CI job does, then `lintian`.
Expected: builds, and no new lintian errors.

- [ ] **Step 4: Commit**

```bash
git add docs debian/changelog
git commit -m "docs: bans, enforcement, evidence and the ban permissions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review notes

- **Spec coverage:**
  - ban by UID/IP, timed or permanent, with a reason → Tasks 3, 4, 8, 10, 15
  - edit/list → Tasks 4, 8, 10, 15
  - instance/global → Tasks 4, 8, 11
  - unban → `RevokeBan` in Tasks 4, 8, 10, 15
  - evidence (logs, images; detector) → Tasks 5, 9, 10, 15, 17
  - fan-out to backends → Task 15
  - verification V1–V4 → Tasks 1, 2, 14, 16
  - `bans.txt` rendering at start and in serve → Tasks 6, 11
  - enforcer → Task 12
  - permissions → Tasks 7, 8
  - error handling (pending sync, 400/403/404/503) → Tasks 8, 11
  - docs → Task 17

  The spec's "Ban templates" and country/ASN are non-goals.
- **Type consistency:** `BanInput`, `Ban.Input()`, `Target{Kind, Value, Derived}`, `BanFilter{State, Instance, Query, Limit, Now}` (store) vs `apiclient.BanFilter{State, Instance, Query, Limit}` (client; no `Now`, which the server sets), `api.BanResult{Ban, Sync}`, `Service.Changed(ctx, b, prev)` and `api.Moderation.Changed` have the same signature, and `moderation.Instance{Name, BEDir, RConAddr, RConPass}` is used by Task 13.
- **Known judgement calls for the executor:** the formats follow the V1/V2 captures (Tasks 6 and 11 say how to adapt). The test helpers `newEnv` in `internal/api` and `internal/web` gain fields; existing tests must stay green.

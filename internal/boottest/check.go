// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package boottest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Status is the result of one check.
type Status string

// The statuses of a check. A report fails if any check fails; warnings and
// infos are shown but do not fail it.
const (
	Pass Status = "pass"
	Fail Status = "fail"
	Warn Status = "warn"
	Info Status = "info"
)

// Check is one line of the report.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Baseline is what an unmodded server of the same build logs: how many script
// files each module loads, and the log lines that are noise on this build (a
// stripped dedicated server logs hundreds of missing-file errors). It is
// recorded by booting vanilla once (`dzo test boot --vanilla`).
type Baseline struct {
	Modules map[string]int `json:"modules"`
	Noise   []string       `json:"noise"`
}

// LoadBaseline reads a baseline file.
func LoadBaseline(path string) (*Baseline, error) {
	b, err := os.ReadFile(path) //nolint:gosec // dzo's cache
	if err != nil {
		return nil, err
	}
	var bl Baseline
	if err := json.Unmarshal(b, &bl); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &bl, nil
}

// Save writes the baseline.
func (b *Baseline) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

var (
	moduleRe  = regexp.MustCompile(`Module: (\w+); loaded (\d+)x? files`)
	scriptErr = regexp.MustCompile(`SCRIPT\s+\(E\)`)
	// Leaked 'X' script instance (1x)! and ==== Total Leaks (2x)! come at shutdown, as errors on a diag build.
	scriptLeak = regexp.MustCompile(`SCRIPT\s+\(E\): (Leaked '.*' script instance|=+ Total Leaks)`)
	// The engine's own error classes, in error.log and the RPT: "ANIMATION (E)",
	// "SCRIPT (E)", and plain words in the RPT.
	engineErr = regexp.MustCompile(`^!!!|\(E\)|(?i:\b(error|cannot|can't|failed|missing|not found|invalid)\b)`)
	// The lines that fail a boot, from what a real 1.29 server logs for
	// deliberately broken inputs (S9): the central economy marks its errors
	// with "!!!". A types.xml that does not parse, a <ce folder> or a file
	// that does not exist all end up here. "Type 'X' will be ignored" is only a
	// warning: Bohemia's own mission lists classes the server does not know.
	failing = regexp.MustCompile(`\[ERROR\]\[XML\]|\[CE\]\[offlineDB\] :: Failed to read`)
	digits  = regexp.MustCompile(`\d+`)
	stamp   = regexp.MustCompile(`^\d\d:\d\d:\d\d\s+`)
	// error.log also holds plain information ("ENTITY : Load entity type ...",
	// "WORLD : Create entity type ..."), which differs from run to run; only
	// errors and warnings count.
	flagged = regexp.MustCompile(`\((E|W)\)`)
	// Every mod without animations logs this for its own directory.
	benign = regexp.MustCompile(`skeletons\.anim\.xml|Failed to open file, line 0, column 0|failed: 0\b`)
)

// Modules returns how many script files each module loaded, from the script
// logs in dir.
func Modules(dir string) map[string]int {
	out := map[string]int{}
	files, _ := filepath.Glob(filepath.Join(dir, "script_*.log"))
	for _, f := range files {
		for _, l := range lines(f) {
			if m := moduleRe.FindStringSubmatch(l); m != nil {
				var n int
				_, _ = fmt.Sscan(m[2], &n)
				out[m[1]] = n
			}
		}
	}
	return out
}

func lines(path string) []string {
	f, err := os.Open(path) //nolint:gosec // a log in the profiles dir
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
	}
	return out
}

// fingerprint is a line without its numbers: counts and timings differ from
// run to run ("Failed to spawn the requested amount (17 < 22)").
func fingerprint(s string) string { return digits.ReplaceAllString(s, "N") }

// engineErrors returns the error lines of error.log and the RPT, without
// time stamps and duplicates, in the order first seen.
func engineErrors(dir string) []string {
	var src []string
	if l := lines(filepath.Join(dir, "error.log")); len(l) > 0 {
		for _, s := range l {
			if flagged.MatchString(s) {
				src = append(src, s)
			}
		}
	}
	rpts, _ := filepath.Glob(filepath.Join(dir, "*.RPT"))
	for _, f := range rpts {
		for _, s := range lines(f) {
			if engineErr.MatchString(stamp.ReplaceAllString(s, "")) {
				src = append(src, s)
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range src {
		s = strings.TrimSpace(stamp.ReplaceAllString(s, ""))
		if s != "" && !seen[s] && !benign.MatchString(s) {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// RecordBaseline builds the baseline of a finished vanilla run.
func RecordBaseline(dir string) *Baseline {
	var noise []string
	seen := map[string]bool{}
	for _, l := range engineErrors(dir) {
		if f := fingerprint(l); !seen[f] {
			seen[f] = true
			noise = append(noise, f)
		}
	}
	return &Baseline{Modules: Modules(dir), Noise: noise}
}

// Eval is what the log checks need to know about a run.
type Eval struct {
	Profiles    string
	Ready       bool   // the A2S probe answered
	NoMission   bool   // ... but the mission never loaded
	Exited      string // non-empty: the server exited before it was ready, and how
	Expect      []string
	Baseline    *Baseline
	ModScripts  map[string]int // script files the mods ship, per module
	Admin       *bool          // whether dzo-admin made contact; nil if not tested
	AdminDetail string         // what the test endpoint saw
}

func tail(path string, n int) string {
	l := lines(path)
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

// Evaluate runs the checks of the plan's table over the logs in a profiles dir.
func Evaluate(e Eval) []Check {
	var out []Check
	add := func(name string, s Status, format string, a ...any) {
		out = append(out, Check{Name: name, Status: s, Detail: fmt.Sprintf(format, a...)})
	}

	switch {
	case e.Exited != "":
		rpt, _ := filepath.Glob(filepath.Join(e.Profiles, "*.RPT"))
		detail := "the server " + e.Exited + " before it was ready"
		if len(rpt) > 0 {
			detail += "\n" + tail(rpt[len(rpt)-1], 15)
		}
		add("ready", Fail, "%s", detail)
	case !e.Ready:
		add("ready", Fail, "the server did not answer the Steam query in time")
	default:
		add("ready", Pass, "the server answered the Steam query")
	}

	if e.Ready && e.NoMission {
		add("mission", Fail, "the Steam query answered, but the mission's scripts never loaded")
	}
	if dumps, _ := filepath.Glob(filepath.Join(e.Profiles, "*.mdmp")); len(dumps) > 0 {
		add("crash", Fail, "crash dump %s", filepath.Base(dumps[0]))
	}

	var compile []string
	leaks := 0
	scripts, _ := filepath.Glob(filepath.Join(e.Profiles, "script_*.log"))
	for _, f := range scripts {
		for _, l := range lines(f) {
			switch {
			case scriptLeak.MatchString(l):
				leaks++ // reported when the server stops; the diag builds log them as errors
			case scriptErr.MatchString(l):
				compile = append(compile, strings.TrimSpace(l))
			}
		}
	}
	if len(compile) > 0 {
		shown := compile
		if len(shown) > 20 {
			shown = append(shown[:20:20], fmt.Sprintf("... and %d more", len(compile)-20))
		}
		add("scripts", Fail, "%d script compile error(s):\n%s", len(compile), strings.Join(shown, "\n"))
	} else if len(scripts) > 0 {
		detail := "no script compile errors"
		if leaks > 0 {
			detail += fmt.Sprintf(" (%d leak report(s) at shutdown, not counted)", leaks)
		}
		add("scripts", Pass, "%s", detail)
	} else {
		add("scripts", Fail, "no script log was written")
	}

	e.modules(add)
	e.engine(add)

	if len(e.Expect) > 0 {
		var all strings.Builder
		for _, f := range scripts {
			all.WriteString(strings.Join(lines(f), "\n"))
		}
		rpts, _ := filepath.Glob(filepath.Join(e.Profiles, "*.RPT"))
		for _, f := range rpts {
			all.WriteString(strings.Join(lines(f), "\n"))
		}
		for _, pat := range e.Expect {
			re, err := regexp.Compile(pat)
			switch {
			case err != nil:
				add("expect", Fail, "bad expression %q: %v", pat, err)
			case re.MatchString(all.String()):
				add("expect", Pass, "found %q", pat)
			default:
				add("expect", Fail, "%q is not in the script log or the RPT", pat)
			}
		}
	}
	if e.Admin != nil {
		if *e.Admin {
			add("dzo-admin", Pass, "the mod registered at the test endpoint (%s)", e.AdminDetail)
		} else {
			add("dzo-admin", Fail, "the mod did not register at the test endpoint (%s)", e.AdminDetail)
		}
	}
	return out
}

// modules compares the script file counts with the vanilla baseline: a mod
// that ships scripts for a module must raise its count, or its scripts were
// not loaded (an absolute mod path, or a PBO without a prefix).
func (e Eval) modules(add func(string, Status, string, ...any)) {
	got := Modules(e.Profiles)
	if len(got) == 0 {
		return
	}
	names := make([]string, 0, len(got))
	for m := range got {
		names = append(names, m)
	}
	sort.Strings(names)
	var counts []string
	for _, m := range names {
		counts = append(counts, fmt.Sprintf("%s %d", m, got[m]))
	}
	if e.Baseline == nil {
		add("modules", Info, "%s (no vanilla baseline: run dzo test boot --vanilla)", strings.Join(counts, ", "))
		return
	}
	failed := false
	for _, m := range []string{"Game", "World", "Mission"} {
		base, shipped := e.Baseline.Modules[m], e.ModScripts[m]
		switch {
		case shipped > 0 && got[m] <= base:
			add("modules", Fail, "%s: the mods ship %d script file(s), but the module loaded %d (vanilla %d): the scripts were not loaded", m, shipped, got[m], base)
			failed = true
		case shipped > 0 && got[m] != base+shipped:
			add("modules", Warn, "%s: %d file(s) loaded, vanilla %d plus %d shipped would be %d", m, got[m], base, shipped, base+shipped)
		}
	}
	if !failed {
		add("modules", Pass, "%s (vanilla: %s)", strings.Join(counts, ", "), baselineText(e.Baseline))
	}
}

func baselineText(b *Baseline) string {
	var parts []string
	for _, m := range []string{"Game", "World", "Mission"} {
		parts = append(parts, fmt.Sprintf("%s %d", m, b.Modules[m]))
	}
	return strings.Join(parts, ", ")
}

// engine reports error lines that the baseline does not know. Those that the
// catalogue knows to be errors fail the boot; the rest are warnings, shown
// verbatim, because which of them matter depends on the build and the mods.
func (e Eval) engine(add func(string, Status, string, ...any)) {
	noise := map[string]bool{}
	if e.Baseline != nil {
		for _, n := range e.Baseline.Noise {
			noise[fingerprint(n)] = true
		}
	}
	var fail, warn []string
	for _, l := range engineErrors(e.Profiles) {
		switch {
		case noise[fingerprint(l)]:
		case failing.MatchString(l):
			fail = append(fail, l)
		default:
			warn = append(warn, l)
		}
	}
	show := func(ls []string) string {
		if len(ls) > 20 {
			ls = append(ls[:20:20], fmt.Sprintf("... and %d more", len(ls)-20))
		}
		return strings.Join(ls, "\n")
	}
	if len(fail) > 0 {
		add("ce", Fail, "%d error(s) of the central economy or the mission files:\n%s", len(fail), show(fail))
	}
	switch {
	case len(warn) == 0 && len(fail) == 0:
		add("engine", Pass, "no new error lines in error.log and the RPT")
	case len(warn) == 0:
	case e.Baseline == nil:
		add("engine", Info, "%d error line(s); without a vanilla baseline they cannot be told from the noise a stripped server logs", len(warn))
	default:
		add("engine", Warn, "%d error line(s) vanilla does not log:\n%s", len(warn), show(warn))
	}
}

// Failed reports whether any check failed.
func Failed(cs []Check) bool {
	for _, c := range cs {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

// Text renders checks for a terminal.
func Text(cs []Check) string {
	var b strings.Builder
	for _, c := range cs {
		first, rest, _ := strings.Cut(c.Detail, "\n")
		fmt.Fprintf(&b, "%-5s %-10s %s\n", strings.ToUpper(string(c.Status)), c.Name, first)
		for _, l := range strings.Split(rest, "\n") {
			if l != "" {
				fmt.Fprintf(&b, "                 %s\n", l)
			}
		}
	}
	return b.String()
}

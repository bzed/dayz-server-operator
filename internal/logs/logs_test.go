// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package logs

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/site"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func put(t *testing.T, root, rel, body string, age time.Duration) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func rels(as []Action) string {
	var s []string
	for _, a := range as {
		s = append(s, a.Rel)
	}
	return strings.Join(s, ",")
}

func profile(t *testing.T) string {
	root := t.TempDir()
	put(t, root, "DayZServer_x64_1.RPT", "old", 5*time.Hour)
	put(t, root, "DayZServer_x64_2.RPT", "new", 1*time.Hour)
	put(t, root, "DayZServer_x64_3.RPT", "fresh", time.Minute) // too young
	put(t, root, "script_1.log", "s1", 3*time.Hour)
	put(t, root, "script_2.log", "s2", 2*time.Hour)
	put(t, root, "serverDZ.cfg", "x", 9*time.Hour)
	put(t, root, "dzo-admin/config.json", "{}", 9*time.Hour)
	put(t, root, "big.dat", strings.Repeat("x", unmatchedMin), 9*time.Hour)
	return root
}

func TestPlanKeepsNewestAndYoung(t *testing.T) {
	root := profile(t)
	p, err := MakePlan(root, site.LogsConfig{}, now)
	if err != nil {
		t.Fatal(err)
	}
	// RPT: newest (3, young) stays, 2 is the second... keep 1 = the newest only.
	if got := rels(p.Move); got != "DayZServer_x64_1.RPT,DayZServer_x64_2.RPT,script_1.log" {
		t.Fatalf("moves: %s", got)
	}
	if len(p.Unmatched) != 1 || p.Unmatched[0].Rel != "big.dat" {
		t.Fatalf("unmatched: %+v", p.Unmatched)
	}
}

func TestDefaultRulesMatchBothRPTNames(t *testing.T) {
	root := t.TempDir()
	put(t, root, "DayZServer_2026-10-04_23-37-16.RPT", "1.30 old", 5*time.Hour)
	put(t, root, "DayZServer_2026-10-05_00-00-25.RPT", "1.30 new", time.Hour)
	put(t, root, "DayZServer_2026-10-04_23-37-16.ADM", "adm old", 5*time.Hour)
	put(t, root, "DayZServer_2026-10-05_00-00-25.ADM", "adm new", time.Hour)
	p, err := MakePlan(root, site.LogsConfig{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := rels(p.Move); got != "DayZServer_2026-10-04_23-37-16.ADM,DayZServer_2026-10-04_23-37-16.RPT" {
		t.Fatalf("moves: %s", got)
	}
}

func TestExcludedFilesAreNeverMoved(t *testing.T) {
	root := profile(t)
	cfg := site.LogsConfig{Rotate: []site.LogRule{{Match: `.*`}}}
	p, err := MakePlan(root, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Move {
		if a.Rel == "serverDZ.cfg" || strings.HasPrefix(a.Rel, "dzo-admin/") {
			t.Fatalf("moved %s", a.Rel)
		}
	}
	if len(p.Excluded) != 2 {
		t.Fatalf("excluded: %v", p.Excluded)
	}
}

func TestBadRule(t *testing.T) {
	if err := Validate(site.LogsConfig{Rotate: []site.LogRule{{Match: "("}}}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := MakePlan(t.TempDir(), site.LogsConfig{Rotate: []site.LogRule{{Match: "("}}}, now); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRotateArchivesAndRemoves(t *testing.T) {
	root, arch := profile(t), t.TempDir()
	done, err := Rotate(root, arch, site.LogsConfig{}, now)
	if err != nil || len(done) != 3 {
		t.Fatalf("%v %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(root, "DayZServer_x64_1.RPT")); !os.IsNotExist(err) {
		t.Fatal("original still there")
	}
	f, err := os.Open(filepath.Join(arch, "2026-10-06", "DayZServer_x64_1.RPT.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "old" {
		t.Fatalf("content %q", b)
	}
	// Again: the name is taken, the new one gets a suffix.
	put(t, root, "script_1.log", "again", 3*time.Hour)
	put(t, root, "script_2.log", "again2", 2*time.Hour)
	if _, err := Rotate(root, arch, site.LogsConfig{}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(arch, "2026-10-06", "script_1.log.gz.1")); err != nil {
		t.Fatal(err)
	}
}

func TestRotateWithoutCompression(t *testing.T) {
	root, arch := profile(t), t.TempDir()
	if _, err := Rotate(root, arch, site.LogsConfig{Archive: site.LogArchive{Compress: "none"}}, now); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(arch, "2026-10-06", "script_1.log")); err != nil || string(b) != "s1" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestMissingProfiles(t *testing.T) {
	p, err := MakePlan(filepath.Join(t.TempDir(), "none"), site.LogsConfig{}, now)
	if err != nil || len(p.Move) != 0 {
		t.Fatalf("%v %v", p, err)
	}
}

func TestPruneByAgeAndSize(t *testing.T) {
	arch := t.TempDir()
	put(t, arch, "2026-01-01/script_1.log.gz", "aaaa", 200*24*time.Hour)
	put(t, arch, "2026-09-01/DayZServer_x64_1.mdmp.gz", "dump", 20*24*time.Hour) // rule: 14d
	put(t, arch, "2026-10-01/script_2.log.gz", "bbbb", 5*24*time.Hour)
	put(t, arch, "2026-10-02/script_3.log.gz", "cccc", 4*24*time.Hour)
	cfg := site.LogsConfig{Rotate: DefaultRules()}
	n, err := Prune(arch, cfg, now)
	if err != nil || n != 2 {
		t.Fatalf("removed %d %v", n, err)
	}
	cfg.Archive.MaxSize = 5
	n, err = Prune(arch, cfg, now)
	if err != nil || n != 1 {
		t.Fatalf("size prune removed %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(arch, "2026-10-02/script_3.log.gz")); err != nil {
		t.Fatal("the newest has to stay")
	}
	if _, err := os.Stat(filepath.Join(arch, "2026-01-01")); !os.IsNotExist(err) {
		t.Fatal("empty directory left")
	}
}

func TestListAndSize(t *testing.T) {
	arch := t.TempDir()
	put(t, arch, "2026-10-01/a.gz", "aa", 48*time.Hour)
	put(t, arch, "2026-10-05/b.gz", "bbb", time.Hour)
	es, err := List(arch, time.Time{}, nil)
	if err != nil || len(es) != 2 || es[0].Path != "2026-10-01/a.gz" {
		t.Fatalf("%v %v", es, err)
	}
	es, _ = List(arch, now.Add(-24*time.Hour), regexp.MustCompile("b"))
	if len(es) != 1 {
		t.Fatalf("%v", es)
	}
	if ArchiveSize(arch) != 5 {
		t.Fatal("size")
	}
}

func TestCrashSummary(t *testing.T) {
	root := t.TempDir()
	put(t, root, "error.log", "one\ntwo\nthree\n", time.Hour)
	put(t, root, "DayZServer_x64_1.RPT", "r1", 2*time.Hour)
	put(t, root, "DayZServer_x64_2.RPT", strings.Repeat("filler line\n", 20000)+"last rpt line\n", time.Hour)
	s := CrashSummary(root, 2)
	if !strings.Contains(s, "--- error.log") || !strings.Contains(s, "two\nthree") || strings.Contains(s, "one\n") {
		t.Fatalf("%s", s)
	}
	if !strings.Contains(s, "last rpt line") || strings.Contains(s, "r1") {
		t.Fatalf("%s", s)
	}
	if CrashSummary(t.TempDir(), 5) != "" {
		t.Fatal("no files, no summary")
	}
}

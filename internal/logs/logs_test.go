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
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func put(t testing.TB, root, rel, body string, age time.Duration) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	require.NoError(t, os.Chtimes(p, now.Add(-age), now.Add(-age)))
}

func rels(as []Action) string {
	var s []string
	for _, a := range as {
		s = append(s, a.Rel)
	}
	return strings.Join(s, ",")
}

func profile(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "DayZServer_x64_1.RPT", "old", 5*time.Hour)
	put(t, root, "DayZServer_x64_2.RPT", "new", 1*time.Hour)
	put(t, root, "DayZServer_x64_3.RPT", "fresh", time.Minute) // too young
	put(t, root, "script_1.log", "s1", 3*time.Hour)
	put(t, root, "script_2.log", "s2", 2*time.Hour)
	put(t, root, "serverDZ.cfg", "x", 9*time.Hour)
	put(t, root, "dzo/admin/config.json", "{}", 9*time.Hour)
	put(t, root, "big.dat", strings.Repeat("x", unmatchedMin), 9*time.Hour)
	return root
}

func TestPlanKeepsNewestAndYoung(t *testing.T) {
	root := profile(t)
	p, err := MakePlan(root, site.LogsConfig{}, now)
	require.NoError(t, err)
	// RPT: newest (3, young) stays, 2 is the second... keep 1 = the newest only.
	require.Equal(t, "DayZServer_x64_1.RPT,DayZServer_x64_2.RPT,script_1.log", rels(p.Move), "moves")
	require.Len(t, p.Unmatched, 1)
	require.Equal(t, "big.dat", p.Unmatched[0].Rel)
}

func TestDefaultRulesMatchBothRPTNames(t *testing.T) {
	root := t.TempDir()
	put(t, root, "DayZServer_2026-10-04_23-37-16.RPT", "1.30 old", 5*time.Hour)
	put(t, root, "DayZServer_2026-10-05_00-00-25.RPT", "1.30 new", time.Hour)
	put(t, root, "DayZServer_2026-10-04_23-37-16.ADM", "adm old", 5*time.Hour)
	put(t, root, "DayZServer_2026-10-05_00-00-25.ADM", "adm new", time.Hour)
	p, err := MakePlan(root, site.LogsConfig{}, now)
	require.NoError(t, err)
	require.Equal(t, "DayZServer_2026-10-04_23-37-16.ADM,DayZServer_2026-10-04_23-37-16.RPT", rels(p.Move), "moves")
}

func TestExcludedFilesAreNeverMoved(t *testing.T) {
	root := profile(t)
	cfg := site.LogsConfig{Rotate: []site.LogRule{{Match: `.*`}}}
	p, err := MakePlan(root, cfg, now)
	require.NoError(t, err)
	for _, a := range p.Move {
		require.NotEqual(t, "serverDZ.cfg", a.Rel, "moved")
		require.False(t, strings.HasPrefix(a.Rel, "dzo-admin/"), "moved %s", a.Rel)
	}
	require.Len(t, p.Excluded, 2)
}

func TestBadRule(t *testing.T) {
	require.Error(t, Validate(site.LogsConfig{Rotate: []site.LogRule{{Match: "("}}}))
	_, err := MakePlan(t.TempDir(), site.LogsConfig{Rotate: []site.LogRule{{Match: "("}}}, now)
	require.Error(t, err)
}

func TestRotateArchivesAndRemoves(t *testing.T) {
	root, arch := profile(t), t.TempDir()
	done, err := Rotate(root, arch, site.LogsConfig{}, now)
	require.NoError(t, err)
	require.Len(t, done, 3)
	require.NoFileExists(t, filepath.Join(root, "DayZServer_x64_1.RPT"), "original still there")
	f, err := os.Open(filepath.Join(arch, "2026-10-06", "DayZServer_x64_1.RPT.gz"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	b, _ := io.ReadAll(zr)
	require.Equal(t, "old", string(b))
	// Again: the name is taken, the new one gets a suffix.
	put(t, root, "script_1.log", "again", 3*time.Hour)
	put(t, root, "script_2.log", "again2", 2*time.Hour)
	_, err = Rotate(root, arch, site.LogsConfig{}, now)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(arch, "2026-10-06", "script_1.log.gz.1"))
}

func TestRotateWithoutCompression(t *testing.T) {
	root, arch := profile(t), t.TempDir()
	_, err := Rotate(root, arch, site.LogsConfig{Archive: site.LogArchive{Compress: "none"}}, now)
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(arch, "2026-10-06", "script_1.log"))
	require.NoError(t, err)
	require.Equal(t, "s1", string(b))
}

func TestMissingProfiles(t *testing.T) {
	p, err := MakePlan(filepath.Join(t.TempDir(), "none"), site.LogsConfig{}, now)
	require.NoError(t, err)
	require.Empty(t, p.Move)
}

func TestPruneByAgeAndSize(t *testing.T) {
	arch := t.TempDir()
	put(t, arch, "2026-01-01/script_1.log.gz", "aaaa", 200*24*time.Hour)
	put(t, arch, "2026-09-01/DayZServer_x64_1.mdmp.gz", "dump", 20*24*time.Hour) // rule: 14d
	put(t, arch, "2026-10-01/script_2.log.gz", "bbbb", 5*24*time.Hour)
	put(t, arch, "2026-10-02/script_3.log.gz", "cccc", 4*24*time.Hour)
	cfg := site.LogsConfig{Rotate: DefaultRules()}
	n, err := Prune(arch, cfg, now)
	require.NoError(t, err)
	require.Equal(t, 2, n, "removed")
	cfg.Archive.MaxSize = 5
	n, err = Prune(arch, cfg, now)
	require.NoError(t, err)
	require.Equal(t, 1, n, "size prune removed")
	require.FileExists(t, filepath.Join(arch, "2026-10-02/script_3.log.gz"), "the newest has to stay")
	require.NoDirExists(t, filepath.Join(arch, "2026-01-01"), "empty directory left")
}

func TestListAndSize(t *testing.T) {
	arch := t.TempDir()
	put(t, arch, "2026-10-01/a.gz", "aa", 48*time.Hour)
	put(t, arch, "2026-10-05/b.gz", "bbb", time.Hour)
	es, err := List(arch, time.Time{}, nil)
	require.NoError(t, err)
	require.Len(t, es, 2)
	require.Equal(t, "2026-10-01/a.gz", es[0].Path)
	es, _ = List(arch, now.Add(-24*time.Hour), regexp.MustCompile("b"))
	require.Len(t, es, 1)
	require.EqualValues(t, 5, ArchiveSize(arch), "size")
}

func TestCrashSummary(t *testing.T) {
	root := t.TempDir()
	put(t, root, "error.log", "one\ntwo\nthree\n", time.Hour)
	put(t, root, "DayZServer_x64_1.RPT", "r1", 2*time.Hour)
	put(t, root, "DayZServer_x64_2.RPT", strings.Repeat("filler line\n", 20000)+"last rpt line\n", time.Hour)
	s := CrashSummary(root, 2)
	require.Contains(t, s, "--- error.log")
	require.Contains(t, s, "two\nthree")
	require.NotContains(t, s, "one\n")
	require.Contains(t, s, "last rpt line")
	require.NotContains(t, s, "r1")
	require.Empty(t, CrashSummary(t.TempDir(), 5), "no files, no summary")
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitRepo creates a repo with one commit holding files and returns its path
// (usable as a git "URL").
func gitRepo(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, files)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	return dir
}

type tree struct{ pristine, fallback, live, manifest, history string }

func newTree(t testing.TB) tree {
	t.Helper()
	root := t.TempDir()
	return tree{
		pristine: filepath.Join(root, "servermpmissions", "m"), fallback: filepath.Join(root, "fallback"),
		live: filepath.Join(root, "mpmissions", "m"), manifest: filepath.Join(root, "mpmissions", ".dzo-manifest.json"),
		history: filepath.Join(root, "filehistory"),
	}
}

func (tr tree) render(t testing.TB, dry bool, unmanaged ...string) (Plan, Report) {
	t.Helper()
	fallback := tr.fallback
	if _, err := os.Stat(fallback); err != nil {
		fallback = "" // most tests have no fallback mission
	}
	plan, report, err := Render(RenderInput{
		PristineDir: tr.pristine, FallbackDir: fallback, LiveDir: tr.live, ManifestPath: tr.manifest,
		FileHistoryDir: tr.history, Unmanaged: unmanaged, DryRun: dry,
	})
	require.NoError(t, err, "Render")
	return plan, report
}

func TestRenderInitThenIncremental(t *testing.T) {
	tr := newTree(t)
	writeTree(t, tr.pristine, map[string]string{"db/types.xml": "v1", "init.c": "c1"})
	writeTree(t, tr.fallback, map[string]string{"cfgweather.xml": "weather", "init.c": "fallback-init", "db/types.xml": "not-a-base-file"})

	plan, _ := tr.render(t, true)
	require.Len(t, plan.Changed(), 3)
	_, err := os.Stat(tr.live)
	require.True(t, os.IsNotExist(err), "dry run must not create the live mission")

	_, rep := tr.render(t, false)
	require.Len(t, rep.Written, 3)
	got := readString(t, filepath.Join(tr.live, "init.c"))
	assert.Equal(t, "c1", got)

	// The game creates foreign files; pristine gains one, changes one.
	writeTree(t, tr.live, map[string]string{"storage_1/data.bin": "player data", "expansion/x.json": "foreign"})
	writeTree(t, tr.pristine, map[string]string{"db/types.xml": "v2", "new.xml": "n"})
	_, rep = tr.render(t, false)
	require.Len(t, rep.Written, 2)
	for f, want := range map[string]string{"storage_1/data.bin": "player data", "expansion/x.json": "foreign", "db/types.xml": "v2"} {
		got := readString(t, filepath.Join(tr.live, f))
		assert.Equal(t, want, got)
	}
	plan, _ = tr.render(t, true)
	assert.True(t, plan.Empty(), "a third render must be a no-op, plan = %+v", plan)
}

func TestRenderDriftIsBackedUp(t *testing.T) {
	tr := newTree(t)
	writeTree(t, tr.pristine, map[string]string{"cfgweather.xml": "p1"})
	tr.render(t, false)
	writeTree(t, tr.live, map[string]string{"cfgweather.xml": "admin edit"})
	writeTree(t, tr.pristine, map[string]string{"cfgweather.xml": "p2"})
	_, rep := tr.render(t, false)
	require.Len(t, rep.Drifted, 1)
	var backup string
	_ = filepath.WalkDir(tr.history, func(p string, d fs.DirEntry, _ error) error {
		if !d.IsDir() && strings.HasSuffix(p, "cfgweather.xml") {
			backup = readString(t, p)
		}
		return nil
	})
	assert.Equal(t, "admin edit", backup)
}

func TestRenderErrors(t *testing.T) {
	tr := newTree(t)
	_, _, err := Render(RenderInput{PristineDir: tr.pristine, LiveDir: tr.live, ManifestPath: tr.manifest})
	require.Error(t, err)
	writeTree(t, tr.pristine, map[string]string{"a": "1"})
	_, _, err = Render(RenderInput{PristineDir: tr.pristine, FallbackDir: tr.fallback, LiveDir: tr.live, ManifestPath: tr.manifest})
	require.Error(t, err)
	err = os.MkdirAll(filepath.Dir(tr.manifest), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(tr.manifest, []byte("{"), 0o600)
	require.NoError(t, err)
	_, _, err = Render(RenderInput{PristineDir: tr.pristine, LiveDir: tr.live, ManifestPath: tr.manifest})
	require.Error(t, err, "corrupt manifest must fail")
	_, err = os.Stat(tr.live)
	assert.True(t, os.IsNotExist(err), "a failed render must not touch the live mission")
}

func TestRenderFallbackUnreadable(t *testing.T) {
	tr := newTree(t)
	writeTree(t, tr.pristine, map[string]string{"a": "1"})
	writeTree(t, tr.fallback, map[string]string{"cfgweather.xml/x": "a dir where a file is expected"})
	_, _, err := Render(RenderInput{PristineDir: tr.pristine, FallbackDir: tr.fallback, LiveDir: tr.live, ManifestPath: tr.manifest})
	require.Error(t, err, "an unreadable fallback file must fail")
}

// snapshot maps every file under root to its content.
func snapshot(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out[filepath.ToSlash(rel)] = readString(t, p)
		}
		return nil
	})
	return out
}

// TestRenderTouchesOnlyManagedFiles is the §C6 property: over random
// pristine/live trees and repeated renders, a render only ever writes paths
// that are in the manifest or new in pristine, and never touches storage_*
// or unmanaged paths, or any file dzo does not own.
func TestRenderTouchesOnlyManagedFiles(t *testing.T) {
	names := []string{"a.xml", "b.json", "db/types.xml", "env/z.xml", "mod_1/x.xml", "expansion/e.json", "storage_1/p.bin", "storage_2/db/q", "deep/er/f"}
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		tr := newTree(t)
		unmanaged := []string{"expansion/**"}
		for round := 0; round < 4; round++ {
			pristine := map[string]string{}
			for _, n := range names {
				if rng.Intn(2) == 0 {
					pristine[n] = fmt.Sprintf("p-%d-%d", seed, rng.Intn(3))
				}
			}
			err := os.RemoveAll(tr.pristine)
			require.NoError(t, err)
			err = os.MkdirAll(tr.pristine, 0o750)
			require.NoError(t, err)
			writeTree(t, tr.pristine, pristine)
			// the game and admins write files dzo does not own
			foreign := map[string]string{
				"storage_1/p.bin": fmt.Sprintf("s-%d-%d", seed, round), "expansion/e.json": "foreign", fmt.Sprintf("mine-%d.txt", round): "x",
			}
			writeTree(t, tr.live, foreign)

			before := snapshot(t, tr.live)
			m, err := LoadManifest(tr.manifest)
			require.NoError(t, err)
			owned := map[string]bool{}
			for _, p := range m.Paths() {
				owned[p] = true
			}
			tr.render(t, false, unmanaged...)
			after := snapshot(t, tr.live)

			for path, was := range before {
				if after[path] == was {
					continue
				}
				top, _, _ := strings.Cut(path, "/")
				_, inPristine := pristine[path]
				switch {
				case strings.HasPrefix(top, "storage_"), MatchAny(unmanaged, path):
					require.Failf(t, "unmanaged file changed", "seed %d round %d: %s must never change, was %q now %q", seed, round, path, was, after[path])
				case !owned[path] && !inPristine:
					require.Failf(t, "foreign file changed", "seed %d round %d: %s", seed, round, path)
				}
			}
			for path := range after {
				top, _, _ := strings.Cut(path, "/")
				if _, existed := before[path]; existed {
					continue
				}
				_, ok := pristine[path]
				require.True(t, ok, "seed %d round %d: render created %s, which pristine does not ship (or is excluded)", seed, round, path)
				require.False(t, strings.HasPrefix(top, "storage_"), "seed %d round %d: render created %s, which pristine does not ship (or is excluded)", seed, round, path)
				require.False(t, MatchAny(unmanaged, path), "seed %d round %d: render created %s, which pristine does not ship (or is excluded)", seed, round, path)
			}
		}
	}
}

func TestFetchPristine(t *testing.T) {
	repo := gitRepo(t, map[string]string{"empty.map/init.c": "c", "empty.map/db/types.xml": "t", "other/x": "y"})
	dest := filepath.Join(t.TempDir(), "servermpmissions", "empty.map")
	err := FetchPristine(t.Context(), repo, "main", "empty.*", dest)
	require.NoError(t, err, "FetchPristine")
	got := snapshot(t, dest)
	require.Len(t, got, 2)
	assert.Equal(t, "t", got["db/types.xml"])
	// a refetch replaces the tree
	writeTree(t, dest, map[string]string{"stale": "x"})
	err = FetchPristine(t.Context(), repo, "main", "empty.map", dest)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dest, "stale"))
	assert.True(t, os.IsNotExist(err), "a refetch must replace the old pristine tree")
}

func TestFetchPristineErrors(t *testing.T) {
	repo := gitRepo(t, map[string]string{"a/f": "1", "b/f": "2", "file": "x"})
	dest := filepath.Join(t.TempDir(), "p")
	writeTree(t, dest, map[string]string{"keep": "me"})
	tests := []struct{ name, url, ref, path string }{
		{"bad ref", repo, "nope", "a"},
		{"bad url", "/nonexistent/repo", "main", "a"},
		{"glob matches two", repo, "main", "?"},
		{"no match", repo, "main", "zzz"},
		{"not a dir", repo, "main", "file"},
		{"bad glob", repo, "main", "["},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FetchPristine(t.Context(), tt.url, tt.ref, tt.path, dest)
			require.Error(t, err)
			assert.Equal(t, "me", readString(t, filepath.Join(dest, "keep")), "a failed fetch must leave the old pristine mission alone")
		})
	}
	// dest's parent cannot be created below a regular file
	blocker := filepath.Join(t.TempDir(), "f")
	err := os.WriteFile(blocker, nil, 0o600)
	require.NoError(t, err)
	err = FetchPristine(t.Context(), repo, "main", "a", filepath.Join(blocker, "x", "p"))
	require.Error(t, err)
}

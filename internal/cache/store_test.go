// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAgedGenerations creates the generations in order and backdates their
// directories one minute apart, oldest first, so that the creation order does
// not depend on the file system's timestamp granularity.
func newAgedGenerations(t testing.TB, s *Store, ids ...string) {
	t.Helper()
	base := time.Now().Add(-time.Hour)
	for i, id := range ids {
		path, err := s.NewGeneration(id)
		require.NoError(t, err, "NewGeneration(%s)", id)
		at := base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, os.Chtimes(path, at, at))
	}
}

func TestNewGenerationCreatesDir(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "store"))
	path, err := s.NewGeneration("123")
	require.NoError(t, err, "NewGeneration")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "expected a directory at %s", path)
}

func TestNewGenerationRefusesDuplicate(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.NewGeneration("123")
	require.NoError(t, err, "NewGeneration")
	_, err = s.NewGeneration("123")
	require.Error(t, err, "expected an error creating the same generation twice")
}

func TestNewGenerationRejectsReservedNames(t *testing.T) {
	s := New(t.TempDir())
	for _, name := range []string{"", "current", ".dzo-lock"} {
		_, err := s.NewGeneration(name)
		assert.Error(t, err, "expected an error for reserved name %q", name)
	}
}

func TestCurrentEmptyByDefault(t *testing.T) {
	s := New(t.TempDir())
	cur, err := s.Current()
	require.NoError(t, err, "Current")
	assert.Empty(t, cur)
}

func TestSetCurrentAndCurrent(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.NewGeneration("100")
	require.NoError(t, err, "NewGeneration")
	err = s.SetCurrent("100")
	require.NoError(t, err, "SetCurrent")
	cur, err := s.Current()
	require.NoError(t, err, "Current")
	assert.Equal(t, "100", cur)
}

func TestSetCurrentSwitchesAtomically(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{"100", "200"} {
		_, err := s.NewGeneration(id)
		require.NoError(t, err, "NewGeneration(%s)", id)
	}
	err := s.SetCurrent("100")
	require.NoError(t, err, "SetCurrent(100)")
	err = s.SetCurrent("200")
	require.NoError(t, err, "SetCurrent(200)")
	cur, _ := s.Current()
	assert.Equal(t, "200", cur)
}

func TestSetCurrentRejectsMissingGeneration(t *testing.T) {
	s := New(t.TempDir())
	err := s.SetCurrent("nope")
	require.Error(t, err, "expected an error for a nonexistent generation")
}

func TestGenerationsSortedByAge(t *testing.T) {
	s := New(t.TempDir())
	want := []string{"a", "b", "c"}
	newAgedGenerations(t, s, want...)
	gens, err := s.Generations()
	require.NoError(t, err, "Generations")
	require.Equal(t, want, gens)
}

func TestGenerationsEmptyStore(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "does-not-exist-yet"))
	gens, err := s.Generations()
	require.NoError(t, err, "Generations")
	require.Empty(t, gens)
}

func TestRemoveDeletesGeneration(t *testing.T) {
	s := New(t.TempDir())
	path, err := s.NewGeneration("100")
	require.NoError(t, err, "NewGeneration")
	err = s.Remove("100")
	require.NoError(t, err, "Remove")
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "generation directory should be gone")
}

func TestRemoveRefusesCurrent(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.NewGeneration("100")
	require.NoError(t, err, "NewGeneration")
	err = s.SetCurrent("100")
	require.NoError(t, err, "SetCurrent")
	err = s.Remove("100")
	require.Error(t, err, "expected an error removing the current generation")
}

func TestRemoveRejectsReservedNames(t *testing.T) {
	s := New(t.TempDir())
	err := s.Remove("current")
	require.Error(t, err, "expected an error removing the reserved 'current' name")
}

func TestGCKeepsMostRecentAndCurrent(t *testing.T) {
	s := New(t.TempDir())
	ids := []string{"g1", "g2", "g3", "g4", "g5"}
	newAgedGenerations(t, s, ids...)
	// g2 is old but marked current, and must survive GC even though it
	// falls outside the "keep 2 most recent" window.
	err := s.SetCurrent("g2")
	require.NoError(t, err, "SetCurrent")

	removed, err := s.GC(2)
	require.NoError(t, err, "GC")
	wantRemoved := []string{"g3", "g1"}
	require.ElementsMatch(t, wantRemoved, removed, "GC removed")

	remaining, err := s.Generations()
	require.NoError(t, err, "Generations")
	wantRemaining := []string{"g2", "g4", "g5"}
	require.ElementsMatch(t, wantRemaining, remaining)
}

func TestGCRejectsNonPositiveKeep(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.GC(0)
	require.Error(t, err, "expected an error for keep=0")
}

func TestGCOnEmptyStore(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "empty"))
	removed, err := s.GC(3)
	require.NoError(t, err, "GC")
	require.Empty(t, removed)
}

func TestCopyGeneration(t *testing.T) {
	src := t.TempDir()
	err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0o640)
	require.NoError(t, err, "write fixture")
	err = os.MkdirAll(filepath.Join(src, "sub"), 0o750)
	require.NoError(t, err, "mkdir fixture")
	err = os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("world"), 0o640)
	require.NoError(t, err, "write nested fixture")

	dst := filepath.Join(t.TempDir(), "dst")
	err = os.MkdirAll(dst, 0o750)
	require.NoError(t, err, "mkdir dst")
	err = CopyGeneration(src, dst)
	require.NoError(t, err, "CopyGeneration")

	data, err := os.ReadFile(filepath.Join(dst, "file.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	data, err = os.ReadFile(filepath.Join(dst, "sub", "nested.txt"))
	require.NoError(t, err)
	require.Equal(t, "world", string(data))
}

func TestCopyGenerationMissingSource(t *testing.T) {
	err := CopyGeneration("/nonexistent/source/dir", t.TempDir())
	require.Error(t, err, "expected an error for a missing source directory")
}

func TestFlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := Flock(path)
	require.NoError(t, err)
	unlock()
	unlock2, err := Flock(path) // free again after unlock
	require.NoError(t, err)
	unlock2()
	_, err = Flock(filepath.Join(t.TempDir(), "no", "such", "dir", "lock"))
	assert.Error(t, err, "an uncreatable lock file must fail")
}

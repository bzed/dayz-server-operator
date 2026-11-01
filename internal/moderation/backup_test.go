// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupToWritesAConsistentCopy(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "backup")
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	path, err := s.BackupTo(t.Context(), dir, now, 7)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "dzo-20261101T120000Z.sqlite"), path)
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), di.Mode().Perm())

	c, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	bans, err := c.ListBans(t.Context(), BanFilter{})
	require.NoError(t, err)
	assert.Len(t, bans, 1, "the copy holds the ban")
}

func TestBackupToKeepsTheNewest(t *testing.T) {
	s, _ := newStore(t)
	dir := filepath.Join(t.TempDir(), "backup")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	other := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(other, []byte("x"), 0o600))
	stale := filepath.Join(dir, "dzo-20261101T120000Z.sqlite.tmp")
	require.NoError(t, os.WriteFile(stale, []byte("junk"), 0o600))
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := range 9 {
		_, err := s.BackupTo(t.Context(), dir, now.Add(time.Duration(i)*time.Hour), 7)
		require.NoError(t, err)
	}
	names, err := filepath.Glob(filepath.Join(dir, "dzo-*.sqlite"))
	require.NoError(t, err)
	require.Len(t, names, 7)
	assert.Equal(t, "dzo-20261101T140000Z.sqlite", filepath.Base(names[0]), "the two oldest are gone")
	assert.FileExists(t, other)
	assert.NoFileExists(t, stale)
}

func TestLatestBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backup")
	got, err := LatestBackup(dir)
	require.NoError(t, err)
	assert.Empty(t, got, "no directory")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	got, err = LatestBackup(dir)
	require.NoError(t, err)
	assert.Empty(t, got, "no copy")
	for _, n := range []string{"dzo-20261101T120000Z.sqlite", "dzo-20261103T120000Z.sqlite", "dzo-20261102T120000Z.sqlite", "other.sqlite"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), nil, 0o600))
	}
	got, err = LatestBackup(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "dzo-20261103T120000Z.sqlite"), got)
}

func TestServiceBacksUpWhenDue(t *testing.T) {
	e := newSvc(t)
	dir := filepath.Join(t.TempDir(), "backup")
	e.svc.BackupDir, e.svc.BackupEvery, e.svc.BackupKeep = dir, 24*time.Hour, 7
	count := func() int {
		names, _ := filepath.Glob(filepath.Join(dir, "dzo-*.sqlite"))
		return len(names)
	}
	sweep := func(at time.Time) {
		e.setNow(at)
		e.tick <- at
		e.tick <- at // taken only after the first sweep finished
	}
	sweep(t0)
	require.Equal(t, 1, count(), "the first sweep copies")
	sweep(t0.Add(time.Hour))
	require.Equal(t, 1, count(), "not due yet")
	sweep(t0.Add(24 * time.Hour))
	require.Equal(t, 2, count(), "due again")
}

func TestServiceBackupOffWritesNothing(t *testing.T) {
	e := newSvc(t)
	dir := filepath.Join(t.TempDir(), "backup")
	e.svc.BackupDir, e.svc.BackupEvery = dir, 0
	e.tick <- t0
	e.tick <- t0
	assert.NoDirExists(t, dir)
}

func TestBackupNamesOnlyTakeTheStampedForm(t *testing.T) {
	s, _ := newStore(t)
	dir := filepath.Join(t.TempDir(), "backup")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for _, n := range []string{"dzo-before-upgrade.sqlite", "dzo-20261101T120000Z.sqlite.bak", "dzo-2026.sqlite", "dzo-99999999T999999Z.sqlite"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600))
	}
	latest, err := LatestBackup(dir)
	require.NoError(t, err)
	assert.Empty(t, latest, "a hand-saved copy is not a rotating backup")

	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		_, err := s.BackupTo(t.Context(), dir, now.Add(time.Duration(i)*time.Hour), 1)
		require.NoError(t, err)
	}
	for _, n := range []string{"dzo-before-upgrade.sqlite", "dzo-2026.sqlite", "dzo-99999999T999999Z.sqlite"} {
		assert.FileExists(t, filepath.Join(dir, n), "rotation leaves %s alone", n)
	}
	latest, err = LatestBackup(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "dzo-20261101T140000Z.sqlite"), latest)
}

func TestBackupToTightensAnExistingDirectory(t *testing.T) {
	s, _ := newStore(t)
	dir := filepath.Join(t.TempDir(), "backup")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.Chmod(dir, 0o755)) // whatever the umask was
	_, err := s.BackupTo(t.Context(), dir, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), 7)
	require.NoError(t, err)
	di, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), di.Mode().Perm())
}

func TestBackupToCopyIsNeverWiderThan0600(t *testing.T) {
	s, _ := newStore(t)
	dir := filepath.Join(t.TempDir(), "backup")
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	path, err := s.BackupTo(t.Context(), dir, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), 7)
	require.NoError(t, err)
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

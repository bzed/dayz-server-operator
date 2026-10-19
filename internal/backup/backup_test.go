// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/btrfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFS copies directories where btrfs would snapshot them.
type fakeFS struct {
	free         uint64
	failSnapshot bool
	failDelete   map[string]int // path suffix -> how many times Delete fails
	deleted      []string
}

func (f *fakeFS) Snapshot(src, dst string, _ bool) error {
	if f.failSnapshot {
		return errors.New("snapshot failed")
	}
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("exists")
	}
	return copyTree(src, dst)
}
func (f *fakeFS) SetReadOnly(string, bool) error { return nil }
func (f *fakeFS) Delete(p string) error {
	for suffix, n := range f.failDelete {
		if strings.HasSuffix(p, suffix) && n > 0 {
			f.failDelete[suffix]--
			return errors.New("busy")
		}
	}
	f.deleted = append(f.deleted, filepath.Base(p))
	return os.RemoveAll(p)
}
func (f *fakeFS) FreeBytes(string) (uint64, error) { return f.free, nil }

type env struct {
	t   testing.TB
	m   *Manager
	fs  *fakeFS
	now time.Time
}

func write(t testing.TB, path, body string) {
	t.Helper()
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(path, []byte(body), 0o600)
	require.NoError(t, err)
}

func read(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func newEnv(t testing.TB) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, fs: &fakeFS{free: 1 << 40}, now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	e.m = &Manager{
		Instance: "hashima", InstanceDir: filepath.Join(root, "instances", "hashima"), Root: filepath.Join(root, "snapshots", "hashima"),
		Policy: Policy{Keep: 20, MinKeep: 1}, FS: e.fs, Now: func() time.Time { return e.now },
	}
	write(t, filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "data"), "v1")
	write(t, filepath.Join(e.m.InstanceDir, "profiles", "log"), "l")
	return e
}

func (e *env) create(reason Reason) Snapshot {
	e.t.Helper()
	e.now = e.now.Add(time.Minute)
	res, err := e.m.Create(e.t.Context(), reason)
	require.NoError(e.t, err, "create %s", reason)
	return res.Snapshot
}

func TestCreateRecordsASnapshot(t *testing.T) {
	e := newEnv(t)
	s := e.create(Manual)
	require.Equal(t, Complete, s.State)
	require.Equal(t, Manual, s.Reason)
	require.True(t, strings.HasSuffix(s.ID, "-manual"))
	require.True(t, strings.HasPrefix(s.ID, "20261001-120100"))
	assert.Equal(t, "v1", read(filepath.Join(e.m.Root, s.ID, "mpmissions", "m", "storage_1", "data")))
	write(t, filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "data"), "v2")
	assert.Equal(t, "v1", read(filepath.Join(e.m.Root, s.ID, "mpmissions", "m", "storage_1", "data")))
	ss, err := e.m.List()
	require.NoError(t, err)
	require.Len(t, ss, 1)
	require.Equal(t, s.ID, ss[0].ID)
	// the same second, the same reason: a different id
	e.m.Now = func() time.Time { return e.now }
	r2, err := e.m.Create(t.Context(), Manual)
	require.NoError(t, err)
	require.NotEqual(t, s.ID, r2.Snapshot.ID)
	assert.True(t, strings.HasSuffix(r2.Snapshot.ID, "-manual-2"))
	_, err = e.m.Create(t.Context(), "bogus")
	assert.Error(t, err, "an unknown reason must be refused")
}

func TestFailedCreateLeavesNothingBehind(t *testing.T) {
	e := newEnv(t)
	e.fs.failSnapshot = true
	_, err := e.m.Create(t.Context(), Manual)
	require.Error(t, err, "must fail")
	ss, _ := e.m.List()
	assert.Empty(t, ss)
}

func TestRetentionRunsAfterACreate(t *testing.T) {
	e := newEnv(t)
	e.m.Policy = Policy{Keep: 2, MinKeep: 1}
	first := e.create(Manual)
	err := e.m.Pin(first.ID, true)
	require.NoError(t, err)
	for i := 0; i < 4; i++ {
		e.create(Scheduled)
	}
	ss, _ := e.m.List()
	// keep is the total: the pinned one and the newest
	assert.Len(t, ss, 2, "want the pinned and the newest")
	_, err = os.Stat(filepath.Join(e.m.Root, first.ID))
	assert.NoError(t, err)
	assert.Len(t, e.fs.deleted, 3)
}

func TestPruneDryRunAndInterruptedWork(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 4; i++ {
		e.create(Manual)
	}
	e.m.Policy = Policy{Keep: 2, MinKeep: 1}
	would, err := e.m.Prune(true)
	require.NoError(t, err)
	require.Len(t, would, 2)
	require.Empty(t, e.fs.deleted)

	// a deletion that fails stays "deleting" and is finished by the next run
	e.fs.failDelete = map[string]int{would[0].ID: 1}
	_, err = e.m.Prune(false)
	require.Error(t, err, "a failing delete must be reported")
	raw, _ := e.m.load() // without the recovery that every operation starts with
	var states []State
	for _, s := range raw {
		states = append(states, s.State)
	}
	require.Len(t, raw, 3)
	require.Contains(t, states, Deleting)
	ss, _ := e.m.List() // the next operation finishes the deletion
	assert.Len(t, ss, 2, "the resumed deletion must finish")

	// an interrupted creation is cleaned up
	write(t, filepath.Join(e.m.Root, "20261001-130000-manual", "half"), "x")
	ss, _ = e.m.List()
	ss = append(ss, Snapshot{ID: "20261001-130000-manual", Reason: Manual, State: Creating, Created: e.now})
	err = e.m.save(ss)
	require.NoError(t, err)
	ss, _ = e.m.List()
	assert.Len(t, ss, 2)
	_, err = os.Stat(filepath.Join(e.m.Root, "20261001-130000-manual"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMissingSnapshotsAreMarked(t *testing.T) {
	e := newEnv(t)
	s := e.create(Manual)
	err := os.RemoveAll(filepath.Join(e.m.Root, s.ID))
	require.NoError(t, err)
	got, err := e.m.Get(s.ID)
	require.NoError(t, err)
	assert.Equal(t, Missing, got.State)
	_, err = e.m.Path(s.ID)
	assert.Error(t, err, "a missing snapshot has no path")
	_, err = e.m.Get("nope")
	assert.Error(t, err, "unknown id")
}

func TestOrphansAreReportedNotDeleted(t *testing.T) {
	e := newEnv(t)
	e.create(Manual)
	write(t, filepath.Join(e.m.Root, "20260101-000000-manual", "f"), "x")
	write(t, filepath.Join(e.m.Root, "stranger", "f"), "x")
	o, err := e.m.Orphans()
	require.NoError(t, err)
	require.Len(t, o, 2)
	_, err = e.m.Prune(false)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(e.m.Root, "stranger"))
	assert.NoError(t, err)
	err = e.m.AdoptOrphans([]string{"20260101-000000-manual"})
	require.NoError(t, err)
	s, err := e.m.Get("20260101-000000-manual")
	require.NoError(t, err)
	require.Equal(t, 2026, s.Created.Year())
	assert.Equal(t, time.January, s.Created.Month())
	err = e.m.DeleteOrphans([]string{"stranger"})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(e.m.Root, "stranger"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	err = e.m.DeleteOrphans([]string{e.create(Manual).ID})
	assert.Error(t, err, "an indexed snapshot is not an orphan")
	err = e.m.AdoptOrphans([]string{"nothing"})
	assert.Error(t, err, "adopting a name that is not an orphan must fail")
}

func TestSafePathRefusesAnythingButAChild(t *testing.T) {
	e := newEnv(t)
	for _, bad := range []string{"", "..", ".", "a/b", "../x", indexFile, lockFile} {
		_, err := e.m.safePath(bad)
		assert.Error(t, err, "%q must be refused", bad)
	}
	err := os.MkdirAll(e.m.Root, 0o750)
	require.NoError(t, err)
	err = os.Symlink(e.m.InstanceDir, filepath.Join(e.m.Root, "link"))
	require.NoError(t, err)
	_, err = e.m.safePath("link")
	assert.Error(t, err, "a symlink must be refused: deleting it must never reach the instance")
}

func TestFreeSpaceIsMadeBeforeASnapshot(t *testing.T) {
	e := newEnv(t)
	e.m.Policy = Policy{Keep: 20, MinKeep: 2, MinFreeBytes: 100}
	for i := 0; i < 4; i++ {
		e.create(Manual)
	}
	e.fs.free = 10 // too little
	res, err := e.m.Create(t.Context(), Manual)
	require.NoError(t, err)
	ss, _ := e.m.List()
	// room is made down to min_keep 2, then the new one is taken; free space never improves in this fake
	require.Len(t, ss, 3)
	require.NotEmpty(t, res.Warnings)
	assert.Contains(t, res.Warnings[0], "nothing may be pruned")
}

func TestPostBackupHook(t *testing.T) {
	e := newEnv(t)
	var got string
	e.m.PostBackup = func(_ context.Context, p string) error { got = p; return errors.New("restic failed") }
	e.now = e.now.Add(time.Minute)
	res, err := e.m.Create(t.Context(), Manual)
	require.NoError(t, err, "a failing hook must not fail the backup")
	require.Equal(t, filepath.Join(e.m.Root, res.Snapshot.ID), got)
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "post_backup hook: restic failed")
}

func TestRestoreFull(t *testing.T) {
	e := newEnv(t)
	s := e.create(Manual)
	write(t, filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "data"), "v2-newer")
	write(t, filepath.Join(e.m.InstanceDir, "extra"), "only live")

	var order []string
	e.now = e.now.Add(time.Hour)
	res, err := e.m.Restore(t.Context(), s.ID, RestoreOptions{
		Stop:  func(context.Context) error { order = append(order, "stop"); return nil },
		Start: func(context.Context) error { order = append(order, "start"); return nil },
	})
	require.NoError(t, err)
	assert.Equal(t, "stop,start", strings.Join(order, ","))
	assert.Equal(t, "v1", read(filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "data")))
	_, err = os.Stat(filepath.Join(e.m.InstanceDir, "extra"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	ss, _ := e.m.List()
	reasons := map[Reason]bool{}
	for _, x := range ss {
		reasons[x.Reason] = true
	}
	require.True(t, reasons[PreRestore])
	require.True(t, reasons[Replaced])
	assert.Equal(t, PreRestore, res.Snapshot.Reason)
	for _, x := range ss {
		if x.Reason == Replaced {
			assert.Equal(t, "only live", read(filepath.Join(e.m.Root, x.ID, "extra")), "the replaced snapshot must hold what was live before")
		}
	}
	aside, _ := filepath.Glob(filepath.Join(filepath.Dir(e.m.InstanceDir), ".hashima.replaced-*"))
	assert.Empty(t, aside)
}

func TestRestoreFullFailureLeavesTheInstanceAlone(t *testing.T) {
	e := newEnv(t)
	s := e.create(Manual)
	write(t, filepath.Join(e.m.InstanceDir, "marker"), "live")
	calls := 0
	e.m.FS = &countingFS{fakeFS: e.fs, failOn: 2, calls: &calls} // the safety snapshot works, the restore does not
	_, err := e.m.Restore(t.Context(), s.ID, RestoreOptions{})
	require.ErrorContains(t, err, "the instance is as it was")
	assert.Equal(t, "live", read(filepath.Join(e.m.InstanceDir, "marker")))
	// the safety snapshot failing stops the restore before anything happens
	e.fs.failSnapshot = true
	e.m.FS = e.fs
	_, err = e.m.Restore(t.Context(), s.ID, RestoreOptions{})
	assert.ErrorContains(t, err, "nothing was restored")
	_, err = e.m.Restore(t.Context(), "nope", RestoreOptions{})
	assert.Error(t, err, "unknown snapshot")
	stopErr := errors.New("won't stop")
	e.fs.failSnapshot = false
	_, err = e.m.Restore(t.Context(), s.ID, RestoreOptions{Stop: func(context.Context) error { return stopErr }})
	assert.ErrorIs(t, err, stopErr)
}

// countingFS fails the failOn-th Snapshot call.
type countingFS struct {
	*fakeFS
	failOn int
	calls  *int
}

func (c *countingFS) Snapshot(src, dst string, ro bool) error {
	*c.calls++
	if *c.calls == c.failOn {
		return errors.New("no space left")
	}
	return c.fakeFS.Snapshot(src, dst, ro)
}

func TestRestorePath(t *testing.T) {
	e := newEnv(t)
	s := e.create(Manual)
	write(t, filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "data"), "v2")
	write(t, filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "newfile"), "n")
	write(t, filepath.Join(e.m.InstanceDir, "profiles", "log"), "keep me")
	_, err := e.m.Restore(t.Context(), s.ID, RestoreOptions{Path: "mpmissions/m/storage_1"})
	require.NoError(t, err)
	assert.Equal(t, "v1", read(filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "data")))
	_, err = os.Stat(filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "newfile"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Equal(t, "keep me", read(filepath.Join(e.m.InstanceDir, "profiles", "log")))
	old, _ := filepath.Glob(filepath.Join(e.m.InstanceDir, "mpmissions", "m", "*.dzo-restore-old"))
	assert.Empty(t, old)
	for _, bad := range []string{"../other", "/etc", "..", ".", "a/../../b"} {
		_, err := e.m.Restore(t.Context(), s.ID, RestoreOptions{Path: bad})
		assert.Error(t, err, "path %q must be refused", bad)
	}
	_, err = e.m.Restore(t.Context(), s.ID, RestoreOptions{Path: "not/in/backup"})
	assert.Error(t, err, "a path that is not in the snapshot must be refused before anything happens")
	ss, _ := e.m.List()
	assert.Equal(t, 1, countReason(ss, PreRestore))
}

func countReason(ss []Snapshot, r Reason) int {
	n := 0
	for _, s := range ss {
		if s.Reason == r {
			n++
		}
	}
	return n
}

func TestDiff(t *testing.T) {
	e := newEnv(t)
	a := e.create(Manual)
	write(t, filepath.Join(e.m.InstanceDir, "mpmissions", "m", "storage_1", "data"), "v2-longer")
	write(t, filepath.Join(e.m.InstanceDir, "new"), "n")
	err := os.Remove(filepath.Join(e.m.InstanceDir, "profiles", "log"))
	require.NoError(t, err)
	got, err := e.m.DiffWith(a.ID, "live")
	require.NoError(t, err)
	want := map[string]string{"mpmissions/m/storage_1/data": "~", "new": "+", "profiles/log": "-"}
	require.Len(t, got, 3)
	for _, c := range got {
		assert.Equal(t, c.Kind, want[c.Path])
	}
	b := e.create(Manual)
	same, _ := e.m.DiffWith(b.ID, "")
	assert.Empty(t, same)
	_, err = e.m.DiffWith("nope", "")
	assert.Error(t, err, "unknown snapshot")
}

// On a real btrfs filesystem (the user's cache directory, or DZO_BTRFS_TESTDIR).
func TestOnRealBtrfs(t *testing.T) {
	base := os.Getenv("DZO_BTRFS_TESTDIR")
	if base == "" {
		c, err := os.UserCacheDir()
		if err != nil {
			t.Skip("no cache dir")
		}
		base = c
	}
	if ok, err := btrfs.IsBtrfs(base); err != nil || !ok {
		t.Skipf("%s is not on btrfs; set DZO_BTRFS_TESTDIR", base)
	}
	dir, err := os.MkdirTemp(base, "dzo-backup-test-")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() {
		es, _ := os.ReadDir(filepath.Join(dir, "snapshots", "x"))
		for _, e := range es {
			_ = btrfs.Delete(filepath.Join(dir, "snapshots", "x", e.Name()))
		}
		es, _ = os.ReadDir(filepath.Join(dir, "instances"))
		for _, e := range es {
			_ = btrfs.Delete(filepath.Join(dir, "instances", e.Name()))
		}
		_ = os.RemoveAll(dir)
	})
	err = os.MkdirAll(filepath.Join(dir, "instances"), 0o750)
	require.NoError(t, err)
	inst := filepath.Join(dir, "instances", "x")
	err = btrfs.CreateSubvolume(inst)
	require.NoError(t, err)
	write(t, filepath.Join(inst, "mpmissions", "m", "storage_1", "data"), "v1")

	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := &Manager{Instance: "x", InstanceDir: inst, Root: filepath.Join(dir, "snapshots", "x"), FS: Btrfs{},
		Policy: Policy{Keep: 2, MinKeep: 1}, Now: func() time.Time { clock = clock.Add(time.Minute); return clock }}
	snaps := []Snapshot{}
	for i := 0; i < 3; i++ {
		res, err := m.Create(t.Context(), Manual)
		require.NoError(t, err)
		snaps = append(snaps, res.Snapshot)
	}
	ss, _ := m.List()
	require.Len(t, ss, 2)
	ro, err := btrfs.ReadOnly(filepath.Join(m.Root, snaps[2].ID))
	require.NoError(t, err)
	assert.True(t, ro)

	write(t, filepath.Join(inst, "mpmissions", "m", "storage_1", "data"), "v2")
	_, err = m.Restore(t.Context(), snaps[2].ID, RestoreOptions{Path: "mpmissions/m/storage_1"})
	require.NoError(t, err)
	assert.Equal(t, "v1", read(filepath.Join(inst, "mpmissions", "m", "storage_1", "data")))
	write(t, filepath.Join(inst, "marker"), "live")
	_, err = m.Restore(t.Context(), snaps[2].ID, RestoreOptions{})
	require.NoError(t, err)
	ok, _ := btrfs.IsSubvolume(inst)
	assert.True(t, ok)
	_, err = os.Stat(filepath.Join(inst, "marker"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	err = os.WriteFile(filepath.Join(inst, "writable"), []byte("x"), 0o600)
	assert.NoError(t, err, "the restored instance must be writable")
	var replaced Snapshot
	ss, _ = m.List()
	for _, s := range ss {
		if s.Reason == Replaced {
			replaced = s
		}
	}
	require.NotEmpty(t, replaced.ID)
	assert.Equal(t, "live", read(filepath.Join(m.Root, replaced.ID, "marker")))
	ro, _ = btrfs.ReadOnly(filepath.Join(m.Root, replaced.ID))
	assert.True(t, ro)
}

func TestRestoreSourceSurvivesTheRetention(t *testing.T) {
	e := newEnv(t)
	e.m.Policy = Policy{Keep: 2, MinKeep: 1}
	oldest := e.create(Manual)
	e.create(Manual)
	e.create(Manual) // retention has already removed the oldest
	ss, _ := e.m.List()
	require.Len(t, ss, 2)
	require.NotEqual(t, oldest.ID, ss[0].ID)
	src := ss[0] // the older of the two: the first the retention would delete next
	_, err := e.m.Restore(t.Context(), src.ID, RestoreOptions{})
	require.NoError(t, err, "restoring the snapshot the retention would delete next must work")
	// the pin is lifted afterwards
	got, err := e.m.Get(src.ID)
	if err == nil {
		assert.False(t, got.Pinned, "the restore must not leave the snapshot pinned")
	}
}

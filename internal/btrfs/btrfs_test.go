// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package btrfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// testDir returns a scratch directory on a btrfs filesystem, or skips: set
// DZO_BTRFS_TESTDIR to a directory on btrfs that the test user may write to.
// Without it the user's cache directory is tried.
func testDir(t testing.TB) string {
	t.Helper()
	base := os.Getenv("DZO_BTRFS_TESTDIR")
	if base == "" {
		c, err := os.UserCacheDir()
		if err != nil {
			t.Skip("no cache dir")
		}
		base = c
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Skip(err)
	}
	if ok, err := IsBtrfs(base); err != nil || !ok {
		t.Skipf("%s is not on btrfs; set DZO_BTRFS_TESTDIR", base)
	}
	dir, err := os.MkdirTemp(base, "dzo-btrfs-test-")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() {
		// snapshots and subvolumes need Delete, RemoveAll cannot remove a subvolume with the read-only flag
		es, _ := os.ReadDir(dir)
		for _, e := range es {
			_ = Delete(filepath.Join(dir, e.Name()))
		}
		_ = os.RemoveAll(dir)
	})
	return dir
}

func TestSubvolumeSnapshotAndDelete(t *testing.T) {
	dir := testDir(t)
	sub := filepath.Join(dir, "inst")
	require.NoError(t, CreateSubvolume(sub), "create")
	ok, err := IsSubvolume(sub)
	require.NoError(t, err)
	require.True(t, ok, "a created subvolume must be one")
	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.Mkdir(plain, 0o750))
	isSub, _ := IsSubvolume(plain)
	assert.False(t, isSub, "a plain directory is not a subvolume")
	require.NoError(t, os.MkdirAll(filepath.Join(sub, "mpmissions", "m", "storage_1"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "mpmissions", "m", "storage_1", "data"), []byte("v1"), 0o600))

	snap := filepath.Join(dir, "20261002-120000-manual")
	require.NoError(t, Snapshot(sub, snap, true), "snapshot")
	ro, err := ReadOnly(snap)
	require.NoError(t, err)
	require.True(t, ro, "a read-only snapshot must be read-only")
	b, _ := os.ReadFile(filepath.Join(snap, "mpmissions", "m", "storage_1", "data"))
	assert.Equal(t, "v1", string(b), "snapshot content")
	err = os.WriteFile(filepath.Join(snap, "x"), []byte("x"), 0o600)
	assert.ErrorIs(t, err, unix.EROFS, "writing into a read-only snapshot must fail with EROFS")
	// the live data moves on, the snapshot does not
	require.NoError(t, os.WriteFile(filepath.Join(sub, "mpmissions", "m", "storage_1", "data"), []byte("v2"), 0o600))
	b, _ = os.ReadFile(filepath.Join(snap, "mpmissions", "m", "storage_1", "data"))
	assert.Equal(t, "v1", string(b), "a snapshot must not follow the live data")

	// a writable snapshot of the backup is how a restore brings it back
	restored := filepath.Join(dir, "restored")
	require.NoError(t, Snapshot(snap, restored, false))
	assert.NoError(t, os.WriteFile(filepath.Join(restored, "x"), []byte("x"), 0o600), "a restored instance must be writable")

	for _, p := range []string{snap, restored, sub} {
		require.NoError(t, Delete(p), "delete %s", p)
		assert.NoDirExists(t, p, "still exists after Delete")
	}
}

func TestSetReadOnlyRoundTrip(t *testing.T) {
	dir := testDir(t)
	sub := filepath.Join(dir, "s")
	require.NoError(t, CreateSubvolume(sub))
	require.NoError(t, SetReadOnly(sub, true))
	ro, _ := ReadOnly(sub)
	assert.True(t, ro, "read-only flag not set")
	require.NoError(t, SetReadOnly(sub, false))
	ro, _ = ReadOnly(sub)
	assert.False(t, ro, "read-only flag not cleared")
}

func TestErrors(t *testing.T) {
	dir := testDir(t)
	assert.Error(t, Snapshot(filepath.Join(dir, "missing"), filepath.Join(dir, "x"), true), "a snapshot of a missing source must fail")
	plain := filepath.Join(dir, "plain")
	_ = os.Mkdir(plain, 0o750)
	assert.Error(t, Snapshot(plain, filepath.Join(dir, "x"), true), "a plain directory cannot be snapshotted")
	sub := filepath.Join(dir, "s")
	require.NoError(t, CreateSubvolume(sub))
	assert.Error(t, CreateSubvolume(sub), "creating a subvolume that exists must fail")
	for _, bad := range []string{"", "a/b", strings.Repeat("x", 5000)} {
		err := CreateSubvolume(filepath.Join(dir, bad))
		if bad != "" {
			assert.Error(t, err, "name %q must be refused", bad)
		}
	}
	assert.Error(t, Delete(filepath.Join(dir, "nothing")), "deleting a missing path must fail")
	ok, err := IsBtrfs("/proc")
	require.NoError(t, err)
	assert.False(t, ok, "/proc is not btrfs")
	_, err = IsBtrfs(filepath.Join(dir, "nope"))
	assert.Error(t, err, "a missing path is an error")
}

func TestSameFilesystemAndMountOptions(t *testing.T) {
	dir := testDir(t)
	same, err := SameFilesystem(dir, filepath.Join(dir))
	require.NoError(t, err)
	assert.True(t, same, "a directory is on its own filesystem")
	same, err = SameFilesystem(dir, "/proc")
	require.NoError(t, err)
	assert.False(t, same, "/proc is another filesystem")
	opts, err := MountOptions(dir)
	require.NoError(t, err)
	assert.True(t, strings.Contains(opts, "btrfs") || strings.Contains(opts, "rw"), "mount options of a btrfs dir: %q", opts)
	_ = UserSubvolRmAllowed(dir) // either is fine; it must not panic
}

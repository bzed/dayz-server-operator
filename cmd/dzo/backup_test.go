// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/btrfs"
)

// btrfsData returns a data directory on btrfs (DZO_BTRFS_TESTDIR, or the
// user's cache directory), or skips. Subvolumes need btrfs.Delete to go.
func btrfsData(t testing.TB) string {
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
	if ok, err := btrfs.IsBtrfs(base); err != nil || !ok {
		t.Skipf("%s is not on btrfs; set DZO_BTRFS_TESTDIR", base)
	}
	data, err := os.MkdirTemp(base, "dzo-cli-test-") //nolint:usetesting // must live on the btrfs mount, t.TempDir() does not
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() {
		for _, sub := range []string{"snapshots/x", "instances"} {
			es, _ := os.ReadDir(filepath.Join(data, sub))
			for _, e := range es {
				_ = btrfs.Delete(filepath.Join(data, sub, e.Name()))
			}
		}
		_ = os.RemoveAll(data)
	})
	return data
}

func TestBackupAndRestoreThroughTheCLI(t *testing.T) {
	data := btrfsData(t)
	cfg, root, _ := renderSetupIn(t, data)
	out, err := runCmd(t, "backup", "create", "x", "--config", cfg)
	require.Error(t, err, "an instance that was never rendered is not a subvolume yet: %s", out)
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "render: %s", out)
	ok, err := btrfs.IsSubvolume(root)
	require.NoError(t, err)
	require.True(t, ok, "render must create the instance as a subvolume")
	live := filepath.Join(root, "mpmissions", "empty.m")
	storage := filepath.Join(live, "storage_1", "persistence")
	writeFile(t, storage, "player data v1")

	out, err = runCmd(t, "backup", "create", "x", "--config", cfg)
	require.NoError(t, err, "create: %s", out)
	id := strings.Fields(out)[0]
	require.True(t, strings.HasSuffix(id, "-manual"), "snapshot id = %q", id)
	out, err = runCmd(t, "backup", "list", "x", "--config", cfg)
	require.NoError(t, err, "list: %s", out)
	require.Contains(t, out, id, "list")
	require.Contains(t, out, "complete", "list")
	writeFile(t, storage, "player data v2, changed")
	out, err = runCmd(t, "backup", "diff", "x", id, "--config", cfg)
	require.NoError(t, err, "diff: %s", out)
	require.Contains(t, out, "~ mpmissions/empty.m/storage_1/persistence", "diff")
	_, err = runCmd(t, "backup", "pin", "x", id, "--config", cfg)
	require.NoError(t, err)
	out, _ = runCmd(t, "backup", "list", "x", "--config", cfg)
	assert.Contains(t, out, "pinned", "pinned snapshots are marked")
	_, err = runCmd(t, "backup", "unpin", "x", id, "--config", cfg)
	require.NoError(t, err)
	out, err = runCmd(t, "backup", "prune", "--dry-run", "--config", cfg)
	require.NoError(t, err, "one snapshot is within the policy: %s", out)
	assert.NotContains(t, out, "would prune", "one snapshot is within the policy")

	// a path restore, then a full restore, with the instance not running (no systemd here)
	lcFlags := []string{"--command", "false"} // `false is-active` fails: the unit is not active
	args := append([]string{"restore", "x", id, "--path", "mpmissions/empty.m/storage_1", "--config", cfg}, lcFlags...)
	out, err = runCmd(t, args...)
	require.NoError(t, err, "restore --path: %s", out)
	require.Contains(t, out, "restored", "restore --path")
	b, _ := os.ReadFile(storage)
	assert.Equal(t, "player data v1", string(b), "the path must be back")
	writeFile(t, filepath.Join(root, "marker"), "live")
	args = append([]string{"restore", "x", id, "--config", cfg}, lcFlags...)
	out, err = runCmd(t, args...)
	require.NoError(t, err, "restore: %s", out)
	_, err = os.Stat(filepath.Join(root, "marker"))
	require.ErrorIs(t, err, os.ErrNotExist, "a full restore brings back the backup")
	ok, _ = btrfs.IsSubvolume(root)
	assert.True(t, ok, "the restored instance is a subvolume")
	out, _ = runCmd(t, "backup", "list", "x", "--config", cfg)
	for _, reason := range []string{"pre_restore", "replaced"} {
		assert.Contains(t, out, reason, "a restore leaves a %s snapshot", reason)
	}
	_, err = runCmd(t, "backup", "create", "x", "--reason", "bogus", "--config", cfg)
	assert.Error(t, err, "an unknown reason must fail")
}

func TestRenderTakesTheSnapshotsThePolicyAsks(t *testing.T) {
	data := btrfsData(t)
	cfg, _, _ := renderSetupIn(t, data)
	site := filepath.Join(data, "site", "instances", "x", "instance.yaml")
	b, _ := os.ReadFile(site)
	writeFile(t, site, string(b)+"backup: {before: [render, mission_update]}\n")

	out, err := runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "the first render has nothing to protect: %s", out)
	require.NotContains(t, out, "snapshot ", "the first render has nothing to protect")
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "a later render: %s", out)
	require.Equal(t, 1, strings.Count(out, "snapshot "), "a later render takes the render snapshot: %s", out)
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg, "--update-pristine")
	require.NoError(t, err, "--update-pristine: %s", out)
	require.Equal(t, 2, strings.Count(out, "snapshot "), "--update-pristine adds the mission_update snapshot: %s", out)
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg, "--dry-run")
	require.NoError(t, err, "a dry run: %s", out)
	require.NotContains(t, out, "snapshot ", "a dry run changes nothing")
}

func TestBackupNeedsBtrfs(t *testing.T) {
	cfg, _, _ := renderSetup(t) // t.TempDir may or may not be btrfs
	if ok, _ := btrfs.IsBtrfs(filepath.Dir(cfg)); ok {
		t.Skip("the temp dir is on btrfs")
	}
	out, err := runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "a render works without btrfs: %s", out)
	_, err = runCmd(t, "backup", "create", "x", "--config", cfg)
	assert.ErrorContains(t, err, "btrfs", "a backup without btrfs must say so")
}

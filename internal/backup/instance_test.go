// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/btrfs"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyOf(t *testing.T) {
	p := PolicyOf(site.BackupConfig{
		Keep: 5, MinKeep: 2, MaxAge: site.Age(48 * time.Hour), MinFreeBytes: 100,
		KeepByReason: map[string]int{"manual": 3},
	})
	require.Equal(t, 5, p.Keep)
	require.Equal(t, 2, p.MinKeep)
	require.Equal(t, 48*time.Hour, p.MaxAge)
	require.Equal(t, int64(100), p.MinFreeBytes)
	assert.Equal(t, 3, p.KeepByReason[Manual])
	q := PolicyOf(site.BackupConfig{Keep: 1})
	assert.Nil(t, q.KeepByReason)
}

func TestBeforeHonoursThePolicy(t *testing.T) {
	e := newEnv(t)
	cfg := site.BackupConfig{Before: []string{"update"}}
	_, took, err := Before(t.Context(), e.m, cfg, ModUpdate)
	require.NoError(t, err)
	assert.False(t, took)
	res, took, err := Before(t.Context(), e.m, cfg, Update)
	require.NoError(t, err)
	require.True(t, took)
	require.NotEmpty(t, res.Snapshot.ID)
	err = os.RemoveAll(e.m.InstanceDir)
	require.NoError(t, err)
	_, took, err = Before(t.Context(), e.m, cfg, Update)
	require.NoError(t, err)
	assert.False(t, took)
}

func TestEnsureInstanceDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances", "x")
	_, err := EnsureInstanceDir(path)
	require.NoError(t, err)
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, fi.IsDir())
	_, err = EnsureInstanceDir(path)
	assert.NoError(t, err, "a second call must be fine")
}

func testConfig(t testing.TB) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.Instances = filepath.Join(root, "instances")
	cfg.Paths.Snapshots = filepath.Join(root, "snapshots")
	cfg.Paths.Site = filepath.Join(root, "site")
	return cfg
}

func TestCheckWithoutBtrfs(t *testing.T) {
	cfg := testConfig(t)
	if ok, _ := btrfs.IsBtrfs(os.TempDir()); ok {
		t.Skip("the temporary directory is on btrfs")
	}
	inst := &resolve.Instance{Name: "x", Paths: resolve.Paths{Root: filepath.Join(cfg.Paths.Instances, "x")}}
	err := Check(cfg, inst)
	assert.ErrorContains(t, err, "backups need btrfs")
}

func TestForBuildsTheManager(t *testing.T) {
	cfg := testConfig(t)
	inst := &resolve.Instance{Name: "x", Paths: resolve.Paths{Root: filepath.Join(cfg.Paths.Instances, "x")}, Backup: site.BackupConfig{Keep: 7}}
	m := For(cfg, inst)
	require.Equal(t, "x", m.Instance)
	require.Equal(t, inst.Paths.Root, m.InstanceDir)
	require.Equal(t, filepath.Join(cfg.Paths.Snapshots, "x"), m.Root)
	require.Equal(t, 7, m.Policy.Keep)
	assert.Nil(t, m.PostBackup)
	inst.Hooks.PostBackup = []string{"missing.sh"}
	m = For(cfg, inst)
	require.NotNil(t, m.PostBackup)
	err := m.PostBackup(t.Context(), "/snap")
	assert.Error(t, err, "a hook that cannot run must fail the post-backup step")
}

func TestPostBackupHookGetsTheDatabaseCopy(t *testing.T) {
	cfg := testConfig(t)
	cfg.Paths.DB = filepath.Join(t.TempDir(), "db")
	inst := &resolve.Instance{Name: "x", Paths: resolve.Paths{Root: filepath.Join(cfg.Paths.Instances, "x")}}
	siteDir := filepath.Join(cfg.Paths.Site, "instances", "x")
	require.NoError(t, os.MkdirAll(siteDir, 0o750))
	out := filepath.Join(t.TempDir(), "stdin.json")
	require.NoError(t, os.WriteFile(filepath.Join(siteDir, "hook.sh"), []byte("#!/bin/sh\ncat > "+out+"\n"), 0o700)) //nolint:gosec // a test hook must be executable
	inst.Hooks.PostBackup = []string{filepath.Join(siteDir, "hook.sh")}

	run := func() map[string]any {
		require.NoError(t, os.RemoveAll(out))
		require.NoError(t, For(cfg, inst).PostBackup(t.Context(), "/snap"))
		b, err := os.ReadFile(out)
		require.NoError(t, err)
		var got struct {
			Extra map[string]any `json:"extra"`
		}
		require.NoError(t, json.Unmarshal(b, &got))
		return got.Extra
	}
	assert.NotContains(t, run(), "db_backup", "no copy yet")
	dir := DBBackupDir(cfg)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	copyPath := filepath.Join(dir, "dzo-20261101T120000Z.sqlite")
	require.NoError(t, os.WriteFile(copyPath, nil, 0o600))
	extra := run()
	assert.Equal(t, copyPath, extra["db_backup"])
	assert.Equal(t, "/snap", extra["snapshot"])
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/btrfs"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

func TestPolicyOf(t *testing.T) {
	p := PolicyOf(site.BackupConfig{
		Keep: 5, MinKeep: 2, MaxAge: site.Age(48 * time.Hour), MinFreeBytes: 100,
		KeepByReason: map[string]int{"manual": 3},
	})
	if p.Keep != 5 || p.MinKeep != 2 || p.MaxAge != 48*time.Hour || p.MinFreeBytes != 100 || p.KeepByReason[Manual] != 3 {
		t.Errorf("policy = %+v", p)
	}
	if q := PolicyOf(site.BackupConfig{Keep: 1}); q.KeepByReason != nil {
		t.Errorf("no keep_by_reason must stay nil: %+v", q)
	}
}

func TestBeforeHonoursThePolicy(t *testing.T) {
	e := newEnv(t)
	cfg := site.BackupConfig{Before: []string{"update"}}
	if _, took, err := Before(context.Background(), e.m, cfg, ModUpdate); err != nil || took {
		t.Errorf("a reason the policy does not list: took=%v err=%v", took, err)
	}
	res, took, err := Before(context.Background(), e.m, cfg, Update)
	if err != nil || !took || res.Snapshot.ID == "" {
		t.Fatalf("listed reason: res=%+v took=%v err=%v", res, took, err)
	}
	if err := os.RemoveAll(e.m.InstanceDir); err != nil {
		t.Fatal(err)
	}
	if _, took, err := Before(context.Background(), e.m, cfg, Update); err != nil || took {
		t.Errorf("an instance that does not exist yet has nothing to protect: took=%v err=%v", took, err)
	}
}

func TestEnsureInstanceDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances", "x")
	if _, err := EnsureInstanceDir(path); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		t.Fatalf("the instance directory must exist: %v", err)
	}
	if _, err := EnsureInstanceDir(path); err != nil {
		t.Errorf("a second call must be fine: %v", err)
	}
}

func testConfig(t *testing.T) *config.Config {
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
	if err == nil || !strings.Contains(err.Error(), "backups need btrfs") {
		t.Errorf("err = %v", err)
	}
}

func TestForBuildsTheManager(t *testing.T) {
	cfg := testConfig(t)
	inst := &resolve.Instance{Name: "x", Paths: resolve.Paths{Root: filepath.Join(cfg.Paths.Instances, "x")}, Backup: site.BackupConfig{Keep: 7}}
	m := For(cfg, inst)
	if m.Instance != "x" || m.InstanceDir != inst.Paths.Root || m.Root != filepath.Join(cfg.Paths.Snapshots, "x") || m.Policy.Keep != 7 || m.PostBackup != nil {
		t.Errorf("manager = %+v", m)
	}
	inst.Hooks.PostBackup = []string{"missing.sh"}
	m = For(cfg, inst)
	if m.PostBackup == nil {
		t.Fatal("post_backup hooks must set PostBackup")
	}
	if err := m.PostBackup(context.Background(), "/snap"); err == nil {
		t.Error("a hook that cannot run must fail the post-backup step")
	}
}

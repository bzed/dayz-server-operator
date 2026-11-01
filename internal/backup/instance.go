// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/bzed/dayz-server-operator/internal/btrfs"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/hooks"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// PolicyOf converts the site config to a retention policy.
func PolicyOf(c site.BackupConfig) Policy {
	p := Policy{Keep: c.Keep, MinKeep: c.MinKeep, MaxAge: c.MaxAge.Std(), MinFreeBytes: int64(c.MinFreeBytes)}
	if len(c.KeepByReason) > 0 {
		p.KeepByReason = map[Reason]int{}
		for r, n := range c.KeepByReason {
			p.KeepByReason[Reason(r)] = n
		}
	}
	return p
}

// DBBackupDir is where dzo serve writes the copies of the database: <paths.db>/backup.
func DBBackupDir(cfg *config.Config) string { return filepath.Join(cfg.Paths.DB, "backup") }

// For builds the manager of a resolved instance: its subvolume under
// paths.instances, its snapshots under paths.snapshots, the retention of the
// instance's backup config and its post_backup hooks.
func For(cfg *config.Config, inst *resolve.Instance) *Manager {
	m := &Manager{
		Instance: inst.Name, InstanceDir: inst.Paths.Root, Root: filepath.Join(cfg.Paths.Snapshots, inst.Name),
		Policy: PolicyOf(inst.Backup), FS: Btrfs{},
	}
	if hs := inst.Hooks.PostBackup; len(hs) > 0 {
		runner := &hooks.Runner{Dir: filepath.Join(cfg.Paths.Site, "instances", inst.Name)}
		m.PostBackup = func(ctx context.Context, path string) error {
			var failed []string
			extra := map[string]any{"snapshot": path}
			if db, err := moderation.LatestBackup(DBBackupDir(cfg)); err == nil && db != "" {
				extra["db_backup"] = db // the newest consistent copy of dzo.sqlite, for the hook to ship along
			}
			for _, r := range hooks.RunAll(ctx, runner, hs, hooks.Context{Instance: inst.Name, Point: "post_backup", Extra: extra}, false) {
				if r.Err != nil {
					failed = append(failed, r.Err.Error())
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("%v", failed)
			}
			return nil
		}
	}
	return m
}

// Check says whether an instance can be backed up: paths.instances and
// paths.snapshots on the same btrfs filesystem, and the instance a subvolume.
func Check(cfg *config.Config, inst *resolve.Instance) error {
	for name, dir := range map[string]string{"paths.instances": cfg.Paths.Instances, "paths.snapshots": cfg.Paths.Snapshots} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		if ok, err := btrfs.IsBtrfs(dir); err != nil || !ok {
			return fmt.Errorf("backups need btrfs: %s (%s) is not on a btrfs filesystem", name, dir)
		}
	}
	if same, err := btrfs.SameFilesystem(cfg.Paths.Instances, cfg.Paths.Snapshots); err != nil || !same {
		return fmt.Errorf("paths.snapshots (%s) must be on the same filesystem as paths.instances (%s): a snapshot cannot cross filesystems", cfg.Paths.Snapshots, cfg.Paths.Instances)
	}
	if ok, err := btrfs.IsSubvolume(inst.Paths.Root); err != nil || !ok {
		return fmt.Errorf("instance %s (%s) is not a btrfs subvolume: dzo creates instances as subvolumes on their first render", inst.Name, inst.Paths.Root)
	}
	return nil
}

// EnsureInstanceDir creates the instance directory as a btrfs subvolume (the
// unit of a backup) if it does not exist. On a filesystem that is not btrfs it
// is a plain directory and the returned flag is false: such an instance runs
// but cannot be backed up.
func EnsureInstanceDir(path string) (subvolume bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); err == nil {
		return btrfs.IsSubvolume(path)
	}
	if ok, err := btrfs.IsBtrfs(filepath.Dir(path)); err == nil && ok {
		return true, btrfs.CreateSubvolume(path)
	}
	return false, os.MkdirAll(path, 0o750)
}

// Before takes a snapshot first if the instance's policy asks for one before
// this kind of change (for update, mod_update, mission_update, config_change
// and render). It reports whether it did. An error means the snapshot failed,
// and the policy is to abort: nothing may be applied.
func Before(ctx context.Context, m *Manager, cfg site.BackupConfig, reason Reason) (Result, bool, error) {
	if !slices.Contains(cfg.Before, string(reason)) {
		return Result{}, false, nil
	}
	if _, err := os.Lstat(m.InstanceDir); os.IsNotExist(err) {
		return Result{}, false, nil // nothing to protect yet
	}
	res, err := m.Create(ctx, reason)
	return res, err == nil, err
}

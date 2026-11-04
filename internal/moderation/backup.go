// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	metaBackedUp  = "db_backup_at"
	backupPrefix  = "dzo-"
	backupSuffix  = ".sqlite"
	backupStamp   = "20060102T150405Z"
	backupPattern = backupPrefix + "*" + backupSuffix
)

// backupNames are the copies in dir, oldest first (the names sort by time).
func backupNames(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("moderation: backups: %w", err)
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if e.Type().IsRegular() && isBackupName(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// isBackupName reports whether n is exactly dzo-<UTC stamp>.sqlite, as BackupTo writes it. Any other file
// (a copy saved by hand, say) is neither returned nor rotated away.
func isBackupName(n string) bool {
	stamp, ok := strings.CutPrefix(n, backupPrefix)
	if !ok {
		return false
	}
	stamp, ok = strings.CutSuffix(stamp, backupSuffix)
	if !ok {
		return false
	}
	_, err := time.Parse(backupStamp, stamp)
	return err == nil
}

// LatestBackup is the newest copy of the database in dir, or "" (and no error) when there is none.
func LatestBackup(dir string) (string, error) {
	names, err := backupNames(dir)
	if err != nil || len(names) == 0 {
		return "", err
	}
	return filepath.Join(dir, names[len(names)-1]), nil
}

// BackupTo writes a consistent copy of the database to dir/dzo-<UTC time>.sqlite (mode 0600, the
// directory 0750) with VACUUM INTO and deletes the oldest copies beyond keep. It returns the path.
func (s *Store) BackupTo(ctx context.Context, dir string, now time.Time, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	if err := os.Chmod(dir, 0o750); err != nil { //nolint:gosec // G302: a directory needs the x bit; MkdirAll leaves an existing one as it was
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	path := filepath.Join(dir, backupPrefix+now.UTC().Format(backupStamp)+backupSuffix)
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	// VACUUM INTO accepts an empty file: create it 0600 so the copy is never readable by others, not even
	// until the chmod below.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: tmp is built from the configured backup directory
	if err != nil {
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp) // best effort: a half-written copy must not stay
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("moderation: backup: %w", err)
	}
	// Leftovers of an interrupted copy.
	if tmps, err := filepath.Glob(filepath.Join(dir, backupPattern+".tmp")); err == nil {
		for _, t := range tmps {
			_ = os.Remove(t) // best effort
		}
	}
	names, err := backupNames(dir)
	if err != nil {
		return path, err
	}
	for len(names) > max(keep, 1) {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return path, fmt.Errorf("moderation: backup: %w", err)
		}
		names = names[1:]
	}
	return path, nil
}

// BackupDue reports whether the last copy is at least every ago (or there was none).
func (s *Store) BackupDue(ctx context.Context, now time.Time, every time.Duration) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, metaBackedUp).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("moderation: backup due: %w", err)
	}
	at, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return true, nil // an unreadable mark is as good as none
	}
	return !now.Before(time.Unix(at, 0).Add(every)), nil
}

// MarkBackedUp records that a copy was written at now.
func (s *Store) MarkBackedUp(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		metaBackedUp, strconv.FormatInt(now.Unix(), 10))
	if err != nil {
		return fmt.Errorf("moderation: backup mark: %w", err)
	}
	return nil
}

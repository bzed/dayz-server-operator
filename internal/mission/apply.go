// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultKeepFileHistory is used when Options.KeepFileHistory is unset.
const DefaultKeepFileHistory = 5

// Options controls Apply's behaviour.
type Options struct {
	// KeepFileHistory is how many filehistory/<ts>/ generations to retain
	// (mission.keep_file_history); <= 0 means DefaultKeepFileHistory.
	KeepFileHistory int
	// Now is a seam for tests; defaults to time.Now.
	Now func() time.Time
}

func (o Options) keep() int {
	if o.KeepFileHistory <= 0 {
		return DefaultKeepFileHistory
	}
	return o.KeepFileHistory
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Report summarises what Apply did, for logging, the job record and
// drift notifications.
type Report struct {
	Timestamp string
	Written   []PlanEntry // new/update/drift entries actually written
	Drifted   []PlanEntry // subset of Written whose live file had changed underneath dzo
	Obsolete  []PlanEntry // dropped from the manifest, left untouched on disk
}

// Apply writes plan's changed entries into liveDir, backing up any
// existing live content to filehistoryDir/<timestamp>/ first (§C6 safety
// net), and updates manifest in place (mutated, not saved - the caller
// persists it, e.g. after also running any post-render hooks). Every
// write is atomic (temp file + rename) and Apply never touches a path
// outside plan.Changed()/plan.Obsolete() - Classify already excluded
// storage_* and configured-unmanaged paths.
func Apply(plan Plan, stagingDir, liveDir, filehistoryDir string, manifest *Manifest, opts Options) (Report, error) {
	ts := opts.now().UTC().Format("20060102T150405.000000000Z")
	report := Report{Timestamp: ts}
	historyDir := filepath.Join(filehistoryDir, ts)
	historyDirCreated := false

	ensureHistoryDir := func() error {
		if historyDirCreated {
			return nil
		}
		if err := os.MkdirAll(historyDir, 0o750); err != nil {
			return fmt.Errorf("mission: create filehistory dir: %w", err)
		}
		historyDirCreated = true
		return nil
	}

	if plan.Empty() {
		return report, nil
	}
	root, err := openLive(liveDir, true)
	if err != nil {
		return report, err
	}
	defer root.Close() //nolint:errcheck // the files are closed and renamed already

	for _, entry := range plan.Changed() {
		// A live path that is a link, FIFO or the like is not backed up (it is never read); the
		// write below replaces it.
		if existing, err := readRootFile(root, entry.Path); err == nil {
			if err := ensureHistoryDir(); err != nil {
				return report, err
			}
			backupPath := filepath.Join(historyDir, entry.Path)
			if err := writeFileAtomic(backupPath, existing, 0o640); err != nil {
				return report, fmt.Errorf("mission: back up %s: %w", entry.Path, err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, errNotRegular) {
			return report, fmt.Errorf("mission: read live %s: %w", entry.Path, err)
		}

		content, err := os.ReadFile(filepath.Join(stagingDir, entry.Path)) //nolint:gosec // entry.Path comes from Classify's staging walk
		if err != nil {
			return report, fmt.Errorf("mission: read staging %s: %w", entry.Path, err)
		}
		if err := writeRootAtomic(root, entry.Path, content, 0o640); err != nil {
			return report, fmt.Errorf("mission: write live %s: %w", entry.Path, err)
		}

		manifest.Entries[entry.Path] = Entry{Origin: entry.Origin, SourceHash: entry.StagingHash, WrittenHash: entry.StagingHash}
		report.Written = append(report.Written, entry)
		if entry.Action == ActionDrift {
			report.Drifted = append(report.Drifted, entry)
		}
	}

	for _, entry := range plan.Obsolete() {
		// EditorFiles/ is dzo's: a file removed from the site is removed from the mission (after a
		// backup). Everything else that became obsolete stays on disk.
		if strings.HasPrefix(entry.Path, "EditorFiles/") && filepath.IsLocal(entry.Path) {
			existing, err := readRootFile(root, entry.Path)
			if err == nil {
				if err := ensureHistoryDir(); err != nil {
					return report, err
				}
				if err := writeFileAtomic(filepath.Join(historyDir, entry.Path), existing, 0o640); err != nil {
					return report, fmt.Errorf("mission: back up %s: %w", entry.Path, err)
				}
			} else if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, errNotRegular) {
				return report, fmt.Errorf("mission: read live %s: %w", entry.Path, err)
			}
			if err := root.Remove(entry.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return report, fmt.Errorf("mission: remove live %s: %w", entry.Path, err)
			}
			removeEmptyDirs(root, path.Dir(entry.Path))
		}
		delete(manifest.Entries, entry.Path)
		report.Obsolete = append(report.Obsolete, entry)
	}

	if historyDirCreated {
		if err := pruneFileHistory(filehistoryDir, opts.keep()); err != nil {
			return report, fmt.Errorf("mission: prune filehistory: %w", err)
		}
	}

	return report, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup; the rename below removes it on success

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("activate %s: %w", path, err)
	}
	return nil
}

// pruneFileHistory keeps only the newest `keep` timestamp directories
// directly under filehistoryDir (lexicographic order = chronological,
// since the timestamp format sorts correctly).
func pruneFileHistory(filehistoryDir string, keep int) error {
	entries, err := os.ReadDir(filehistoryDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", filehistoryDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) <= keep {
		return nil
	}
	for _, name := range names[:len(names)-keep] {
		if err := os.RemoveAll(filepath.Join(filehistoryDir, name)); err != nil {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return nil
}

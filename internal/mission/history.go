// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HistorySets lists the filehistory sets (one per apply that overwrote files), oldest first.
func HistorySets(filehistoryDir string) ([]string, error) {
	entries, err := os.ReadDir(filehistoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mission: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Rollback restores the files of one filehistory set into the live mission: the set whose name
// starts with ts, or the newest when ts is "". Only files that are in the manifest are
// restored (a history set holds nothing else). The manifest records the restored content as what
// dzo wrote, so the next render replaces it quietly instead of reporting drift: a rollback holds
// until the next render, and lasting changes belong in the pristine mission or an overlay.
// The caller saves the manifest. It returns the set used and the restored paths.
func Rollback(filehistoryDir, liveDir string, m *Manifest, ts string) (string, []string, error) {
	sets, err := HistorySets(filehistoryDir)
	if err != nil {
		return "", nil, err
	}
	var set string
	for i := len(sets) - 1; i >= 0; i-- {
		if strings.HasPrefix(sets[i], ts) {
			set = sets[i]
			break
		}
	}
	if set == "" {
		if ts == "" {
			return "", nil, fmt.Errorf("mission: there is no file history to roll back to")
		}
		return "", nil, fmt.Errorf("mission: no file history set starts with %q (have: %s)", ts, strings.Join(sets, ", "))
	}
	root := filepath.Join(filehistoryDir, set)
	var restored []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		e, managed := m.Entries[rel]
		if !managed {
			return nil
		}
		data, err := os.ReadFile(p) //nolint:gosec // below dzo's own filehistory directory
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(liveDir, rel), data, 0o640); err != nil {
			return err
		}
		e.WrittenHash = HashBytes(data)
		m.Entries[rel] = e
		restored = append(restored, rel)
		return nil
	})
	sort.Strings(restored)
	return set, restored, err
}

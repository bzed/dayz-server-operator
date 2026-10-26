// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package integrate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// MapStreamSrc is the folder of an instance in the site repository that holds the DayZ Editor
// files of dzo_mapstream; SyncMapStream mirrors it to MapStreamDst below the instance's profiles/.
const MapStreamSrc = "MapStreamDZE"

// MapStreamDst is where dzo_mapstream reads its .dze files, relative to profiles/.
var MapStreamDst = filepath.Join("dzo", "mapstream", "DZE")

// SyncMapStream makes <profiles>/dzo/mapstream/DZE an exact copy of <instDir>/MapStreamDZE:
// files are written (only when they differ), files and empty folders that are not in the site any
// more are removed. An instance without a MapStreamDZE folder is left alone. It returns the
// number of files written and removed.
func SyncMapStream(instDir, profiles string) (written, removed int, err error) {
	src := filepath.Join(instDir, MapStreamSrc)
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	files, err := readFiles(MapStreamSrc, src, "")
	if err != nil {
		return 0, 0, err
	}
	dst := filepath.Join(profiles, MapStreamDst)
	want := map[string]bool{}
	for _, f := range files {
		want[f.Name] = true
		p := filepath.Join(dst, filepath.FromSlash(f.Name))
		if old, err := os.ReadFile(p); err == nil && string(old) == string(f.Data) { //nolint:gosec // below profiles/
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return written, removed, fmt.Errorf("mapstream: %w", err)
		}
		if err := writeAtomic(p, f.Data); err != nil {
			return written, removed, fmt.Errorf("mapstream: %w", err)
		}
		written++
	}

	var stray, dirs []string
	err = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(dst, p)
		switch {
		case rel == ".":
		case d.IsDir():
			dirs = append(dirs, p)
		case !want[filepath.ToSlash(rel)]:
			stray = append(stray, p)
		}
		return nil
	})
	if err != nil {
		return written, removed, fmt.Errorf("mapstream: %w", err)
	}
	for _, p := range stray {
		if err := os.Remove(p); err != nil {
			return written, removed, fmt.Errorf("mapstream: %w", err)
		}
		removed++
	}
	// deepest first, so a folder that only held removed files goes too; non-empty ones stay.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		_ = os.Remove(d) // fails while the folder still holds a wanted file, which is the point
	}
	return written, removed, nil
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // gone after the rename; cleans up after a failure
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

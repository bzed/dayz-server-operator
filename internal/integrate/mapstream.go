// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package integrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bzed/dayz-server-operator/internal/safefs"
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
	// profiles/ is writable by the game container: work through an os.Root, so that a planted
	// symlink can neither redirect a write nor make the cleanup leave the tree.
	if err := os.MkdirAll(profiles, 0o750); err != nil {
		return 0, 0, fmt.Errorf("mapstream: %w", err)
	}
	root, err := os.OpenRoot(profiles)
	if err != nil {
		return 0, 0, fmt.Errorf("mapstream: %w", err)
	}
	defer func() { _ = root.Close() }() // handle only
	dst := filepath.ToSlash(MapStreamDst)
	want := map[string]bool{}
	for _, f := range files {
		want[f.Name] = true
		p := path.Join(dst, f.Name)
		if old, err := safefs.ReadFile(root, p, mapStreamReadMax); err == nil && string(old) == string(f.Data) {
			continue
		}
		if err := safefs.WriteFile(root, p, f.Data, 0o600); err != nil {
			return written, removed, fmt.Errorf("mapstream: %w", err)
		}
		written++
	}

	var stray, dirs []string
	err = fs.WalkDir(root.FS(), dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, dst), "/")
		switch {
		case rel == "":
		case d.IsDir():
			dirs = append(dirs, p)
		case !want[rel]:
			stray = append(stray, p)
		}
		return nil
	})
	if err != nil {
		return written, removed, fmt.Errorf("mapstream: %w", err)
	}
	for _, p := range stray {
		if err := root.Remove(p); err != nil {
			return written, removed, fmt.Errorf("mapstream: %w", err)
		}
		removed++
	}
	// deepest first, so a folder that only held removed files goes too; non-empty ones stay.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		_ = root.Remove(d) // fails while the folder still holds a wanted file, which is the point
	}
	return written, removed, nil
}

// mapStreamReadMax bounds the read of an existing file for the "unchanged" comparison.
const mapStreamReadMax = 64 << 20

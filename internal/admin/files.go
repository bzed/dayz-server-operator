// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/safefs"
)

const fileMarkerTTL = 5 * time.Second

// fileMarkers reads the file drop (§C16 path 3): any mod or tool writes
// <layer>.json into $profile:dzo/admin/markers/, either a JSON array of
// markers or {"markers": [...]}. The layer defaults to the file name.
type fileMarkers struct {
	mu      sync.Mutex
	dir     string
	read    time.Time
	markers []Marker
}

// markerRel is the marker drop directory relative to profiles/ (see MarkerDir).
const markerRel = "dzo/admin/markers"

func (f *fileMarkers) load(dir string, now time.Time) []Marker {
	if dir == "" {
		return nil
	}
	f.mu.Lock()
	if f.dir == dir && now.Sub(f.read) < fileMarkerTTL {
		m := f.markers
		f.mu.Unlock()
		return m
	}
	f.mu.Unlock()
	// The directory is writable by the game container: read it without the lock, so that a
	// stuck file cannot stall other callers.
	markers := readMarkerDir(dir)
	f.mu.Lock()
	f.dir, f.read, f.markers = dir, now, markers
	f.mu.Unlock()
	return markers
}

// readMarkerDir reads the *.json files of dir through an os.Root on the profiles tree (or on
// the parent of dir for a directory outside the usual layout). It skips links, anything that is
// not a regular file and files above safefs.MaxFile.
func readMarkerDir(dir string) []Marker {
	base, rel := filepath.Dir(dir), filepath.Base(dir)
	if clean := filepath.ToSlash(filepath.Clean(dir)); strings.HasSuffix(clean, "/"+markerRel) {
		base, rel = filepath.FromSlash(strings.TrimSuffix(clean, "/"+markerRel)), markerRel
	}
	top, err := os.OpenRoot(base)
	if err != nil {
		return nil
	}
	defer func() { _ = top.Close() }() // handle only
	root, err := top.OpenRoot(rel)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }() // handle only
	d, err := root.Open(".")
	if err != nil {
		return nil
	}
	names, _ := d.Readdirnames(-1)
	_ = d.Close()
	sort.Strings(names)
	var out []Marker
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := safefs.ReadFile(root, name, safefs.MaxFile)
		if err != nil {
			continue
		}
		out = append(out, parseMarkerFile(strings.TrimSuffix(name, ".json"), b)...)
	}
	return out
}

// parseMarkerFile accepts both file forms and drops markers without an id.
// A broken file yields nothing rather than an error: mods write these files
// while the game runs, so a half-written one is normal.
func parseMarkerFile(layer string, b []byte) []Marker {
	var list []Marker
	if err := json.Unmarshal(b, &list); err != nil {
		var wrapped struct {
			Markers []Marker `json:"markers"`
		}
		if err := json.Unmarshal(b, &wrapped); err != nil {
			return nil
		}
		list = wrapped.Markers
	}
	out := list[:0]
	for _, m := range list {
		if m.ID == "" {
			continue
		}
		if m.Layer == "" {
			m.Layer = layer
		}
		if m.Shape == "" {
			m.Shape = "point"
		}
		m.Source = "file"
		out = append(out, m)
	}
	return out
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

func (f *fileMarkers) load(dir string, now time.Time) []Marker {
	if dir == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dir == dir && now.Sub(f.read) < fileMarkerTTL {
		return f.markers
	}
	f.dir, f.read, f.markers = dir, now, nil
	names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, name := range names {
		b, err := os.ReadFile(name) //nolint:gosec // the marker drop directory is the instance's own profile dir
		if err != nil {
			continue
		}
		layer := strings.TrimSuffix(filepath.Base(name), ".json")
		f.markers = append(f.markers, parseMarkerFile(layer, b)...)
	}
	return f.markers
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

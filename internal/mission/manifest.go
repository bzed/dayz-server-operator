// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mission implements the live mission render/apply pipeline
// (§C6): the live mission directory is persistent game data (D13) that is
// never wiped or recreated. dzo only ever updates the files it manages in
// place, tracked in a manifest, and never touches anything else.
//
// Scope note: this package implements the manifest, apply-plan
// classification, atomic apply with a filehistory safety net, and the
// merge-strategy dispatcher over internal/ce - the parts that are fully
// specifiable and testable without a real Steam/DayZ mod source. Resolving
// which files a mod integration actually contributes (from the site
// repo's local files, a URL, or a mod's own PBO) depends on
// internal/product's workshop cache and PBO reading, neither built yet;
// Renderer therefore takes already-resolved Contributions rather than mod
// ids, so the pipeline is complete and testable now and gets wired to real
// mod resolution later without changing its core logic.
package mission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Origin classifies why dzo manages a file (§C6 file classes).
type Origin string

const (
	OriginPristine  Origin = "pristine"  // exists in the pristine mission, refreshed/merged into on every render
	OriginGenerated Origin = "generated" // written by a mod/overlay contribution (mod_<id>/, custom_<dir>/, ...)
)

// Entry is one manifest record: what dzo last wrote to one live path, and
// why.
type Entry struct {
	Origin      Origin `json:"origin"`
	SourceHash  string `json:"source_hash"`  // hash of the staging content that produced WrittenHash
	WrittenHash string `json:"written_hash"` // hash of the bytes dzo actually wrote to the live path
}

// Manifest is instances/<name>/runtime/mission-manifest.json (outside the
// container-writable mpmissions mount): every live
// path dzo owns. Paths are slash-separated, relative to the mission root
// (e.g. "db/types.xml", "mod_123456/events.xml").
type Manifest struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// NewManifest returns an empty manifest.
func NewManifest() *Manifest {
	return &Manifest{Version: 1, Entries: map[string]Entry{}}
}

// LoadManifest reads a manifest file. A missing file is not an error: it
// returns a fresh empty manifest, matching a mission that has never been
// rendered before.
func LoadManifest(path string) (*Manifest, error) {
	data, err := readFileSafe(path, maxManifestFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewManifest(), nil
		}
		return nil, fmt.Errorf("mission: read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("mission: parse manifest %s: %w", path, err)
	}
	if m.Entries == nil {
		m.Entries = map[string]Entry{}
	}
	return &m, nil
}

// Save writes the manifest atomically (temp file + rename), with sorted
// keys (encoding/json already sorts map keys, so this is deterministic
// byte-for-byte across renders of the same logical state).
func (m *Manifest) Save(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("mission: marshal manifest: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("mission: create temp manifest: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup; the rename below removes it on success

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mission: write temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mission: close temp manifest: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("mission: activate manifest: %w", err)
	}
	return nil
}

// Paths returns every managed path, sorted.
func (m *Manifest) Paths() []string {
	paths := make([]string, 0, len(m.Entries))
	for p := range m.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// HashBytes returns the manifest's hash representation of data
// ("sha256:<hex>").
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HashFile hashes a file's contents the same way. A missing file returns
// ("", nil): callers treat that as "no prior content", not an error.
func HashFile(path string) (string, error) {
	data, err := readFileSafe(path, maxMissionFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("mission: hash %s: %w", path, err)
	}
	return HashBytes(data), nil
}

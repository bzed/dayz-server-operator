// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadManifestMissingFileReturnsEmpty(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), "nope.json"))
	require.NoError(t, err, "LoadManifest")
	require.Empty(t, m.Entries)
}

func TestManifestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m := NewManifest()
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: "sha256:a", WrittenHash: "sha256:a"}
	m.Entries["mod_123/events.xml"] = Entry{Origin: OriginGenerated, SourceHash: "sha256:b", WrittenHash: "sha256:b"}

	err := m.Save(path)
	require.NoError(t, err, "Save")
	loaded, err := LoadManifest(path)
	require.NoError(t, err, "LoadManifest")
	require.Len(t, loaded.Entries, 2)
	assert.Equal(t, "sha256:a", loaded.Entries["db/types.xml"].SourceHash)
}

func TestManifestSaveIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	m := NewManifest()
	m.Entries["z.xml"] = Entry{Origin: OriginPristine, SourceHash: "1", WrittenHash: "1"}
	m.Entries["a.xml"] = Entry{Origin: OriginPristine, SourceHash: "2", WrittenHash: "2"}

	p1 := filepath.Join(dir, "m1.json")
	p2 := filepath.Join(dir, "m2.json")
	err := m.Save(p1)
	require.NoError(t, err, "Save")
	err = m.Save(p2)
	require.NoError(t, err, "Save")
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	require.Equal(t, string(b2), string(b1))
}

func TestManifestSaveAtomicNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	m := NewManifest()
	err := m.Save(path)
	require.NoError(t, err, "Save")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "ReadDir")
	require.Len(t, entries, 1)
}

func TestManifestPathsSorted(t *testing.T) {
	m := NewManifest()
	m.Entries["z"] = Entry{}
	m.Entries["a"] = Entry{}
	m.Entries["m"] = Entry{}
	paths := m.Paths()
	require.Equal(t, "a", paths[0])
	require.Equal(t, "m", paths[1])
	assert.Equal(t, "z", paths[2])
}

func TestHashBytesStable(t *testing.T) {
	h1 := HashBytes([]byte("hello"))
	h2 := HashBytes([]byte("hello"))
	require.Equal(t, h2, h1)
	require.NotEqual(t, h1, HashBytes([]byte("world")), "different content produced the same hash")
}

func TestHashFileMissingReturnsEmpty(t *testing.T) {
	h, err := HashFile(filepath.Join(t.TempDir(), "nope"))
	require.NoError(t, err, "HashFile")
	assert.Empty(t, h)
}

func TestLoadManifestInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	err := os.WriteFile(path, []byte("not json"), 0o600)
	require.NoError(t, err, "write")
	_, err = LoadManifest(path)
	require.Error(t, err)
}

func TestManifestSaveMissingParentDirErrors(t *testing.T) {
	m := NewManifest()
	path := filepath.Join(t.TempDir(), "nonexistent-subdir", "manifest.json")
	err := m.Save(path)
	require.Error(t, err)
}

func TestHashFileReadErrorOnDirectory(t *testing.T) {
	// Reading a directory as a file is a real (non-ENOENT) error, distinct
	// from "missing file".
	_, err := HashFile(t.TempDir())
	require.Error(t, err)
}

func TestHashFileMatchesHashBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	content := []byte("some content")
	err := os.WriteFile(path, content, 0o600)
	require.NoError(t, err, "write")
	h, err := HashFile(path)
	require.NoError(t, err, "HashFile")
	assert.Equal(t, HashBytes(content), h)
}

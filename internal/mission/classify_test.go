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

func writeTree(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		err := os.MkdirAll(filepath.Dir(full), 0o750)
		require.NoError(t, err, "mkdir")
		err = os.WriteFile(full, []byte(content), 0o640)
		require.NoError(t, err, "write %s", path)
	}
}

func findEntry(t testing.TB, plan Plan, path string) PlanEntry {
	t.Helper()
	for _, e := range plan.Entries {
		if e.Path == path {
			return e
		}
	}
	require.Failf(t, "no plan entry", "for %q in %+v", path, plan.Entries)
	return PlanEntry{}
}

func TestClassifyFirstRenderIsAllNew(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"db/types.xml": "types-v1",
		"init.c":       "init-v1",
	})

	plan, err := Classify(staging, live, NewManifest(), nil)
	require.NoError(t, err, "Classify")
	require.Len(t, plan.Changed(), 2)
	for _, e := range plan.Changed() {
		assert.Equal(t, ActionNew, e.Action)
		assert.Equal(t, OriginPristine, e.Origin)
	}
}

func TestClassifyGeneratedOrigin(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"mod_123/types.xml":        "x",
		"custom_loadout/gear.json": "y",
		"EditorFiles/a.dze":        "z",
	})
	plan, err := Classify(staging, live, NewManifest(), nil)
	require.NoError(t, err, "Classify")
	assert.Equal(t, OriginGenerated, findEntry(t, plan, "mod_123/types.xml").Origin)
	assert.Equal(t, OriginGenerated, findEntry(t, plan, "custom_loadout/gear.json").Origin)
	assert.Equal(t, OriginGenerated, findEntry(t, plan, "EditorFiles/a.dze").Origin)
}

func TestClassifyUnchanged(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	content := "types-v1"
	writeTree(t, staging, map[string]string{"db/types.xml": content})
	writeTree(t, live, map[string]string{"db/types.xml": content})

	hash := HashBytes([]byte(content))
	m := NewManifest()
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	require.True(t, plan.Empty(), "expected an empty plan, got %+v", plan.Entries)
	assert.Equal(t, ActionUnchanged, findEntry(t, plan, "db/types.xml").Action)
}

func TestClassifyUpdateWhenStagingChangesButLiveUntouched(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	oldContent := "v1"
	newContent := "v2"
	writeTree(t, staging, map[string]string{"db/types.xml": newContent})
	writeTree(t, live, map[string]string{"db/types.xml": oldContent})

	m := NewManifest()
	oldHash := HashBytes([]byte(oldContent))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: oldHash, WrittenHash: oldHash}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	assert.Equal(t, ActionUpdate, findEntry(t, plan, "db/types.xml").Action)
}

func TestClassifyDriftWhenLiveChangedUnderneath(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	original := "v1"
	writeTree(t, staging, map[string]string{"db/types.xml": original}) // staging unchanged
	writeTree(t, live, map[string]string{"db/types.xml": "modified-by-something-else"})

	m := NewManifest()
	hash := HashBytes([]byte(original))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	assert.Equal(t, ActionDrift, findEntry(t, plan, "db/types.xml").Action)
}

func TestClassifyNewWhenLiveFileMissingDespiteManifest(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir() // nothing written here
	content := "v1"
	writeTree(t, staging, map[string]string{"db/types.xml": content})

	m := NewManifest()
	hash := HashBytes([]byte(content))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	assert.Equal(t, ActionNew, findEntry(t, plan, "db/types.xml").Action)
}

func TestClassifyObsoleteWhenRemovedFromStaging(t *testing.T) {
	staging := t.TempDir() // empty: nothing ships this file anymore
	live := t.TempDir()
	writeTree(t, live, map[string]string{"mod_999/types.xml": "old"})

	m := NewManifest()
	m.Entries["mod_999/types.xml"] = Entry{Origin: OriginGenerated, SourceHash: "h", WrittenHash: "h"}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	require.Len(t, plan.Obsolete(), 1)
	require.Equal(t, "mod_999/types.xml", plan.Obsolete()[0].Path)
	// Never counted as something to write.
	require.Empty(t, plan.Changed())
}

func TestClassifyHardExcludesStorage(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"storage_1/base.pbo": "should never be touched",
		"db/types.xml":       "fine",
	})

	plan, err := Classify(staging, live, NewManifest(), nil)
	require.NoError(t, err, "Classify")
	for _, e := range plan.Entries {
		require.NotEqual(t, "storage_1/base.pbo", e.Path)
	}
	require.Len(t, plan.Entries, 1)
}

func TestClassifyRespectsConfiguredUnmanaged(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"expansion/traders/config.json": "should stay foreign",
		"db/types.xml":                  "fine",
	})

	plan, err := Classify(staging, live, NewManifest(), []string{"expansion/**"})
	require.NoError(t, err, "Classify")
	for _, e := range plan.Entries {
		require.NotEqual(t, "expansion/traders/config.json", e.Path)
	}
	require.Len(t, plan.Entries, 1)
}

func TestClassifyUnmanagedPathDroppedFromManifestBecomesObsoleteNotForeignSilently(t *testing.T) {
	// If a path was previously managed and is now marked unmanaged, it
	// must be reported as Obsolete (dropped from the manifest), not
	// silently forgotten.
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{"expansion/traders/config.json": "x"})
	writeTree(t, live, map[string]string{"expansion/traders/config.json": "x"})

	m := NewManifest()
	hash := HashBytes([]byte("x"))
	m.Entries["expansion/traders/config.json"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, []string{"expansion/**"})
	require.NoError(t, err, "Classify")
	require.Len(t, plan.Obsolete(), 1)
	require.Equal(t, "expansion/traders/config.json", plan.Obsolete()[0].Path)
}

func TestClassifyEmptyStagingAndLive(t *testing.T) {
	plan, err := Classify(t.TempDir(), t.TempDir(), NewManifest(), nil)
	require.NoError(t, err, "Classify")
	require.True(t, plan.Empty(), "expected an empty plan, got %+v", plan.Entries)
}

func TestClassifyMissingStagingDirErrors(t *testing.T) {
	_, err := Classify(filepath.Join(t.TempDir(), "nope"), t.TempDir(), NewManifest(), nil)
	require.Error(t, err)
}

func TestActionString(t *testing.T) {
	cases := map[Action]string{
		ActionNew:       "new",
		ActionUpdate:    "update",
		ActionDrift:     "drift",
		ActionUnchanged: "unchanged",
		ActionObsolete:  "obsolete",
		Action(99):      "unknown",
	}
	for a, want := range cases {
		got := a.String()
		assert.Equal(t, want, got)
	}
}

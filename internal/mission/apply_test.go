// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readString(t testing.TB, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	return string(data)
}

func TestApplyWritesNewFiles(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	writeTree(t, staging, map[string]string{"db/types.xml": "content-v1"})

	m := NewManifest()
	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	report, err := Apply(plan, staging, live, filehistory, m, Options{})
	require.NoError(t, err, "Apply")
	require.Len(t, report.Written, 1)
	got := readString(t, filepath.Join(live, "db/types.xml"))
	assert.Equal(t, "content-v1", got)
	assert.Equal(t, HashBytes([]byte("content-v1")), m.Entries["db/types.xml"].SourceHash)
}

func TestApplyNewFileHasNothingToBackUp(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	writeTree(t, staging, map[string]string{"db/types.xml": "v1"})

	m := NewManifest()
	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	_, err = Apply(plan, staging, live, filehistory, m, Options{})
	require.NoError(t, err, "Apply")
	entries, err := os.ReadDir(filehistory)
	require.NoError(t, err, "ReadDir")
	assert.Empty(t, entries)
}

func TestApplyBacksUpDriftedFileBeforeOverwriting(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()

	original := "v1"
	writeTree(t, staging, map[string]string{"db/types.xml": original})
	writeTree(t, live, map[string]string{"db/types.xml": "modified-by-something-else"})

	m := NewManifest()
	hash := HashBytes([]byte(original))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	report, err := Apply(plan, staging, live, filehistory, m, Options{Now: func() time.Time { return time.Unix(1000000000, 0) }})
	require.NoError(t, err, "Apply")
	require.Len(t, report.Drifted, 1)

	// The drifted content must be preserved in filehistory before being overwritten.
	tsDirs, err := os.ReadDir(filehistory)
	require.NoError(t, err, "ReadDir")
	require.Len(t, tsDirs, 1)
	backedUp := readString(t, filepath.Join(filehistory, tsDirs[0].Name(), "db/types.xml"))
	assert.Equal(t, "modified-by-something-else", backedUp)

	// And the live file now has the staging content.
	got := readString(t, filepath.Join(live, "db/types.xml"))
	assert.Equal(t, original, got)
}

func TestApplyObsoleteNeverTouchesDisk(t *testing.T) {
	staging := t.TempDir() // no longer ships this file
	live := t.TempDir()
	filehistory := t.TempDir()
	writeTree(t, live, map[string]string{"mod_999/types.xml": "still here"})

	m := NewManifest()
	m.Entries["mod_999/types.xml"] = Entry{Origin: OriginGenerated, SourceHash: "h", WrittenHash: "h"}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	report, err := Apply(plan, staging, live, filehistory, m, Options{})
	require.NoError(t, err, "Apply")
	require.Len(t, report.Obsolete, 1)
	// The file itself is untouched on disk.
	got := readString(t, filepath.Join(live, "mod_999/types.xml"))
	assert.Equal(t, "still here", got)
	// But it's gone from the manifest (no longer "managed").
	_, ok := m.Entries["mod_999/types.xml"]
	assert.False(t, ok, "obsolete entry should have been dropped from the manifest")
}

func TestApplyObsoleteEditorFilesAreRemovedAndBackedUp(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	writeTree(t, live, map[string]string{"EditorFiles/sub/a.dze": "A", "EditorFiles/b.dze": "B", "EditorFiles/foreign.dze": "F"})

	m := NewManifest()
	m.Entries["EditorFiles/sub/a.dze"] = Entry{Origin: OriginGenerated, SourceHash: "h", WrittenHash: "h"}
	m.Entries["EditorFiles/b.dze"] = Entry{Origin: OriginGenerated, SourceHash: "h", WrittenHash: "h"}

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	report, err := Apply(plan, staging, live, filehistory, m, Options{})
	require.NoError(t, err, "Apply")
	require.Len(t, report.Obsolete, 2)
	assert.NoFileExists(t, filepath.Join(live, "EditorFiles/b.dze"))
	assert.NoFileExists(t, filepath.Join(live, "EditorFiles/sub/a.dze"))
	assert.NoDirExists(t, filepath.Join(live, "EditorFiles/sub"), "an emptied folder goes too")
	assert.FileExists(t, filepath.Join(live, "EditorFiles/foreign.dze"), "a file dzo never wrote stays")
	backups, err := filepath.Glob(filepath.Join(filehistory, "*", "EditorFiles", "b.dze"))
	require.NoError(t, err)
	assert.Len(t, backups, 1, "the removed file is kept in the file history")
}

func TestApplyUnchangedDoesNotRewriteFile(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	content := "stable"
	writeTree(t, staging, map[string]string{"db/types.xml": content})
	writeTree(t, live, map[string]string{"db/types.xml": content})

	hash := HashBytes([]byte(content))
	m := NewManifest()
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	livePath := filepath.Join(live, "db/types.xml")
	before, err := os.Stat(livePath)
	require.NoError(t, err, "stat")

	plan, err := Classify(staging, live, m, nil)
	require.NoError(t, err, "Classify")
	report, err := Apply(plan, staging, live, filehistory, m, Options{})
	require.NoError(t, err, "Apply")
	require.Empty(t, report.Written)

	after, err := os.Stat(livePath)
	require.NoError(t, err, "stat")
	assert.True(t, before.ModTime().Equal(after.ModTime()), "an unchanged file must not be rewritten (mtime changed)")
}

func TestApplyNeverWritesOutsidePlan(t *testing.T) {
	// A foreign file living in "live" (e.g. storage_1/*, or anything not
	// in the manifest/staging) must survive Apply completely untouched,
	// even when it shares a directory with managed files.
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	writeTree(t, staging, map[string]string{"db/types.xml": "new content"})
	writeTree(t, live, map[string]string{
		"storage_1/player.bin": "foreign game data",
	})

	plan, err := Classify(staging, live, NewManifest(), nil)
	require.NoError(t, err, "Classify")
	_, err = Apply(plan, staging, live, filehistory, NewManifest(), Options{})
	require.NoError(t, err, "Apply")
	got := readString(t, filepath.Join(live, "storage_1/player.bin"))
	require.Equal(t, "foreign game data", got)
}

func TestApplyPrunesOldFileHistory(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()

	m := NewManifest()
	content := "v0"
	writeTree(t, staging, map[string]string{"db/types.xml": content})
	writeTree(t, live, map[string]string{"db/types.xml": content})
	hash := HashBytes([]byte(content))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	// Cause 3 drift-and-overwrite cycles at 3 distinct timestamps, keeping
	// only the newest 2.
	base := time.Unix(2000000000, 0)
	for i := 0; i < 3; i++ {
		writeTree(t, live, map[string]string{"db/types.xml": "drifted"}) // force drift each round
		plan, err := Classify(staging, live, m, nil)
		require.NoError(t, err, "Classify")
		now := base.Add(time.Duration(i) * time.Hour)
		_, err = Apply(plan, staging, live, filehistory, m, Options{KeepFileHistory: 2, Now: func() time.Time { return now }})
		require.NoError(t, err, "Apply")
	}

	entries, err := os.ReadDir(filehistory)
	require.NoError(t, err, "ReadDir")
	require.Len(t, entries, 2)
}

func TestApplyMissingStagingFileErrors(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()

	// Craft a plan entry that doesn't actually exist in staging, to
	// exercise the error path (Classify itself would never produce this).
	plan := Plan{Entries: []PlanEntry{{Path: "missing.xml", Action: ActionNew, Origin: OriginPristine, StagingHash: "x"}}}
	_, err := Apply(plan, staging, live, filehistory, NewManifest(), Options{})
	require.Error(t, err)
}

func TestApplyWriteFailsWhenLiveDirBlockedByFile(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	writeTree(t, staging, map[string]string{"db/types.xml": "v1"})

	plan, err := Classify(staging, live, NewManifest(), nil)
	require.NoError(t, err, "Classify")
	// Only now block "live/db" with a plain file, after Classify has
	// already produced its plan against the (still-empty) live dir - this
	// isolates the failure to Apply's write step.
	err = os.WriteFile(filepath.Join(live, "db"), []byte("blocker"), 0o600)
	require.NoError(t, err, "write blocker")
	_, err = Apply(plan, staging, live, filehistory, NewManifest(), Options{})
	require.Error(t, err)
}

func TestApplyEmptyPlanIsNoOp(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	report, err := Apply(Plan{}, staging, live, filehistory, NewManifest(), Options{})
	require.NoError(t, err, "Apply")
	require.Empty(t, report.Written)
	require.Empty(t, report.Obsolete)
	entries, err := os.ReadDir(filehistory)
	require.NoError(t, err, "ReadDir")
	assert.Empty(t, entries, "an empty plan must not create a filehistory generation")
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/ce"
)

func TestCopyPristineCopiesTreeAndDirs(t *testing.T) {
	pristine := t.TempDir()
	staging := t.TempDir()
	writeTree(t, pristine, map[string]string{
		"db/types.xml": "types content",
		"init.c":       "init content",
		"env/x.xml":    "env content",
	})

	err := CopyPristine(pristine, staging)
	require.NoError(t, err, "CopyPristine")
	for path, want := range map[string]string{
		"db/types.xml": "types content",
		"init.c":       "init content",
		"env/x.xml":    "env content",
	} {
		got := readString(t, filepath.Join(staging, path))
		assert.Equal(t, want, got)
	}
}

func TestCopyPristineMissingSourceErrors(t *testing.T) {
	err := CopyPristine(filepath.Join(t.TempDir(), "nope"), t.TempDir())
	require.Error(t, err)
}

func TestWriteFileCreatesParentDirs(t *testing.T) {
	staging := t.TempDir()
	err := WriteFile(staging, "mod_123/types.xml", []byte("hi"))
	require.NoError(t, err, "WriteFile")
	got := readString(t, filepath.Join(staging, "mod_123/types.xml"))
	assert.Equal(t, "hi", got)
}

func TestMergeJSONFileCreatesWhenMissing(t *testing.T) {
	staging := t.TempDir()
	err := MergeJSONFile(staging, "cfggameplay.json", []byte(`{"a": 1}`), nil)
	require.NoError(t, err, "MergeJSONFile")
	got := readString(t, filepath.Join(staging, "cfggameplay.json"))
	assert.Contains(t, got, `"a": 1`)
}

func TestMergeJSONFileMergesWithExisting(t *testing.T) {
	staging := t.TempDir()
	err := WriteFile(staging, "cfggameplay.json", []byte(`{"WorldsData": {"objectSpawnersArr": ["a.json"]}}`))
	require.NoError(t, err, "WriteFile")
	overlay := []byte(`{"WorldsData": {"objectSpawnersArr": ["b.json"]}}`)
	err = MergeJSONFile(staging, "cfggameplay.json", overlay, []string{"objectSpawnersArr"})
	require.NoError(t, err, "MergeJSONFile")
	got := readString(t, filepath.Join(staging, "cfggameplay.json"))
	require.Contains(t, got, "a.json")
	assert.Contains(t, got, "b.json")
}

func TestMergeJSONFileInvalidOverlayErrors(t *testing.T) {
	staging := t.TempDir()
	err := MergeJSONFile(staging, "x.json", []byte("not json"), nil)
	require.Error(t, err)
}

func TestMergeXMLFileCreatesRootWhenMissing(t *testing.T) {
	staging := t.TempDir()
	overlay := []byte(`<eventgroupdef><eventgroup name="A"/></eventgroupdef>`)
	err := MergeXMLFile(staging, "cfgeventgroups.xml", "eventgroupdef", overlay, "name")
	require.NoError(t, err, "MergeXMLFile")
	got := readString(t, filepath.Join(staging, "cfgeventgroups.xml"))
	assert.Contains(t, got, `name="A"`)
}

func TestMergeXMLFileMergesWithExisting(t *testing.T) {
	staging := t.TempDir()
	err := WriteFile(staging, "cfgeventgroups.xml", []byte(`<eventgroupdef><eventgroup name="A"/></eventgroupdef>`))
	require.NoError(t, err, "WriteFile")
	overlay := []byte(`<eventgroupdef><eventgroup name="B"/></eventgroupdef>`)
	err = MergeXMLFile(staging, "cfgeventgroups.xml", "eventgroupdef", overlay, "name")
	require.NoError(t, err, "MergeXMLFile")
	got := readString(t, filepath.Join(staging, "cfgeventgroups.xml"))
	require.Contains(t, got, `name="A"`)
	assert.Contains(t, got, `name="B"`)
}

func TestMergeXMLFileInvalidOverlayErrors(t *testing.T) {
	staging := t.TempDir()
	err := MergeXMLFile(staging, "x.xml", "root", []byte("<not valid xml"), "name")
	require.Error(t, err)
}

func TestMergeXMLFileInvalidExistingErrors(t *testing.T) {
	staging := t.TempDir()
	err := WriteFile(staging, "x.xml", []byte("<broken"))
	require.NoError(t, err, "WriteFile")
	err = MergeXMLFile(staging, "x.xml", "root", []byte("<root/>"), "name")
	require.Error(t, err)
}

func TestRegisterCEFolderCreatesEconomyRoot(t *testing.T) {
	staging := t.TempDir()
	files := []ce.CEFile{{Name: "types.xml", Type: "types"}}
	err := RegisterCEFolder(staging, "cfgeconomycore.xml", "mod_123", files)
	require.NoError(t, err, "RegisterCEFolder")
	got := readString(t, filepath.Join(staging, "cfgeconomycore.xml"))
	require.Contains(t, got, `folder="mod_123"`)
	assert.Contains(t, got, `name="types.xml"`)
}

func TestRegisterCEFolderAppendsInOrder(t *testing.T) {
	staging := t.TempDir()
	err := RegisterCEFolder(staging, "cfgeconomycore.xml", "mod_1", nil)
	require.NoError(t, err, "RegisterCEFolder")
	err = RegisterCEFolder(staging, "cfgeconomycore.xml", "mod_2", nil)
	require.NoError(t, err, "RegisterCEFolder")
	got := readString(t, filepath.Join(staging, "cfgeconomycore.xml"))
	idx1 := strings.Index(got, "mod_1")
	idx2 := strings.Index(got, "mod_2")
	require.GreaterOrEqual(t, idx1, 0, "expected mod_1 registered before mod_2, got %q", got)
	require.GreaterOrEqual(t, idx2, 0, "expected mod_1 registered before mod_2, got %q", got)
	assert.LessOrEqual(t, idx1, idx2, "expected mod_1 registered before mod_2, got %q", got)
}

func TestRegisterCEFolderInvalidExistingErrors(t *testing.T) {
	staging := t.TempDir()
	err := WriteFile(staging, "cfgeconomycore.xml", []byte("<broken"))
	require.NoError(t, err, "WriteFile")
	err = RegisterCEFolder(staging, "cfgeconomycore.xml", "mod_1", nil)
	require.Error(t, err)
}

func TestRegisterCEFolderWrongRootErrors(t *testing.T) {
	staging := t.TempDir()
	err := WriteFile(staging, "cfgeconomycore.xml", []byte("<notEconomy/>"))
	require.NoError(t, err, "WriteFile")
	err = RegisterCEFolder(staging, "cfgeconomycore.xml", "mod_1", nil)
	require.Error(t, err)
}

func TestMergeJSONFileWriteErrorPropagates(t *testing.T) {
	staging := t.TempDir()
	// "blocked" exists as a file, so writing "blocked/x.json" must fail
	// at the mkdir step inside writeFileAtomic.
	err := os.WriteFile(filepath.Join(staging, "blocked"), []byte("x"), 0o600)
	require.NoError(t, err, "write blocker")
	err = MergeJSONFile(staging, "blocked/x.json", []byte("{}"), nil)
	require.Error(t, err)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissionDiffAndApply(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	filehistory := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "manifest.json")

	require.NoError(t, os.MkdirAll(filepath.Join(staging, "db"), 0o750), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(staging, "db/types.xml"), []byte("v1"), 0o600), "write")

	out, err := runCmd(t, "mission", "diff", staging, live, "--manifest", manifest)
	require.NoError(t, err, "mission diff")
	assert.Contains(t, out, "db/types.xml", "diff output")
	assert.Contains(t, out, "new", "diff output")

	out, err = runCmd(t, "mission", "apply", staging, live, "--manifest", manifest, "--filehistory", filehistory)
	require.NoError(t, err, "mission apply")
	assert.Contains(t, out, "written: 1", "apply output")

	data, err := os.ReadFile(filepath.Join(live, "db/types.xml"))
	require.NoError(t, err, "read live file")
	assert.Equal(t, "v1", string(data), "live content")

	// Second apply with nothing changed should report no writes.
	out, err = runCmd(t, "mission", "diff", staging, live, "--manifest", manifest)
	require.NoError(t, err, "mission diff (2nd)")
	assert.Contains(t, out, "no changes", "expected no changes on the second diff")
}

func TestMissionDiffRequiresManifestFlag(t *testing.T) {
	_, err := runCmd(t, "mission", "diff", t.TempDir(), t.TempDir())
	require.Error(t, err, "expected an error for missing --manifest")
}

func TestMissionApplyRequiresFilehistoryFlag(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	_, err := runCmd(t, "mission", "apply", t.TempDir(), t.TempDir(), "--manifest", manifest)
	require.Error(t, err, "expected an error for missing --filehistory")
}

func TestMissionDiffWithUnmanagedGlobs(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	require.NoError(t, os.MkdirAll(filepath.Join(staging, "expansion"), 0o750), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(staging, "expansion/x.json"), []byte("x"), 0o600), "write")

	out, err := runCmd(t, "mission", "diff", staging, live, "--manifest", manifest, "--unmanaged", "expansion/**")
	require.NoError(t, err, "mission diff")
	assert.NotContains(t, out, "expansion", "expansion/* should be excluded via --unmanaged")
}

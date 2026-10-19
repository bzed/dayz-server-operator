// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractPatchesFromConfig(t *testing.T) {
	src := `
class CfgPatches {
	class ModA { requiredAddons[] = {"DZ_Data"}; };
	class ModB { requiredAddons[] = {"DZ_Data", "ModA"}; };
};
`
	patches, err := ExtractPatchesFromConfig([]byte(src))
	require.NoError(t, err, "ExtractPatchesFromConfig")
	require.Len(t, patches, 2)
	byName := map[string]Patch{}
	for _, p := range patches {
		byName[p.Name] = p
	}
	assert.True(t, equalStrings(byName["ModB"].RequiredAddons, []string{"DZ_Data", "ModA"}), "ModB.RequiredAddons = %v", byName["ModB"].RequiredAddons)
}

func TestExtractPatchesNoCfgPatches(t *testing.T) {
	patches, err := ExtractPatchesFromConfig([]byte(`class Something { x = 1; };`))
	require.NoError(t, err, "ExtractPatchesFromConfig")
	assert.Nil(t, patches)
}

func TestExtractPatchesNoRequiredAddons(t *testing.T) {
	patches, err := ExtractPatchesFromConfig([]byte(`class CfgPatches { class M { units[] = {}; }; };`))
	require.NoError(t, err, "ExtractPatchesFromConfig")
	require.Len(t, patches, 1)
	require.Equal(t, "M", patches[0].Name)
	assert.Nil(t, patches[0].RequiredAddons)
}

func TestExtractPatchesScalarRequiredAddonsErrors(t *testing.T) {
	_, err := ExtractPatchesFromConfig([]byte(`class CfgPatches { class M { requiredAddons = "not an array"; }; };`))
	require.Error(t, err, "expected an error for a non-array requiredAddons")
}

func TestExtractPatchesInvalidConfigErrors(t *testing.T) {
	_, err := ExtractPatchesFromConfig([]byte(`class {`))
	require.Error(t, err, "expected a parse error to propagate")
}

func TestExtractPatchesFromPBO(t *testing.T) {
	data := buildPBO(t, false, []pboEntry{
		{name: "config.cpp", data: []byte(`class CfgPatches { class M { requiredAddons[] = {"A"}; }; };`)},
	})
	pbo, err := OpenPBO(data)
	require.NoError(t, err, "OpenPBO")
	patches, err := ExtractPatchesFromPBO(pbo)
	require.NoError(t, err, "ExtractPatchesFromPBO")
	require.Len(t, patches, 1)
	assert.Equal(t, "M", patches[0].Name)
}

func TestExtractPatchesFromPBOMissingEntry(t *testing.T) {
	data := buildPBO(t, false, []pboEntry{{name: "other.txt", data: []byte("x")}})
	pbo, err := OpenPBO(data)
	require.NoError(t, err, "OpenPBO")
	_, err = ExtractPatchesFromPBO(pbo)
	require.Error(t, err, "expected an error for a missing config.cpp entry")
}

// Real config.cpp files hold enum blocks, macros and odd tokens after CfgPatches; only that block is needed.
func TestExtractPatchesFallsBackToCfgPatches(t *testing.T) {
	src := `// class CfgPatches { not this one };
#define _ARMA_
enum { destructno = 0, destructtree = 3 };
/* class CfgPatches { nor this } */
class CfgPatches
{
	class Mod_A { text = "}{ in a string"; requiredAddons[] = {"DZ_Data", "ModB"}; };
};
class CfgVehicles { class X: Y { v = -1 * (2 + .5); }; };
`
	patches, err := ExtractPatchesFromConfig([]byte(src))
	require.NoError(t, err, "ExtractPatchesFromConfig")
	require.Len(t, patches, 1)
	require.Equal(t, "Mod_A", patches[0].Name)
	assert.True(t, equalStrings(patches[0].RequiredAddons, []string{"DZ_Data", "ModB"}))
	for _, bad := range []string{`enum { a = 1 }; class Other { };`, `enum { a = 1 }; /* open`, `enum { a = 1 }; class CfgPatches { class M {`} {
		_, err := ExtractPatchesFromConfig([]byte(bad))
		assert.Error(t, err, "no error for %q", bad)
	}
}

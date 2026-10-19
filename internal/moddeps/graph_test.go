// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAllSatisfied(t *testing.T) {
	mods := []Mod{
		{ID: 0, Patches: []Patch{{Name: "DZ_Data"}, {Name: "DZ_Scripts"}}},
		{ID: 1, Patches: []Patch{{Name: "ModA", RequiredAddons: []string{"DZ_Data"}}}},
		{ID: 2, Patches: []Patch{{Name: "ModB", RequiredAddons: []string{"DZ_Data", "ModA"}}}},
	}
	result := Validate(mods)
	assert.True(t, result.OK(), "Missing = %v, want none", result.Missing)
}

func TestValidateMissingDependency(t *testing.T) {
	mods := []Mod{
		{ID: 0, Patches: []Patch{{Name: "DZ_Data"}}},
		{ID: 1, Patches: []Patch{{Name: "ModA", RequiredAddons: []string{"DZ_Data", "SomeMissingMod"}}}},
	}
	result := Validate(mods)
	require.False(t, result.OK(), "expected a missing dependency")
	require.Len(t, result.Missing, 1)
	m := result.Missing[0]
	require.Equal(t, uint64(1), m.ModID)
	require.Equal(t, "ModA", m.PatchName)
	assert.Equal(t, "SomeMissingMod", m.RequiredBy)
	assert.NotEmpty(t, m.String())
}

func TestValidateEmptyMods(t *testing.T) {
	assert.True(t, Validate(nil).OK(), "Validate(nil) should be OK")
}

func TestTopoSortOrdersByDependency(t *testing.T) {
	mods := []Mod{
		{ID: 2, Patches: []Patch{{Name: "ModB", RequiredAddons: []string{"ModA"}}}},
		{ID: 1, Patches: []Patch{{Name: "ModA", RequiredAddons: []string{"DZ_Data"}}}},
		{ID: 0, Patches: []Patch{{Name: "DZ_Data"}}},
	}
	sorted, err := TopoSort(mods)
	require.NoError(t, err, "TopoSort")
	pos := map[uint64]int{}
	for i, m := range sorted {
		pos[m.ID] = i
	}
	assert.Less(t, pos[0], pos[1], "order = %v, want 0 before 1 before 2", idsInOrder(sorted))
	assert.Less(t, pos[1], pos[2], "order = %v, want 0 before 1 before 2", idsInOrder(sorted))
}

func TestTopoSortDeterministicTieBreak(t *testing.T) {
	mods := []Mod{
		{ID: 5, Patches: []Patch{{Name: "M5"}}},
		{ID: 3, Patches: []Patch{{Name: "M3"}}},
		{ID: 1, Patches: []Patch{{Name: "M1"}}},
	}
	sorted, err := TopoSort(mods)
	require.NoError(t, err, "TopoSort")
	require.Equal(t, []uint64{1, 3, 5}, idsInOrder(sorted))
}

func TestTopoSortDetectsCycle(t *testing.T) {
	mods := []Mod{
		{ID: 1, Patches: []Patch{{Name: "ModA", RequiredAddons: []string{"ModB"}}}},
		{ID: 2, Patches: []Patch{{Name: "ModB", RequiredAddons: []string{"ModA"}}}},
	}
	_, err := TopoSort(mods)
	require.Error(t, err, "expected a cycle error")
	var cycleErr ErrCycle
	require.True(t, asErrCycle(err, &cycleErr), "err = %v (%T), want ErrCycle", err, err)
	assert.NotEmpty(t, cycleErr.Error())
}

func TestTopoSortIgnoresUnsatisfiedRequirement(t *testing.T) {
	// A requiredAddons entry nothing provides is not a graph edge (there
	// is no provider to order against); TopoSort must not error on it -
	// that is Validate's job.
	mods := []Mod{
		{ID: 1, Patches: []Patch{{Name: "ModA", RequiredAddons: []string{"NothingProvidesThis"}}}},
	}
	sorted, err := TopoSort(mods)
	require.NoError(t, err, "TopoSort")
	require.Len(t, sorted, 1)
}

func TestTopoSortEmpty(t *testing.T) {
	sorted, err := TopoSort(nil)
	require.NoError(t, err, "TopoSort")
	assert.Empty(t, sorted)
}

func TestTopoSortSelfReferenceIsNotAnEdge(t *testing.T) {
	mods := []Mod{
		{ID: 1, Patches: []Patch{{Name: "ModA", RequiredAddons: []string{"ModA"}}}},
	}
	_, err := TopoSort(mods)
	require.NoError(t, err)
}

func idsInOrder(mods []Mod) []uint64 {
	ids := make([]uint64, len(mods))
	for i, m := range mods {
		ids[i] = m.ID
	}
	return ids
}

func asErrCycle(err error, target *ErrCycle) bool {
	e, ok := err.(ErrCycle)
	if ok {
		*target = e
	}
	return ok
}

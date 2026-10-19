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

func writeConfigCpp(t testing.TB, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.cpp")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600), "write config.cpp")
	return path
}

func TestModCfgPatchesCmd(t *testing.T) {
	path := writeConfigCpp(t, `class CfgPatches { class M { requiredAddons[] = {"A", "B"}; }; };`)
	out, err := runCmd(t, "mod", "cfgpatches", path)
	require.NoError(t, err, "mod cfgpatches: %s", out)
	assert.Contains(t, out, "M requires [A B]")
}

func TestModCfgPatchesCmdMissingFile(t *testing.T) {
	_, err := runCmd(t, "mod", "cfgpatches", "/nonexistent/config.cpp")
	require.Error(t, err, "expected an error for a missing file")
}

func TestModCfgPatchesCmdInvalidConfig(t *testing.T) {
	path := writeConfigCpp(t, `class {`)
	_, err := runCmd(t, "mod", "cfgpatches", path)
	require.Error(t, err, "expected a parse error")
}

func TestModDepsCmdAllSatisfied(t *testing.T) {
	a := writeConfigCpp(t, `class CfgPatches { class ModA { requiredAddons[] = {"DZ_Data"}; }; };`)
	out, err := runCmd(t, "mod", "deps", a, "--provides", "DZ_Data,DZ_Scripts")
	require.NoError(t, err, "mod deps: %s", out)
	assert.Contains(t, out, "all requiredAddons entries are satisfied")
}

func TestModDepsCmdMissingDependency(t *testing.T) {
	a := writeConfigCpp(t, `class CfgPatches { class ModA { requiredAddons[] = {"SomethingMissing"}; }; };`)
	out, err := runCmd(t, "mod", "deps", a)
	require.Error(t, err, "expected an error for a missing dependency")
	assert.Contains(t, out, "SomethingMissing")
}

func TestModDepsCmdShowsOrder(t *testing.T) {
	a := writeConfigCpp(t, `class CfgPatches { class ModA { requiredAddons[] = {"DZ_Data"}; }; };`)
	b := writeConfigCpp(t, `class CfgPatches { class ModB { requiredAddons[] = {"ModA"}; }; };`)
	out, err := runCmd(t, "mod", "deps", b, a, "--provides", "DZ_Data", "--order")
	require.NoError(t, err, "mod deps: %s", out)
	assert.Contains(t, out, "load order")
}

func TestModDepsCmdMissingFile(t *testing.T) {
	_, err := runCmd(t, "mod", "deps", "/nonexistent/config.cpp")
	require.Error(t, err, "expected an error for a missing file")
}

func leU32(v int) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }

// minimalPBO packs one uncompressed entry into a PBO: header record,
// terminating zero record, data.
func minimalPBO(name string, data []byte) []byte {
	out := append([]byte(name), 0)
	out = append(out, leU32(0)...)
	out = append(out, leU32(len(data))...)
	out = append(out, leU32(0)...)
	out = append(out, leU32(0)...)
	out = append(out, leU32(len(data))...)
	out = append(out, make([]byte, 21)...)
	return append(out, data...)
}

// rapifiedPatches is a hand-assembled config.bin:
// CfgPatches { M { requiredAddons[] = {"A"}; }; }.
func rapifiedPatches() []byte {
	bin := []byte{0, 'r', 'a', 'P', 0, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 0}
	bin = append(bin, 0, 1, 0) // root: no parent, 1 entry, type class
	bin = append(bin, "CfgPatches\x00"...)
	bin = append(bin, leU32(34)...)    // body offset
	bin = append(bin, 0, 1, 0, 'M', 0) // CfgPatches body: 1 class "M"
	bin = append(bin, leU32(43)...)    // body offset
	bin = append(bin, 0, 1, 2)         // M body: no parent, 1 entry, type array
	bin = append(bin, "requiredAddons\x00"...)
	return append(bin, 1, 0, 'A', 0)
}

func TestModCfgPatchesCmdPBOWithConfigBin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mod.pbo")
	require.NoError(t, os.WriteFile(path, minimalPBO("config.bin", rapifiedPatches()), 0o600))
	out, err := runCmd(t, "mod", "cfgpatches", path)
	require.NoError(t, err, "mod cfgpatches: %s", out)
	assert.Contains(t, out, "M requires [A]")
	out, err = runCmd(t, "mod", "deps", path, "--provides", "A")
	require.NoError(t, err, "mod deps: %s", out)
}

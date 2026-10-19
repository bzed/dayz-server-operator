// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfigSimpleClass(t *testing.T) {
	src := `
class CfgPatches
{
	class MyMod
	{
		units[] = {};
		weapons[] = {};
		requiredVersion = 0.1;
		requiredAddons[] = {"DZ_Data", "DZ_Scripts", "DZ_Weapons_Melee"};
	};
};
`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	patches, ok := root.Classes["CfgPatches"]
	require.True(t, ok, "CfgPatches not found")
	myMod, ok := patches.Classes["MyMod"]
	require.True(t, ok, "MyMod not found")
	req, ok := myMod.Properties["requiredAddons"]
	require.True(t, ok)
	require.True(t, req.IsArray)
	want := []string{"DZ_Data", "DZ_Scripts", "DZ_Weapons_Melee"}
	assert.True(t, equalStrings(req.Array, want), "requiredAddons = %v, want %v", req.Array, want)
	assert.Equal(t, "0.1", myMod.Properties["requiredVersion"].Scalar)
}

func TestParseConfigMultipleTopLevelClasses(t *testing.T) {
	src := `
class CfgPatches
{
	class ModA { requiredAddons[] = {"DZ_Data"}; };
	class ModB { requiredAddons[] = {"DZ_Data", "ModA"}; };
};
class CfgMods
{
	class MyMod { name = "My Mod"; };
};
`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	assert.Len(t, root.Classes["CfgPatches"].Classes, 2)
	_, ok := root.Classes["CfgMods"]
	assert.True(t, ok)
}

func TestParseConfigParentClass(t *testing.T) {
	src := `class Base { x = 1; }; class Derived : Base { y = 2; };`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	assert.Equal(t, "Base", root.Classes["Derived"].Parent)
}

func TestParseConfigForwardDeclaration(t *testing.T) {
	src := `class Foo; class CfgPatches { class Bar { requiredAddons[] = {"Foo"}; }; };`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	_, ok := root.Classes["Foo"]
	assert.True(t, ok)
}

func TestParseConfigComments(t *testing.T) {
	src := `
// line comment
class CfgPatches /* block comment */ {
	class M { requiredAddons[] = {"A" /* inline */, "B"}; }; // trailing
};
`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	req := root.Classes["CfgPatches"].Classes["M"].Properties["requiredAddons"]
	assert.True(t, equalStrings(req.Array, []string{"A", "B"}), "requiredAddons = %v", req.Array)
}

func TestParseConfigPreprocessorLinesIgnored(t *testing.T) {
	src := `
#define MY_MACRO 1
class CfgPatches {
	class M { requiredAddons[] = {"A"}; };
};
`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	_, ok := root.Classes["CfgPatches"]
	require.True(t, ok)
}

func TestParseConfigEscapedQuoteInString(t *testing.T) {
	src := `class M { name = "Say ""Hi""."; };`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	got := root.Classes["M"].Properties["name"].Scalar
	assert.Equal(t, `Say "Hi".`, got)
}

func TestParseConfigPlusEqualsArray(t *testing.T) {
	src := `class M { requiredAddons[] = {"A"}; requiredAddons[] += {"B"}; };`
	// += is accepted grammatically (real config.cpp uses it to extend a
	// parent's array); this parser does not merge with a prior value, it
	// just records the latest assignment - documented as a simplification.
	_, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
}

func TestParseConfigErrors(t *testing.T) {
	cases := []string{
		`class {`,                // missing name
		`class M { x = ; };`,     // missing value
		`class M { x[] = (); };`, // wrong array delimiter
		`"unterminated`,
		`/* unterminated`,
	}
	for _, c := range cases {
		_, err := ParseConfig([]byte(c))
		assert.Error(t, err, "ParseConfig(%q): expected an error", c)
	}
}

func TestParseConfigNumberScalar(t *testing.T) {
	src := `class M { requiredVersion = -1.5; };`
	root, err := ParseConfig([]byte(src))
	require.NoError(t, err, "ParseConfig")
	got := root.Classes["M"].Properties["requiredVersion"].Scalar
	assert.Equal(t, "-1.5", got)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseConfigClassNameStartingWithDigits(t *testing.T) {
	root, err := ParseConfig([]byte(`class CfgPatches { class 416M4_Sounds { requiredAddons[] = {"DZ_Data"}; v = 0.1; }; };`))
	require.NoError(t, err)
	c := root.Classes["CfgPatches"].Classes["416M4_Sounds"]
	require.NotNil(t, c)
	assert.Equal(t, "0.1", c.Properties["v"].Scalar)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import "testing"

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
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	patches, ok := root.Classes["CfgPatches"]
	if !ok {
		t.Fatal("CfgPatches not found")
	}
	myMod, ok := patches.Classes["MyMod"]
	if !ok {
		t.Fatal("MyMod not found")
	}
	req, ok := myMod.Properties["requiredAddons"]
	if !ok || !req.IsArray {
		t.Fatalf("requiredAddons = %+v, ok=%v", req, ok)
	}
	want := []string{"DZ_Data", "DZ_Scripts", "DZ_Weapons_Melee"}
	if !equalStrings(req.Array, want) {
		t.Errorf("requiredAddons = %v, want %v", req.Array, want)
	}
	if myMod.Properties["requiredVersion"].Scalar != "0.1" {
		t.Errorf("requiredVersion = %q", myMod.Properties["requiredVersion"].Scalar)
	}
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
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(root.Classes["CfgPatches"].Classes) != 2 {
		t.Errorf("CfgPatches has %d children, want 2", len(root.Classes["CfgPatches"].Classes))
	}
	if _, ok := root.Classes["CfgMods"]; !ok {
		t.Error("CfgMods not found")
	}
}

func TestParseConfigParentClass(t *testing.T) {
	src := `class Base { x = 1; }; class Derived : Base { y = 2; };`
	root, err := ParseConfig([]byte(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if root.Classes["Derived"].Parent != "Base" {
		t.Errorf("Parent = %q, want Base", root.Classes["Derived"].Parent)
	}
}

func TestParseConfigForwardDeclaration(t *testing.T) {
	src := `class Foo; class CfgPatches { class Bar { requiredAddons[] = {"Foo"}; }; };`
	root, err := ParseConfig([]byte(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if _, ok := root.Classes["Foo"]; !ok {
		t.Error("forward-declared Foo should still be recorded")
	}
}

func TestParseConfigComments(t *testing.T) {
	src := `
// line comment
class CfgPatches /* block comment */ {
	class M { requiredAddons[] = {"A" /* inline */, "B"}; }; // trailing
};
`
	root, err := ParseConfig([]byte(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	req := root.Classes["CfgPatches"].Classes["M"].Properties["requiredAddons"]
	if !equalStrings(req.Array, []string{"A", "B"}) {
		t.Errorf("requiredAddons = %v", req.Array)
	}
}

func TestParseConfigPreprocessorLinesIgnored(t *testing.T) {
	src := `
#define MY_MACRO 1
class CfgPatches {
	class M { requiredAddons[] = {"A"}; };
};
`
	root, err := ParseConfig([]byte(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if _, ok := root.Classes["CfgPatches"]; !ok {
		t.Fatal("CfgPatches not found")
	}
}

func TestParseConfigEscapedQuoteInString(t *testing.T) {
	src := `class M { name = "Say ""Hi""."; };`
	root, err := ParseConfig([]byte(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := root.Classes["M"].Properties["name"].Scalar; got != `Say "Hi".` {
		t.Errorf("name = %q", got)
	}
}

func TestParseConfigPlusEqualsArray(t *testing.T) {
	src := `class M { requiredAddons[] = {"A"}; requiredAddons[] += {"B"}; };`
	// += is accepted grammatically (real config.cpp uses it to extend a
	// parent's array); this parser does not merge with a prior value, it
	// just records the latest assignment - documented as a simplification.
	if _, err := ParseConfig([]byte(src)); err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
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
		if _, err := ParseConfig([]byte(c)); err == nil {
			t.Errorf("ParseConfig(%q): expected an error", c)
		}
	}
}

func TestParseConfigNumberScalar(t *testing.T) {
	src := `class M { requiredVersion = -1.5; };`
	root, err := ParseConfig([]byte(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := root.Classes["M"].Properties["requiredVersion"].Scalar; got != "-1.5" {
		t.Errorf("requiredVersion = %q", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	c := root.Classes["CfgPatches"].Classes["416M4_Sounds"]
	if c == nil || c.Properties["v"].Scalar != "0.1" {
		t.Errorf("class = %+v", c)
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/site"
)

func stagingTree(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(d, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("cfgeconomycore.xml", `<economycore><ce folder="db"><file name="types.xml" type="types"/></ce></economycore>`)
	put("db/types.xml", `<types/>`)
	put("mapgrouppos.xml", `<map><group name="A" pos="1 2 3"/></map>`)
	put("cfggameplay.json", `{"version":119,"WorldsData":{"objectSpawnersArr":["base.json"]}}`)
	put("db/messages.xml", `<messages/>`)
	put("init.c", "void main()\n{\n}\n")
	return d
}

func readStaging(t *testing.T, d, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d, rel)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIntegrateRegistersCEFoldersInOrder(t *testing.T) {
	d := stagingTree(t)
	res, err := Integrate(d, []Contribution{
		{Name: "mod 1", Folder: "mod_1", Files: []ContribFile{{Name: "types.xml", Data: []byte(`<types><type name="A"/></types>`)}, {Name: "cfgspawnabletypes.xml", Data: []byte(`<spawnabletypes/>`)}}},
		{Name: "mod 2", Folder: "mod_2", Files: []ContribFile{{Name: "events.xml", Data: []byte(`<events/>`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	core := readStaging(t, d, "cfgeconomycore.xml")
	i1, i2 := strings.Index(core, `folder="mod_1"`), strings.Index(core, `folder="mod_2"`)
	if i1 < 0 || i2 < i1 {
		t.Errorf("mod_1 must be registered before mod_2:\n%s", core)
	}
	if !strings.Contains(core, `type="spawnabletypes"`) {
		t.Errorf("file types missing:\n%s", core)
	}
	if err := ValidateStaging(d, res.Touched); err != nil {
		t.Errorf("a clean merge must validate: %v", err)
	}
	// A registered file that is gone fails the validation.
	if err := os.Remove(filepath.Join(d, "mod_1", "types.xml")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStaging(d, res.Touched); err == nil || !strings.Contains(err.Error(), "mod_1") {
		t.Errorf("a missing CE file must be reported: %v", err)
	}
}

func TestIntegrateMergesXMLAndJSON(t *testing.T) {
	d := stagingTree(t)
	_, err := Integrate(d, []Contribution{
		{Name: "mod 1", Folder: "mod_1", Files: []ContribFile{
			{Name: "mapgrouppos.xml", Data: []byte(`<map><group name="B" pos="4 5 6"/><group name="A" pos="9 9 9"/><group name="A" pos="1 2 3" a="7"/></map>`)},
			{Name: "cfggameplay.json", Data: []byte(`{"WorldsData":{"objectSpawnersArr":["base.json","mod.json"]},"x":1}`)},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := readStaging(t, d, "mapgrouppos.xml")
	// mapgrouppos.xml has many groups of one name, one per position: a group is replaced only when
	// name and position are the same, a group of the same name elsewhere is added
	if strings.Count(m, `name="A"`) != 2 || !strings.Contains(m, `pos="9 9 9"`) || !strings.Contains(m, `a="7"`) || !strings.Contains(m, `name="B"`) {
		t.Errorf("a group is replaced by name and position, others are appended:\n%s", m)
	}
	g := readStaging(t, d, "cfggameplay.json")
	if strings.Count(g, "base.json") != 1 || !strings.Contains(g, "mod.json") || !strings.Contains(g, `"x": 1`) {
		t.Errorf("gameplay merge:\n%s", g)
	}
}

func TestIntegrateOverlayReferencesAndExtras(t *testing.T) {
	d := stagingTree(t)
	_, err := Integrate(d, []Contribution{{
		Name: "overlay loadout", Folder: "custom_loadout",
		Files: []ContribFile{
			{Name: "spawns/a.json", Data: []byte(`{}`)}, {Name: "vanilla_loadout.json", Data: []byte(`{}`)}, {Name: "notes.txt", Data: []byte("x")},
		},
		Overlay: site.OverlayManifest{SpawnGearPresets: []string{"vanilla_*.json"}, ObjectSpawners: []string{"spawns/*.json"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"custom_loadout/spawns/a.json", "custom_loadout/vanilla_loadout.json", "custom_loadout/notes.txt"} {
		if _, err := os.Stat(filepath.Join(d, f)); err != nil {
			t.Errorf("extra file %s missing: %v", f, err)
		}
	}
	g := readStaging(t, d, "cfggameplay.json")
	if !strings.Contains(g, "custom_loadout/vanilla_loadout.json") || !strings.Contains(g, "custom_loadout/spawns/a.json") || !strings.Contains(g, "base.json") {
		t.Errorf("the declared files must be referenced with their path, the base kept:\n%s", g)
	}
	if _, err := Integrate(d, []Contribution{{Name: "bad", Folder: "custom_b", Files: []ContribFile{{Name: "a.json", Data: []byte("{}")}}, Overlay: site.OverlayManifest{ObjectSpawners: []string{"["}}}}); err == nil {
		t.Error("a malformed pattern must be an error")
	}
}

func TestIntegrateReplaceReportsConflicts(t *testing.T) {
	d := stagingTree(t)
	res, err := Integrate(d, []Contribution{
		{Name: "mod 1", Folder: "mod_1", Files: []ContribFile{{Name: "cfgweather.xml", Data: []byte(`<weather a="1"/>`)}, {Name: "messages.xml", Data: []byte(`<messages a="1"/>`)}}},
		{Name: "mod 2", Folder: "mod_2", Files: []ContribFile{{Name: "cfgweather.xml", Data: []byte(`<weather a="2"/>`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readStaging(t, d, "cfgweather.xml"); !strings.Contains(got, `a="2"`) {
		t.Errorf("the later contributor wins: %s", got)
	}
	if got := readStaging(t, d, "db/messages.xml"); !strings.Contains(got, `a="1"`) {
		t.Errorf("messages.xml goes to db/: %s", got)
	}
	if len(res.Conflicts) != 1 || !strings.Contains(res.Conflicts[0], "mod 2") {
		t.Errorf("conflicts = %v", res.Conflicts)
	}
}

func TestIntegrateGlobalsAndUnsafeNames(t *testing.T) {
	d := stagingTree(t)
	res, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "globals.xml", Data: []byte(`<variables/>`)}}}})
	if err != nil || len(res.Notes) != 1 {
		t.Fatalf("globals: err=%v notes=%v", err, res.Notes)
	}
	if strings.Contains(readStaging(t, d, "cfgeconomycore.xml"), "mod_1") {
		t.Error("globals.xml must not be registered as a CE file")
	}
	for _, bad := range []string{"../x.xml", "/etc/passwd", ""} {
		if _, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: bad, Data: []byte("x")}}}}); err == nil {
			t.Errorf("name %q must be rejected", bad)
		}
	}
}

func TestIntegrateAfterMergeAndErrors(t *testing.T) {
	d := stagingTree(t)
	var seen string
	_, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", AfterMerge: func(s string) error { seen = s; return nil }}})
	if err != nil || seen != d {
		t.Fatalf("AfterMerge: err=%v saw %q", err, seen)
	}
	if _, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", AfterMerge: func(string) error { return errors.New("no") }}}); err == nil || !strings.Contains(err.Error(), "post_merge") {
		t.Errorf("a failing hook must fail the merge: %v", err)
	}
	if _, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "cfggameplay.json", Data: []byte("{")}}}}); err == nil {
		t.Error("malformed JSON must fail the merge")
	}
	if err := ValidateStaging(d, []string{"cfggameplay.json", "nope.xml"}); err == nil {
		t.Error("a touched file that is missing must fail the validation")
	}
	bad := filepath.Join(d, "broken.xml")
	if err := os.WriteFile(bad, []byte("<a>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStaging(d, []string{"broken.xml"}); err == nil {
		t.Error("broken XML must fail the validation")
	}
}

func TestIntegratePatchesInitC(t *testing.T) {
	if _, err := exec.LookPath("patch"); err != nil {
		t.Skip("patch(1) is not installed")
	}
	d := stagingTree(t)
	diff := "--- init.c\n+++ init.c\n@@ -1,3 +1,4 @@\n void main()\n {\n+\tPrint(\"hello\");\n }\n"
	if _, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "init.c", Data: []byte(diff)}}}}); err != nil {
		t.Fatal(err)
	}
	if got := readStaging(t, d, "init.c"); !strings.Contains(got, `Print("hello");`) {
		t.Errorf("init.c = %q", got)
	}
	if _, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "init.c", Data: []byte("not a patch\n")}}}}); err == nil {
		t.Error("a diff that does not apply must fail the merge")
	}
	if err := os.Remove(filepath.Join(d, "init.c")); err != nil {
		t.Fatal(err)
	}
	if _, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "init.c", Data: []byte(diff)}}}}); err == nil {
		t.Error("a mission without init.c cannot be patched")
	}
}

func TestIntegrateStrategiesOnlyApplyAtTheTop(t *testing.T) {
	d := stagingTree(t)
	res, err := Integrate(d, []Contribution{{Name: "o", Folder: "custom_o", Files: []ContribFile{
		{Name: "spawns/types.xml", Data: []byte("<types/>")},
		{Name: "env/zombie_territories.xml", Data: []byte(`<territory-type><territory name="Z"/></territory-type>`)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "custom_o", "spawns", "types.xml")); err != nil {
		t.Errorf("a nested types.xml is an extra file: %v", err)
	}
	if strings.Contains(readStaging(t, d, "cfgeconomycore.xml"), "custom_o") {
		t.Error("a nested types.xml must not be registered as CE data")
	}
	if got := readStaging(t, d, "env/zombie_territories.xml"); !strings.Contains(got, `name="Z"`) {
		t.Errorf("env/zombie_territories.xml merges into its mission file: %s", got)
	}
	if len(res.Touched) == 0 {
		t.Error("touched files must be listed")
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/site"
)

func stagingTree(t testing.TB) string {
	t.Helper()
	d := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(d, rel)
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		require.NoError(t, err)
		err = os.WriteFile(p, []byte(body), 0o600)
		require.NoError(t, err)
	}
	put("cfgeconomycore.xml", `<economycore><ce folder="db"><file name="types.xml" type="types"/></ce></economycore>`)
	put("db/types.xml", `<types/>`)
	put("mapgrouppos.xml", `<map><group name="A" pos="1 2 3"/></map>`)
	put("cfggameplay.json", `{"version":119,"WorldsData":{"objectSpawnersArr":["base.json"]}}`)
	put("db/messages.xml", `<messages/>`)
	put("init.c", "void main()\n{\n}\n")
	return d
}

func readStaging(t testing.TB, d, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d, rel)) //nolint:gosec // test fixture
	require.NoError(t, err)
	return string(b)
}

func TestIntegrateRegistersCEFoldersInOrder(t *testing.T) {
	d := stagingTree(t)
	res, err := Integrate(d, []Contribution{
		{Name: "mod 1", Folder: "mod_1", Files: []ContribFile{{Name: "types.xml", Data: []byte(`<types><type name="A"/></types>`)}, {Name: "cfgspawnabletypes.xml", Data: []byte(`<spawnabletypes/>`)}}},
		{Name: "mod 2", Folder: "mod_2", Files: []ContribFile{{Name: "events.xml", Data: []byte(`<events/>`)}}},
	})
	require.NoError(t, err)
	core := readStaging(t, d, "cfgeconomycore.xml")
	i1, i2 := strings.Index(core, `folder="mod_1"`), strings.Index(core, `folder="mod_2"`)
	require.GreaterOrEqual(t, i1, 0, "mod_1 must be registered before mod_2:\n%s", core)
	assert.GreaterOrEqual(t, i2, i1, "mod_1 must be registered before mod_2:\n%s", core)
	assert.Contains(t, core, `type="spawnabletypes"`)
	err = ValidateStaging(d, res.Touched)
	require.NoError(t, err, "a clean merge must validate")
	// A registered file that is gone fails the validation.
	err = os.Remove(filepath.Join(d, "mod_1", "types.xml"))
	require.NoError(t, err)
	err = ValidateStaging(d, res.Touched)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mod_1")
}

func TestIntegrateMergesXMLAndJSON(t *testing.T) {
	d := stagingTree(t)
	_, err := Integrate(d, []Contribution{
		{Name: "mod 1", Folder: "mod_1", Files: []ContribFile{
			{Name: "mapgrouppos.xml", Data: []byte(`<map><group name="B" pos="4 5 6"/><group name="A" pos="9 9 9"/><group name="A" pos="1 2 3" a="7"/></map>`)},
			{Name: "cfggameplay.json", Data: []byte(`{"WorldsData":{"objectSpawnersArr":["base.json","mod.json"]},"x":1}`)},
		}},
	})
	require.NoError(t, err)
	m := readStaging(t, d, "mapgrouppos.xml")
	// mapgrouppos.xml has many groups of one name, one per position: a group is replaced only when
	// name and position are the same, a group of the same name elsewhere is added
	require.Equal(t, 2, strings.Count(m, `name="A"`))
	require.Contains(t, m, `pos="9 9 9"`)
	require.Contains(t, m, `a="7"`)
	assert.Contains(t, m, `name="B"`)
	g := readStaging(t, d, "cfggameplay.json")
	require.Equal(t, 1, strings.Count(g, "base.json"))
	require.Contains(t, g, "mod.json")
	assert.Contains(t, g, `"x": 1`)
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
	require.NoError(t, err)
	for _, f := range []string{"custom_loadout/spawns/a.json", "custom_loadout/vanilla_loadout.json", "custom_loadout/notes.txt"} {
		_, err := os.Stat(filepath.Join(d, f))
		require.NoError(t, err)
	}
	g := readStaging(t, d, "cfggameplay.json")
	require.Contains(t, g, "custom_loadout/vanilla_loadout.json")
	require.Contains(t, g, "custom_loadout/spawns/a.json")
	assert.Contains(t, g, "base.json")
	_, err = Integrate(d, []Contribution{{Name: "bad", Folder: "custom_b", Files: []ContribFile{{Name: "a.json", Data: []byte("{}")}}, Overlay: site.OverlayManifest{ObjectSpawners: []string{"["}}}})
	require.Error(t, err, "a malformed pattern must be an error")
}

func TestIntegrateReplaceReportsConflicts(t *testing.T) {
	d := stagingTree(t)
	res, err := Integrate(d, []Contribution{
		{Name: "mod 1", Folder: "mod_1", Files: []ContribFile{{Name: "cfgweather.xml", Data: []byte(`<weather a="1"/>`)}, {Name: "messages.xml", Data: []byte(`<messages a="1"/>`)}}},
		{Name: "mod 2", Folder: "mod_2", Files: []ContribFile{{Name: "cfgweather.xml", Data: []byte(`<weather a="2"/>`)}}},
	})
	require.NoError(t, err)
	got := readStaging(t, d, "cfgweather.xml")
	assert.Contains(t, got, `a="2"`)
	got = readStaging(t, d, "db/messages.xml")
	assert.Equal(t, site.EmptyMessagesXML, got)
	require.Len(t, res.Conflicts, 1)
	assert.Contains(t, res.Conflicts[0], "mod 2")
}

func TestIntegrateGlobalsAndUnsafeNames(t *testing.T) {
	d := stagingTree(t)
	err := os.WriteFile(filepath.Join(d, "db/globals.xml"), []byte(`<variables><var name="A" type="0" value="1"/><var name="B" type="0" value="1"/></variables>`), 0o600)
	require.NoError(t, err)
	res, err := Integrate(d, []Contribution{
		{Name: "m1", Folder: "mod_1", Files: []ContribFile{{Name: "globals.xml", Data: []byte(`<variables><var name="B" type="0" value="2"/></variables>`)}}},
		{Name: "m2", Folder: "mod_2", Files: []ContribFile{{Name: "globals.xml", Data: []byte(`<variables><var name="C" type="0" value="3"/></variables>`)}}},
	})
	require.NoError(t, err)
	g := readStaging(t, d, "db/globals.xml")
	for _, want := range []string{`name="A"`, `name="B" type="0" value="2"`, `name="C"`} {
		assert.Contains(t, g, want)
	}
	assert.True(t, slices.Contains(res.Touched, "db/globals.xml"), "touched = %v", res.Touched)
	assert.NotContains(t, readStaging(t, d, "cfgeconomycore.xml"), "mod_1", "globals.xml must not be registered as a CE file")
	for _, bad := range []string{"../x.xml", "/etc/passwd", ""} {
		_, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: bad, Data: []byte("x")}}}})
		require.Error(t, err, "name %q must be rejected", bad)
	}
}

func TestIntegrateAfterMergeAndErrors(t *testing.T) {
	d := stagingTree(t)
	var seen string
	_, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", AfterMerge: func(s string) error { seen = s; return nil }}})
	require.NoError(t, err, "AfterMerge: err=%v saw %q", err, seen)
	require.Equal(t, d, seen, "AfterMerge: err=%v saw %q", err, seen)
	_, err = Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", AfterMerge: func(string) error { return errors.New("no") }}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "post_merge")
	_, err = Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "cfggameplay.json", Data: []byte("{")}}}})
	require.Error(t, err, "malformed JSON must fail the merge")
	err = ValidateStaging(d, []string{"cfggameplay.json", "nope.xml"})
	require.Error(t, err)
	bad := filepath.Join(d, "broken.xml")
	err = os.WriteFile(bad, []byte("<a>"), 0o600)
	require.NoError(t, err)
	err = ValidateStaging(d, []string{"broken.xml"})
	require.Error(t, err, "broken XML must fail the validation")
}

func TestIntegratePatchesInitC(t *testing.T) {
	if _, err := exec.LookPath("patch"); err != nil {
		t.Skip("patch(1) is not installed")
	}
	d := stagingTree(t)
	diff := "--- init.c\n+++ init.c\n@@ -1,3 +1,4 @@\n void main()\n {\n+\tPrint(\"hello\");\n }\n"
	_, err := Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "init.c", Data: []byte(diff)}}}})
	require.NoError(t, err)
	got := readStaging(t, d, "init.c")
	assert.Contains(t, got, `Print("hello");`)
	_, err = Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "init.c", Data: []byte("not a patch\n")}}}})
	require.Error(t, err, "a diff that does not apply must fail the merge")
	err = os.Remove(filepath.Join(d, "init.c"))
	require.NoError(t, err)
	_, err = Integrate(d, []Contribution{{Name: "m", Folder: "mod_1", Files: []ContribFile{{Name: "init.c", Data: []byte(diff)}}}})
	require.Error(t, err, "a mission without init.c cannot be patched")
}

func TestIntegrateStrategiesOnlyApplyAtTheTop(t *testing.T) {
	d := stagingTree(t)
	res, err := Integrate(d, []Contribution{{Name: "o", Folder: "custom_o", Files: []ContribFile{
		{Name: "spawns/types.xml", Data: []byte("<types/>")},
		{Name: "env/zombie_territories.xml", Data: []byte(`<territory-type><territory name="Z"/></territory-type>`)},
	}}})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(d, "custom_o", "spawns", "types.xml"))
	require.NoError(t, err, "a nested types.xml is an extra file")
	assert.NotContains(t, readStaging(t, d, "cfgeconomycore.xml"), "custom_o", "a nested types.xml must not be registered as CE data")
	got := readStaging(t, d, "env/zombie_territories.xml")
	assert.Contains(t, got, `name="Z"`)
	assert.NotEmpty(t, res.Touched, "touched files must be listed")
}

func TestMessagesXMLStaysEmpty(t *testing.T) {
	d := stagingTree(t)
	full := []byte("<messages><message><deadline>60</deadline><shutdown>1</shutdown><text>x</text></message></messages>")
	_, err := Integrate(d, []Contribution{
		{Name: "instance messages.xml", Folder: "custom_instance", Files: []ContribFile{{Name: "messages.xml", Data: []byte(site.EmptyMessagesXML)}}},
		{Name: "overlay o", Folder: "custom_o", Files: []ContribFile{{Name: "db/messages.xml", Data: full}}},
	})
	require.NoError(t, err)
	got := readStaging(t, d, "db/messages.xml")
	assert.Equal(t, site.EmptyMessagesXML, got)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package integrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func put(t testing.TB, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func tree(t testing.TB, dir string) *site.Tree {
	t.Helper()
	put(t, filepath.Join(dir, "instances", "x", "instance.yaml"), "name: x\nproduct: dayz-stable\nmap: empty.m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 2302, rcon: 2306, query: 27016}\nnetwork: host\n")
	tr, err := site.LoadTree(dir)
	require.NoError(t, err)
	return tr
}

func TestCollectOrderAndSources(t *testing.T) {
	dir := t.TempDir()
	modDir := filepath.Join(t.TempDir(), "mod")
	put(t, filepath.Join(modDir, "info", "events.xml"), "<events/>")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), `mod: 1
name: One
aliases: [2]
files:
  types.xml: {source: local, path: files/types.xml}
  events.xml: {source: mod, path: ./info/events.xml}
  cfgeventspawns.xml: {source: local, path: files/spawns.xml, maps: [other.map]}
  cfggameplay.json: {source: local, path: files/gp.json, maps: [empty.m]}
normalize: [xml-decl]
`)
	put(t, filepath.Join(dir, "integrations", "mods", "1", "files", "types.xml"), "<types/>")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "files", "spawns.xml"), "<eventposdef/>")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "files", "gp.json"), "{}")
	put(t, filepath.Join(dir, "overlays", "loadout", "overlay.yaml"), "spawn_gear_presets: [\"*.json\"]\n")
	put(t, filepath.Join(dir, "overlays", "loadout", "gear.json"), "{}")
	put(t, filepath.Join(dir, "overlays", "loadout", "sub", "a.txt"), "a")
	tr := tree(t, dir)

	var logged []string
	inst := &resolve.Instance{
		Name: "x", Map: "empty.m", Overlays: []string{"loadout"},
		Mods: []resolve.Mod{{Local: "dzo-admin", Server: true}, {ID: 2, Dir: modDir}, {ID: 3}},
	}
	cs, err := Collect(t.Context(), tr, inst, Options{CacheDir: t.TempDir(), Log: func(f string, a ...any) { logged = append(logged, f) }})
	require.NoError(t, err)
	var got []string
	for _, c := range cs {
		got = append(got, c.Name)
	}
	require.Equal(t, []string{"instance messages.xml", "mod 2 (One)", "overlay loadout"}, got,
		"a local mod and a mod without integration add nothing; the alias finds the integration")
	mod := cs[1]
	assert.Equal(t, "mod_2", mod.Folder)
	var files []string
	for _, f := range mod.Files {
		files = append(files, f.Name)
	}
	assert.Equal(t, []string{"cfggameplay.json", "events.xml", "types.xml"}, files,
		"the variant for another map is left out, the others sorted")
	for _, f := range mod.Files {
		if f.Name == "types.xml" {
			assert.True(t, strings.HasPrefix(string(f.Data), "<?xml"), "normalize: [xml-decl] was not applied to types.xml: %q", f.Data)
		}
		if f.Name == "cfggameplay.json" {
			assert.False(t, strings.HasPrefix(string(f.Data), "<?xml"), "a json file must not be normalised")
		}
	}
	ov := cs[2]
	assert.Equal(t, "custom_loadout", ov.Folder)
	assert.Len(t, ov.Overlay.SpawnGearPresets, 1)
	assert.Len(t, ov.Files, 2)
}

func TestInstanceIntegrationOverridesTheSharedOne(t *testing.T) {
	dir := t.TempDir()
	for _, where := range []string{"integrations/mods/1", "instances/x/integrations/1"} {
		put(t, filepath.Join(dir, where, "integration.yaml"), "mod: 1\nname: "+filepath.Base(filepath.Dir(where))+"\nfiles:\n  types.xml: {source: local, path: t.xml}\n")
		put(t, filepath.Join(dir, where, "t.xml"), "<types name=\""+where+"\"/>")
	}
	tr := tree(t, dir)
	cs, err := Collect(t.Context(), tr, &resolve.Instance{Name: "x", Map: "m", Mods: []resolve.Mod{{ID: 1}}}, Options{})
	require.NoError(t, err)
	require.Len(t, cs, 2) // the instance messages.xml comes first
	assert.Contains(t, string(cs[1].Files[0].Data), "instances/x/integrations/1", "the instance's own integration must win")
}

func TestSourcesAreContainedAndMustExist(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	put(t, outside, "secret")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  types.xml: {source: local, path: ../../../../secret}\n")
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "m", Mods: []resolve.Mod{{ID: 1}}}
	_, err := Collect(t.Context(), tr, inst, Options{})
	require.Error(t, err, "a local path that leaves the integration folder must be refused")
	// a link out is no way around it
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  types.xml: {source: local, path: link.xml}\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "integrations", "mods", "1", "link.xml")))
	tr = tree(t, dir)
	_, err = Collect(t.Context(), tr, inst, Options{})
	require.Error(t, err, "a link out of the folder must be refused")
	// a mod source needs the mod
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  events.xml: {source: mod, path: info/events.xml}\n")
	tr = tree(t, dir)
	_, err = Collect(t.Context(), tr, inst, Options{})
	require.ErrorContains(t, err, "not downloaded")
	_, err = Collect(t.Context(), tr, &resolve.Instance{Name: "x", Map: "m", Overlays: []string{"nope"}}, Options{})
	assert.Error(t, err, "an overlay that does not exist must be an error")
}

func TestURLSourceIsCachedAndPinned(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("<types/>"))
	}))
	defer srv.Close()
	sum := sha256.Sum256([]byte("<types/>"))
	good := hex.EncodeToString(sum[:])

	dir := t.TempDir()
	write := func(src string) *site.Tree {
		put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  types.xml: {source: url, "+src+"}\n")
		return tree(t, dir)
	}
	cache := t.TempDir()
	inst := &resolve.Instance{Name: "x", Map: "m", Mods: []resolve.Mod{{ID: 1}}}
	run := func(tr *site.Tree) error {
		_, err := Collect(t.Context(), tr, inst, Options{CacheDir: cache})
		return err
	}
	pinned := write("url: " + srv.URL + "/t.xml, sha256: " + good)
	require.NoError(t, run(pinned))
	require.NoError(t, run(pinned))
	assert.Equal(t, 1, hits, "a pinned file is fetched once")
	require.ErrorContains(t, run(write("url: "+srv.URL+"/t.xml, sha256: "+strings.Repeat("0", 64))), "pinned", "a wrong hash must be refused")
	unpinned := write("url: " + srv.URL + "/u.xml")
	require.NoError(t, run(unpinned))
	before := hits
	require.NoError(t, run(unpinned))
	assert.Equal(t, before, hits, "an unpinned file is kept by URL too")
	require.ErrorContains(t, run(write("url: "+srv.URL+"/missing")), "404", "an HTTP error must be reported")
	// the Get hook replaces the HTTP client
	_, err := Collect(t.Context(), write("url: http://example.invalid/x"), inst, Options{CacheDir: t.TempDir(), Get: func(context.Context, string) ([]byte, error) { return nil, errors.New("no network") }})
	assert.ErrorContains(t, err, "no network")
}

func TestPostMergeHooksReceiveTheStaging(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(t.TempDir(), "mark")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nhooks:\n  post_merge: [hooks/h.sh]\n")
	script := filepath.Join(dir, "integrations", "mods", "1", "hooks", "h.sh")
	put(t, script, "#!/bin/sh\necho \"$DZO_STAGING $DZO_MAP $DZO_INSTANCE\"; echo ran > "+mark+"\n")
	require.NoError(t, os.Chmod(script, 0o755)) //nolint:gosec // test fixture
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "m", Mods: []resolve.Mod{{ID: 1}}, Paths: resolve.Paths{Live: "/live"}}
	var logged []string
	cs, err := Collect(t.Context(), tr, inst, Options{Log: func(f string, a ...any) { logged = append(logged, f) }})
	require.NoError(t, err)
	require.Len(t, cs, 2) // cs[0] is the instance messages.xml
	require.NotNil(t, cs[1].AfterMerge)
	require.NoError(t, cs[1].AfterMerge("/stage"))
	b, _ := os.ReadFile(mark) //nolint:gosec // test fixture
	assert.Contains(t, string(b), "ran", "the hook did not run")
	assert.Len(t, logged, 1, "the hook's output must be logged")
	put(t, script, "#!/bin/sh\nexit 2\n")
	assert.Error(t, cs[1].AfterMerge("/stage"), "a failing hook must fail the merge")
}

func TestInstanceEditorFilesGoToTheEditorFilesFolder(t *testing.T) {
	dir := t.TempDir()
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m"}
	cs, err := Collect(t.Context(), tr, inst, Options{CacheDir: t.TempDir()})
	require.NoError(t, err)
	require.Len(t, cs, 1, "no EditorFiles folder adds nothing but the messages.xml")
	require.Equal(t, "instance messages.xml", cs[0].Name)
	put(t, filepath.Join(dir, "instances", "x", "EditorFiles", "a.dze"), "A")
	put(t, filepath.Join(dir, "instances", "x", "EditorFiles", "sub", "b.dze"), "B")
	cs, err = Collect(t.Context(), tr, inst, Options{CacheDir: t.TempDir()})
	require.NoError(t, err)
	require.Len(t, cs, 2)
	require.Equal(t, "EditorFiles", cs[1].Folder)
	require.Len(t, cs[1].Files, 2)
	require.Equal(t, "a.dze", cs[1].Files[0].Name)
	require.Equal(t, "sub/b.dze", cs[1].Files[1].Name)
}

func collectMessages(t testing.TB, dir string, msgs []site.Message) ([]byte, error) {
	t.Helper()
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m", Messages: msgs}
	cs, err := Collect(t.Context(), tr, inst, Options{CacheDir: t.TempDir()})
	if err != nil {
		return nil, err
	}
	require.NotEmpty(t, cs, "the instance messages.xml is always the first contribution")
	require.Equal(t, "instance messages.xml", cs[0].Name, "the instance messages.xml is always the first contribution")
	require.Len(t, cs[0].Files, 1)
	require.Equal(t, "messages.xml", cs[0].Files[0].Name)
	return cs[0].Files[0].Data, nil
}

func TestCollectMessagesXMLIsAlwaysEmpty(t *testing.T) {
	custom := "Hello"
	for _, inst := range []*resolve.Instance{
		{Name: "x", Map: "empty.m"},
		{Name: "x", Map: "empty.m", Welcome: &custom, Messages: []site.Message{{Text: "Homepage", Repeat: 30}}},
	} {
		data, err := collectMessages(t, t.TempDir(), inst.Messages)
		require.NoError(t, err)
		assert.Equal(t, site.EmptyMessagesXML, string(data), "dzo sends the messages itself, the file stays empty")
	}
}

func TestCollectMessagesFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "instances", "x", "messages.xml"), "<messages/>")
	_, err := collectMessages(t, dir, nil)
	require.Error(t, err, "a messages.xml of the instance is refused with a pointer to the config")
	assert.Contains(t, err.Error(), "messages.xml")
	assert.Contains(t, err.Error(), "welcome:")
}

func TestCollectModMessagesAreDropped(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  messages.xml: {source: local, path: files/m.xml}\n")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "files", "m.xml"), "<messages><message><shutdown>1</shutdown><deadline>60</deadline><text>x</text></message></messages>")
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m", Mods: []resolve.Mod{{ID: 1}}}
	_, err := Collect(t.Context(), tr, inst, Options{CacheDir: t.TempDir()})
	require.NoError(t, err, "a mod's messages.xml is accepted (and later replaced by the empty one)")
}

func TestSyncMapStreamMirrorsTheInstanceFolder(t *testing.T) {
	site, profiles := t.TempDir(), t.TempDir()
	inst := filepath.Join(site, "instances", "x")
	dst := filepath.Join(profiles, "dzo", "mapstream", "DZE")

	w, r, err := SyncMapStream(inst, profiles)
	require.NoError(t, err)
	require.Zero(t, w+r, "no MapStreamDZE folder: nothing to do")
	put(t, filepath.Join(dst, "keep.dze"), "K") // untouched without a source folder
	_, _, err = SyncMapStream(inst, profiles)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dst, "keep.dze"))

	put(t, filepath.Join(inst, "MapStreamDZE", "a.dze"), "A")
	put(t, filepath.Join(inst, "MapStreamDZE", "sub", "b.dze"), "B")
	put(t, filepath.Join(dst, "old", "gone.dze"), "G")
	w, r, err = SyncMapStream(inst, profiles)
	require.NoError(t, err)
	require.Equal(t, 2, w)
	require.Equal(t, 2, r, "keep.dze and old/gone.dze are not in the site")
	require.NoFileExists(t, filepath.Join(dst, "keep.dze"))
	require.NoDirExists(t, filepath.Join(dst, "old"))
	got, err := os.ReadFile(filepath.Join(dst, "sub", "b.dze"))
	require.NoError(t, err)
	require.Equal(t, "B", string(got))

	w, r, err = SyncMapStream(inst, profiles)
	require.NoError(t, err)
	require.Zero(t, w+r, "a second sync changes nothing")

	require.NoError(t, os.Remove(filepath.Join(inst, "MapStreamDZE", "sub", "b.dze")))
	put(t, filepath.Join(inst, "MapStreamDZE", "a.dze"), "A2")
	w, r, err = SyncMapStream(inst, profiles)
	require.NoError(t, err)
	require.Equal(t, 1, w)
	require.Equal(t, 1, r)
	require.NoFileExists(t, filepath.Join(dst, "sub", "b.dze"))
	require.NoDirExists(t, filepath.Join(dst, "sub"))
}

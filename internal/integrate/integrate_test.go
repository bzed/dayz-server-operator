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
)

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, dir string) *site.Tree {
	t.Helper()
	put(t, filepath.Join(dir, "instances", "x", "instance.yaml"), "name: x\nproduct: dayz-stable\nmap: empty.m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 2302, rcon: 2306, query: 27016}\nnetwork: host\n")
	tr, err := site.LoadTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func names(cs []string) string { return strings.Join(cs, ",") }

func TestCollectOrderAndSources(t *testing.T) {
	dir := t.TempDir()
	modDir := filepath.Join(t.TempDir(), "mod")
	put(t, filepath.Join(modDir, "info", "events.xml"), "<events/>")
	put(t, filepath.Join(dir, "instances", "x", "messages.xml"), "<messages/>")
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
	cs, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir(), Log: func(f string, a ...any) { logged = append(logged, f) }})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.Name)
	}
	if want := "instance messages.xml,mod 2 (One),overlay loadout"; names(got) != want {
		t.Fatalf("contributions = %q, want %q (a local mod and a mod without integration add nothing; the alias finds the integration)", names(got), want)
	}
	mod := cs[1]
	if mod.Folder != "mod_2" {
		t.Errorf("folder = %s", mod.Folder)
	}
	var files []string
	for _, f := range mod.Files {
		files = append(files, f.Name)
	}
	if names(files) != "cfggameplay.json,events.xml,types.xml" {
		t.Errorf("files = %v: the variant for another map is left out, the others sorted", files)
	}
	for _, f := range mod.Files {
		if f.Name == "types.xml" && !strings.HasPrefix(string(f.Data), "<?xml") {
			t.Errorf("normalize: [xml-decl] was not applied to types.xml: %q", f.Data)
		}
		if f.Name == "cfggameplay.json" && strings.HasPrefix(string(f.Data), "<?xml") {
			t.Error("a json file must not be normalised")
		}
	}
	ov := cs[2]
	if ov.Folder != "custom_loadout" || len(ov.Overlay.SpawnGearPresets) != 1 || len(ov.Files) != 2 {
		t.Errorf("overlay = %+v", ov)
	}
}

func TestInstanceIntegrationOverridesTheSharedOne(t *testing.T) {
	dir := t.TempDir()
	for _, where := range []string{"integrations/mods/1", "instances/x/integrations/1"} {
		put(t, filepath.Join(dir, where, "integration.yaml"), "mod: 1\nname: "+filepath.Base(filepath.Dir(where))+"\nfiles:\n  types.xml: {source: local, path: t.xml}\n")
		put(t, filepath.Join(dir, where, "t.xml"), "<types name=\""+where+"\"/>")
	}
	tr := tree(t, dir)
	cs, err := Collect(context.Background(), tr, &resolve.Instance{Name: "x", Map: "m", Mods: []resolve.Mod{{ID: 1}}}, Options{})
	if err != nil || len(cs) != 2 { // the instance messages.xml comes first
		t.Fatalf("err=%v cs=%d", err, len(cs))
	}
	if !strings.Contains(string(cs[1].Files[0].Data), "instances/x/integrations/1") {
		t.Errorf("the instance's own integration must win: %s", cs[1].Files[0].Data)
	}
}

func TestSourcesAreContainedAndMustExist(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	put(t, outside, "secret")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  types.xml: {source: local, path: ../../../../secret}\n")
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "m", Mods: []resolve.Mod{{ID: 1}}}
	if _, err := Collect(context.Background(), tr, inst, Options{}); err == nil {
		t.Error("a local path that leaves the integration folder must be refused")
	}
	// a link out is no way around it
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  types.xml: {source: local, path: link.xml}\n")
	if err := os.Symlink(outside, filepath.Join(dir, "integrations", "mods", "1", "link.xml")); err != nil {
		t.Fatal(err)
	}
	tr = tree(t, dir)
	if _, err := Collect(context.Background(), tr, inst, Options{}); err == nil {
		t.Error("a link out of the folder must be refused")
	}
	// a mod source needs the mod
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  events.xml: {source: mod, path: info/events.xml}\n")
	tr = tree(t, dir)
	if _, err := Collect(context.Background(), tr, inst, Options{}); err == nil || !strings.Contains(err.Error(), "not downloaded") {
		t.Errorf("err = %v", err)
	}
	if _, err := Collect(context.Background(), tr, &resolve.Instance{Name: "x", Map: "m", Overlays: []string{"nope"}}, Options{}); err == nil {
		t.Error("an overlay that does not exist must be an error")
	}
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
		_, err := Collect(context.Background(), tr, inst, Options{CacheDir: cache})
		return err
	}
	pinned := write("url: " + srv.URL + "/t.xml, sha256: " + good)
	if err := run(pinned); err != nil {
		t.Fatal(err)
	}
	if err := run(pinned); err != nil || hits != 1 {
		t.Errorf("a pinned file is fetched once: hits=%d err=%v", hits, err)
	}
	if err := run(write("url: " + srv.URL + "/t.xml, sha256: " + strings.Repeat("0", 64))); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Errorf("a wrong hash must be refused: %v", err)
	}
	unpinned := write("url: " + srv.URL + "/u.xml")
	if err := run(unpinned); err != nil {
		t.Fatal(err)
	}
	before := hits
	if err := run(unpinned); err != nil || hits != before {
		t.Errorf("an unpinned file is kept by URL too: hits %d -> %d", before, hits)
	}
	if err := run(write("url: " + srv.URL + "/missing")); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("an HTTP error must be reported: %v", err)
	}
	// the Get hook replaces the HTTP client
	_, err := Collect(context.Background(), write("url: http://example.invalid/x"), inst, Options{CacheDir: t.TempDir(), Get: func(context.Context, string) ([]byte, error) { return nil, errors.New("no network") }})
	if err == nil || !strings.Contains(err.Error(), "no network") {
		t.Errorf("err = %v", err)
	}
}

func TestPostMergeHooksReceiveTheStaging(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(t.TempDir(), "mark")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nhooks:\n  post_merge: [hooks/h.sh]\n")
	script := filepath.Join(dir, "integrations", "mods", "1", "hooks", "h.sh")
	put(t, script, "#!/bin/sh\necho \"$DZO_STAGING $DZO_MAP $DZO_INSTANCE\"; echo ran > "+mark+"\n")
	if err := os.Chmod(script, 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "m", Mods: []resolve.Mod{{ID: 1}}, Paths: resolve.Paths{Live: "/live"}}
	var logged []string
	cs, err := Collect(context.Background(), tr, inst, Options{Log: func(f string, a ...any) { logged = append(logged, f) }})
	if err != nil || len(cs) != 2 || cs[1].AfterMerge == nil { // cs[0] is the instance messages.xml
		t.Fatalf("err=%v cs=%v", err, cs)
	}
	if err := cs[1].AfterMerge("/stage"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(mark); !strings.Contains(string(b), "ran") { //nolint:gosec // test fixture
		t.Error("the hook did not run")
	}
	if len(logged) != 1 {
		t.Errorf("the hook's output must be logged: %v", logged)
	}
	put(t, script, "#!/bin/sh\nexit 2\n")
	if err := cs[1].AfterMerge("/stage"); err == nil {
		t.Error("a failing hook must fail the merge")
	}
}

func TestInstanceEditorFilesGoToTheEditorFilesFolder(t *testing.T) {
	dir := t.TempDir()
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m"}
	cs, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()})
	if err != nil || len(cs) != 1 || cs[0].Name != "instance messages.xml" {
		t.Fatalf("no EditorFiles folder adds nothing but the messages.xml: %v %v", cs, err)
	}
	put(t, filepath.Join(dir, "instances", "x", "EditorFiles", "a.dze"), "A")
	put(t, filepath.Join(dir, "instances", "x", "EditorFiles", "sub", "b.dze"), "B")
	cs, err = Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[1].Folder != "EditorFiles" || len(cs[1].Files) != 2 || cs[1].Files[0].Name != "a.dze" || cs[1].Files[1].Name != "sub/b.dze" {
		t.Fatalf("contributions = %+v", cs)
	}
}

func collectMessages(t *testing.T, dir string, msgs []site.Message) ([]byte, error) {
	t.Helper()
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m", Messages: msgs}
	cs, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()})
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 || cs[0].Name != "instance messages.xml" || len(cs[0].Files) != 1 || cs[0].Files[0].Name != "messages.xml" {
		t.Fatalf("the instance messages.xml is always the first contribution: %+v", cs)
	}
	return cs[0].Files[0].Data, nil
}

func TestCollectDefaultWelcome(t *testing.T) {
	data, err := collectMessages(t, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<text>Welcome to #name!</text>") || !strings.Contains(string(data), "<onConnect>1</onConnect>") {
		t.Errorf("default welcome: %s", data)
	}
}

func TestCollectWelcomeOverrideAndOff(t *testing.T) {
	tr := tree(t, t.TempDir())
	collect := func(inst *resolve.Instance) string {
		cs, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return string(cs[0].Files[0].Data)
	}
	custom := "Hello on #name, rules: example.org"
	got := collect(&resolve.Instance{Name: "x", Map: "empty.m", Welcome: &custom, Messages: []site.Message{{Text: "Homepage", Repeat: 30}}})
	if !strings.Contains(got, "Hello on #name, rules: example.org") || strings.Contains(got, "Welcome to #name!") {
		t.Errorf("override replaces the default: %s", got)
	}
	if strings.Index(got, "Hello on #name") > strings.Index(got, "Homepage") {
		t.Errorf("the welcome comes first: %s", got)
	}
	off := ""
	got = collect(&resolve.Instance{Name: "x", Map: "empty.m", Welcome: &off})
	if strings.Contains(got, "<message>") {
		t.Errorf(`welcome: "" and no messages disables everything: %s`, got)
	}
}

func TestCollectMessagesFromConfig(t *testing.T) {
	data, err := collectMessages(t, t.TempDir(), []site.Message{{Text: "Welcome to #name", OnConnect: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<text>Welcome to #name</text>") {
		t.Errorf("%s", data)
	}
}

func TestCollectMessagesFileIsKeptAndChecked(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "instances", "x", "messages.xml"), "<messages><message><repeat>5</repeat><text>hi</text></message></messages>")
	data, err := collectMessages(t, dir, nil)
	if err != nil || !strings.Contains(string(data), "<text>hi</text>") || strings.Contains(string(data), "Welcome") {
		t.Fatalf("hand-written file is used as is, without the default welcome: %v %s", err, data)
	}
	for name, body := range map[string]string{
		"deadline": "<messages><message><deadline>600</deadline><text>x</text></message></messages>",
		"shutdown": "<messages><message><shutdown>1</shutdown><text>x</text></message></messages>",
		"tmin":     "<messages><message><repeat>1</repeat><text>#tmin</text></message></messages>",
	} {
		d := t.TempDir()
		put(t, filepath.Join(d, "instances", "x", "messages.xml"), body)
		if _, err := collectMessages(t, d, nil); err == nil {
			t.Errorf("%s in messages.xml must fail", name)
		}
	}
}

func TestCollectMessagesFileAndConfigAreAmbiguous(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "instances", "x", "messages.xml"), "<messages/>")
	_, err := collectMessages(t, dir, []site.Message{{Text: "x", Repeat: 1}})
	if err == nil || !strings.Contains(err.Error(), "messages.xml") || !strings.Contains(err.Error(), "messages:") {
		t.Fatalf("both set must be an error naming both: %v", err)
	}
}

func TestCollectModMessagesWithCountdownRejected(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  messages.xml: {source: local, path: files/m.xml}\n")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "files", "m.xml"), "<messages><message><shutdown>1</shutdown><deadline>60</deadline><text>x</text></message></messages>")
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m", Mods: []resolve.Mod{{ID: 1}}}
	if _, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "mod 1") {
		t.Fatalf("a mod's countdown must fail the collect: %v", err)
	}
}

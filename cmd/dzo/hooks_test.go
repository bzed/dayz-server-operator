// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/site"
)

func siteHooks() site.HooksConfig { return site.HooksConfig{} }

func hookSite(t *testing.T, hooksYAML string) (cfg, dir string) {
	t.Helper()
	dir = t.TempDir()
	cfg = filepath.Join(dir, "config.yaml")
	writeFile(t, cfg, "paths:\n  data: "+dir+"\n")
	writeFile(t, filepath.Join(dir, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
`+hooksYAML)
	return cfg, dir
}

func writeHook(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, "site", "instances", "x", "hooks", name)
	writeFile(t, p, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(p, 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func TestHookScriptsKnowsEveryPoint(t *testing.T) {
	for _, p := range []string{hookPreStart, hookPostStop, hookPreUpdate, hookPostUpdate, hookPostDownload, hookPostMerge, hookPostRender, "post_backup"} {
		if _, err := hookScripts(siteHooks(), p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := hookScripts(siteHooks(), "nope"); err == nil {
		t.Error("an unknown hook point must be an error")
	}
}

func TestInstanceHookRunsWithTheContract(t *testing.T) {
	cfg, dir := hookSite(t, "hooks:\n  pre_start: [hooks/a.sh]\n  post_stop: [hooks/fail.sh]\n")
	marker := filepath.Join(dir, "marker")
	writeHook(t, dir, "a.sh", `echo "$DZO_INSTANCE $DZO_HOOK_POINT $DZO_MAP $DZO_LIVE_MISSION" > `+marker+"\ncat > "+marker+".stdin")
	writeHook(t, dir, "fail.sh", "echo broken >&2\nexit 3")

	if _, err := runCmd(t, "instance", "hook", "x", "pre_start", "--config", cfg); err != nil {
		t.Fatalf("pre_start: %v", err)
	}
	b, _ := os.ReadFile(marker) //nolint:gosec // test fixture
	want := "x pre_start empty.m " + filepath.Join(dir, "instances", "x", "mpmissions", "empty.m")
	if got := strings.TrimSpace(string(b)); got != want {
		t.Errorf("hook saw %q, want %q", got, want)
	}
	if in, _ := os.ReadFile(marker + ".stdin"); !strings.Contains(string(in), `"point":"pre_start"`) { //nolint:gosec // test fixture
		t.Errorf("the JSON context is missing on stdin: %s", in)
	}

	// post_stop cannot abort anything: a failing script is reported, not an error.
	out, err := runCmd(t, "instance", "hook", "x", "post_stop", "--config", cfg)
	if err != nil || !strings.Contains(out, "fail.sh") {
		t.Errorf("post_stop failure: err=%v out=%q", err, out)
	}
	if _, err := runCmd(t, "instance", "hook", "x", "bogus", "--config", cfg); err == nil {
		t.Error("an unknown point must fail")
	}
}

func TestPreStartHookFailureAborts(t *testing.T) {
	cfg, dir := hookSite(t, "hooks:\n  pre_start: [hooks/ok.sh, hooks/fail.sh, hooks/never.sh]\n")
	writeHook(t, dir, "ok.sh", "exit 0")
	writeHook(t, dir, "fail.sh", "exit 1")
	writeHook(t, dir, "never.sh", "touch "+filepath.Join(dir, "never"))
	if _, err := runCmd(t, "instance", "hook", "x", "pre_start", "--config", cfg); err == nil {
		t.Fatal("a failing pre_start hook must fail the start")
	}
	if _, err := os.Stat(filepath.Join(dir, "never")); err == nil {
		t.Error("the scripts after the failing one must not run")
	}
}

// TestRenderMergesIntegrationsAndRunsHooks drives the whole render: a mod integration (CE folder,
// post_merge hook), an overlay, the instance's post_merge and post_render hooks.
func TestRenderMergesIntegrationsAndRunsHooks(t *testing.T) {
	data := t.TempDir()
	cfg, root, repo := renderSetupIn(t, data)
	writeFile(t, filepath.Join(repo, "empty.m", "cfgeconomycore.xml"), `<economycore><ce folder="db"><file name="types.xml" type="types"/></ce></economycore>`)
	writeFile(t, filepath.Join(repo, "empty.m", "db", "types.xml"), `<types/>`)
	writeFile(t, filepath.Join(repo, "empty.m", "cfggameplay.json"), `{"WorldsData":{"objectSpawnersArr":[]}}`)
	cmdGit := func(a ...string) {
		t.Helper()
		if out, err := runGit(repo, a...); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	cmdGit("add", "-A")
	cmdGit("-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qm", "ce")

	site := filepath.Join(data, "site")
	mark := filepath.Join(data, "marks")
	writeFile(t, filepath.Join(site, "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: empty.m
mission_source: {git: `+repo+`, ref: main, path: empty.m}
fallback_mission: dayzOffline.fb
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
mods:
  - {id: 111}
overlays: [loadout]
hooks:
  post_merge: [hooks/post_merge.sh]
  post_render: [hooks/post_render.sh]
`)
	writeFile(t, filepath.Join(site, "integrations", "mods", "111", "integration.yaml"), `mod: 111
name: TestMod
files:
  types.xml: {source: local, path: files/types.xml}
hooks:
  post_merge: [hooks/mod_merge.sh]
`)
	writeFile(t, filepath.Join(site, "integrations", "mods", "111", "files", "types.xml"), `<types><type name="Foo"/></types>`)
	writeFile(t, filepath.Join(site, "overlays", "loadout", "overlay.yaml"), "spawn_gear_presets: [\"*.json\"]\n")
	writeFile(t, filepath.Join(site, "overlays", "loadout", "gear.json"), `{}`)
	script := func(rel, body string) {
		p := filepath.Join(site, rel)
		writeFile(t, p, "#!/bin/sh\n"+body+"\n")
		if err := os.Chmod(p, 0o755); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
	}
	script("integrations/mods/111/hooks/mod_merge.sh", `test -f "$DZO_STAGING/mod_111/types.xml" && echo mod >> `+mark)
	script("instances/x/hooks/post_merge.sh", `test -f "$DZO_STAGING/custom_loadout/gear.json" && echo instance >> `+mark)
	script("instances/x/hooks/post_render.sh", `test -f "$DZO_LIVE_MISSION/mod_111/types.xml" && echo render >> `+mark)

	out, err := runCmd(t, "instance", "render", "x", "--config", cfg)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	live := filepath.Join(root, "mpmissions", "empty.m")
	if b, _ := os.ReadFile(filepath.Join(live, "mod_111", "types.xml")); !strings.Contains(string(b), "Foo") {
		t.Errorf("the mod's types.xml is not in its CE folder: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "cfgeconomycore.xml")); !strings.Contains(string(b), `folder="mod_111"`) {
		t.Errorf("CE folder not registered: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "cfggameplay.json")); !strings.Contains(string(b), "custom_loadout/gear.json") {
		t.Errorf("overlay reference missing: %s", b)
	}
	m, _ := os.ReadFile(mark) //nolint:gosec // test fixture
	if got := strings.Fields(string(m)); strings.Join(got, ",") != "mod,instance,render" {
		t.Errorf("hooks ran as %v, want mod (after its merge), instance (after all merges), render (after the apply)", got)
	}

	// A failing post_merge hook fails the render and leaves the live mission as it was.
	script("instances/x/hooks/post_merge.sh", "exit 1")
	writeFile(t, filepath.Join(site, "integrations", "mods", "111", "files", "types.xml"), `<types><type name="Changed"/></types>`)
	if _, err := runCmd(t, "instance", "render", "x", "--config", cfg); err == nil {
		t.Fatal("a failing post_merge hook must fail the render")
	}
	if b, _ := os.ReadFile(filepath.Join(live, "mod_111", "types.xml")); strings.Contains(string(b), "Changed") {
		t.Error("the live mission must not change when the render fails")
	}
}

func runGit(dir string, args ...string) ([]byte, error) {
	c := exec.Command("git", args...) //nolint:gosec // fixed test calls
	c.Dir = dir
	return c.CombinedOutput()
}

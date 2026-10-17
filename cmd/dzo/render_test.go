// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// renderSetup builds a data dir with a one-instance site repo whose mission
// source is a local git repo, plus an installed product build that ships a
// fallback mission. It returns the config path and the instance root.
func renderSetup(t *testing.T) (cfg, instRoot, missionRepo string) {
	t.Helper()
	return renderSetupIn(t, t.TempDir())
}

// renderSetupIn is renderSetup with the data directory chosen by the caller.
func renderSetupIn(t *testing.T, data string) (cfg, instRoot, missionRepo string) {
	t.Helper()
	missionRepo = filepath.Join(t.TempDir(), "mission")
	writeFile(t, filepath.Join(missionRepo, "empty.m", "init.c"), "init")
	writeFile(t, filepath.Join(missionRepo, "empty.m", "db", "types.xml"), "types")
	commitAll(t, missionRepo)

	writeFile(t, filepath.Join(data, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: empty.m
mission_source: {git: `+missionRepo+`, ref: main, path: empty.m}
fallback_mission: dayzOffline.fb
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
`)
	writeFile(t, filepath.Join(data, "site", "instances", "x", "serverDZ.cfg"), "hostname = \"x\";\ntemplate = \"stale\";\n")
	build := filepath.Join(data, "cache", "products", "dayz-stable", "1")
	writeFile(t, filepath.Join(build, "mpmissions", "dayzOffline.fb", "cfgweather.xml"), "weather")
	writeFile(t, filepath.Join(build, "keys", "dayz.bikey"), "bikey")
	if err := os.Symlink("1", filepath.Join(data, "cache", "products", "dayz-stable", "current")); err != nil {
		t.Fatal(err)
	}
	cfg = filepath.Join(data, "config.yaml")
	writeFile(t, cfg, "paths:\n  data: "+data+"\n")
	return cfg, filepath.Join(data, "instances", "x"), missionRepo
}

func TestInstanceRender(t *testing.T) {
	cfg, root, repo := renderSetup(t)
	live := filepath.Join(root, "mpmissions", "empty.m")

	out, err := runCmd(t, "instance", "render", "x", "--config", cfg, "--dry-run")
	if err != nil {
		t.Fatalf("render --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ init.c (new)") || strings.Contains(out, "written:") {
		t.Errorf("dry-run output = %s", out)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatal("dry-run must not create the live mission")
	}

	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	if !strings.Contains(out, "written: 4") { // init.c, types.xml, cfgweather.xml from the fallback, the generated messages.xml
		t.Errorf("output = %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "db", "messages.xml")); strings.Contains(string(b), "<message>") {
		t.Errorf("a render writes an empty messages.xml, dzo sends the messages itself: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "cfgweather.xml")); string(b) != "weather" {
		t.Errorf("fallback file = %q", b)
	}
	// the files the start needs besides the mission
	if b, _ := os.ReadFile(filepath.Join(root, "runtime", "serverDZ.cfg")); !strings.Contains(string(b), `template = "empty.m";`) || !strings.Contains(string(b), "steamQueryPort = 27016;") {
		t.Errorf("runtime/serverDZ.cfg = %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "runtime", "keys", "dayz.bikey")); string(b) != "bikey" {
		t.Errorf("runtime/keys/dayz.bikey = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "profiles", "battleye", "beserver_x64.cfg")); !strings.Contains(string(b), "RConPort 2306") {
		t.Errorf("BattlEye seed = %s", b)
	}

	// The mission repo moves on; a plain render must not fetch, --update-pristine must.
	writeFile(t, filepath.Join(repo, "empty.m", "init.c"), "init2")
	commitAll2(t, repo)
	if out, err = runCmd(t, "instance", "render", "x", "--config", cfg); err != nil || !strings.Contains(out, "no changes") {
		t.Fatalf("plain render = %v\n%s", err, out)
	}
	if out, err = runCmd(t, "instance", "render", "x", "--config", cfg, "--update-pristine"); err != nil || !strings.Contains(out, "~ init.c (update)") {
		t.Fatalf("--update-pristine render = %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "init.c")); string(b) != "init2" {
		t.Errorf("init.c = %q", b)
	}
}

func commitAll2(t *testing.T, dir string) {
	t.Helper()
	c := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qam", "y")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func TestInstanceRenderFailures(t *testing.T) {
	cfg, _, repo := renderSetup(t)
	if _, err := runCmd(t, "instance", "render", "nope", "--config", cfg); err == nil {
		t.Error("unknown instance must fail")
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "instance", "render", "x", "--config", cfg); err == nil {
		t.Error("unfetchable pristine must fail")
	}
}

func TestInstanceRenderNeedsServerCfgBeforeTouchingTheMission(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	data := filepath.Dir(cfg)
	if err := os.Remove(filepath.Join(data, "site", "instances", "x", "serverDZ.cfg")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--dry-run"}, {}} {
		out, err := runCmd(t, append([]string{"instance", "render", "x", "--config", cfg}, args...)...)
		if err == nil || !strings.Contains(out+err.Error(), "serverDZ.cfg is missing") {
			t.Errorf("render %v without serverDZ.cfg must fail: %v\n%s", args, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "mpmissions")); !os.IsNotExist(err) {
		t.Error("the live mission must not be touched when the start would fail anyway")
	}
	writeFile(t, filepath.Join(data, "site", "instances", "x", "serverDZ.cfg"), "not a config")
	if _, err := runCmd(t, "instance", "render", "x", "--config", cfg); err == nil {
		t.Error("a serverDZ.cfg that does not parse must fail")
	}
}

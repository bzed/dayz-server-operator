// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
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

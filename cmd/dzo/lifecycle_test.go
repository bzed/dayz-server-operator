// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/resolve"
)

// isolatedHome keeps dzo's user directories (quadlets, units) out of the real home.
func isolatedHome(t *testing.T) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
}

func installBuild(t *testing.T, data, id string) {
	t.Helper()
	b := filepath.Join(data, "cache", "products", "dayz-stable", id)
	writeFile(t, filepath.Join(b, "keys", "dayz.bikey"), "bikey")
	writeFile(t, filepath.Join(b, "mpmissions", "dayzOffline.fb", "cfgweather.xml"), "weather")
}

func TestInstanceCreateAndApply(t *testing.T) {
	isolatedHome(t)
	data := t.TempDir()
	cfg, root, _ := renderSetupIn(t, data)
	out, err := runCmd(t, "instance", "create", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "created x") {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "mpmissions", "empty.m", "init.c")); err != nil {
		t.Errorf("the live mission was not created: %v", err)
	}
	if _, err := runCmd(t, "instance", "create", "x", "--config", cfg); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("a second create must fail: %v", err)
	}
	if _, err := runCmd(t, "instance", "create", "nope", "--config", cfg); err == nil {
		t.Error("an unknown instance must fail")
	}
	// apply: a dry run says what it would write and calls no systemctl
	out, err = runCmd(t, "instance", "apply", "x", "--config", cfg, "--dry-run")
	if err != nil || !strings.Contains(out, "would write") {
		t.Fatalf("apply --dry-run: %v\n%s", err, out)
	}
	if _, err := runCmd(t, "instance", "apply", "nope", "--config", cfg, "--dry-run"); err == nil {
		t.Error("apply of an unknown instance must fail")
	}
}

func TestInstanceCreateNeedsTheBuild(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	if err := os.RemoveAll(filepath.Join(data, "cache")); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "instance", "create", "x", "--config", cfg); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("create without the server build must say so: %v", err)
	}
}

func TestInstanceUpgradeDryRunAndChecks(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	installBuild(t, data, "2")
	out, err := runCmd(t, "instance", "upgrade", "x", "--build", "2", "--dry-run", "--config", cfg)
	if err != nil || !strings.Contains(out, "build 1 -> 2") || !strings.Contains(out, "+ init.c") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(data, "instances", "x", "mpmissions")); err == nil {
		t.Error("a dry run must not create the live mission")
	}
	if _, err := runCmd(t, "instance", "upgrade", "x", "--build", "1", "--dry-run", "--config", cfg); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("the build it runs: %v", err)
	}
	if _, err := runCmd(t, "instance", "upgrade", "x", "--build", "9", "--dry-run", "--config", cfg); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("an unknown build: %v", err)
	}
	if _, err := runCmd(t, "instance", "upgrade", "x", "--config", cfg); err == nil {
		t.Error("--build is required")
	}
}

func testCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	return c, &out
}

func TestUpgradeFuncPinsTheBuildAndRevertsOnFailure(t *testing.T) {
	isolatedHome(t)
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	installBuild(t, data, "2")
	_, inst, err := loadInstance(cfg, "x")
	if err != nil {
		t.Fatal(err)
	}
	conf := loadCfg(t, cfg)

	good := instance.Lifecycle{Command: writeFakeSystemctlCmd(t, 0), UserMode: true}
	c, out := testCmd()
	if err := upgradeFunc(c, conf, cfg, inst, "2", false, good)(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(resolve.BuildPinFile(inst.Paths.Runtime)); strings.TrimSpace(string(b)) != "2" { //nolint:gosec // test fixture
		t.Errorf("pin = %q, want 2", b)
	}
	if !strings.Contains(out.String(), "now runs build 2") || !strings.Contains(out.String(), "no snapshot") {
		t.Errorf("output = %s", out)
	}

	// A unit that cannot be written (the site no longer loads) puts the pin back.
	if err := resolve.PinBuild(inst.Paths.Runtime, "1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(data, "site", "instances", "x", "instance.yaml"), "name: x\nbogus: [\n")
	c, _ = testCmd()
	if err := upgradeFunc(c, conf, cfg, inst, "2", false, good)(context.Background()); err == nil {
		t.Fatal("a site that does not load must fail the upgrade")
	}
	if b, _ := os.ReadFile(resolve.BuildPinFile(inst.Paths.Runtime)); strings.TrimSpace(string(b)) != "1" { //nolint:gosec // test fixture
		t.Errorf("the pin must be back at 1, it is %q", b)
	}

	// No wipe without a snapshot (the test directory is not btrfs).
	c, _ = testCmd()
	if err := upgradeFunc(c, conf, cfg, inst, "2", true, good)(context.Background()); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("wipe without snapshot: %v", err)
	}
}

func TestInstanceModsAndModRemove(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	path := filepath.Join(data, "site", "instances", "x", "instance.yaml")
	b, _ := os.ReadFile(path) //nolint:gosec // test fixture
	writeFile(t, path, string(b)+"mods:\n  - {id: 11}\n  - {id: 22, server: true}\n  - {id: 33}\n")

	out, err := runCmd(t, "instance", "mods", "list", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "@11") || !strings.Contains(out, "-servermod") || !strings.Contains(out, "not installed") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if _, err := runCmd(t, "instance", "mods", "move", "x", "33", "--before", "11", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	out, _ = runCmd(t, "instance", "mods", "list", "x", "--config", cfg)
	if i33, i11 := strings.Index(out, "@33"), strings.Index(out, "@11"); i33 < 0 || i33 > i11 {
		t.Errorf("33 must come before 11:\n%s", out)
	}
	if _, err := runCmd(t, "instance", "mods", "move", "x", "11", "--after", "22", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "instance", "mods", "move", "x", "11", "--config", cfg); err == nil {
		t.Error("--before or --after is required")
	}
	if _, err := runCmd(t, "mod", "remove", "22", "--instance", "x", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "id: 22") { //nolint:gosec // test fixture
		t.Errorf("mod 22 is still listed:\n%s", b)
	}
	if _, err := runCmd(t, "mod", "remove", "22", "--instance", "x", "--config", cfg); err == nil {
		t.Error("removing a mod that is not listed must fail")
	}
}

func TestMissionUpdateInitRollbackReinit(t *testing.T) {
	cfg, root, repo := renderSetup(t)
	live := filepath.Join(root, "mpmissions", "empty.m")

	if _, err := runCmd(t, "mission", "rollback", "x", "--config", cfg); err == nil {
		t.Error("a rollback without history must fail")
	}
	if out, err := runCmd(t, "mission", "init", "x", "--config", cfg); err != nil || !strings.Contains(out, "written:") {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if _, err := runCmd(t, "mission", "init", "x", "--config", cfg); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("a second init must fail: %v", err)
	}

	// the mission repository moves on
	writeFile(t, filepath.Join(repo, "empty.m", "init.c"), "init2")
	commitAll2(t, repo)
	out, err := runCmd(t, "mission", "update", "x", "--dry-run", "--config", cfg)
	if err != nil || !strings.Contains(out, "~ init.c (update)") {
		t.Fatalf("update --dry-run: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "servermpmissions", "empty.m", "init.c")); string(b) != "init" { //nolint:gosec // test fixture
		t.Errorf("--dry-run must leave the instance's pristine mission alone: %q", b)
	}
	if out, err = runCmd(t, "mission", "update", "x", "--config", cfg); err != nil || !strings.Contains(out, "~ init.c (update)") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "init.c")); string(b) != "init" { //nolint:gosec // test fixture
		t.Errorf("update must not touch the live mission: %q", b)
	}
	if _, err := runCmd(t, "instance", "render", "x", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "init.c")); string(b) != "init2" { //nolint:gosec // test fixture
		t.Fatalf("render brings the new file: %q", b)
	}

	out, err = runCmd(t, "mission", "rollback", "x", "--list", "--config", cfg)
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("--list: %v %q", err, out)
	}
	if out, err = runCmd(t, "mission", "rollback", "x", "--config", cfg); err != nil || !strings.Contains(out, "restored init.c") {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "init.c")); string(b) != "init" { //nolint:gosec // test fixture
		t.Errorf("rollback restored %q", b)
	}

	// reinit needs a snapshot, and the test directory is not btrfs
	if _, err := runCmd(t, "mission", "reinit", "x", "--yes", "--config", cfg); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("reinit without btrfs: %v", err)
	}
}

func TestWipe(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	storage := filepath.Join(root, "storage", "empty.m")
	writeFile(t, filepath.Join(storage, "storage_1", "data.bin"), "world")
	if _, err := runCmd(t, "wipe", "x", "--yes", "--config", cfg); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("a wipe without btrfs and without --no-snapshot must fail: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage, "storage_1")); err != nil {
		t.Fatal("nothing may be deleted when the wipe was refused")
	}
	if _, err := runCmdWithStdin(t, "no\n", "wipe", "x", "--no-snapshot", "--config", cfg); err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Errorf("an answer other than yes aborts: %v", err)
	}
	out, err := runCmdWithStdin(t, "yes\n", "wipe", "x", "--no-snapshot", "--config", cfg)
	if err != nil || !strings.Contains(out, "wiped") {
		t.Fatalf("wipe: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(storage, "storage_1")); !os.IsNotExist(err) {
		t.Error("the world must be gone")
	}
	if n, err := removeStorage(filepath.Join(root, "nowhere")); err != nil || n != 0 {
		t.Errorf("a missing storage directory is nothing to wipe: %d %v", n, err)
	}
}

func TestShellAndExecRunPodman(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	podman := filepath.Join(dir, "podman")
	writeFile(t, podman, "#!/bin/sh\necho \"$@\" > "+args+"\n")
	if err := os.Chmod(podman, 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	// the instance needs its build to have a quadlet
	if _, err := runCmd(t, "shell", "--podman", podman, "--config", cfg, "x"); err != nil {
		t.Fatalf("shell: %v", err)
	}
	b, _ := os.ReadFile(args) //nolint:gosec // test fixture
	got := string(b)
	for _, want := range []string{"run --rm -i --network none --workdir /dayz", "/dayz:O", "/dayz/keys:ro", "localhost/dzo-runtime:latest /bin/bash"} {
		if !strings.Contains(got, want) {
			t.Errorf("podman args %q miss %q", got, want)
		}
	}
	if _, err := runCmd(t, "shell", "--podman", podman, "--network", "host", "--config", cfg, "x", "ls", "/dayz"); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(args); !strings.Contains(string(b), "--network host") || !strings.HasSuffix(strings.TrimSpace(string(b)), "ls /dayz") { //nolint:gosec // test fixture
		t.Errorf("shell with a command: %s", b)
	}
	if _, err := runCmd(t, "exec", "--podman", podman, "x", "ls", "-l"); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(args); strings.TrimSpace(string(b)) != "exec dzo-x ls -l" { //nolint:gosec // test fixture
		t.Errorf("exec args = %q", b)
	}
	if _, err := runCmd(t, "shell", "--podman", podman, "--config", cfg, "nope"); err == nil {
		t.Error("an unknown instance must fail")
	}
}

func TestPodmanVolume(t *testing.T) {
	for v, want := range map[quadlet.Volume]string{
		{Source: "/a", Destination: "/b"}:                 "/a:/b",
		{Source: "/a", Destination: "/b", ReadOnly: true}: "/a:/b:ro",
		{Source: "/a", Destination: "/b", Overlay: true}:  "/a:/b:O",
	} {
		if got := podmanVolume(v); got != want {
			t.Errorf("podmanVolume(%+v) = %q, want %q", v, got, want)
		}
	}
}

func loadCfg(t *testing.T, path string) *config.Config {
	t.Helper()
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func addInstanceY(t *testing.T, data, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(data, "site", "instances", "y", "instance.yaml"), `name: y
product: dayz-stable
map: empty.m
mission_source: {git: `+repo+`, ref: main, path: empty.m}
fallback_mission: dayzOffline.fb
ports: {game: 2402, rcon: 2406, query: 27116}
network: host
`)
	writeFile(t, filepath.Join(data, "site", "instances", "y", "serverDZ.cfg"), "hostname = \"y\";\n")
}

func TestInstanceCloneAndRemove(t *testing.T) {
	isolatedHome(t)
	data := t.TempDir()
	cfg, root, repo := renderSetupIn(t, data)
	addInstanceY(t, data, repo)
	if _, err := runCmd(t, "instance", "create", "x", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "storage", "empty.m", "data", "marker"), "world")
	writeFile(t, filepath.Join(root, "runtime", "build"), "1")
	writeFile(t, filepath.Join(root, "profiles", "old.log"), "log")

	for _, bad := range [][]string{{"x", "x"}, {"x", "nope"}} {
		if _, err := runCmd(t, append([]string{"instance", "clone"}, append(bad, "--config", cfg)...)...); err == nil {
			t.Errorf("clone %v must fail", bad)
		}
	}
	out, err := runCmd(t, "instance", "clone", "x", "y", "--config", cfg)
	if err != nil || !strings.Contains(out, "cloned x to y") {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	y := filepath.Join(data, "instances", "y")
	if b, err := os.ReadFile(filepath.Join(y, "storage", "empty.m", "data", "marker")); err != nil || string(b) != "world" {
		t.Errorf("the world was not copied: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(y, "runtime", "build")); string(b) != "1" {
		t.Errorf("the build pin was lost: %q", b)
	}
	if _, err := os.Stat(filepath.Join(y, "profiles", "old.log")); err == nil {
		t.Error("the profile logs were copied")
	}
	if _, err := runCmd(t, "instance", "clone", "x", "y", "--config", cfg); err == nil {
		t.Error("clone onto an existing instance must fail")
	}

	// remove: units are deleted, the data directory is gone, the site stays
	q, u := userDirs()
	writeFile(t, filepath.Join(q, "dzo-y.container"), "x")
	writeFile(t, filepath.Join(u, "dzo-restart-y.timer"), "x")
	writeFile(t, filepath.Join(data, "snapshots", "y", "s1", "f"), "x")
	out, err = runCmd(t, "instance", "remove", "y", "--yes", "--config", cfg)
	if err != nil || !strings.Contains(out, "and 2 unit file") || !strings.Contains(out, "removed 1 snapshot(s)") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	for _, p := range []string{y, filepath.Join(q, "dzo-y.container"), filepath.Join(data, "snapshots", "y")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	if _, err := runCmd(t, "instance", "remove", "y", "--yes", "--config", cfg); err == nil {
		t.Error("removing an instance twice must fail")
	}
	if _, err := runCmd(t, "instance", "remove", "../x", "--yes", "--config", cfg); err == nil {
		t.Error("a path as the name must fail")
	}
	if _, err := runCmd(t, "instance", "remove", "x", "--keep-snapshots", "--delete-logs", "--config", cfg); err == nil {
		t.Error("without --yes and without input remove must abort")
	}
}

func TestMissionStatusAndSiteStatusCommit(t *testing.T) {
	isolatedHome(t)
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	site := filepath.Join(data, "site")
	out, err := runCmd(t, "mission", "status", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "pristine: not fetched yet") || !strings.Contains(out, "live mission: not created") {
		t.Fatalf("status before create: %v\n%s", err, out)
	}
	if _, err := runCmd(t, "instance", "create", "x", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	out, err = runCmd(t, "mission", "status", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "managed file(s)") || !strings.Contains(out, "next render:") {
		t.Fatalf("status: %v\n%s", err, out)
	}

	if _, err := runCmd(t, "site", "status", "--config", cfg); err == nil {
		t.Error("without a checkout, site status must say so")
	}
	commitAll(t, site)
	writeFile(t, filepath.Join(site, "instances", "x", "note"), "n")
	out, err = runCmd(t, "site", "status", "--config", cfg)
	if err != nil || !strings.Contains(out, "note") {
		t.Fatalf("site status: %v\n%s", err, out)
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	out, err = runCmd(t, "site", "commit", "-m", "note", "--config", cfg)
	if err != nil || !strings.Contains(out, "committed") {
		t.Fatalf("site commit: %v\n%s", err, out)
	}
	out, err = runCmd(t, "site", "commit", "-m", "again", "--config", cfg)
	if err != nil || !strings.Contains(out, "nothing to commit") {
		t.Fatalf("second commit: %v\n%s", err, out)
	}
}

func TestLegacyConvertConfig(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "files", "serverDZ.cfg"), "template=\"dayzOffline.chernarusplus\";\npassword = \"s\";\n")
	writeFile(t, filepath.Join(repo, "config", "containers", "server.json"), `{"ports": ["3302:3302/udp","3303:3303/udp","37016:37016/udp"]}`)
	writeFile(t, filepath.Join(repo, "server", "bin", "dz"), "parameters=\"-cpuCount=2 -config=x -port=1\"\n")
	commitAll(t, repo)
	out := filepath.Join(t.TempDir(), "site")
	if _, err := runCmd(t, "legacy", "convert-config", "--repo", repo, "--out", out); err == nil {
		t.Error("--ref is required")
	}
	o, err := runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "main", "--out", out, "--dry-run")
	if err != nil || !strings.Contains(o, "would write instances/main/instance.yaml") {
		t.Fatalf("dry run: %v\n%s", err, o)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a dry run wrote files")
	}
	o, err = runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "main", "--out", out, "--port-offset", "100")
	if err != nil || !strings.Contains(o, "the site validates") {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	b, _ := os.ReadFile(filepath.Join(out, "instances", "main", "instance.yaml"))
	if !strings.Contains(string(b), "game: 3402") {
		t.Errorf("instance.yaml:\n%s", b)
	}
	o, err = runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "main", "--out", out, "--port-offset", "100")
	if err != nil || !strings.Contains(o, "0 of ") {
		t.Fatalf("a second run changes nothing: %v\n%s", err, o)
	}
	if _, err := runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "nope", "--out", out); err == nil {
		t.Error("an unknown branch must fail")
	}
}

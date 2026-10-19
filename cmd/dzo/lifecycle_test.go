// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/resolve"
)

// isolatedHome keeps dzo's user directories (quadlets, units) out of the real home.
func isolatedHome(t testing.TB) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
}

func installBuild(t testing.TB, data, id string) {
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
	require.NoError(t, err, "create: %s", out)
	require.Contains(t, out, "created x", "create")
	_, err = os.Stat(filepath.Join(root, "mpmissions", "empty.m", "init.c"))
	require.NoError(t, err, "the live mission was not created")
	_, err = runCmd(t, "instance", "create", "x", "--config", cfg)
	require.ErrorContains(t, err, "exists", "a second create must fail")
	_, err = runCmd(t, "instance", "create", "nope", "--config", cfg)
	require.Error(t, err, "an unknown instance must fail")
	// apply: a dry run says what it would write and calls no systemctl
	out, err = runCmd(t, "instance", "apply", "x", "--config", cfg, "--dry-run")
	require.NoError(t, err, "apply --dry-run: %s", out)
	require.Contains(t, out, "would write", "apply --dry-run")
	_, err = runCmd(t, "instance", "apply", "nope", "--config", cfg, "--dry-run")
	assert.Error(t, err, "apply of an unknown instance must fail")
}

func TestInstanceCreateNeedsTheBuild(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	require.NoError(t, os.RemoveAll(filepath.Join(data, "cache")))
	_, err := runCmd(t, "instance", "create", "x", "--config", cfg)
	assert.ErrorContains(t, err, "not installed", "create without the server build must say so")
}

func TestInstanceUpgradeDryRunAndChecks(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	installBuild(t, data, "2")
	out, err := runCmd(t, "instance", "upgrade", "x", "--build", "2", "--dry-run", "--config", cfg)
	require.NoError(t, err, "dry run: %s", out)
	require.Contains(t, out, "build 1 -> 2", "dry run")
	require.Contains(t, out, "+ init.c", "dry run")
	_, err = os.Stat(filepath.Join(data, "instances", "x", "mpmissions"))
	require.Error(t, err, "a dry run must not create the live mission")
	_, err = runCmd(t, "instance", "upgrade", "x", "--build", "1", "--dry-run", "--config", cfg)
	require.ErrorContains(t, err, "already", "the build it runs")
	_, err = runCmd(t, "instance", "upgrade", "x", "--build", "9", "--dry-run", "--config", cfg)
	require.ErrorContains(t, err, "not installed", "an unknown build")
	_, err = runCmd(t, "instance", "upgrade", "x", "--config", cfg)
	assert.Error(t, err, "--build is required")
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
	require.NoError(t, err)
	conf := loadCfg(t, cfg)

	good := instance.Lifecycle{Command: writeFakeSystemctlCmd(t, 0), UserMode: true}
	c, out := testCmd()
	require.NoError(t, upgradeFunc(c, conf, cfg, inst, "2", false, good)(t.Context()), "upgrade: %s", out)
	b, _ := os.ReadFile(resolve.BuildPinFile(inst.Paths.Runtime)) //nolint:gosec // test fixture
	assert.Equal(t, "2", strings.TrimSpace(string(b)), "pin")
	assert.Contains(t, out.String(), "now runs build 2")
	assert.Contains(t, out.String(), "no snapshot")

	// A unit that cannot be written (the site no longer loads) puts the pin back.
	require.NoError(t, resolve.PinBuild(inst.Paths.Runtime, "1"))
	writeFile(t, filepath.Join(data, "site", "instances", "x", "instance.yaml"), "name: x\nbogus: [\n")
	c, _ = testCmd()
	require.Error(t, upgradeFunc(c, conf, cfg, inst, "2", false, good)(t.Context()), "a site that does not load must fail the upgrade")
	b, _ = os.ReadFile(resolve.BuildPinFile(inst.Paths.Runtime)) //nolint:gosec // test fixture
	assert.Equal(t, "1", strings.TrimSpace(string(b)), "the pin must be back at 1")

	// No wipe without a snapshot (the test directory is not btrfs).
	c, _ = testCmd()
	err = upgradeFunc(c, conf, cfg, inst, "2", true, good)(t.Context())
	assert.ErrorContains(t, err, "snapshot", "wipe without snapshot")
}

func TestInstanceModsAndModRemove(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	path := filepath.Join(data, "site", "instances", "x", "instance.yaml")
	b, _ := os.ReadFile(path) //nolint:gosec // test fixture
	writeFile(t, path, string(b)+"mods:\n  - {id: 11}\n  - {id: 22, server: true}\n  - {id: 33}\n")

	out, err := runCmd(t, "instance", "mods", "list", "x", "--config", cfg)
	require.NoError(t, err, "list: %s", out)
	require.Contains(t, out, "@11", "list")
	require.Contains(t, out, "-servermod", "list")
	require.Contains(t, out, "not installed", "list")
	_, err = runCmd(t, "instance", "mods", "move", "x", "33", "--before", "11", "--config", cfg)
	require.NoError(t, err)
	out, _ = runCmd(t, "instance", "mods", "list", "x", "--config", cfg)
	i33, i11 := strings.Index(out, "@33"), strings.Index(out, "@11")
	assert.GreaterOrEqual(t, i33, 0, "33 must be listed:\n%s", out)
	assert.LessOrEqual(t, i33, i11, "33 must come before 11:\n%s", out)
	_, err = runCmd(t, "instance", "mods", "move", "x", "11", "--after", "22", "--config", cfg)
	require.NoError(t, err)
	_, err = runCmd(t, "instance", "mods", "move", "x", "11", "--config", cfg)
	require.Error(t, err, "--before or --after is required")
	_, err = runCmd(t, "mod", "remove", "22", "--instance", "x", "--config", cfg)
	require.NoError(t, err)
	b, _ = os.ReadFile(path) //nolint:gosec // test fixture
	assert.NotContains(t, string(b), "id: 22", "mod 22 is still listed")
	_, err = runCmd(t, "mod", "remove", "22", "--instance", "x", "--config", cfg)
	assert.Error(t, err, "removing a mod that is not listed must fail")
}

func TestMissionUpdateInitRollbackReinit(t *testing.T) {
	cfg, root, repo := renderSetup(t)
	live := filepath.Join(root, "mpmissions", "empty.m")

	_, err := runCmd(t, "mission", "rollback", "x", "--config", cfg)
	require.Error(t, err, "a rollback without history must fail")
	out, err := runCmd(t, "mission", "init", "x", "--config", cfg)
	require.NoError(t, err, "init: %s", out)
	require.Contains(t, out, "written:", "init")
	_, err = runCmd(t, "mission", "init", "x", "--config", cfg)
	require.ErrorContains(t, err, "exists", "a second init must fail")

	// the mission repository moves on
	writeFile(t, filepath.Join(repo, "empty.m", "init.c"), "init2")
	commitAll2(t, repo)
	out, err = runCmd(t, "mission", "update", "x", "--dry-run", "--config", cfg)
	require.NoError(t, err, "update --dry-run: %s", out)
	require.Contains(t, out, "~ init.c (update)", "update --dry-run")
	b, _ := os.ReadFile(filepath.Join(root, "servermpmissions", "empty.m", "init.c")) //nolint:gosec // test fixture
	assert.Equal(t, "init", string(b), "--dry-run must leave the instance's pristine mission alone")
	out, err = runCmd(t, "mission", "update", "x", "--config", cfg)
	require.NoError(t, err, "update: %s", out)
	require.Contains(t, out, "~ init.c (update)", "update")
	b, _ = os.ReadFile(filepath.Join(live, "init.c")) //nolint:gosec // test fixture
	assert.Equal(t, "init", string(b), "update must not touch the live mission")
	_, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err)
	b, _ = os.ReadFile(filepath.Join(live, "init.c")) //nolint:gosec // test fixture
	require.Equal(t, "init2", string(b), "render brings the new file")

	out, err = runCmd(t, "mission", "rollback", "x", "--list", "--config", cfg)
	require.NoError(t, err, "--list: %q", out)
	require.NotEmpty(t, strings.TrimSpace(out), "--list")
	out, err = runCmd(t, "mission", "rollback", "x", "--config", cfg)
	require.NoError(t, err, "rollback: %s", out)
	require.Contains(t, out, "restored init.c", "rollback")
	b, _ = os.ReadFile(filepath.Join(live, "init.c")) //nolint:gosec // test fixture
	assert.Equal(t, "init", string(b), "rollback restored")

	// reinit needs a snapshot, and the test directory is not btrfs
	_, err = runCmd(t, "mission", "reinit", "x", "--yes", "--config", cfg)
	assert.ErrorContains(t, err, "snapshot", "reinit without btrfs")
}

func TestWipe(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	storage := filepath.Join(root, "storage", "empty.m")
	writeFile(t, filepath.Join(storage, "storage_1", "data.bin"), "world")
	_, err := runCmd(t, "wipe", "x", "--yes", "--config", cfg)
	require.ErrorContains(t, err, "snapshot", "a wipe without btrfs and without --no-snapshot must fail")
	_, err = os.Stat(filepath.Join(storage, "storage_1"))
	require.NoError(t, err, "nothing may be deleted when the wipe was refused")
	_, err = runCmdWithStdin(t, "no\n", "wipe", "x", "--no-snapshot", "--config", cfg)
	require.ErrorContains(t, err, "aborted", "an answer other than yes aborts")
	out, err := runCmdWithStdin(t, "yes\n", "wipe", "x", "--no-snapshot", "--config", cfg)
	require.NoError(t, err, "wipe: %s", out)
	require.Contains(t, out, "wiped", "wipe")
	_, err = os.Stat(filepath.Join(storage, "storage_1"))
	require.ErrorIs(t, err, os.ErrNotExist, "the world must be gone")
	n, err := removeStorage(filepath.Join(root, "nowhere"))
	require.NoError(t, err, "a missing storage directory is nothing to wipe")
	assert.Zero(t, n, "a missing storage directory is nothing to wipe")
}

func TestShellAndExecRunPodman(t *testing.T) {
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	podman := filepath.Join(dir, "podman")
	writeFile(t, podman, "#!/bin/sh\necho \"$@\" > "+args+"\n")
	require.NoError(t, os.Chmod(podman, 0o755)) //nolint:gosec // test fixture
	// the instance needs its build to have a quadlet
	_, err := runCmd(t, "shell", "--podman", podman, "--config", cfg, "x")
	require.NoError(t, err, "shell")
	b, _ := os.ReadFile(args) //nolint:gosec // test fixture
	got := string(b)
	for _, want := range []string{"run --rm -i --network none --workdir /dayz", "/dayz:O", "/dayz/keys:ro", "localhost/dzo-runtime:latest /bin/bash"} {
		assert.Contains(t, got, want, "podman args")
	}
	_, err = runCmd(t, "shell", "--podman", podman, "--network", "host", "--config", cfg, "x", "ls", "/dayz")
	require.NoError(t, err)
	b, _ = os.ReadFile(args) //nolint:gosec // test fixture
	assert.Contains(t, string(b), "--network host", "shell with a command")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(string(b)), "ls /dayz"), "shell with a command: %s", b)
	_, err = runCmd(t, "exec", "--podman", podman, "x", "ls", "-l")
	require.NoError(t, err)
	b, _ = os.ReadFile(args) //nolint:gosec // test fixture
	assert.Equal(t, "exec dzo-x ls -l", strings.TrimSpace(string(b)), "exec args")
	_, err = runCmd(t, "shell", "--podman", podman, "--config", cfg, "nope")
	assert.Error(t, err, "an unknown instance must fail")
}

func TestPodmanVolume(t *testing.T) {
	for v, want := range map[quadlet.Volume]string{
		{Source: "/a", Destination: "/b"}:                 "/a:/b",
		{Source: "/a", Destination: "/b", ReadOnly: true}: "/a:/b:ro",
		{Source: "/a", Destination: "/b", Overlay: true}:  "/a:/b:O",
	} {
		assert.Equal(t, want, podmanVolume(v), "podmanVolume(%+v)", v)
	}
}

func loadCfg(t testing.TB, path string) *config.Config {
	t.Helper()
	c, err := config.Load(path)
	require.NoError(t, err)
	return c
}

func addInstanceY(t testing.TB, data, repo string) {
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
	_, err := runCmd(t, "instance", "create", "x", "--config", cfg)
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, "storage", "empty.m", "data", "marker"), "world")
	writeFile(t, filepath.Join(root, "runtime", "build"), "1")
	writeFile(t, filepath.Join(root, "profiles", "old.log"), "log")

	for _, bad := range [][]string{{"x", "x"}, {"x", "nope"}} {
		_, err := runCmd(t, append([]string{"instance", "clone"}, append(bad, "--config", cfg)...)...)
		require.Error(t, err, "clone %v must fail", bad)
	}
	out, err := runCmd(t, "instance", "clone", "x", "y", "--config", cfg)
	require.NoError(t, err, "clone: %s", out)
	require.Contains(t, out, "cloned x to y", "clone")
	y := filepath.Join(data, "instances", "y")
	b, err := os.ReadFile(filepath.Join(y, "storage", "empty.m", "data", "marker"))
	require.NoError(t, err, "the world was not copied")
	assert.Equal(t, "world", string(b), "the world was not copied")
	b, _ = os.ReadFile(filepath.Join(y, "runtime", "build"))
	assert.Equal(t, "1", string(b), "the build pin was lost")
	_, err = os.Stat(filepath.Join(y, "profiles", "old.log"))
	require.Error(t, err, "the profile logs were copied")
	_, err = runCmd(t, "instance", "clone", "x", "y", "--config", cfg)
	require.Error(t, err, "clone onto an existing instance must fail")

	// remove: units are deleted, the data directory is gone, the site stays
	q, u := userDirs()
	writeFile(t, filepath.Join(q, "dzo-y.container"), "x")
	writeFile(t, filepath.Join(u, "dzo-restart-y.timer"), "x")
	writeFile(t, filepath.Join(data, "snapshots", "y", "s1", "f"), "x")
	out, err = runCmd(t, "instance", "remove", "y", "--yes", "--config", cfg)
	require.NoError(t, err, "remove: %s", out)
	require.Contains(t, out, "and 2 unit file", "remove")
	require.Contains(t, out, "removed 1 snapshot(s)", "remove")
	for _, p := range []string{y, filepath.Join(q, "dzo-y.container"), filepath.Join(data, "snapshots", "y")} {
		_, err := os.Lstat(p)
		require.Error(t, err, "%s is still there", p)
	}
	_, err = runCmd(t, "instance", "remove", "y", "--yes", "--config", cfg)
	require.Error(t, err, "removing an instance twice must fail")
	_, err = runCmd(t, "instance", "remove", "../x", "--yes", "--config", cfg)
	require.Error(t, err, "a path as the name must fail")
	_, err = runCmd(t, "instance", "remove", "x", "--keep-snapshots", "--delete-logs", "--config", cfg)
	assert.Error(t, err, "without --yes and without input remove must abort")
}

func TestMissionStatusAndSiteStatusCommit(t *testing.T) {
	isolatedHome(t)
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	site := filepath.Join(data, "site")
	out, err := runCmd(t, "mission", "status", "x", "--config", cfg)
	require.NoError(t, err, "status before create: %s", out)
	require.Contains(t, out, "pristine: not fetched yet", "status before create")
	require.Contains(t, out, "live mission: not created", "status before create")
	_, err = runCmd(t, "instance", "create", "x", "--config", cfg)
	require.NoError(t, err)
	out, err = runCmd(t, "mission", "status", "x", "--config", cfg)
	require.NoError(t, err, "status: %s", out)
	require.Contains(t, out, "managed file(s)", "status")
	require.Contains(t, out, "next render:", "status")

	_, err = runCmd(t, "site", "status", "--config", cfg)
	require.Error(t, err, "without a checkout, site status must say so")
	commitAll(t, site)
	writeFile(t, filepath.Join(site, "instances", "x", "note"), "n")
	out, err = runCmd(t, "site", "status", "--config", cfg)
	require.NoError(t, err, "site status: %s", out)
	require.Contains(t, out, "note", "site status")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	out, err = runCmd(t, "site", "commit", "-m", "note", "--config", cfg)
	require.NoError(t, err, "site commit: %s", out)
	require.Contains(t, out, "committed", "site commit")
	out, err = runCmd(t, "site", "commit", "-m", "again", "--config", cfg)
	require.NoError(t, err, "second commit: %s", out)
	require.Contains(t, out, "nothing to commit", "second commit")
}

func TestLegacyConvertConfig(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "files", "serverDZ.cfg"), "template=\"dayzOffline.chernarusplus\";\npassword = \"s\";\n")
	writeFile(t, filepath.Join(repo, "config", "containers", "server.json"), `{"ports": ["3302:3302/udp","3303:3303/udp","37016:37016/udp"]}`)
	writeFile(t, filepath.Join(repo, "server", "bin", "dz"), "parameters=\"-cpuCount=2 -config=x -port=1\"\n")
	commitAll(t, repo)
	out := filepath.Join(t.TempDir(), "site")
	_, err := runCmd(t, "legacy", "convert-config", "--repo", repo, "--out", out)
	require.Error(t, err, "--ref is required")
	o, err := runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "main", "--out", out, "--dry-run")
	require.NoError(t, err, "dry run: %s", o)
	require.Contains(t, o, "would write instances/main/instance.yaml", "dry run")
	_, err = os.Stat(out)
	require.Error(t, err, "a dry run wrote files")
	o, err = runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "main", "--out", out, "--port-offset", "100")
	require.NoError(t, err, "convert: %s", o)
	require.Contains(t, o, "the site validates", "convert")
	b, _ := os.ReadFile(filepath.Join(out, "instances", "main", "instance.yaml"))
	assert.Contains(t, string(b), "game: 3402", "instance.yaml")
	o, err = runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "main", "--out", out, "--port-offset", "100")
	require.NoError(t, err, "a second run changes nothing: %s", o)
	require.Contains(t, o, "0 of ", "a second run changes nothing")
	_, err = runCmd(t, "legacy", "convert-config", "--repo", repo, "--ref", "nope", "--out", out)
	assert.Error(t, err, "an unknown branch must fail")
}

func TestBackupPruneSkipsInstancesThatCannotBeBackedUp(t *testing.T) {
	data := t.TempDir()
	cfg, root, _ := renderSetupIn(t, data)
	require.NoError(t, os.MkdirAll(root, 0o750)) // a plain directory: not a btrfs subvolume
	out, err := runCmd(t, "backup", "prune", "--config", cfg)
	require.NoError(t, err, "prune of all instances must skip x: %s", out)
	require.Contains(t, out, "skipping x", "prune of all instances must skip x")
	_, err = runCmd(t, "backup", "prune", "x", "--config", cfg)
	assert.Error(t, err, "prune of x by name must say why it cannot")
}

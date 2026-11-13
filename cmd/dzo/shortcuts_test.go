// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartAndStopShortForms(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	for _, c := range []string{"start", "stop"} {
		_, err := runCmd(t, c, "deerisle", "--command", script)
		assert.NoError(t, err, "dzo %s", c)
	}
}

func TestLogsAsksTheJournalOfTheUnit(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := filepath.Join(dir, "journalctl")
	writeFile(t, script, "#!/bin/sh\necho \"$@\" > "+args+"\n")
	require.NoError(t, os.Chmod(script, 0o755)) //nolint:gosec // test fixture
	_, err := runCmd(t, "logs", "deerisle", "-f", "-n", "5", "--journalctl", script)
	require.NoError(t, err)
	b, _ := os.ReadFile(args) //nolint:gosec // test fixture
	assert.Equal(t, "--user -u dzo-deerisle.service -n 5 --no-pager -f", strings.TrimSpace(string(b)), "journalctl args")
}

func TestSiteValidateAndPull(t *testing.T) {
	cfg := exporterSite(t, "")
	out, err := runCmd(t, "site", "validate", "--config", cfg)
	require.NoError(t, err, "validate: %s", out)
	require.Contains(t, out, "x", "validate")
	require.Contains(t, out, "ok", "validate")
	// A bad instance fails the validation.
	dir := filepath.Dir(cfg)
	writeFile(t, filepath.Join(dir, "site", "instances", "bad", "instance.yaml"), "name: bad\nproduct: nonexistent\nmap: m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 2402, rcon: 2406, query: 27116}\nnetwork: host\n")
	_, err = runCmd(t, "site", "validate", "--config", cfg)
	require.Error(t, err, "an instance with an unknown product must fail the validation")

	// pull: clone a local remote, then fast-forward it.
	remote := filepath.Join(t.TempDir(), "remote")
	git := func(dir string, a ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, a...)...) //nolint:gosec // fixed test git calls
		c.Dir = dir
		o, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", a, o)
	}
	writeFile(t, filepath.Join(remote, "site.yaml"), "{}\n")
	git(remote, "init", "-q", "-b", "main")
	git(remote, "add", "-A")
	git(remote, "commit", "-q", "-m", "one")
	pullDir := t.TempDir()
	pcfg := filepath.Join(pullDir, "config.yaml")
	writeFile(t, pcfg, "paths:\n  data: "+pullDir+"\nsite:\n  remote: "+remote+"\n  branch: main\n")
	out, err = runCmd(t, "site", "pull", "--config", pcfg)
	require.NoError(t, err, "clone: %s", out)
	require.Contains(t, out, "up to date", "clone")
	writeFile(t, filepath.Join(remote, "more.yaml"), "x: 1\n")
	git(remote, "add", "-A")
	git(remote, "commit", "-q", "-m", "two")
	_, err = runCmd(t, "site", "pull", "--config", pcfg)
	require.NoError(t, err, "pull")
	_, err = os.Stat(filepath.Join(pullDir, "site", "more.yaml"))
	assert.NoError(t, err, "the second commit was not pulled")
}

func TestStopWithCountdownOptions(t *testing.T) {
	cfg := exporterSite(t, "")
	script := writeFakeSystemctlCmd(t, 0) // not active: there is nobody to announce to
	for _, args := range [][]string{
		{"stop", "x", "--graceful", "--config", cfg, "--command", script},
		{"stop", "x", "--minutes", "5", "--lock", "2", "--kick", "1", "--text", "Maintenance", "--config", cfg, "--command", script},
	} {
		out, err := runCmd(t, args...)
		require.NoError(t, err, "dzo %v: %s", args, out)
	}
	// the countdown options need the site; an unknown instance fails instead of stopping plainly
	_, err := runCmd(t, "stop", "nope", "--graceful", "--config", cfg, "--command", script)
	assert.Error(t, err, "an unknown instance must fail")
}

func TestStopCancelNamesTheInstance(t *testing.T) {
	cfg := exporterSite(t, "")
	script := writeFakeSystemctlCmd(t, 0)
	out, err := runCmd(t, "stop", "x", "--cancel", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --cancel: %s", out)
	assert.Contains(t, out, "x: cancelled", "the cancel message names the instance")
}

func TestStopCountdownLogsNameTheInstance(t *testing.T) {
	cfg := exporterSite(t, "")
	script := writeActiveSystemctlCmd(t) // is-active answers "active", but the fixture has no RCon password
	out, err := runCmd(t, "stop", "x", "--graceful", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --graceful: %s", out)
	assert.Contains(t, out, "x: RCon unreachable", "the countdown log names the instance")
}

// writeActiveSystemctlCmd writes a systemctl stub whose is-active answers "active" and that
// succeeds otherwise: the unit is running, so dzo stop counts down before stopping it.
func writeActiveSystemctlCmd(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-systemctl.sh")
	writeFile(t, path, "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = \"is-active\" ] && { echo active; exit 0; }; done\nexit 0\n")
	require.NoError(t, os.Chmod(path, 0o755)) //nolint:gosec // test fixture, needs to run
	return path
}

// allSite is exporterSite with a second instance, y, for the --all commands.
func allSite(t testing.TB) string {
	t.Helper()
	cfg := exporterSite(t, "")
	writeFile(t, filepath.Join(filepath.Dir(cfg), "site", "instances", "y", "instance.yaml"), `name: y
product: dayz-stable
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2402, rcon: 2406, query: 27116}
network: host
`)
	return cfg
}

// writeRecordingSystemctlCmd writes a systemctl stub that appends its arguments to argsFile and succeeds.
func writeRecordingSystemctlCmd(t testing.TB, argsFile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-systemctl.sh")
	writeFile(t, path, "#!/bin/sh\necho \"$@\" >> "+argsFile+"\nexit 0\n")
	require.NoError(t, os.Chmod(path, 0o755)) //nolint:gosec // test fixture, needs to run
	return path
}

// writeActiveRecordingSystemctlCmd is writeRecordingSystemctlCmd whose is-active answers "active":
// the units are running, so dzo stop counts down before stopping them.
func writeActiveRecordingSystemctlCmd(t testing.TB, argsFile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-systemctl.sh")
	writeFile(t, path, "#!/bin/sh\necho \"$@\" >> "+argsFile+"\nfor a in \"$@\"; do [ \"$a\" = \"is-active\" ] && { echo active; exit 0; }; done\nexit 0\n")
	require.NoError(t, os.Chmod(path, 0o755)) //nolint:gosec // test fixture, needs to run
	return path
}

func TestStartAll(t *testing.T) {
	cfg := allSite(t)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeRecordingSystemctlCmd(t, args)
	out, err := runCmd(t, "start", "--all", "--config", cfg, "--command", script)
	require.NoError(t, err, "start --all: %s", out)
	b, err := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user start dzo-x.service", "x was started")
	assert.Contains(t, string(b), "--user start dzo-y.service", "y was started")
}

func TestStartAllNeedsNameOrAll(t *testing.T) {
	_, err := runCmd(t, "start")
	assert.Error(t, err, "start without a name and without --all must fail")
	_, err = runCmd(t, "start", "--all", "x")
	assert.Error(t, err, "start --all together with a name must fail")
}

func TestStartAllContinuesAfterAFailure(t *testing.T) {
	cfg := allSite(t)
	dir := t.TempDir()
	args := filepath.Join(dir, "systemctl-args")
	script := filepath.Join(dir, "systemctl")
	writeFile(t, script, "#!/bin/sh\necho \"$@\" >> "+args+"\nfor a in \"$@\"; do [ \"$a\" = \"dzo-y.service\" ] && exit 1; done\nexit 0\n")
	require.NoError(t, os.Chmod(script, 0o755)) //nolint:gosec // test fixture, needs to run
	_, err := runCmd(t, "start", "--all", "--config", cfg, "--command", script)
	require.Error(t, err, "one failing instance must fail the run")
	assert.Contains(t, err.Error(), "1 instance(s) failed")
	b, rerr := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, rerr, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user start dzo-x.service", "x was started although y failed")
}

func TestStopAll(t *testing.T) {
	cfg := allSite(t)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeRecordingSystemctlCmd(t, args)
	out, err := runCmd(t, "stop", "--all", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all: %s", out)
	b, err := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user stop dzo-x.service", "x was stopped")
	assert.Contains(t, string(b), "--user stop dzo-y.service", "y was stopped")
}

func TestStopAllNeedsNameOrAll(t *testing.T) {
	_, err := runCmd(t, "stop")
	assert.Error(t, err, "stop without a name and without --all must fail")
	_, err = runCmd(t, "stop", "--all", "x")
	assert.Error(t, err, "stop --all together with a name must fail")
}

func TestStopAllGraceful(t *testing.T) {
	cfg := allSite(t)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeActiveRecordingSystemctlCmd(t, args) // both units are active, neither has a RCon password
	out, err := runCmd(t, "stop", "--all", "--graceful", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all --graceful: %s", out)
	assert.Contains(t, out, "x: RCon unreachable", "x was counted down")
	assert.Contains(t, out, "y: RCon unreachable", "y was counted down")
	b, err := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user stop dzo-x.service", "x was stopped")
	assert.Contains(t, string(b), "--user stop dzo-y.service", "y was stopped")
}

func TestStopAllCancel(t *testing.T) {
	cfg := allSite(t)
	script := writeFakeSystemctlCmd(t, 0)
	out, err := runCmd(t, "stop", "--all", "--cancel", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all --cancel: %s", out)
	assert.Contains(t, out, "x: cancelled", "x's countdown was cancelled")
	assert.Contains(t, out, "y: cancelled", "y's countdown was cancelled")
	dir := filepath.Dir(cfg)
	for _, n := range []string{"x", "y"} {
		_, err := os.Stat(filepath.Join(dir, "instances", n, "runtime", "restart-cancel")) //nolint:gosec // test fixture path under t.TempDir()
		assert.NoError(t, err, "the cancel file of %s exists", n)
	}
}

func TestStopAllContinuesAfterAResolveFailure(t *testing.T) {
	cfg := allSite(t)
	writeFile(t, filepath.Join(filepath.Dir(cfg), "site", "instances", "y", "instance.yaml"), `name: y
product: nonexistent
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2402, rcon: 2406, query: 27116}
network: host
`)
	args := filepath.Join(t.TempDir(), "systemctl-args")
	script := writeRecordingSystemctlCmd(t, args)
	_, err := runCmd(t, "stop", "--all", "--graceful", "--config", cfg, "--command", script)
	require.Error(t, err, "one instance failing to resolve must fail the run")
	assert.Contains(t, err.Error(), "1 instance(s) failed")
	b, rerr := os.ReadFile(args) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, rerr, "read the recorded systemctl arguments")
	assert.Contains(t, string(b), "--user stop dzo-x.service", "x was stopped although y does not resolve")
}

func TestStopAllWithNoInstances(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	writeFile(t, cfg, "paths:\n  data: "+dir+"\n")
	script := writeFakeSystemctlCmd(t, 0)
	out, err := runCmd(t, "stop", "--all", "--config", cfg, "--command", script)
	require.NoError(t, err, "stop --all on an empty site: %s", out)
}

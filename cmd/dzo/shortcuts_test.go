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
	assert.Error(t, err, "an instance with an unknown product must fail the validation")

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
		assert.NoError(t, err, "dzo %v: %s", args, out)
	}
	// the countdown options need the site; an unknown instance fails instead of stopping plainly
	_, err := runCmd(t, "stop", "nope", "--graceful", "--config", cfg, "--command", script)
	assert.Error(t, err, "an unknown instance must fail")
}

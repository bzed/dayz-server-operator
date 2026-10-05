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

func TestStartAndStopShortForms(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	for _, c := range []string{"start", "stop"} {
		if _, err := runCmd(t, c, "deerisle", "--command", script); err != nil {
			t.Errorf("dzo %s: %v", c, err)
		}
	}
}

func TestLogsAsksTheJournalOfTheUnit(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := filepath.Join(dir, "journalctl")
	writeFile(t, script, "#!/bin/sh\necho \"$@\" > "+args+"\n")
	if err := os.Chmod(script, 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if _, err := runCmd(t, "logs", "deerisle", "-f", "-n", "5", "--journalctl", script); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(args) //nolint:gosec // test fixture
	if got := strings.TrimSpace(string(b)); got != "--user -u dzo-deerisle.service -n 5 --no-pager -f" {
		t.Errorf("journalctl args = %q", got)
	}
}

func TestSiteValidateAndPull(t *testing.T) {
	cfg := exporterSite(t, "")
	out, err := runCmd(t, "site", "validate", "--config", cfg)
	if err != nil || !strings.Contains(out, "x") || !strings.Contains(out, "ok") {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	// A bad instance fails the validation.
	dir := filepath.Dir(cfg)
	writeFile(t, filepath.Join(dir, "site", "instances", "bad", "instance.yaml"), "name: bad\nproduct: nonexistent\nmap: m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 2402, rcon: 2406, query: 27116}\nnetwork: host\n")
	if _, err := runCmd(t, "site", "validate", "--config", cfg); err == nil {
		t.Error("an instance with an unknown product must fail the validation")
	}

	// pull: clone a local remote, then fast-forward it.
	remote := filepath.Join(t.TempDir(), "remote")
	git := func(dir string, a ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, a...)...) //nolint:gosec // fixed test git calls
		c.Dir = dir
		if o, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, o)
		}
	}
	writeFile(t, filepath.Join(remote, "site.yaml"), "{}\n")
	git(remote, "init", "-q", "-b", "main")
	git(remote, "add", "-A")
	git(remote, "commit", "-q", "-m", "one")
	pullDir := t.TempDir()
	pcfg := filepath.Join(pullDir, "config.yaml")
	writeFile(t, pcfg, "paths:\n  data: "+pullDir+"\nsite:\n  remote: "+remote+"\n  branch: main\n")
	if out, err := runCmd(t, "site", "pull", "--config", pcfg); err != nil || !strings.Contains(out, "up to date") {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(remote, "more.yaml"), "x: 1\n")
	git(remote, "add", "-A")
	git(remote, "commit", "-q", "-m", "two")
	if _, err := runCmd(t, "site", "pull", "--config", pcfg); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pullDir, "site", "more.yaml")); err != nil {
		t.Errorf("the second commit was not pulled: %v", err)
	}
}

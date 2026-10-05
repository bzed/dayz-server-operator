// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeFakeSystemctl writes a shell script standing in for systemctl: it
// dumps its own arguments to argsFile (one per line) and exits with
// exitCode, optionally printing stdout first. Real systemctl behaviour is
// not exercised here (no live systemd user session in this environment);
// this only verifies Lifecycle builds the right command line and handles
// the exit code correctly.
func writeFakeSystemctl(t *testing.T, argsFile string, stdout string, exitCode int) string {
	t.Helper()
	script := "#!/bin/sh\n"
	script += "printf '%s\\n' \"$@\" > " + argsFile + "\n"
	if stdout != "" {
		script += "echo " + stdout + "\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	path := filepath.Join(t.TempDir(), "fake-systemctl.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, fixed 0755 mode
		t.Fatalf("write fake systemctl: %v", err)
	}
	return path
}

func readArgsFile(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path under t.TempDir()
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	return strings.Fields(string(data))
}

func TestLifecycleStart(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script, UserMode: true}

	if err := l.Start(context.Background(), "deerisle"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	args := readArgsFile(t, argsFile)
	want := []string{"--user", "start", "dzo-deerisle.service"}
	if !equalStrings(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestLifecycleStop(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	if err := l.Stop(context.Background(), "deerisle"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	args := readArgsFile(t, argsFile)
	want := []string{"stop", "dzo-deerisle.service"}
	if !equalStrings(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestLifecycleRestart(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	if err := l.Restart(context.Background(), "deerisle"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	args := readArgsFile(t, argsFile)
	want := []string{"restart", "dzo-deerisle.service"}
	if !equalStrings(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestLifecycleFailurePropagates(t *testing.T) {
	script := writeFakeSystemctl(t, filepath.Join(t.TempDir(), "args.txt"), "some error", 1)
	l := Lifecycle{Command: script}

	err := l.Start(context.Background(), "deerisle")
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if !strings.Contains(err.Error(), "some error") {
		t.Errorf("err = %v, want it to include the command's output", err)
	}
}

func TestLifecycleIsActiveTrue(t *testing.T) {
	script := writeFakeSystemctl(t, filepath.Join(t.TempDir(), "args.txt"), "active", 0)
	l := Lifecycle{Command: script}

	active, err := l.IsActive(context.Background(), "deerisle")
	if err != nil {
		t.Fatalf("IsActive: %v", err)
	}
	if !active {
		t.Error("expected IsActive to be true")
	}
}

func TestLifecycleIsActiveFalseOnNonZeroExit(t *testing.T) {
	// systemctl is-active exits non-zero for "inactive"/"failed" etc -
	// that must not surface as a Go error.
	script := writeFakeSystemctl(t, filepath.Join(t.TempDir(), "args.txt"), "inactive", 3)
	l := Lifecycle{Command: script}

	active, err := l.IsActive(context.Background(), "deerisle")
	if err != nil {
		t.Fatalf("IsActive: %v", err)
	}
	if active {
		t.Error("expected IsActive to be false")
	}
}

func TestLifecycleAckFailure(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	if err := l.AckFailure(context.Background(), "deerisle"); err != nil {
		t.Fatalf("AckFailure: %v", err)
	}
	args := readArgsFile(t, argsFile)
	want := []string{"reset-failed", "dzo-deerisle.service"}
	if !equalStrings(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestLifecycleDaemonReload(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	if err := l.DaemonReload(context.Background()); err != nil {
		t.Fatalf("DaemonReload: %v", err)
	}
	args := readArgsFile(t, argsFile)
	want := []string{"daemon-reload"}
	if !equalStrings(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestLifecycleDefaultCommandIsSystemctl(t *testing.T) {
	l := Lifecycle{}
	if l.command() != "systemctl" {
		t.Errorf("command() = %q, want systemctl", l.command())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLifecycleEnableNowAndDisable(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	l := Lifecycle{Command: writeFakeSystemctl(t, argsFile, "", 0)}
	if err := l.EnableNow(context.Background(), "a.timer", "b.timer"); err != nil {
		t.Fatal(err)
	}
	if got := readArgsFile(t, argsFile); !equalStrings(got, []string{"enable", "--now", "a.timer", "b.timer"}) {
		t.Errorf("enable args = %v", got)
	}
	if err := l.Disable(context.Background(), "a.timer"); err != nil {
		t.Fatal(err)
	}
	if got := readArgsFile(t, argsFile); !equalStrings(got, []string{"disable", "--now", "a.timer"}) {
		t.Errorf("disable args = %v", got)
	}
}

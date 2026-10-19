// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFakeSystemctl writes a shell script standing in for systemctl: it
// dumps its own arguments to argsFile (one per line) and exits with
// exitCode, optionally printing stdout first. Real systemctl behaviour is
// not exercised here (no live systemd user session in this environment);
// this only verifies Lifecycle builds the right command line and handles
// the exit code correctly.
func writeFakeSystemctl(t testing.TB, argsFile string, stdout string, exitCode int) string {
	t.Helper()
	script := "#!/bin/sh\n"
	script += "printf '%s\\n' \"$@\" > " + argsFile + "\n"
	if stdout != "" {
		script += "echo " + stdout + "\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	path := filepath.Join(t.TempDir(), "fake-systemctl.sh")
	err := os.WriteFile(path, []byte(script), 0o755)
	require.NoError(t, err, "write fake systemctl")
	return path
}

func readArgsFile(t testing.TB, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read args file")
	return strings.Fields(string(data))
}

func TestLifecycleStart(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script, UserMode: true}

	err := l.Start(t.Context(), "deerisle")
	require.NoError(t, err, "Start")
	args := readArgsFile(t, argsFile)
	want := []string{"--user", "start", "dzo-deerisle.service"}
	assert.True(t, equalStrings(args, want), "args = %v, want %v", args, want)
}

func TestLifecycleStop(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	err := l.Stop(t.Context(), "deerisle")
	require.NoError(t, err, "Stop")
	args := readArgsFile(t, argsFile)
	want := []string{"stop", "dzo-deerisle.service"}
	assert.True(t, equalStrings(args, want), "args = %v, want %v", args, want)
}

func TestLifecycleRestart(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	err := l.Restart(t.Context(), "deerisle")
	require.NoError(t, err, "Restart")
	args := readArgsFile(t, argsFile)
	want := []string{"restart", "dzo-deerisle.service"}
	assert.True(t, equalStrings(args, want), "args = %v, want %v", args, want)
}

func TestLifecycleFailurePropagates(t *testing.T) {
	script := writeFakeSystemctl(t, filepath.Join(t.TempDir(), "args.txt"), "some error", 1)
	l := Lifecycle{Command: script}

	err := l.Start(t.Context(), "deerisle")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some error")
}

func TestLifecycleIsActiveTrue(t *testing.T) {
	script := writeFakeSystemctl(t, filepath.Join(t.TempDir(), "args.txt"), "active", 0)
	l := Lifecycle{Command: script}

	active, err := l.IsActive(t.Context(), "deerisle")
	require.NoError(t, err, "IsActive")
	assert.True(t, active, "expected IsActive to be true")
}

func TestLifecycleIsActiveFalseOnNonZeroExit(t *testing.T) {
	// systemctl is-active exits non-zero for "inactive"/"failed" etc -
	// that must not surface as a Go error.
	script := writeFakeSystemctl(t, filepath.Join(t.TempDir(), "args.txt"), "inactive", 3)
	l := Lifecycle{Command: script}

	active, err := l.IsActive(t.Context(), "deerisle")
	require.NoError(t, err, "IsActive")
	assert.False(t, active, "expected IsActive to be false")
}

func TestLifecycleAckFailure(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	err := l.AckFailure(t.Context(), "deerisle")
	require.NoError(t, err, "AckFailure")
	args := readArgsFile(t, argsFile)
	want := []string{"reset-failed", "dzo-deerisle.service"}
	assert.True(t, equalStrings(args, want), "args = %v, want %v", args, want)
}

func TestLifecycleDaemonReload(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeSystemctl(t, argsFile, "", 0)
	l := Lifecycle{Command: script}

	err := l.DaemonReload(t.Context())
	require.NoError(t, err, "DaemonReload")
	args := readArgsFile(t, argsFile)
	want := []string{"daemon-reload"}
	assert.True(t, equalStrings(args, want), "args = %v, want %v", args, want)
}

func TestLifecycleDefaultCommandIsSystemctl(t *testing.T) {
	l := Lifecycle{}
	assert.Equal(t, "systemctl", l.command())
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
	err := l.EnableNow(t.Context(), "a.timer", "b.timer")
	require.NoError(t, err)
	got := readArgsFile(t, argsFile)
	assert.True(t, equalStrings(got, []string{"enable", "--now", "a.timer", "b.timer"}), "enable args = %v", got)
	err = l.Disable(t.Context(), "a.timer")
	require.NoError(t, err)
	got = readArgsFile(t, argsFile)
	assert.True(t, equalStrings(got, []string{"disable", "--now", "a.timer"}), "disable args = %v", got)
}

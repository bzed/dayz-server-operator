// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFakeSystemctlCmd(t testing.TB, exitCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-systemctl.sh")
	script := "#!/bin/sh\nexit " + strconv.Itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755), "write fake systemctl") //nolint:gosec // test fixture, fixed 0755 mode
	return path
}

func TestInstanceStart(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	_, err := runCmd(t, "instance", "start", "deerisle", "--command", script)
	require.NoError(t, err, "instance start")
}

func TestInstanceStop(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	_, err := runCmd(t, "instance", "stop", "deerisle", "--command", script)
	require.NoError(t, err, "instance stop")
}

func TestInstanceRestartPlain(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	_, err := runCmd(t, "instance", "restart", "deerisle", "--command", script)
	require.NoError(t, err, "instance restart")
}

func TestInstanceStartFailurePropagates(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 1)
	_, err := runCmd(t, "instance", "start", "deerisle", "--command", script)
	require.Error(t, err, "expected an error for a failing systemctl")
}

func TestInstanceRestartGracefulRequiresRconAddr(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	_, err := runCmd(t, "instance", "restart", "deerisle", "--command", script, "--graceful")
	require.Error(t, err, "expected an error for --graceful without --rcon-addr")
}

func TestInstanceRestartGracefulFallsBackWhenRconUnreachable(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	out, err := runCmd(t, "instance", "restart", "deerisle", "--command", script,
		"--graceful", "--rcon-addr", "127.0.0.1:1", "--rcon-password", "x", "--rcon-timeout", "200ms")
	require.NoError(t, err, "instance restart --graceful: %s", out)
	assert.Contains(t, out, "RCon unreachable")
}

func TestInstanceAckFailure(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	gateFile := filepath.Join(t.TempDir(), "gate.json")
	require.NoError(t, os.WriteFile(gateFile, []byte(`{"failed":true,"input_hash":"sha256:abc"}`), 0o600), "write gate file")
	_, err := runCmd(t, "instance", "ack-failure", "deerisle", "--command", script, "--gate-file", gateFile)
	require.NoError(t, err, "instance ack-failure")
	data, err := os.ReadFile(gateFile) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read gate file")
	assert.NotContains(t, string(data), `"failed": true`, "gate file should be cleared")
}

func TestInstanceAckFailureWithoutGateFile(t *testing.T) {
	script := writeFakeSystemctlCmd(t, 0)
	_, err := runCmd(t, "instance", "ack-failure", "deerisle", "--command", script)
	require.NoError(t, err, "instance ack-failure")
}

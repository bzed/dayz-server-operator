// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFakeSteamcmdCmd(t testing.TB, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-steamcmd.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755), "write fake steamcmd") //nolint:gosec // test fixture, fixed 0755 mode
	return path
}

func runCmdWithStdin(t testing.TB, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestSteamLoginSuccess(t *testing.T) {
	script := writeFakeSteamcmdCmd(t, `
printf 'password: '
read pass
echo ""
echo "Waiting for user info...OK"
`)
	statusFile := filepath.Join(t.TempDir(), "status.json")

	out, err := runCmdWithStdin(t, "s3cr3t\n", "steam", "login",
		"--user", "bob", "--command", script, "--status-file", statusFile)
	require.NoError(t, err, "steam login: %s", out)
	assert.Contains(t, out, "login OK")

	statusOut, err := runCmdWithStdin(t, "", "steam", "status", "--status-file", statusFile)
	require.NoError(t, err, "steam status: %s", statusOut)
	assert.Contains(t, statusOut, "account:       bob", "status output")
	assert.Contains(t, statusOut, "auth required: false", "status output")
}

func TestSteamLoginFailureSetsAuthRequired(t *testing.T) {
	script := writeFakeSteamcmdCmd(t, `
printf 'password: '
read pass
echo ""
echo "FAILED (Invalid Password)"
`)
	statusFile := filepath.Join(t.TempDir(), "status.json")

	_, err := runCmdWithStdin(t, "wrong\n", "steam", "login",
		"--user", "bob", "--command", script, "--status-file", statusFile)
	require.Error(t, err, "expected an error for a failed login")

	statusOut, err := runCmdWithStdin(t, "", "steam", "status", "--status-file", statusFile)
	require.NoError(t, err, "steam status: %s", statusOut)
	assert.Contains(t, statusOut, "auth required: true", "status output")
	assert.Contains(t, statusOut, "invalid_password", "status output, want the failure reason")
}

func TestSteamStatusNoLoginRecorded(t *testing.T) {
	out, err := runCmdWithStdin(t, "", "steam", "status", "--status-file", filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err, "steam status")
	assert.Contains(t, out, "no login recorded yet")
}

func TestSteamLoginRequiresUser(t *testing.T) {
	_, err := runCmdWithStdin(t, "", "steam", "login", "--status-file", filepath.Join(t.TempDir(), "status.json"))
	require.Error(t, err, "expected an error for a missing --user flag")
}

func TestSteamStatusFallsBackToConfigWithoutStatusFile(t *testing.T) {
	_, err := runCmdWithStdin(t, "", "steam", "status", "--config", "/nonexistent/config.yaml")
	require.Error(t, err, "expected an error resolving the status path from a missing config file")
}

func TestSteamLoginGuardCode(t *testing.T) {
	script := writeFakeSteamcmdCmd(t, `
printf 'password: '
read pass
echo ""
printf 'Steam Guard code: '
read code
echo ""
echo "Waiting for user info...OK"
`)
	statusFile := filepath.Join(t.TempDir(), "status.json")

	out, err := runCmdWithStdin(t, "s3cr3t\n123456\n", "steam", "login",
		"--user", "bob", "--command", script, "--status-file", statusFile)
	require.NoError(t, err, "steam login: %s", out)
	assert.Contains(t, out, "login OK")
}

// A login whose final output the marker table does not recognise is confirmed against the cached
// session before it is reported as failed.
func TestSteamLoginUnrecognisedOutputRechecksCachedSession(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "logged-in")
	script := writeFakeSteamcmdCmd(t, `
if [ -e `+marker+` ]; then
	echo "Waiting for user info...OK"
	exit 0
fi
printf 'password: '
read pass
echo ""
touch `+marker+`
echo "something steamcmd never said before"
`)
	statusFile := filepath.Join(t.TempDir(), "status.json")

	out, err := runCmdWithStdin(t, "s3cr3t\n", "steam", "login",
		"--user", "bob", "--command", script, "--status-file", statusFile)
	require.NoError(t, err, "steam login: %s", out)
	assert.Contains(t, out, "login OK")
	assert.NotContains(t, out, "login failed")
}

func TestSteamLoginAppConfirmWaiting(t *testing.T) {
	script := writeFakeSteamcmdCmd(t, `
printf 'password: '
read pass
echo ""
echo "Please confirm this login in the Steam Mobile app..."
sleep 0.05
echo "Waiting for user info...OK"
`)
	statusFile := filepath.Join(t.TempDir(), "status.json")

	out, err := runCmdWithStdin(t, "s3cr3t\n", "steam", "login",
		"--user", "bob", "--command", script, "--status-file", statusFile)
	require.NoError(t, err, "steam login: %s", out)
	assert.Contains(t, out, "waiting", "want the app-confirmation notice")
}

func TestSteamLoginPassthrough(t *testing.T) {
	script := writeFakeSteamcmdCmd(t, `
echo "Logging in user 'bob' to Steam Public..."
echo "Waiting for user info...OK"
`)
	out, err := runCmdWithStdin(t, "", "steam", "login",
		"--user", "bob", "--command", script, "--passthrough")
	require.NoError(t, err, "steam login --passthrough: %s", out)
	assert.Contains(t, out, "Waiting for user info...OK", "want steamcmd's raw output copied through")
}

func TestSteamLoginMissingCommandErrors(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status.json")
	_, err := runCmdWithStdin(t, "s3cr3t\n", "steam", "login",
		"--user", "bob", "--command", filepath.Join(t.TempDir(), "does-not-exist"), "--status-file", statusFile)
	require.Error(t, err, "expected an error for a missing steamcmd binary")
}

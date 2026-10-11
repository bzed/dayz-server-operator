// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeSteamcmdCmd(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-steamcmd.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil { //nolint:gosec // test fixture, fixed 0755 mode
		t.Fatalf("write fake steamcmd: %v", err)
	}
	return path
}

func runCmdWithStdin(t *testing.T, stdin string, args ...string) (string, error) {
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
	if err != nil {
		t.Fatalf("steam login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "login OK") {
		t.Errorf("output = %q", out)
	}

	statusOut, err := runCmdWithStdin(t, "", "steam", "status", "--status-file", statusFile)
	if err != nil {
		t.Fatalf("steam status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(statusOut, "account:       bob") || !strings.Contains(statusOut, "auth required: false") {
		t.Errorf("status output = %q", statusOut)
	}
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
	if err == nil {
		t.Fatal("expected an error for a failed login")
	}

	statusOut, err := runCmdWithStdin(t, "", "steam", "status", "--status-file", statusFile)
	if err != nil {
		t.Fatalf("steam status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(statusOut, "auth required: true") {
		t.Errorf("status output = %q, want auth required: true", statusOut)
	}
	if !strings.Contains(statusOut, "invalid_password") {
		t.Errorf("status output = %q, want the failure reason", statusOut)
	}
}

func TestSteamStatusNoLoginRecorded(t *testing.T) {
	out, err := runCmdWithStdin(t, "", "steam", "status", "--status-file", filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("steam status: %v", err)
	}
	if !strings.Contains(out, "no login recorded yet") {
		t.Errorf("output = %q", out)
	}
}

func TestSteamLoginRequiresUser(t *testing.T) {
	if _, err := runCmdWithStdin(t, "", "steam", "login", "--status-file", filepath.Join(t.TempDir(), "status.json")); err == nil {
		t.Fatal("expected an error for a missing --user flag")
	}
}

func TestSteamStatusFallsBackToConfigWithoutStatusFile(t *testing.T) {
	if _, err := runCmdWithStdin(t, "", "steam", "status", "--config", "/nonexistent/config.yaml"); err == nil {
		t.Fatal("expected an error resolving the status path from a missing config file")
	}
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
	if err != nil {
		t.Fatalf("steam login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "login OK") {
		t.Errorf("output = %q", out)
	}
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
	if err != nil {
		t.Fatalf("steam login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "login OK") || strings.Contains(out, "login failed") {
		t.Errorf("output = %q", out)
	}
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
	if err != nil {
		t.Fatalf("steam login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "waiting") {
		t.Errorf("output = %q, want the app-confirmation notice", out)
	}
}

func TestSteamLoginPassthrough(t *testing.T) {
	script := writeFakeSteamcmdCmd(t, `
echo "Logging in user 'bob' to Steam Public..."
echo "Waiting for user info...OK"
`)
	out, err := runCmdWithStdin(t, "", "steam", "login",
		"--user", "bob", "--command", script, "--passthrough")
	if err != nil {
		t.Fatalf("steam login --passthrough: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Waiting for user info...OK") {
		t.Errorf("output = %q, want steamcmd's raw output copied through", out)
	}
}

func TestSteamLoginMissingCommandErrors(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status.json")
	_, err := runCmdWithStdin(t, "s3cr3t\n", "steam", "login",
		"--user", "bob", "--command", filepath.Join(t.TempDir(), "does-not-exist"), "--status-file", statusFile)
	if err == nil {
		t.Fatal("expected an error for a missing steamcmd binary")
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePodmanOnPath puts a podman shell script into a directory that PATH sees
// first; internal/supervise runs it as the podman client.
func fakePodmanOnPath(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestInstanceSuperviseRequiresTheSeparator(t *testing.T) {
	cmd := newInstanceSuperviseCmd()
	cmd.SetArgs([]string{"a", "run", "img"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--")
	assert.Contains(t, err.Error(), "supervise <name> -- ")
}

func TestInstanceSuperviseRequiresArgumentsAfterTheSeparator(t *testing.T) {
	cmd := newInstanceSuperviseCmd()
	cmd.SetArgs([]string{"a", "--"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supervise <name> -- ")
}

func TestInstanceSuperviseRejectsAMismatchedContainer(t *testing.T) {
	fakePodmanOnPath(t, "#!/bin/sh\nexit 0\n")
	cmd := newInstanceSuperviseCmd()
	cmd.SetArgs([]string{"a", "--", "run", "--name", "dzo-b", "--replace", "--rm", "img"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"dzo-b"`)
}

func TestInstanceSuperviseReturnsTheChildExitCode(t *testing.T) {
	// The fake answers the version check, says the container is not running and
	// exits the run child with 3: the supervisor reports the server's code, and
	// the command turns it into the process exit code via exitCodeErr.
	fakePodmanOnPath(t, `#!/bin/sh
case "$1" in
version) echo 5.8.6 ;;
inspect) echo false ;;
run) exit 3 ;;
*) exit 0 ;;
esac
`)
	cmd := newInstanceSuperviseCmd()
	cmd.SetArgs([]string{"a", "--", "run", "--name", "dzo-a", "--replace", "--rm", "img"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	var exitErr *exitCodeErr
	require.ErrorAs(t, err, &exitErr, "want an exit code error, got %v", err)
	assert.Equal(t, 3, exitErr.code)
}

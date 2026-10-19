// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServermodsBuildChecksAndUpdatesTheLock(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "servermods", "demo")
	writeFile(t, filepath.Join(mod, "src", "Demo", "$PBOPREFIX$"), "Demo\n")
	writeFile(t, filepath.Join(mod, "src", "Demo", "config.cpp"), "class CfgPatches {};\n")
	writeFile(t, filepath.Join(mod, "src", "Demo", "scripts", "3_Game", "Demo", "A.c"), "class A {}\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"}} {
		out, err := exec.Command("git", append([]string{"-C", mod}, args...)...).CombinedOutput() //nolint:gosec // test
		require.NoError(t, err, "git %v: %s", args, out)
	}
	lock := filepath.Join(dir, "servermods.lock")
	build := func(extra ...string) (string, error) {
		return runCmd(t, append([]string{"servermods", "build", "--src", filepath.Join(dir, "servermods"), "--out", filepath.Join(dir, "dist"), "--lock", lock}, extra...)...)
	}
	_, err := build()
	require.ErrorContains(t, err, "make servermods-lock", "without a lock file")
	out, err := build("--update-lock")
	require.NoError(t, err, "update: %s", out)
	require.Contains(t, out, "wrote", "update")
	out, err = build()
	require.NoError(t, err, "check: %s", out)
	require.Contains(t, out, "matches the build", "check")
	writeFile(t, filepath.Join(mod, "src", "Demo", "scripts", "3_Game", "Demo", "A.c"), "class A { int x; }\n")
	_, err = build()
	require.ErrorContains(t, err, "differ", "a changed mod")
	_ = os.Remove(lock)
}

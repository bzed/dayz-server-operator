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

func TestServermodsBuildChecksAndUpdatesTheLock(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "servermods", "demo")
	writeFile(t, filepath.Join(mod, "src", "Demo", "$PBOPREFIX$"), "Demo\n")
	writeFile(t, filepath.Join(mod, "src", "Demo", "config.cpp"), "class CfgPatches {};\n")
	writeFile(t, filepath.Join(mod, "src", "Demo", "scripts", "3_Game", "Demo", "A.c"), "class A {}\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", mod}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // test
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	lock := filepath.Join(dir, "servermods.lock")
	build := func(extra ...string) (string, error) {
		return runCmd(t, append([]string{"servermods", "build", "--src", filepath.Join(dir, "servermods"), "--out", filepath.Join(dir, "dist"), "--lock", lock}, extra...)...)
	}
	if _, err := build(); err == nil || !strings.Contains(err.Error(), "make servermods-lock") {
		t.Fatalf("without a lock file: %v", err)
	}
	if out, err := build("--update-lock"); err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if out, err := build(); err != nil || !strings.Contains(out, "matches the build") {
		t.Fatalf("check: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(mod, "src", "Demo", "scripts", "3_Game", "Demo", "A.c"), "class A { int x; }\n")
	if _, err := build(); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("a changed mod: %v", err)
	}
	_ = os.Remove(lock)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogsRotateArchiveAndCrashSummary(t *testing.T) {
	data := t.TempDir()
	cfg, root, _ := renderSetupIn(t, data)
	prof := filepath.Join(root, "profiles")
	old := time.Now().Add(-5 * time.Hour)
	for _, n := range []string{"script_1.log", "script_2.log"} {
		writeFile(t, filepath.Join(prof, n), "line of "+n+"\n")
		if err := os.Chtimes(filepath.Join(prof, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Join(prof, "script_2.log"), old.Add(time.Hour), old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "logs", "rotate", "x", "--dry-run", "--config", cfg)
	if err != nil || !strings.Contains(out, "move script_1.log") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(prof, "script_1.log")); err != nil {
		t.Fatal("a dry run moved a file")
	}
	if _, err := runCmd(t, "logs", "rotate", "--config", cfg); err == nil {
		t.Error("rotate needs a name or --all")
	}
	out, err = runCmd(t, "logs", "rotate", "--all", "--config", cfg)
	if err != nil || !strings.Contains(out, "archived 1 log file") {
		t.Fatalf("rotate: %v\n%s", err, out)
	}
	out, err = runCmd(t, "logs", "archive", "x", "--since", "1d", "--match", "script", "--config", cfg)
	if err != nil || !strings.Contains(out, "script_1.log.gz") {
		t.Fatalf("archive: %v\n%s", err, out)
	}
	if _, err := runCmd(t, "logs", "archive", "x", "--since", "xx", "--config", cfg); err == nil {
		t.Error("a bad --since must fail")
	}
	if _, err := runCmd(t, "logs", "archive", "x", "--match", "(", "--config", cfg); err == nil {
		t.Error("a bad --match must fail")
	}

	t.Setenv("SERVICE_RESULT", "success")
	if out, err = runCmd(t, "logs", "crash-summary", "x", "--config", cfg); err != nil || out != "" {
		t.Fatalf("a clean stop prints nothing: %v %q", err, out)
	}
	t.Setenv("SERVICE_RESULT", "exit-code")
	out, err = runCmd(t, "logs", "crash-summary", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "script_2.log") || !strings.Contains(out, "line of script_2.log") {
		t.Fatalf("crash summary: %v\n%s", err, out)
	}
}

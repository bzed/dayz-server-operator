// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogsRotateArchiveAndCrashSummary(t *testing.T) {
	data := t.TempDir()
	cfg, root, _ := renderSetupIn(t, data)
	prof := filepath.Join(root, "profiles")
	old := time.Now().Add(-5 * time.Hour)
	for _, n := range []string{"script_1.log", "script_2.log"} {
		writeFile(t, filepath.Join(prof, n), "line of "+n+"\n")
		require.NoError(t, os.Chtimes(filepath.Join(prof, n), old, old))
	}
	require.NoError(t, os.Chtimes(filepath.Join(prof, "script_2.log"), old.Add(time.Hour), old.Add(time.Hour)))
	out, err := runCmd(t, "logs", "rotate", "x", "--dry-run", "--config", cfg)
	require.NoError(t, err, "dry run: %s", out)
	require.Contains(t, out, "move script_1.log", "dry run")
	_, err = os.Stat(filepath.Join(prof, "script_1.log"))
	require.NoError(t, err, "a dry run moved a file")
	_, err = runCmd(t, "logs", "rotate", "--config", cfg)
	require.Error(t, err, "rotate needs a name or --all")
	out, err = runCmd(t, "logs", "rotate", "--all", "--config", cfg)
	require.NoError(t, err, "rotate: %s", out)
	require.Contains(t, out, "archived 1 log file", "rotate")
	out, err = runCmd(t, "logs", "archive", "x", "--since", "1d", "--match", "script", "--config", cfg)
	require.NoError(t, err, "archive: %s", out)
	require.Contains(t, out, "script_1.log.gz", "archive")
	_, err = runCmd(t, "logs", "archive", "x", "--since", "xx", "--config", cfg)
	require.Error(t, err, "a bad --since must fail")
	_, err = runCmd(t, "logs", "archive", "x", "--match", "(", "--config", cfg)
	require.Error(t, err, "a bad --match must fail")

	t.Setenv("SERVICE_RESULT", "success")
	out, err = runCmd(t, "logs", "crash-summary", "x", "--config", cfg)
	require.NoError(t, err, "a clean stop prints nothing")
	require.Empty(t, out, "a clean stop prints nothing")
	t.Setenv("SERVICE_RESULT", "exit-code")
	out, err = runCmd(t, "logs", "crash-summary", "x", "--config", cfg)
	require.NoError(t, err, "crash summary: %s", out)
	require.Contains(t, out, "script_2.log", "crash summary")
	require.Contains(t, out, "line of script_2.log", "crash summary")
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeScript(t testing.TB, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hook.sh")
	full := "#!/bin/sh\n" + body
	require.NoError(t, os.WriteFile(path, []byte(full), 0o755), "write script")
	return path
}

func TestRunSuccessCapturesStdout(t *testing.T) {
	script := writeScript(t, `echo "hello from hook"`)
	r := &Runner{}
	res := r.Run(t.Context(), script, Context{Instance: "deerisle", Point: "pre_start"})
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "hello from hook")
	assert.Equal(t, 0, res.ExitCode)
}

func TestRunPassesEnvContract(t *testing.T) {
	script := writeScript(t, `echo "$DZO_INSTANCE/$DZO_HOOK_POINT/$DZO_MAP"`)
	r := &Runner{Env: map[string]string{"DZO_MAP": "empty.deerisle"}}
	res := r.Run(t.Context(), script, Context{Instance: "deerisle", Point: "pre_start"})
	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "deerisle/pre_start/empty.deerisle")
}

func TestRunPassesJSONContextOnStdin(t *testing.T) {
	script := writeScript(t, `cat`)
	r := &Runner{}
	res := r.Run(t.Context(), script, Context{
		Instance: "hashima",
		Point:    "post_download",
		Extra:    map[string]any{"mod_id": float64(1559212036)},
	})
	require.NoError(t, res.Err)
	assert.True(t, strings.Contains(res.Stdout, `"mod_id":1.559212036e+09`) || strings.Contains(res.Stdout, "1559212036"),
		"Stdout (echoed stdin) = %q", res.Stdout)
	assert.Contains(t, res.Stdout, `"instance":"hashima"`)
}

func TestRunNonZeroExit(t *testing.T) {
	script := writeScript(t, `echo "failing" >&2; exit 3`)
	r := &Runner{}
	res := r.Run(t.Context(), script, Context{Instance: "x", Point: "post_render"})
	require.Error(t, res.Err, "expected an error for a non-zero exit")
	assert.Equal(t, 3, res.ExitCode)
	assert.Contains(t, res.Err.Error(), "failing", "error should include the stderr tail")
}

func TestRunMissingScript(t *testing.T) {
	r := &Runner{}
	res := r.Run(t.Context(), filepath.Join(t.TempDir(), "nonexistent.sh"), Context{})
	require.Error(t, res.Err, "expected an error for a missing script")
}

func TestRunTimeout(t *testing.T) {
	script := writeScript(t, `sleep 5`)
	r := &Runner{Timeout: 100 * time.Millisecond}

	start := time.Now()
	res := r.Run(t.Context(), script, Context{})
	elapsed := time.Since(start)

	require.Error(t, res.Err, "expected a timeout error")
	// The whole process group must be killed promptly; if only the
	// direct child (e.g. a shell) is killed while a grandchild it forked
	// (here, "sleep" itself) is left running and holding the stdout/
	// stderr pipes open, Cmd.Wait blocks until that grandchild exits on
	// its own - defeating Timeout. This must return in well under 5s.
	require.LessOrEqual(t, elapsed, 2*time.Second, "the child process tree was not killed promptly after a 100ms timeout")
}

func TestRunAllStopsOnError(t *testing.T) {
	ok := writeScript(t, `echo ok`)
	fail := writeScript(t, `exit 1`)
	neverRun := writeScript(t, `touch "$MARKER_FILE"`)
	markerDir := t.TempDir()
	marker := filepath.Join(markerDir, "ran")

	r := &Runner{Env: map[string]string{"MARKER_FILE": marker}}
	results := RunAll(t.Context(), r, []string{ok, fail, neverRun}, Context{}, true)

	require.Len(t, results, 2, "stopped after the failure")
	require.NoError(t, results[0].Err)
	require.Error(t, results[1].Err)
	assert.NoFileExists(t, marker, "the third script should never have run")
}

func TestRunAllContinuesOnError(t *testing.T) {
	ok := writeScript(t, `echo ok`)
	fail := writeScript(t, `exit 1`)
	alsoOK := writeScript(t, `echo also-ok`)

	r := &Runner{}
	results := RunAll(t.Context(), r, []string{ok, fail, alsoOK}, Context{}, false)

	require.Len(t, results, 3, "all scripts run")
	require.NoError(t, results[0].Err)
	require.Error(t, results[1].Err)
	require.NoError(t, results[2].Err)
}

func TestRunAllEmpty(t *testing.T) {
	r := &Runner{}
	results := RunAll(t.Context(), r, nil, Context{}, true)
	require.Empty(t, results)
}

func TestLastLine(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"one":               "one",
		"one\n":             "one",
		"one\ntwo\n":        "two",
		"one\ntwo\nthree\n": "three",
		"\n\n":              "",
	}
	for in, want := range cases {
		assert.Equal(t, want, lastLine(in), "lastLine(%q)", in)
	}
}

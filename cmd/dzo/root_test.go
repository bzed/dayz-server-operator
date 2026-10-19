// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/goleak"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runCmd(t testing.TB, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestVersionCmd(t *testing.T) {
	out, err := runCmd(t, "version")
	require.NoError(t, err, "version")
	assert.Contains(t, out, "dev", "version output should mention the dev version")
}

func TestConfigValidateCmd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("paths:\n  data: "+dir+"\n"), 0o600), "write fixture")
	out, err := runCmd(t, "config", "validate", "--config", path)
	require.NoError(t, err, "config validate: %s", out)
	assert.Contains(t, out, "is valid")
}

func TestConfigValidateCmdMissingFile(t *testing.T) {
	_, err := runCmd(t, "config", "validate", "--config", "/nonexistent/config.yaml")
	require.Error(t, err, "expected an error for a missing config file")
}

func TestServerCfgShowAndDiff(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.cfg")
	newPath := filepath.Join(dir, "new.cfg")
	require.NoError(t, os.WriteFile(oldPath, []byte(`hostname = "old"; maxPlayers = 40;`), 0o600), "write old.cfg")
	require.NoError(t, os.WriteFile(newPath, []byte(`hostname = "new"; maxPlayers = 40;`), 0o600), "write new.cfg")

	out, err := runCmd(t, "servercfg", "show", oldPath)
	require.NoError(t, err, "show")
	assert.Contains(t, out, `hostname = "old";`)

	out, err = runCmd(t, "servercfg", "diff", oldPath, newPath)
	require.NoError(t, err, "diff")
	assert.Contains(t, out, "hostname")
}

func TestServerCfgShowMissingFile(t *testing.T) {
	_, err := runCmd(t, "servercfg", "show", "/nonexistent/serverDZ.cfg")
	require.Error(t, err, "expected an error for a missing file")
}

func TestServerCfgDiffParseError(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.cfg")
	require.NoError(t, os.WriteFile(badPath, []byte("not valid"), 0o600), "write bad.cfg")
	goodPath := filepath.Join(dir, "good.cfg")
	require.NoError(t, os.WriteFile(goodPath, []byte(`hostname = "ok";`), 0o600), "write good.cfg")
	_, err := runCmd(t, "servercfg", "diff", badPath, goodPath)
	require.Error(t, err, "expected a parse error")
}

func TestMain(m *testing.M) {
	// The package build runs the tests as root.
	_ = os.Setenv(allowRootEnv, "1")
	goleak.VerifyTestMain(m)
}

func TestRefuseRoot(t *testing.T) {
	root := newRootCmd()
	find := func(args ...string) *cobra.Command {
		c, _, err := root.Find(args)
		require.NoError(t, err)
		return c
	}
	for _, tc := range []struct {
		args     []string
		euid     int
		override string
		refused  bool
	}{
		{[]string{"mod", "update"}, 0, "", true},
		{[]string{"instance", "render"}, 0, "", true},
		{[]string{"serve"}, 0, "0", true},
		{[]string{"mod", "update"}, 1000, "", false},
		{[]string{"mod", "update"}, 0, "1", false},
		{[]string{"health", "live"}, 0, "", false},
		{[]string{"servermods", "build"}, 0, "", false},
		{[]string{"gen-man"}, 0, "", false},
		{[]string{"version"}, 0, "", false},
		{nil, 0, "", false},
	} {
		err := refuseRoot(find(tc.args...), tc.euid, tc.override)
		if tc.refused {
			assert.Error(t, err, "%v euid %d override %q should be refused", tc.args, tc.euid, tc.override)
		} else {
			assert.NoError(t, err, "%v euid %d override %q should not be refused", tc.args, tc.euid, tc.override)
		}
	}
}

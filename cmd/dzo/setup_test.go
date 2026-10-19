// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupDryRunCommand(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	writeFile(t, cfg, "paths:\n  data: "+filepath.Join(dir, "data")+"\n")
	images := filepath.Join(dir, "images")
	for _, n := range []string{"runtime", "steamcmd"} {
		writeFile(t, filepath.Join(images, n, "Containerfile"), "FROM scratch\n")
	}
	if os.Geteuid() == 0 {
		t.Skip("dzo setup refuses to run as root")
	}
	out, err := runCmd(t, "setup", "--dry-run", "--config", cfg, "--images-dir", images, "--unit-dir", filepath.Join(dir, "units"))
	if err != nil {
		require.Contains(t, out, "podman is not installed", "setup --dry-run: %v", err)
	}
	assert.Contains(t, out, "todo  create paths.data")
	assert.Contains(t, out, "dzo-image-refresh.timer")
	_, err = os.Stat(filepath.Join(dir, "data"))
	assert.ErrorIs(t, err, os.ErrNotExist, "--dry-run created the data directory")
	_, err = runCmd(t, "setup", "--config", filepath.Join(dir, "none.yaml"))
	assert.Error(t, err, "a missing config must fail")
}

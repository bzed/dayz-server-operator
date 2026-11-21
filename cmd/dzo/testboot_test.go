// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/config"
)

// A remote host's podman only sees paths.data at the same path, so the boot
// test's run tree has to be below it; the cache directory is invisible there.
func TestBootTestRunDirRemote(t *testing.T) {
	cache, err := os.UserCacheDir()
	require.NoError(t, err)
	dir, err := bootRunDir(config.Default(), "20260101-120000-abcdef")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "dzo", "boottest", "20260101-120000-abcdef"), dir, "a native host keeps the run tree in the cache")

	cfg, err := config.Parse([]byte("podman:\n  mode: remote\npaths:\n  data: /srv/dzo-remote\n"))
	require.NoError(t, err)
	dir, err = bootRunDir(cfg, "20260101-120000-abcdef")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/srv/dzo-remote", "boottest", "20260101-120000-abcdef"), dir)
}

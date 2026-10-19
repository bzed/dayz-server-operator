// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/cache"
)

func TestCacheListAndGC(t *testing.T) {
	dir := t.TempDir()
	s := cache.New(dir)
	for _, id := range []string{"1", "2", "3"} {
		_, err := s.NewGeneration(id)
		require.NoError(t, err, "NewGeneration(%s)", id)
	}
	require.NoError(t, s.SetCurrent("2"), "SetCurrent")

	out, err := runCmd(t, "cache", "list", dir)
	require.NoError(t, err, "cache list")
	assert.Contains(t, out, "* 2", "list output should mark 2 as current")

	out, err = runCmd(t, "cache", "gc", dir, "--keep", "1")
	require.NoError(t, err, "cache gc")
	assert.Contains(t, out, "removed", "gc output")
}

func TestCacheListMissingStore(t *testing.T) {
	out, err := runCmd(t, "cache", "list", "/nonexistent/store")
	require.NoError(t, err, "cache list on an empty store should not error")
	assert.Empty(t, out, "expected no output for an empty store")
}

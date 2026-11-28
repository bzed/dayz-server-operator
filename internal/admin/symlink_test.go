// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteModConfigDoesNotFollowPlantedSymlinks(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim.db")
	require.NoError(t, os.WriteFile(victim, []byte("HOST-ONLY-ORIGINAL"), 0o600))
	tokens := t.TempDir()

	// leaf: config.json.tmp and config.json are links to the victim
	profiles := filepath.Join(base, "p1")
	adm := filepath.Join(profiles, "dzo", "admin")
	require.NoError(t, os.MkdirAll(adm, 0o750))
	require.NoError(t, os.Symlink(victim, filepath.Join(adm, "config.json.tmp")))
	require.NoError(t, os.Symlink(victim, filepath.Join(adm, "config.json")))
	require.NoError(t, WriteModConfig(profiles, tokens, "a", ModConfig{Endpoint: "http://127.0.0.1:1/"}))
	got, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "HOST-ONLY-ORIGINAL", string(got))
	fi, err := os.Lstat(filepath.Join(adm, "config.json"))
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular())

	// parent: dzo is a link to a directory outside the profiles
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o750))
	p2 := filepath.Join(base, "p2")
	require.NoError(t, os.MkdirAll(p2, 0o750))
	require.NoError(t, os.Symlink(outside, filepath.Join(p2, "dzo")))
	require.Error(t, WriteModConfig(p2, tokens, "b", ModConfig{Endpoint: "http://127.0.0.1:1/"}))
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries)
}

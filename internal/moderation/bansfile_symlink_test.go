// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBansFileDoesNotFollowPlantedSymlinks(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim.db")
	require.NoError(t, os.WriteFile(victim, []byte("HOST-ONLY-ORIGINAL"), 0o600))
	be := filepath.Join(base, "profiles", "battleye")
	require.NoError(t, os.MkdirAll(be, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(be, BansTxt), nil, 0o600))
	require.NoError(t, os.Symlink(victim, filepath.Join(be, sidecar)))
	_, err := WriteBansFile(be, nil)
	require.NoError(t, err)
	got, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "HOST-ONLY-ORIGINAL", string(got))

	// a symlinked battleye directory is refused
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o750))
	p2 := filepath.Join(base, "p2")
	require.NoError(t, os.MkdirAll(p2, 0o750))
	require.NoError(t, os.Symlink(outside, filepath.Join(p2, "battleye")))
	_, err = WriteBansFile(filepath.Join(p2, "battleye"), []byte("x"))
	require.Error(t, err)
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package integrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncMapStreamDoesNotFollowPlantedSymlinks(t *testing.T) {
	base := t.TempDir()
	site, profiles := filepath.Join(base, "site"), filepath.Join(base, "profiles")
	inst := filepath.Join(site, "instances", "x")
	victim := filepath.Join(base, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("ORIGINAL"), 0o600))
	put(t, filepath.Join(inst, "MapStreamDZE", "a.dze"), "A")
	dst := filepath.Join(profiles, "dzo", "mapstream", "DZE")
	require.NoError(t, os.MkdirAll(dst, 0o750))
	require.NoError(t, os.Symlink(victim, filepath.Join(dst, "a.dze")))
	require.NoError(t, os.Symlink(victim, filepath.Join(dst, "stray.dze")))

	_, _, err := SyncMapStream(inst, profiles)
	require.NoError(t, err)
	got, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "ORIGINAL", string(got), "the link is replaced or removed, never written through")
	b, err := os.ReadFile(filepath.Join(dst, "a.dze"))
	require.NoError(t, err)
	require.Equal(t, "A", string(b))
	require.NoFileExists(t, filepath.Join(dst, "stray.dze"))
}

func TestSyncMapStreamRefusesSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	inst := filepath.Join(base, "site", "instances", "x")
	profiles := filepath.Join(base, "profiles")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o750))
	put(t, filepath.Join(inst, "MapStreamDZE", "a.dze"), "A")
	require.NoError(t, os.MkdirAll(profiles, 0o750))
	require.NoError(t, os.Symlink(outside, filepath.Join(profiles, "dzo")))

	_, _, err := SyncMapStream(inst, profiles)
	require.Error(t, err)
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries)
}

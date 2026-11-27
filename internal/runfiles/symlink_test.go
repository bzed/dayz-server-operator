// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package runfiles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The game container can plant symlinks in profiles/. Write must never follow them out of the tree.
func TestWriteDoesNotFollowPlantedSymlinks(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim.db")
	require.NoError(t, os.WriteFile(victim, []byte("HOST-ONLY-ORIGINAL"), 0o600))
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "runtime"), 0o750))
	in := func(profiles string) Input {
		return Input{RuntimeDir: filepath.Join(base, "runtime"), ProfilesDir: profiles, RConPass: "pw", RConPort: 2306}
	}

	t.Run("leaf", func(t *testing.T) {
		profiles := filepath.Join(base, "p1")
		require.NoError(t, os.MkdirAll(filepath.Join(profiles, "battleye"), 0o750))
		require.NoError(t, os.Symlink(victim, filepath.Join(profiles, "battleye", "beserver_x64.cfg")))
		require.NoError(t, Write(in(profiles)))
		got, err := os.ReadFile(victim)
		require.NoError(t, err)
		require.Equal(t, "HOST-ONLY-ORIGINAL", string(got))
		b, err := os.ReadFile(filepath.Join(profiles, "battleye", "beserver_x64.cfg"))
		require.NoError(t, err)
		require.Contains(t, string(b), "RConPassword pw")
	})
	t.Run("parent", func(t *testing.T) {
		profiles := filepath.Join(base, "p2")
		require.NoError(t, os.MkdirAll(profiles, 0o750))
		require.NoError(t, os.Symlink(outside, filepath.Join(profiles, "battleye")))
		require.Error(t, Write(in(profiles)))
		require.NoFileExists(t, filepath.Join(outside, "beserver_x64.cfg"))
	})
}

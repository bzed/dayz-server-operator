// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestHashFileAndLoadManifestRefuseSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))
	_, err := HashFile(fifo)
	require.ErrorIs(t, err, errNotRegular)
	_, err = LoadManifest(fifo)
	require.ErrorIs(t, err, errNotRegular)

	target := filepath.Join(dir, "t")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
	link := filepath.Join(dir, "l")
	require.NoError(t, os.Symlink(target, link))
	_, err = HashFile(link)
	require.ErrorIs(t, err, errNotRegular)

	big := filepath.Join(dir, "big")
	f, err := os.Create(big)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxManifestFile+1))
	require.NoError(t, f.Close())
	_, err = LoadManifest(big)
	require.ErrorIs(t, err, errNotRegular)
}

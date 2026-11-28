// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileMarkersIgnoreLinksFIFOsAndHugeFiles(t *testing.T) {
	base := t.TempDir()
	profiles := filepath.Join(base, "profiles")
	dir := MarkerDir(profiles)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	outside := filepath.Join(base, "outside.json")
	require.NoError(t, os.WriteFile(outside, []byte(`[{"id":"HOST"}]`), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.json")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "fifo.json"), 0o600))
	huge := append([]byte(`[{"id":"H"}]`), make([]byte, 2<<20)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "huge.json"), huge, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.json"), []byte(`[{"id":"ok"}]`), 0o600))

	var f fileMarkers
	done := make(chan []Marker, 1)
	go func() { done <- f.load(dir, time.Now()) }()
	select {
	case got := <-done:
		require.Len(t, got, 1)
		require.Equal(t, "ok", got[0].ID)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "load blocked on a FIFO")
	}

	// a path component that links out of the profiles tree is not read
	other := filepath.Join(base, "p2")
	outDir := filepath.Join(base, "outdir")
	require.NoError(t, os.MkdirAll(outDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "x.json"), []byte(`[{"id":"x"}]`), 0o600))
	require.NoError(t, os.MkdirAll(other, 0o750))
	require.NoError(t, os.Symlink(outDir, filepath.Join(other, "dzo")))
	var g fileMarkers
	require.Empty(t, g.load(MarkerDir(other), time.Now()))
}

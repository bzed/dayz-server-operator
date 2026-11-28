// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChangedOutsideRefusesLinksFIFOsAndHugeFiles(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("guid 0 x\n"), 0o600))
	mk := func(name string) string {
		d := filepath.Join(base, name, "battleye")
		require.NoError(t, os.MkdirAll(d, 0o750))
		return d
	}
	link := mk("link")
	require.NoError(t, os.Symlink(outside, filepath.Join(link, BansTxt)))
	_, _, err := ChangedOutside(link)
	require.Error(t, err)

	fifo := mk("fifo")
	require.NoError(t, syscall.Mkfifo(filepath.Join(fifo, BansTxt), 0o600))
	huge := mk("huge")
	require.NoError(t, os.WriteFile(filepath.Join(huge, BansTxt), make([]byte, 2<<20), 0o600))

	done := make(chan error, 2)
	go func() { _, _, err := ChangedOutside(fifo); done <- err }()
	go func() { _, _, err := ChangedOutside(huge); done <- err }()
	for range 2 {
		select {
		case err := <-done:
			require.Error(t, err)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "ChangedOutside blocked or read without a limit")
		}
	}
}

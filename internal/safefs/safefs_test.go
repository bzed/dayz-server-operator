// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package safefs_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/safefs"
)

func TestWriteFileRefusesLinks(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("ORIGINAL"), 0o600))
	tree := filepath.Join(base, "tree")
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "d"), 0o750))
	require.NoError(t, os.Symlink(victim, filepath.Join(tree, "d", "leaf")))
	require.NoError(t, os.Symlink(base, filepath.Join(tree, "esc")))
	root, err := os.OpenRoot(tree)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()

	// A symlink at the leaf is replaced, the target stays unchanged.
	require.NoError(t, safefs.WriteFile(root, "d/leaf", []byte("new"), 0o600))
	got, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "ORIGINAL", string(got))
	fi, err := os.Lstat(filepath.Join(tree, "d", "leaf"))
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular())

	// A symlinked parent that leaves the root is an error.
	require.Error(t, safefs.WriteFile(root, "esc/planted", []byte("x"), 0o600))
	require.NoFileExists(t, filepath.Join(base, "planted"))
}

func TestReadFile(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	tree := filepath.Join(base, "tree")
	require.NoError(t, os.MkdirAll(tree, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "ok"), []byte("hi"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "big"), make([]byte, 100), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(tree, "abs")))
	require.NoError(t, os.Symlink("ok", filepath.Join(tree, "rel")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(tree, "fifo"), 0o600))
	root, err := os.OpenRoot(tree)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()

	b, err := safefs.ReadFile(root, "ok", 0)
	require.NoError(t, err)
	require.Equal(t, "hi", string(b))
	_, err = safefs.ReadFile(root, "missing", 0)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = safefs.ReadFile(root, "abs", 0)
	require.Error(t, err)
	_, err = safefs.ReadFile(root, "rel", 0)
	require.Error(t, err)
	_, err = safefs.ReadFile(root, "big", 10)
	require.ErrorIs(t, err, safefs.ErrTooLarge)

	done := make(chan error, 1)
	go func() { _, err := safefs.ReadFile(root, "fifo", 0); done <- err }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, safefs.ErrNotRegular)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "ReadFile blocked on a FIFO")
	}
}

func TestOpenDirRefusesEscapingLink(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "tree")
	require.NoError(t, os.MkdirAll(tree, 0o750))
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.Symlink(outside, filepath.Join(tree, "be")))
	_, err := safefs.OpenDir(filepath.Join(tree, "be"))
	require.Error(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "real"), 0o750))
	r, err := safefs.OpenDir(filepath.Join(tree, "real"))
	require.NoError(t, err)
	require.NoError(t, r.Close())
}

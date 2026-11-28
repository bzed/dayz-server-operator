// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestCopyPristineRejectsLinksAndSpecialFiles(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secret, []byte("HOST-SECRET"), 0o600))

	for name, plant := range map[string]func(p string) error{
		"file symlink": func(p string) error { return os.Symlink(secret, p) },
		"dir symlink":  func(p string) error { return os.Symlink(filepath.Dir(secret), p) },
		"fifo":         func(p string) error { return unix.Mkfifo(p, 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			writeTree(t, src, map[string]string{"init.c": "c"})
			require.NoError(t, plant(filepath.Join(src, "leak.txt")))
			err := CopyPristine(src, dst)
			require.ErrorContains(t, err, "not a regular file")
			assert.NoFileExists(t, filepath.Join(dst, "leak.txt"))
		})
	}
}

func TestFetchPristineRejectsCommittedSymlink(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secret, []byte("HOST-SECRET"), 0o600))
	repo := gitRepo(t, map[string]string{"m/init.c": "c"})
	require.NoError(t, os.Symlink(secret, filepath.Join(repo, "m", "f.txt")))
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "link"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	parent := filepath.Join(t.TempDir(), "servermpmissions")
	require.NoError(t, os.MkdirAll(parent, 0o750))
	dst := filepath.Join(parent, "m")
	err := FetchPristine(t.Context(), "file://"+repo, "main", "m", dst)
	require.ErrorContains(t, err, "not a regular file")
	assert.NoDirExists(t, dst)
}

func TestFillFromFallbackRejectsLink(t *testing.T) {
	fallback, staging := t.TempDir(), t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secret, []byte("HOST-SECRET"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(fallback, "init.c")))
	err := fillFromFallback(fallback, staging)
	require.ErrorContains(t, err, "init.c")
	assert.NoFileExists(t, filepath.Join(staging, "init.c"))
}

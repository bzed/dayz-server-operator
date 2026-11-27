// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// The live mission is writable by the game container, so nothing in it is
// trusted. All reads and writes of live files go through an os.Root on the live
// directory (no path leaves it, whatever links the container plants), and a leaf
// is opened without following a link and must be a regular file of sane size.
const (
	// maxMissionFile caps the size of one mission file that dzo hashes or copies.
	maxMissionFile = 256 << 20
	// maxManifestFile caps the size of the manifest.
	maxManifestFile = 64 << 20
	// hashUnusable stands for the hash of a live path that is not a usable regular file
	// (a link, FIFO, device or oversize file). It never equals a written hash, so Classify
	// reports drift and Apply replaces the path.
	hashUnusable = "unusable"
)

// errNotRegular reports a path that is not a regular file within the size limit.
var errNotRegular = errors.New("not a regular file within the size limit")

// readLimited opens a file without following a final link and without blocking on a
// FIFO (open does the real open, by path or relative to a Root), checks with fstat that
// it is a regular file within limit, and reads it.
func readLimited(open func(flag int) (*os.File, error), limit int64) ([]byte, error) {
	f, err := open(os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%w: a link", errNotRegular)
		}
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only handle
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if !fi.Mode().IsRegular() || fi.Size() > limit {
		return nil, errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, errNotRegular
	}
	return data, nil
}

// readFileSafe reads a regular file by path, without following a final link.
func readFileSafe(p string, limit int64) ([]byte, error) {
	return readLimited(func(flag int) (*os.File, error) { return os.OpenFile(p, flag, 0) }, limit) //nolint:gosec // opened with O_NOFOLLOW and checked with fstat
}

// openLive opens a Root on the live mission directory. A liveDir that is a link is
// refused. With create, a missing directory is created; without, a missing directory
// returns (nil, nil).
func openLive(liveDir string, create bool) (*os.Root, error) {
	fi, err := os.Lstat(liveDir)
	switch {
	case err == nil:
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return nil, fmt.Errorf("mission: live mission %s is not a plain directory", liveDir)
		}
	case errors.Is(err, fs.ErrNotExist):
		if !create {
			return nil, nil
		}
		if err := os.MkdirAll(liveDir, 0o750); err != nil {
			return nil, fmt.Errorf("mission: create live mission: %w", err)
		}
	default:
		return nil, fmt.Errorf("mission: %w", err)
	}
	root, err := os.OpenRoot(liveDir)
	if err != nil {
		return nil, fmt.Errorf("mission: open live mission: %w", err)
	}
	return root, nil
}

// readRootFile reads a regular file below root (not following a final link).
func readRootFile(root *os.Root, rel string) ([]byte, error) {
	// Lstat does not follow the leaf: a link (even one that leaves the root) is "not regular", not
	// an escape error. The open below still uses O_NOFOLLOW and fstat for a leaf swapped meanwhile.
	fi, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return readLimited(func(flag int) (*os.File, error) { return root.OpenFile(rel, flag, 0) }, maxMissionFile)
}

// hashLive hashes rel below root. A missing file (or no root) returns "", nil; a path that
// is not a usable regular file returns hashUnusable.
func hashLive(root *os.Root, rel string) (string, error) {
	if root == nil {
		return "", nil
	}
	data, err := readRootFile(root, rel)
	switch {
	case err == nil:
		return HashBytes(data), nil
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case errors.Is(err, errNotRegular):
		return hashUnusable, nil
	default:
		return "", fmt.Errorf("mission: hash live %s: %w", rel, err)
	}
}

// writeRootAtomic writes data to rel below root (temp file + rename inside root).
func writeRootAtomic(root *os.Root, rel string, data []byte, perm os.FileMode) error {
	dir := path.Dir(rel)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tmp := path.Join(dir, ".tmp-"+rand.Text())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = root.Remove(tmp) // best-effort cleanup
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := root.Rename(tmp, rel); err != nil {
		return fmt.Errorf("activate %s: %w", rel, err)
	}
	ok = true
	return nil
}

// removeEmptyDirs removes dir and its parents below root as far as they are empty.
func removeEmptyDirs(root *os.Root, dir string) {
	for dir != "." && dir != "/" && dir != "" {
		if root.Remove(dir) != nil { // not empty (or gone): stop
			return
		}
		dir = path.Dir(dir)
	}
}

// RemoveLegacyManifest deletes the manifest that older dzo versions kept in the
// container-writable mpmissions directory. dzo never reads it: the container could
// have forged it. A link or any other leaf is removed as the leaf itself.
func RemoveLegacyManifest(mpmissionsDir string) error {
	err := os.Remove(filepath.Join(mpmissionsDir, ".dzo-manifest.json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mission: remove legacy manifest: %w", err)
	}
	return nil
}

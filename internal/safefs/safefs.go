// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package safefs reads and writes files in trees that a game container can
// modify. The container runs as the same host user as dzo, so it can plant
// symlinks and FIFOs. Every helper works inside an os.Root: a path that
// escapes the root through a link is an error. Reads also refuse links at the
// leaf, anything that is not a regular file, and files above a size limit.
package safefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// MaxFile is the default read limit: 1 MiB.
const MaxFile = 1 << 20

// ErrNotRegular means the name is not a regular file (a link, a FIFO, a device, a directory).
var ErrNotRegular = errors.New("not a regular file")

// ErrTooLarge means the file is larger than the read limit.
var ErrTooLarge = errors.New("file too large")

// OpenDir opens dir as a root. A symlink in dir's final component that leaves
// its parent directory is refused. Callers that know a trusted ancestor
// should open that ancestor with os.OpenRoot and use a relative path instead.
func OpenDir(dir string) (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return nil, fmt.Errorf("safefs: %w", err)
	}
	defer func() { _ = parent.Close() }() // the child root stays valid
	r, err := parent.OpenRoot(filepath.Base(dir))
	if err != nil {
		return nil, fmt.Errorf("safefs: %w", err)
	}
	return r, nil
}

// ReadFile reads name below root without following a link at the leaf and
// without blocking on a FIFO. It returns ErrNotRegular for anything but a
// regular file, ErrTooLarge above limit bytes (MaxFile when limit <= 0), and
// an error wrapping fs.ErrNotExist when the file is missing.
func ReadFile(root *os.Root, name string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = MaxFile
	}
	// os.Root follows links that stay inside the root, so refuse a link at the
	// leaf here. O_NONBLOCK keeps a FIFO swapped in after the check from
	// blocking the open; the fstat below then rejects it.
	if li, err := root.Lstat(name); err != nil {
		return nil, fmt.Errorf("safefs: %w", err)
	} else if !li.Mode().IsRegular() {
		return nil, fmt.Errorf("safefs: %s: %w", name, ErrNotRegular)
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("safefs: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only handle
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("safefs: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("safefs: %s: %w", name, ErrNotRegular)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("safefs: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("safefs: %s: %w", name, ErrTooLarge)
	}
	return b, nil
}

// WriteFile writes data to name below root through a temporary file created
// with O_EXCL in the same directory, then renames it over name. A symlink at
// name is replaced, not followed. Missing parent directories get mode 0o750.
func WriteFile(root *os.Root, name string, data []byte, perm os.FileMode) error {
	name = filepath.ToSlash(name)
	dir := path.Dir(name)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("safefs: %w", err)
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Errorf("safefs: %w", err)
	}
	tmp := path.Join(dir, ".tmp-"+hex.EncodeToString(b[:]))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("safefs: %w", err)
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp) // best effort; the write error is what matters
		return fmt.Errorf("safefs: write %s: %w", name, err)
	}
	return nil
}

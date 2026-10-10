// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// maxUnpacked bounds what an archive may expand to.
const maxUnpacked = 4 << 30

// LocalSource says where a local servermod comes from (§C7, D37): a
// directory (in the site repo, or an absolute host path), or a release
// archive pinned by sha256.
type LocalSource struct {
	Dir    string
	URL    string
	SHA256 string
	HTTP   *http.Client // defaults to http.DefaultClient
	// Check, if set, is called with the servermod directory before anything is
	// imported; an error stops the import (the compat.yaml gate of shipped
	// servermods).
	Check func(dir string) error
	// Signed imports a debug client mod (site.ModRef.DebugClient): its keys/ directory is kept,
	// and every PBO has to carry a signature made with one of the keys in it.
	Signed bool
}

// ImportLocal validates a local servermod and stores it as a content-hashed
// generation cache/local/<name>/<hash>/, then makes it current. Identical
// content gives the same generation, so re-running is a no-op. Only regular
// files are taken (a symlink is an error), a top-level keys/ dir is ignored.
func (in *Installer) ImportLocal(ctx context.Context, name string, src LocalSource) (InstallResult, error) {
	scratch, err := in.scratch()
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(scratch) //nolint:errcheck // gone after a successful rename

	root := src.Dir
	if src.URL != "" {
		unpacked := filepath.Join(scratch, "unpacked")
		if err := fetchAndUnpack(ctx, src, unpacked); err != nil {
			return InstallResult{}, err
		}
		root = unpacked
	}
	if root, err = modRoot(root); err != nil {
		return InstallResult{}, err
	}
	if src.Check != nil && src.URL == "" && !src.Signed {
		if err := src.Check(root); err != nil {
			return InstallResult{}, err
		}
	}
	tree := filepath.Join(scratch, "tree")
	if err := copyRegular(root, tree, src.Signed); err != nil {
		return InstallResult{}, err
	}
	if err := ValidateMod(tree, true); err != nil {
		return InstallResult{}, err
	}
	if src.Signed {
		if err := ValidateSigned(tree); err != nil {
			return InstallResult{}, err
		}
	}
	hash, err := hashTree(tree)
	if err != nil {
		return InstallResult{}, err
	}
	store := LocalModStore(in.CacheRoot, name)
	created, err := installGeneration(store, hash, tree)
	if err != nil {
		return InstallResult{}, err
	}
	if err := store.SetCurrent(hash); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Generation: hash, Changed: created}, nil
}

// modRoot finds the dir holding addons/: dir itself, or its only @<name> child.
func modRoot(dir string) (string, error) {
	if isDir(filepath.Join(dir, "addons")) {
		return dir, nil
	}
	subs, _ := filepath.Glob(filepath.Join(dir, "@*"))
	if len(subs) == 1 && isDir(filepath.Join(subs[0], "addons")) {
		return subs[0], nil
	}
	return "", fmt.Errorf("product: no addons/ dir in %s (directly or in a single @<name> dir)", dir)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// copyRegular copies the regular files of src into dst, refusing anything
// else, so a host path cannot pull in files from outside the mod.
func copyRegular(src, dst string, keepKeys bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return os.MkdirAll(dst, 0o750)
		}
		if rel == "keys" && !keepKeys {
			return fs.SkipDir
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		case !d.Type().IsRegular():
			return fmt.Errorf("product: %s is not a regular file (links and special files are rejected)", p)
		}
		in, err := os.Open(p) //nolint:gosec // p comes from walking the operator's mod source
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		info, err := d.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode().Perm()) //nolint:gosec // inside our scratch dir
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

// hashTree is a sha256 over the sorted relative paths, permission bits and
// contents of root's regular files.
func hashTree(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%o\x00%d\x00", filepath.ToSlash(rel), info.Mode().Perm(), info.Size())
		f, err := os.Open(p) //nolint:gosec // inside our scratch dir
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(h, f)
		return err
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

// fetchAndUnpack downloads src.URL, verifies its sha256 before opening it,
// and unpacks the tar.gz or zip into dst.
func fetchAndUnpack(ctx context.Context, src LocalSource, dst string) error {
	if src.SHA256 == "" {
		return fmt.Errorf("product: %s: sha256 is required", src.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return err
	}
	client := src.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("product: fetch %s: %w", src.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("product: fetch %s: HTTP %d", src.URL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxUnpacked))
	if err != nil {
		return fmt.Errorf("product: fetch %s: %w", src.URL, err)
	}
	if sum := sha256.Sum256(data); !strings.EqualFold(hex.EncodeToString(sum[:]), src.SHA256) {
		return fmt.Errorf("product: %s: sha256 mismatch (got %x)", src.URL, sum)
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		return unzip(data, dst)
	}
	return untar(data, dst)
}

// safeJoin maps an archive member name below dst, rejecting absolute paths
// and "..".
func safeJoin(dst, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("product: archive member %q escapes the mod dir", name)
	}
	return filepath.Join(dst, clean), nil
}

func writeMember(path string, r io.Reader, mode fs.FileMode, budget *int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode.Perm()) //nolint:gosec // path passed safeJoin
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, *budget+1))
	*budget -= n
	if err == nil && *budget < 0 {
		err = fmt.Errorf("product: archive expands beyond %d bytes", int64(maxUnpacked))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func untar(data []byte, dst string) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("product: archive is neither zip nor tar.gz: %w", err)
	}
	budget := int64(maxUnpacked)
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("product: read archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		path, err := safeJoin(dst, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(path, 0o750)
		case tar.TypeReg:
			err = writeMember(path, tr, fs.FileMode(h.Mode), &budget) //nolint:gosec // G115: tar modes fit
		default:
			err = fmt.Errorf("product: archive member %q is a link or special file", h.Name)
		}
		if err != nil {
			return err
		}
	}
}

func unzip(data []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("product: read archive: %w", err)
	}
	budget := int64(maxUnpacked)
	for _, f := range zr.File {
		path, err := safeJoin(dst, f.Name)
		if err != nil {
			return err
		}
		switch {
		case f.FileInfo().IsDir():
			err = os.MkdirAll(path, 0o750)
		case f.Mode().IsRegular():
			var rc io.ReadCloser
			if rc, err = f.Open(); err == nil {
				err = writeMember(path, rc, f.Mode(), &budget)
				_ = rc.Close()
			}
		default:
			err = fmt.Errorf("product: archive member %q is a link or special file", f.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

// vendored is the web files compiled into dzo: htmx, Leaflet, Fork Awesome and the country flags. The
// subdirectories are named one by one; SHA256SUMS is for scripts/vendor-web.sh --update and is not served.
//
//go:embed vendor/htmx vendor/leaflet vendor/fork-awesome vendor/flags
var vendored embed.FS

var (
	// embeddedVendor is the tree the /vendor/ URLs serve, and embeddedFlags its flags subdirectory.
	embeddedVendor = subFS(vendored, "vendor")
	embeddedFlags  = subFS(embeddedVendor, "flags")
)

// subFS returns the tree below dir of f. The paths are part of the embed directive above, so the error
// cannot happen; falling back to the whole tree keeps the function free of a panic.
func subFS(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		return f
	}
	return sub
}

// vendorFS returns the vendored web files, overlaid by dir when it is not empty: a file that exists in
// dir wins, a file it lacks comes from the binary.
func vendorFS(dir string) fs.FS {
	return overlayFS{local: localFS(dir), embedded: embeddedVendor}
}

// flagsFS returns the vendored country flags, overlaid by dir when it is not empty, as vendorFS.
func flagsFS(dir string) fs.FS {
	return overlayFS{local: localFS(dir), embedded: embeddedFlags}
}

// localFS is the optional directory above the vendored files: os.DirFS resolves symlinks and paths the
// way the http.Dir of the old handler did, and a directory that does not exist reads as empty.
func localFS(dir string) fs.FS {
	if dir == "" {
		return nil
	}
	return os.DirFS(dir)
}

// overlayFS opens a file from local and, when local has no such file, from embedded.
type overlayFS struct {
	local    fs.FS // nil when no directory is configured
	embedded fs.FS
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if o.local != nil {
		if f, err := o.local.Open(name); err == nil {
			return f, nil
		}
	}
	return o.embedded.Open(name)
}

// VendorFile returns the content of name in the vendored web files, as /vendor/ serves them: the file
// dir holds wins, a file it lacks comes from the binary. name is a slash-separated path below the tree.
func VendorFile(dir, name string) ([]byte, error) {
	return fs.ReadFile(vendorFS(dir), name)
}

// vendorHandler serves the files below the root of fsys. It serves no directory: no listing, and no
// index.html either.
func vendorHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" || strings.HasSuffix(name, "/") {
			http.NotFound(w, r)
			return
		}
		fi, err := fs.Stat(fsys, name)
		if err != nil || fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

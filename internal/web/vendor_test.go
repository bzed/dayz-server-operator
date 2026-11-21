// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/apiclient"
)

// newAssetEnv starts the web interface with the options under test; the API is never reached by these
// requests.
func newAssetEnv(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	srv, err := New(&Backend{Name: "main", Client: apiclient.New("http://127.0.0.1:1", "test")}, opts)
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// fetch gets a URL and returns its status, headers and body.
func fetch(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // a test server on 127.0.0.1
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, string(b)
}

// vendored is the content of a file of the vendored tree as it is on disk.
func vendoredFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("vendor", filepath.FromSlash(name)))
	require.NoError(t, err)
	return string(b)
}

func TestVendorEmbeddedDefault(t *testing.T) {
	ts := newAssetEnv(t, Options{})
	for _, name := range []string{"htmx/htmx.min.js", "leaflet/leaflet.css", "leaflet/leaflet.js", "fork-awesome/css/fork-awesome.css"} {
		code, _, body := fetch(t, ts.URL+"/vendor/"+name)
		require.Equal(t, http.StatusOK, code, name)
		assert.Equal(t, vendoredFile(t, name), body, name)
	}
}

func TestVendorDirOverridesFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "htmx"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "htmx", "htmx.min.js"), []byte("local"), 0o600))
	ts := newAssetEnv(t, Options{Assets: dir})

	code, _, body := fetch(t, ts.URL+"/vendor/htmx/htmx.min.js")
	require.Equal(t, http.StatusOK, code, "the file of the directory")
	assert.Equal(t, "local", body, "the file of the directory wins")

	code, _, body = fetch(t, ts.URL+"/vendor/leaflet/leaflet.js")
	require.Equal(t, http.StatusOK, code, "the file the directory lacks")
	assert.Equal(t, vendoredFile(t, "leaflet/leaflet.js"), body, "a missing file comes from the binary")
}

func TestVendorMissingDirFallsBack(t *testing.T) {
	ts := newAssetEnv(t, Options{Assets: filepath.Join(t.TempDir(), "gone")})
	code, _, body := fetch(t, ts.URL+"/vendor/htmx/htmx.min.js")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, vendoredFile(t, "htmx/htmx.min.js"), body)
}

func TestVendorNoListing(t *testing.T) {
	ts := newAssetEnv(t, Options{})
	for _, path := range []string{"/vendor/", "/vendor/htmx/", "/vendor/htmx", "/vendor/SHA256SUMS"} {
		code, _, _ := fetch(t, ts.URL+path)
		assert.Equal(t, http.StatusNotFound, code, path)
	}
}

func TestFlagsEmbedded(t *testing.T) {
	ts := newAssetEnv(t, Options{})
	code, h, body := fetch(t, ts.URL+"/flags/de")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "image/svg+xml", h.Get("Content-Type"))
	assert.Equal(t, vendoredFile(t, "flags/de.svg"), body)
	assert.Contains(t, h.Get("Cache-Control"), "max-age")

	// The pages name the code as GeoIP gives it, upper case.
	code, _, _ = fetch(t, ts.URL+"/flags/DE.svg")
	assert.Equal(t, http.StatusOK, code, "upper case")

	for _, path := range []string{"/flags/xx", "/flags/d", "/flags/deu", "/flags/d1", "/flags/%2e%2e%2fa"} {
		code, _, _ := fetch(t, ts.URL+path)
		assert.Equal(t, http.StatusNotFound, code, path)
	}
	// The mux cleans a raw .. away before the handler sees it; either way no file outside flags is read.
	code, _, _ = fetch(t, ts.URL+"/flags/..")
	assert.NotEqual(t, http.StatusOK, code, "/flags/..")
}

// TestVendorFileEmbedded reads the built-in stylesheet without a directory: dzo site validate checks the
// admin-map icon names that way.
func TestVendorFileEmbedded(t *testing.T) {
	css, err := VendorFile("", "fork-awesome/css/fork-awesome.css")
	require.NoError(t, err)
	assert.Equal(t, vendoredFile(t, "fork-awesome/css/fork-awesome.css"), string(css), "the built-in stylesheet")
	assert.Contains(t, string(css), ".fa-car:before", "the built-in stylesheet names the icons")

	_, err = VendorFile("", "fork-awesome/css/nonexistent.css")
	assert.Error(t, err, "a file the tree does not have")
}

func TestVendorFileOverride(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "fork-awesome", "css"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fork-awesome", "css", "fork-awesome.css"), []byte(".fa-fire:before{}"), 0o600))

	css, err := VendorFile(dir, "fork-awesome/css/fork-awesome.css")
	require.NoError(t, err)
	assert.Equal(t, ".fa-fire:before{}", string(css), "the file of the directory wins")

	css, err = VendorFile(dir, "htmx/htmx.min.js")
	require.NoError(t, err)
	assert.Equal(t, vendoredFile(t, "htmx/htmx.min.js"), string(css), "a file the directory lacks comes from the binary")
}

func TestFlagsDirOverridesFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "de.svg"), []byte("<svg>local</svg>"), 0o600))
	ts := newAssetEnv(t, Options{FlagsDir: dir})

	code, _, body := fetch(t, ts.URL+"/flags/DE.svg")
	require.Equal(t, http.StatusOK, code, "the flag of the directory")
	assert.Equal(t, "<svg>local</svg>", body, "the flag of the directory wins")

	code, _, body = fetch(t, ts.URL+"/flags/fr.svg")
	require.Equal(t, http.StatusOK, code, "a flag the directory lacks")
	assert.Equal(t, vendoredFile(t, "flags/fr.svg"), body, "a missing flag comes from the binary")
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func localInstaller(t testing.TB) *Installer {
	t.Helper()
	return &Installer{CacheRoot: t.TempDir()}
}

func TestImportLocalDirIsContentHashed(t *testing.T) {
	in := localInstaller(t)
	src := t.TempDir()
	files := modFiles(true)
	files["keys/x.bikey"] = []byte("ignored")
	writeTree(t, src, files)
	ctx := t.Context()

	r1, err := in.ImportLocal(ctx, "tools", LocalSource{Dir: src})
	require.NoError(t, err)
	require.True(t, r1.Changed)
	require.Len(t, r1.Generation, 64)
	store := LocalModStore(in.CacheRoot, "tools")
	_, err = os.Stat(filepath.Join(store.Root, r1.Generation, "keys"))
	assert.True(t, os.IsNotExist(err), "keys/ must be ignored")
	cur, _ := store.Current()
	assert.Equal(t, r1.Generation, cur)

	// same content: same generation, nothing new
	r2, err := in.ImportLocal(ctx, "tools", LocalSource{Dir: src})
	require.NoError(t, err)
	require.False(t, r2.Changed)
	require.Equal(t, r1.Generation, r2.Generation)
	// a change in keys/ does not matter either (ignored)
	writeTree(t, src, map[string][]byte{"keys/y.bikey": []byte("more")})
	r, _ := in.ImportLocal(ctx, "tools", LocalSource{Dir: src})
	assert.Equal(t, r1.Generation, r.Generation, "ignored files must not change the hash")
	// changed content: a new generation, the old one is kept
	writeTree(t, src, map[string][]byte{"addons/b.pbo": buildPBO("dzo/b")})
	r3, err := in.ImportLocal(ctx, "tools", LocalSource{Dir: src})
	require.NoError(t, err)
	require.True(t, r3.Changed)
	require.NotEqual(t, r1.Generation, r3.Generation)
	gens, _ := store.Generations()
	assert.Len(t, gens, 2)
}

func TestImportLocalModRootInAtDir(t *testing.T) {
	in := localInstaller(t)
	src := t.TempDir()
	files := map[string][]byte{}
	for k, v := range modFiles(true) {
		files["@Tools/"+k] = v
	}
	writeTree(t, src, files)
	_, err := in.ImportLocal(t.Context(), "tools", LocalSource{Dir: src})
	require.NoError(t, err, "import from @Tools/")
}

func TestImportLocalRejects(t *testing.T) {
	ctx := t.Context()
	t.Run("no addons", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, map[string][]byte{"readme": nil})
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: src})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no addons/")
	})
	t.Run("symlink", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, modFiles(true))
		err := os.Symlink("/etc/passwd", filepath.Join(src, "addons", "link.pbo"))
		require.NoError(t, err)
		_, err = localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: src})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	})
	t.Run("no prefix", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, map[string][]byte{"addons/a.pbo": buildPBO("")})
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: src})
		require.Error(t, err, "a PBO without prefix must be rejected")
	})
	t.Run("missing dir", func(t *testing.T) {
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: "/nonexistent"})
		require.Error(t, err)
	})
}

func tarGz(t testing.TB, members []tar.Header, data map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range members {
		h := h
		h.Size = int64(len(data[h.Name]))
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		err := tw.WriteHeader(&h)
		require.NoError(t, err)
		_, err = tw.Write(data[h.Name])
		require.NoError(t, err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sumOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func serve(t testing.TB, body []byte, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportLocalArchives(t *testing.T) {
	ctx := t.Context()
	pbo := buildPBO("dzo/t")
	good := tarGz(t, []tar.Header{{Name: "@Tools/", Typeflag: tar.TypeDir}, {Name: "@Tools/addons/t.pbo"}}, map[string][]byte{"@Tools/addons/t.pbo": pbo})

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("addons/t.pbo")
	_, _ = w.Write(pbo)
	_ = zw.Close()

	t.Run("tar.gz", func(t *testing.T) {
		srv := serve(t, good, 200)
		r, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(good)})
		require.NoError(t, err)
		require.True(t, r.Changed)
	})
	t.Run("zip gives the same generation as a dir with the same files", func(t *testing.T) {
		srv := serve(t, zbuf.Bytes(), 200)
		in := localInstaller(t)
		r, err := in.ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: strings.ToUpper(sumOf(zbuf.Bytes()))})
		require.NoError(t, err)
		dir := t.TempDir()
		writeTree(t, dir, map[string][]byte{"addons/t.pbo": pbo})
		r2, err := in.ImportLocal(ctx, "t", LocalSource{Dir: dir})
		require.NoError(t, err, "dir import = %+v, %v; want the zip's generation %s", r2, err, r.Generation)
		require.False(t, r2.Changed, "dir import = %+v, %v; want the zip's generation %s", r2, err, r.Generation)
		assert.Equal(t, r.Generation, r2.Generation, "dir import = %+v, %v; want the zip's generation %s", r2, err, r.Generation)
	})
	t.Run("wrong sha256", func(t *testing.T) {
		srv := serve(t, good, 200)
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf([]byte("x"))})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sha256 mismatch")
	})
	t.Run("sha256 required", func(t *testing.T) {
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: "http://x.invalid/a.tgz"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sha256 is required")
	})
	t.Run("http error", func(t *testing.T) {
		srv := serve(t, nil, 404)
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: "00"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 404")
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := serve(t, nil, 200)
		url := srv.URL
		srv.Close()
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: url, SHA256: "00"})
		require.Error(t, err)
	})
	t.Run("not an archive", func(t *testing.T) {
		body := []byte("just text")
		srv := serve(t, body, 200)
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(body)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "neither zip nor tar.gz")
	})
	t.Run("corrupt zip", func(t *testing.T) {
		body := []byte("PKgarbage")
		srv := serve(t, body, 200)
		_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(body)})
		require.Error(t, err)
	})
}

func TestUnpackRejectsUnsafeMembers(t *testing.T) {
	ctx := t.Context()
	for name, arc := range map[string][]byte{
		"parent traversal": tarGz(t, []tar.Header{{Name: "../evil"}}, map[string][]byte{"../evil": []byte("x")}),
		"absolute path":    tarGz(t, []tar.Header{{Name: "/etc/evil"}}, map[string][]byte{"/etc/evil": []byte("x")}),
		"symlink":          tarGz(t, []tar.Header{{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, nil),
		"hard link":        tarGz(t, []tar.Header{{Name: "l", Typeflag: tar.TypeLink, Linkname: "x"}}, nil),
		"device":           tarGz(t, []tar.Header{{Name: "d", Typeflag: tar.TypeChar}}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			srv := serve(t, arc, 200)
			_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(arc)})
			require.Error(t, err)
		})
	}
	t.Run("zip traversal and symlink", func(t *testing.T) {
		for _, mk := range []func(zw *zip.Writer){
			func(zw *zip.Writer) { w, _ := zw.Create("../evil"); _, _ = w.Write([]byte("x")) },
			func(zw *zip.Writer) {
				h := &zip.FileHeader{Name: "l"}
				h.SetMode(os.ModeSymlink | 0o777)
				w, _ := zw.CreateHeader(h)
				_, _ = w.Write([]byte("/etc/passwd"))
			},
		} {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			mk(zw)
			_ = zw.Close()
			srv := serve(t, buf.Bytes(), 200)
			_, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(buf.Bytes())})
			require.Error(t, err)
		}
	})
}

func TestSafeJoin(t *testing.T) {
	p, err := safeJoin("/d", "./a/b")
	require.NoError(t, err)
	assert.Equal(t, "/d/a/b", p)
	for _, bad := range []string{"..", "../x", "a/../../x", "/abs"} {
		_, err := safeJoin("/d", bad)
		require.Error(t, err, "safeJoin(%q) must fail", bad)
	}
}

func TestImportLocalRunsTheCheckBeforeImporting(t *testing.T) {
	in := localInstaller(t)
	src := t.TempDir()
	writeTree(t, src, modFiles(true))
	var got string
	_, err := in.ImportLocal(t.Context(), "tools", LocalSource{Dir: src, Check: func(dir string) error {
		got = dir
		return os.ErrPermission
	}})
	require.Error(t, err, "a failing check must stop the import: err=%v dir=%q", err, got)
	require.Equal(t, src, got, "a failing check must stop the import: err=%v dir=%q", err, got)
	_, err = os.Stat(LocalModStore(in.CacheRoot, "tools").Root)
	assert.True(t, os.IsNotExist(err), "nothing may be stored when the check fails")
	_, err = in.ImportLocal(t.Context(), "tools", LocalSource{Dir: src, Check: func(string) error { return nil }})
	require.NoError(t, err)
}

// signedModFiles is a mod that ships its public key and a signature per PBO.
func signedModFiles() map[string][]byte {
	f := modFiles(true)
	f["keys/Dbg.bikey"] = []byte("public key")
	f["addons/a.pbo.Dbg.bisign"] = []byte("signature")
	return f
}

func TestImportLocalSignedKeepsKeys(t *testing.T) {
	in := localInstaller(t)
	src := t.TempDir()
	writeTree(t, src, signedModFiles())
	r, err := in.ImportLocal(t.Context(), "dbg", LocalSource{Dir: src, Signed: true})
	require.NoError(t, err, "import")
	store := LocalModStore(in.CacheRoot, "dbg")
	_, err = os.Stat(filepath.Join(store.Root, r.Generation, "keys", "Dbg.bikey"))
	require.NoError(t, err, "a debug client mod keeps keys/")
	// the same files as a servermod: keys/ is dropped, a different generation
	r2, err := in.ImportLocal(t.Context(), "dbg2", LocalSource{Dir: src})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(LocalModStore(in.CacheRoot, "dbg2").Root, r2.Generation, "keys"))
	assert.True(t, os.IsNotExist(err), "a servermod must not keep keys/")
}

func TestValidateSigned(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string][]byte)
		wantErr string
	}{
		{"signed", func(map[string][]byte) {}, ""},
		{"authority in other case", func(f map[string][]byte) {
			delete(f, "addons/a.pbo.Dbg.bisign")
			f["addons/A.PBO.DBG.BISIGN"] = []byte("s")
			f["addons/a.pbo"], f["addons/A.PBO"] = nil, f["addons/a.pbo"]
			delete(f, "addons/a.pbo")
		}, ""},
		{"no key", func(f map[string][]byte) { delete(f, "keys/Dbg.bikey") }, "needs its public key"},
		{"no signature", func(f map[string][]byte) { delete(f, "addons/a.pbo.Dbg.bisign") }, "bisign"},
		{"signature of another key", func(f map[string][]byte) {
			delete(f, "addons/a.pbo.Dbg.bisign")
			f["addons/a.pbo.Other.bisign"] = []byte("s")
		}, "bisign"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := signedModFiles()
			c.mutate(files)
			writeTree(t, dir, files)
			err := ValidateSigned(dir)
			if c.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, c.wantErr)
			}
		})
	}
}

func TestImportLocalSignedRejectsAnUnsignedMod(t *testing.T) {
	src := t.TempDir()
	files := modFiles(true)
	files["keys/Dbg.bikey"] = []byte("k")
	writeTree(t, src, files)
	_, err := localInstaller(t).ImportLocal(t.Context(), "dbg", LocalSource{Dir: src, Signed: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bisign")
}

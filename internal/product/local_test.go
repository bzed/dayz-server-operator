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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localInstaller(t *testing.T) *Installer {
	t.Helper()
	return &Installer{CacheRoot: t.TempDir()}
}

func TestImportLocalDirIsContentHashed(t *testing.T) {
	in := localInstaller(t)
	src := t.TempDir()
	files := modFiles(true)
	files["keys/x.bikey"] = []byte("ignored")
	writeTree(t, src, files)
	ctx := context.Background()

	r1, err := in.ImportLocal(ctx, "tools", LocalSource{Dir: src})
	if err != nil || !r1.Changed || len(r1.Generation) != 64 {
		t.Fatalf("first import = %+v, %v", r1, err)
	}
	store := LocalModStore(in.CacheRoot, "tools")
	if _, err := os.Stat(filepath.Join(store.Root, r1.Generation, "keys")); !os.IsNotExist(err) {
		t.Error("keys/ must be ignored")
	}
	if cur, _ := store.Current(); cur != r1.Generation {
		t.Errorf("current = %q", cur)
	}

	// same content: same generation, nothing new
	r2, err := in.ImportLocal(ctx, "tools", LocalSource{Dir: src})
	if err != nil || r2.Changed || r2.Generation != r1.Generation {
		t.Fatalf("re-import = %+v, %v", r2, err)
	}
	// a change in keys/ does not matter either (ignored)
	writeTree(t, src, map[string][]byte{"keys/y.bikey": []byte("more")})
	if r, _ := in.ImportLocal(ctx, "tools", LocalSource{Dir: src}); r.Generation != r1.Generation {
		t.Error("ignored files must not change the hash")
	}
	// changed content: a new generation, the old one is kept
	writeTree(t, src, map[string][]byte{"addons/b.pbo": buildPBO("dzo/b")})
	r3, err := in.ImportLocal(ctx, "tools", LocalSource{Dir: src})
	if err != nil || !r3.Changed || r3.Generation == r1.Generation {
		t.Fatalf("changed import = %+v, %v", r3, err)
	}
	if gens, _ := store.Generations(); len(gens) != 2 {
		t.Errorf("generations = %v", gens)
	}
}

func TestImportLocalModRootInAtDir(t *testing.T) {
	in := localInstaller(t)
	src := t.TempDir()
	files := map[string][]byte{}
	for k, v := range modFiles(true) {
		files["@Tools/"+k] = v
	}
	writeTree(t, src, files)
	if _, err := in.ImportLocal(context.Background(), "tools", LocalSource{Dir: src}); err != nil {
		t.Fatalf("import from @Tools/: %v", err)
	}
}

func TestImportLocalRejects(t *testing.T) {
	ctx := context.Background()
	t.Run("no addons", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, map[string][]byte{"readme": nil})
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: src}); err == nil || !strings.Contains(err.Error(), "no addons/") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, modFiles(true))
		if err := os.Symlink("/etc/passwd", filepath.Join(src, "addons", "link.pbo")); err != nil {
			t.Fatal(err)
		}
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: src}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no prefix", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, map[string][]byte{"addons/a.pbo": buildPBO("")})
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: src}); err == nil {
			t.Error("a PBO without prefix must be rejected")
		}
	})
	t.Run("missing dir", func(t *testing.T) {
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{Dir: "/nonexistent"}); err == nil {
			t.Error("want an error")
		}
	})
}

func tarGz(t *testing.T, members []tar.Header, data map[string][]byte) []byte {
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
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data[h.Name]); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sumOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func serve(t *testing.T, body []byte, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportLocalArchives(t *testing.T) {
	ctx := context.Background()
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
		if err != nil || !r.Changed {
			t.Fatalf("import = %+v, %v", r, err)
		}
	})
	t.Run("zip gives the same generation as a dir with the same files", func(t *testing.T) {
		srv := serve(t, zbuf.Bytes(), 200)
		in := localInstaller(t)
		r, err := in.ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: strings.ToUpper(sumOf(zbuf.Bytes()))})
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		writeTree(t, dir, map[string][]byte{"addons/t.pbo": pbo})
		if r2, err := in.ImportLocal(ctx, "t", LocalSource{Dir: dir}); err != nil || r2.Changed || r2.Generation != r.Generation {
			t.Errorf("dir import = %+v, %v; want the zip's generation %s", r2, err, r.Generation)
		}
	})
	t.Run("wrong sha256", func(t *testing.T) {
		srv := serve(t, good, 200)
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf([]byte("x"))}); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("sha256 required", func(t *testing.T) {
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: "http://x.invalid/a.tgz"}); err == nil || !strings.Contains(err.Error(), "sha256 is required") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("http error", func(t *testing.T) {
		srv := serve(t, nil, 404)
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: "00"}); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := serve(t, nil, 200)
		url := srv.URL
		srv.Close()
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: url, SHA256: "00"}); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("not an archive", func(t *testing.T) {
		body := []byte("just text")
		srv := serve(t, body, 200)
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(body)}); err == nil || !strings.Contains(err.Error(), "neither zip nor tar.gz") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("corrupt zip", func(t *testing.T) {
		body := []byte("PKgarbage")
		srv := serve(t, body, 200)
		if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(body)}); err == nil {
			t.Error("want an error")
		}
	})
}

func TestUnpackRejectsUnsafeMembers(t *testing.T) {
	ctx := context.Background()
	for name, arc := range map[string][]byte{
		"parent traversal": tarGz(t, []tar.Header{{Name: "../evil"}}, map[string][]byte{"../evil": []byte("x")}),
		"absolute path":    tarGz(t, []tar.Header{{Name: "/etc/evil"}}, map[string][]byte{"/etc/evil": []byte("x")}),
		"symlink":          tarGz(t, []tar.Header{{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, nil),
		"hard link":        tarGz(t, []tar.Header{{Name: "l", Typeflag: tar.TypeLink, Linkname: "x"}}, nil),
		"device":           tarGz(t, []tar.Header{{Name: "d", Typeflag: tar.TypeChar}}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			srv := serve(t, arc, 200)
			if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(arc)}); err == nil {
				t.Fatal("want an error")
			}
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
			if _, err := localInstaller(t).ImportLocal(ctx, "t", LocalSource{URL: srv.URL, SHA256: sumOf(buf.Bytes())}); err == nil {
				t.Fatal("want an error")
			}
		}
	})
}

func TestSafeJoin(t *testing.T) {
	if p, err := safeJoin("/d", "./a/b"); err != nil || p != "/d/a/b" {
		t.Errorf("safeJoin = %q, %v", p, err)
	}
	for _, bad := range []string{"..", "../x", "a/../../x", "/abs"} {
		if _, err := safeJoin("/d", bad); err == nil {
			t.Errorf("safeJoin(%q) must fail", bad)
		}
	}
}

func TestImportLocalRunsTheCheckBeforeImporting(t *testing.T) {
	in := localInstaller(t)
	src := t.TempDir()
	writeTree(t, src, modFiles(true))
	var got string
	_, err := in.ImportLocal(context.Background(), "tools", LocalSource{Dir: src, Check: func(dir string) error {
		got = dir
		return os.ErrPermission
	}})
	if err == nil || got != src {
		t.Fatalf("a failing check must stop the import: err=%v dir=%q", err, got)
	}
	if _, err := os.Stat(LocalModStore(in.CacheRoot, "tools").Root); !os.IsNotExist(err) {
		t.Error("nothing may be stored when the check fails")
	}
	if _, err := in.ImportLocal(context.Background(), "tools", LocalSource{Dir: src, Check: func(string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
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
	r, err := in.ImportLocal(context.Background(), "dbg", LocalSource{Dir: src, Signed: true})
	if err != nil {
		t.Fatalf("import = %v", err)
	}
	store := LocalModStore(in.CacheRoot, "dbg")
	if _, err := os.Stat(filepath.Join(store.Root, r.Generation, "keys", "Dbg.bikey")); err != nil {
		t.Errorf("a debug client mod keeps keys/: %v", err)
	}
	// the same files as a servermod: keys/ is dropped, a different generation
	r2, err := in.ImportLocal(context.Background(), "dbg2", LocalSource{Dir: src})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(LocalModStore(in.CacheRoot, "dbg2").Root, r2.Generation, "keys")); !os.IsNotExist(err) {
		t.Error("a servermod must not keep keys/")
	}
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
			if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("ValidateSigned = %v, want %q", err, c.wantErr)
			}
		})
	}
}

func TestImportLocalSignedRejectsAnUnsignedMod(t *testing.T) {
	src := t.TempDir()
	files := modFiles(true)
	files["keys/Dbg.bikey"] = []byte("k")
	writeTree(t, src, files)
	if _, err := localInstaller(t).ImportLocal(context.Background(), "dbg", LocalSource{Dir: src, Signed: true}); err == nil || !strings.Contains(err.Error(), "bisign") {
		t.Errorf("err = %v", err)
	}
}

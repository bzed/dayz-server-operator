// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servermods

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/moddeps"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/stretchr/testify/require"
)

func write(t testing.TB, path, body string) {
	t.Helper()
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(path, []byte(body), 0o600)
	require.NoError(t, err)
}

// fixture is a servermods/ tree with one mod holding one addon.
func fixture(t testing.TB) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "servermods")
	src := filepath.Join(root, "demo", "src", "Demo")
	write(t, filepath.Join(src, PrefixFile), "Demo\n")
	write(t, filepath.Join(src, "config.cpp"), "class CfgPatches {};\n")
	write(t, filepath.Join(src, "scripts", "3_Game", "Demo", "A.c"), "class A {}\n")
	write(t, filepath.Join(src, "scripts", "5_Mission", "Demo", "B.c"), "class B {}\n")
	return root
}

func TestBuildProducesAValidLocalMod(t *testing.T) {
	root := fixture(t)
	out := filepath.Join(t.TempDir(), "out")
	res, err := Build(t.Context(), root, out)
	require.NoError(t, err, "Build")
	require.Len(t, res, 1)
	r := res[0]
	require.Equal(t, "demo", r.Mod)
	require.Equal(t, "Demo", r.Addon)
	require.Equal(t, "Demo", r.Prefix)
	require.Len(t, r.SHA256, 64)
	require.Equal(t, "demo.pbo", filepath.Base(r.PBO))
	// What dzo mod add would accept: addons/*.pbo with a prefix header.
	err = product.ValidateMod(filepath.Join(out, "demo"), true)
	require.NoError(t, err, "ValidateMod")
	b, _ := os.ReadFile(filepath.Join(out, "demo", "meta.cpp"))
	require.Contains(t, string(b), "publishedid = 0;")
	data, _ := os.ReadFile(r.PBO)
	p, err := moddeps.OpenPBO(data)
	require.NoError(t, err)
	var names []string
	for _, e := range p.Entries {
		names = append(names, strings.ReplaceAll(e.Name, `\`, "/"))
	}
	want := []string{"config.cpp", "scripts/3_Game/Demo/A.c", "scripts/5_Mission/Demo/B.c"}
	require.Equal(t, strings.Join(want, ","), strings.Join(names, ","))
}

func TestBuildIsReproducible(t *testing.T) {
	root := fixture(t)
	a, err := Build(t.Context(), root, filepath.Join(t.TempDir(), "a"))
	require.NoError(t, err)
	// A later mtime on a source file must not change the output.
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(root, "demo", "src", "Demo", "config.cpp"), later, later)
	b, err := Build(t.Context(), root, filepath.Join(t.TempDir(), "b"))
	require.NoError(t, err)
	require.Equal(t, b[0].SHA256, a[0].SHA256)
	// And a content change must.
	write(t, filepath.Join(root, "demo", "src", "Demo", "scripts", "3_Game", "Demo", "A.c"), "class A2 {}\n")
	c, _ := Build(t.Context(), root, filepath.Join(t.TempDir(), "c"))
	require.NotEqual(t, a[0].SHA256, c[0].SHA256)
}

func TestBuildErrors(t *testing.T) {
	ctx := t.Context()
	out := t.TempDir()
	_, err := Build(ctx, filepath.Join(t.TempDir(), "none"), out)
	require.ErrorContains(t, err, "no servermods")
	empty := t.TempDir()
	err = os.MkdirAll(filepath.Join(empty, "dzo-admin"), 0o750)
	require.NoError(t, err)
	_, err = Build(ctx, empty, out)
	require.ErrorContains(t, err, "submodule")
	root := fixture(t)
	err = os.Remove(filepath.Join(root, "demo", "src", "Demo", PrefixFile))
	require.NoError(t, err)
	_, err = Build(ctx, root, out)
	require.Error(t, err, "missing prefix file")
	write(t, filepath.Join(root, "demo", "src", "Demo", PrefixFile), "  \n")
	_, err = Build(ctx, root, out)
	require.ErrorContains(t, err, "empty")
	only := t.TempDir()
	write(t, filepath.Join(only, PrefixFile), "X")
	_, _, err = Pack(ctx, only, filepath.Join(out, "x.pbo"))
	require.ErrorContains(t, err, "no files")
	link := fixture(t)
	err = os.Symlink("/etc/passwd", filepath.Join(link, "demo", "src", "Demo", "evil.c"))
	require.NoError(t, err)
	_, err = Build(ctx, link, out)
	require.ErrorContains(t, err, "regular")
}

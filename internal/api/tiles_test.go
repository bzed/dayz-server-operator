// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTiles(t *testing.T) {
	e := newEnv(t)
	root := t.TempDir()
	set := filepath.Join(root, "m", "abcdef0123456789")
	err := os.MkdirAll(filepath.Join(set, "0", "0"), 0o750)
	require.NoError(t, err)
	_ = os.WriteFile(filepath.Join(set, "metadata.json"), []byte(`{"map":"m"}`), 0o600)
	_ = os.WriteFile(filepath.Join(set, "0", "0", "0.jpg"), []byte("JPEG"), 0o600)
	err = os.Symlink("abcdef0123456789", filepath.Join(root, "m", "current"))
	require.NoError(t, err)
	tok := e.token(RoleViewer, nil, false)

	code, _ := e.do("GET", "/api/v1/tiles/m/metadata.json", tok, "")
	require.Equal(t, 404, code)
	e.srv.TilesDir = root
	code, _ = e.do("GET", "/api/v1/tiles/m/metadata.json", "", "")
	require.Equal(t, 401, code)
	code, b := e.do("GET", "/api/v1/tiles/m/metadata.json", tok, "")
	require.Equal(t, 200, code, "metadata: %d %s", code, b)
	require.Contains(t, string(b), `"map":"m"`, "metadata: %d %s", code, b)
	code, b = e.do("GET", "/api/v1/tiles/m/abcdef0123456789/0/0/0.jpg", tok, "")
	require.Equal(t, 200, code, "tile: %d %s", code, b)
	require.Equal(t, "JPEG", string(b), "tile: %d %s", code, b)
	for _, p := range []string{
		"/api/v1/tiles/m/abcdef0123456789/0/0/1.jpg",    // not built
		"/api/v1/tiles/other/metadata.json",             // unknown map
		"/api/v1/tiles/m/current/0/0/0.jpg",             // only the hash is a valid tile set name
		"/api/v1/tiles/m/abcdef0123456789/0/0/0.png",    // wrong extension
		"/api/v1/tiles/m/abcdef0123456789/0/0/../0.jpg", // traversal
		"/api/v1/tiles/M/metadata.json",                 // map names are lower case
		"/api/v1/tiles/m/metadata.json/x",
	} {
		code, _ := e.do("GET", p, tok, "")
		assert.Equal(t, 404, code)
	}
}

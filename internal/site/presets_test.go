// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The presets the package ships must load as a site's integrations/maps.
func TestShippedMapPresetsLoad(t *testing.T) {
	site := t.TempDir()
	dst := filepath.Join(site, "integrations", "maps")
	err := os.MkdirAll(dst, 0o750)
	require.NoError(t, err)
	files, _ := filepath.Glob("../../contrib/presets/maps/*.yaml")
	require.Len(t, files, 3)
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // test fixture
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dst, filepath.Base(f)), b, 0o600)
		require.NoError(t, err)
	}
	tree, err := LoadTree(site)
	require.NoError(t, err)
	p, ok := tree.Maps["vanilla-enoch"]
	require.True(t, ok, "presets = %+v", tree.Maps)
	require.NotEmpty(t, p.Git, "presets = %+v", tree.Maps)
	require.NotEmpty(t, p.Ref, "presets = %+v", tree.Maps)
	require.Equal(t, "dayzOffline.enoch", p.Path, "presets = %+v", tree.Maps)
}

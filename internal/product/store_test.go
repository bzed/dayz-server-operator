// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductStoreRoot(t *testing.T) {
	s := ProductStore("/var/lib/dzo/cache", "dayz-stable")
	want := filepath.Join("/var/lib/dzo/cache", "products", "dayz-stable")
	assert.Equal(t, want, s.Root)
}

func TestModStoreRoot(t *testing.T) {
	s := ModStore("/var/lib/dzo/cache", 221100, 111111)
	want := filepath.Join("/var/lib/dzo/cache", "workshop", "221100", "111111")
	assert.Equal(t, want, s.Root)
}

func TestModGenerationID(t *testing.T) {
	cases := []struct {
		timeUpdated int64
		retry       int
		want        string
	}{
		{1700000000, 0, "1700000000"},
		{1700000000, 1, "1700000000-r1"},
		{1700000000, 2, "1700000000-r2"},
	}
	for _, c := range cases {
		got := ModGenerationID(c.timeUpdated, c.retry)
		assert.Equal(t, c.want, got)
	}
}

func TestStoreIntegrationWithCache(t *testing.T) {
	root := t.TempDir()
	s := ModStore(root, 221100, 111111)
	err := s.EnsureRoot()
	require.NoError(t, err, "EnsureRoot")
	id := ModGenerationID(1700000000, 0)
	genDir, err := s.NewGeneration(id)
	require.NoError(t, err, "NewGeneration")
	err = s.SetCurrent(id)
	require.NoError(t, err, "SetCurrent")
	current, err := s.Current()
	require.NoError(t, err, "Current")
	assert.Equal(t, id, current)
	assert.NotEmpty(t, genDir, "NewGeneration returned an empty path")
}

func TestLocalModStore(t *testing.T) {
	got, want := LocalModStore("/c", "tools").Root, "/c/local/tools"
	assert.Equal(t, want, got)
}

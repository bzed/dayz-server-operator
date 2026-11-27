// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQuantity(t *testing.T) {
	for in, want := range map[string]float64{
		"": QuantityDefault, "default": QuantityDefault, " Default ": QuantityDefault, "-1": QuantityDefault,
		"max": QuantityMax, "MAX": QuantityMax, "-2": QuantityMax,
		"0": 0, "5": 5, " 2.5 ": 2.5,
	} {
		got, err := ParseQuantity(in)
		require.NoError(t, err)
		assert.InDelta(t, want, got, 1e-9)
	}
	for _, in := range []string{"lots", "-3", "-0.5", "NaN", "Inf", "1e999"} {
		_, err := ParseQuantity(in)
		assert.Error(t, err, "ParseQuantity(%q) did not fail", in)
	}
}

func TestTypeListRefusesBadChunks(t *testing.T) {
	cases := map[string]TypesChunk{
		"total zero":      {Hash: "h", Total: 0, Names: []string{"A"}},
		"negative total":  {Hash: "h", Total: -1, Names: []string{"A"}},
		"huge total":      {Hash: "h", Total: 1 << 40, Names: []string{"A"}},
		"long hash":       {Hash: strings.Repeat("x", 1000), Total: 1, Names: []string{"A"}},
		"more than total": {Hash: "h", Total: 1, Names: []string{"A", "B"}},
		"bad name":        {Hash: "h", Total: 2, Names: []string{"A", "not a class"}},
		"long name":       {Hash: "h", Total: 1, Names: []string{strings.Repeat("a", 129)}},
	}
	for name, c := range cases {
		var l typeList
		require.True(t, l.add(TypesChunk{Hash: "h", Total: 4, Names: []string{"A"}}), name)
		require.False(t, l.add(c), name)
		require.Empty(t, l.names, name)
		require.False(t, l.complete(), name)
	}

	// The list stays bounded when a token holder keeps sending chunks of a huge list.
	var l typeList
	for i := 0; i < 50; i++ {
		l.add(TypesChunk{Hash: "h", Offset: len(l.names), Total: 1 << 40, Names: []string{"A", "B"}})
	}
	require.Empty(t, l.names)
}

func TestHubResendsAfterBadTypesChunk(t *testing.T) {
	h := NewHub("a")
	r := h.Sync(SyncRequest{Proto: 1, Types: &TypesChunk{Hash: "h", Total: 1 << 40, Names: []string{"A"}}})
	require.True(t, r.ResendTypes)
	require.Zero(t, h.Status().Types)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchGlobDoubleStarSuffix(t *testing.T) {
	cases := map[string]bool{
		"expansion":            true,
		"expansion/traders":    true,
		"expansion/a/b/c.json": true,
		"expansions/traders":   false,
		"other/expansion/x":    false,
	}
	for path, want := range cases {
		got := MatchGlob("expansion/**", path)
		assert.Equal(t, want, got)
	}
}

func TestMatchGlobSingleSegmentWildcard(t *testing.T) {
	assert.True(t, MatchGlob("custom_*/keys/*.bikey", "custom_loadout/keys/x.bikey"), "expected a match")
	assert.False(t, MatchGlob("custom_*/keys/*.bikey", "custom_loadout/sub/keys/x.bikey"), "single * must not cross a path separator")
}

func TestMatchGlobExact(t *testing.T) {
	assert.True(t, MatchGlob("db/messages.xml", "db/messages.xml"), "expected an exact match")
	assert.False(t, MatchGlob("db/messages.xml", "db/other.xml"), "expected no match")
}

func TestMatchAny(t *testing.T) {
	patterns := []string{"expansion/**", "*.bak"}
	assert.True(t, MatchAny(patterns, "expansion/x"), "expected a match on the first pattern")
	assert.True(t, MatchAny(patterns, "foo.bak"), "expected a match on the second pattern")
	assert.False(t, MatchAny(patterns, "foo.xml"), "expected no match")
}

func TestMatchAnyEmpty(t *testing.T) {
	assert.False(t, MatchAny(nil, "anything"), "no patterns should never match")
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveGUIDMatchesTheLiveCapture(t *testing.T) {
	b, err := os.ReadFile("testdata/v2/pair.txt")
	require.NoError(t, err)
	f := strings.Fields(string(b))
	require.Len(t, f, 2)
	got, err := DeriveGUID(f[0])
	require.NoError(t, err, "DeriveGUID(%s) = %q, %v; the server said %s", f[0], got, err, f[1])
	require.Equal(t, f[1], got, "DeriveGUID(%s) = %q, %v; the server said %s", f[0], got, err, f[1])
}

func TestDeriveGUIDRefusesBadIDs(t *testing.T) {
	for _, id := range []string{"", "123", "76561198000000001x", "86561198000000001", "765611980000000011"} {
		_, err := DeriveGUID(id)
		require.Error(t, err, "DeriveGUID(%q) accepted", id)
	}
}

func TestValidators(t *testing.T) {
	require.True(t, ValidSteamID("76561198000000001"), "ValidSteamID")
	assert.False(t, ValidSteamID("7656119800000000"), "ValidSteamID")
	require.True(t, ValidGUID("907725ff524e1026154b5ac3cccb0258"), "ValidGUID: lowercase hex, 32 digits")
	require.False(t, ValidGUID("907725FF524E1026154B5AC3CCCB0258"), "ValidGUID: lowercase hex, 32 digits")
	assert.False(t, ValidGUID("907725ff"), "ValidGUID: lowercase hex, 32 digits")
	for in, want := range map[string]string{"192.0.2.7": "192.0.2.7", " 10.0.0.1 ": "10.0.0.1", "::ffff:192.0.2.7": "192.0.2.7"} {
		got, err := ParseIP(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	for _, in := range []string{"", "0.0.0.0", "2001:db8::7", "192.0.2.7:2304", "10.0.0.0/8", "example.org"} {
		_, err := ParseIP(in)
		require.Error(t, err, "ParseIP(%q) accepted", in)
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlayersOfTheLiveCapture(t *testing.T) {
	b, err := os.ReadFile("../moderation/testdata/v2/players.out")
	require.NoError(t, err)
	pair, err := os.ReadFile("../moderation/testdata/v2/pair.txt")
	require.NoError(t, err)
	guid := strings.Fields(string(pair))[1]
	ps := ParsePlayers(string(b))
	require.Len(t, ps, 1)
	require.Equal(t, "dzotester", ps[0].Name)
	require.Equal(t, guid, ps[0].GUID)
	require.NotEmpty(t, ps[0].IP)
	require.NotEqual(t, 0, ps[0].Port)
}

func TestParsePlayersVariants(t *testing.T) {
	raw := "Players on server:\n[#] [IP Address]:[Port] [Ping] [GUID] [Name]\n--------------------------------------------------\n" +
		"0   10.0.0.3:2304         15   0123456789abcdef0123456789abcdef(OK) alice (Lobby)\n" +
		"12  192.0.2.7:41939       -1   fedcba9876543210fedcba9876543210(?) bob with spaces\n" +
		"3   garbage line\n(2 players in total)\n"
	ps := ParsePlayers(raw)
	require.Len(t, ps, 2)
	p := ps[0]
	require.Equal(t, 0, p.ID)
	require.Equal(t, "10.0.0.3", p.IP)
	require.Equal(t, 2304, p.Port)
	require.True(t, p.Verified, "alice %+v", p)
	assert.Equal(t, "alice", p.Name)
	p = ps[1]
	require.Equal(t, 12, p.ID)
	require.Equal(t, -1, p.Ping)
	require.False(t, p.Verified, "bob %+v", p)
	assert.Equal(t, "bob with spaces", p.Name)
}

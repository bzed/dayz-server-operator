// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package a2s

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfoRequestNoChallenge(t *testing.T) {
	req := InfoRequest(nil)
	require.True(t, bytes.HasPrefix(req, []byte{0xFF, 0xFF, 0xFF, 0xFF, 'T'}), "request header = %v", req[:5])
	assert.Contains(t, string(req), "Source Engine Query\x00", "request missing query string")
}

func TestInfoRequestWithChallenge(t *testing.T) {
	req := InfoRequest([]byte{1, 2, 3, 4})
	assert.True(t, bytes.HasSuffix(req, []byte{1, 2, 3, 4}), "request should end with the challenge bytes: %v", req)
}

func TestPacketKind(t *testing.T) {
	packet := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'I'}, []byte("body")...)
	kind, body, err := PacketKind(packet)
	require.NoError(t, err, "PacketKind")
	assert.Equal(t, byte('I'), kind)
	assert.Equal(t, "body", string(body))
}

func TestPacketKindRejectsShort(t *testing.T) {
	_, _, err := PacketKind([]byte{0xFF, 0xFF})
	require.Error(t, err, "expected an error for a too-short packet")
}

func TestPacketKindRejectsBadHeader(t *testing.T) {
	_, _, err := PacketKind([]byte{0, 0, 0, 0, 'I'})
	require.Error(t, err, "expected an error for a bad header")
}

func TestChallengeNumber(t *testing.T) {
	c, err := ChallengeNumber([]byte{9, 8, 7, 6, 0xFF})
	require.NoError(t, err, "ChallengeNumber")
	assert.Equal(t, []byte{9, 8, 7, 6}, c)
}

func TestChallengeNumberTooShort(t *testing.T) {
	_, err := ChallengeNumber([]byte{1, 2})
	require.Error(t, err, "expected an error for a too-short challenge body")
}

func buildInfoBody(t testing.TB, info InfoResponse) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteByte(info.Protocol)
	b.WriteString(info.Name)
	b.WriteByte(0)
	b.WriteString(info.Map)
	b.WriteByte(0)
	b.WriteString(info.Folder)
	b.WriteByte(0)
	b.WriteString(info.Game)
	b.WriteByte(0)
	b.WriteByte(byte(info.AppID))
	b.WriteByte(byte(info.AppID >> 8))
	b.WriteByte(info.Players)
	b.WriteByte(info.MaxPlayers)
	b.WriteByte(info.Bots)
	b.WriteByte(info.ServerType)
	b.WriteByte(info.Environment)
	b.WriteByte(info.Visibility)
	b.WriteByte(info.VAC)
	b.WriteString(info.Version)
	b.WriteByte(0)
	return b.Bytes()
}

func TestParseInfoResponse(t *testing.T) {
	want := InfoResponse{
		Protocol: 17, Name: "dzo test server", Map: "dayzOffline.chernarusplus",
		Folder: "dayz", Game: "DayZ", AppID: 2211,
		Players: 3, MaxPlayers: 60, Bots: 0,
		ServerType: 'd', Environment: 'l', Visibility: 0, VAC: 1,
		Version: "1.29.123456",
	}
	got, err := ParseInfoResponse(buildInfoBody(t, want))
	require.NoError(t, err, "ParseInfoResponse")
	assert.Equal(t, want, got)
}

func TestParseInfoResponseTruncated(t *testing.T) {
	full := buildInfoBody(t, InfoResponse{Name: "x", Map: "y", Folder: "z", Game: "w", Version: "1"})
	for cut := 0; cut < len(full); cut++ {
		_, err := ParseInfoResponse(full[:cut])
		assert.Error(t, err, "cut=%d: expected an error for truncated input", cut)
	}
}

func TestParseInfoResponseUnterminatedString(t *testing.T) {
	// Protocol byte + an unterminated name string.
	_, err := ParseInfoResponse([]byte{17, 'h', 'i'})
	require.Error(t, err, "expected an error for an unterminated string")
}

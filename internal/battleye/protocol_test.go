// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serverEncodeLoginResponse and friends below build server-side packets by
// hand (mirroring wrap()) so the tests don't rely on the client-side
// encoders to validate the client-side decoders.
func serverPacket(typ PacketType, rest ...byte) []byte {
	body := append([]byte{packetMarker, byte(typ)}, rest...)
	return wrap(body)
}

func TestEncodeLoginAndPeekType(t *testing.T) {
	pkt := EncodeLogin("s3cret")
	require.Equal(t, uint8('B'), pkt[0], "bad header: %v", pkt[:2])
	require.Equal(t, uint8('E'), pkt[1], "bad header: %v", pkt[:2])
	typ, err := PeekType(pkt)
	require.NoError(t, err, "PeekType")
	assert.Equal(t, PacketLogin, typ)
}

func TestDecodeLoginResponseSuccess(t *testing.T) {
	pkt := serverPacket(PacketLogin, 0x01)
	ok, err := DecodeLoginResponse(pkt)
	require.NoError(t, err, "DecodeLoginResponse")
	assert.True(t, ok, "expected login success")
}

func TestDecodeLoginResponseFailure(t *testing.T) {
	pkt := serverPacket(PacketLogin, 0x00)
	ok, err := DecodeLoginResponse(pkt)
	require.NoError(t, err, "DecodeLoginResponse")
	assert.False(t, ok, "expected login failure")
}

func TestDecodeLoginResponseWrongType(t *testing.T) {
	pkt := serverPacket(PacketCommand, 0x00, 0x01)
	_, err := DecodeLoginResponse(pkt)
	require.Error(t, err)
}

func TestEncodeCommandAndSinglePacketResponse(t *testing.T) {
	pkt := EncodeCommand(7, "players")
	typ, err := PeekType(pkt)
	require.NoError(t, err)
	require.Equal(t, PacketCommand, typ)

	resp := serverPacket(PacketCommand, 7, 'o', 'k')
	seq, multipart, payload, err := DecodeCommandResponse(resp)
	require.NoError(t, err, "DecodeCommandResponse")
	assert.Equal(t, uint8(7), seq)
	assert.Nil(t, multipart)
	assert.Equal(t, "ok", string(payload))
}

func TestEncodeCommandEmptyIsKeepAlive(t *testing.T) {
	pkt := EncodeCommand(1, "")
	body, err := unwrap(pkt)
	require.NoError(t, err, "unwrap")
	require.Len(t, body, 3)
}

func TestDecodeCommandResponseMultipart(t *testing.T) {
	// part 1 of 2
	resp := serverPacket(PacketCommand, 9, 0x00, 2, 0, 'h', 'e')
	seq, multipart, payload, err := DecodeCommandResponse(resp)
	require.NoError(t, err, "DecodeCommandResponse")
	assert.Equal(t, uint8(9), seq)
	require.NotNil(t, multipart)
	require.Equal(t, 2, multipart.Total)
	require.Equal(t, 0, multipart.Index)
	assert.Equal(t, "he", string(payload))

	// part 2 of 2
	resp = serverPacket(PacketCommand, 9, 0x00, 2, 1, 'l', 'l', 'o')
	_, multipart, payload, err = DecodeCommandResponse(resp)
	require.NoError(t, err, "DecodeCommandResponse")
	require.NotNil(t, multipart)
	require.Equal(t, 1, multipart.Index)
	assert.Equal(t, "llo", string(payload))
}

func TestDecodeCommandResponseWrongType(t *testing.T) {
	resp := serverPacket(PacketLogin, 0x01)
	_, _, _, err := DecodeCommandResponse(resp)
	require.Error(t, err)
}

func TestServerMessageAndAck(t *testing.T) {
	msg := serverPacket(PacketMessage, 3, 'h', 'i')
	seq, payload, err := DecodeServerMessage(msg)
	require.NoError(t, err, "DecodeServerMessage")
	require.Equal(t, uint8(3), seq, "seq=%d payload=%q", seq, payload)
	assert.Equal(t, "hi", string(payload), "seq=%d payload=%q", seq, payload)

	ack := EncodeMessageAck(3)
	body, err := unwrap(ack)
	require.NoError(t, err, "unwrap(ack)")
	require.Len(t, body, 3)
	require.Equal(t, byte(PacketMessage), body[1])
	assert.Equal(t, uint8(3), body[2])
}

func TestDecodeServerMessageWrongType(t *testing.T) {
	pkt := serverPacket(PacketCommand, 1, 'x')
	_, _, err := DecodeServerMessage(pkt)
	require.Error(t, err)
}

func TestUnwrapRejectsShortPacket(t *testing.T) {
	_, err := unwrap([]byte{'B', 'E', 0, 0})
	require.Error(t, err)
}

func TestUnwrapRejectsBadHeader(t *testing.T) {
	pkt := serverPacket(PacketLogin, 0x01)
	pkt[0] = 'X'
	_, err := unwrap(pkt)
	require.Error(t, err)
}

func TestUnwrapRejectsMissingMarker(t *testing.T) {
	pkt := serverPacket(PacketLogin, 0x01)
	pkt[6] = 0x00 // corrupt the 0xFF marker
	// Recompute nothing: this must be caught before CRC even matters, but
	// since the marker is part of the CRC input, corrupting it also breaks
	// the CRC. Rebuild with a bad marker but correct CRC to isolate the check.
	body := []byte{0x00, byte(PacketLogin), 0x01}
	bad := wrap(body)
	_, err := unwrap(bad)
	require.Error(t, err)
}

func TestUnwrapRejectsCRCMismatch(t *testing.T) {
	pkt := serverPacket(PacketLogin, 0x01)
	pkt[2] ^= 0xFF // flip bits in the CRC field
	_, err := unwrap(pkt)
	require.Error(t, err)
}

func TestBodyTypeRejectsShortBody(t *testing.T) {
	_, err := bodyType([]byte{packetMarker})
	require.Error(t, err)
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	body := []byte{packetMarker, byte(PacketCommand), 42, 'a', 'b', 'c'}
	pkt := wrap(body)
	got, err := unwrap(pkt)
	require.NoError(t, err, "unwrap")
	assert.Equal(t, body, got)
}

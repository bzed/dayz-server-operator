// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func addPacketSeeds(f *testing.F) {
	f.Helper()
	f.Add(serverPacket(PacketLogin, 0x01))
	f.Add(serverPacket(PacketLogin, 0x00))
	f.Add(serverPacket(PacketCommand, 42, 'a', 'b', 'c'))
	f.Add(serverPacket(PacketCommand, 7, 0x00, 0x03, 0x01, 'x'))
	f.Add(serverPacket(PacketMessage, 5, 'P', 'l', 'a', 'y', 'e', 'r'))
	f.Add(EncodeLogin("s3cret"))
	f.Add(EncodeCommand(1, "players"))
	f.Add([]byte{'B', 'E'})
	f.Add([]byte{})
}

func FuzzDecodeLoginResponse(f *testing.F) {
	addPacketSeeds(f)
	f.Fuzz(func(_ *testing.T, packet []byte) {
		_, _ = DecodeLoginResponse(packet)
	})
}

func FuzzDecodeServerMessage(f *testing.F) {
	addPacketSeeds(f)
	f.Fuzz(func(t *testing.T, packet []byte) {
		_, message, err := DecodeServerMessage(packet)
		if err == nil {
			require.LessOrEqual(t, len(message), len(packet))
		}
	})
}

func FuzzDecodeCommandResponse(f *testing.F) {
	addPacketSeeds(f)
	f.Fuzz(func(t *testing.T, packet []byte) {
		_, multipart, payload, err := DecodeCommandResponse(packet)
		if err != nil {
			return
		}
		require.LessOrEqual(t, len(payload), len(packet))
		if multipart != nil {
			require.Less(t, multipart.Index, multipart.Total)
		}
	})
}

func FuzzParsePlayers(f *testing.F) {
	f.Add("Players on server:\n[#] [IP Address]:[Port] [Ping] [GUID] [Name]\n--------------------------------------------------\n0   203.0.113.5:2304   12   0123456789abcdef0123456789abcdef(OK) dzotester\n(1 players in total)")
	f.Add("")
	f.Fuzz(func(_ *testing.T, raw string) { _ = ParsePlayers(raw) })
}

func FuzzParseEvent(f *testing.F) {
	f.Add("Player #3 Survivor123 (203.0.113.5:2304) connected")
	f.Add("Player #3 Survivor123 - GUID: 0123456789abcdef0123456789abcdef")
	f.Add("Verified GUID (0123456789abcdef0123456789abcdef) for player #3 Survivor123")
	f.Add("Player #3 Survivor123 disconnected")
	f.Add("")
	f.Fuzz(func(_ *testing.T, payload string) { _ = ParseEvent(payload) })
}

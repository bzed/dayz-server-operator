// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package a2s implements Valve's Server Query Protocol ("A2S"), enough to
// send an A2S_INFO request and parse its reply. It backs both the
// in-container health probes (HealthStartupCmd/HealthCmd, §C9 - the query
// port only answers once the mission is loaded, which is exactly the
// readiness signal) and the remote status/Icinga checks.
//
// Reference: https://developer.valvesoftware.com/wiki/Server_queries
// (a public, stable protocol; DayZ uses the standard Source engine query
// port). This file holds the pure wire-format logic, fully unit-tested
// without a real game server; Client in client.go does the networking.
package a2s

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	simplePacketHeader    = 0xFFFFFFFF
	requestKindInfo       = 'T'
	responseKindInfo      = 'I'
	responseKindChallenge = 'A'

	queryString = "Source Engine Query\x00"
)

// InfoRequest builds an A2S_INFO request packet. challenge is nil for the
// first request; after a challenge response, pass its 4 raw bytes to
// retry the request (the protocol's mandatory two-round-trip handshake).
func InfoRequest(challenge []byte) []byte {
	buf := make([]byte, 0, 4+1+len(queryString)+len(challenge))
	buf = binary.LittleEndian.AppendUint32(buf, simplePacketHeader)
	buf = append(buf, requestKindInfo)
	buf = append(buf, []byte(queryString)...)
	buf = append(buf, challenge...)
	return buf
}

// PacketKind returns the single-byte type of a validated simple (non-split)
// response packet, and the body following it.
func PacketKind(packet []byte) (kind byte, body []byte, err error) {
	if len(packet) < 5 {
		return 0, nil, fmt.Errorf("a2s: packet too short (%d bytes)", len(packet))
	}
	header := binary.LittleEndian.Uint32(packet[:4])
	if header != simplePacketHeader {
		return 0, nil, fmt.Errorf("a2s: unsupported/split packet header %#x", header)
	}
	return packet[4], packet[5:], nil
}

// ChallengeNumber extracts the 4-byte challenge from an 'A' (challenge)
// response body.
func ChallengeNumber(body []byte) ([]byte, error) {
	if len(body) < 4 {
		return nil, fmt.Errorf("a2s: challenge body too short (%d bytes)", len(body))
	}
	return body[:4], nil
}

// InfoResponse is the subset of A2S_INFO fields dzo needs (readiness,
// player counts, map/name for status reporting). Fields after Version
// (the optional Extra Data Flag section: port, SteamID, keywords, ...)
// are not parsed - nothing in dzo needs them yet.
type InfoResponse struct {
	Protocol byte
	Name     string
	Map      string
	Folder   string
	Game     string
	// AppID is the wire "ID" field, a signed 16-bit value per the Valve
	// spec. DayZ's real app ids (221100/223350) don't fit in it, and real
	// 1.29 and 1.30 servers send 0 (checked against both): do not use it to
	// tell servers apart.
	AppID       int16
	Players     byte
	MaxPlayers  byte
	Bots        byte
	ServerType  byte
	Environment byte
	Visibility  byte
	VAC         byte
	Version     string
}

// ParseInfoResponse parses an A2S_INFO ('I') response body.
func ParseInfoResponse(body []byte) (InfoResponse, error) {
	var info InfoResponse
	r := &cstringReader{data: body}

	var err error
	if info.Protocol, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: protocol: %w", err)
	}
	if info.Name, err = r.cstring(); err != nil {
		return info, fmt.Errorf("a2s: name: %w", err)
	}
	if info.Map, err = r.cstring(); err != nil {
		return info, fmt.Errorf("a2s: map: %w", err)
	}
	if info.Folder, err = r.cstring(); err != nil {
		return info, fmt.Errorf("a2s: folder: %w", err)
	}
	if info.Game, err = r.cstring(); err != nil {
		return info, fmt.Errorf("a2s: game: %w", err)
	}
	if info.AppID, err = r.int16(); err != nil {
		return info, fmt.Errorf("a2s: app id: %w", err)
	}
	if info.Players, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: players: %w", err)
	}
	if info.MaxPlayers, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: max players: %w", err)
	}
	if info.Bots, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: bots: %w", err)
	}
	if info.ServerType, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: server type: %w", err)
	}
	if info.Environment, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: environment: %w", err)
	}
	if info.Visibility, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: visibility: %w", err)
	}
	if info.VAC, err = r.byte_(); err != nil {
		return info, fmt.Errorf("a2s: vac: %w", err)
	}
	if info.Version, err = r.cstring(); err != nil {
		return info, fmt.Errorf("a2s: version: %w", err)
	}
	// EDF + extra fields (port, SteamID, keywords, game id, ...) follow
	// optionally and are intentionally not parsed.
	return info, nil
}

type cstringReader struct {
	data []byte
	pos  int
}

func (r *cstringReader) byte_() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("unexpected end of data")
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *cstringReader) int16() (int16, error) {
	if r.pos+2 > len(r.data) {
		return 0, fmt.Errorf("unexpected end of data")
	}
	v := int16(binary.LittleEndian.Uint16(r.data[r.pos : r.pos+2])) //nolint:gosec // deliberate reinterpretation of the wire "short" field, see InfoResponse.AppID's doc comment
	r.pos += 2
	return v, nil
}

func (r *cstringReader) cstring() (string, error) {
	end := bytes.IndexByte(r.data[r.pos:], 0)
	if end < 0 {
		return "", fmt.Errorf("unterminated string")
	}
	s := string(r.data[r.pos : r.pos+end])
	r.pos += end + 1
	return s, nil
}

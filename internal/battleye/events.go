// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"regexp"
	"strconv"
)

// EventKind classifies an unsolicited BattlEye Server Message (FR-22: event
// stream connect/GUID/chat/kick).
type EventKind int

const (
	EventUnknown EventKind = iota
	EventConnect
	EventGUID
	EventDisconnect
	EventChat
	EventKick
	EventVerifiedGUID
)

// Event is a parsed Server Message.
type Event struct {
	Kind EventKind
	Raw  string

	PlayerID   int    // BE client slot number, when present
	PlayerName string // when present
	GUID       string // BattlEye GUID, when present
	Address    string // "ip:port", when present (connect)
	Channel    string // chat channel/scope, when present
	Message    string // chat text or kick reason, when present
}

// Patterns for the BattlEye RCon messages of a real DayZ 1.29 server, captured with a client that
// runs BattlEye (spike S4): connect, GUID, chat, kick and disconnect are real lines, kept in the
// tests. The "Verified GUID" line is not: the BattlEye master did not answer ("Ban check timed
// out, no response from BE Master"), so that one still follows the documented format of other games.
// Other server messages ("Connected to BE Master", "RCon admin #0 (ip:port) logged in", ...) are
// EventUnknown.
var (
	reConnect       = regexp.MustCompile(`^Player #(\d+) (.+) \(([0-9.]+:\d+)\) connected$`)
	reGUIDComputing = regexp.MustCompile(`^Player #(\d+) (.+) - (?:BE )?GUID: ([0-9a-f]+)$`)
	reGUIDVerified  = regexp.MustCompile(`^Verified GUID \(([0-9a-f]+)\) for player #(\d+) (.+)$`)
	reDisconnect    = regexp.MustCompile(`^Player #(\d+) (.+) disconnected$`)
	reChat          = regexp.MustCompile(`^\((\w+)\) (.+?): (.*)$`)
	// The kick line carries the GUID in parentheses: "Player #0 name (guid) has been kicked by
	// BattlEye: Admin Kick (reason)". A name may itself end in parentheses, so the GUID is 32 hex digits.
	reKick = regexp.MustCompile(`^Player #(\d+) (.+?)(?: \(([0-9a-f]{32})\))? has been kicked by BattlEye: (.*)$`)
)

// ParseEvent classifies a Server Message payload into a structured Event.
// Anything that matches none of the known patterns is returned as
// EventUnknown with Raw set, rather than an error: unrecognised server
// chatter must never abort the RCon event loop.
func ParseEvent(payload string) Event {
	if m := reConnect.FindStringSubmatch(payload); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Event{Kind: EventConnect, Raw: payload, PlayerID: id, PlayerName: m[2], Address: m[3]}
	}
	if m := reGUIDVerified.FindStringSubmatch(payload); m != nil {
		id, _ := strconv.Atoi(m[2])
		return Event{Kind: EventVerifiedGUID, Raw: payload, GUID: m[1], PlayerID: id, PlayerName: m[3]}
	}
	if m := reGUIDComputing.FindStringSubmatch(payload); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Event{Kind: EventGUID, Raw: payload, PlayerID: id, PlayerName: m[2], GUID: m[3]}
	}
	if m := reKick.FindStringSubmatch(payload); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Event{Kind: EventKick, Raw: payload, PlayerID: id, PlayerName: m[2], GUID: m[3], Message: m[4]}
	}
	if m := reDisconnect.FindStringSubmatch(payload); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Event{Kind: EventDisconnect, Raw: payload, PlayerID: id, PlayerName: m[2]}
	}
	if m := reChat.FindStringSubmatch(payload); m != nil {
		return Event{Kind: EventChat, Raw: payload, Channel: m[1], PlayerName: m[2], Message: m[3]}
	}
	return Event{Kind: EventUnknown, Raw: payload}
}

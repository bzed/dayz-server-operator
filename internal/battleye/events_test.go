// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import "testing"

func TestParseEventConnect(t *testing.T) {
	e := ParseEvent("Player #3 Survivor123 (203.0.113.5:2304) connected")
	if e.Kind != EventConnect {
		t.Fatalf("Kind = %v, want EventConnect", e.Kind)
	}
	if e.PlayerID != 3 || e.PlayerName != "Survivor123" || e.Address != "203.0.113.5:2304" {
		t.Errorf("Event = %+v", e)
	}
}

func TestParseEventGUID(t *testing.T) {
	e := ParseEvent("Player #3 Survivor123 - GUID: 0123456789abcdef0123456789abcdef")
	if e.Kind != EventGUID {
		t.Fatalf("Kind = %v, want EventGUID", e.Kind)
	}
	if e.PlayerID != 3 || e.PlayerName != "Survivor123" || e.GUID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("Event = %+v", e)
	}
}

func TestParseEventVerifiedGUID(t *testing.T) {
	e := ParseEvent("Verified GUID (0123456789abcdef0123456789abcdef) for player #3 Survivor123")
	if e.Kind != EventVerifiedGUID {
		t.Fatalf("Kind = %v, want EventVerifiedGUID", e.Kind)
	}
	if e.PlayerID != 3 || e.PlayerName != "Survivor123" || e.GUID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("Event = %+v", e)
	}
}

func TestParseEventDisconnect(t *testing.T) {
	e := ParseEvent("Player #3 Survivor123 disconnected")
	if e.Kind != EventDisconnect {
		t.Fatalf("Kind = %v, want EventDisconnect", e.Kind)
	}
	if e.PlayerID != 3 || e.PlayerName != "Survivor123" {
		t.Errorf("Event = %+v", e)
	}
}

func TestParseEventChat(t *testing.T) {
	e := ParseEvent("(Side) Survivor123: hello there")
	if e.Kind != EventChat {
		t.Fatalf("Kind = %v, want EventChat", e.Kind)
	}
	if e.Channel != "Side" || e.PlayerName != "Survivor123" || e.Message != "hello there" {
		t.Errorf("Event = %+v", e)
	}
}

func TestParseEventKick(t *testing.T) {
	e := ParseEvent("Player #3 Survivor123 has been kicked by BattlEye: Admin Kick")
	if e.Kind != EventKick {
		t.Fatalf("Kind = %v, want EventKick", e.Kind)
	}
	if e.PlayerID != 3 || e.PlayerName != "Survivor123" || e.Message != "Admin Kick" {
		t.Errorf("Event = %+v", e)
	}
}

func TestParseEventUnknown(t *testing.T) {
	e := ParseEvent("something we've never seen before")
	if e.Kind != EventUnknown {
		t.Fatalf("Kind = %v, want EventUnknown", e.Kind)
	}
	if e.Raw != "something we've never seen before" {
		t.Errorf("Raw = %q", e.Raw)
	}
}

// The lines of a real DayZ 1.29 server (a client with BattlEye joined, chatted, was kicked and left
// on a local server and on a development host, 2026-10-10).
func TestParseRealServerLines(t *testing.T) {
	const guid = "907725ff524e1026154b5ac3cccb0258"
	tests := []struct {
		line string
		want Event
	}{
		{"Player #0 dzotester (10.125.0.3:41939) connected", Event{Kind: EventConnect, PlayerID: 0, PlayerName: "dzotester", Address: "10.125.0.3:41939"}},
		{"Player #0 dzotester - BE GUID: " + guid, Event{Kind: EventGUID, PlayerID: 0, PlayerName: "dzotester", GUID: guid}},
		{"(Global) dzotester: dzo-be-test second: with a colon", Event{Kind: EventChat, Channel: "Global", PlayerName: "dzotester", Message: "dzo-be-test second: with a colon"}},
		{"Player #0 dzotester (" + guid + ") has been kicked by BattlEye: Admin Kick (dzo test kick)", Event{Kind: EventKick, PlayerID: 0, PlayerName: "dzotester", GUID: guid, Message: "Admin Kick (dzo test kick)"}},
		{"Player #0 dzotester disconnected", Event{Kind: EventDisconnect, PlayerID: 0, PlayerName: "dzotester"}},
		// a name with parentheses is not mistaken for the GUID
		{"Player #2 Bob (the builder) (" + guid + ") has been kicked by BattlEye: Admin Kick ()", Event{Kind: EventKick, PlayerID: 2, PlayerName: "Bob (the builder)", GUID: guid, Message: "Admin Kick ()"}},
		{"Player #2 Bob (the builder) has been kicked by BattlEye: Admin Kick ()", Event{Kind: EventKick, PlayerID: 2, PlayerName: "Bob (the builder)", Message: "Admin Kick ()"}},
		// server chatter that is not a player event
		{"Connected to BE Master", Event{Kind: EventUnknown}},
		{"Ban check timed out, no response from BE Master", Event{Kind: EventUnknown}},
		{"RCon admin #0 (127.0.0.1:39951) logged in", Event{Kind: EventUnknown}},
	}
	for _, tt := range tests {
		got := ParseEvent(tt.line)
		tt.want.Raw = tt.line
		if got != tt.want {
			t.Errorf("ParseEvent(%q) =\n %+v\nwant\n %+v", tt.line, got, tt.want)
		}
	}
}

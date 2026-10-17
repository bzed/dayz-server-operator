// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"os"
	"strings"
	"testing"
)

func TestParsePlayersOfTheLiveCapture(t *testing.T) {
	b, err := os.ReadFile("../moderation/testdata/v2/players.out")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := os.ReadFile("../moderation/testdata/v2/pair.txt")
	if err != nil {
		t.Fatal(err)
	}
	guid := strings.Fields(string(pair))[1]
	ps := ParsePlayers(string(b))
	if len(ps) != 1 || ps[0].Name != "dzotester" || ps[0].GUID != guid || ps[0].IP == "" || ps[0].Port == 0 {
		t.Fatalf("ParsePlayers = %+v", ps)
	}
}

func TestParsePlayersVariants(t *testing.T) {
	raw := "Players on server:\n[#] [IP Address]:[Port] [Ping] [GUID] [Name]\n--------------------------------------------------\n" +
		"0   10.0.0.3:2304         15   0123456789abcdef0123456789abcdef(OK) alice (Lobby)\n" +
		"12  192.0.2.7:41939       -1   fedcba9876543210fedcba9876543210(?) bob with spaces\n" +
		"3   garbage line\n(2 players in total)\n"
	ps := ParsePlayers(raw)
	if len(ps) != 2 {
		t.Fatalf("%+v", ps)
	}
	if p := ps[0]; p.ID != 0 || p.IP != "10.0.0.3" || p.Port != 2304 || !p.Verified || p.Name != "alice" {
		t.Errorf("alice %+v", p)
	}
	if p := ps[1]; p.ID != 12 || p.Ping != -1 || p.Verified || p.Name != "bob with spaces" {
		t.Errorf("bob %+v", p)
	}
}

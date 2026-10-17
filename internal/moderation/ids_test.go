// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"strings"
	"testing"
)

func TestDeriveGUIDMatchesTheLiveCapture(t *testing.T) {
	b, err := os.ReadFile("testdata/v2/pair.txt")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		t.Fatalf("pair.txt: want \"<steamid> <guid>\", got %q", b)
	}
	got, err := DeriveGUID(f[0])
	if err != nil || got != f[1] {
		t.Fatalf("DeriveGUID(%s) = %q, %v; the server said %s", f[0], got, err, f[1])
	}
}

func TestDeriveGUIDRefusesBadIDs(t *testing.T) {
	for _, id := range []string{"", "123", "76561198000000001x", "86561198000000001", "765611980000000011"} {
		if _, err := DeriveGUID(id); err == nil {
			t.Errorf("DeriveGUID(%q) accepted", id)
		}
	}
}

func TestValidators(t *testing.T) {
	if !ValidSteamID("76561198000000001") || ValidSteamID("7656119800000000") {
		t.Error("ValidSteamID")
	}
	if !ValidGUID("907725ff524e1026154b5ac3cccb0258") || ValidGUID("907725FF524E1026154B5AC3CCCB0258") || ValidGUID("907725ff") {
		t.Error("ValidGUID: lowercase hex, 32 digits")
	}
	for in, want := range map[string]string{"192.0.2.7": "192.0.2.7", " 10.0.0.1 ": "10.0.0.1", "::ffff:192.0.2.7": "192.0.2.7"} {
		if got, err := ParseIP(in); err != nil || got != want {
			t.Errorf("ParseIP(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0.0.0.0", "2001:db8::7", "192.0.2.7:2304", "10.0.0.0/8", "example.org"} {
		if _, err := ParseIP(in); err == nil {
			t.Errorf("ParseIP(%q) accepted", in)
		}
	}
}

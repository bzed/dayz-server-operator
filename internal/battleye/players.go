// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"regexp"
	"strconv"
	"strings"
)

// PlayerInfo is one line of the RCon "players" answer.
type PlayerInfo struct {
	ID       int
	IP       string
	Port     int
	Ping     int
	GUID     string
	Verified bool // "(OK)"; "(?)" while the BattlEye master has not confirmed it
	Name     string
}

// rePlayer matches "<#> <ip>:<port> <ping> <guid>(OK|?) <name>[ (Lobby)]" (testdata/v2 of
// internal/moderation holds a real answer).
var rePlayer = regexp.MustCompile(`^(\d+)\s+([0-9.]+):(\d+)\s+(-?\d+)\s+([0-9a-f]{32})\((OK|\?)\)\s+(.*?)(?:\s+\(Lobby\))?$`)

// ParsePlayers reads the players of a "players" answer; lines it does not understand are skipped.
func ParsePlayers(raw string) []PlayerInfo {
	var out []PlayerInfo
	for _, line := range strings.Split(raw, "\n") {
		m := rePlayer.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		id, _ := strconv.Atoi(m[1]) // the pattern only matches digits
		port, _ := strconv.Atoi(m[3])
		ping, _ := strconv.Atoi(m[4])
		out = append(out, PlayerInfo{ID: id, IP: m[2], Port: port, Ping: ping, GUID: m[5], Verified: m[6] == "OK", Name: m[7]})
	}
	return out
}

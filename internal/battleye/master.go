// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"
)

// Game is a game BattlEye's master server answers ban queries for. The port selects the game.
type Game struct {
	ID   string // "dayz", "arma3", "arma2oa"; the same ids as moderation.BEGame
	Name string
	Port int
}

// Games are the games the master answers for, DayZ first (found by a port sweep of 2300-2400; Arma Reforger
// has no port there, it identifies players by Bohemia account).
var Games = []Game{{"dayz", "DayZ", 2354}, {"arma3", "Arma 3", 2344}, {"arma2oa", "Arma 2: OA", 2324}}

// MasterHost is the master server. dayz1, arma31 and arma2oa1.battleye.com resolve to the same address.
const MasterHost = "dayz1.battleye.com"

// GlobalBan is BattlEye's answer for a GUID.
type GlobalBan struct {
	Banned bool
	Reason string
}

// ErrNoAnswer means the master did not answer in time. That is not "not banned".
var ErrNoAnswer = errors.New("battleye: master did not answer")

const (
	maxMasterReason = 256
	defaultTimeout  = 2 * time.Second
	defaultTries    = 2
)

// Master asks BattlEye's master server whether a GUID is globally banned. It uses the undocumented UDP query
// that game servers use when a player joins: one datagram, five dots and the GUID; the answer echoes the first
// four bytes and then says the reason, or nothing (or "0") when the GUID is not banned.
type Master struct {
	Host string         // default MasterHost; tests point it at 127.0.0.1
	Port func(Game) int // default g.Port
	// Timeout is how long one try waits for the answer (default 2 s); Tries is how many datagrams are sent
	// (default 2).
	Timeout time.Duration
	Tries   int
}

func validGUID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Check asks about guid in one game. It sends at most Tries datagrams and returns ErrNoAnswer when the master
// stays silent. The reason is shortened and stripped of control characters: it comes from outside.
func (m *Master) Check(ctx context.Context, g Game, guid string) (GlobalBan, error) {
	if !validGUID(guid) {
		return GlobalBan{}, fmt.Errorf("battleye: %q is not a GUID (32 lowercase hex digits)", guid)
	}
	host := m.Host
	if host == "" {
		host = MasterHost
	}
	port := g.Port
	if m.Port != nil {
		port = m.Port(g)
	}
	timeout, tries := m.Timeout, m.Tries
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if tries <= 0 {
		tries = defaultTries
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return GlobalBan{}, fmt.Errorf("battleye: master: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	req := []byte("....." + guid)
	buf := make([]byte, 1024)
	for range tries {
		if err := ctx.Err(); err != nil {
			return GlobalBan{}, fmt.Errorf("battleye: master: %w", err)
		}
		if _, err := conn.Write(req); err != nil {
			return GlobalBan{}, fmt.Errorf("battleye: master: %w", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(timeout)) // a failed deadline only makes the read block longer, bounded by ctx
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break // timeout: the next try
			}
			if n < 4 || !bytes.Equal(buf[:4], req[:4]) {
				continue // not an answer to us
			}
			return ban(buf[4:n]), nil
		}
	}
	if err := ctx.Err(); err != nil {
		return GlobalBan{}, fmt.Errorf("battleye: master: %w", err)
	}
	return GlobalBan{}, ErrNoAnswer
}

// ban reads the reason of an answer.
func ban(raw []byte) GlobalBan {
	reason := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, string(raw))
	reason = strings.TrimSpace(reason)
	if len(reason) > maxMasterReason {
		reason = strings.ToValidUTF8(reason[:maxMasterReason], "")
	}
	if reason == "" || reason == "0" {
		return GlobalBan{}
	}
	return GlobalBan{Banned: true, Reason: reason}
}

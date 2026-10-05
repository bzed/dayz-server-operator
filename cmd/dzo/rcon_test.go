// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"hash/crc32"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRconExecMissingFlags(t *testing.T) {
	if _, err := runCmd(t, "rcon", "exec", "players"); err == nil {
		t.Fatal("expected an error for missing --addr/--password")
	}
}

func TestRconExecEndToEnd(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = conn.Close() }()

	go func() {
		buf := make([]byte, 4096)
		// login
		_, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		_, _ = conn.WriteToUDP(loginOK(), remote)

		// command
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		body := buf[6:n]
		seq := body[2]
		_, _ = conn.WriteToUDP(commandOK(seq, "pong"), remote)
	}()

	out, err := runCmd(t, "rcon", "exec",
		"--addr", conn.LocalAddr().String(),
		"--password", "pw",
		"--timeout", "2s",
		"ping",
	)
	if err != nil {
		t.Fatalf("rcon exec: %v", err)
	}
	if !strings.Contains(out, "pong") {
		t.Errorf("output = %q, want it to contain pong", out)
	}
}

func TestRconExecDialFailure(t *testing.T) {
	// Nothing listens on this address, and the login handshake should time
	// out fast on a loopback address with no responder... but the default
	// timeout is long, so instead point at a reserved, non-routable address
	// to force a quick connection error at Write time is not guaranteed on
	// UDP. To keep this test fast and deterministic, use a port that
	// immediately refuses (closed by us right before use).
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := conn.LocalAddr().String()
	_ = conn.Close() // now nothing is listening there

	start := time.Now()
	_, err = runCmd(t, "rcon", "exec", "--addr", addr, "--password", "pw", "--timeout", "1s", "ping")
	if err == nil {
		t.Fatal("expected a dial/login error")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("dial failure took too long")
	}
}

// loginOK/commandOK build minimal, valid BattlEye packets by hand (cmd/dzo
// has no access to the internal/battleye test helpers, which are
// unexported).
func loginOK() []byte {
	return bePacket(0x00, 0x01)
}

func commandOK(seq byte, text string) []byte {
	body := append([]byte{seq}, []byte(text)...)
	return bePacket(0x01, body...)
}

func bePacket(typ byte, rest ...byte) []byte {
	body := append([]byte{0xFF, typ}, rest...)
	crc := crc32.ChecksumIEEE(body)
	buf := make([]byte, 2+4+len(body))
	buf[0], buf[1] = 'B', 'E'
	buf[2] = byte(crc)
	buf[3] = byte(crc >> 8)
	buf[4] = byte(crc >> 16)
	buf[5] = byte(crc >> 24)
	copy(buf[6:], body)
	return buf
}

func TestRconExecNeedsATarget(t *testing.T) {
	_, err := runCmd(t, "rcon", "exec", "players")
	if err == nil || !strings.Contains(err.Error(), "--instance") {
		t.Errorf("err = %v, want the hint to give --instance or --addr and --password", err)
	}
}

func TestRconExecUnknownInstance(t *testing.T) {
	cfg := exporterSite(t, "")
	if _, err := runCmd(t, "rcon", "exec", "--instance", "nope", "--config", cfg, "players"); err == nil {
		t.Error("an instance that is not in the site must be an error")
	}
	if _, err := runCmd(t, "instance", "shutdown", "nope", "--config", cfg); err == nil {
		t.Error("dzo instance shutdown of an unknown instance must be an error")
	}
}

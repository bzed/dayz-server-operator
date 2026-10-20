// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"hash/crc32"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRconExecMissingFlags(t *testing.T) {
	_, err := runCmd(t, "rcon", "exec", "players")
	require.Error(t, err, "expected an error for missing --addr/--password")
}

func TestRconExecEndToEnd(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
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
	require.NoError(t, err, "rcon exec")
	assert.Contains(t, out, "pong")
}

func TestRconExecDialFailure(t *testing.T) {
	// Nothing listens on this address, and the login handshake should time
	// out fast on a loopback address with no responder... but the default
	// timeout is long, so instead point at a reserved, non-routable address
	// to force a quick connection error at Write time is not guaranteed on
	// UDP. To keep this test fast and deterministic, use a port that
	// immediately refuses (closed by us right before use).
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	addr := conn.LocalAddr().String()
	_ = conn.Close() // now nothing is listening there

	start := time.Now()
	_, err = runCmd(t, "rcon", "exec", "--addr", addr, "--password", "pw", "--timeout", "1s", "ping")
	require.Error(t, err, "expected a dial/login error")
	require.LessOrEqual(t, time.Since(start), 10*time.Second, "dial failure took too long")
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
	assert.ErrorContains(t, err, "--instance", "want the hint to give --instance or --addr and --password")
}

func TestRconExecUnknownInstance(t *testing.T) {
	cfg := exporterSite(t, "")
	_, err := runCmd(t, "rcon", "exec", "--instance", "nope", "--config", cfg, "players")
	require.Error(t, err, "an instance that is not in the site must be an error")
	_, err = runCmd(t, "instance", "shutdown", "nope", "--config", cfg)
	assert.Error(t, err, "dzo instance shutdown of an unknown instance must be an error")
}

func TestRconConsoleAndRotate(t *testing.T) {
	isolatedHome(t)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n >= 8 && buf[7] == 0x00 { // login
				_, _ = conn.WriteToUDP(loginOK(), remote)
				continue
			}
			if n >= 9 && buf[7] == 0x01 {
				// a server message first (type 0x02, sequence 7), then the answer: the console prints what the server says
				_, _ = conn.WriteToUDP(bePacket(0x02, append([]byte{7}, []byte("Player #1 Survivor connected")...)...), remote)
				_, _ = conn.WriteToUDP(commandOK(buf[8], "pong"), remote)
			}
		}
	}()
	data := t.TempDir()
	cfg, _, _ := renderSetupIn(t, data)
	yaml := filepath.Join(data, "site", "instances", "x", "instance.yaml")
	b, _ := os.ReadFile(yaml)
	port := conn.LocalAddr().(*net.UDPAddr).Port
	writeFile(t, yaml, strings.Replace(string(b), "rcon: 2306", "rcon: "+strconv.Itoa(port), 1))

	out, err := runCmdWithStdin(t, "\nplayers\nexit\nignored\n", "rcon", "console", "x", "--config", cfg, "--timeout", "3s")
	require.NoError(t, err, "console: %s", out)
	require.Contains(t, out, "connected to x", "console")
	require.Contains(t, out, "pong", "console")
	require.Contains(t, out, "< Player #1 Survivor connected", "the console prints server events that arrived before it ended")

	pwFile := filepath.Join(data, "secrets", "rcon", "x")
	writeFile(t, pwFile, "old\n")
	out, err = runCmd(t, "rcon", "rotate", "x", "--config", cfg)
	require.NoError(t, err, "rotate: %s", out)
	require.Contains(t, out, "new RCon password", "rotate")
	b, _ = os.ReadFile(pwFile)
	pw := strings.TrimSpace(string(b))
	assert.NotEqual(t, "old", pw, "password was not rotated")
	assert.Len(t, pw, 24, "password")
	_, err = runCmd(t, "rcon", "rotate", "nope", "--config", cfg)
	assert.Error(t, err, "an unknown instance must fail")
}

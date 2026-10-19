// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package health

import (
	"bytes"
	"hash/crc32"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeA2SServer answers every A2S_INFO request directly (no challenge),
// which is enough to exercise Probe's "query port answers" path.
func fakeA2SServer(t testing.TB) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_ = n
			resp := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'I'}, minimalA2SInfoBody()...)
			if _, err := conn.WriteToUDP(resp, remote); err != nil {
				return
			}
		}
	}()
	return conn.LocalAddr().String()
}

// minimalA2SInfoBody builds an A2S_INFO body that internal/a2s.ParseInfoResponse
// accepts: protocol byte, four empty c-strings (name/map/folder/game), a
// 2-byte app id, seven single-byte fields, and an empty version c-string.
func minimalA2SInfoBody() []byte {
	var b bytes.Buffer
	b.WriteByte(17) // protocol
	for i := 0; i < 4; i++ {
		b.WriteByte(0) // name, map, folder, game: empty c-strings
	}
	b.WriteByte(0) // app id low byte
	b.WriteByte(0) // app id high byte
	for i := 0; i < 7; i++ {
		b.WriteByte(0) // players, max players, bots, server type, environment, visibility, vac
	}
	b.WriteByte(0) // version: empty c-string
	return b.Bytes()
}

func procWithServerRunning(t testing.TB) string {
	t.Helper()
	root := fakeProc(t, map[string]struct{ comm, cmdline string }{
		"1234": {comm: "DayZServer"},
	})
	return root
}

func TestProbeSuccess(t *testing.T) {
	opts := Options{
		QueryAddr: fakeA2SServer(t),
		ProcRoot:  procWithServerRunning(t),
		Timeout:   2 * time.Second,
	}
	err := Probe(opts)
	require.NoError(t, err, "Probe")
}

func TestProbeRequiresQueryAddr(t *testing.T) {
	opts := Options{ProcRoot: procWithServerRunning(t)}
	err := Probe(opts)
	require.ErrorContains(t, err, "QueryAddr")
}

func TestProbeRequiresProcRoot(t *testing.T) {
	opts := Options{QueryAddr: fakeA2SServer(t)}
	err := Probe(opts)
	require.ErrorContains(t, err, "ProcRoot")
}

func TestProbeFailsWhenProcessNotRunning(t *testing.T) {
	opts := Options{
		QueryAddr: fakeA2SServer(t),
		ProcRoot:  fakeProc(t, map[string]struct{ comm, cmdline string }{"1": {comm: "bash"}}),
		Timeout:   time.Second,
	}
	err := Probe(opts)
	require.ErrorContains(t, err, "process not found")
}

func TestProbeFailsWhenQueryPortSilent(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	addr := conn.LocalAddr().String()
	_ = conn.Close() // nothing answers

	opts := Options{
		QueryAddr: addr,
		ProcRoot:  procWithServerRunning(t),
		Timeout:   200 * time.Millisecond,
	}
	err = Probe(opts)
	require.ErrorContains(t, err, "not answering")
}

func TestProbeWithRConSuccess(t *testing.T) {
	rconAddr, cleanup := fakeRConServer(t, true)
	defer cleanup()

	opts := Options{
		QueryAddr: fakeA2SServer(t),
		ProcRoot:  procWithServerRunning(t),
		Timeout:   2 * time.Second,
		RCon:      &RConProbe{Addr: rconAddr, Password: "pw", Timeout: 2 * time.Second},
	}
	err := Probe(opts)
	require.NoError(t, err, "Probe")
}

func TestProbeWithRConLoginFailure(t *testing.T) {
	rconAddr, cleanup := fakeRConServer(t, false)
	defer cleanup()

	opts := Options{
		QueryAddr: fakeA2SServer(t),
		ProcRoot:  procWithServerRunning(t),
		Timeout:   2 * time.Second,
		RCon:      &RConProbe{Addr: rconAddr, Password: "wrong", Timeout: 2 * time.Second},
	}
	err := Probe(opts)
	require.ErrorContains(t, err, "rcon login failed")
}

// fakeRConServer is a minimal BattlEye server: it accepts login (or not,
// per loginOK) and, if logged in, answers any Command with an empty OK
// response - just enough to exercise the version probe.
func fakeRConServer(t testing.TB, loginOK bool) (addr string, cleanup func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	go func() {
		buf := make([]byte, 4096)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			body := buf[6:n]
			if len(body) < 2 {
				continue
			}
			switch body[1] {
			case 0x00: // login
				result := byte(0x00)
				if loginOK {
					result = 0x01
				}
				writeBEPacket(conn, remote, 0x00, result)
			case 0x01: // command
				seq := body[2]
				writeBEPacket(conn, remote, 0x01, seq, 'o', 'k')
			}
		}
	}()
	return conn.LocalAddr().String(), func() { _ = conn.Close() }
}

func writeBEPacket(conn *net.UDPConn, remote *net.UDPAddr, typ byte, rest ...byte) {
	body := append([]byte{0xFF, typ}, rest...)
	crc := crc32.ChecksumIEEE(body)
	buf := make([]byte, 2+4+len(body))
	buf[0], buf[1] = 'B', 'E'
	buf[2] = byte(crc)
	buf[3] = byte(crc >> 8)
	buf[4] = byte(crc >> 16)
	buf[5] = byte(crc >> 24)
	copy(buf[6:], body)
	_, _ = conn.WriteToUDP(buf, remote)
}

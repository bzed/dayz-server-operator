// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package a2s

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer is a minimal A2S server: it always challenges the first
// request, then answers the second with a canned InfoResponse.
type fakeServer struct {
	t    testing.TB
	conn *net.UDPConn
	info InfoResponse
}

func newFakeServer(t testing.TB, info InfoResponse) *fakeServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = conn.Close() })
	s := &fakeServer{t: t, conn: conn, info: info}
	go s.serve()
	return s
}

func (s *fakeServer) addr() string { return s.conn.LocalAddr().String() }

func (s *fakeServer) serve() {
	buf := make([]byte, ReadBufferSize)
	seenChallenge := false
	for {
		n, remote, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := buf[:n]
		if !seenChallenge && len(req) == len(InfoRequest(nil)) {
			seenChallenge = true
			resp := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, responseKindChallenge}, 0xAA, 0xBB, 0xCC, 0xDD)
			if _, err := s.conn.WriteToUDP(resp, remote); err != nil {
				return
			}
			continue
		}
		resp := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, responseKindInfo}, buildInfoBody(s.t, s.info)...)
		if _, err := s.conn.WriteToUDP(resp, remote); err != nil {
			return
		}
	}
}

func TestQuerySuccess(t *testing.T) {
	want := InfoResponse{
		Protocol: 17, Name: "dzo", Map: "empty.deerisle", Folder: "dayz", Game: "DayZ",
		AppID: 2211, Players: 5, MaxPlayers: 60, ServerType: 'd', Environment: 'l',
		Version: "1.29",
	}
	s := newFakeServer(t, want)

	got, err := Query(s.addr(), 2*time.Second)
	require.NoError(t, err, "Query")
	assert.Equal(t, want, got)
}

func TestQueryTimeout(t *testing.T) {
	// Nothing listens here.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	addr := conn.LocalAddr().String()
	_ = conn.Close()

	_, err = Query(addr, 200*time.Millisecond)
	require.Error(t, err, "expected an error querying a closed port")
}

func TestQueryBadAddress(t *testing.T) {
	_, err := Query("not-an-address", time.Second)
	require.Error(t, err, "expected an error for an unresolvable address")
}

func TestQueryDirectInfoNoChallenge(t *testing.T) {
	// Some servers might answer directly without challenging; Query must
	// accept that too.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = conn.Close() })

	want := InfoResponse{Name: "direct", Map: "m", Folder: "f", Game: "g", Version: "v"}
	go func() {
		buf := make([]byte, ReadBufferSize)
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		_ = n
		resp := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, responseKindInfo}, buildInfoBody(t, want)...)
		_, _ = conn.WriteToUDP(resp, remote)
	}()

	got, err := Query(conn.LocalAddr().String(), 2*time.Second)
	require.NoError(t, err, "Query")
	assert.Equal(t, want, got)
}

func TestQueryUnexpectedResponseType(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, ReadBufferSize)
		_, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		resp := []byte{0xFF, 0xFF, 0xFF, 0xFF, 'X'}
		_, _ = conn.WriteToUDP(resp, remote)
	}()

	_, err = Query(conn.LocalAddr().String(), 2*time.Second)
	require.Error(t, err, "expected an error for an unexpected response type")
}

func TestQueryBadChallengeThenBadFollowup(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, ReadBufferSize)
		count := 0
		for {
			_, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			count++
			if count == 1 {
				resp := []byte{0xFF, 0xFF, 0xFF, 0xFF, responseKindChallenge, 1, 2, 3, 4}
				_, _ = conn.WriteToUDP(resp, remote)
				continue
			}
			// second round: answer with a challenge again (protocol
			// violation from the server's side) instead of info.
			resp := []byte{0xFF, 0xFF, 0xFF, 0xFF, responseKindChallenge, 1, 2, 3, 4}
			_, _ = conn.WriteToUDP(resp, remote)
		}
	}()

	_, err = Query(conn.LocalAddr().String(), 2*time.Second)
	require.Error(t, err, "expected an error when the server never sends an info response")
}

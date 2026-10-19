// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answeringServer is a fake RCon server that logs everyone in and answers every command with
// "echo:<command>"; kill makes it vanish (a restart of the game server).
type answeringServer struct {
	conn *net.UDPConn
	dead atomic.Bool
	wg   sync.WaitGroup
}

func newAnswering(t testing.TB, keepAlives bool) *answeringServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	s := &answeringServer{conn: conn}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		buf := make([]byte, ReadBufferSize)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			body, err := unwrap(buf[:n])
			if err != nil || s.dead.Load() {
				continue
			}
			switch PacketType(body[1]) {
			case PacketLogin:
				_, _ = conn.WriteToUDP(serverPacket(PacketLogin, 0x01), from)
			case PacketCommand:
				seq, cmd := body[2], string(body[3:])
				if cmd == "" && !keepAlives {
					continue
				}
				_, _ = conn.WriteToUDP(serverPacket(PacketCommand, append([]byte{seq}, []byte("echo:"+cmd)...)...), from)
			}
		}
	}()
	t.Cleanup(func() { _ = conn.Close(); s.wg.Wait() })
	return s
}

func (s *answeringServer) addr() string { return s.conn.LocalAddr().String() }

func TestSessionCommandsAndReconnect(t *testing.T) {
	a := newAnswering(t, true)
	b := newAnswering(t, true)
	var addrMu sync.Mutex
	addrs := []string{a.addr(), b.addr()}
	var dials atomic.Int32
	s := &Session{
		Addr: "ignored", Password: "pw", MinBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
		Options: []Option{WithKeepAliveInterval(20 * time.Millisecond), WithLivenessTimeout(100 * time.Millisecond), WithLoginTimeout(time.Second)},
		Dial: func(_, pw string, opts ...Option) (*Client, error) {
			addrMu.Lock()
			defer addrMu.Unlock()
			i := int(dials.Add(1)) - 1
			if i >= len(addrs) {
				i = len(addrs) - 1
			}
			return Dial(addrs[i], pw, opts...)
		},
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out, err := s.Command(ctx, "players")
	require.NoError(t, err)
	require.Equal(t, "echo:players", out)
	assert.True(t, s.Connected(), "the session is connected")

	// The server goes silent: the liveness timeout notices, the session dials the next address.
	a.dead.Store(true)
	require.Eventually(t, func() bool { return dials.Load() >= 2 }, 3*time.Second, 10*time.Millisecond, "the session did not dial again after the server went silent")
	out, err = s.Command(ctx, "lock")
	require.NoError(t, err)
	require.Equal(t, "echo:lock", out)
}

func TestSessionRetriesAfterAFailedDialAndForwardsEvents(t *testing.T) {
	srv := newAnswering(t, true)
	var tries atomic.Int32
	s := &Session{
		Addr: "x", Password: "pw", MinBackoff: 5 * time.Millisecond, MaxBackoff: 10 * time.Millisecond,
		Options: []Option{WithLoginTimeout(time.Second)},
		Dial: func(_, pw string, opts ...Option) (*Client, error) {
			if tries.Add(1) < 3 {
				return nil, errors.New("not up yet")
			}
			return Dial(srv.addr(), pw, opts...)
		},
	}
	var logged atomic.Int32
	s.Log = func(string, ...any) { logged.Add(1) }
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// Command waits for the connection that is not there yet.
	out, err := s.Command(ctx, "players")
	require.NoError(t, err)
	require.Equal(t, "echo:players", out)
	require.Equal(t, int32(3), tries.Load(), "two failures, then the connection")
	assert.GreaterOrEqual(t, logged.Load(), int32(3), "two failures, then the connection")
	// An event of the server reaches Events (the connect message of a player).
	srv.conn.SetWriteDeadline(time.Now().Add(time.Second)) //nolint:errcheck // best effort
	s.mu.Lock()
	c := s.cur
	s.mu.Unlock()
	select {
	case c.events <- Event{Kind: EventConnect}:
	default:
		require.Fail(t, "event channel full")
	}
	select {
	case ev := <-s.Events():
		assert.Equal(t, EventConnect, ev.Kind)
	case <-time.After(2 * time.Second):
		require.Fail(t, "no event forwarded")
	}
}

func TestSessionCloseAndContext(t *testing.T) {
	s := &Session{Addr: "x", Password: "pw", MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond, Dial: func(string, string, ...Option) (*Client, error) { return nil, errors.New("down") }}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Command(ctx, "players")
	require.ErrorIs(t, err, context.DeadlineExceeded, "a command with no connection waits for its context")
	err = s.Close()
	require.NoError(t, err)
	err = s.Close()
	require.NoError(t, err, "a second Close is fine")
	_, err = s.Command(t.Context(), "players")
	require.ErrorIs(t, err, ErrSessionClosed, "after Close")
	assert.False(t, s.Connected(), "closed sessions are not connected")
}

func TestSessionBackoffDefaults(t *testing.T) {
	min, max := (&Session{}).backoff()
	require.Equal(t, time.Second, min, "defaults = %s %s", min, max)
	assert.Equal(t, 30*time.Second, max, "defaults = %s %s", min, max)
	min, max = (&Session{MinBackoff: time.Minute}).backoff()
	require.Equal(t, time.Minute, min, "max below min is raised to min: %s %s", min, max)
	assert.Equal(t, time.Minute, max, "max below min is raised to min: %s %s", min, max)
}

func TestSessionWaitConnected(t *testing.T) {
	srv := newAnswering(t, true)
	s := &Session{Addr: srv.addr(), Password: "pw", Options: []Option{WithLoginTimeout(time.Second)}}
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := s.WaitConnected(ctx)
	require.NoError(t, err)
	down := &Session{Addr: "127.0.0.1:1", Password: "pw", MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond, Options: []Option{WithLoginTimeout(50 * time.Millisecond)}}
	defer func() { _ = down.Close() }()
	short, cancel2 := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel2()
	err = down.WaitConnected(short)
	require.ErrorIs(t, err, context.DeadlineExceeded, "an unreachable server")
}

func TestSessionCommandTimesOutInsteadOfHanging(t *testing.T) {
	var asked atomic.Int32
	// A server that logs in and then never answers a command.
	silent := newFakeServer(t)
	go func() {
		silent.recv() // login
		silent.send(PacketLogin, 0x01)
		for {
			if _, err := silent.tryRecv(); err != nil {
				return
			}
			asked.Add(1) // never answers
		}
	}()
	s := &Session{Addr: silent.addr(), Password: "pw", CommandTimeout: 50 * time.Millisecond, Options: []Option{WithLoginTimeout(time.Second), WithKeepAliveInterval(time.Hour)}}
	defer func() { _ = s.Close() }()
	start := time.Now()
	_, err := s.Command(t.Context(), "silent")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	took := time.Since(start)
	assert.LessOrEqual(t, took, 2*time.Second)
	n := asked.Load()
	assert.Equal(t, int32(3), n)
}

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
)

// answeringServer is a fake RCon server that logs everyone in and answers every command with
// "echo:<command>"; kill makes it vanish (a restart of the game server).
type answeringServer struct {
	conn *net.UDPConn
	dead atomic.Bool
	wg   sync.WaitGroup
}

func newAnswering(t *testing.T, keepAlives bool) *answeringServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := s.Command(ctx, "players")
	if err != nil || out != "echo:players" {
		t.Fatalf("first command: %q %v", out, err)
	}
	if !s.Connected() {
		t.Error("the session is connected")
	}

	// The server goes silent: the liveness timeout notices, the session dials the next address.
	a.dead.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for dials.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if dials.Load() < 2 {
		t.Fatal("the session did not dial again after the server went silent")
	}
	out, err = s.Command(ctx, "lock")
	if err != nil || out != "echo:lock" {
		t.Fatalf("command after the reconnect: %q %v", out, err)
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Command waits for the connection that is not there yet.
	if out, err := s.Command(ctx, "players"); err != nil || out != "echo:players" {
		t.Fatalf("command: %q %v", out, err)
	}
	if tries.Load() != 3 || logged.Load() < 3 {
		t.Errorf("tries=%d logged=%d: two failures, then the connection", tries.Load(), logged.Load())
	}
	// An event of the server reaches Events (the connect message of a player).
	srv.conn.SetWriteDeadline(time.Now().Add(time.Second)) //nolint:errcheck // best effort
	s.mu.Lock()
	c := s.cur
	s.mu.Unlock()
	select {
	case c.events <- Event{Kind: EventConnect}:
	default:
		t.Fatal("event channel full")
	}
	select {
	case ev := <-s.Events():
		if ev.Kind != EventConnect {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event forwarded")
	}
}

func TestSessionCloseAndContext(t *testing.T) {
	s := &Session{Addr: "x", Password: "pw", MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond, Dial: func(string, string, ...Option) (*Client, error) { return nil, errors.New("down") }}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Command(ctx, "players"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a command with no connection waits for its context: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("a second Close is fine: %v", err)
	}
	if _, err := s.Command(context.Background(), "players"); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("after Close: %v", err)
	}
	if s.Connected() {
		t.Error("closed sessions are not connected")
	}
}

func TestSessionBackoffDefaults(t *testing.T) {
	min, max := (&Session{}).backoff()
	if min != time.Second || max != 30*time.Second {
		t.Errorf("defaults = %s %s", min, max)
	}
	if min, max = (&Session{MinBackoff: time.Minute}).backoff(); min != time.Minute || max != time.Minute {
		t.Errorf("max below min is raised to min: %s %s", min, max)
	}
}

func TestSessionWaitConnected(t *testing.T) {
	srv := newAnswering(t, true)
	s := &Session{Addr: srv.addr(), Password: "pw", Options: []Option{WithLoginTimeout(time.Second)}}
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.WaitConnected(ctx); err != nil {
		t.Fatal(err)
	}
	down := &Session{Addr: "127.0.0.1:1", Password: "pw", MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond, Options: []Option{WithLoginTimeout(50 * time.Millisecond)}}
	defer func() { _ = down.Close() }()
	short, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if err := down.WaitConnected(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("an unreachable server: %v", err)
	}
}

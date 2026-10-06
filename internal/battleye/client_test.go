// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

// fakeServer is a minimal BattlEye RCon server used to test Client without
// a real DayZ server (C15: "RCon protocol (fake UDP server)").
type fakeServer struct {
	t      *testing.T
	conn   *net.UDPConn
	remote *net.UDPAddr
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &fakeServer{t: t, conn: conn}
}

func (s *fakeServer) addr() string { return s.conn.LocalAddr().String() }

// recv reads one client packet and returns its validated body (from the
// 0xFF marker on), failing the test on any error. Only call this from the
// test's own goroutine (see tryRecv for use from a spawned goroutine:
// t.Fatal must run in the test's own goroutine, staticcheck SA2002).
func (s *fakeServer) recv() []byte {
	s.t.Helper()
	body, err := s.tryRecv()
	if err != nil {
		s.t.Fatal(err)
	}
	return body
}

// tryRecv is recv without calling t.Fatal, safe to use from a goroutine
// that only ever consumes a packet without asserting on it (e.g. a fake
// server that deliberately never replies).
func (s *fakeServer) tryRecv() ([]byte, error) {
	buf := make([]byte, ReadBufferSize)
	if err := s.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, fmt.Errorf("SetReadDeadline: %w", err)
	}
	n, addr, err := s.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, fmt.Errorf("recv: %w", err)
	}
	s.remote = addr
	body, err := unwrap(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("recv: invalid packet: %w", err)
	}
	return body, nil
}

func (s *fakeServer) send(typ PacketType, rest ...byte) {
	s.t.Helper()
	if s.remote == nil {
		s.t.Fatal("send called before recv learned the client address")
	}
	if _, err := s.conn.WriteToUDP(serverPacket(typ, rest...), s.remote); err != nil {
		s.t.Fatalf("send: %v", err)
	}
}

func dialFake(t *testing.T, s *fakeServer, password string, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{WithLoginTimeout(2 * time.Second)}, opts...)
	c, err := Dial(s.addr(), password, opts...)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestDialLoginSuccess(t *testing.T) {
	s := newFakeServer(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		body := s.recv()
		if body[1] != byte(PacketLogin) {
			t.Errorf("expected a Login packet, got type %#x", body[1])
		}
		if string(body[2:]) != "hunter2" {
			t.Errorf("password = %q, want hunter2", body[2:])
		}
		s.send(PacketLogin, 0x01)
	}()

	c := dialFake(t, s, "hunter2")
	<-done
	if c == nil {
		t.Fatal("expected a client")
	}
}

func TestDialLoginFailure(t *testing.T) {
	s := newFakeServer(t)
	go func() {
		s.recv()
		s.send(PacketLogin, 0x00)
	}()

	_, err := Dial(s.addr(), "wrong", WithLoginTimeout(2*time.Second))
	if err != ErrLoginFailed {
		t.Fatalf("err = %v, want ErrLoginFailed", err)
	}
}

func TestDialLoginTimeout(t *testing.T) {
	s := newFakeServer(t)
	// Server never responds.
	_, err := Dial(s.addr(), "whatever", WithLoginTimeout(100*time.Millisecond))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func acceptLogin(t *testing.T, s *fakeServer) {
	t.Helper()
	s.recv()
	s.send(PacketLogin, 0x01)
}

func TestCommandSinglePacketResponse(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	go func() {
		body := s.recv()
		if body[1] != byte(PacketCommand) {
			t.Errorf("expected a Command packet, got %#x", body[1])
		}
		seq := body[2]
		if string(body[3:]) != "players" {
			t.Errorf("command = %q, want players", body[3:])
		}
		s.send(PacketCommand, seq, 'o', 'k')
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "players")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if resp != "ok" {
		t.Errorf("resp = %q, want ok", resp)
	}
}

func TestCommandMultipartResponse(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	go func() {
		body := s.recv()
		seq := body[2]
		s.send(PacketCommand, seq, 0x00, 2, 0, 'h', 'e')
		s.send(PacketCommand, seq, 0x00, 2, 1, 'l', 'l', 'o')
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "bigcommand")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if resp != "hello" {
		t.Errorf("resp = %q, want hello", resp)
	}
}

func TestCommandMultipartOutOfOrder(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	go func() {
		body := s.recv()
		seq := body[2]
		// Send part 1 before part 0.
		s.send(PacketCommand, seq, 0x00, 2, 1, 'l', 'l', 'o')
		s.send(PacketCommand, seq, 0x00, 2, 0, 'h', 'e')
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "bigcommand")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if resp != "hello" {
		t.Errorf("resp = %q, want hello", resp)
	}
}

func TestCommandContextTimeout(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	go func() { _, _ = s.tryRecv() }() // consume the command, never reply

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Command(ctx, "slow")
	if err == nil {
		t.Fatal("expected a context deadline error")
	}
}

func TestEventsAndAck(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	go func() {
		payload := append([]byte{5}, []byte("Player #1 Bob (203.0.113.1:2302) connected")...)
		s.send(PacketMessage, payload...)
	}()

	ack := s.recv()
	if ack[1] != byte(PacketMessage) || ack[2] != 5 {
		t.Fatalf("ack body = %v, want message ack for seq 5", ack)
	}

	select {
	case e := <-c.Events():
		if e.Kind != EventConnect || e.PlayerName != "Bob" {
			t.Errorf("Event = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the event")
	}
}

func TestKeepAliveSendsEmptyCommand(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw", WithKeepAliveInterval(20*time.Millisecond))

	body := s.recv()
	if body[1] != byte(PacketCommand) {
		t.Fatalf("expected a keep-alive Command packet, got %#x", body[1])
	}
	if len(body) != 3 {
		t.Fatalf("expected an empty command payload, got %d bytes", len(body)-3)
	}
	_ = c
}

func TestConnectionLostFailsPendingCommand(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	go func() { _, _ = s.tryRecv() }() // consume the command, never reply

	done := make(chan struct{})
	var cmdErr error
	go func() {
		defer close(done)
		_, cmdErr = c.Command(context.Background(), "players")
	}()

	// Simulate the socket disappearing out from under the read loop,
	// without going through the normal Close() path.
	time.Sleep(50 * time.Millisecond)
	_ = c.conn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Command did not return after the connection was lost")
	}
	if cmdErr == nil {
		t.Fatal("expected an error when the connection is lost")
	}
}

func TestDialMalformedLoginResponse(t *testing.T) {
	s := newFakeServer(t)
	go func() {
		s.recv()
		// Not a valid BE packet at all (no 'B','E' header) - login must
		// surface a decode error, not hang until the timeout.
		if _, err := s.conn.WriteToUDP([]byte("garbage"), s.remote); err != nil {
			t.Errorf("write garbage: %v", err)
		}
	}()

	start := time.Now()
	_, err := Dial(s.addr(), "pw", WithLoginTimeout(2*time.Second))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a decode error for a malformed login response")
	}
	if elapsed > time.Second {
		t.Errorf("Dial took %v, want it to fail fast on a decode error rather than wait out the login timeout", elapsed)
	}
}

func TestDialResolveFailure(t *testing.T) {
	if _, err := Dial("not a valid address", "pw"); err == nil {
		t.Fatal("expected a resolve error")
	}
}

func TestDialConnectionRefused(t *testing.T) {
	// Nothing listens here; on Linux this reliably surfaces as ECONNREFUSED
	// on the next read, well before the login timeout - but the test only
	// asserts an error occurs, not the exact latency, in case that varies
	// by platform/sandbox.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := conn.LocalAddr().String()
	_ = conn.Close()

	if _, err := Dial(addr, "pw", WithLoginTimeout(2*time.Second)); err == nil {
		t.Fatal("expected an error dialing an address nothing listens on")
	}
}

func TestCommandAlreadyExpiredContext(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")
	go func() { _, _ = s.tryRecv() }() // consume the command, never reply

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done() // guarantee it is already expired before Command is even called

	_, err := c.Command(ctx, "players")
	if err == nil {
		t.Fatal("expected an error for an already-expired context")
	}
}

func TestCommandContextCancelled(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")
	go func() { _, _ = s.tryRecv() }() // consume the command, never reply

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Command(ctx, "players"); done <- err }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Command did not return after ctx was cancelled")
	}
}

func TestUnsolicitedResponseForUnknownSeqIsDroppedSafely(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	// Send a Command-type response for a seq nobody registered (e.g. a
	// stale/duplicate packet, or a response that arrived after the
	// original Command already gave up). The client must drop it without
	// panicking, and subsequent real commands must still work.
	go func() {
		body := s.recv()
		seq := body[2]
		s.send(PacketCommand, 200, 'b', 'o', 'g', 'u', 's') // unrelated seq, never requested
		s.send(PacketCommand, seq, 'o', 'k')
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "players")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if resp != "ok" {
		t.Errorf("resp = %q, want ok (the bogus packet must not have disrupted it)", resp)
	}
}

func TestMalformedServerMessageDroppedWithoutCrashing(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	// A Message-type packet with no sequence byte (too short to decode).
	go func() {
		if _, err := s.conn.WriteToUDP(wrap([]byte{packetMarker, byte(PacketMessage)}), s.remote); err != nil {
			t.Errorf("write malformed message: %v", err)
		}
	}()

	// The client must keep working afterwards: a real command should
	// still complete normally.
	time.Sleep(50 * time.Millisecond)
	go func() {
		body := s.recv()
		s.send(PacketCommand, body[2], 'o', 'k')
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Command(ctx, "players"); err != nil {
		t.Fatalf("Command after malformed message: %v", err)
	}
}

func TestKeepAliveStopsAfterClose(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c, err := Dial(s.addr(), "pw", WithLoginTimeout(2*time.Second), WithKeepAliveInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// No further keep-alives should arrive; give any in-flight tick a
	// window to (incorrectly) fire, then confirm nothing shows up.
	_, err = s.tryRecv()
	if err == nil {
		t.Error("expected no further packets after Close, but one arrived")
	}
}

func TestConcurrentCommandsGetDistinctSequenceNumbers(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	const n = 20
	seen := make(chan byte, n)
	go func() {
		for i := 0; i < n; i++ {
			body := s.recv()
			seq := body[2]
			seen <- seq
			s.send(PacketCommand, seq, 'o', 'k')
		}
	}()

	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := c.Command(ctx, "players")
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("Command: %v", err)
		}
	}

	// Read exactly n values rather than closing seen from this goroutine:
	// the server goroutine (not this one) is what writes to it, and
	// closing it here without a real synchronization edge back to that
	// goroutine's last write is a data race the race detector correctly
	// flags, even though the UDP round-trip makes it logically safe.
	seqs := map[byte]bool{}
	for i := 0; i < n; i++ {
		seq := <-seen
		if seqs[seq] {
			t.Errorf("sequence number %d used more than once among %d concurrent commands", seq, n)
		}
		seqs[seq] = true
	}
}

func TestCloseIsIdempotentAndRejectsCommands(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c, err := Dial(s.addr(), "pw", WithLoginTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	_, err = c.Command(context.Background(), "players")
	if err != ErrClosed {
		t.Fatalf("Command after Close: err = %v, want ErrClosed", err)
	}

	if _, open := <-c.Events(); open {
		t.Error("Events() channel should be closed after Close()")
	}
}

func TestLivenessTimeoutMarksTheConnectionLost(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw", WithKeepAliveInterval(10*time.Millisecond), WithLivenessTimeout(40*time.Millisecond))
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a server that answers nothing must be declared lost")
	}
	if _, err := c.Command(context.Background(), "players"); !errors.Is(err, ErrConnectionLost) {
		t.Errorf("a command on a lost connection: %v", err)
	}
}

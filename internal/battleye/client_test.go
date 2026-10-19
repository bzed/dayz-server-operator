// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer is a minimal BattlEye RCon server used to test Client without
// a real DayZ server (C15: "RCon protocol (fake UDP server)").
type fakeServer struct {
	t      testing.TB
	conn   *net.UDPConn
	remote *net.UDPAddr
}

func newFakeServer(t testing.TB) *fakeServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = conn.Close() })
	return &fakeServer{t: t, conn: conn}
}

func (s *fakeServer) addr() string { return s.conn.LocalAddr().String() }

// recv reads one client packet and returns its validated body (from the
// 0xFF marker on), failing the test on any error. Only call this from the
// test's own goroutine (see tryRecv for use from a spawned goroutine:
// require must run in the test's own goroutine, testifylint go-require).
func (s *fakeServer) recv() []byte {
	s.t.Helper()
	body, err := s.tryRecv()
	require.NoError(s.t, err)
	return body
}

// tryRecv is recv without failing the test, safe to use from a goroutine
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
	require.NotNil(s.t, s.remote, "send called before recv learned the client address")
	_, err := s.conn.WriteToUDP(serverPacket(typ, rest...), s.remote)
	require.NoError(s.t, err, "send")
}

func dialFake(t testing.TB, s *fakeServer, password string, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{WithLoginTimeout(2 * time.Second)}, opts...)
	c, err := Dial(s.addr(), password, opts...)
	require.NoError(t, err, "Dial")
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestDialLoginSuccess(t *testing.T) {
	s := newFakeServer(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		body := s.recv()
		assert.Equal(t, byte(PacketLogin), body[1])
		assert.Equal(t, "hunter2", string(body[2:]))
		s.send(PacketLogin, 0x01)
	}()

	c := dialFake(t, s, "hunter2")
	<-done
	require.NotNil(t, c)
}

func TestDialLoginFailure(t *testing.T) {
	s := newFakeServer(t)
	go func() {
		s.recv()
		s.send(PacketLogin, 0x00)
	}()

	_, err := Dial(s.addr(), "wrong", WithLoginTimeout(2*time.Second))
	require.Equal(t, ErrLoginFailed, err)
}

func TestDialLoginTimeout(t *testing.T) {
	s := newFakeServer(t)
	// Server never responds.
	_, err := Dial(s.addr(), "whatever", WithLoginTimeout(100*time.Millisecond))
	require.Error(t, err)
}

func acceptLogin(t testing.TB, s *fakeServer) {
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
		assert.Equal(t, byte(PacketCommand), body[1])
		seq := body[2]
		assert.Equal(t, "players", string(body[3:]))
		s.send(PacketCommand, seq, 'o', 'k')
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "players")
	require.NoError(t, err, "Command")
	assert.Equal(t, "ok", resp)
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

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "bigcommand")
	require.NoError(t, err, "Command")
	assert.Equal(t, "hello", resp)
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

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "bigcommand")
	require.NoError(t, err, "Command")
	assert.Equal(t, "hello", resp)
}

func TestCommandContextTimeout(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	go func() { _, _ = s.tryRecv() }() // consume the command, never reply

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Command(ctx, "slow")
	require.Error(t, err)
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
	require.Equal(t, byte(PacketMessage), ack[1])
	require.Equal(t, uint8(5), ack[2])

	select {
	case e := <-c.Events():
		require.Equal(t, EventConnect, e.Kind)
		assert.Equal(t, "Bob", e.PlayerName)
	case <-time.After(2 * time.Second):
		require.Fail(t, "timed out waiting for the event")
	}
}

func TestKeepAliveSendsEmptyCommand(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw", WithKeepAliveInterval(20*time.Millisecond))

	body := s.recv()
	require.Equal(t, byte(PacketCommand), body[1])
	require.Len(t, body, 3)
	_ = c
}

func TestConnectionLostFailsPendingCommand(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	consumed := make(chan struct{})
	go func() { _, _ = s.tryRecv(); close(consumed) }() // consume the command, never reply

	done := make(chan struct{})
	var cmdErr error
	go func() {
		defer close(done)
		_, cmdErr = c.Command(t.Context(), "players")
	}()

	// Simulate the socket disappearing out from under the read loop,
	// without going through the normal Close() path.
	<-consumed
	_ = c.conn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "Command did not return after the connection was lost")
	}
	require.Error(t, cmdErr)
}

func TestDialMalformedLoginResponse(t *testing.T) {
	s := newFakeServer(t)
	go func() {
		s.recv()
		// Not a valid BE packet at all (no 'B','E' header) - login must
		// surface a decode error, not hang until the timeout.
		_, err := s.conn.WriteToUDP([]byte("garbage"), s.remote)
		assert.NoError(t, err, "write garbage")
	}()

	start := time.Now()
	_, err := Dial(s.addr(), "pw", WithLoginTimeout(2*time.Second))
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.LessOrEqual(t, elapsed, time.Second)
}

func TestDialResolveFailure(t *testing.T) {
	_, err := Dial("not a valid address", "pw")
	require.Error(t, err)
}

func TestDialConnectionRefused(t *testing.T) {
	// Nothing listens here; on Linux this reliably surfaces as ECONNREFUSED
	// on the next read, well before the login timeout - but the test only
	// asserts an error occurs, not the exact latency, in case that varies
	// by platform/sandbox.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	addr := conn.LocalAddr().String()
	_ = conn.Close()

	_, err = Dial(addr, "pw", WithLoginTimeout(2*time.Second))
	require.Error(t, err)
}

func TestCommandAlreadyExpiredContext(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")
	go func() { _, _ = s.tryRecv() }() // consume the command, never reply

	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	<-ctx.Done() // guarantee it is already expired before Command is even called

	_, err := c.Command(ctx, "players")
	require.Error(t, err)
}

func TestCommandContextCancelled(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")
	consumed := make(chan struct{})
	go func() { _, _ = s.tryRecv(); close(consumed) }() // consume the command, never reply

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := c.Command(ctx, "players"); done <- err }()

	<-consumed
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		require.Fail(t, "Command did not return after ctx was cancelled")
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

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resp, err := c.Command(ctx, "players")
	require.NoError(t, err, "Command")
	assert.Equal(t, "ok", resp)
}

func TestMalformedServerMessageDroppedWithoutCrashing(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw")

	// A Message-type packet with no sequence byte (too short to decode).
	_, err := s.conn.WriteToUDP(wrap([]byte{packetMarker, byte(PacketMessage)}), s.remote)
	require.NoError(t, err, "write malformed message")

	// The client must keep working afterwards: a real command should
	// still complete normally.
	go func() {
		body := s.recv()
		s.send(PacketCommand, body[2], 'o', 'k')
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = c.Command(ctx, "players")
	require.NoError(t, err, "Command after malformed message")
}

func TestKeepAliveStopsAfterClose(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c, err := Dial(s.addr(), "pw", WithLoginTimeout(2*time.Second), WithKeepAliveInterval(10*time.Millisecond))
	require.NoError(t, err, "Dial")

	err = c.Close()
	require.NoError(t, err, "Close")
	// No further keep-alives should arrive; give any in-flight tick a
	// window to (incorrectly) fire, then confirm nothing shows up.
	_, err = s.tryRecv()
	require.Error(t, err)
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
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			_, err := c.Command(ctx, "players")
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		err := <-errs
		require.NoError(t, err, "Command")
	}

	// Read exactly n values rather than closing seen from this goroutine:
	// the server goroutine (not this one) is what writes to it, and
	// closing it here without a real synchronization edge back to that
	// goroutine's last write is a data race the race detector correctly
	// flags, even though the UDP round-trip makes it logically safe.
	seqs := map[byte]bool{}
	for i := 0; i < n; i++ {
		seq := <-seen
		assert.False(t, seqs[seq], "sequence number %d used more than once among %d concurrent commands", seq, n)
		seqs[seq] = true
	}
}

func TestCloseIsIdempotentAndRejectsCommands(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c, err := Dial(s.addr(), "pw", WithLoginTimeout(2*time.Second))
	require.NoError(t, err, "Dial")

	err = c.Close()
	require.NoError(t, err, "Close")
	err = c.Close()
	require.NoError(t, err, "second Close")

	_, err = c.Command(t.Context(), "players")
	require.Equal(t, ErrClosed, err)

	_, open := <-c.Events()
	assert.False(t, open, "Events() channel should be closed after Close()")
}

func TestLivenessTimeoutMarksTheConnectionLost(t *testing.T) {
	s := newFakeServer(t)
	go acceptLogin(t, s)
	c := dialFake(t, s, "pw", WithKeepAliveInterval(10*time.Millisecond), WithLivenessTimeout(40*time.Millisecond))
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		require.Fail(t, "a server that answers nothing must be declared lost")
	}
	_, err := c.Command(t.Context(), "players")
	require.ErrorIs(t, err, ErrConnectionLost, "a command on a lost connection")
}

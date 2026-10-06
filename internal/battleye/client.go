// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// KeepAliveInterval is how often the client sends an empty Command packet
// to keep the RCon connection alive. The server times the connection out
// after ~45s of silence; this stays comfortably under that.
const KeepAliveInterval = 30 * time.Second

// ReadBufferSize is large enough for any single BattlEye RCon UDP packet.
const ReadBufferSize = 4096

// ErrLoginFailed is returned by Dial when the server rejects the password.
var ErrLoginFailed = errors.New("battleye: login failed")

// ErrClosed is returned by Command when the client has been closed.
var ErrClosed = errors.New("battleye: client closed")

// ErrConnectionLost is returned (wrapped) by a Command that was pending when the connection to the
// server was lost, and by every later one on the same Client. A Session dials again.
var ErrConnectionLost = errors.New("battleye: connection lost")

type commandResult struct {
	payload []byte
	err     error
}

type multipartAssembly struct {
	total  int
	chunks [][]byte
	got    int
}

// Client is a single BattlEye RCon connection: one Dial, one login handshake,
// then Command calls until Close. When the connection is lost (a socket error,
// or silence for longer than WithLivenessTimeout) every pending and later
// Command fails with ErrConnectionLost and Done is closed; the Client does not
// dial again. Session is the long-lived layer that does, with backoff. The
// protocol was checked against a real DayZServer 1.29 and 1.30 (TestLiveServer):
// players, an unknown command, #lock/#unlock/#kick/#shutdown, and that the server
// answers every keep-alive.
type Client struct {
	conn *net.UDPConn

	mu        sync.Mutex
	nextSeq   byte
	pending   map[byte]chan commandResult
	multipart map[byte]*multipartAssembly
	closed    bool

	events chan Event

	keepAliveInterval time.Duration
	loginTimeout      time.Duration

	// livenessTimeout, when positive, declares the connection lost when the server sends nothing
	// for that long (it answers every keep-alive, so silence means it is gone or restarted).
	livenessTimeout time.Duration
	lastRX          atomic.Int64 // unix nanoseconds of the last packet from the server

	lost     chan struct{} // closed when the connection is lost
	lostOnce sync.Once

	stop chan struct{}
	wg   sync.WaitGroup
}

// Done is closed when the connection to the server is lost (a read error, or silence for longer
// than WithLivenessTimeout). Close does not close it.
func (c *Client) Done() <-chan struct{} { return c.lost }

func (c *Client) markLost(err error) {
	c.lostOnce.Do(func() {
		c.failAllPending(fmt.Errorf("%w: %v", ErrConnectionLost, err))
		close(c.lost)
	})
}

func (c *Client) lastRXTime() time.Time { return time.Unix(0, c.lastRX.Load()) }

// Option customises a Client created by Dial.
type Option func(*Client)

// WithKeepAliveInterval overrides KeepAliveInterval, mainly for tests.
func WithKeepAliveInterval(d time.Duration) Option {
	return func(c *Client) { c.keepAliveInterval = d }
}

// WithLivenessTimeout makes the client declare the connection lost (Done is closed, pending
// commands fail with ErrConnectionLost) when the server sends nothing for d. The server answers
// the keep-alives, so this notices a server that restarted or went away without any socket error.
// It needs d to be well above the keep-alive interval.
func WithLivenessTimeout(d time.Duration) Option {
	return func(c *Client) { c.livenessTimeout = d }
}

// WithLoginTimeout overrides the 5s default login handshake timeout, mainly
// for tests.
func WithLoginTimeout(d time.Duration) Option {
	return func(c *Client) { c.loginTimeout = d }
}

// Dial connects to a BattlEye RCon endpoint and performs the login
// handshake. On success it starts the background read and keep-alive
// loops; the caller must call Close when done.
func Dial(addr, password string, opts ...Option) (*Client, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("battleye: resolve %s: %w", addr, err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("battleye: dial %s: %w", addr, err)
	}

	c := &Client{
		conn:              conn,
		pending:           map[byte]chan commandResult{},
		multipart:         map[byte]*multipartAssembly{},
		events:            make(chan Event, 64),
		keepAliveInterval: KeepAliveInterval,
		loginTimeout:      5 * time.Second,
		stop:              make(chan struct{}),
		lost:              make(chan struct{}),
	}
	c.lastRX.Store(time.Now().UnixNano())
	for _, opt := range opts {
		opt(c)
	}

	if err := c.login(password); err != nil {
		_ = conn.Close()
		return nil, err
	}

	c.wg.Add(2)
	go c.readLoop()
	go c.keepAliveLoop()
	return c, nil
}

func (c *Client) login(password string) error {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.loginTimeout)); err != nil {
		return fmt.Errorf("battleye: set read deadline: %w", err)
	}
	defer c.conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort deadline reset

	if _, err := c.conn.Write(EncodeLogin(password)); err != nil {
		return fmt.Errorf("battleye: send login: %w", err)
	}

	buf := make([]byte, ReadBufferSize)
	n, err := c.conn.Read(buf)
	if err != nil {
		return fmt.Errorf("battleye: read login response: %w", err)
	}
	ok, err := DecodeLoginResponse(buf[:n])
	if err != nil {
		return fmt.Errorf("battleye: decode login response: %w", err)
	}
	if !ok {
		return ErrLoginFailed
	}
	return nil
}

// Events returns the channel of parsed unsolicited Server Messages
// (connect/GUID/chat/kick, FR-22). The client acknowledges each one
// automatically; callers only observe them.
func (c *Client) Events() <-chan Event {
	return c.events
}

// Command sends an RCon command and waits for its (possibly reassembled)
// response, or ctx's deadline/cancellation.
func (c *Client) Command(ctx context.Context, command string) (string, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", ErrClosed
	}
	seq := c.nextSeq
	c.nextSeq++
	result := make(chan commandResult, 1)
	c.pending[seq] = result
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, seq)
		delete(c.multipart, seq)
		c.mu.Unlock()
	}()

	if _, err := c.conn.Write(EncodeCommand(seq, command)); err != nil {
		return "", fmt.Errorf("battleye: send command: %w", err)
	}

	select {
	case res := <-result:
		if res.err != nil {
			return "", res.err
		}
		return string(res.payload), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.lost:
		return "", ErrConnectionLost
	case <-c.stop:
		return "", ErrClosed
	}
}

// Close stops the background loops and closes the UDP socket.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	close(c.stop)
	err := c.conn.Close()
	c.wg.Wait()
	close(c.events)
	return err
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	buf := make([]byte, ReadBufferSize)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			select {
			case <-c.stop:
				return
			default:
			}
			c.markLost(err)
			return
		}
		c.lastRX.Store(time.Now().UnixNano())
		c.handlePacket(buf[:n])
	}
}

func (c *Client) handlePacket(packet []byte) {
	typ, err := PeekType(packet)
	if err != nil {
		return // malformed/corrupt packet: drop it, never crash the loop.
	}
	switch typ {
	case PacketCommand:
		c.handleCommandResponse(packet)
	case PacketMessage:
		c.handleServerMessage(packet)
	default:
		// Login responses after the handshake are unexpected; ignore.
	}
}

func (c *Client) handleCommandResponse(packet []byte) {
	seq, multipart, payload, err := DecodeCommandResponse(packet)
	if err != nil {
		return
	}
	// packet aliases readLoop's shared receive buffer, which the next
	// c.conn.Read overwrites as soon as this handler returns. Anything
	// kept beyond this call (multipart reassembly, the queued result) must
	// be copied out first.
	payload = append([]byte(nil), payload...)

	c.mu.Lock()
	ch, ok := c.pending[seq]
	if !ok {
		c.mu.Unlock()
		return // no one is waiting (e.g. a keep-alive), drop it.
	}

	var complete []byte
	if multipart == nil {
		complete = payload
	} else {
		asm, exists := c.multipart[seq]
		if !exists {
			asm = &multipartAssembly{total: multipart.Total, chunks: make([][]byte, multipart.Total)}
			c.multipart[seq] = asm
		}
		if multipart.Index < 0 || multipart.Index >= asm.total {
			c.mu.Unlock()
			return
		}
		if asm.chunks[multipart.Index] == nil {
			asm.chunks[multipart.Index] = payload
			asm.got++
		}
		if asm.got < asm.total {
			c.mu.Unlock()
			return
		}
		for _, chunk := range asm.chunks {
			complete = append(complete, chunk...)
		}
	}
	delete(c.pending, seq)
	c.mu.Unlock()

	ch <- commandResult{payload: complete}
}

func (c *Client) handleServerMessage(packet []byte) {
	seq, payload, err := DecodeServerMessage(packet)
	if err != nil {
		return
	}
	if _, err := c.conn.Write(EncodeMessageAck(seq)); err != nil {
		return
	}
	event := ParseEvent(string(payload))
	select {
	case c.events <- event:
	default:
		// Event channel is full; drop rather than block the read loop.
	}
}

func (c *Client) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for seq, ch := range c.pending {
		ch <- commandResult{err: err}
		delete(c.pending, seq)
	}
}

func (c *Client) keepAliveLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			seq := c.nextSeq
			c.nextSeq++
			c.mu.Unlock()
			if _, err := c.conn.Write(EncodeCommand(seq, "")); err != nil {
				c.markLost(err)
				return
			}
			if c.livenessTimeout > 0 {
				if quiet := time.Since(c.lastRXTime()); quiet > c.livenessTimeout {
					c.markLost(fmt.Errorf("no packet from the server for %s", quiet.Round(time.Second)))
					return
				}
			}
		case <-c.stop:
			return
		case <-c.lost:
			return
		}
	}
}

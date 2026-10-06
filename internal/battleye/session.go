// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Session is the long-lived RCon connection of one instance (§C8): it dials, keeps the Client,
// and dials again with backoff when the connection is lost, when the server restarts, or when it is
// not up yet. Callers share it: Command waits for a connection (until its context ends), and the
// event stream continues across reconnects.
type Session struct {
	Addr, Password string
	// Options are passed to every Dial. WithLivenessTimeout is added when none is given: a server
	// that restarted answers nothing on the old session.
	Options []Option
	// MinBackoff and MaxBackoff bound the wait between dial attempts (1s and 30s by default).
	MinBackoff, MaxBackoff time.Duration
	// Dial is Dial; tests replace it.
	Dial func(addr, password string, opts ...Option) (*Client, error)
	// CommandTimeout bounds the wait for one answer of one try, see Command (default 15s).
	CommandTimeout time.Duration
	// Log reports connects, losses and failed attempts.
	Log func(format string, args ...any)

	once   sync.Once
	mu     sync.Mutex
	cur    *Client
	ready  chan struct{} // closed while cur is set
	events chan Event
	stop   chan struct{}
	done   chan struct{}
	closed bool
}

// ErrSessionClosed is returned by Command after Close.
var ErrSessionClosed = errors.New("battleye: session closed")

func (s *Session) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log(format, a...)
	}
}

// Start begins connecting in the background. It is idempotent; Command and Events call it.
func (s *Session) Start() {
	s.once.Do(func() {
		s.ready = make(chan struct{})
		s.events = make(chan Event, 64)
		s.stop = make(chan struct{})
		s.done = make(chan struct{})
		go s.run()
	})
}

// Events returns the stream of server events (connect, GUID, chat, kick) across reconnects. Events
// that nobody reads are dropped rather than held back.
func (s *Session) Events() <-chan Event {
	s.Start()
	return s.events
}

// Connected reports whether a connection is up right now.
func (s *Session) Connected() bool {
	s.Start()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil
}

// Close ends the session and its connection. Pending commands fail.
func (s *Session) Close() error {
	s.Start()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	c := s.cur
	close(s.stop)
	s.mu.Unlock()
	<-s.done
	if c != nil {
		return c.Close()
	}
	return nil
}

func (s *Session) backoff() (min, max time.Duration) {
	min, max = s.MinBackoff, s.MaxBackoff
	if min <= 0 {
		min = time.Second
	}
	if max < min {
		max = 30 * time.Second
		if max < min {
			max = min
		}
	}
	return min, max
}

func (s *Session) options() []Option {
	opts := append([]Option{}, s.Options...)
	// Placed first, so an explicit WithLivenessTimeout in Options still wins.
	return append([]Option{WithLivenessTimeout(3 * KeepAliveInterval)}, opts...)
}

func (s *Session) run() {
	defer close(s.done)
	dial := s.Dial
	if dial == nil {
		dial = Dial
	}
	min, max := s.backoff()
	wait := min
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		c, err := dial(s.Addr, s.Password, s.options()...)
		if err != nil {
			s.logf("RCon %s: %v, trying again in %s", s.Addr, err, wait)
			select {
			case <-s.stop:
				return
			case <-time.After(wait):
			}
			if wait *= 2; wait > max {
				wait = max
			}
			continue
		}
		wait = min
		s.logf("RCon %s: connected", s.Addr)
		s.mu.Lock()
		s.cur = c
		close(s.ready)
		s.mu.Unlock()

		fwdDone := make(chan struct{})
		go func() {
			defer close(fwdDone)
			for ev := range c.Events() {
				select {
				case s.events <- ev:
				default: // nobody reads: drop, as the client does
				}
			}
		}()
		select {
		case <-c.Done():
			s.logf("RCon %s: connection lost, connecting again", s.Addr)
		case <-s.stop:
		}
		s.mu.Lock()
		s.cur = nil
		s.ready = make(chan struct{})
		s.mu.Unlock()
		_ = c.Close()
		<-fwdDone
	}
}

// connection waits for the current Client.
func (s *Session) connection(ctx context.Context) (*Client, error) {
	for {
		s.mu.Lock()
		c, ready, closed := s.cur, s.ready, s.closed
		s.mu.Unlock()
		if closed {
			return nil, ErrSessionClosed
		}
		if c != nil {
			return c, nil
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.stop:
			return nil, ErrSessionClosed
		}
	}
}

// WaitConnected blocks until the first connection is up, or until ctx ends: a caller that wants to
// know at once whether the server can be reached uses it with a short timeout.
func (s *Session) WaitConnected(ctx context.Context) error {
	s.Start()
	_, err := s.connection(ctx)
	return err
}

// Command sends a command over the current connection, waiting for one if there is none. It never
// waits for an answer longer than CommandTimeout (15s by default) per try: the server answers over
// UDP and an answer that does not come would otherwise hang the caller (a restart) for good. When
// the connection was lost with the command pending, or no answer came in time, the command is sent
// again, up to three times in all: for the commands dzo sends (players, lock, kick, say) a second
// delivery is harmless, and a restart of the server must not turn into an error for the caller.
func (s *Session) Command(ctx context.Context, command string) (string, error) {
	s.Start()
	timeout := s.CommandTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	for attempt := 1; ; attempt++ {
		c, err := s.connection(ctx)
		if err != nil {
			return "", err
		}
		actx, cancel := context.WithTimeout(ctx, timeout)
		out, err := c.Command(actx, command)
		cancel()
		if err == nil || ctx.Err() != nil || attempt >= 3 {
			return out, err
		}
		switch {
		case errors.Is(err, ErrConnectionLost):
			select {
			case <-c.Done():
			case <-ctx.Done():
				return "", ctx.Err()
			}
		case errors.Is(err, context.DeadlineExceeded): // no answer in time: ask again
		default:
			return out, err
		}
	}
}

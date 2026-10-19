// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Announcement is the timeline of a restart. Every number is minutes before the
// restart itself: the countdown starts Minutes before it, the server is locked
// against new logins Lock minutes before it, and the players are kicked Kick
// minutes before it. The countdown ends at the kick, the server is stopped when
// the restart time is reached.
type Announcement struct {
	Minutes int
	Lock    int
	Kick    int
	Text    string
	// Event names what is coming in the messages to the players: "restart" (the default) or
	// "shutdown".
	Event string
	// Rules say how the announcements reach the players and when; empty means the default of
	// announce.ways (the last ten minutes on screen, once a minute).
	Rules []AnnounceRule
}

func (a Announcement) event() string {
	if a.Event == "" {
		return "restart"
	}
	return a.Event
}

type step struct {
	left time.Duration // time left until the restart
	lock bool
	kick bool
	say  string // announcement text, empty for none
	// How the announcement reaches the players: way (rcon, chat, notification, expansion), typ (the
	// Expansion type), seconds on screen. now marks the NOW notice.
	way, typ string
	seconds  int
	now      bool
}

func durationText(d time.Duration) string {
	if d >= time.Minute {
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", n)
	}
	n := int(d / time.Second)
	if n == 1 {
		return "1 second"
	}
	return fmt.Sprintf("%d seconds", n)
}

// Restarter restarts an instance gracefully: announce, lock, kick, stop, do
// something while it is down (an update's snapshot and unit switch), start.
type Restarter struct {
	Lifecycle Lifecycle
	// Dial connects to the instance's RCon; an error means it is unreachable
	// and the restart goes ahead at once, without announcing (§C8).
	Dial func(ctx context.Context) (Commander, error)
	// Notify sends an announcement through the notification system of the game server (way chat,
	// notification or expansion, through dzo-admin). nil, or an error, makes the announcement go
	// out over RCon "say" instead.
	Notify func(ctx context.Context, way, typ, text string, seconds int) error
	// Sleep waits; tests replace it.
	Sleep func(time.Duration)
	// Log reports what happens, for the journal.
	Log func(format string, a ...any)
	// Cancelled, if set, is asked every minute of the countdown; true unlocks
	// the server and ends the restart (dzo restart --cancel).
	Cancelled func() bool
}

// ErrCancelled is returned by a restart or stop that was cancelled during its countdown.
var ErrCancelled = errors.New("instance: the restart was cancelled")

func (r Restarter) sleep(d time.Duration) {
	if r.Sleep != nil {
		r.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (r Restarter) log(format string, a ...any) {
	if r.Log != nil {
		r.Log(format, a...)
	}
}

// Restart runs the sequence. While the instance is down, between runs; if it
// fails the instance is started again unchanged and the error is returned, so a
// failed snapshot never leaves the server down. An instance that is not running
// is not announced to: it is only started (after between).
func (r Restarter) Restart(ctx context.Context, name string, a Announcement, between func(ctx context.Context) error) error {
	active, _ := r.Lifecycle.IsActive(ctx, name)
	if active {
		if err := r.announceAndStop(ctx, name, a); err != nil {
			return err
		}
	}
	if between != nil {
		if err := between(ctx); err != nil {
			if active {
				if serr := r.Lifecycle.Start(ctx, name); serr != nil {
					return fmt.Errorf("%w (and starting %s again failed: %v)", err, name, serr)
				}
			}
			return err
		}
	}
	return r.Lifecycle.Start(ctx, name)
}

// Stop stops the instance and leaves it stopped, after the same countdown a restart makes: the
// announcements, the lock and the kick. An instance that is not running is not announced to.
func (r Restarter) Stop(ctx context.Context, name string, a Announcement) error {
	a.Event = "shutdown"
	if active, _ := r.Lifecycle.IsActive(ctx, name); !active {
		return r.Lifecycle.Stop(ctx, name)
	}
	return r.announceAndStop(ctx, name, a)
}

// announceAndStop counts down to the players when RCon is reachable, then stops the unit.
func (r Restarter) announceAndStop(ctx context.Context, name string, a Announcement) error {
	if c, err := r.Dial(ctx); err != nil {
		r.log("RCon unreachable (%v), stopping without announcement", err)
	} else {
		rd := &redialer{dial: r.Dial, c: c}
		err := r.countdown(ctx, rd, a)
		rd.Close()
		if err != nil {
			return err
		}
	}
	return r.Lifecycle.Stop(ctx, name)
}

// redialer is a Commander that connects again after a command failed (a timeout, a dropped
// connection): the next command goes out on a new connection.
type redialer struct {
	dial func(ctx context.Context) (Commander, error)
	c    Commander
}

func (d *redialer) Command(ctx context.Context, cmd string) (string, error) {
	if d.c == nil {
		c, err := d.dial(ctx)
		if err != nil {
			return "", err
		}
		d.c = c
	}
	out, err := d.c.Command(ctx, cmd)
	if err != nil {
		d.Close()
	}
	return out, err
}

func (d *redialer) Close() {
	if cl, ok := d.c.(io.Closer); ok {
		_ = cl.Close()
	}
	d.c = nil
}

// countdown announces, locks and kicks while the time runs down. RCon is a courtesy to the players,
// so a command that fails (a timeout, a dropped connection) is logged and skipped: the next one goes
// out on a new connection, and the restart or the stop still happens at the time asked for. The lock
// and the kick are tried once more on the new connection. A cancelled context ends the countdown.
func (r Restarter) countdown(ctx context.Context, c Commander, a Announcement) error {
	text := strings.TrimSpace(a.Text)
	if text == "" {
		text = "Server " + a.event()
	}
	// try runs f; a failure is logged and skipped, after one more try when again is set.
	try := func(what string, again bool, f func() error) error {
		err := f()
		if err != nil && again && ctx.Err() == nil {
			r.log("RCon %s failed (%v), connecting again", what, err)
			err = f()
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			r.log("RCon %s failed (%v), skipped", what, err)
		}
		return nil
	}
	locked := false
	// The way of the last announcement, which a cancel notice goes out by as well.
	last := step{way: "rcon"}
	left := time.Duration(a.Minutes) * time.Minute
	for _, st := range a.timeline() {
		if err := r.wait(ctx, c, left-st.left, &locked, a.event(), func(text string) error {
			return r.deliver(ctx, c, step{way: last.way, typ: last.typ, say: text, seconds: noticeSeconds})
		}); err != nil {
			return err
		}
		left = st.left
		lock := func() error {
			err := Lock(ctx, c)
			if err == nil {
				locked = true
			}
			return err
		}
		if st.lock && !locked {
			if err := try("lock", true, lock); err != nil {
				return err
			}
		}
		if st.say != "" {
			last = st
			if err := try("announcement", false, func() error { return r.deliver(ctx, c, st) }); err != nil {
				return err
			}
		}
		if st.kick {
			if !locked {
				if err := try("lock", true, lock); err != nil {
					return err
				}
			}
			if err := try("kick", true, func() error { return KickAll(ctx, c, text, 3) }); err != nil {
				return err
			}
		}
	}
	return r.wait(ctx, c, left, &locked, a.event(), func(text string) error {
		return r.deliver(ctx, c, step{way: last.way, typ: last.typ, say: text, seconds: noticeSeconds})
	})
}

// deliver sends a step's announcement the way the step says. A way that needs the game server's
// notification system falls back to RCon "say" when Notify is not set or fails: the players get the
// words either way.
func (r Restarter) deliver(ctx context.Context, c Commander, st step) error {
	if st.way != "rcon" && st.way != "" && r.Notify != nil {
		err := r.Notify(ctx, st.way, st.typ, st.say, st.seconds)
		if err == nil {
			return nil
		}
		if ctx.Err() == nil {
			r.log("announcement by %s failed (%v), using RCon say", st.way, err)
		}
	}
	return Announce(ctx, c, st.say)
}

// wait sleeps for d in steps of at most a minute, so that a cancelled restart ends quickly. A
// cancelled restart unlocks the server again.
func (r Restarter) wait(ctx context.Context, c Commander, d time.Duration, locked *bool, event string, say func(text string) error) error {
	for d > 0 {
		step := d
		if step > time.Minute {
			step = time.Minute
		}
		r.sleep(step)
		d -= step
		if err := ctx.Err(); err != nil {
			_ = Unlock(context.Background(), c) // a cancelled restart must not leave the server locked
			return err
		}
		if r.Cancelled != nil && r.Cancelled() {
			_ = say("The " + event + " was cancelled")
			_ = Unlock(ctx, c)
			return ErrCancelled
		}
	}
	return nil
}

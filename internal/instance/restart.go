// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
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
}

func (a Announcement) event() string {
	if a.Event == "" {
		return "restart"
	}
	return a.Event
}

// Announcements are made at these times left until the kick, plus one at the start of the
// countdown. This is the rhythm of the legacy dayz_restart: every ten minutes, then every five,
// then every minute, and in the last minute every few seconds.
var announceMarks = []time.Duration{
	30 * time.Minute, 20 * time.Minute, 15 * time.Minute, 10 * time.Minute, 5 * time.Minute,
	4 * time.Minute, 3 * time.Minute, 2 * time.Minute, time.Minute,
	30 * time.Second, 15 * time.Second, 10 * time.Second, 5 * time.Second,
	4 * time.Second, 3 * time.Second, 2 * time.Second, time.Second,
}

type step struct {
	left time.Duration // time left until the restart
	lock bool
	kick bool
	say  string // announcement text, empty for none
}

// timeline lists the steps of a restart in order. The announcements count down to the kick.
func (a Announcement) timeline() []step {
	kick := time.Duration(a.Kick) * time.Minute
	lock := time.Duration(a.Lock) * time.Minute
	total := time.Duration(a.Minutes) * time.Minute
	text := strings.TrimSpace(a.Text)
	if text == "" {
		text = "Server " + a.event()
	}
	if lock < kick {
		lock = kick // the server is locked at the latest when the players are kicked
	}
	var steps []step
	if first := total - kick; first > 0 {
		steps = append(steps, step{left: total, say: fmt.Sprintf("%s in %s", text, durationText(first))})
		for _, m := range announceMarks {
			if m < first {
				steps = append(steps, step{left: m + kick, say: fmt.Sprintf("%s in %s", text, durationText(m))})
			}
		}
	}
	if lock > kick {
		steps = append(steps, step{left: lock, lock: true, say: "The server is locked for the " + a.event() + ": no new logins"})
	}
	steps = append(steps, step{left: kick, kick: true})
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].left > steps[j].left })
	return steps
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
		err := r.countdown(ctx, c, a)
		if cl, ok := c.(io.Closer); ok {
			_ = cl.Close()
		}
		if err != nil {
			return err
		}
	}
	return r.Lifecycle.Stop(ctx, name)
}

func (r Restarter) countdown(ctx context.Context, c Commander, a Announcement) error {
	text := strings.TrimSpace(a.Text)
	if text == "" {
		text = "Server " + a.event()
	}
	locked := false
	left := time.Duration(a.Minutes) * time.Minute
	for _, st := range a.timeline() {
		if err := r.wait(ctx, c, left-st.left, &locked, a.event()); err != nil {
			return err
		}
		left = st.left
		if st.lock && !locked {
			if err := Lock(ctx, c); err != nil {
				return fmt.Errorf("instance: lock: %w", err)
			}
			locked = true
		}
		if st.say != "" {
			if err := Announce(ctx, c, st.say); err != nil {
				return fmt.Errorf("instance: announce: %w", err)
			}
		}
		if st.kick {
			if !locked {
				if err := Lock(ctx, c); err != nil {
					return fmt.Errorf("instance: lock: %w", err)
				}
			}
			if err := KickAll(ctx, c, text, 3); err != nil {
				return err
			}
		}
	}
	return r.wait(ctx, c, left, &locked, a.event())
}

// wait sleeps for d in steps of at most a minute, so that a cancelled restart ends quickly. A
// cancelled restart unlocks the server again.
func (r Restarter) wait(ctx context.Context, c Commander, d time.Duration, locked *bool, event string) error {
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
			_ = Announce(ctx, c, "The "+event+" was cancelled")
			_ = Unlock(ctx, c)
			return ErrCancelled
		}
	}
	return nil
}

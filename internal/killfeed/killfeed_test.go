// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package killfeed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/notify"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   admin.FeedEvent
		want string
	}{
		{"player kill", admin.FeedEvent{Kind: admin.FeedKill, Name: "Alice", TargetName: "Bob", Weapon: "M4-A1", Distance: 123.4},
			"💀 **Alice** killed **Bob** with M4-A1 (123 m)"},
		{"ai kill", admin.FeedEvent{Kind: admin.FeedKill, Name: "AI (East)", AI: true, TargetName: "Bob", Weapon: "AKM", Distance: 54},
			"🤖 **AI (East)** killed **Bob** with AKM (54 m)"},
		{"melee has no distance", admin.FeedEvent{Kind: admin.FeedKill, Name: "Alice", TargetName: "Bob", Weapon: "fists"},
			"💀 **Alice** killed **Bob** with fists"},
		{"vehicle", admin.FeedEvent{Kind: admin.FeedKill, Name: "Alice", TargetName: "Bob", Weapon: "Ada 4x4"},
			"💀 **Alice** killed **Bob** with Ada 4x4"},
		{"no weapon", admin.FeedEvent{Kind: admin.FeedKill, Name: "Alice", TargetName: "Bob"},
			"💀 **Alice** killed **Bob**"},
		{"infected", admin.FeedEvent{Kind: admin.FeedDeath, TargetName: "Bob", Cause: "infected"},
			"☠️ **Bob** was killed by infected"},
		{"self", admin.FeedEvent{Kind: admin.FeedDeath, TargetName: "Bob", Cause: "self"},
			"☠️ **Bob** died"},
		{"chat has no line", admin.FeedEvent{Kind: admin.FeedChat, Name: "Bob", Text: "hi"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, Line(tc.ev)) })
	}
}

// Review focus 1: names are shown literally and ping nobody.
func TestLineEscapes(t *testing.T) {
	for _, name := range []string{"@everyone", "**x**", "`y`", "<@123>", "||z||", "_u_", "~s~", "[l](http://x)", "a\nb\r"} {
		got := Line(admin.FeedEvent{Kind: admin.FeedKill, Name: name, TargetName: "Bob", Weapon: name})
		body := strings.NewReplacer("**Bob**", "", "💀 ", "", " killed ", "", " with ", "").Replace(got)
		require.NotContains(t, body, "<@", name)
		require.NotContains(t, body, "\n")
		require.NotContains(t, body, "\r")
		require.NotRegexp(t, `(^|[^\\])[*`+"`"+`|_~\[\]]`, strings.ReplaceAll(body, "**", ""), name)
		require.NotRegexp(t, "@[^​]", body+"x", name)
	}
	require.Contains(t, Line(admin.FeedEvent{Kind: admin.FeedKill, Name: "@everyone", TargetName: "B"}), "@​everyone")
}

func TestLineCapsLongNames(t *testing.T) {
	got := Line(admin.FeedEvent{Kind: admin.FeedKill, Name: strings.Repeat("ü", 5000), TargetName: "B"})
	require.Less(t, len(got), 1000)
}

func TestWanted(t *testing.T) {
	yes, no := true, false
	_ = no
	c := Config{AIKillers: yes}
	kill := admin.FeedEvent{Kind: admin.FeedKill, Name: "A", TargetName: "B"}
	require.True(t, Wanted(c, kill), "player kills player")

	aiKill := admin.FeedEvent{Kind: admin.FeedKill, Name: "AI", AI: true, TargetName: "B"}
	require.True(t, Wanted(c, aiKill))
	require.False(t, Wanted(Config{AIKillers: false}, aiKill))

	killsAI := admin.FeedEvent{Kind: admin.FeedKill, Name: "A", TargetName: "AI", TargetAI: true}
	require.False(t, Wanted(c, killsAI))
	require.True(t, Wanted(Config{AIKillers: true, AIVictims: true}, killsAI))

	death := admin.FeedEvent{Kind: admin.FeedDeath, TargetName: "B", Cause: "infected"}
	require.False(t, Wanted(c, death))
	require.True(t, Wanted(Config{Deaths: true}, death))
	require.False(t, Wanted(Config{Deaths: true}, admin.FeedEvent{Kind: admin.FeedDeath, TargetName: "AI", TargetAI: true}), "an AI dying needs ai_victims")

	require.False(t, Wanted(Config{AIKillers: true, AIVictims: true, Deaths: true}, admin.FeedEvent{Kind: admin.FeedChat}))
}

func TestQueueDelay(t *testing.T) {
	var q Queue
	require.Zero(t, q.Add(t0.Add(5*time.Minute), "a"))
	require.Empty(t, q.Due(t0.Add(4*time.Minute+59*time.Second)))
	require.Equal(t, []string{"a"}, q.Due(t0.Add(5*time.Minute)))
	require.Empty(t, q.Due(t0.Add(time.Hour)), "sent once")
}

func TestQueueCap(t *testing.T) {
	var q Queue
	dropped := 0
	for i := 0; i < MaxQueued+1; i++ {
		dropped += q.Add(t0, fmt.Sprint(i))
	}
	require.Equal(t, 1, dropped)
	got := q.Due(t0)
	require.Len(t, got, MaxQueued)
	require.Equal(t, "1", got[0], "the oldest was dropped")
}

func TestSplit(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, Line(admin.FeedEvent{Kind: admin.FeedKill, Name: fmt.Sprintf("Alice%02d", i), TargetName: "Bob", Weapon: "M4-A1", Distance: 123}))
	}
	pieces := Split(lines)
	require.Greater(t, len(pieces), 1)
	for _, p := range pieces {
		require.LessOrEqual(t, len(p), 2000)
	}
	require.Equal(t, lines, strings.Split(strings.Join(pieces, "\n"), "\n"), "order kept, nothing lost")
	require.Empty(t, Split(nil))
}

// env is a Manager over fake clock, ticks and notifier.
type env struct {
	m    *Manager
	reg  *admin.Registry
	tick chan time.Time

	mu    sync.Mutex
	now   time.Time
	calls []notify.Event
	logs  []string
	fail  func(call int) error
	block func(ctx context.Context, ev notify.Event) error
}

func newEnv(t *testing.T, instances ...string) *env {
	t.Helper()
	e := &env{reg: admin.NewRegistry(t.TempDir()), tick: make(chan time.Time), now: t0}
	for _, n := range instances {
		e.reg.Add(admin.NewHub(n))
	}
	e.m = &Manager{
		Notify: e.notify,
		Now:    func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now },
		Tick:   e.tick,
		Logf: func(f string, a ...any) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.logs = append(e.logs, fmt.Sprintf(f, a...))
		},
		PostTimeout: 5 * time.Second,
	}
	return e
}

func (e *env) notify(ctx context.Context, ev notify.Event) error {
	e.mu.Lock()
	n := len(e.calls)
	e.calls = append(e.calls, ev)
	fail, block := e.fail, e.block
	e.mu.Unlock()
	if block != nil {
		return block(ctx, ev)
	}
	if fail != nil {
		return fail(n)
	}
	return nil
}

func (e *env) advance(d time.Duration) { e.mu.Lock(); e.now = e.now.Add(d); e.mu.Unlock() }
func (e *env) sent() []notify.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]notify.Event(nil), e.calls...)
}
func (e *env) logged() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.logs...)
}

// start runs the manager and stops it with the test.
func (e *env) start(t *testing.T, cfgs ...Config) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	e.m.Set(ctx, e.reg, cfgs)
	t.Cleanup(func() { cancel(); e.m.Wait() })
	return cancel
}

func (e *env) hub(name string) *admin.Hub { h, _ := e.reg.Hub(name); return h }

func killOf(a, b string) admin.FeedEvent {
	return admin.FeedEvent{Kind: admin.FeedKill, Name: a, TargetName: b, Weapon: "M4-A1", Distance: 123}
}

func cfg(instance string) Config {
	return Config{Instance: instance, Targets: []string{"pvp"}, AIKillers: true}
}

func TestPostsAtOnceWithoutDelay(t *testing.T) {
	e := newEnv(t, "alpha")
	e.start(t, cfg("alpha"))
	e.hub("alpha").AddFeed(killOf("Alice", "Bob"))
	require.Eventually(t, func() bool { return len(e.sent()) == 1 }, 5*time.Second, time.Millisecond)
	ev := e.sent()[0]
	require.Equal(t, notify.KindKill, ev.Kind)
	require.Equal(t, "alpha", ev.Instance)
	require.Equal(t, "💀 **Alice** killed **Bob** with M4-A1 (123 m)", ev.Data["Lines"])
	require.NotNil(t, ev.Targets)
	require.Equal(t, []string{"pvp"}, *ev.Targets)
}

func TestDelayHoldsUntilDue(t *testing.T) {
	e := newEnv(t, "alpha")
	c := cfg("alpha")
	c.Delay = 5 * time.Minute
	e.start(t, c)
	e.hub("alpha").AddFeed(killOf("Alice", "Bob"))
	// The kill is queued by the stream wake; a tick before it is due sends nothing.
	require.Eventually(t, func() bool { return e.m.Queued("alpha") == 1 }, 5*time.Second, time.Millisecond)
	e.advance(4*time.Minute + 59*time.Second)
	e.tick <- e.m.Now()
	e.tick <- e.m.Now()
	require.Equal(t, 1, e.m.Queued("alpha"))
	require.Empty(t, e.sent())
	e.advance(time.Second)
	e.tick <- e.m.Now()
	require.Eventually(t, func() bool { return len(e.sent()) == 1 }, 5*time.Second, time.Millisecond)
}

func TestBurstIsSplitInOrder(t *testing.T) {
	e := newEnv(t, "alpha")
	c := cfg("alpha")
	c.Delay = time.Minute
	e.start(t, c)
	for i := 0; i < 60; i++ {
		e.hub("alpha").AddFeed(killOf(fmt.Sprintf("Alice%02d", i), "Bob"))
	}
	require.Eventually(t, func() bool { return e.m.Queued("alpha") == 60 }, 5*time.Second, time.Millisecond)
	e.advance(time.Minute)
	e.tick <- e.m.Now()
	require.Eventually(t, func() bool { return len(e.sent()) >= 2 }, 5*time.Second, time.Millisecond)
	var all []string
	for _, ev := range e.sent() {
		lines := ev.Data["Lines"].(string)
		require.LessOrEqual(t, len(lines), 2000)
		all = append(all, strings.Split(lines, "\n")...)
	}
	require.Len(t, all, 60)
	for i, l := range all {
		require.Contains(t, l, fmt.Sprintf("Alice%02d", i))
	}
}

func TestFailedPostIsDropped(t *testing.T) {
	e := newEnv(t, "alpha")
	e.fail = func(call int) error {
		if call == 0 {
			return errors.New("429")
		}
		return nil
	}
	e.start(t, cfg("alpha"))
	e.hub("alpha").AddFeed(killOf("Alice", "Bob"))
	require.Eventually(t, func() bool { return len(e.logged()) == 1 }, 5*time.Second, time.Millisecond)
	require.Contains(t, e.logged()[0], "alpha")
	e.hub("alpha").AddFeed(killOf("Carl", "Dan"))
	require.Eventually(t, func() bool { return len(e.sent()) == 2 }, 5*time.Second, time.Millisecond)
	require.Contains(t, e.sent()[1].Data["Lines"], "Carl")
	require.NotContains(t, e.sent()[1].Data["Lines"], "Alice", "the failed kill is not retried")
	e.tick <- e.m.Now()
	e.tick <- e.m.Now()
	require.Len(t, e.sent(), 2)
}

func TestQueueCapDropsOldest(t *testing.T) {
	e := newEnv(t, "alpha")
	c := cfg("alpha")
	c.Delay = time.Hour
	e.start(t, c)
	for i := 0; i < MaxQueued+1; i++ {
		e.hub("alpha").AddFeed(killOf(fmt.Sprintf("Killer%03d", i), "Bob"))
	}
	require.Eventually(t, func() bool { return len(e.logged()) == 1 }, 5*time.Second, time.Millisecond)
	require.Contains(t, e.logged()[0], "dropped")
	require.Equal(t, MaxQueued, e.m.Queued("alpha"))
}

// Review focus 2: a reload keeps the queue and its due times; a new delay is for new kills.
func TestSetKeepsQueue(t *testing.T) {
	e := newEnv(t, "alpha")
	c := cfg("alpha")
	c.Delay = 5 * time.Minute
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); e.m.Wait() })
	e.m.Set(ctx, e.reg, []Config{c})
	e.hub("alpha").AddFeed(killOf("Old", "Bob"))
	require.Eventually(t, func() bool { return e.m.Queued("alpha") == 1 }, 5*time.Second, time.Millisecond)

	c.Delay = time.Hour
	e.m.Set(ctx, e.reg, []Config{c})
	require.Equal(t, 1, e.m.Queued("alpha"))
	e.hub("alpha").AddFeed(killOf("New", "Bob"))
	require.Eventually(t, func() bool { return e.m.Queued("alpha") == 2 }, 5*time.Second, time.Millisecond)

	e.advance(5 * time.Minute)
	e.tick <- e.m.Now()
	require.Eventually(t, func() bool { return len(e.sent()) == 1 }, 5*time.Second, time.Millisecond)
	require.Contains(t, e.sent()[0].Data["Lines"], "Old")
	require.NotContains(t, e.sent()[0].Data["Lines"], "New")
}

func TestSetStopsRemoved(t *testing.T) {
	e := newEnv(t, "alpha", "beta")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); e.m.Wait() })
	e.m.Set(ctx, e.reg, []Config{cfg("alpha"), cfg("beta")})
	e.m.Set(ctx, e.reg, []Config{cfg("beta")}) // returns once alpha's goroutine ended
	e.hub("alpha").AddFeed(killOf("Alice", "Bob"))
	e.hub("beta").AddFeed(killOf("Carl", "Dan"))
	require.Eventually(t, func() bool { return len(e.sent()) == 1 }, 5*time.Second, time.Millisecond)
	require.Equal(t, "beta", e.sent()[0].Instance)
}

// Review focus 4: a hung Discord costs that instance a dropped post, nobody else anything.
func TestHungDiscordDoesNotBlock(t *testing.T) {
	e := newEnv(t, "alpha", "beta")
	e.m.PostTimeout = 50 * time.Millisecond
	e.block = func(ctx context.Context, ev notify.Event) error {
		if ev.Instance == "alpha" {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	e.start(t, cfg("alpha"), cfg("beta"))
	e.hub("alpha").AddFeed(killOf("Alice", "Bob"))
	e.hub("beta").AddFeed(killOf("Carl", "Dan"))
	require.Eventually(t, func() bool {
		for _, ev := range e.sent() {
			if ev.Instance == "beta" {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return len(e.logged()) == 1 }, 5*time.Second, time.Millisecond, "the hung post is dropped and logged")
	require.Contains(t, e.logged()[0], "alpha")
}

func TestCancelEndsAHungPost(t *testing.T) {
	e := newEnv(t, "alpha")
	e.block = func(ctx context.Context, _ notify.Event) error { <-ctx.Done(); return ctx.Err() }
	cancel := e.start(t, cfg("alpha"))
	e.hub("alpha").AddFeed(killOf("Alice", "Bob"))
	require.Eventually(t, func() bool { return len(e.sent()) == 1 }, 5*time.Second, time.Millisecond)
	cancel()
	done := make(chan struct{})
	go func() { e.m.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after cancel")
	}
}

func TestStartsAfterNewest(t *testing.T) {
	e := newEnv(t, "alpha")
	e.hub("alpha").AddFeed(killOf("Before", "Bob"))
	e.start(t, cfg("alpha"))
	e.hub("alpha").AddFeed(killOf("After", "Bob"))
	require.Eventually(t, func() bool { return len(e.sent()) == 1 }, 5*time.Second, time.Millisecond)
	require.Contains(t, e.sent()[0].Data["Lines"], "After")
	require.NotContains(t, e.sent()[0].Data["Lines"], "Before")
}

func TestFilteredEventsAreNotPosted(t *testing.T) {
	e := newEnv(t, "alpha")
	e.start(t, cfg("alpha"))
	e.hub("alpha").AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "Bob", Text: "hi"})
	e.hub("alpha").AddFeed(killOf("Alice", "Bob"))
	require.Eventually(t, func() bool { return len(e.sent()) == 1 }, 5*time.Second, time.Millisecond)
	require.NotContains(t, e.sent()[0].Data["Lines"], "hi")
}

func TestNoNotifierLogsOnce(t *testing.T) {
	e := newEnv(t, "alpha")
	e.m.Notify = nil
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); e.m.Wait() })
	e.m.Set(ctx, e.reg, []Config{cfg("alpha")})
	e.m.Set(ctx, e.reg, []Config{cfg("alpha")})
	require.Len(t, e.logged(), 1)
	require.Contains(t, e.logged()[0], "alpha")
}

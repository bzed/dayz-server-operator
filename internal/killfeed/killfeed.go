// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package killfeed posts the kills of the event feed (internal/admin) to Discord: a filter, a delay queue
// for PvP servers that must not give away a fight as it happens, and batches that stay under Discord's
// message limit. It is best effort: a post that fails, is rate limited or hangs is dropped and logged,
// never retried, and nothing on the game side waits for Discord.
package killfeed

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/notify"
)

const (
	// MaxQueued is how many kills wait per instance; the oldest are dropped beyond it.
	MaxQueued = 500
	// MaxMessage is Discord's limit for the content of a message.
	MaxMessage = 2000
	// maxName caps a name or weapon, so one line stays far below MaxMessage.
	maxName = 64
	// DefaultPostTimeout is how long one post may take.
	DefaultPostTimeout = 5 * time.Second
)

// Config is the killfeed of one instance.
type Config struct {
	Instance                     string
	Targets                      []string // webhook names
	Delay                        time.Duration
	AIKillers, AIVictims, Deaths bool
}

// Wanted is the filter: which feed events become a line.
func Wanted(c Config, ev admin.FeedEvent) bool {
	switch ev.Kind {
	case admin.FeedKill:
		if bool(ev.AI) && !c.AIKillers {
			return false
		}
	case admin.FeedDeath:
		if !c.Deaths {
			return false
		}
	default:
		return false
	}
	return !bool(ev.TargetAI) || c.AIVictims
}

// esc makes untrusted text show literally in Discord: markdown characters get a backslash, a mention
// (@everyone, <@id>, <#id>) is broken with a zero-width space, control characters become spaces.
func esc(s string) string {
	if utf8.RuneCountInString(s) > maxName {
		s = string([]rune(s)[:maxName]) + "…"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\' || r == '*' || r == '_' || r == '~' || r == '|' || r == '`' || r == '[' || r == ']':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '@' || r == '<':
			b.WriteRune(r)
			b.WriteRune('​')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func orElse(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Line is the Discord line of a feed event, escaped; empty for an event that has none.
func Line(ev admin.FeedEvent) string {
	switch ev.Kind {
	case admin.FeedKill:
		icon := "💀"
		if ev.AI {
			icon = "🤖"
		}
		s := fmt.Sprintf("%s **%s** killed **%s**", icon, esc(orElse(ev.Name, "Someone")), esc(orElse(ev.TargetName, "someone")))
		if ev.Weapon != "" {
			s += " with " + esc(ev.Weapon)
			if ev.Distance >= 0.5 {
				s += fmt.Sprintf(" (%.0f m)", ev.Distance)
			}
		}
		return s
	case admin.FeedDeath:
		what := map[string]string{
			"infected": "was killed by infected", "animal": "was killed by an animal", "explosion": "was blown up",
			"vehicle": "was run over",
		}[ev.Cause]
		return fmt.Sprintf("☠️ **%s** %s", esc(orElse(ev.TargetName, "Someone")), orElse(what, "died"))
	}
	return ""
}

// Queue holds the kills that wait for their delay. The zero value is empty and ready.
type Queue struct {
	items []queued
}

type queued struct {
	due  time.Time
	line string
}

// Add queues a line until due and returns how many old ones were dropped to stay within MaxQueued.
func (q *Queue) Add(due time.Time, line string) (dropped int) {
	q.items = append(q.items, queued{due, line})
	if over := len(q.items) - MaxQueued; over > 0 {
		q.items = append([]queued(nil), q.items[over:]...)
		dropped = over
	}
	return dropped
}

// Due removes and returns the lines that are due at now, oldest first. Kills leave in the order they
// came in, so a later kill that is already due waits for an earlier one.
func (q *Queue) Due(now time.Time) []string {
	n := 0
	for n < len(q.items) && !q.items[n].due.After(now) {
		n++
	}
	out := make([]string, n)
	for i := range out {
		out[i] = q.items[i].line
	}
	q.items = q.items[n:]
	return out
}

// Len is how many lines wait.
func (q *Queue) Len() int { return len(q.items) }

// Split joins lines into messages of at most MaxMessage bytes, keeping their order.
func Split(lines []string) []string {
	var out []string
	var cur strings.Builder
	for _, l := range lines {
		if len(l) > MaxMessage {
			l = strings.ToValidUTF8(l[:MaxMessage-4], "") + " …"
		}
		if cur.Len() > 0 && cur.Len()+1+len(l) > MaxMessage {
			out = append(out, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(l)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// Manager runs one killfeed per configured instance.
type Manager struct {
	// Notify posts one message. Nil: a configured killfeed logs that once and posts nothing.
	Notify func(context.Context, notify.Event) error
	// Now is the clock (default time.Now). Tick paces the queues (default: every second); tests replace both.
	Now  func() time.Time
	Tick <-chan time.Time
	Logf func(string, ...any)
	// PostTimeout is how long one post may take before it is dropped (default DefaultPostTimeout).
	PostTimeout time.Duration

	mu     sync.Mutex
	ctx    context.Context
	insts  map[string]*instance
	warned map[string]bool
	wg     sync.WaitGroup
}

type instance struct {
	hub    *admin.Hub
	cancel context.CancelFunc
	done   chan struct{}
	kick   chan struct{}

	mu    sync.Mutex // guards cfg and queue
	cfg   Config
	queue Queue
	last  int64 // newest feed id seen; only the run goroutine touches it
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// Set starts, updates and stops the killfeeds so they match cfgs. An instance that stays keeps its queue
// (a new delay is for the kills that come after it); one that is gone stops, and Set returns once it has.
// Events already in an instance's feed when its killfeed starts are not posted. ctx ends all of them.
func (m *Manager) Set(ctx context.Context, reg *admin.Registry, cfgs []Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.insts == nil {
		m.ctx, m.insts, m.warned = ctx, map[string]*instance{}, map[string]bool{}
		m.wg.Add(1)
		go m.pump()
	}
	want := map[string]Config{}
	for _, c := range cfgs {
		if len(c.Targets) == 0 {
			continue
		}
		want[c.Instance] = c
	}
	for name, in := range m.insts {
		if _, ok := want[name]; !ok {
			in.cancel()
			<-in.done
			delete(m.insts, name)
		}
	}
	for name, c := range want {
		if m.Notify == nil {
			if !m.warned[name] {
				m.warned[name] = true
				m.logf("killfeed: %s: no Discord webhook is configured in config.yaml notify.discord, nothing is posted", name)
			}
			continue
		}
		if in, ok := m.insts[name]; ok {
			in.mu.Lock()
			in.cfg = c
			in.mu.Unlock()
			continue
		}
		hub, ok := reg.Hub(name)
		if !ok {
			continue
		}
		ictx, cancel := context.WithCancel(m.ctx)
		in := &instance{hub: hub, cancel: cancel, done: make(chan struct{}), kick: make(chan struct{}, 1), cfg: c}
		if newest := hub.Feed(0, 1); len(newest) > 0 {
			in.last = newest[0].ID
		}
		sub := hub.Subscribe(ictx)
		m.insts[name] = in
		m.wg.Add(1)
		go m.run(ictx, in, sub)
	}
}

// Wait blocks until every goroutine has ended, which they do once the context given to Set is done.
func (m *Manager) Wait() { m.wg.Wait() }

// Queued is how many kills of an instance wait for their delay.
func (m *Manager) Queued(name string) int {
	m.mu.Lock()
	in := m.insts[name]
	m.mu.Unlock()
	if in == nil {
		return 0
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.queue.Len()
}

// pump hands every tick to every instance; an instance that is busy posting misses it.
func (m *Manager) pump() {
	defer m.wg.Done()
	tick := m.Tick
	if tick == nil {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick:
			m.mu.Lock()
			for _, in := range m.insts {
				select {
				case in.kick <- struct{}{}:
				default:
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) run(ctx context.Context, in *instance, sub <-chan admin.StreamEvent) {
	defer m.wg.Done()
	defer close(in.done)
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-sub:
			if !ok {
				return
			}
		case <-in.kick:
		}
		m.poll(ctx, in)
	}
}

// poll queues the new kills and posts the ones that are due. It reads the hub's feed after the last id it
// saw, so a stream event that was dropped loses nothing.
func (m *Manager) poll(ctx context.Context, in *instance) {
	in.mu.Lock()
	cfg := in.cfg
	in.mu.Unlock()
	now := m.now()
	var dropped int
	var lines []string
	in.mu.Lock()
	for _, ev := range in.hub.Feed(in.last, 0) {
		in.last = max(in.last, ev.ID)
		if !Wanted(cfg, ev) {
			continue
		}
		if line := Line(ev); line != "" {
			dropped += in.queue.Add(now.Add(cfg.Delay), line)
		}
	}
	lines = in.queue.Due(now)
	in.mu.Unlock()
	if dropped > 0 {
		m.logf("killfeed: %s: more than %d kills waiting, dropped the %d oldest", cfg.Instance, MaxQueued, dropped)
	}
	targets := cfg.Targets
	for _, piece := range Split(lines) {
		if ctx.Err() != nil {
			return
		}
		m.post(ctx, cfg.Instance, targets, piece)
	}
}

func (m *Manager) post(ctx context.Context, instance string, targets []string, text string) {
	timeout := m.PostTimeout
	if timeout <= 0 {
		timeout = DefaultPostTimeout
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := m.Notify(pctx, notify.Event{Kind: notify.KindKill, Instance: instance, Data: map[string]any{"Lines": text}, Targets: &targets})
	if err != nil && ctx.Err() == nil {
		m.logf("killfeed: %s: dropped a post: %v", instance, err)
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

const alice, bob = "76561198000000001", "76561198000000002"

type fakeStore struct {
	mu  sync.Mutex
	log []string
}

func (f *fakeStore) add(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, fmt.Sprintf(format, args...))
}

func (f *fakeStore) PlayerJoined(_ context.Context, instance, steamID, name string, at time.Time) error {
	f.add("join %s %s %s %s", instance, steamID, name, at.Format("15:04:05"))
	return nil
}

func (f *fakeStore) PlayerLeft(_ context.Context, instance, steamID string, at time.Time) error {
	f.add("left %s %s %s", instance, steamID, at.Format("15:04:05"))
	return nil
}

func (f *fakeStore) CloseSessions(_ context.Context, instance string, at time.Time) error {
	f.add("close %s %s", instance, at.Format("15:04:05"))
	return nil
}

func (f *fakeStore) has(entry string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.log, entry)
}

func (f *fakeStore) entries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

// fakeSource is an instance's hub: tests push player lists and connection states through it.
type fakeSource struct {
	mu        sync.Mutex
	players   []admin.Player
	connected bool
	lastSeen  time.Time
	subs      []chan admin.StreamEvent
}

func (s *fakeSource) Players() []admin.Player {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.players)
}

func (s *fakeSource) Status() admin.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return admin.Status{Connected: s.connected, LastSeen: s.lastSeen}
}

func (s *fakeSource) Subscribe(ctx context.Context) <-chan admin.StreamEvent {
	ch := make(chan admin.StreamEvent, 32)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	go func() { <-ctx.Done() }()
	return ch
}

func (s *fakeSource) set(players ...admin.Player) {
	s.mu.Lock()
	s.players, s.connected, s.lastSeen = players, true, t0
	subs := slices.Clone(s.subs)
	s.mu.Unlock()
	for _, c := range subs {
		c <- admin.StreamEvent{Kind: "players", Data: slices.Clone(players)}
	}
}

func (s *fakeSource) lose(lastSeen time.Time) {
	s.mu.Lock()
	s.connected, s.lastSeen = false, lastSeen
	s.mu.Unlock()
}

type rig struct {
	rec    *Recorder
	store  *fakeStore
	src    map[string]*fakeSource
	tick   chan time.Time
	cancel context.CancelFunc
	now    time.Time
	mu     sync.Mutex
	joined []string
}

func newRig(t *testing.T, instances ...string) *rig {
	t.Helper()
	r := &rig{store: &fakeStore{}, src: map[string]*fakeSource{}, tick: make(chan time.Time), now: t0}
	for _, n := range instances {
		r.src[n] = &fakeSource{}
	}
	r.rec = &Recorder{Store: r.store, Tick: r.tick,
		Now:    func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		OnJoin: func(id string) { r.mu.Lock(); r.joined = append(r.joined, id); r.mu.Unlock() }}
	ctx, cancel := context.WithCancel(t.Context())
	r.cancel = cancel
	t.Cleanup(func() { cancel(); r.rec.Wait() })
	r.rec.Set(ctx, r.lookup, instances)
	return r
}

func (r *rig) lookup(name string) (Source, bool) {
	s, ok := r.src[name]
	return s, ok
}

func (r *rig) wait(t *testing.T, entry string) {
	t.Helper()
	require.Eventually(t, func() bool { return r.store.has(entry) }, 5*time.Second, 5*time.Millisecond, "waiting for %q, have %v", entry, r.store.entries())
}

func TestRecorderJoinsAndLeaves(t *testing.T) {
	r := newRig(t, "alpha")
	r.wait(t, "close alpha 12:00:00") // the start
	r.src["alpha"].set(admin.Player{SteamID: alice, Name: "Alice"}, admin.Player{SteamID: bob, Name: "Bob"})
	r.wait(t, "join alpha "+alice+" Alice 12:00:00")
	r.wait(t, "join alpha "+bob+" Bob 12:00:00")
	r.mu.Lock()
	r.now = t0.Add(time.Hour)
	r.mu.Unlock()
	r.src["alpha"].set(admin.Player{SteamID: bob, Name: "Bob"})
	r.wait(t, "left alpha "+alice+" 13:00:00")
	assert.NotContains(t, r.store.entries(), "left alpha "+bob+" 13:00:00")
	n := 0
	for _, e := range r.store.entries() {
		if e == "join alpha "+bob+" Bob 12:00:00" {
			n++
		}
	}
	assert.Equal(t, 1, n, "a player who stays is not joined again")
}

func TestRecorderSkipsPlayersWithoutASteamID(t *testing.T) {
	r := newRig(t, "alpha")
	r.src["alpha"].set(admin.Player{SteamID: "", Name: "x"}, admin.Player{SteamID: "bogus", Name: "y"}, admin.Player{SteamID: alice, Name: "Alice"})
	r.wait(t, "join alpha "+alice+" Alice 12:00:00")
	for _, e := range r.store.entries() {
		assert.NotContains(t, e, "join alpha  ")
		assert.NotContains(t, e, "bogus")
	}
}

func TestRecorderClosesOnDisconnect(t *testing.T) {
	r := newRig(t, "alpha")
	r.src["alpha"].set(admin.Player{SteamID: alice, Name: "Alice"})
	r.wait(t, "join alpha "+alice+" Alice 12:00:00")
	r.src["alpha"].lose(t0.Add(30 * time.Minute))
	r.tick <- t0.Add(time.Hour)
	r.wait(t, "close alpha 12:30:00")
	// when the mod comes back with the same player, a new session starts
	r.src["alpha"].set(admin.Player{SteamID: alice, Name: "Alice"})
	require.Eventually(t, func() bool {
		n := 0
		for _, e := range r.store.entries() {
			if e == "join alpha "+alice+" Alice 12:00:00" {
				n++
			}
		}
		return n == 2
	}, 5*time.Second, 5*time.Millisecond)
}

func TestRecorderClosesLeftoversAtStart(t *testing.T) {
	r := newRig(t, "alpha")
	r.wait(t, "close alpha 12:00:00")
	assert.Equal(t, "close alpha 12:00:00", r.store.entries()[0])
}

func TestRecorderPicksUpPlayersAlreadyOnline(t *testing.T) {
	st := &fakeStore{}
	src := &fakeSource{players: []admin.Player{{SteamID: alice, Name: "Alice"}}, connected: true, lastSeen: t0}
	rec := &Recorder{Store: st, Now: func() time.Time { return t0 }}
	ctx, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); rec.Wait() }()
	rec.Set(ctx, func(string) (Source, bool) { return src, true }, []string{"alpha"})
	require.Eventually(t, func() bool { return st.has("join alpha " + alice + " Alice 12:00:00") }, 5*time.Second, 5*time.Millisecond)
}

func TestRecorderClosesOnShutdown(t *testing.T) {
	r := newRig(t, "alpha")
	r.src["alpha"].set(admin.Player{SteamID: alice, Name: "Alice"})
	r.wait(t, "join alpha "+alice+" Alice 12:00:00")
	r.mu.Lock()
	r.now = t0.Add(2 * time.Hour)
	r.mu.Unlock()
	r.cancel()
	r.rec.Wait()
	assert.True(t, r.store.has("close alpha 14:00:00"), "the sessions end when dzo serve stops: %v", r.store.entries())
}

func TestRecorderSetReplacesInstances(t *testing.T) {
	r := newRig(t, "alpha", "beta")
	r.src["beta"].set(admin.Player{SteamID: bob, Name: "Bob"})
	r.wait(t, "join beta "+bob+" Bob 12:00:00")
	r.rec.Set(t.Context(), r.lookup, []string{"alpha"})
	assert.True(t, r.store.has("close beta 12:00:00"), "a removed instance stops and closes its sessions: %v", r.store.entries())
	r.src["alpha"].set(admin.Player{SteamID: alice, Name: "Alice"})
	r.wait(t, "join alpha "+alice+" Alice 12:00:00")
}

func TestRecorderCallsOnJoin(t *testing.T) {
	r := newRig(t, "alpha")
	r.src["alpha"].set(admin.Player{SteamID: alice, Name: "Alice"})
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return slices.Equal(r.joined, []string{alice})
	}, 5*time.Second, 5*time.Millisecond)
}

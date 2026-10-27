// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package lookups

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

const alice, bob, carol = "76561198000000001", "76561198000000002", "76561198000000003"

func guid(t *testing.T, steam string) string {
	t.Helper()
	g, err := moderation.DeriveGUID(steam)
	require.NoError(t, err)
	return g
}

// fakeStore has a fixed due list per kind and records what is saved.
type fakeStore struct {
	mu    sync.Mutex
	due   map[string][]string
	be    map[string]map[moderation.BEGame]moderation.BEStatus
	steam map[string]moderation.SteamInfo
	asked []string // the arguments of DueLookups
}

func newStore() *fakeStore {
	return &fakeStore{due: map[string][]string{}, be: map[string]map[moderation.BEGame]moderation.BEStatus{}, steam: map[string]moderation.SteamInfo{}}
}

func (f *fakeStore) DueLookups(_ context.Context, kind string, checkedBefore, seenAfter time.Time, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, kind+" "+checkedBefore.Format(time.RFC3339)+" "+seenAfter.Format(time.RFC3339))
	l := f.due[kind]
	return l[:min(limit, len(l))], nil
}

func (f *fakeStore) SaveBEStatus(_ context.Context, g string, st moderation.BEStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.be[g] == nil {
		f.be[g] = map[moderation.BEGame]moderation.BEStatus{}
	}
	f.be[g][st.Game] = st
	return nil
}

func (f *fakeStore) SaveSteamInfo(_ context.Context, id string, in moderation.SteamInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steam[id] = in
	return nil
}

// fakeBE answers by guid; a missing guid or nil answer func means silence.
type fakeBE struct {
	mu      sync.Mutex
	banned  map[string]string // guid -> reason, in the dayz game only
	silent  bool
	order   []string
	block   bool
	packets int
}

func (f *fakeBE) Check(ctx context.Context, g battleye.Game, guid string) (battleye.GlobalBan, error) {
	f.mu.Lock()
	f.packets++
	f.order = append(f.order, guid[:6]+"/"+g.ID)
	silent, block := f.silent, f.block
	reason, banned := f.banned[guid]
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return battleye.GlobalBan{}, ctx.Err()
	}
	if silent {
		return battleye.GlobalBan{}, battleye.ErrNoAnswer
	}
	return battleye.GlobalBan{Banned: banned && g.ID == "dayz", Reason: reason}, nil
}

type fakeSteam struct {
	mu    sync.Mutex
	calls []string
	fail  bool
}

func (f *fakeSteam) Community(_ context.Context, id string) (moderation.SteamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "community "+id)
	if f.fail {
		return moderation.SteamInfo{}, errors.New("steam is down")
	}
	return moderation.SteamInfo{State: moderation.LookupOK, Source: "community", Persona: "p" + id[12:]}, nil
}

func (f *fakeSteam) Bans(_ context.Context, ids []string) (map[string]moderation.SteamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "bans")
	if f.fail {
		return nil, errors.New("steam is down")
	}
	out := map[string]moderation.SteamInfo{}
	for _, id := range ids {
		if id != carol { // carol is unknown to Steam
			out[id] = moderation.SteamInfo{State: moderation.LookupOK, Source: "api", VACBanned: id == alice}
		}
	}
	return out, nil
}

func (f *fakeSteam) Details(_ context.Context, id string, in *moderation.SteamInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "details "+id)
	lvl := 55
	in.Level = &lvl
	return nil
}

// rig is a Refresher with a fake clock whose Sleep advances it.
type rig struct {
	r     *Refresher
	store *fakeStore
	be    *fakeBE
	st    *fakeSteam
	mu    sync.Mutex
	now   time.Time
	slept time.Duration
}

func newRig(t *testing.T, key bool) *rig {
	t.Helper()
	g := &rig{store: newStore(), be: &fakeBE{banned: map[string]string{}}, st: &fakeSteam{}, now: t0}
	g.r = &Refresher{Store: g.store, BE: g.be, Steam: g.st, SteamKey: key, SteamOn: true,
		Now: func() time.Time { g.mu.Lock(); defer g.mu.Unlock(); return g.now },
		Sleep: func(_ context.Context, d time.Duration) error {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.now, g.slept = g.now.Add(d), g.slept+d
			return nil
		},
		Logf: t.Logf}
	return g
}

func (g *rig) advance(d time.Duration) { g.mu.Lock(); g.now = g.now.Add(d); g.mu.Unlock() }

func TestRefresherLooksUpDuePlayers(t *testing.T) {
	g := newRig(t, false)
	g.store.due["be"] = []string{guid(t, alice)}
	g.store.due["steam"] = []string{alice}
	g.be.banned[guid(t, alice)] = "Global Ban #1"
	g.r.round(t.Context())
	be := g.store.be[guid(t, alice)]
	require.Len(t, be, 3, "one answer per game")
	assert.True(t, be["dayz"].Banned)
	assert.Equal(t, "Global Ban #1", be["dayz"].Reason)
	assert.False(t, be["arma3"].Banned)
	assert.Equal(t, moderation.LookupOK, be["dayz"].State)
	assert.False(t, be["dayz"].CheckedAt.Before(t0), "the time of the answer is stored")
	assert.Equal(t, "p00001", g.store.steam[alice].Persona)
	assert.Equal(t, []string{"community " + alice}, g.st.calls, "without a key the community page")
	// the due queries use the cache lifetimes and the 30 day window
	joined := ""
	for _, a := range g.store.asked {
		joined += a + "\n"
	}
	assert.Contains(t, joined, "be "+t0.Add(-7*24*time.Hour).Format(time.RFC3339)+" "+t0.Add(-30*24*time.Hour).Format(time.RFC3339))
	assert.Contains(t, joined, "steam 2026-10-21T12:00:0", "Steam answers are 24 h old at most")
	assert.Contains(t, joined, " 2026-09-22T12:00:0", "players seen in the last 30 days")
}

func TestRefresherUsesTheWebAPIWithAKey(t *testing.T) {
	g := newRig(t, true)
	g.store.due["steam"] = []string{alice, bob, carol}
	g.r.round(t.Context())
	assert.Equal(t, []string{"bans", "details " + alice, "details " + bob}, g.st.calls, "bans batched, details per player Steam knows")
	assert.True(t, g.store.steam[alice].VACBanned)
	require.NotNil(t, g.store.steam[alice].Level)
	assert.Equal(t, moderation.LookupUnknown, g.store.steam[carol].State, "an id Steam did not answer is unknown")
}

func TestRefresherStoresUnknownWhenBattlEyeIsSilent(t *testing.T) {
	g := newRig(t, false)
	g.be.silent = true
	g.store.due["be"] = []string{guid(t, alice)}
	g.r.round(t.Context())
	for _, st := range g.store.be[guid(t, alice)] {
		assert.Equal(t, moderation.LookupUnknown, st.State)
		assert.False(t, st.Banned)
	}
}

func TestRefresherBacksOffAfterFailures(t *testing.T) {
	g := newRig(t, false)
	g.be.silent = true
	g.store.due["be"] = []string{guid(t, alice)}
	for range 3 {
		g.r.round(t.Context())
	}
	before := g.be.packets
	assert.Equal(t, 9, before, "three rounds of three games")
	g.r.round(t.Context())
	assert.Equal(t, before, g.be.packets, "after three silent rounds BattlEye is left alone for an hour")
	g.advance(61 * time.Minute)
	g.r.round(t.Context())
	assert.Greater(t, g.be.packets, before, "and asked again after it")

	// Steam: five failures in a row
	g.st.fail = true
	g.store.due["steam"] = []string{alice}
	for range 5 {
		g.r.round(t.Context())
	}
	n := len(g.st.calls)
	g.r.round(t.Context())
	assert.Len(t, g.st.calls, n, "five failures pause Steam for an hour")
	g.advance(61 * time.Minute)
	g.r.round(t.Context())
	assert.Greater(t, len(g.st.calls), n)
}

func TestRefresherKeepsTheRateLimit(t *testing.T) {
	g := newRig(t, false)
	var due []string
	for i := range 10 {
		id := "7656119800000000" + string(rune('0'+i))
		due = append(due, guid(t, id))
	}
	g.store.due["be"] = due
	g.r.round(t.Context())
	assert.Equal(t, 30, g.be.packets)
	assert.GreaterOrEqual(t, g.slept, 29*time.Second, "at most one packet a second")
}

func TestNudgeComesFirst(t *testing.T) {
	g := newRig(t, false)
	g.store.due["be"] = []string{guid(t, alice), guid(t, bob), guid(t, carol)}
	g.r.Nudge(carol)
	g.r.round(t.Context())
	assert.Equal(t, guid(t, carol)[:6]+"/dayz", g.be.order[0])
	g.r.Nudge("not an id") // ignored, no panic
}

func TestLookupNowAsksBothAndRefusesTooSoon(t *testing.T) {
	g := newRig(t, false)
	g.be.banned[guid(t, alice)] = "Global Ban #1"
	require.NoError(t, g.r.LookupNow(t.Context(), alice))
	assert.True(t, g.store.be[guid(t, alice)]["dayz"].Banned)
	assert.Contains(t, g.store.steam, alice)
	err := g.r.LookupNow(t.Context(), alice)
	require.ErrorIs(t, err, moderation.ErrLookupTooSoon)
	g.advance(11 * time.Minute)
	require.NoError(t, g.r.LookupNow(t.Context(), alice))
	require.Error(t, g.r.LookupNow(t.Context(), "bogus"))
}

func TestLookupNowTimesOut(t *testing.T) {
	g := newRig(t, false)
	g.be.block = true
	g.r.LookupTimeout = 50 * time.Millisecond
	start := time.Now()
	err := g.r.LookupNow(t.Context(), alice)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	// a failed lookup does not lock the player out for ten minutes
	g.be.block = false
	require.NoError(t, g.r.LookupNow(t.Context(), alice))
}

func TestLookupNowLogsAFailure(t *testing.T) {
	g := newRig(t, false)
	var lines []string
	g.r.Logf = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	g.be.block = true
	g.r.LookupTimeout = 50 * time.Millisecond
	require.Error(t, g.r.LookupNow(t.Context(), alice))
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], alice)
	assert.Contains(t, lines[0], "failed after")
	assert.Contains(t, lines[0], "deadline exceeded")
	g.be.block = false
	g.advance(11 * time.Minute)
	require.NoError(t, g.r.LookupNow(t.Context(), alice))
	assert.Len(t, lines, 1, "a lookup that works logs nothing")
}

func TestRunLoopsUntilTheContextEnds(t *testing.T) {
	g := newRig(t, false)
	g.store.due["steam"] = []string{alice}
	tick := make(chan time.Time)
	g.r.Tick = tick
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { g.r.Run(ctx); close(done) }()
	tick <- t0
	require.Eventually(t, func() bool { g.store.mu.Lock(); defer g.store.mu.Unlock(); _, ok := g.store.steam[alice]; return ok }, 5*time.Second, 5*time.Millisecond)
	cancel()
	<-done
}

func TestSteamWaitSpacesRequests(t *testing.T) {
	g := newRig(t, false)
	require.NoError(t, g.r.SteamWait(t.Context()))
	require.NoError(t, g.r.SteamWait(t.Context()))
	assert.Equal(t, 2*time.Second, g.slept, "the community page: one request every 2 s")
	k := newRig(t, true)
	require.NoError(t, k.r.SteamWait(t.Context()))
	require.NoError(t, k.r.SteamWait(t.Context()))
	assert.Equal(t, time.Second, k.slept, "the Web API: one request a second")
}

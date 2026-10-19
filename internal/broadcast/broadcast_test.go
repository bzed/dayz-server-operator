// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package broadcast

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type said struct{ to, text, color string }

// fake is a Backend that records what is said and lets the test have players join.
type fake struct {
	mu     sync.Mutex
	said   []said
	joined chan Player
	fail   error
}

func newFake() *fake { return &fake{joined: make(chan Player, 8)} }

func (f *fake) Joined(context.Context) <-chan Player { return f.joined }

func (f *fake) Say(_ context.Context, p *Player, text, color string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	to := "all"
	if p != nil {
		to = p.Key
	}
	f.said = append(f.said, said{to, text, color})
	return nil
}

func (f *fake) got() []said {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]said(nil), f.said...)
}

func run(t testing.TB, f *fake, ms []site.Message) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	m := &Messenger{Backend: f, Messages: ms, Unit: 10 * time.Millisecond, Name: "dz1", Port: 2302}
	go func() { defer close(done); m.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel
}

func TestWelcomeGoesToTheJoiningPlayer(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "Welcome to #name on #port!", OnConnect: true}})
	f.joined <- Player{Key: "7656", Name: "bob"}
	require.Eventually(t, func() bool { return len(f.got()) == 1 }, 2*time.Second, time.Millisecond)
	require.Equal(t, said{"7656", "Welcome to dz1 on 2302!", ""}, f.got()[0])
}

func TestWelcomeDelayIsInMinutes(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "later", OnConnect: true, Delay: 5}}) // 5 units = 50ms
	start := time.Now()
	f.joined <- Player{Key: "a"}
	require.Eventually(t, func() bool { return len(f.got()) == 1 }, 2*time.Second, time.Millisecond)
	require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond, "sent before the delay")
}

func TestColorIsPassedOn(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "alert", Repeat: 1, Color: "red"}})
	require.Eventually(t, func() bool { return len(f.got()) >= 1 }, 2*time.Second, time.Millisecond)
	require.Equal(t, "red", f.got()[0].color)
}

func TestRepeatGoesToEveryone(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "rules", Repeat: 1}})
	require.Eventually(t, func() bool { return len(f.got()) >= 2 }, 2*time.Second, time.Millisecond)
	for _, s := range f.got() {
		assert.Equal(t, said{"all", "rules", ""}, s)
	}
}

func TestConnectAndRepeatTogether(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "both", OnConnect: true, Repeat: 1}})
	f.joined <- Player{Key: "a"}
	require.Eventually(t, func() bool {
		var one, all bool
		for _, s := range f.got() {
			one = one || s.to == "a"
			all = all || s.to == "all"
		}
		return one && all
	}, 2*time.Second, time.Millisecond)
}

func TestSayErrorsDoNotStopTheMessenger(t *testing.T) {
	f := newFake()
	f.fail = errors.New("nobody there")
	var mu sync.Mutex
	var logs []string
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	m := &Messenger{Backend: f, Messages: []site.Message{{Text: "x", Repeat: 1}}, Unit: 5 * time.Millisecond,
		Log: func(format string, a ...any) { mu.Lock(); logs = append(logs, format); mu.Unlock() }}
	go func() { defer close(done); m.Run(ctx) }()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(logs) >= 2 }, 2*time.Second, time.Millisecond)
	cancel()
	<-done
}

// session is the part of battleye.Session the RCon backend uses.
type session struct {
	mu     sync.Mutex
	cmds   []string
	events chan battleye.Event
}

func (s *session) Events() <-chan battleye.Event { return s.events }
func (s *session) Command(_ context.Context, c string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds = append(s.cmds, c)
	return "", nil
}
func (s *session) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func TestRConJoinedWaitsForTheGraceAndSaysToTheSlot(t *testing.T) {
	s := &session{events: make(chan battleye.Event, 4)}
	b := &RCon{Session: s, Grace: 30 * time.Millisecond}
	ctx := t.Context()
	joined := b.Joined(ctx)
	start := time.Now()
	s.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "bob"}
	select {
	case p := <-joined:
		require.GreaterOrEqual(t, time.Since(start), 30*time.Millisecond, "joined before the grace")
		require.Equal(t, "3", p.Key)
		require.NoError(t, b.Say(ctx, &p, "hi bob", "red"))
	case <-time.After(2 * time.Second):
		require.Fail(t, "no join")
	}
	require.NoError(t, b.Say(ctx, nil, "all of you", ""))
	require.Equal(t, []string{"say 3 hi bob", "say -1 all of you"}, s.sent())
}

func TestRConPlayerWhoLeftDuringTheGraceIsNotGreeted(t *testing.T) {
	s := &session{events: make(chan battleye.Event, 4)}
	b := &RCon{Session: s, Grace: 50 * time.Millisecond}
	joined := b.Joined(t.Context())
	s.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "bob"}
	s.events <- battleye.Event{Kind: battleye.EventDisconnect, PlayerID: 3, PlayerName: "bob"}
	select {
	case p := <-joined:
		require.Fail(t, "greeted", "%v", p)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRConRefusesNewlinesAndSlotsThatLeft(t *testing.T) {
	s := &session{events: make(chan battleye.Event)}
	b := &RCon{Session: s}
	require.Error(t, b.Say(t.Context(), nil, "a\nb", ""), "a newline must be refused")
	assert.Empty(t, s.sent(), "nothing is sent for a refused message")
	assert.Error(t, b.Say(t.Context(), &Player{Key: "9"}, "x", ""), "slot 9 never joined")
}

type fakeHub struct {
	mu        sync.Mutex
	connected bool
	calls     int
	players   []admin.Player
	cmds      []admin.Command
	res       admin.Result
}

func (h *fakeHub) Status() admin.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	return admin.Status{Connected: h.connected}
}
func (h *fakeHub) Players() []admin.Player {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]admin.Player(nil), h.players...)
}
func (h *fakeHub) Submit(_ context.Context, c admin.Command) (admin.Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cmds = append(h.cmds, c)
	return h.res, nil
}
func (h *fakeHub) set(connected bool, ps ...admin.Player) {
	h.mu.Lock()
	h.connected, h.players = connected, ps
	h.mu.Unlock()
}

func TestModGreetsOnlyPlayersWhoJoinAfterTheMod(t *testing.T) {
	h := &fakeHub{res: admin.Result{OK: true}}
	h.set(true, admin.Player{SteamID: "old", Name: "old"})
	m := &Mod{Hub: h, Poll: time.Millisecond}
	joined := m.Joined(t.Context())
	select {
	case p := <-joined:
		require.Fail(t, "greeted a player who was there before", "%v", p)
	case <-time.After(50 * time.Millisecond):
	}
	h.set(true, admin.Player{SteamID: "old"}, admin.Player{SteamID: "new", Name: "bob"})
	select {
	case p := <-joined:
		require.Equal(t, Player{Key: "new", Name: "bob"}, p)
	case <-time.After(2 * time.Second):
		require.Fail(t, "no join")
	}
	// a restart of the server: the mod is gone, then everybody is new but the first list is not greeted
	h.set(false)
	h.mu.Lock()
	n := h.calls
	h.mu.Unlock()
	require.Eventually(t, func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.calls >= n+2 }, 2*time.Second, time.Millisecond)
	h.set(true, admin.Player{SteamID: "again"})
	select {
	case p := <-joined:
		require.Fail(t, "greeted right after the mod came back", "%v", p)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestModSaysInTheStyleOfTheColour(t *testing.T) {
	h := &fakeHub{res: admin.Result{OK: true}}
	m := &Mod{Hub: h}
	for color, style := range map[string]string{"": "important", "red": "important", "green": "friendly", "yellow": "chat", "blue": "blue"} {
		require.NoError(t, m.Say(t.Context(), &Player{Key: "7656"}, "hi", color))
		last := h.cmds[len(h.cmds)-1]
		require.Equal(t, admin.Command{Kind: admin.KindMessage, SteamID: "7656", Text: "hi", Style: style}, last)
	}
	require.NoError(t, m.Say(t.Context(), nil, "all", "red"))
	require.Empty(t, h.cmds[len(h.cmds)-1].SteamID)
	require.Error(t, m.Say(t.Context(), nil, "x", "pink"), "unknown colour")
	h.res = admin.Result{OK: false, Message: "player not online"}
	assert.ErrorContains(t, m.Say(t.Context(), &Player{Key: "x"}, "x", ""), "player not online")
}

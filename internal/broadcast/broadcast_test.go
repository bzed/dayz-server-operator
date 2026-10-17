// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package broadcast

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/site"
)

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		runtime.Gosched()
		<-time.After(time.Millisecond)
	}
}

func equal[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func noError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

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

func run(t *testing.T, f *fake, ms []site.Message) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
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
	eventually(t, func() bool { return len(f.got()) == 1 })
	equal(t, said{"7656", "Welcome to dz1 on 2302!", ""}, f.got()[0])
}

func TestWelcomeDelayIsInMinutes(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "later", OnConnect: true, Delay: 5}}) // 5 units = 50ms
	start := time.Now()
	f.joined <- Player{Key: "a"}
	eventually(t, func() bool { return len(f.got()) == 1 })
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("sent before the delay")
	}
}

func TestColorIsPassedOn(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "alert", Repeat: 1, Color: "red"}})
	eventually(t, func() bool { return len(f.got()) >= 1 })
	equal(t, "red", f.got()[0].color)
}

func TestRepeatGoesToEveryone(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "rules", Repeat: 1}})
	eventually(t, func() bool { return len(f.got()) >= 2 })
	for _, s := range f.got() {
		equal(t, said{"all", "rules", ""}, s)
	}
}

func TestConnectAndRepeatTogether(t *testing.T) {
	f := newFake()
	run(t, f, []site.Message{{Text: "both", OnConnect: true, Repeat: 1}})
	f.joined <- Player{Key: "a"}
	eventually(t, func() bool {
		var one, all bool
		for _, s := range f.got() {
			one = one || s.to == "a"
			all = all || s.to == "all"
		}
		return one && all
	})
}

func TestSayErrorsDoNotStopTheMessenger(t *testing.T) {
	f := newFake()
	f.fail = errors.New("nobody there")
	var mu sync.Mutex
	var logs []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m := &Messenger{Backend: f, Messages: []site.Message{{Text: "x", Repeat: 1}}, Unit: 5 * time.Millisecond,
		Log: func(format string, a ...any) { mu.Lock(); logs = append(logs, format); mu.Unlock() }}
	go func() { defer close(done); m.Run(ctx) }()
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(logs) >= 2 })
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joined := b.Joined(ctx)
	start := time.Now()
	s.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "bob"}
	select {
	case p := <-joined:
		if time.Since(start) < 30*time.Millisecond {
			t.Fatal("joined before the grace")
		}
		equal(t, "3", p.Key)
		noError(t, b.Say(ctx, &p, "hi bob", "red"))
	case <-time.After(2 * time.Second):
		t.Fatal("no join")
	}
	noError(t, b.Say(ctx, nil, "all of you", ""))
	equal(t, strings.Join(s.sent(), "|"), "say 3 hi bob|say -1 all of you")
}

func TestRConPlayerWhoLeftDuringTheGraceIsNotGreeted(t *testing.T) {
	s := &session{events: make(chan battleye.Event, 4)}
	b := &RCon{Session: s, Grace: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joined := b.Joined(ctx)
	s.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "bob"}
	s.events <- battleye.Event{Kind: battleye.EventDisconnect, PlayerID: 3, PlayerName: "bob"}
	select {
	case p := <-joined:
		t.Fatalf("greeted %v", p)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRConRefusesNewlinesAndSlotsThatLeft(t *testing.T) {
	s := &session{events: make(chan battleye.Event)}
	b := &RCon{Session: s}
	if b.Say(context.Background(), nil, "a\nb", "") == nil {
		t.Error("a newline must be refused")
	}
	if len(s.sent()) != 0 {
		t.Error("nothing is sent for a refused message")
	}
	if b.Say(context.Background(), &Player{Key: "9"}, "x", "") == nil {
		t.Error("slot 9 never joined")
	}
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joined := m.Joined(ctx)
	select {
	case p := <-joined:
		t.Fatalf("greeted %v who was there before", p)
	case <-time.After(50 * time.Millisecond):
	}
	h.set(true, admin.Player{SteamID: "old"}, admin.Player{SteamID: "new", Name: "bob"})
	select {
	case p := <-joined:
		equal(t, Player{Key: "new", Name: "bob"}, p)
	case <-time.After(2 * time.Second):
		t.Fatal("no join")
	}
	// a restart of the server: the mod is gone, then everybody is new but the first list is not greeted
	h.set(false)
	h.mu.Lock()
	n := h.calls
	h.mu.Unlock()
	eventually(t, func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.calls >= n+2 })
	h.set(true, admin.Player{SteamID: "again"})
	select {
	case p := <-joined:
		t.Fatalf("greeted %v right after the mod came back", p)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestModSaysInTheStyleOfTheColour(t *testing.T) {
	h := &fakeHub{res: admin.Result{OK: true}}
	m := &Mod{Hub: h}
	for color, style := range map[string]string{"": "important", "red": "important", "green": "friendly", "yellow": "chat", "blue": "blue"} {
		noError(t, m.Say(context.Background(), &Player{Key: "7656"}, "hi", color))
		last := h.cmds[len(h.cmds)-1]
		equal(t, admin.Command{Kind: admin.KindMessage, SteamID: "7656", Text: "hi", Style: style}, last)
	}
	noError(t, m.Say(context.Background(), nil, "all", "red"))
	equal(t, "", h.cmds[len(h.cmds)-1].SteamID)
	if m.Say(context.Background(), nil, "x", "pink") == nil {
		t.Error("unknown colour")
	}
	h.res = admin.Result{OK: false, Message: "player not online"}
	if err := m.Say(context.Background(), &Player{Key: "x"}, "x", ""); err == nil || !strings.Contains(err.Error(), "player not online") {
		t.Errorf("err = %v", err)
	}
}

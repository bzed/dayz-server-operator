// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package presence turns the live player list of each instance into the player history: who joined and left
// which instance and when. It reads what dzo-admin reports; it never talks to the game server.
package presence

import (
	"context"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// Store records the history (moderation.Store).
type Store interface {
	PlayerJoined(ctx context.Context, instance, steamID, name string, at time.Time) error
	PlayerLeft(ctx context.Context, instance, steamID string, at time.Time) error
	CloseSessions(ctx context.Context, instance string, at time.Time) error
}

// Source is what the recorder needs of an instance's hub (admin.Hub).
type Source interface {
	Players() []admin.Player
	Status() admin.Status
	Subscribe(ctx context.Context) <-chan admin.StreamEvent
}

// FromRegistry looks the hubs of a registry up by instance name.
func FromRegistry(reg *admin.Registry) func(string) (Source, bool) {
	return func(name string) (Source, bool) {
		h, ok := reg.Hub(name)
		if !ok {
			return nil, false
		}
		return h, true
	}
}

// closeTimeout bounds the write that ends the sessions when the recorder stops.
const closeTimeout = 5 * time.Second

// Recorder keeps one goroutine per instance.
type Recorder struct {
	Store Store
	// Now is the clock (default time.Now). Tick paces the check for a lost mod (default: every 5 s); tests
	// replace both.
	Now  func() time.Time
	Tick <-chan time.Time
	Logf func(string, ...any)
	// OnJoin, when set, is told of every player who joins. It must not block.
	OnJoin func(steamID string)

	mu    sync.Mutex
	insts map[string]*running
	wg    sync.WaitGroup
}

type running struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Recorder) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// Set starts and stops the recording so it matches instances. An instance that stays keeps its state; one
// that is gone stops and closes its sessions, and Set returns once it has. ctx ends all of them.
func (r *Recorder) Set(ctx context.Context, hubs func(string) (Source, bool), instances []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insts == nil {
		r.insts = map[string]*running{}
	}
	want := map[string]bool{}
	for _, n := range instances {
		want[n] = true
	}
	for name, in := range r.insts {
		if !want[name] {
			in.cancel()
			<-in.done
			delete(r.insts, name)
		}
	}
	for name := range want {
		if _, ok := r.insts[name]; ok {
			continue
		}
		src, ok := hubs(name)
		if !ok {
			continue
		}
		ictx, cancel := context.WithCancel(ctx)
		in := &running{cancel: cancel, done: make(chan struct{})}
		r.insts[name] = in
		r.wg.Add(1)
		go r.run(ictx, in, name, src)
	}
}

// Wait blocks until every goroutine has ended, which they do once the context given to Set is done.
func (r *Recorder) Wait() { r.wg.Wait() }

func (r *Recorder) run(ctx context.Context, in *running, name string, src Source) {
	defer r.wg.Done()
	defer close(in.done)
	sub := src.Subscribe(ctx)
	// Sessions left open by a dzo serve that did not stop cleanly end now; the players that are online
	// start new ones below.
	r.write(ctx, func(c context.Context) error { return r.Store.CloseSessions(c, name, r.now()) })
	here := map[string]string{} // steam id -> name, as recorded
	r.diff(ctx, name, here, src.Players())
	tick := r.Tick
	if tick == nil {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			r.closeAll(name, r.now())
			return
		case ev, ok := <-sub:
			if !ok {
				r.closeAll(name, r.now())
				return
			}
			if list, ok := ev.Data.([]admin.Player); ev.Kind == "players" && ok {
				r.diff(ctx, name, here, list)
			}
		case <-tick:
			if st := src.Status(); !st.Connected && len(here) > 0 {
				// The mod went away without a last list: the players left when it was last heard from.
				r.write(ctx, func(c context.Context) error { return r.Store.CloseSessions(c, name, st.LastSeen) })
				clear(here)
			}
		}
	}
}

// diff records the players who came and went since the last list.
func (r *Recorder) diff(ctx context.Context, name string, here map[string]string, list []admin.Player) {
	now := r.now()
	cur := map[string]string{}
	for _, p := range list {
		if !moderation.ValidSteamID(p.SteamID) {
			continue
		}
		cur[p.SteamID] = p.Name
		if _, ok := here[p.SteamID]; ok {
			continue
		}
		id, pname := p.SteamID, p.Name
		r.write(ctx, func(c context.Context) error { return r.Store.PlayerJoined(c, name, id, pname, now) })
		if r.OnJoin != nil {
			r.OnJoin(id)
		}
	}
	for id := range here {
		if _, ok := cur[id]; !ok {
			r.write(ctx, func(c context.Context) error { return r.Store.PlayerLeft(c, name, id, now) })
		}
	}
	clear(here)
	for id, n := range cur {
		here[id] = n
	}
}

func (r *Recorder) write(ctx context.Context, f func(context.Context) error) {
	if err := f(ctx); err != nil && ctx.Err() == nil {
		r.logf("players: %v", err)
	}
}

// closeAll ends an instance's sessions when the recorder stops; the run's context is done by then, so the
// write has one of its own.
func (r *Recorder) closeAll(name string, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if err := r.Store.CloseSessions(ctx, name, at); err != nil {
		r.logf("players: closing the sessions of %s: %v", name, err)
	}
}

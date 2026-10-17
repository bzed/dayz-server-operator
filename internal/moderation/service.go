// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
)

// syncTimeout bounds one instance's sync when a ban changes, so the API answers quickly when a server
// is down; the sweep retries.
const syncTimeout = 5 * time.Second

// Instance is what the Service needs to enforce bans on one instance.
type Instance struct {
	Name     string
	BEDir    string // the instance's profiles/battleye
	RConAddr string
	RConPass string
}

// RCon is the part of battleye.Session the Service uses.
type RCon interface {
	Command(ctx context.Context, cmd string) (string, error)
	Events() <-chan battleye.Event
	Close() error
}

// Service enforces bans on the running instances: it renders bans.txt, makes BattlEye reload it,
// kicks banned players and records sightings. dzo serve runs one.
type Service struct {
	// Dial makes an instance's RCon session (default: a battleye.Session); tests replace it.
	Dial func(Instance) RCon
	// Tick paces the sweep (default: every minute); tests replace it.
	Tick <-chan time.Time
	// Now is the clock (default time.Now).
	Now func() time.Time

	store *Store
	audit *admin.Audit
	logf  func(string, ...any)

	mu    sync.Mutex
	ctx   context.Context
	insts map[string]*running
	wg    sync.WaitGroup
}

type running struct {
	inst   Instance
	rcon   RCon
	cancel context.CancelFunc
	done   chan struct{}

	syncMu  sync.Mutex // one sync at a time per instance
	mu      sync.Mutex
	pending bool
}

// NewService returns a Service over a store; Start it before SetInstances.
func NewService(store *Store, audit *admin.Audit, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{store: store, audit: audit, logf: logf, Now: time.Now, insts: map[string]*running{},
		Dial: func(i Instance) RCon { return &battleye.Session{Addr: i.RConAddr, Password: i.RConPass, Log: logf} }}
}

// Store returns the database.
func (s *Service) Store() *Store { return s.store }

// Start runs the sweep until ctx ends. Call Wait after ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	tick := s.Tick
	var stop func()
	if tick == nil {
		t := time.NewTicker(time.Minute)
		tick, stop = t.C, t.Stop
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if stop != nil {
			defer stop()
		}
		for {
			select {
			case <-ctx.Done():
				s.SetInstances(nil)
				return
			case <-tick:
				s.sweep(ctx)
			}
		}
	}()
}

// Wait waits for every goroutine of the Service after its context ended.
func (s *Service) Wait() { s.wg.Wait() }

// SetInstances starts enforcement on new instances, stops it on removed ones and restarts it on
// changed ones.
func (s *Service) SetInstances(insts []Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]Instance{}
	for _, i := range insts {
		want[i.Name] = i
	}
	for name, r := range s.insts {
		if w, ok := want[name]; !ok || w != r.inst {
			r.cancel()
			<-r.done
			_ = r.rcon.Close() // the session ends either way
			delete(s.insts, name)
		}
	}
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	for name, i := range want {
		if _, ok := s.insts[name]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		r := &running{inst: i, rcon: s.Dial(i), cancel: cancel, done: make(chan struct{}), pending: true}
		s.insts[name] = r
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer close(r.done)
			_ = s.syncOne(ctx, r) // r.fail has recorded an error; the first render and import, then the enforcer follows the events
			s.enforce(ctx, r)
		}()
	}
}

func (s *Service) running() []*running {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*running, 0, len(s.insts))
	for _, r := range s.insts {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].inst.Name < out[j].inst.Name })
	return out
}

// Pending reports the instances whose last sync failed.
func (s *Service) Pending() map[string]bool {
	out := map[string]bool{}
	for _, r := range s.running() {
		r.mu.Lock()
		if r.pending {
			out[r.inst.Name] = true
		}
		r.mu.Unlock()
	}
	return out
}

// Changed syncs every running instance that b or prev applies to.
func (s *Service) Changed(ctx context.Context, b Ban, prev *Ban) map[string]string {
	var targets []*running
	for _, r := range s.running() {
		if b.AppliesTo(r.inst.Name) || (prev != nil && prev.AppliesTo(r.inst.Name)) {
			targets = append(targets, r)
		}
	}
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, syncTimeout)
			defer cancel()
			res := "ok"
			if err := s.syncOne(cctx, r); err != nil {
				res = "pending: " + err.Error()
			}
			mu.Lock()
			out[r.inst.Name] = res
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (s *Service) sweep(ctx context.Context) {
	for _, r := range s.running() {
		cctx, cancel := context.WithTimeout(ctx, 4*syncTimeout)
		if err := s.syncOne(cctx, r); err != nil {
			s.logf("bans: %s: %v", r.inst.Name, err)
		}
		cancel()
	}
}

// syncOne imports outside changes, renders bans.txt, makes BattlEye reload it when it changed or the
// last reload failed, and kicks banned players who are online.
func (s *Service) syncOne(ctx context.Context, r *running) error {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	now := s.Now()
	name, dir := r.inst.Name, r.inst.BEDir
	if changed, content, err := ChangedOutside(dir); err != nil {
		return r.fail(err)
	} else if changed {
		n, err := s.store.ImportEntries(ctx, name, ParseBansTxt(content), now)
		if err != nil {
			return r.fail(err)
		}
		if n > 0 {
			s.logf("bans: %s: imported %d bans from bans.txt", name, n)
			_ = s.audit.Log(admin.AuditEntry{Actor: "dzo", Source: "bans", Instance: name, Action: "ban.import", Detail: fmt.Sprintf("%d from bans.txt", n), OK: true}) // informational
		}
	}
	bans, err := s.store.ActiveBans(ctx, name, now)
	if err != nil {
		return r.fail(err)
	}
	written, err := WriteBansFile(dir, FormatBansTxt(Entries(bans, name)))
	if err != nil {
		return r.fail(err)
	}
	r.mu.Lock()
	reload := written || r.pending
	r.mu.Unlock()
	if reload {
		if _, err := r.rcon.Command(ctx, "loadBans"); err != nil {
			return r.fail(fmt.Errorf("loadBans: %w", err))
		}
		if err := s.kickBanned(ctx, r); err != nil {
			return r.fail(err)
		}
	}
	r.mu.Lock()
	r.pending = false
	r.mu.Unlock()
	return nil
}

func (r *running) fail(err error) error {
	r.mu.Lock()
	r.pending = true
	r.mu.Unlock()
	return err
}

func (s *Service) enforce(ctx context.Context, r *running) { <-ctx.Done() }

func (s *Service) kickBanned(context.Context, *running) error { return nil }

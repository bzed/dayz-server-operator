// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
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
	// AddressRetention is how long the address of a sighting is kept unless a ban uses it (default 90
	// days; negative keeps them all). VacuumEvery is how often the database file is rebuilt (default 7 days).
	AddressRetention time.Duration
	VacuumEvery      time.Duration
	// SessionRetention is how long the sessions of the player history are kept (default 365 days;
	// negative keeps them all). The players, their names and first-seen times stay.
	SessionRetention time.Duration
	// SessionIdle and SessionMax are the lifetimes of web login sessions (default 12 hours and 7 days);
	// the sweep deletes the sessions past them, and pending logins and Steam nonces that expired.
	SessionIdle, SessionMax time.Duration
	// AuditClientRetention is how long the audit log keeps the client address of an entry (default 90
	// days; negative keeps them all). The sweep blanks older ones once a day.
	AuditClientRetention time.Duration
	// BackupDir, BackupEvery and BackupKeep control the regular copies of the database (BackupTo): the
	// directory, how often (0 or negative: never) and how many to keep. Without a BackupDir there are none.
	BackupDir   string
	BackupEvery time.Duration
	BackupKeep  int
	// OnEvent, when set, sees every BattlEye event of an instance after the ban logic ran. It runs on
	// the instance's event goroutine and must not block. Set it before Start.
	OnEvent func(instance string, ev battleye.Event)

	store *Store
	audit *admin.Audit
	// lastAuditPass is when the client addresses of the audit log were last expired (the sweep goroutine only).
	lastAuditPass time.Time
	logf          func(string, ...any)

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

// Retention defaults.
const (
	DefaultAddressRetention     = 90 * 24 * time.Hour
	DefaultVacuumEvery          = 7 * 24 * time.Hour
	DefaultSessionRetention     = 365 * 24 * time.Hour
	DefaultAuditClientRetention = 90 * 24 * time.Hour
	DefaultSessionIdle          = 12 * time.Hour
	DefaultSessionMax           = 7 * 24 * time.Hour
)

// maintain drops the addresses past their retention and vacuums the database when it is due.
func (s *Service) maintain(ctx context.Context) {
	now := s.Now()
	if keep := cmp.Or(s.AddressRetention, DefaultAddressRetention); keep > 0 {
		if n, err := s.store.PurgeAddresses(ctx, now.Add(-keep)); err != nil {
			s.logf("bans: dropping old addresses: %v", err)
		} else if n > 0 {
			s.logf("bans: dropped %d addresses older than %s", n, keep)
		}
	}
	if keep := cmp.Or(s.SessionRetention, DefaultSessionRetention); keep > 0 {
		if n, err := s.store.PurgeSessions(ctx, now.Add(-keep)); err != nil {
			s.logf("players: dropping old sessions: %v", err)
		} else if n > 0 {
			s.logf("players: dropped %d sessions older than %s", n, keep)
		}
	}
	if keep := cmp.Or(s.AuditClientRetention, DefaultAuditClientRetention); keep > 0 && now.Sub(s.lastAuditPass) >= 24*time.Hour {
		s.lastAuditPass = now
		if n, err := s.audit.ForgetClients(now.Add(-keep)); err != nil {
			s.logf("audit: dropping old client addresses: %v", err)
		} else if n > 0 {
			s.logf("audit: dropped the client address of %d entries older than %s", n, keep)
		}
	}
	if err := s.store.PruneLogins(ctx, now, cmp.Or(s.SessionIdle, DefaultSessionIdle), cmp.Or(s.SessionMax, DefaultSessionMax)); err != nil {
		s.logf("logins: dropping expired sessions: %v", err)
	}
	s.backupIfDue(ctx, now)
	every := cmp.Or(s.VacuumEvery, DefaultVacuumEvery)
	if every < 0 {
		return
	}
	if due, err := s.store.VacuumDue(ctx, now, every); err != nil {
		s.logf("bans: %v", err)
	} else if due {
		if err := s.store.Vacuum(ctx); err != nil {
			s.logf("bans: %v", err)
		} else if err := s.store.MarkVacuumed(ctx, now); err != nil {
			s.logf("bans: %v", err)
		}
	}
}

// backupIfDue writes a copy of the database when the last one is BackupEvery old. A failure is logged
// and tried again at the next sweep; it never stops the service.
func (s *Service) backupIfDue(ctx context.Context, now time.Time) {
	if s.BackupDir == "" || s.BackupEvery <= 0 {
		return
	}
	due, err := s.store.BackupDue(ctx, now, s.BackupEvery)
	if err != nil {
		s.logf("backup: %v", err)
		return
	}
	if !due {
		return
	}
	path, err := s.store.BackupTo(ctx, s.BackupDir, now, s.BackupKeep)
	if err != nil {
		s.logf("backup: %v", err)
		return
	}
	if err := s.store.MarkBackedUp(ctx, now); err != nil {
		s.logf("backup: %v", err)
		return
	}
	s.logf("backup: copied the database to %s", path)
}

func (s *Service) sweep(ctx context.Context) {
	s.maintain(ctx)
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

// slot is what the enforcer knows about a connected player.
type slot struct {
	name, ip, guid string
}

// enforce follows an instance's BattlEye events until ctx ends.
func (s *Service) enforce(ctx context.Context, r *running) {
	slots := map[int]*slot{}
	events := r.rcon.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			s.handle(ctx, r, slots, ev)
			if s.OnEvent != nil {
				s.OnEvent(r.inst.Name, ev)
			}
		}
	}
}

func (s *Service) handle(ctx context.Context, r *running, slots map[int]*slot, ev battleye.Event) {
	name := r.inst.Name
	switch ev.Kind {
	case battleye.EventConnect:
		host, _, err := net.SplitHostPort(ev.Address)
		if err != nil {
			s.logf("bans: %s: connect without an address: %q", name, ev.Raw)
			return
		}
		slots[ev.PlayerID] = &slot{name: ev.PlayerName, ip: host}
		s.check(ctx, r, ev.PlayerID, "", host)
	case battleye.EventGUID, battleye.EventVerifiedGUID:
		sl := slots[ev.PlayerID]
		if sl == nil {
			sl = &slot{name: ev.PlayerName}
			slots[ev.PlayerID] = sl
		}
		sl.guid = ev.GUID
		if sl.ip != "" {
			if err := s.store.Seen(ctx, name, ev.GUID, sl.ip, sl.name, s.Now()); err != nil {
				s.logf("bans: %s: sighting: %v", name, err)
			}
		}
		s.check(ctx, r, ev.PlayerID, ev.GUID, sl.ip)
	case battleye.EventDisconnect, battleye.EventKick:
		delete(slots, ev.PlayerID)
	}
}

// check kicks a player when a ban covers their GUID or IP.
func (s *Service) check(ctx context.Context, r *running, id int, guid, ip string) {
	b, err := s.store.FindActive(ctx, r.inst.Name, guid, ip, s.Now())
	if err != nil {
		s.logf("bans: %s: %v", r.inst.Name, err)
		return
	}
	if b != nil {
		s.kick(ctx, r, id, firstOf(guid, ip), *b)
	}
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *Service) kick(ctx context.Context, r *running, id int, target string, b Ban) {
	_, err := r.rcon.Command(ctx, fmt.Sprintf("kick %d %s", id, BEReason(b.Message)))
	e := admin.AuditEntry{Actor: "dzo", Source: "bans", Instance: r.inst.Name, Action: "ban.kick", Target: target,
		Detail: fmt.Sprintf("ban #%d", b.ID), OK: err == nil}
	if err != nil {
		e.Result = err.Error()
		s.logf("bans: %s: kick %d: %v", r.inst.Name, id, err)
	}
	_ = s.audit.Log(e) // the kick happened (or not) either way
}

// kickBanned kicks the online players a ban covers (after a ban change).
func (s *Service) kickBanned(ctx context.Context, r *running) error {
	raw, err := r.rcon.Command(ctx, "players")
	if err != nil {
		return fmt.Errorf("players: %w", err)
	}
	for _, p := range battleye.ParsePlayers(raw) {
		b, err := s.store.FindActive(ctx, r.inst.Name, p.GUID, p.IP, s.Now())
		if err != nil {
			return err
		}
		if b != nil {
			s.kick(ctx, r, p.ID, firstOf(p.GUID, p.IP), *b)
		}
	}
	return nil
}

// ErrNotOnline is returned by Kick for a player who is not on the instance.
var ErrNotOnline = errors.New("moderation: the player is not online")

// Kick removes an online player from an instance. The reason is what the player is shown (BattlEye keeps
// 64 bytes of it).
func (s *Service) Kick(ctx context.Context, instance, steamID, reason string) error {
	guid, err := DeriveGUID(steamID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	r := s.insts[instance]
	s.mu.Unlock()
	if r == nil {
		return fmt.Errorf("moderation: no BattlEye connection to instance %q", instance)
	}
	raw, err := r.rcon.Command(ctx, "players")
	if err != nil {
		return fmt.Errorf("players: %w", err)
	}
	for _, p := range battleye.ParsePlayers(raw) {
		if p.GUID == guid {
			if _, err := r.rcon.Command(ctx, fmt.Sprintf("kick %d %s", p.ID, BEReason(reason))); err != nil {
				return fmt.Errorf("kick: %w", err)
			}
			return nil
		}
	}
	return ErrNotOnline
}

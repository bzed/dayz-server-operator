// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package updates is the update engine (§C7): `dzo update check [--apply]`,
// run hourly by a timer. It refreshes the mods in the download cache, finds
// the instances whose running unit is older than the cache (pending updates),
// and acts on each by its policy: auto applies in one announced stop,
// snapshot, deploy, start cycle inside the update window; notify tells the
// admins once; manual only records. A new server build is only reported, never
// applied (D7), and while Steam wants a login nothing is downloaded and no
// server is restarted for an update.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/notify"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/steam"
	"github.com/bzed/dayz-server-operator/internal/units"
)

const (
	defaultCheckInterval = time.Hour
	steamReminder        = 6 * time.Hour
	// BuildKey is how a pending server build shows in a pending list.
	BuildKey = "build"
)

// InstanceState is what the engine remembers about one instance.
type InstanceState struct {
	LastCheck     time.Time `json:"last_check,omitzero"`
	Pending       []string  `json:"pending,omitempty"`
	PendingSince  time.Time `json:"pending_since,omitzero"`
	LastRestartAt time.Time `json:"last_restart_at,omitzero"`
	Notified      string    `json:"notified,omitempty"` // the pending set the admins were told about
	Decision      string    `json:"decision,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

// State is the engine's state file, below paths.cache.
type State struct {
	LastCheck     time.Time                 `json:"last_check,omitzero"`
	SteamNotified time.Time                 `json:"steam_notified,omitzero"`
	Instances     map[string]*InstanceState `json:"instances"`
}

// StatePath is where the state lives.
func StatePath(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.Cache, "updates", "state.json")
}

// LoadState reads the state file; a missing file is an empty state.
func LoadState(path string) (State, error) {
	b, err := os.ReadFile(path) //nolint:gosec // dzo's cache
	if os.IsNotExist(err) {
		return State{Instances: map[string]*InstanceState{}}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("updates: %s: %w", path, err)
	}
	if s.Instances == nil {
		s.Instances = map[string]*InstanceState{}
	}
	return s, nil
}

// Save writes the state atomically.
func (s State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *State) inst(name string) *InstanceState {
	if s.Instances[name] == nil {
		s.Instances[name] = &InstanceState{}
	}
	return s.Instances[name]
}

// Pending sums the pending mod updates per instance (a pending server build
// is not an update dzo applies, and is not counted).
func (s State) Pending() map[string]int {
	out := map[string]int{}
	for n, i := range s.Instances {
		for _, p := range i.Pending {
			if p != BuildKey {
				out[n]++
			}
		}
	}
	return out
}

// Engine runs update checks. Everything with a side effect outside the cache
// and the state file is a function, so the policy logic is tested without
// Steam, systemd or a game server.
type Engine struct {
	Cfg  *config.Config
	Load func() (*site.Tree, error)
	Now  func() time.Time

	// Refresh brings the cache up to date for the mods of these instances
	// (workshop updates, local sources). An error wrapping steam.ErrAuthRequired
	// pauses the engine.
	Refresh func(ctx context.Context, instances []string) error
	// Apply updates one instance: announce, stop, snapshot, deploy, start. It
	// returns an error if nothing was changed (for example the snapshot failed
	// and the server was started again as it was).
	Apply func(ctx context.Context, inst *resolve.Instance, a instance.Announcement) error
	// Notify delivers an event; its failure never fails the check.
	Notify func(ctx context.Context, ev notify.Event) error
	// SteamAuthRequired reports whether Steam wants a login; MarkAuthRequired
	// records that it does.
	SteamAuthRequired func() bool
	MarkAuthRequired  func()
	// NextScheduledRestart returns the next maintenance restart (zero if none).
	NextScheduledRestart func(inst *resolve.Instance) time.Time
	Log                  func(format string, a ...any)
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) logf(format string, a ...any) {
	if e.Log != nil {
		e.Log(format, a...)
	}
}

func (e *Engine) notify(ctx context.Context, ev notify.Event, inst *resolve.Instance) {
	if e.Notify == nil {
		return
	}
	if inst != nil {
		ev.Targets = inst.Notify.Discord // nil: the default webhook; empty: nobody
	}
	if err := e.Notify(ctx, ev); err != nil {
		e.logf("notification failed: %v", err)
	}
}

// Outcome is what happened to one instance.
type Outcome struct {
	Instance string
	Pending  []string
	Action   string // none, waiting, ready, notified, recorded, applied, failed
	Reason   string
	Err      error
}

// Report is the result of a check.
type Report struct {
	Paused    bool // Steam wants a login
	Refreshed bool
	Outcomes  []Outcome
}

func mods(pending []string) []string {
	var out []string
	for _, p := range pending {
		if p != BuildKey {
			out = append(out, p)
		}
	}
	return out
}

// DefaultAnnounceText is the countdown text of a mod update restart; the
// countdown appends " in N minutes".
const DefaultAnnounceText = "MOD UPDATE! Restart"

func announcement(inst *resolve.Instance) instance.Announcement {
	a := inst.Updates.RestartAnnounce
	if strings.TrimSpace(a.Text) == "" {
		a.Text = DefaultAnnounceText
	}
	return instance.Announcement{Minutes: a.Minutes, Lock: a.Lock, Kick: a.Kick, Text: a.Text}
}

// Check runs one update check. Without apply it only refreshes, records and
// notifies; with apply it also updates the instances whose policy and window
// allow it now.
func (e *Engine) Check(ctx context.Context, apply bool) (Report, error) {
	var rep Report
	path := StatePath(e.Cfg)
	st, err := LoadState(path)
	if err != nil {
		return rep, err
	}
	tree, err := e.Load()
	if err != nil {
		return rep, err
	}
	now := e.now()

	if e.SteamAuthRequired != nil && e.SteamAuthRequired() {
		rep.Paused = true
		e.pause(ctx, &st, now, "the Steam login has expired")
		return rep, st.Save(path)
	}

	names := tree.InstanceNames()
	insts := map[string]*resolve.Instance{}
	for _, n := range names {
		inst, err := resolve.Resolve(e.Cfg, tree, n)
		if err != nil {
			rep.Outcomes = append(rep.Outcomes, Outcome{Instance: n, Action: "failed", Err: err})
			continue
		}
		insts[n] = inst
	}

	var due []string
	for _, n := range names {
		inst := insts[n]
		if inst == nil {
			continue
		}
		iv := inst.Updates.CheckInterval.Std()
		if iv <= 0 {
			iv = defaultCheckInterval
		}
		if last := st.inst(n).LastCheck; last.IsZero() || now.Sub(last) >= iv {
			due = append(due, n)
		}
	}
	if len(due) > 0 && e.Refresh != nil {
		if err := e.Refresh(ctx, due); err != nil {
			if errors.Is(err, steam.ErrAuthRequired) {
				if e.MarkAuthRequired != nil {
					e.MarkAuthRequired()
				}
				rep.Paused = true
				e.pause(ctx, &st, now, err.Error())
				return rep, st.Save(path)
			}
			e.logf("refresh failed: %v", err)
			e.notify(ctx, notify.Event{Kind: notify.KindJobFailed, Data: map[string]any{"Job": "update check", "Error": err.Error()}}, nil)
		} else {
			rep.Refreshed = true
		}
	}
	for _, n := range due {
		if insts[n] == nil {
			continue
		}
		is := st.inst(n)
		is.LastCheck = now
		pending := units.Pending(insts[n])
		switch {
		case len(pending) == 0:
			is.Pending, is.PendingSince, is.Notified = nil, time.Time{}, ""
		default:
			if is.PendingSince.IsZero() || len(mods(is.Pending)) == 0 {
				is.PendingSince = now
			}
			is.Pending = pending
		}
	}
	st.LastCheck = now

	for _, n := range names {
		inst := insts[n]
		if inst == nil {
			continue
		}
		rep.Outcomes = append(rep.Outcomes, e.act(ctx, &st, inst, apply, now))
	}
	return rep, st.Save(path)
}

func (e *Engine) pause(ctx context.Context, st *State, now time.Time, reason string) {
	if now.Sub(st.SteamNotified) >= steamReminder {
		st.SteamNotified = now
		e.notify(ctx, notify.Event{Kind: notify.KindSteamLoginRequired, Data: map[string]any{"Reason": reason}}, nil)
	}
	e.logf("paused: %s; nothing is downloaded and no server is restarted for an update until `dzo steam login` succeeds", reason)
}

// fingerprint identifies a pending set by the mods and the generations they
// would be updated to, so a newer generation of the same mod is news again.
func fingerprint(inst *resolve.Instance, pending []string) string {
	var out []string
	for _, p := range pending {
		gen := ""
		for _, m := range inst.Mods {
			if m.Name == p {
				gen = m.Generation
			}
		}
		out = append(out, p+"@"+gen)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// act applies the instance's update policy to what is pending.
func (e *Engine) act(ctx context.Context, st *State, inst *resolve.Instance, apply bool, now time.Time) Outcome {
	is := st.inst(inst.Name)
	out := Outcome{Instance: inst.Name, Pending: is.Pending}
	modsPending := mods(is.Pending)
	if len(is.Pending) == 0 {
		out.Action, out.Reason = "none", "up to date"
		is.Decision = out.Reason
		return out
	}
	if len(modsPending) == 0 {
		// only a new server build: reported, never applied (D7)
		out.Action, out.Reason = "recorded", "a new server build is installed; apply it with a restart when you are ready"
		is.Decision = out.Reason
		return out
	}

	switch inst.Updates.Policy {
	case site.PolicyManual:
		out.Action, out.Reason = "recorded", "policy manual: restart when you are ready"
	case site.PolicyNotify:
		out.Action, out.Reason = "recorded", "policy notify"
		if fp := fingerprint(inst, modsPending); is.Notified != fp {
			is.Notified = fp
			out.Action = "notified"
			e.notify(ctx, notify.Event{Kind: notify.KindModUpdateDetected, Instance: inst.Name, Data: map[string]any{"Count": len(modsPending)}}, inst)
		}
	default: // auto, or unset
		in := product.ScheduleInput{PendingSince: is.PendingSince, LastRestartAt: is.LastRestartAt, Now: now}
		if e.NextScheduledRestart != nil {
			in.NextScheduledRestart = e.NextScheduledRestart(inst)
		}
		d, err := product.EvaluateSchedule(inst.Updates, in)
		if err != nil {
			out.Action, out.Err, out.Reason = "failed", err, "the update schedule is invalid"
			break
		}
		out.Reason = d.Reason
		if !d.Apply {
			out.Action = "waiting"
			break
		}
		if !apply {
			out.Action = "ready"
			break
		}
		e.notify(ctx, notify.Event{Kind: notify.KindRestartStarted, Instance: inst.Name, Data: map[string]any{"Reason": "mod update"}}, inst)
		if err := e.Apply(ctx, inst, announcement(inst)); err != nil {
			out.Action, out.Err = "failed", err
			is.LastError = err.Error()
			e.notify(ctx, notify.Event{Kind: notify.KindJobFailed, Instance: inst.Name, Data: map[string]any{"Job": "mod update of " + inst.Name, "Error": err.Error()}}, inst)
			break
		}
		out.Action = "applied"
		is.LastError, is.Pending, is.PendingSince, is.Notified, is.LastRestartAt = "", nil, time.Time{}, "", now
		e.notify(ctx, notify.Event{Kind: notify.KindModUpdateApplied, Instance: inst.Name, Data: map[string]any{"Count": len(modsPending)}}, inst)
	}
	is.Decision = out.Action + ": " + out.Reason
	return out
}

// genStore is what GC needs of a cache store.
type genStore interface {
	Generations() ([]string, error)
	Current() (string, error)
	Remove(string) error
}

// GC removes generations that no instance uses any more and that are older
// than minAge: neither current, nor the one a deployed unit was written with.
// A running server never sees files change, because it only ever uses what its
// unit names, and that is never removed here. It returns what it removed.
func (e *Engine) GC(minAge time.Duration) ([]string, error) {
	tree, err := e.Load()
	if err != nil {
		return nil, err
	}
	type target struct {
		label string
		root  string
		keep  map[string]bool
		store genStore
	}
	targets := map[string]*target{}
	add := func(label, root string, store genStore) *target {
		if targets[root] == nil {
			targets[root] = &target{label: label, root: root, keep: map[string]bool{}, store: store}
		}
		return targets[root]
	}
	cache := e.Cfg.Paths.Cache
	for name := range e.Cfg.Products {
		s := product.ProductStore(cache, name)
		add("product "+name, s.Root, s)
	}
	for _, n := range tree.InstanceNames() {
		inst, err := resolve.Resolve(e.Cfg, tree, n)
		if err != nil {
			continue
		}
		applied, _ := units.Applied(inst)
		if t := targets[product.ProductStore(cache, inst.Product.Name).Root]; t != nil && applied.Build != "" {
			t.keep[applied.Build] = true
		}
		for _, m := range inst.Mods {
			s := product.ModStore(cache, inst.Product.WorkshopAppID, m.ID)
			if m.Local != "" {
				s = product.LocalModStore(cache, m.Local)
			}
			t := add("mod "+m.Name, s.Root, s)
			if g := applied.Mods[m.Name]; g != "" {
				t.keep[g] = true
			}
		}
	}
	var removed []string
	now := e.now()
	for _, t := range targets {
		ids, err := t.store.Generations()
		if err != nil {
			return removed, err
		}
		cur, _ := t.store.Current()
		for _, id := range ids {
			if id == cur || t.keep[id] {
				continue
			}
			fi, err := os.Stat(filepath.Join(t.root, id))
			if err != nil || now.Sub(fi.ModTime()) < minAge {
				continue
			}
			if err := t.store.Remove(id); err != nil {
				return removed, err
			}
			removed = append(removed, t.label+"/"+id)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

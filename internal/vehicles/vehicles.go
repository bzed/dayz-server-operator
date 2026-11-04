// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vehicles turns the vehicle snapshots of each instance into the vehicle records: who drove a vehicle
// last and when. It reads what dzo-admin reports; it never talks to the game server and changes nothing on the
// vehicles.
package vehicles

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// DefaultForgetDays is how long the record of a vehicle that is no longer reported is kept.
const DefaultForgetDays = 7

// ForgetAfter is how long the record of a vanished vehicle of an instance is kept (admin.vehicle_forget_days).
func ForgetAfter(a site.AdminConfig) time.Duration {
	days := DefaultForgetDays
	if a.VehicleForgetDays != nil {
		days = *a.VehicleForgetDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// Store keeps the records (moderation.Store).
type Store interface {
	SyncVehicles(ctx context.Context, instance string, seen []moderation.VehicleSeen, now time.Time) error
	ForgetVehicles(ctx context.Context, instance string, cutoff time.Time) (int64, error)
	// ForgetRemovedInstances forgets the records of instances not in active, after the default time.
	ForgetRemovedInstances(ctx context.Context, active []string, now, cutoff time.Time) (int64, error)
}

// Source is what the recorder needs of an instance's hub (admin.Hub).
type Source interface {
	Vehicles() []admin.Vehicle
	Subscribe(ctx context.Context) <-chan admin.StreamEvent
	// ReportsDrivers tells whether the mod announced admin.FeatureDriver. Without it nothing is recorded: the
	// age of a vehicle would start long before the mod could tell who drove it.
	ReportsDrivers() bool
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

// Recorder keeps one goroutine per instance.
type Recorder struct {
	Store Store
	// Now is the clock (default time.Now). Tick paces the forget sweep (default: hourly); tests replace both.
	Now  func() time.Time
	Tick <-chan time.Time
	Logf func(string, ...any)
	// Forget is how long the record of a vanished vehicle of an instance is kept (default: 7 days).
	Forget func(instance string) time.Duration

	mu    sync.Mutex
	insts map[string]*running
	wg    sync.WaitGroup

	// active is the instance list of the last Set, read by the sweeps (which must not take mu: Set holds it
	// while it waits for an instance to stop).
	activeMu sync.Mutex
	active   []string
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

func (r *Recorder) forgetAfter(name string) time.Duration {
	if r.Forget != nil {
		return r.Forget(name)
	}
	return DefaultForgetDays * 24 * time.Hour
}

// Set starts and stops the recording so it matches instances. An instance that stays keeps running; one that is
// gone stops, and Set returns once it has. ctx ends all of them.
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
	r.activeMu.Lock()
	r.active = slices.Sorted(maps.Keys(want))
	r.activeMu.Unlock()
	r.sweepRemoved(ctx)
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

// sweepRemoved forgets the records of instances that are no longer on the site, after the default time (the
// settings of a removed instance are gone).
func (r *Recorder) sweepRemoved(ctx context.Context) {
	r.activeMu.Lock()
	active := slices.Clone(r.active)
	r.activeMu.Unlock()
	now := r.now()
	if _, err := r.Store.ForgetRemovedInstances(ctx, active, now, now.Add(-DefaultForgetDays*24*time.Hour)); err != nil && ctx.Err() == nil {
		r.logf("vehicles: removed instances: %v", err)
	}
}

// Wait blocks until every goroutine has ended, which they do once the context given to Set is done.
func (r *Recorder) Wait() { r.wg.Wait() }

func (r *Recorder) run(ctx context.Context, in *running, name string, src Source) {
	defer r.wg.Done()
	defer close(in.done)
	sub := src.Subscribe(ctx)
	// A snapshot that is already there is recorded. None yet is not "no vehicles": nothing is hidden before
	// the mod has reported.
	if list := src.Vehicles(); len(list) > 0 {
		r.sync(ctx, name, src, list)
	}
	tick := r.Tick
	if tick == nil {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			if list, ok := ev.Data.([]admin.Vehicle); ev.Kind == "vehicles" && ok {
				r.sync(ctx, name, src, list)
			}
		case <-tick:
			cutoff := r.now().Add(-r.forgetAfter(name))
			if _, err := r.Store.ForgetVehicles(ctx, name, cutoff); err != nil && ctx.Err() == nil {
				r.logf("vehicles: %s: %v", name, err)
			}
			r.sweepRemoved(ctx)
		}
	}
}

// sync records one snapshot. An empty one is a real snapshot (the records are hidden, not deleted).
// It is judged at every snapshot, so the records start once the mod is updated.
func (r *Recorder) sync(ctx context.Context, name string, src Source, list []admin.Vehicle) {
	if !src.ReportsDrivers() {
		return
	}
	seen := make([]moderation.VehicleSeen, 0, len(list))
	for _, v := range list {
		seen = append(seen, moderation.VehicleSeen{ID: v.ID, Type: v.Type, Driver: v.Driver, DriverName: v.DriverName, DrivenAt: v.DrivenAt})
	}
	if err := r.Store.SyncVehicles(ctx, name, seen, r.now()); err != nil && ctx.Err() == nil {
		r.logf("vehicles: %s: %v", name, err)
	}
}

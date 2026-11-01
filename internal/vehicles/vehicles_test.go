// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package vehicles

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/site"
)

type syncCall struct {
	instance string
	seen     []moderation.VehicleSeen
	now      time.Time
}

type forgetCall struct {
	instance string
	cutoff   time.Time
}

type removedCall struct {
	active []string
	now    time.Time
	cutoff time.Time
}

type fakeStore struct {
	mu      sync.Mutex
	removed []removedCall
	syncs   []syncCall
	forgets []forgetCall
}

func (f *fakeStore) SyncVehicles(_ context.Context, instance string, seen []moderation.VehicleSeen, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs = append(f.syncs, syncCall{instance, seen, now})
	return nil
}

func (f *fakeStore) ForgetVehicles(_ context.Context, instance string, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgets = append(f.forgets, forgetCall{instance, cutoff})
	return 0, nil
}

func (f *fakeStore) ForgetRemovedInstances(_ context.Context, active []string, now, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, removedCall{slices.Sorted(slices.Values(active)), now, cutoff})
	return 0, nil
}

func (f *fakeStore) nRemoved() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.removed)
}

func (f *fakeStore) nSyncs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.syncs)
}

func (f *fakeStore) nForgets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.forgets)
}

type fakeSource struct {
	vehicles []admin.Vehicle
	ch       chan admin.StreamEvent
	mu       sync.Mutex
	oldMod   bool
}

// setOldMod makes the mod stop (or start) reporting drivers, as an update of the mod does.
func (s *fakeSource) setOldMod(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oldMod = v
}

func (s *fakeSource) ReportsDrivers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.oldMod
}

func (s *fakeSource) Vehicles() []admin.Vehicle { return s.vehicles }
func (s *fakeSource) Subscribe(context.Context) <-chan admin.StreamEvent {
	return s.ch
}

func newSource(v ...admin.Vehicle) *fakeSource {
	return &fakeSource{vehicles: v, ch: make(chan admin.StreamEvent, 8)}
}

func hubs(src Source) func(string) (Source, bool) {
	return func(string) (Source, bool) { return src, true }
}

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func TestRecorderSyncsEachVehiclesEvent(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	rec := &Recorder{Store: st, Now: func() time.Time { return t0 }}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	drove := t0.Add(-time.Hour)
	src.ch <- admin.StreamEvent{Kind: "vehicles", Data: []admin.Vehicle{
		{ID: "a", Type: "Offroad_02", Driver: "76561198000000001", DriverName: "Alice", DrivenAt: drove},
		{ID: "b", Type: "Sedan_02"},
	}}
	require.Eventually(t, func() bool { return st.nSyncs() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
	c := st.syncs[0]
	require.Equal(t, "alpha", c.instance)
	require.Equal(t, t0, c.now)
	require.Equal(t, []moderation.VehicleSeen{
		{ID: "a", Type: "Offroad_02", Driver: "76561198000000001", DriverName: "Alice", DrivenAt: drove},
		{ID: "b", Type: "Sedan_02"},
	}, c.seen)
}

func TestRecorderSyncsAnEmptyEvent(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	rec := &Recorder{Store: st}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	src.ch <- admin.StreamEvent{Kind: "vehicles", Data: []admin.Vehicle(nil)}
	require.Eventually(t, func() bool { return st.nSyncs() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
	require.NotNil(t, st.syncs[0].seen, "an empty snapshot is a snapshot: the records are hidden")
	require.Empty(t, st.syncs[0].seen)
}

func TestRecorderIgnoresOtherEvents(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	rec := &Recorder{Store: st}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	src.ch <- admin.StreamEvent{Kind: "players", Data: []admin.Player{{SteamID: "76561198000000001"}}}
	src.ch <- admin.StreamEvent{Kind: "status", Data: admin.Status{}}
	src.ch <- admin.StreamEvent{Kind: "vehicles", Data: []admin.Vehicle{{ID: "a"}}}
	require.Eventually(t, func() bool { return st.nSyncs() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
	require.Len(t, st.syncs, 1, "only the vehicles event syncs")
}

func TestRecorderDoesNotHideOnStart(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	tick := make(chan time.Time)
	rec := &Recorder{Store: st, Tick: tick}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	tick <- t0 // the loop is running, and has not synced anything
	require.Eventually(t, func() bool { return st.nForgets() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
	require.Zero(t, st.nSyncs(), "no snapshot yet is not 'no vehicles'")
}

func TestRecorderSyncsTheSnapshotThatIsThere(t *testing.T) {
	st, src := &fakeStore{}, newSource(admin.Vehicle{ID: "a", Type: "Offroad_02"})
	rec := &Recorder{Store: st}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	require.Eventually(t, func() bool { return st.nSyncs() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
}

func TestRecorderForgets(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	tick := make(chan time.Time)
	rec := &Recorder{Store: st, Tick: tick, Now: func() time.Time { return t0 },
		Forget: func(string) time.Duration { return 3 * 24 * time.Hour }}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	tick <- t0
	require.Eventually(t, func() bool { return st.nForgets() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
	require.Equal(t, forgetCall{"alpha", t0.Add(-3 * 24 * time.Hour)}, st.forgets[0])
}

func TestRecorderForgetsAfterAWeekByDefault(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	tick := make(chan time.Time)
	rec := &Recorder{Store: st, Tick: tick, Now: func() time.Time { return t0 }}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	tick <- t0
	require.Eventually(t, func() bool { return st.nForgets() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
	require.Equal(t, t0.Add(-7*24*time.Hour), st.forgets[0].cutoff)
}

func TestRecorderStopsWithContext(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	rec := &Recorder{Store: st}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	cancel()
	rec.Wait()
}

func TestRecorderSetStopsRemovedInstances(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	rec := &Recorder{Store: st}
	ctx, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); rec.Wait() }()
	rec.Set(ctx, hubs(src), []string{"alpha", "beta"})
	rec.Set(ctx, hubs(src), []string{"alpha"}) // returns once beta has stopped
	rec.Set(ctx, func(string) (Source, bool) { return nil, false }, []string{"alpha", "gone"})
}

func TestRecorderForgetsRemovedInstances(t *testing.T) {
	st, src := &fakeStore{}, newSource()
	tick := make(chan time.Time)
	rec := &Recorder{Store: st, Tick: tick, Now: func() time.Time { return t0 }}
	ctx, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); rec.Wait() }()
	rec.Set(ctx, hubs(src), []string{"alpha", "beta"})
	require.Equal(t, 1, st.nRemoved(), "a set forgets the removed instances")
	rec.Set(ctx, hubs(src), []string{"alpha"})
	require.Equal(t, 2, st.nRemoved())
	tick <- t0 // the sweep of the remaining instance covers the removed ones too
	require.Eventually(t, func() bool { return st.nRemoved() == 3 }, 5*time.Second, 5*time.Millisecond)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, removedCall{[]string{"alpha"}, t0, t0.Add(-7 * 24 * time.Hour)}, st.removed[2])
}

func TestForgetAfter(t *testing.T) {
	require.Equal(t, 7*24*time.Hour, ForgetAfter(site.AdminConfig{}))
	d := 30
	require.Equal(t, 30*24*time.Hour, ForgetAfter(site.AdminConfig{VehicleForgetDays: &d}))
}

func TestRecorderWaitsForAModThatReportsDrivers(t *testing.T) {
	st, src := &fakeStore{}, newSource(admin.Vehicle{ID: "a", Type: "Offroad_02"})
	src.setOldMod(true)
	rec := &Recorder{Store: st}
	ctx, cancel := context.WithCancel(t.Context())
	rec.Set(ctx, hubs(src), []string{"alpha"})
	src.ch <- admin.StreamEvent{Kind: "vehicles", Data: []admin.Vehicle{{ID: "a"}}}
	src.ch <- admin.StreamEvent{Kind: "vehicles", Data: []admin.Vehicle{{ID: "a"}}}
	// The marker event is also ignored; a later one proves the earlier ones were handled.
	src.ch <- admin.StreamEvent{Kind: "players", Data: []admin.Player{}}
	require.Eventually(t, func() bool { return len(src.ch) == 0 }, 5*time.Second, 5*time.Millisecond)
	require.Zero(t, st.nSyncs(), "an old mod reports no drivers: nothing is recorded, or every car would look unused later")
	src.setOldMod(false) // the mod is updated
	src.ch <- admin.StreamEvent{Kind: "vehicles", Data: []admin.Vehicle{{ID: "a", Driver: "76561198000000001"}}}
	require.Eventually(t, func() bool { return st.nSyncs() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	rec.Wait()
}

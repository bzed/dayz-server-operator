// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package updates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/notify"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/steam"
	"github.com/bzed/dayz-server-operator/internal/units"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t testing.TB, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

type env struct {
	t    testing.TB
	data string
	cfg  *config.Config
	e    *Engine
	now  time.Time

	refreshed   [][]string
	refreshErr  error
	applied     []string
	applyErr    error
	events      []notify.Event
	authNeeded  bool
	marked      bool
	nextRestart time.Time
}

const instanceYAML = `name: x
product: dayz-stable
map: m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
mods:
  - {local: tools, server: true}
`

func newEnv(t testing.TB, extra string) *env {
	t.Helper()
	data := t.TempDir()
	cfg, err := config.Parse([]byte("paths:\n  data: " + data + "\n"))
	require.NoError(t, err)
	ev := &env{t: t, data: data, cfg: cfg, now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	write(t, filepath.Join(data, "site", "site.yaml"), "local_mods:\n  tools: {path: "+filepath.Join(data, "src")+"}\n")
	write(t, filepath.Join(data, "site", "instances", "x", "instance.yaml"), instanceYAML+extra)
	ev.build("1")
	ev.mod("g1")
	_ = ev.deploy()
	ev.e = &Engine{
		Cfg: cfg, Load: func() (*site.Tree, error) { return site.LoadTree(cfg.Paths.Site) },
		Now: func() time.Time { return ev.now },
		Refresh: func(_ context.Context, names []string) error {
			ev.refreshed = append(ev.refreshed, names)
			return ev.refreshErr
		},
		Apply: func(_ context.Context, inst *resolve.Instance, _ instance.Announcement) error {
			if ev.applyErr != nil {
				return ev.applyErr
			}
			ev.applied = append(ev.applied, inst.Name)
			return ev.deploy()
		},
		Notify:               func(_ context.Context, e notify.Event) error { ev.events = append(ev.events, e); return nil },
		SteamAuthRequired:    func() bool { return ev.authNeeded },
		MarkAuthRequired:     func() { ev.marked = true },
		NextScheduledRestart: func(*resolve.Instance) time.Time { return ev.nextRestart },
	}
	return ev
}

func (ev *env) tree() *site.Tree {
	tree, err := site.LoadTree(ev.cfg.Paths.Site)
	require.NoError(ev.t, err)
	return tree
}

func (ev *env) build(id string) {
	ev.t.Helper()
	s := product.ProductStore(ev.cfg.Paths.Cache, "dayz-stable")
	dir, err := s.NewGeneration(id)
	require.NoError(ev.t, err)
	write(ev.t, filepath.Join(dir, "DayZServer"), id)
	require.NoError(ev.t, s.SetCurrent(id))
}

func (ev *env) mod(gen string) {
	ev.t.Helper()
	s := product.LocalModStore(ev.cfg.Paths.Cache, "tools")
	dir, err := s.NewGeneration(gen)
	require.NoError(ev.t, err)
	write(ev.t, filepath.Join(dir, "addons", "a.pbo"), gen)
	require.NoError(ev.t, s.SetCurrent(gen))
}

// deploy records the current generations, like a unit sync does.
func (ev *env) deploy() error {
	inst, err := resolve.Resolve(ev.cfg, ev.tree(), "x")
	if err != nil {
		return err
	}
	return units.Record(inst)
}

func (ev *env) check(apply bool) Report {
	ev.t.Helper()
	rep, err := ev.e.Check(ev.t.Context(), apply)
	require.NoError(ev.t, err)
	return rep
}

func (ev *env) outcome(rep Report) Outcome {
	ev.t.Helper()
	require.Len(ev.t, rep.Outcomes, 1)
	return rep.Outcomes[0]
}

func (ev *env) kinds() string {
	var ks []string
	for _, e := range ev.events {
		ks = append(ks, string(e.Kind))
	}
	return strings.Join(ks, ",")
}

func (ev *env) advance(d time.Duration) { ev.now = ev.now.Add(d) }

func TestUpToDateAndTheCheckInterval(t *testing.T) {
	ev := newEnv(t, "")
	o := ev.outcome(ev.check(true))
	require.Equal(t, "none", o.Action, "outcome %+v", o)
	require.Len(t, ev.refreshed, 1)
	require.Empty(t, ev.applied)
	ev.advance(30 * time.Minute)
	ev.check(true)
	assert.Len(t, ev.refreshed, 1, "an instance is only refreshed when its check_interval (1h by default) is due")
	ev.advance(31 * time.Minute)
	ev.check(true)
	assert.Len(t, ev.refreshed, 2, "an hour later it is due again")
}

func TestAutoAppliesInOneAnnouncedCycle(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto, restart_announce: {minutes: 5, lock: 2, kick: 3, text: MOD UPDATE}}\n")
	ev.mod("g2") // Refresh finds a new generation
	o := ev.outcome(ev.check(false))
	require.Equal(t, "ready", o.Action, "without --apply nothing is restarted")
	require.Empty(t, ev.applied, "without --apply nothing is restarted")
	ev.advance(time.Hour)
	o = ev.outcome(ev.check(true))
	require.Equal(t, "applied", o.Action)
	require.Len(t, ev.applied, 1)
	assert.Equal(t, "restart_started,mod_update_applied", ev.kinds())
	st, _ := LoadState(StatePath(ev.cfg))
	is := st.Instances["x"]
	assert.Empty(t, is.Pending)
	assert.True(t, is.PendingSince.IsZero(), "state = %+v", is)
	assert.False(t, is.LastRestartAt.IsZero(), "state = %+v", is)
	ev.advance(time.Hour)
	o = ev.outcome(ev.check(true))
	assert.Equal(t, "none", o.Action, "after the update nothing is pending")
}

func TestBatchDelayWindowMinIntervalAndMaxDelay(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto, batch_delay: 20m, window: ['06:00-18:00'], min_restart_interval: 4h, max_delay: 10h}\n")
	ev.mod("g2")
	o := ev.outcome(ev.check(true))
	require.Equal(t, "waiting", o.Action, "first detection waits for more updates")
	require.Contains(t, o.Reason, "batch_delay")
	ev.advance(61 * time.Minute) // past the batch delay, inside the window
	o = ev.outcome(ev.check(true))
	require.Equal(t, "applied", o.Action, "%+v", o)
	ev.mod("g3")
	ev.advance(time.Hour)
	o = ev.outcome(ev.check(true))
	assert.Equal(t, "waiting", o.Action, "a second update within 4 h is batched")
	assert.Contains(t, o.Reason, "min_restart_interval")
	ev.advance(4 * time.Hour) // 19:00, outside the window
	o = ev.outcome(ev.check(true))
	assert.Equal(t, "waiting", o.Action, "outside the window")
	assert.Contains(t, o.Reason, "window")
	ev.advance(6 * time.Hour) // pending for more than max_delay
	o = ev.outcome(ev.check(true))
	assert.Equal(t, "applied", o.Action, "max_delay applies even outside the window")
	assert.Contains(t, o.Reason, "max_delay")
}

func TestAScheduledRestartIsUsedInstead(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto, apply_with_scheduled_restart: true}\n")
	ev.mod("g2")
	ev.nextRestart = ev.now.Add(2 * time.Hour)
	o := ev.outcome(ev.check(true))
	assert.Equal(t, "waiting", o.Action)
	assert.Contains(t, o.Reason, "scheduled restart")
}

func TestNotifyAndManualPolicies(t *testing.T) {
	ev := newEnv(t, "updates: {policy: notify}\nnotify: {discord: [admins]}\n")
	ev.mod("g2")
	o := ev.outcome(ev.check(true))
	require.Equal(t, "notified", o.Action)
	require.Equal(t, "mod_update_detected", ev.kinds())
	require.Empty(t, ev.applied)
	require.NotNil(t, ev.events[0].Targets, "the instance's webhooks are used")
	assert.Equal(t, "admins", (*ev.events[0].Targets)[0], "the instance's webhooks are used")
	ev.advance(2 * time.Hour)
	o = ev.outcome(ev.check(true))
	assert.Equal(t, "recorded", o.Action, "the same pending set is told once")
	assert.Len(t, ev.events, 1, "the same pending set is told once: %s", ev.kinds())
	ev.mod("g3")
	ev.advance(2 * time.Hour)
	ev.check(true)
	assert.Len(t, ev.events, 2, "a changed set is told again: %s", ev.kinds())

	ev = newEnv(t, "updates: {policy: manual}\n")
	ev.mod("g2")
	o = ev.outcome(ev.check(true))
	assert.Equal(t, "recorded", o.Action, "manual only records")
	assert.Empty(t, ev.events, "manual only records")
	assert.Empty(t, ev.applied, "manual only records")
	assert.Equal(t, 1, mustState(t, ev).Pending()["x"], "pending updates are counted for the exporter")
}

func mustState(t testing.TB, ev *env) State {
	t.Helper()
	st, err := LoadState(StatePath(ev.cfg))
	require.NoError(t, err)
	return st
}

func TestANewServerBuildIsNeverApplied(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto}\n")
	ev.build("2")
	o := ev.outcome(ev.check(true))
	assert.Equal(t, "recorded", o.Action, "D7: a build is reported, not applied")
	assert.Contains(t, o.Reason, "server build")
	assert.Empty(t, ev.applied)
	assert.Zero(t, mustState(t, ev).Pending()["x"], "a pending build is not a mod update")
}

func TestSteamAuthPausesEverything(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto}\n")
	ev.mod("g2")
	ev.authNeeded = true
	rep := ev.check(true)
	require.True(t, rep.Paused)
	require.Empty(t, ev.refreshed)
	require.Empty(t, ev.applied)
	require.Equal(t, "steam_login_required", ev.kinds())
	ev.advance(time.Hour)
	ev.check(true)
	assert.Len(t, ev.events, 1, "one notification, then a reminder after hours: %s", ev.kinds())
	ev.advance(6 * time.Hour)
	ev.check(true)
	assert.Len(t, ev.events, 2, "the reminder: %s", ev.kinds())
	ev.authNeeded = false
	ev.advance(time.Hour)
	o := ev.outcome(ev.check(true))
	assert.Equal(t, "applied", o.Action, "after the login the pending update goes ahead")

	// a download that finds the login expired pauses too
	ev = newEnv(t, "updates: {policy: auto}\n")
	ev.refreshErr = fmt.Errorf("%w: run `dzo steam login`", steam.ErrAuthRequired)
	rep = ev.check(true)
	assert.True(t, rep.Paused)
	assert.True(t, ev.marked)
	assert.Equal(t, "steam_login_required", ev.kinds())
}

func TestRefreshAndApplyFailures(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto}\n")
	ev.refreshErr = errors.New("steamcmd timed out")
	rep := ev.check(true)
	assert.False(t, rep.Refreshed, "a failed refresh is reported and the check goes on")
	assert.Equal(t, "job_failed", ev.kinds(), "a failed refresh is reported and the check goes on")
	assert.Equal(t, "none", ev.outcome(rep).Action, "a failed refresh is reported and the check goes on")

	ev = newEnv(t, "updates: {policy: auto}\n")
	ev.mod("g2")
	ev.applyErr = errors.New("the snapshot failed")
	o := ev.outcome(ev.check(true))
	require.Equal(t, "failed", o.Action)
	require.Error(t, o.Err)
	require.Equal(t, "restart_started,job_failed", ev.kinds())
	st := mustState(t, ev)
	assert.Len(t, st.Instances["x"].Pending, 1, "the update stays pending")
	assert.Equal(t, "the snapshot failed", st.Instances["x"].LastError, "the update stays pending")
	assert.True(t, st.Instances["x"].LastRestartAt.IsZero(), "the update stays pending")
	ev.applyErr = nil
	ev.advance(time.Hour)
	o = ev.outcome(ev.check(true))
	assert.Equal(t, "applied", o.Action, "the next check tries again")
}

func TestAnInstanceThatDoesNotResolveDoesNotStopTheOthers(t *testing.T) {
	ev := newEnv(t, "")
	write(t, filepath.Join(ev.data, "site", "instances", "bad", "instance.yaml"), "name: bad\nproduct: nope\nmap: m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 1}\nnetwork: host\n")
	rep := ev.check(true)
	var bad, good Outcome
	for _, o := range rep.Outcomes {
		if o.Instance == "bad" {
			bad = o
		} else {
			good = o
		}
	}
	assert.Equal(t, "failed", bad.Action)
	require.Error(t, bad.Err)
	assert.Equal(t, "none", good.Action)
}

func TestStateFile(t *testing.T) {
	ev := newEnv(t, "")
	st, err := LoadState(filepath.Join(ev.data, "nothing"))
	require.NoError(t, err, "a missing state is an empty one")
	assert.NotNil(t, st.Instances, "a missing state is an empty one")
	write(t, StatePath(ev.cfg), "{broken")
	_, err = ev.e.Check(t.Context(), true)
	assert.Error(t, err, "a corrupt state file must not be ignored silently")
}

func TestGCKeepsWhatIsInUse(t *testing.T) {
	ev := newEnv(t, "")
	ev.mod("g2") // current is g2, the unit runs g1
	ev.mod("g3") // current is g3
	old := ev.now.Add(-40 * 24 * time.Hour)
	root := product.LocalModStore(ev.cfg.Paths.Cache, "tools").Root
	for _, g := range []string{"g1", "g2"} {
		require.NoError(t, os.Chtimes(filepath.Join(root, g), old, old))
	}
	removed, err := ev.e.GC(14 * 24 * time.Hour)
	require.NoError(t, err)
	require.Equal(t, []string{"mod @tools/g2"}, removed, "g1 runs, g3 is current, g2 is old and unused")
	for _, g := range []string{"g1", "g3"} {
		assert.DirExists(t, filepath.Join(root, g), "%s must stay", g)
	}
	ev.mod("g4")
	require.NoError(t, os.Chtimes(filepath.Join(root, "g3"), old, old))
	_ = ev.deploy() // the unit now runs g4; g1 and g3 are no longer used
	_ = os.Chtimes(filepath.Join(root, "g1"), old, old)
	removed, _ = ev.e.GC(14 * 24 * time.Hour)
	assert.Len(t, removed, 2)
	removed, _ = ev.e.GC(14 * 24 * time.Hour)
	assert.Empty(t, removed, "nothing left to remove")
}

func TestAnnouncementDefaultsText(t *testing.T) {
	inst := &resolve.Instance{}
	assert.Equal(t, "MOD UPDATE! Restart", announcement(inst).Text, "default text")
	inst.Updates.RestartAnnounce.Text = "Patching"
	assert.Equal(t, "Patching", announcement(inst).Text, "configured text")
}

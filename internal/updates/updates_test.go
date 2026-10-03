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
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type env struct {
	t    *testing.T
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

func newEnv(t *testing.T, extra string) *env {
	t.Helper()
	data := t.TempDir()
	cfg, err := config.Parse([]byte("paths:\n  data: " + data + "\n"))
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		ev.t.Fatal(err)
	}
	return tree
}

func (ev *env) build(id string) {
	ev.t.Helper()
	s := product.ProductStore(ev.cfg.Paths.Cache, "dayz-stable")
	dir, err := s.NewGeneration(id)
	if err != nil {
		ev.t.Fatal(err)
	}
	write(ev.t, filepath.Join(dir, "DayZServer"), id)
	if err := s.SetCurrent(id); err != nil {
		ev.t.Fatal(err)
	}
}

func (ev *env) mod(gen string) {
	ev.t.Helper()
	s := product.LocalModStore(ev.cfg.Paths.Cache, "tools")
	dir, err := s.NewGeneration(gen)
	if err != nil {
		ev.t.Fatal(err)
	}
	write(ev.t, filepath.Join(dir, "addons", "a.pbo"), gen)
	if err := s.SetCurrent(gen); err != nil {
		ev.t.Fatal(err)
	}
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
	rep, err := ev.e.Check(context.Background(), apply)
	if err != nil {
		ev.t.Fatal(err)
	}
	return rep
}

func (ev *env) outcome(rep Report) Outcome {
	ev.t.Helper()
	if len(rep.Outcomes) != 1 {
		ev.t.Fatalf("outcomes = %+v", rep.Outcomes)
	}
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
	if o.Action != "none" || len(ev.refreshed) != 1 || len(ev.applied) != 0 {
		t.Fatalf("outcome %+v, refreshed %v", o, ev.refreshed)
	}
	ev.advance(30 * time.Minute)
	ev.check(true)
	if len(ev.refreshed) != 1 {
		t.Errorf("an instance is only refreshed when its check_interval (1h by default) is due: %v", ev.refreshed)
	}
	ev.advance(31 * time.Minute)
	ev.check(true)
	if len(ev.refreshed) != 2 {
		t.Errorf("an hour later it is due again: %v", ev.refreshed)
	}
}

func TestAutoAppliesInOneAnnouncedCycle(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto, restart_announce: {minutes: 5, lock: 2, delay: 3, text: MOD UPDATE}}\n")
	ev.mod("g2") // Refresh finds a new generation
	if o := ev.outcome(ev.check(false)); o.Action != "ready" || len(ev.applied) != 0 {
		t.Fatalf("without --apply nothing is restarted: %+v", o)
	}
	ev.advance(time.Hour)
	o := ev.outcome(ev.check(true))
	if o.Action != "applied" || len(ev.applied) != 1 {
		t.Fatalf("outcome %+v applied %v", o, ev.applied)
	}
	if ev.kinds() != "restart_started,mod_update_applied" {
		t.Errorf("events = %s", ev.kinds())
	}
	st, _ := LoadState(StatePath(ev.cfg))
	is := st.Instances["x"]
	if len(is.Pending) != 0 || !is.PendingSince.IsZero() || is.LastRestartAt.IsZero() {
		t.Errorf("state = %+v", is)
	}
	ev.advance(time.Hour)
	if o := ev.outcome(ev.check(true)); o.Action != "none" {
		t.Errorf("after the update nothing is pending: %+v", o)
	}
}

func TestBatchDelayWindowMinIntervalAndMaxDelay(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto, batch_delay: 20m, window: ['06:00-18:00'], min_restart_interval: 4h, max_delay: 10h}\n")
	ev.mod("g2")
	o := ev.outcome(ev.check(true))
	if o.Action != "waiting" || !strings.Contains(o.Reason, "batch_delay") {
		t.Fatalf("first detection waits for more updates: %+v", o)
	}
	ev.advance(61 * time.Minute) // past the batch delay, inside the window
	if o := ev.outcome(ev.check(true)); o.Action != "applied" {
		t.Fatalf("%+v", o)
	}
	ev.mod("g3")
	ev.advance(time.Hour)
	if o := ev.outcome(ev.check(true)); o.Action != "waiting" || !strings.Contains(o.Reason, "min_restart_interval") {
		t.Errorf("a second update within 4 h is batched: %+v", o)
	}
	ev.advance(4 * time.Hour) // 19:00, outside the window
	if o := ev.outcome(ev.check(true)); o.Action != "waiting" || !strings.Contains(o.Reason, "window") {
		t.Errorf("outside the window: %+v", o)
	}
	ev.advance(6 * time.Hour) // pending for more than max_delay
	if o := ev.outcome(ev.check(true)); o.Action != "applied" || !strings.Contains(o.Reason, "max_delay") {
		t.Errorf("max_delay applies even outside the window: %+v", o)
	}
}

func TestAScheduledRestartIsUsedInstead(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto, apply_with_scheduled_restart: true}\n")
	ev.mod("g2")
	ev.nextRestart = ev.now.Add(2 * time.Hour)
	if o := ev.outcome(ev.check(true)); o.Action != "waiting" || !strings.Contains(o.Reason, "scheduled restart") {
		t.Errorf("%+v", o)
	}
}

func TestNotifyAndManualPolicies(t *testing.T) {
	ev := newEnv(t, "updates: {policy: notify}\nnotify: {discord: [admins]}\n")
	ev.mod("g2")
	o := ev.outcome(ev.check(true))
	if o.Action != "notified" || ev.kinds() != "mod_update_detected" || len(ev.applied) != 0 {
		t.Fatalf("%+v events %s", o, ev.kinds())
	}
	if ev.events[0].Targets == nil || (*ev.events[0].Targets)[0] != "admins" {
		t.Errorf("the instance's webhooks are used: %+v", ev.events[0].Targets)
	}
	ev.advance(2 * time.Hour)
	if o := ev.outcome(ev.check(true)); o.Action != "recorded" || len(ev.events) != 1 {
		t.Errorf("the same pending set is told once: %+v events %s", o, ev.kinds())
	}
	ev.mod("g3")
	ev.advance(2 * time.Hour)
	ev.check(true)
	if len(ev.events) != 2 {
		t.Errorf("a changed set is told again: %s", ev.kinds())
	}

	ev = newEnv(t, "updates: {policy: manual}\n")
	ev.mod("g2")
	if o := ev.outcome(ev.check(true)); o.Action != "recorded" || len(ev.events) != 0 || len(ev.applied) != 0 {
		t.Errorf("manual only records: %+v events %s", o, ev.kinds())
	}
	if p := mustState(t, ev).Pending()["x"]; p != 1 {
		t.Errorf("pending updates are counted for the exporter: %d", p)
	}
}

func mustState(t *testing.T, ev *env) State {
	t.Helper()
	st, err := LoadState(StatePath(ev.cfg))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestANewServerBuildIsNeverApplied(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto}\n")
	ev.build("2")
	o := ev.outcome(ev.check(true))
	if o.Action != "recorded" || !strings.Contains(o.Reason, "server build") || len(ev.applied) != 0 {
		t.Errorf("D7: a build is reported, not applied: %+v", o)
	}
	if mustState(t, ev).Pending()["x"] != 0 {
		t.Error("a pending build is not a mod update")
	}
}

func TestSteamAuthPausesEverything(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto}\n")
	ev.mod("g2")
	ev.authNeeded = true
	rep := ev.check(true)
	if !rep.Paused || len(ev.refreshed) != 0 || len(ev.applied) != 0 || ev.kinds() != "steam_login_required" {
		t.Fatalf("paused %v refreshed %v applied %v events %s", rep.Paused, ev.refreshed, ev.applied, ev.kinds())
	}
	ev.advance(time.Hour)
	ev.check(true)
	if len(ev.events) != 1 {
		t.Errorf("one notification, then a reminder after hours: %s", ev.kinds())
	}
	ev.advance(6 * time.Hour)
	ev.check(true)
	if len(ev.events) != 2 {
		t.Errorf("the reminder: %s", ev.kinds())
	}
	ev.authNeeded = false
	ev.advance(time.Hour)
	if o := ev.outcome(ev.check(true)); o.Action != "applied" {
		t.Errorf("after the login the pending update goes ahead: %+v", o)
	}

	// a download that finds the login expired pauses too
	ev = newEnv(t, "updates: {policy: auto}\n")
	ev.refreshErr = fmt.Errorf("%w: run `dzo steam login`", steam.ErrAuthRequired)
	rep = ev.check(true)
	if !rep.Paused || !ev.marked || ev.kinds() != "steam_login_required" {
		t.Errorf("paused %v marked %v events %s", rep.Paused, ev.marked, ev.kinds())
	}
}

func TestRefreshAndApplyFailures(t *testing.T) {
	ev := newEnv(t, "updates: {policy: auto}\n")
	ev.refreshErr = errors.New("steamcmd timed out")
	rep := ev.check(true)
	if rep.Refreshed || ev.kinds() != "job_failed" || ev.outcome(rep).Action != "none" {
		t.Errorf("a failed refresh is reported and the check goes on: %+v events %s", rep, ev.kinds())
	}

	ev = newEnv(t, "updates: {policy: auto}\n")
	ev.mod("g2")
	ev.applyErr = errors.New("the snapshot failed")
	o := ev.outcome(ev.check(true))
	if o.Action != "failed" || o.Err == nil || ev.kinds() != "restart_started,job_failed" {
		t.Fatalf("%+v events %s", o, ev.kinds())
	}
	st := mustState(t, ev)
	if len(st.Instances["x"].Pending) != 1 || st.Instances["x"].LastError != "the snapshot failed" || !st.Instances["x"].LastRestartAt.IsZero() {
		t.Errorf("the update stays pending: %+v", st.Instances["x"])
	}
	ev.applyErr = nil
	ev.advance(time.Hour)
	if o := ev.outcome(ev.check(true)); o.Action != "applied" {
		t.Errorf("the next check tries again: %+v", o)
	}
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
	if bad.Action != "failed" || bad.Err == nil || good.Action != "none" {
		t.Errorf("bad %+v good %+v", bad, good)
	}
}

func TestStateFile(t *testing.T) {
	ev := newEnv(t, "")
	if st, err := LoadState(filepath.Join(ev.data, "nothing")); err != nil || st.Instances == nil {
		t.Errorf("a missing state is an empty one: %+v %v", st, err)
	}
	write(t, StatePath(ev.cfg), "{broken")
	if _, err := ev.e.Check(context.Background(), true); err == nil {
		t.Error("a corrupt state file must not be ignored silently")
	}
}

func TestGCKeepsWhatIsInUse(t *testing.T) {
	ev := newEnv(t, "")
	ev.mod("g2") // current is g2, the unit runs g1
	ev.mod("g3") // current is g3
	for _, gen := range []string{"g2", "g3"} {
		_ = gen
	}
	old := ev.now.Add(-40 * 24 * time.Hour)
	root := product.LocalModStore(ev.cfg.Paths.Cache, "tools").Root
	for _, g := range []string{"g1", "g2"} {
		if err := os.Chtimes(filepath.Join(root, g), old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := ev.e.GC(14 * 24 * time.Hour)
	if err != nil || len(removed) != 1 || removed[0] != "mod @tools/g2" {
		t.Fatalf("removed = %v %v (g1 runs, g3 is current, g2 is old and unused)", removed, err)
	}
	for _, g := range []string{"g1", "g3"} {
		if _, err := os.Stat(filepath.Join(root, g)); err != nil {
			t.Errorf("%s must stay: %v", g, err)
		}
	}
	ev.mod("g4")
	if err := os.Chtimes(filepath.Join(root, "g3"), old, old); err != nil {
		t.Fatal(err)
	}
	_ = ev.deploy() // the unit now runs g4; g1 and g3 are no longer used
	_ = os.Chtimes(filepath.Join(root, "g1"), old, old)
	removed, _ = ev.e.GC(14 * 24 * time.Hour)
	if len(removed) != 2 {
		t.Errorf("removed = %v", removed)
	}
	if removed, _ := ev.e.GC(14 * 24 * time.Hour); len(removed) != 0 {
		t.Errorf("nothing left to remove: %v", removed)
	}
}

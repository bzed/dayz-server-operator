// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSystemctl returns a Lifecycle whose systemctl logs its calls to a file
// and answers is-active from the file's presence.
func fakeSystemctl(t *testing.T, active bool) (Lifecycle, func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if active {
		script += "[ \"$2\" = is-active ] && echo active\n"
	} else {
		script += "[ \"$2\" = is-active ] && { echo inactive; exit 3; }\n"
	}
	script += "exit 0\n"
	p := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	return Lifecycle{Command: p, UserMode: true}, func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

type recCommander struct {
	cmds    []string
	players string
}

func (r *recCommander) Command(_ context.Context, c string) (string, error) {
	r.cmds = append(r.cmds, c)
	if c == "players" {
		out := r.players
		r.players = "" // everybody is gone after the first kick pass
		return out, nil
	}
	return "", nil
}

func TestRestartAnnouncesLocksKicksStopsAndStarts(t *testing.T) {
	lc, calls := fakeSystemctl(t, true)
	cmd := &recCommander{players: "Players on server:\n[#] [IP Address]:[Port] [Ping] [GUID] [Name]\n--------\n0   1.2.3.4:5 10 abc(OK) Bob\n(1 players in total)\n"}
	var slept []time.Duration
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return cmd, nil }, Sleep: func(d time.Duration) { slept = append(slept, d) }}
	var order []string
	err := r.Restart(context.Background(), "x", Announcement{Minutes: 3, Lock: 2, Kick: 1, Text: "MOD UPDATE", Rules: legacyRules()}, func(context.Context) error {
		order = append(order, "between")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"say -1 MOD UPDATE in 2 minutes", "say -1 MOD UPDATE in 1 minute", "#lock", "say -1 The server is locked for the restart: no new logins", "say -1 MOD UPDATE in 30 seconds", "say -1 MOD UPDATE in 15 seconds", "say -1 MOD UPDATE in 10 seconds", "say -1 MOD UPDATE in 5 seconds", "say -1 MOD UPDATE in 4 seconds", "say -1 MOD UPDATE in 3 seconds", "say -1 MOD UPDATE in 2 seconds", "say -1 MOD UPDATE in 1 second", "players", "kick 0 MOD UPDATE", "players", "#kick -1"}
	if strings.Join(cmd.cmds, "|") != strings.Join(want, "|") {
		t.Errorf("RCon commands:\n got %q\nwant %q", cmd.cmds, want)
	}
	var total time.Duration
	for _, d := range slept {
		total += d
	}
	if total != 3*time.Minute {
		t.Errorf("sleeps add up to %v, want the three minutes from the start of the countdown to the restart", total)
	}
	got := calls()
	if len(got) < 3 || !strings.Contains(got[len(got)-2], "stop") || !strings.HasSuffix(got[len(got)-1], "start dzo-x.service") {
		t.Errorf("systemctl calls = %q: stop, then start", got)
	}
	if len(order) != 1 {
		t.Error("between must run once, while the instance is down")
	}
}

func TestRestartWithoutRConStopsAtOnce(t *testing.T) {
	lc, calls := fakeSystemctl(t, true)
	var logged string
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return nil, errors.New("connection refused") }, Sleep: func(time.Duration) { t.Error("nothing to wait for without RCon") }, Log: func(f string, a ...any) { logged = f }}
	if err := r.Restart(context.Background(), "x", Announcement{Minutes: 10}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logged, "RCon unreachable") || !strings.Contains(strings.Join(calls(), "\n"), "stop") {
		t.Errorf("logged %q calls %v", logged, calls())
	}
}

func TestRestartOfAStoppedInstanceOnlyStartsIt(t *testing.T) {
	lc, calls := fakeSystemctl(t, false)
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { t.Error("a stopped server has no RCon"); return nil, nil }}
	ran := false
	if err := r.Restart(context.Background(), "x", Announcement{Minutes: 5}, func(context.Context) error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !ran || strings.Contains(strings.Join(calls(), "\n"), " stop ") {
		t.Errorf("between ran %v, calls %v", ran, calls())
	}
}

func TestAFailingBetweenStartsTheInstanceAgain(t *testing.T) {
	lc, calls := fakeSystemctl(t, true)
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return &recCommander{}, nil }, Sleep: func(time.Duration) {}}
	boom := errors.New("snapshot failed")
	err := r.Restart(context.Background(), "x", Announcement{}, func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	got := calls()
	if !strings.HasSuffix(got[len(got)-1], "start dzo-x.service") {
		t.Errorf("the server must be running again, unchanged: %v", got)
	}
}

func TestCancelledCountdownUnlocksTheServer(t *testing.T) {
	lc, _ := fakeSystemctl(t, true)
	cmd := &recCommander{}
	ctx, cancel := context.WithCancel(context.Background())
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return cmd, nil }, Sleep: func(time.Duration) { cancel() }}
	if err := r.Restart(ctx, "x", Announcement{Minutes: 5, Lock: 5}, nil); err == nil {
		t.Fatal("a cancelled restart must stop")
	}
	if cmd.cmds[len(cmd.cmds)-1] != "#unlock" {
		t.Errorf("a locked server must be unlocked again: %v", cmd.cmds)
	}
}

func TestACancelMarkerEndsTheCountdown(t *testing.T) {
	lc, calls := fakeSystemctl(t, true)
	cmd := &recCommander{}
	cancelled := false
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return cmd, nil },
		Sleep: func(time.Duration) { cancelled = true }, Cancelled: func() bool { return cancelled }}
	err := r.Restart(context.Background(), "x", Announcement{Minutes: 10, Lock: 5}, nil)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	if cmd.cmds[len(cmd.cmds)-1] != "#unlock" || strings.Contains(strings.Join(calls(), "\n"), " stop ") {
		t.Errorf("a cancelled restart unlocks and does not stop: %v %v", cmd.cmds, calls())
	}
}

func TestTimelineLocksSevenAndKicksThreeMinutesBeforeTheRestart(t *testing.T) {
	var got []string
	for _, st := range (Announcement{Minutes: 30, Lock: 7, Kick: 3, Rules: legacyRules()}).timeline() {
		what := "say"
		switch {
		case st.kick:
			what = "kick"
		case st.lock:
			what = "lock"
		}
		got = append(got, what+"@"+st.left.String())
	}
	// The announcements count down to the kick: 27 minutes left at the start, 3 minutes before the restart.
	want := "say@30m0s say@23m0s say@18m0s say@13m0s say@12m0s say@11m0s say@10m0s say@9m0s say@8m0s say@7m0s lock@7m0s say@6m0s say@5m0s say@4m0s " +
		"say@3m30s say@3m15s say@3m10s say@3m5s say@3m4s say@3m3s say@3m2s say@3m1s kick@3m0s"
	if strings.Join(got, " ") != want {
		t.Errorf("timeline:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestStopCountsDownThenStopsAndStaysStopped(t *testing.T) {
	lc, calls := fakeSystemctl(t, true)
	cmd := &recCommander{}
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return cmd, nil }, Sleep: func(time.Duration) {}}
	if err := r.Stop(context.Background(), "x", Announcement{Minutes: 2, Lock: 1, Kick: 1}); err != nil {
		t.Fatal(err)
	}
	said := strings.Join(cmd.cmds, "|")
	for _, want := range []string{"say -1 Server shutdown in 1 minute", "#lock", "kick", "#kick -1"} {
		if !strings.Contains(said, want) {
			t.Errorf("RCon commands lack %q: %q", want, cmd.cmds)
		}
	}
	if strings.Contains(said, "restart") {
		t.Errorf("a stop must not talk about a restart: %q", cmd.cmds)
	}
	got := calls()
	if !strings.Contains(got[len(got)-1], "stop dzo-x.service") || strings.Contains(strings.Join(got, "\n"), "start dzo-x.service") {
		t.Errorf("systemctl calls = %q: stop, and no start", got)
	}
}

func TestStopOfAStoppedInstanceDoesNotAnnounce(t *testing.T) {
	lc, calls := fakeSystemctl(t, false)
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { t.Error("a stopped server has no RCon"); return nil, nil }}
	if err := r.Stop(context.Background(), "x", Announcement{Minutes: 5}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls(), "\n"), "stop dzo-x.service") {
		t.Errorf("calls %v", calls())
	}
}

func TestACancelledStopSaysSoAndKeepsTheServerRunning(t *testing.T) {
	lc, calls := fakeSystemctl(t, true)
	cmd := &recCommander{}
	r := Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return cmd, nil }, Sleep: func(time.Duration) {}, Cancelled: func() bool { return true }}
	err := r.Stop(context.Background(), "x", Announcement{Minutes: 5, Kick: 1})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(strings.Join(cmd.cmds, "|"), "The shutdown was cancelled") || strings.Contains(strings.Join(calls(), "\n"), "stop dzo-x.service") {
		t.Errorf("RCon %q, systemctl %v", cmd.cmds, calls())
	}
}

// flakyRCon hands out connections that share one log; the failAt-th command overall fails once, as a
// connection that times out does, and takes its connection down with it.
type flakyRCon struct {
	cmds   []string
	failAt int
	dials  int
}

type flakyConn struct{ f *flakyRCon }

func (c flakyConn) Command(_ context.Context, cmd string) (string, error) {
	c.f.cmds = append(c.f.cmds, cmd)
	if len(c.f.cmds) == c.f.failAt {
		return "", context.DeadlineExceeded
	}
	return "", nil
}

func (f *flakyRCon) dial(context.Context) (Commander, error) {
	f.dials++
	return flakyConn{f}, nil
}

func TestAFailedRConCommandIsSkippedAndTheNextOneReconnects(t *testing.T) {
	// The commands of a 3 minute countdown with the lock at 2 and the kick at 1 minute before it:
	// 1 say 2 min, 2 say 1 min, 3 #lock, 4 say locked, 5-12 the seconds, 13 players, 14 #kick -1.
	for _, tc := range []struct {
		name    string
		failAt  int
		retried string // the command that is sent twice in a row, if any
		skipped bool   // the first announcement is sent once, the failed attempt, and not again
	}{
		{"an announcement", 1, "", true},
		{"the lock", 3, "#lock", false},
		{"the kick", 13, "players", false},
	} {
		lc, calls := fakeSystemctl(t, true)
		f := &flakyRCon{failAt: tc.failAt}
		var slept time.Duration
		var logged []string
		r := Restarter{Lifecycle: lc, Dial: f.dial, Sleep: func(d time.Duration) { slept += d },
			Log: func(format string, a ...any) { logged = append(logged, fmt.Sprintf(format, a...)) }}
		if err := r.Restart(context.Background(), "x", Announcement{Minutes: 3, Lock: 2, Kick: 1, Text: "UP", Rules: legacyRules()}, nil); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		all := strings.Join(f.cmds, "|")
		if f.dials != 2 {
			t.Errorf("%s: dialled %d times, want the first connection and one more", tc.name, f.dials)
		}
		if tc.retried != "" && !strings.Contains(all, tc.retried+"|"+tc.retried) {
			t.Errorf("%s: %q was not tried again: %q", tc.name, tc.retried, f.cmds)
		}
		if tc.skipped && strings.Count(all, "say -1 UP in 2 minutes") != 1 {
			t.Errorf("%s: the failed announcement was sent again: %q", tc.name, f.cmds)
		}
		if !strings.Contains(all, "#lock") || !strings.HasSuffix(all, "#kick -1") || !strings.Contains(all, "say -1 UP in 1 second") {
			t.Errorf("%s: the countdown did not go on after the failure: %q", tc.name, f.cmds)
		}
		if slept != 3*time.Minute {
			t.Errorf("%s: slept %v, want the whole countdown", tc.name, slept)
		}
		got := calls()
		if len(got) < 3 || !strings.Contains(got[len(got)-2], "stop") || !strings.HasSuffix(got[len(got)-1], "start dzo-x.service") {
			t.Errorf("%s: systemctl calls = %q: stop, then start", tc.name, got)
		}
		if len(logged) == 0 || !strings.Contains(strings.Join(logged, "\n"), "context deadline exceeded") {
			t.Errorf("%s: logged %q", tc.name, logged)
		}
	}
}

func TestACancelledContextStillEndsTheCountdownAfterAFailure(t *testing.T) {
	lc, _ := fakeSystemctl(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	f := &flakyRCon{failAt: 1}
	r := Restarter{Lifecycle: lc, Dial: f.dial, Sleep: func(time.Duration) { cancel() }}
	if err := r.Restart(ctx, "x", Announcement{Minutes: 3, Lock: 2, Kick: 1}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sc = time.Second
	mn = time.Minute
)

// legacyRules is the countdown before announce.ways existed: RCon say at the old marks.
func legacyRules() []AnnounceRule {
	var at []time.Duration
	for _, m := range []int{30, 20, 15, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1} {
		at = append(at, time.Duration(m)*mn)
	}
	for _, s := range []int{30, 15, 10, 5, 4, 3, 2, 1} {
		at = append(at, time.Duration(s)*sc)
	}
	return []AnnounceRule{{Way: "rcon", At: at}}
}

// plan renders a timeline as "<left until the restart> <way> <text>[ lock|kick]".
func plan(a Announcement) []string {
	var out []string
	for _, st := range a.timeline() {
		s := st.left.String()
		switch {
		case st.kick:
			s += " kick"
		case st.lock && st.say == "":
			s += " lock"
		default:
			way := st.way
			if st.typ != "" {
				way += ":" + st.typ
			}
			s += " " + way + " " + st.say
			if st.lock {
				s += " +lock"
			}
		}
		out = append(out, s)
	}
	return out
}

func TestDefaultTimelineNotifiesTheLastTenMinutes(t *testing.T) {
	got := plan(Announcement{Minutes: 30, Lock: 7, Kick: 3})
	want := []string{
		"30m0s rcon Server restart in 27 minutes",
		"23m0s rcon Server restart in 20 minutes",
		"18m0s rcon Server restart in 15 minutes",
		"13m0s notification Server restart in 10 minutes",
		"12m0s notification Server restart in 9 minutes",
		"11m0s notification Server restart in 8 minutes",
		"10m0s notification Server restart in 7 minutes",
		"9m0s notification Server restart in 6 minutes",
		"8m0s notification Server restart in 5 minutes",
		"7m0s notification Server restart in 4 minutes. The server is locked for the restart: no new logins +lock",
		"6m0s notification Server restart in 3 minutes",
		"5m0s notification Server restart in 2 minutes",
		"4m0s notification Server restart in 1 minute",
		"3m10s notification Server restart NOW",
		"3m0s kick",
	}
	assert.Equal(t, strings.Join(want, "\n"), strings.Join(got, "\n"))
}

func TestTimelineEveryAndWithin(t *testing.T) {
	a := Announcement{Minutes: 25, Rules: []AnnounceRule{
		{Way: "chat", Every: 5 * mn},
		{Within: 10 * mn, Way: "expansion", Type: "baguette", Every: 2 * mn},
	}}
	want := "25m0s chat Server restart in 25 minutes|20m0s chat Server restart in 20 minutes|15m0s chat Server restart in 15 minutes|" +
		"10m0s expansion:baguette Server restart in 10 minutes|8m0s expansion:baguette Server restart in 8 minutes|6m0s expansion:baguette Server restart in 6 minutes|" +
		"4m0s expansion:baguette Server restart in 4 minutes|2m0s expansion:baguette Server restart in 2 minutes|0s kick"
	got := strings.Join(plan(a), "|")
	assert.Equal(t, want, got)
}

func TestTimelineAtMarks(t *testing.T) {
	a := Announcement{Minutes: 30, Rules: []AnnounceRule{{Way: "rcon", At: []time.Duration{20 * mn, 5 * mn, 40 * mn}}}}
	want := "30m0s rcon Server restart in 30 minutes|20m0s rcon Server restart in 20 minutes|5m0s rcon Server restart in 5 minutes|0s kick"
	got := strings.Join(plan(a), "|")
	assert.Equal(t, want, got)
}

func TestNowIsSkippedWhenItDoesNotFitTheCountdown(t *testing.T) {
	a := Announcement{Minutes: 3, Rules: []AnnounceRule{{Way: "notification", Every: mn, Now: 5 * mn}}}
	for _, s := range plan(a) {
		assert.NotContains(t, s, "NOW")
	}
}

func TestAShortCountdownStartsWithTheCoveringRule(t *testing.T) {
	got := strings.Join(plan(Announcement{Minutes: 3}), "|")
	want := "3m0s notification Server restart in 3 minutes|2m0s notification Server restart in 2 minutes|1m0s notification Server restart in 1 minute|" +
		"10s notification Server restart NOW|0s kick"
	assert.Equal(t, want, got)
}

func TestNoCountdownAnnouncesNothing(t *testing.T) {
	got := strings.Join(plan(Announcement{}), "|")
	assert.Equal(t, "0s kick", got)
}

func TestNotificationStepsAreAtLeastAMinuteApart(t *testing.T) {
	for minutes := 1; minutes <= 40; minutes++ {
		for _, lock := range []int{0, minutes / 2, minutes} {
			a := Announcement{Minutes: minutes, Lock: lock, Kick: minutes / 4}
			last := time.Duration(-1)
			for _, st := range a.timeline() {
				if st.way != "notification" || st.now {
					continue
				}
				if last >= 0 {
					require.GreaterOrEqual(t, last-st.left, mn, "%+v: notifications at %v and %v are less than a minute apart", a, last, st.left)
				}
				last = st.left
			}
		}
	}
}

type notifyCall struct {
	way, typ, text string
	seconds        int
}

func countdownRestarter(t testing.TB, cmd *recCommander, notify func(context.Context, string, string, string, int) error) Restarter {
	t.Helper()
	lc, _ := fakeSystemctl(t, true)
	return Restarter{Lifecycle: lc, Dial: func(context.Context) (Commander, error) { return cmd, nil }, Sleep: func(time.Duration) {}, Notify: notify}
}

func TestNotificationStepsGoThroughNotify(t *testing.T) {
	cmd := &recCommander{}
	var calls []notifyCall
	r := countdownRestarter(t, cmd, func(_ context.Context, way, typ, text string, seconds int) error {
		calls = append(calls, notifyCall{way, typ, text, seconds})
		return nil
	})
	err := r.Restart(t.Context(), "x", Announcement{Minutes: 2}, nil)
	require.NoError(t, err)
	want := []notifyCall{
		{"notification", "", "Server restart in 2 minutes", 10},
		{"notification", "", "Server restart in 1 minute", 10},
		{"notification", "", "Server restart NOW", 8},
	}
	assert.Equal(t, fmt.Sprint(want), fmt.Sprint(calls))
	for _, c := range cmd.cmds {
		assert.False(t, strings.HasPrefix(c, "say"), "an announcement went over RCon as well: %v", cmd.cmds)
	}
}

func TestNotifyFailureFallsBackToSay(t *testing.T) {
	cmd := &recCommander{}
	r := countdownRestarter(t, cmd, func(context.Context, string, string, string, int) error { return errors.New("API is down") })
	var logged []string
	r.Log = func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	err := r.Restart(t.Context(), "x", Announcement{Minutes: 2}, nil)
	require.NoError(t, err)
	said := strings.Join(cmd.cmds, "|")
	require.Contains(t, said, "say -1 Server restart in 2 minutes", "RCon commands %q", cmd.cmds)
	assert.Contains(t, said, "say -1 Server restart NOW", "RCon commands %q", cmd.cmds)
	require.NotEmpty(t, logged)
	assert.Contains(t, strings.Join(logged, "\n"), "API is down")
}

func TestNilNotifyUsesSay(t *testing.T) {
	cmd := &recCommander{}
	r := countdownRestarter(t, cmd, nil)
	err := r.Restart(t.Context(), "x", Announcement{Minutes: 2}, nil)
	require.NoError(t, err)
	assert.Contains(t, strings.Join(cmd.cmds, "|"), "say -1 Server restart in 2 minutes")
}

func TestACancelNoticeUsesTheWayOfTheCountdown(t *testing.T) {
	cmd := &recCommander{}
	var calls []notifyCall
	r := countdownRestarter(t, cmd, func(_ context.Context, way, typ, text string, seconds int) error {
		calls = append(calls, notifyCall{way, typ, text, seconds})
		return nil
	})
	r.Cancelled = func() bool { return true }
	err := r.Restart(t.Context(), "x", Announcement{Minutes: 5, Kick: 1}, nil)
	require.ErrorIs(t, err, ErrCancelled, "err")
	last := calls[len(calls)-1]
	require.Equal(t, "notification", last.way, "calls = %v", calls)
	assert.Equal(t, "The restart was cancelled", last.text, "calls = %v", calls)
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func TestSlowNotificationsDoNotDelayTheRestart(t *testing.T) {
	cmd := &recCommander{}
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	start := clk.t
	r := countdownRestarter(t, cmd, func(ctx context.Context, way, typ, text string, seconds int) error {
		dl, ok := ctx.Deadline()
		require.True(t, ok, "Notify has no short deadline: %v %v", dl, ok)
		assert.LessOrEqual(t, time.Until(dl), 5*time.Second, "Notify has no short deadline: %v %v", dl, ok)
		clk.add(5 * time.Second) // the API and the mod take their time
		return nil
	})
	r.Now = clk.now
	r.Sleep = clk.add
	err := r.Restart(t.Context(), "x", Announcement{Minutes: 3}, nil)
	require.NoError(t, err)
	got := clk.t.Sub(start)
	assert.Equal(t, 3*time.Minute, got)
}

func TestDurationTextKeepsTheSecondsOfAFractionalMinute(t *testing.T) {
	for d, want := range map[time.Duration]string{
		90 * sc:       "1 minute 30 seconds",
		150 * sc:      "2 minutes 30 seconds",
		mn:            "1 minute",
		2 * mn:        "2 minutes",
		30 * sc:       "30 seconds",
		sc:            "1 second",
		3*mn + 1*sc:   "3 minutes 1 second",
		10*mn + 59*sc: "10 minutes 59 seconds",
	} {
		got := durationText(d)
		assert.Equal(t, want, got)
	}
}

func TestEveryKeepsTheLastMarkBelowTheInterval(t *testing.T) {
	a := Announcement{Minutes: 10, Rules: []AnnounceRule{{Within: 10 * mn, Way: "chat", Every: 3 * mn}}}
	want := "10m0s chat Server restart in 10 minutes|7m0s chat Server restart in 7 minutes|4m0s chat Server restart in 4 minutes|1m0s chat Server restart in 1 minute|0s kick"
	got := strings.Join(plan(a), "|")
	assert.Equal(t, want, got)
}

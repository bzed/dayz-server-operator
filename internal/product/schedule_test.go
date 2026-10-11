// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/site"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestEvaluateScheduleNothingPending(t *testing.T) {
	d, err := EvaluateSchedule(site.UpdatesConfig{}, ScheduleInput{Now: mustParse(t, "2026-01-01T12:00:00")})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if d.Apply {
		t.Errorf("Apply = true, want false: %s", d.Reason)
	}
}

func TestEvaluateScheduleNoConstraintsAppliesImmediately(t *testing.T) {
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(site.UpdatesConfig{}, ScheduleInput{PendingSince: now, Now: now})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if !d.Apply {
		t.Errorf("Apply = false, want true: %s", d.Reason)
	}
}

func TestEvaluateScheduleQuietHoursBlocks(t *testing.T) {
	cfg := site.UpdatesConfig{QuietHours: []string{"18:00-24:00"}}
	now := mustParse(t, "2026-01-01T20:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{PendingSince: now, Now: now})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if d.Apply {
		t.Errorf("Apply = true, want false (quiet hours): %s", d.Reason)
	}
}

func TestEvaluateScheduleOutsideWindowBlocks(t *testing.T) {
	cfg := site.UpdatesConfig{Window: []string{"06:00-10:00", "14:00-16:00"}}
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{PendingSince: now, Now: now})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if d.Apply {
		t.Errorf("Apply = true, want false (outside window): %s", d.Reason)
	}
}

func TestEvaluateScheduleInsideWindowApplies(t *testing.T) {
	cfg := site.UpdatesConfig{Window: []string{"06:00-10:00", "14:00-16:00"}}
	now := mustParse(t, "2026-01-01T15:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{PendingSince: now, Now: now})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if !d.Apply {
		t.Errorf("Apply = false, want true (inside window): %s", d.Reason)
	}
}

func TestEvaluateScheduleWindowSpanningMidnight(t *testing.T) {
	cfg := site.UpdatesConfig{Window: []string{"22:00-02:00"}}
	cases := map[string]bool{
		"2026-01-01T23:00:00": true,
		"2026-01-01T01:00:00": true,
		"2026-01-01T12:00:00": false,
	}
	for ts, want := range cases {
		now := mustParse(t, ts)
		d, err := EvaluateSchedule(cfg, ScheduleInput{PendingSince: now, Now: now})
		if err != nil {
			t.Fatalf("EvaluateSchedule(%s): %v", ts, err)
		}
		if d.Apply != want {
			t.Errorf("EvaluateSchedule(%s).Apply = %v, want %v (%s)", ts, d.Apply, want, d.Reason)
		}
	}
}

func TestEvaluateScheduleMinRestartIntervalBlocks(t *testing.T) {
	cfg := site.UpdatesConfig{MinRestartInterval: site.Duration(4 * time.Hour)}
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{
		PendingSince:  now,
		LastRestartAt: now.Add(-1 * time.Hour),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if d.Apply {
		t.Errorf("Apply = true, want false (min_restart_interval): %s", d.Reason)
	}
}

func TestEvaluateScheduleMinRestartIntervalElapsedApplies(t *testing.T) {
	cfg := site.UpdatesConfig{MinRestartInterval: site.Duration(4 * time.Hour)}
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{
		PendingSince:  now,
		LastRestartAt: now.Add(-5 * time.Hour),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if !d.Apply {
		t.Errorf("Apply = false, want true: %s", d.Reason)
	}
}

func TestEvaluateScheduleBatchDelayBlocks(t *testing.T) {
	cfg := site.UpdatesConfig{BatchDelay: site.Duration(20 * time.Minute)}
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{
		PendingSince: now.Add(-5 * time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if d.Apply {
		t.Errorf("Apply = true, want false (batch_delay): %s", d.Reason)
	}
}

func TestEvaluateScheduleBatchDelayElapsedApplies(t *testing.T) {
	cfg := site.UpdatesConfig{BatchDelay: site.Duration(20 * time.Minute)}
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{
		PendingSince: now.Add(-30 * time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if !d.Apply {
		t.Errorf("Apply = false, want true: %s", d.Reason)
	}
}

func TestEvaluateScheduleDeferredToScheduledRestart(t *testing.T) {
	cfg := site.UpdatesConfig{ApplyWithScheduledRestart: true}
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{
		PendingSince:         now,
		NextScheduledRestart: now.Add(1 * time.Hour),
		Now:                  now,
	})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if d.Apply {
		t.Errorf("Apply = true, want false (deferred to scheduled restart): %s", d.Reason)
	}
}

func TestEvaluateScheduleMaxDelayOverridesEverything(t *testing.T) {
	cfg := site.UpdatesConfig{
		Window:             []string{"06:00-10:00"},
		QuietHours:         []string{"00:00-23:59"},
		MinRestartInterval: site.Duration(4 * time.Hour),
		MaxDelay:           site.Duration(12 * time.Hour),
	}
	now := mustParse(t, "2026-01-01T12:00:00")
	d, err := EvaluateSchedule(cfg, ScheduleInput{
		PendingSince:  now.Add(-13 * time.Hour),
		LastRestartAt: now.Add(-1 * time.Minute),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if !d.Apply {
		t.Errorf("Apply = false, want true (max_delay override): %s", d.Reason)
	}
}

func TestEvaluateScheduleInvalidWindowErrors(t *testing.T) {
	cfg := site.UpdatesConfig{Window: []string{"not-a-range"}}
	now := mustParse(t, "2026-01-01T12:00:00")
	_, err := EvaluateSchedule(cfg, ScheduleInput{PendingSince: now, Now: now})
	if err == nil {
		t.Fatal("expected an error for an invalid window range")
	}
}

func TestEvaluateScheduleInvalidQuietHoursErrors(t *testing.T) {
	cfg := site.UpdatesConfig{QuietHours: []string{"25:00-26:00"}}
	now := mustParse(t, "2026-01-01T12:00:00")
	_, err := EvaluateSchedule(cfg, ScheduleInput{PendingSince: now, Now: now})
	if err == nil {
		t.Fatal("expected an error for an invalid quiet_hours range")
	}
}

func TestParseClockAcceptsTwentyFourZeroZero(t *testing.T) {
	d, err := parseClock("24:00")
	if err != nil {
		t.Fatalf("parseClock: %v", err)
	}
	if d != 24*time.Hour {
		t.Errorf("parseClock(24:00) = %v, want 24h", d)
	}
}

func TestParseClockRejectsInvalid(t *testing.T) {
	cases := []string{"24:30", "abc", "12", "12:60", "-1:00"}
	for _, c := range cases {
		if _, err := parseClock(c); err == nil {
			t.Errorf("parseClock(%q): expected an error", c)
		}
	}
}

func TestEvaluateScheduleDefaultsNowWhenZero(t *testing.T) {
	// A zero ScheduleInput.Now must not be treated as a real timestamp
	// (year 1); EvaluateSchedule should fall back to time.Now().
	d, err := EvaluateSchedule(site.UpdatesConfig{}, ScheduleInput{PendingSince: time.Now()})
	if err != nil {
		t.Fatalf("EvaluateSchedule: %v", err)
	}
	if !d.Apply {
		t.Errorf("Apply = false, want true: %s", d.Reason)
	}
}

func TestEvaluateScheduleUpdateSlot(t *testing.T) {
	zero, half := 0, 30
	ann := site.Announce{Minutes: 30, Lock: 7, Kick: 3}
	for _, c := range []struct {
		minute *int
		now    string
		apply  bool
	}{
		{&zero, "2026-01-01T12:30:00", true},  // announced from :30, restart at 13:00
		{&zero, "2026-01-01T12:35:00", true},  // a late timer still starts the slot
		{&zero, "2026-01-01T12:45:00", false}, // too late for this hour's slot
		{&zero, "2026-01-01T12:00:00", false},
		{&half, "2026-01-01T12:00:00", true}, // announced from :00, restart at 12:30
		{&half, "2026-01-01T12:30:00", false},
		{nil, "2026-01-01T12:17:00", true}, // no slot: immediately
	} {
		now := mustParse(t, c.now)
		d, err := EvaluateSchedule(site.UpdatesConfig{RestartMinute: c.minute, RestartAnnounce: ann}, ScheduleInput{PendingSince: now.Add(-time.Hour), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if d.Apply != c.apply {
			t.Errorf("minute %v at %s: Apply = %v (%s), want %v", c.minute, c.now, d.Apply, d.Reason, c.apply)
		}
	}
}

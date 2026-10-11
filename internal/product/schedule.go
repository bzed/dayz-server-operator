// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/site"
)

// Decision is the update engine's answer to "should the pending mod
// update(s) for this instance be applied right now?", with a
// human-readable reason for logs, job records and notifications either way.
type Decision struct {
	Apply  bool
	Reason string
}

// ScheduleInput is everything EvaluateSchedule needs beyond the instance's
// own UpdatesConfig, all supplied by the caller (internal/instance, once it
// exists) since this package has no notion of instance state itself.
type ScheduleInput struct {
	// PendingSince is when the earliest currently undelivered update was
	// first detected. Zero means nothing is pending.
	PendingSince time.Time
	// LastRestartAt is when this instance was last restarted for an
	// update. Zero means never.
	LastRestartAt time.Time
	// NextScheduledRestart is the next scheduled maintenance restart, if
	// any (RestartsConfig.Schedule resolved by the caller). Zero means
	// none is scheduled.
	NextScheduledRestart time.Time
	Now                  time.Time
}

// EvaluateSchedule implements the window/quiet-hours/batching/
// min-restart-interval decision documented in §C7. It assumes cfg.Policy
// is already known to be site.PolicyAuto: callers dispatch on Policy
// themselves (notify only records+notifies, manual only records), since
// this package has no notion of "record" or "notify" either.
func EvaluateSchedule(cfg site.UpdatesConfig, in ScheduleInput) (Decision, error) {
	if in.PendingSince.IsZero() {
		return Decision{Apply: false, Reason: "nothing pending"}, nil
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	pendingFor := now.Sub(in.PendingSince)

	// max_delay is an unconditional upper bound (§C7: "then apply even
	// outside the window"): once hit, every other gate below is skipped.
	if cfg.MaxDelay.Std() > 0 && pendingFor >= cfg.MaxDelay.Std() {
		return Decision{Apply: true, Reason: "max_delay exceeded, applying regardless of window/quiet-hours"}, nil
	}

	if len(cfg.QuietHours) > 0 {
		inQuiet, err := inAnyRange(now, cfg.QuietHours)
		if err != nil {
			return Decision{}, fmt.Errorf("product: quiet_hours: %w", err)
		}
		if inQuiet {
			return Decision{Apply: false, Reason: "inside quiet_hours"}, nil
		}
	}

	if len(cfg.Window) > 0 {
		inWindow, err := inAnyRange(now, cfg.Window)
		if err != nil {
			return Decision{}, fmt.Errorf("product: window: %w", err)
		}
		if !inWindow {
			return Decision{Apply: false, Reason: "outside update window"}, nil
		}
	}

	if cfg.MinRestartInterval.Std() > 0 && !in.LastRestartAt.IsZero() {
		if since := now.Sub(in.LastRestartAt); since < cfg.MinRestartInterval.Std() {
			return Decision{Apply: false, Reason: fmt.Sprintf(
				"min_restart_interval not elapsed (last restart %s ago)", since.Round(time.Second))}, nil
		}
	}

	if cfg.ApplyWithScheduledRestart && !in.NextScheduledRestart.IsZero() && in.NextScheduledRestart.After(now) {
		return Decision{Apply: false, Reason: fmt.Sprintf(
			"deferred to the scheduled restart at %s", in.NextScheduledRestart.Format(time.RFC3339))}, nil
	}

	if cfg.BatchDelay.Std() > 0 && pendingFor < cfg.BatchDelay.Std() {
		return Decision{Apply: false, Reason: "batch_delay not elapsed yet, waiting for more updates"}, nil
	}

	if m := cfg.RestartMinute; m != nil {
		// The restart is at minute *m; its announcement starts restart_announce.minutes earlier,
		// so that is when the update check has to apply it (the timer fires then).
		start := ((*m-cfg.RestartAnnounce.Minutes)%60 + 60) % 60
		if (now.Minute()-start+60)%60 >= slotTolerance {
			return Decision{Apply: false, Reason: fmt.Sprintf(
				"waiting for the update slot (restart at XX:%02d, announced from XX:%02d)", *m, start)}, nil
		}
	}

	return Decision{Apply: true, Reason: "within window, past batch_delay and min_restart_interval"}, nil
}

// slotTolerance is how many minutes after the start of its slot an update restart is still begun.
const slotTolerance = 10

// inAnyRange reports whether now's local time-of-day falls inside any of
// ranges, each formatted "HH:MM-HH:MM". A range whose end is not after its
// start is treated as spanning midnight (e.g. "22:00-02:00").
func inAnyRange(now time.Time, ranges []string) (bool, error) {
	nowOfDay := timeOfDay(now)
	for _, r := range ranges {
		start, end, err := parseRange(r)
		if err != nil {
			return false, err
		}
		if start <= end {
			if nowOfDay >= start && nowOfDay < end {
				return true, nil
			}
		} else {
			// Spans midnight: "in range" means at or after start, or before end.
			if nowOfDay >= start || nowOfDay < end {
				return true, nil
			}
		}
	}
	return false, nil
}

func timeOfDay(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second
}

func parseRange(r string) (start, end time.Duration, err error) {
	parts := strings.SplitN(r, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range %q, want \"HH:MM-HH:MM\"", r)
	}
	start, err = parseClock(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range %q: %w", r, err)
	}
	end, err = parseClock(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range %q: %w", r, err)
	}
	return start, end, nil
}

// parseClock parses "HH:MM", accepting "24:00" as end-of-day (a common way
// to write a window that runs to midnight).
func parseClock(s string) (time.Duration, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid time %q, want \"HH:MM\"", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 24 {
		return 0, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid minute in %q", s)
	}
	if h == 24 && m != 0 {
		return 0, fmt.Errorf("invalid time %q: 24:00 is the only valid hour-24 value", s)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}

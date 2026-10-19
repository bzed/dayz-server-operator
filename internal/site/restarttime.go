// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var weekdayNames = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// RestartTime is one entry of restarts.schedule or backup.schedule: the time of day, on every
// day or on the listed weekdays. In the site config it is written "04:30", "Mon,Thu 04:30",
// "*:30" (every hour at :30) or "hourly" (the same as "*:00").
type RestartTime struct {
	Days   []int // 0 = Monday ... 6 = Sunday; empty means every day
	Hour   int   // -1 means every hour
	Minute int
}

// ParseRestartTime parses "HH:MM" or "<weekday>[,<weekday>...] HH:MM" with the weekdays
// Mon, Tue, Wed, Thu, Fri, Sat, Sun. "*:MM" stands for every hour, and "hourly" for "*:00".
func ParseRestartTime(s string) (RestartTime, error) {
	return parseTime("restarts.schedule", s)
}

// ParseBackupTime parses an entry of backup.schedule, written like one of restarts.schedule.
func ParseBackupTime(s string) (RestartTime, error) {
	return parseTime("backup.schedule", s)
}

func parseTime(key, s string) (RestartTime, error) {
	var r RestartTime
	if s == "hourly" {
		s = "*:00"
	}
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 2 {
		return r, fmt.Errorf("%s: %q: want HH:MM or Mon,Thu HH:MM", key, s)
	}
	if len(fields) == 2 {
		for _, d := range strings.Split(fields[0], ",") {
			i := indexOf(weekdayNames, d)
			if i < 0 {
				return r, fmt.Errorf("%s: %q: %q is not one of %s", key, s, d, strings.Join(weekdayNames, ", "))
			}
			if indexOfInt(r.Days, i) < 0 {
				r.Days = append(r.Days, i)
			}
		}
	}
	hm := strings.Split(fields[len(fields)-1], ":")
	if len(hm) != 2 || (len(hm[0]) != 2 && hm[0] != "*") || len(hm[1]) != 2 {
		return r, fmt.Errorf("%s: %q: the time must be HH:MM or *:MM", key, s)
	}
	h, errH := strconv.Atoi(hm[0])
	if hm[0] == "*" {
		h, errH = -1, nil
	}
	m, errM := strconv.Atoi(hm[1])
	if errH != nil || errM != nil || h < -1 || h > 23 || m < 0 || m > 59 {
		return r, fmt.Errorf("%s: %q: the time must be 00:00 to 23:59", key, s)
	}
	r.Hour, r.Minute = h, m
	return r, nil
}

func indexOf(l []string, s string) int {
	for i, x := range l {
		if x == s {
			return i
		}
	}
	return -1
}

func indexOfInt(l []int, v int) int {
	for i, x := range l {
		if x == v {
			return i
		}
	}
	return -1
}

// OnCalendar returns the systemd calendar expression for the moment `before` ahead of the
// restart time, rounded to whole minutes: the time the countdown has to start. The weekdays
// move back with it when it falls on the day before.
func (r RestartTime) OnCalendar(before time.Duration) string {
	if r.Hour < 0 {
		// every hour: only the minute moves, and it stays an hourly timer when it wraps
		m := ((r.Minute-int(before/time.Minute))%60 + 60) % 60
		at := fmt.Sprintf("*-*-* *:%02d:00", m)
		if len(r.Days) == 0 {
			return at
		}
		var days []string
		for _, d := range r.Days {
			days = append(days, weekdayNames[d])
		}
		return strings.Join(days, ",") + " " + at
	}
	mins := r.Hour*60 + r.Minute - int(before/time.Minute)
	dayShift := 0
	for mins < 0 {
		mins += 24 * 60
		dayShift--
	}
	for mins >= 24*60 {
		mins -= 24 * 60
		dayShift++
	}
	at := fmt.Sprintf("*-*-* %02d:%02d:00", mins/60, mins%60)
	if len(r.Days) == 0 {
		return at
	}
	var days []string
	for _, d := range r.Days {
		days = append(days, weekdayNames[((d+dayShift)%7+7)%7])
	}
	return strings.Join(days, ",") + " " + at
}

// Validate checks every schedule entry.
func (c BackupConfig) Validate() error {
	for _, s := range c.Schedule {
		if _, err := parseTime("backup.schedule", s); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks every schedule entry.
func (c RestartsConfig) Validate() error {
	for _, s := range c.Schedule {
		if _, err := ParseRestartTime(s); err != nil {
			return err
		}
	}
	a := c.Announce
	if a.Minutes < 0 || a.Lock < 0 || a.Kick < 0 || (a.Minutes > 0 && (a.Kick > a.Lock || a.Lock > a.Minutes)) {
		return fmt.Errorf("restarts.announce: want minutes >= lock >= kick >= 0 (minutes before the restart), got %d, %d, %d", a.Minutes, a.Lock, a.Kick)
	}
	return a.validateWays("restarts.announce.ways")
}

// Next returns the first time after `after` that matches the entry, in the location of `after`
// (the systemd timers that run the restarts use the local time of the host).
func (r RestartTime) Next(after time.Time) time.Time {
	day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, after.Location())
	for i := 0; i <= 8; i++ {
		d := day.AddDate(0, 0, i)
		if len(r.Days) > 0 && indexOfInt(r.Days, (int(d.Weekday())+6)%7) < 0 {
			continue
		}
		for h := 0; h < 24; h++ {
			if r.Hour >= 0 && r.Hour != h {
				continue
			}
			if t := time.Date(d.Year(), d.Month(), d.Day(), h, r.Minute, 0, 0, d.Location()); t.After(after) {
				return t
			}
		}
	}
	return time.Time{}
}

// NextRestart returns the earliest next time of the restarts.schedule entries (invalid entries are
// skipped), or the zero time when there are none.
func NextRestart(entries []string, after time.Time) time.Time {
	var next time.Time
	for _, e := range entries {
		rt, err := ParseRestartTime(e)
		if err != nil {
			continue
		}
		if t := rt.Next(after); !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	return next
}

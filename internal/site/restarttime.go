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

// RestartTime is one entry of restarts.schedule: the time of day the server restarts, on every
// day or on the listed weekdays. In the site config it is written "04:30" or "Mon,Thu 04:30".
type RestartTime struct {
	Days   []int // 0 = Monday ... 6 = Sunday; empty means every day
	Hour   int
	Minute int
}

// ParseRestartTime parses "HH:MM" or "<weekday>[,<weekday>...] HH:MM" with the weekdays
// Mon, Tue, Wed, Thu, Fri, Sat, Sun.
func ParseRestartTime(s string) (RestartTime, error) {
	var r RestartTime
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 2 {
		return r, fmt.Errorf("restarts.schedule: %q: want HH:MM or Mon,Thu HH:MM", s)
	}
	if len(fields) == 2 {
		for _, d := range strings.Split(fields[0], ",") {
			i := indexOf(weekdayNames, d)
			if i < 0 {
				return r, fmt.Errorf("restarts.schedule: %q: %q is not one of %s", s, d, strings.Join(weekdayNames, ", "))
			}
			if indexOfInt(r.Days, i) < 0 {
				r.Days = append(r.Days, i)
			}
		}
	}
	hm := strings.Split(fields[len(fields)-1], ":")
	if len(hm) != 2 || len(hm[0]) != 2 || len(hm[1]) != 2 {
		return r, fmt.Errorf("restarts.schedule: %q: the time must be HH:MM", s)
	}
	h, errH := strconv.Atoi(hm[0])
	m, errM := strconv.Atoi(hm[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return r, fmt.Errorf("restarts.schedule: %q: the time must be 00:00 to 23:59", s)
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
	return nil
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"testing"
	"time"
)

func TestRestartTimeOnCalendar(t *testing.T) {
	for _, c := range []struct {
		entry  string
		before time.Duration
		want   string
	}{
		{"04:00", 0, "*-*-* 04:00:00"},
		{"04:00", 30 * time.Minute, "*-*-* 03:30:00"},
		{"00:10", 30 * time.Minute, "*-*-* 23:40:00"},
		{"Mon,Thu 00:10", 30 * time.Minute, "Sun,Wed *-*-* 23:40:00"},
		{"Mon 12:00", 30 * time.Minute, "Mon *-*-* 11:30:00"},
		{"Sun 00:00", 30 * time.Minute, "Sat *-*-* 23:30:00"},
	} {
		rt, err := ParseRestartTime(c.entry)
		if err != nil {
			t.Fatalf("%q: %v", c.entry, err)
		}
		if got := rt.OnCalendar(c.before); got != c.want {
			t.Errorf("%q - %v = %q, want %q", c.entry, c.before, got, c.want)
		}
	}
}

func TestParseRestartTimeRejectsSystemdSyntax(t *testing.T) {
	for _, s := range []string{"", "*-*-* 00/4:00", "4:00", "24:00", "04:60", "Fri..Sun 04:00", "Mon 04:00 extra", "Foo 04:00", "04:00:00"} {
		if _, err := ParseRestartTime(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestRestartsConfigValidatesTheAnnouncement(t *testing.T) {
	if err := (RestartsConfig{Announce: Announce{Minutes: 30, Lock: 7, Kick: 3}}).Validate(); err != nil {
		t.Error(err)
	}
	if err := (RestartsConfig{Announce: Announce{Minutes: 30, Lock: 2, Kick: 3}}).Validate(); err == nil {
		t.Error("the kick cannot come before the lock")
	}
	if err := (RestartsConfig{Announce: Announce{Minutes: 5, Lock: 7, Kick: 3}}).Validate(); err == nil {
		t.Error("the lock cannot come before the countdown")
	}
}

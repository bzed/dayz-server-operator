// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
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
		{"hourly", 0, "*-*-* *:00:00"},
		{"*:30", 0, "*-*-* *:30:00"},
		{"*:00", 30 * time.Minute, "*-*-* *:30:00"},
		{"Sat,Sun *:15", 0, "Sat,Sun *-*-* *:15:00"},
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
	for _, s := range []string{"", "*-*-* 00/4:00", "4:00", "24:00", "04:60", "Fri..Sun 04:00", "Mon 04:00 extra", "Foo 04:00", "04:00:00", "*:5", "*:60", "**:00", "Mon hourly"} {
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

func TestBackupConfigValidatesTheSchedule(t *testing.T) {
	if err := (BackupConfig{Schedule: []string{"00:00", "Sat 22:30"}}).Validate(); err != nil {
		t.Errorf("valid schedule: %v", err)
	}
	err := (BackupConfig{Schedule: []string{"*-*-* 00/6:00"}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "backup.schedule") {
		t.Errorf("a systemd expression must be rejected, naming backup.schedule: %v", err)
	}
}

func TestValidateRConBind(t *testing.T) {
	for _, s := range []string{"", "127.0.0.1", "0.0.0.0", "::", "::1", "192.0.2.7"} {
		if err := ValidateRConBind(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"localhost", "all", "192.0.2", "127.0.0.1:2303"} {
		if err := ValidateRConBind(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestMergeWatchByLayerInstanceWins(t *testing.T) {
	def := []admin.WatchRule{
		{Layer: "fires", Classes: []string{"FireplaceBase"}, Icon: "fire", Color: "red"},
		{Layer: "ufo", Classes: []string{"UFO*"}, Icon: "rocket"},
	}
	own := []admin.WatchRule{
		{Layer: "fires", Classes: []string{"Campfire"}, Icon: "tree"}, // replaces the default rule as a whole
		{Layer: "convoy", Classes: []string{"eAIBase"}},
	}
	got := MergeWatch(def, own)
	var layers []string
	for _, w := range got {
		layers = append(layers, w.Layer)
	}
	if strings.Join(layers, ",") != "fires,ufo,convoy" {
		t.Fatalf("layers = %v: defaults in their order, the instance's new layers after", layers)
	}
	if got[0].Icon != "tree" || got[0].Color != "" || got[0].Classes[0] != "Campfire" {
		t.Errorf("fires = %+v: the instance rule wins as a whole", got[0])
	}
	if got[1].Icon != "rocket" {
		t.Errorf("ufo = %+v: a default without an instance rule stays", got[1])
	}
	if out := MergeWatch(nil, own); len(out) != 2 {
		t.Errorf("no defaults = %v", out)
	}
	if out := MergeWatch(def, nil); len(out) != 2 {
		t.Errorf("no own rules = %v", out)
	}
}

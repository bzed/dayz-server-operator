// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		require.NoError(t, err, "%q", c.entry)
		got := rt.OnCalendar(c.before)
		assert.Equal(t, c.want, got)
	}
}

func TestParseRestartTimeRejectsSystemdSyntax(t *testing.T) {
	for _, s := range []string{"", "*-*-* 00/4:00", "4:00", "24:00", "04:60", "Fri..Sun 04:00", "Mon 04:00 extra", "Foo 04:00", "04:00:00", "*:5", "*:60", "**:00", "Mon hourly"} {
		_, err := ParseRestartTime(s)
		require.Error(t, err)
	}
}

func TestRestartsConfigValidatesTheAnnouncement(t *testing.T) {
	err := (RestartsConfig{Announce: Announce{Minutes: 30, Lock: 7, Kick: 3}}).Validate()
	require.NoError(t, err)
	err = (RestartsConfig{Announce: Announce{Minutes: 30, Lock: 2, Kick: 3}}).Validate()
	require.Error(t, err, "the kick cannot come before the lock")
	err = (RestartsConfig{Announce: Announce{Minutes: 5, Lock: 7, Kick: 3}}).Validate()
	require.Error(t, err, "the lock cannot come before the countdown")
}

func TestBackupConfigValidatesTheSchedule(t *testing.T) {
	err := (BackupConfig{Schedule: []string{"00:00", "Sat 22:30"}}).Validate()
	require.NoError(t, err, "valid schedule")
	err = (BackupConfig{Schedule: []string{"*-*-* 00/6:00"}}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backup.schedule")
}

func TestValidateRConBind(t *testing.T) {
	for _, s := range []string{"", "127.0.0.1", "0.0.0.0", "::", "::1", "192.0.2.7"} {
		err := ValidateRConBind(s)
		require.NoError(t, err, "%q", s)
	}
	for _, s := range []string{"localhost", "all", "192.0.2", "127.0.0.1:2303"} {
		err := ValidateRConBind(s)
		require.Error(t, err)
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
	require.Equal(t, "fires,ufo,convoy", strings.Join(layers, ","))
	require.Equal(t, "tree", got[0].Icon)
	require.Empty(t, got[0].Color)
	assert.Equal(t, "Campfire", got[0].Classes[0])
	assert.Equal(t, "rocket", got[1].Icon)
	out := MergeWatch(nil, own)
	assert.Len(t, out, 2)
	out = MergeWatch(def, nil)
	assert.Len(t, out, 2)
}

func TestNextRestart(t *testing.T) {
	loc := time.UTC
	at := func(s string) time.Time { v, _ := time.ParseInLocation("2006-01-02 15:04", s, loc); return v }
	for _, c := range []struct {
		entries []string
		after   string
		want    string
	}{
		{[]string{"01:00", "05:00", "09:00"}, "2026-10-16 02:06", "2026-10-16 05:00"},
		{[]string{"01:00", "05:00"}, "2026-10-16 06:00", "2026-10-17 01:00"},
		{[]string{"05:00"}, "2026-10-16 05:00", "2026-10-17 05:00"},
		{[]string{"*:30"}, "2026-10-16 02:45", "2026-10-16 03:30"},
		{[]string{"Mon,Thu 04:30"}, "2026-10-16 02:00", "2026-10-19 04:30"}, // 2026-10-16 is a Friday
		{[]string{"Mon 04:30", "nonsense"}, "2026-10-16 02:00", "2026-10-19 04:30"},
	} {
		got := NextRestart(c.entries, at(c.after))
		assert.True(t, got.Equal(at(c.want)), "%v after %s = %v, want %s", c.entries, c.after, got, c.want)
	}
	assert.True(t, NextRestart(nil, time.Now()).IsZero(), "no schedule, no restart")
}

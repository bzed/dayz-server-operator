// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/yaml.v3"
)

func TestEffectiveWaysDefault(t *testing.T) {
	want := []AnnounceWay{
		{Way: "rcon", At: []Duration{Duration(30 * time.Minute), Duration(20 * time.Minute), Duration(15 * time.Minute)}},
		{Within: Duration(10 * time.Minute), Way: "notification", Every: Duration(time.Minute), Now: Duration(10 * time.Second)},
	}
	got := Announce{Minutes: 30}.EffectiveWays()
	assert.Equal(t, want, got)
	own := []AnnounceWay{{Way: "chat", Every: Duration(time.Minute)}}
	got = (Announce{Ways: own}).EffectiveWays()
	assert.Equal(t, own, got)
}

func TestWaysYAMLRoundTrip(t *testing.T) {
	var a Announce
	src := "minutes: 30\nlock: 7\nkick: 3\nways:\n  - {within: 10m, way: expansion, type: baguette, every: 1m, now: 10s}\n  - {way: rcon, at: [30m, 15m]}\n"
	err := yaml.Unmarshal([]byte(src), &a)
	require.NoError(t, err)
	require.Len(t, a.Ways, 2)
	require.Equal(t, "baguette", a.Ways[0].Type)
	require.Equal(t, 10*time.Second, a.Ways[0].Now.Std())
	require.Len(t, a.Ways[1].At, 2)
	require.Equal(t, 15*time.Minute, a.Ways[1].At[1].Std())
	err = a.ValidateWays()
	require.NoError(t, err, "valid ways rejected")
	err = (Announce{}).ValidateWays()
	require.NoError(t, err, "the defaults must validate")
}

func TestValidateWaysRejects(t *testing.T) {
	m := func(n int) Duration { return Duration(time.Duration(n) * time.Minute) }
	s := func(n int) Duration { return Duration(time.Duration(n) * time.Second) }
	ok := AnnounceWay{Way: "rcon", Every: m(1)}
	for name, tc := range map[string]struct {
		ways []AnnounceWay
		want string
	}{
		"notification every 30s": {[]AnnounceWay{ok, {Within: m(10), Way: "notification", Every: s(30)}}, "announce.ways[1]"},
		"type on chat":           {[]AnnounceWay{{Way: "chat", Type: "toast", Every: m(1)}}, "type"},
		"unknown type":           {[]AnnounceWay{{Way: "expansion", Type: "popup", Every: m(1)}}, "baguette"},
		"unknown way":            {[]AnnounceWay{{Way: "smoke", Every: m(1)}}, "way"},
		"duplicate within":       {[]AnnounceWay{{Within: m(10), Way: "chat", Every: m(1)}, {Within: m(10), Way: "rcon", Every: m(1)}}, "announce.ways[1]"},
		"two without within":     {[]AnnounceWay{{Way: "chat", Every: m(1)}, {Way: "rcon", Every: m(1)}}, "announce.ways[1]"},
		"two now":                {[]AnnounceWay{{Way: "chat", Every: m(1), Now: s(10)}, {Within: m(5), Way: "rcon", Every: m(1), Now: s(5)}}, "now"},
		"now not below within":   {[]AnnounceWay{{Within: m(1), Way: "chat", Every: m(1), Now: m(1)}}, "now"},
		"neither every nor at":   {[]AnnounceWay{{Way: "chat"}}, "every"},
		"both every and at":      {[]AnnounceWay{{Way: "chat", Every: m(1), At: []Duration{m(5)}}}, "every"},
		"at marks too close":     {[]AnnounceWay{{Way: "notification", At: []Duration{m(5), m(5) - s(30)}}}, "minute"},
		"rcon every below 1s":    {[]AnnounceWay{{Way: "rcon", Every: Duration(time.Millisecond)}}, "every"},
		"negative within":        {[]AnnounceWay{{Within: -m(1), Way: "chat", Every: m(1)}}, "within"},
	} {
		err := Announce{Ways: tc.ways}.ValidateWays()
		require.Error(t, err, "%s: err = %v, want it to mention %q", name, err, tc.want)
		assert.Contains(t, err.Error(), tc.want, "%s: err = %v, want it to mention %q", name, err, tc.want)
	}
}

func TestInstanceValidatesTheWaysOfBothAnnouncements(t *testing.T) {
	bad := []AnnounceWay{{Way: "smoke", Every: Duration(time.Minute)}}
	i := validInstance()
	i.Restarts.Announce.Ways = bad
	err := i.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restarts.announce.ways")
	i = validInstance()
	i.Updates.RestartAnnounce.Ways = bad
	err = i.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "updates.restart_announce.ways")
}

func TestValidateWaysRejectsAnAtMarkOutsideItsRule(t *testing.T) {
	m := func(n int) Duration { return Duration(time.Duration(n) * time.Minute) }
	err := Announce{Ways: []AnnounceWay{{Within: m(10), Way: "notification", At: []Duration{m(15)}}}}.ValidateWays()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "within")
}

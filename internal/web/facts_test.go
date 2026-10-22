// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

func TestSpanMatchesTheMap(t *testing.T) {
	for in, want := range map[time.Duration]string{
		0:                                "<1m",
		59 * time.Second:                 "<1m",
		12 * time.Minute:                 "12m",
		3*time.Hour + 12*time.Minute:     "3h 12m",
		50 * time.Hour:                   "2d 2h",
		-5 * time.Minute:                 "<1m",
		24*time.Hour + 30*time.Minute:    "1d 0h",
		2*time.Hour + 59*time.Second + 1: "2h 0m",
	} {
		assert.Equal(t, want, span(in), in.String())
	}
}

func TestFactsOfAConnectedInstance(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	next := now.Add(47 * time.Minute)
	f := factsOf(admin.Status{
		Connected: true, Players: 12, GameTime: "14:05", NextRestart: &next,
		Hello: &admin.Hello{World: "chernarusplus", ModVersion: "0.6.0", Since: now.Add(-3*time.Hour - 12*time.Minute)},
	}, now)
	assert.Equal(t, factsView{Connected: true, World: "chernarusplus", Mod: "0.6.0", Clock: "14:05", Day: true, Players: 12, Up: "up 3h 12m", Restart: "restart in 47m", RestartAt: next}, f)
}

func TestFactsAtNightAndWithoutAClock(t *testing.T) {
	now := time.Now()
	assert.False(t, factsOf(admin.Status{Connected: true, GameTime: "23:10", Hello: &admin.Hello{}}, now).Day)
	assert.Empty(t, factsOf(admin.Status{Connected: true, Hello: &admin.Hello{}}, now).Clock)
}

func TestFactsWithoutTheMod(t *testing.T) {
	now := time.Now()
	next := now.Add(59*time.Minute + 30*time.Second)
	f := factsOf(admin.Status{Connected: false, GameTime: "14:05", NextRestart: &next}, now)
	assert.False(t, f.Connected)
	assert.Empty(t, f.Up)
	assert.Empty(t, f.Clock)
	assert.Equal(t, "restart in 59m", f.Restart, "the schedule is known without the mod")
}

func TestRestartOverdueSaysNow(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	assert.Equal(t, "restart now", factsOf(admin.Status{Connected: true, NextRestart: &past, Hello: &admin.Hello{}}, now).Restart)
}

func TestDashboardShowsTheFacts(t *testing.T) {
	e := newEnv(t, "admin")
	e.hub.NextRestart = func(t time.Time) time.Time { return t.Add(47*time.Minute + 30*time.Second) }
	e.hub.Sync(admin.SyncRequest{Proto: 1, Has: []string{"clock"}, Clock: 14*60 + 5, Players: []admin.Player{{SteamID: "1", Name: "a"}}})
	_, body, _ := e.get("/")
	for _, want := range []string{"enoch", "14:05", "restart in 47m", "0.1.0", "up &lt;1m"} {
		assert.Contains(t, body, want)
	}
}

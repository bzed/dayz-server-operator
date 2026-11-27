// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestE2EPagesLoadWithoutErrors(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	for _, p := range []string{"/", "/b/main/i/alpha", "/b/main/i/alpha/map", "/b/main/i/alpha/events", "/b/main/audit", "/b/main/bans", "/b/main/bans/new"} {
		b.run(t, chromedp.Navigate(e.url(p)), chromedp.WaitReady("body"))
	}
	// the collector's cleanup fails the test on any console error, exception or CSP report
}

func TestE2EPlayerNameIsText(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	var who string
	var bold int
	b.run(t,
		chromedp.Navigate(e.url("/b/main/i/alpha")),
		// td.who is filled by the htmx "load" poll, and a later poll swaps the node again: wait and read in the
		// page, since a node id taken by WaitVisible/Text can be gone by the time it is used.
		chromedp.Poll(`document.querySelector("td.who") !== null && document.querySelector("td.who").textContent !== ""`, nil),
		chromedp.Evaluate(`document.querySelector("td.who").textContent`, &who),
		chromedp.Evaluate(`document.querySelectorAll("td.who b").length`, &bold),
	)
	require.Contains(t, who, "<b>alice</b>")
	require.Zero(t, bold)
}

func TestE2ESwitcherOpensAndReachesAnInstance(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	var open bool
	var path string
	b.run(t,
		chromedp.Navigate(e.url("/b/main/i/alpha/events")),
		chromedp.Click(`details.switch summary`),
		chromedp.Evaluate(`document.querySelector("details.switch").open`, &open),
	)
	require.True(t, open)
	b.run(t,
		chromedp.KeyEvent("\x1b"), // Escape
		chromedp.Evaluate(`document.querySelector("details.switch").open`, &open),
	)
	require.False(t, open, "Escape closes the switcher")
	b.run(t,
		chromedp.Click(`details.switch summary`),
		chromedp.Click(`.switchmenu a[href="/b/main/i/alpha"]`),
		chromedp.WaitVisible(`.instbar`),
		chromedp.Evaluate(`location.pathname`, &path),
	)
	require.Equal(t, "/b/main/i/alpha", path)
}

func TestE2ENoHorizontalScrollOnAPhone(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.EmulateViewport(360, 740))
	for _, p := range []string{"/", "/b/main/i/alpha", "/b/main/i/alpha/map", "/b/main/i/alpha/events", "/b/main/bans"} {
		var w int
		b.run(t, chromedp.Navigate(e.url(p)), chromedp.WaitReady("body"), chromedp.Evaluate(`document.documentElement.scrollWidth`, &w))
		require.LessOrEqual(t, w, 360, p)
	}
}

func TestE2EVehicleTableSortsByLastDriven(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	now := time.Now()
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "a", Kind: "car", Type: "Car5h"}, {ID: "b", Kind: "car", Type: "Car3d"},
		{ID: "c", Kind: "car", Type: "Car30m"}, {ID: "d", Kind: "car", Type: "CarNever"}}})
	require.NoError(t, e.st.SyncVehicles(t.Context(), "alpha", []moderation.VehicleSeen{
		{ID: "a", Type: "Car5h", Driver: "76561198000000001", DrivenAt: now.Add(-5*time.Hour - 2*time.Minute)},
		{ID: "b", Type: "Car3d", Driver: "76561198000000001", DrivenAt: now.Add(-72 * time.Hour)},
		{ID: "c", Type: "Car30m", Driver: "76561198000000001", DrivenAt: now.Add(-30 * time.Minute)},
		{ID: "d", Type: "CarNever"}}, time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)))
	b := newBrowser(t)
	const order = `Array.from(document.querySelectorAll("table[data-sort-default] tbody tr")).map(function (r) { return r.cells[0].textContent.trim(); }).join(",")`
	var got string
	var posSort bool
	b.run(t,
		chromedp.Navigate(e.url("/b/main/i/alpha")),
		chromedp.Poll(`document.querySelectorAll("table[data-sort-default] tbody tr").length === 4`, nil),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("table[data-sort-default] th")).some(function (h) { return h.textContent === "Position" && h.hasAttribute("data-sort"); })`, &posSort),
	)
	require.False(t, posSort, "the Position column has no single order")
	click := `Array.from(document.querySelectorAll("table[data-sort-default] th")).find(function (h) { return h.textContent === "Last driven"; }).click()`
	b.run(t, chromedp.Evaluate(click, nil), chromedp.Evaluate(order, &got))
	require.Equal(t, "CarNever,Car3d,Car5h,Car30m", got, "ascending by time, oldest first")
	b.run(t, chromedp.Evaluate(click, nil), chromedp.Evaluate(order, &got))
	require.Equal(t, "Car30m,Car5h,Car3d,CarNever", got, "descending by time")
}

func TestE2EPlayerTableSortsByTheBareName(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	useRealAssets(t, e.assets)
	// "Al" has a BattlEye ban, so the cell text reads "Al flagged: BattlEye global ban", which sorts after
	// "Al b". The sort key is the bare name, so "Al" stays first.
	seedPlayer(t, st, webSteam, "Al", time.Now())
	g, _ := moderation.DeriveGUID(webSteam)
	require.NoError(t, st.SaveBEStatus(t.Context(), g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "x", CheckedAt: time.Now()}))
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{
		{SteamID: "3", Name: "Bo", Alive: true}, {SteamID: webSteam, Name: "Al", Alive: true}, {SteamID: "2", Name: "Al b", Alive: true}}})
	b := newBrowser(t)
	const order = `Array.from(document.querySelectorAll("table.players tbody tr")).map(function (r) { return r.id; }).join(",")`
	var got string
	b.run(t,
		chromedp.Navigate(e.url("/b/main/i/alpha")),
		chromedp.Poll(`document.querySelectorAll("table.players tbody tr").length === 3 && document.querySelector("table.players .flagged") !== null && document.querySelector("table.players th").getAttribute("aria-sort") === "ascending"`, nil),
	)
	b.run(t, chromedp.Evaluate(order, &got))
	require.Equal(t, "player-"+webSteam+",player-2,player-3", got, "A to Z by the name alone before any click")
	click := `document.querySelector("table.players th").click()`
	b.run(t, chromedp.Evaluate(click, nil), chromedp.Evaluate(order, &got))
	require.Equal(t, "player-3,player-2,player-"+webSteam, got, "a click on the sorted Name column reverses it")
	b.run(t, chromedp.Evaluate(click, nil), chromedp.Evaluate(order, &got))
	require.Equal(t, "player-"+webSteam+",player-2,player-3", got, "a second click restores A to Z")
}

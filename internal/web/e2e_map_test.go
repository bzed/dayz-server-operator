// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// markerStyle is the inline style of the first Leaflet marker that matches sel: Leaflet positions a
// marker with a transform, so it changes when the marker moves.
func (b *browser) markerStyle(sel string) string {
	var s string
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`(document.querySelector('`+sel+`')||{style:{}}).style.cssText||""`, &s))
	return s
}

func TestE2EMapShowsPlayersAndVehicles(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha/map")), chromedp.WaitVisible(`.leaflet-container`))
	require.Eventually(t, func() bool { return b.count(".pm-wrap") == 1 && b.count(".vm-wrap") == 4 }, 10*time.Second, 100*time.Millisecond,
		"one player and four vehicles are on the map (players %d, vehicles %d)", b.count(".pm-wrap"), b.count(".vm-wrap"))
	require.Contains(t, b.textOf(".pm-wrap .pm-l"), "<b>alice</b>")
	require.Zero(t, b.count(".pm-wrap b"), "the player name is text, not markup")
}

func TestE2EMapFollowsTheStream(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha/map")), chromedp.WaitVisible(`.leaflet-container`))
	var before string
	require.Eventually(t, func() bool {
		before = b.markerStyle(".pm-wrap")
		return before != ""
	}, 10*time.Second, 100*time.Millisecond, "the player marker appears")

	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, X: 5000, Z: 5000}}})
	require.Eventually(t, func() bool { return b.markerStyle(".pm-wrap") != before }, 10*time.Second, 100*time.Millisecond, "the marker moves with the stream")
}

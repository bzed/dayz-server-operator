// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/chromedp/cdproto/input"
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
	require.Eventually(t, func() bool { return b.count(".pm-wrap") == 1 && b.count(".vm-wrap") == 5 }, 10*time.Second, 100*time.Millisecond,
		"one player and five vehicles are on the map (players %d, vehicles %d)", b.count(".pm-wrap"), b.count(".vm-wrap"))
	require.Contains(t, b.textOf(".pm-wrap .pm-l"), "<b>alice</b>")
	require.Zero(t, b.count(".pm-wrap b"), "the player name is text, not markup")
	require.Equal(t, 1, b.count(".vm.boat i.fa-ship"), "a boat has a ship marker")
	require.Zero(t, b.count(".vm.boat.dot"), "and not the plain diamond of an unknown icon")
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

// focusNearCentre reports whether the focus marker sits within 30 px of the middle of the map.
func (b *browser) focusNearCentre() bool {
	var ok bool
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`(function(){var f=document.querySelector('.focus'),m=document.getElementById('map');if(!f||!m){return false;}
var a=f.getBoundingClientRect(),r=m.getBoundingClientRect();return Math.abs(a.left+a.width/2-(r.left+r.width/2))<30&&Math.abs(a.top+a.height/2-(r.top+r.height/2))<30;})()`, &ok))
	return ok
}

func TestE2EMapOpensOnALink(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha/map?x=7500&z=7600&label=Kill+of+Bob")), chromedp.WaitVisible(`.leaflet-container`))
	require.Eventually(t, func() bool { return b.count(".focus") == 1 && b.focusNearCentre() }, 10*time.Second, 100*time.Millisecond, "the place is marked and in the middle")
	require.Contains(t, b.textOf(".leaflet-tooltip"), "Kill of Bob", "the label is the tooltip")
	require.Zero(t, b.count(".far"), "the map is zoomed in, not fitted")
}

func TestE2EMapIgnoresABadLink(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	for _, q := range []string{"x=abc&z=1", "x=-5000&z=1", "x=1&z=99999", "x=&z=", "x=1"} {
		b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha/map?"+q)), chromedp.WaitVisible(`.leaflet-container`))
		require.Eventually(t, func() bool { return b.count(".far") == 1 }, 10*time.Second, 100*time.Millisecond, q)
		require.Zero(t, b.count(".focus"), q)
	}
}

// hover moves the pointer over the first element that matches sel.
func (b *browser) hover(t *testing.T, sel string) {
	t.Helper()
	var r []float64
	b.run(t, chromedp.Evaluate(`(function(){var r=document.querySelector('`+sel+`').getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2];})()`, &r))
	require.Len(t, r, 2)
	b.run(t, input.DispatchMouseEvent(input.MouseMoved, r[0]-5, r[1]-5), input.DispatchMouseEvent(input.MouseMoved, r[0], r[1]))
}

func TestE2ERebuiltHouseShowsItsDecay(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.EmulateViewport(1280, 800), chromedp.Navigate(e.url("/b/main/i/alpha/map")), chromedp.WaitVisible(`.leaflet-container`))
	e.hub.Sync(admin.SyncRequest{Proto: 1,
		Layers: []admin.Layer{{Name: "Rebuilt houses", Icon: "home", Color: "#8f6f00", Visibility: "operator"}},
		Markers: []admin.Marker{{Layer: "Rebuilt houses", ID: "r1", Shape: "point", X: 6400, Z: 6400, Icon: "home", Label: "House", Source: "rebuilt", TTL: 7380, Visibility: "operator",
			Props: []admin.KV{{K: "parts", V: "3/12"}, {K: "weakest", V: "wall_1 64%"}}},
			// worn down already: no timer, not drawn
			{Layer: "Rebuilt houses", ID: "r2", Shape: "point", X: 6800, Z: 6800, Icon: "home", Label: "Old house", Source: "rebuilt", Visibility: "operator",
				Props: []admin.KV{{K: "decay", V: "worn down"}}}}})
	require.Eventually(t, func() bool { return b.count(".mi-wrap") == 1 }, 10*time.Second, 100*time.Millisecond)
	b.hover(t, ".mi-wrap")
	require.Eventually(t, func() bool { return strings.Contains(b.textOf(".leaflet-tooltip"), "worn down in 2h 3m") }, 5*time.Second, 50*time.Millisecond, "tooltip: %q", b.textOf(".leaflet-tooltip"))
	tip := b.textOf(".leaflet-tooltip")
	require.Contains(t, tip, "parts: 3/12")
	require.Contains(t, tip, "weakest: wall_1 64%")
	require.NotContains(t, tip, "goes down", "flags go down, a rebuilt house is worn down")
	require.Contains(t, b.textOf(".leaflet-control-layers"), "Rebuilt houses")
}

func TestE2ESandstormDrawsAQuietArea(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.EmulateViewport(1280, 800), chromedp.Navigate(e.url("/b/main/i/alpha/map")), chromedp.WaitVisible(`.leaflet-container`))
	box := func(id, band string, from, to float64) admin.Marker {
		return admin.Marker{Layer: "Sandstorm", ID: id, Shape: "polygon", Source: "sandstorm", Visibility: "viewer", Color: "#c8a464",
			Points: []admin.Point{{X: from, Z: from}, {X: to, Z: from}, {X: to, Z: to}, {X: from, Z: to}}, Props: []admin.KV{{K: "band", V: band}}}
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1,
		Layers: []admin.Layer{{Name: "Sandstorm", Icon: "asterisk", Color: "#c8a464", Visibility: "viewer"}},
		Markers: []admin.Marker{box("s0", "light", 4000, 9000), box("s1", "heavy", 5500, 7500),
			{Layer: "Sandstorm", ID: "storm", Shape: "point", X: 6400, Z: 6400, Icon: "asterisk", Label: "Sandstorm", Source: "sandstorm", Visibility: "viewer", TTL: 1500, Color: "#c8a464",
				Props: []admin.KV{{K: "heading", V: "NE (47°)"}, {K: "speed", V: "12 km/h"}, {K: "strength", V: "80%"}}}}})
	require.Eventually(t, func() bool { return b.count(".leaflet-sand-pane path") == 2 }, 10*time.Second, 100*time.Millisecond, "the two bands are drawn in the sand pane")
	require.Zero(t, b.count(".leaflet-sand-pane path.leaflet-interactive"), "the sand does not take clicks or hovers")
	b.hover(t, ".mi-wrap")
	require.Eventually(t, func() bool { return strings.Contains(b.textOf(".leaflet-tooltip"), "reaches its end in 25m") }, 5*time.Second, 50*time.Millisecond, "tooltip: %q", b.textOf(".leaflet-tooltip"))
	require.Contains(t, b.textOf(".leaflet-tooltip"), "heading: NE (47°)")
	var tr string
	b.run(t, chromedp.Evaluate(`document.querySelector('.mi-wrap .storm-dir').style.transform`, &tr))
	require.Equal(t, "rotate(47deg)", tr, "the arrow points the way the storm moves")
}

func TestE2EVehicleLinkOpensTheMap(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{{ID: "o1", Kind: "car", Type: "Offroad_02", Owner: "Bob", X: 7500, Z: 7600}}})
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha")), chromedp.WaitVisible(`a[href*="/map?"]`), chromedp.Click(`a[href*="/map?"]`), chromedp.WaitVisible(`.leaflet-container`))
	require.Eventually(t, func() bool { return b.count(".focus") == 1 && b.focusNearCentre() }, 10*time.Second, 100*time.Millisecond, "the vehicle is marked and in the middle")
	require.Contains(t, b.textOf(".leaflet-tooltip"), "Offroad_02 (owner Bob)")
}

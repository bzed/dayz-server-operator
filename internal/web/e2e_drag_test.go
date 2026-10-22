// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// atPx is the world position (x east, z north) of the map point dx, dy pixels from the middle of the map.
func (b *browser) atPx(t *testing.T, dx, dy float64) (x, z float64) {
	t.Helper()
	var r []float64
	b.run(t, chromedp.Evaluate(fmt.Sprintf(`(function(){var m=document.getElementById('map')._dzoMap,p=m.getSize().divideBy(2).add([%v,%v]),l=m.containerPointToLatLng(p);return [l.lng,l.lat];})()`, dx, dy), &r))
	require.Len(t, r, 2)
	return r[0], r[1]
}

// dragEnv opens the map with alice in the middle and bob's car 100 px east of her, on the path of a drag.
func dragEnv(t *testing.T) (*env, *browser) {
	t.Helper()
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.EmulateViewport(1280, 800), chromedp.Navigate(e.url("/b/main/i/alpha/map")), chromedp.WaitVisible(`.leaflet-container`))
	require.Eventually(t, func() bool { return b.count(".pm-wrap") == 1 }, 10*time.Second, 100*time.Millisecond)
	b.placeAliceAndACar(t, e, 100, 100)
	return e, b
}

// placeAliceAndACar moves alice to the middle of the map (with the given health) and the car 100 px east of it.
// It waits until the car is there, and the player's event came before the vehicles' in the same sync.
func (b *browser) placeAliceAndACar(t *testing.T, e *env, health float64, carDX float64) {
	t.Helper()
	px, pz := b.atPx(t, 0, 0)
	cx, cz := b.atPx(t, carDX, 0)
	before := b.markerStyle(".vm-wrap")
	e.hub.Sync(admin.SyncRequest{Proto: 1,
		Players:  []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, Health: health, X: px, Z: pz}},
		Vehicles: []admin.Vehicle{{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback", X: cx, Z: cz}},
	})
	require.Eventually(t, func() bool { return b.markerStyle(".vm-wrap") != before && b.count(".vm-wrap") == 1 }, 10*time.Second, 50*time.Millisecond, "the sync arrived")
}

// menuText is the text of the open menu, "" when there is none.
func (b *browser) menuText() string {
	var s string
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`(function(){var c=document.querySelector('.ctx');return c&&!c.hidden?c.textContent:"";})()`, &s))
	return s
}

func (b *browser) wantDropAt(t *testing.T, x, z float64) {
	t.Helper()
	require.Eventually(t, func() bool { return strings.Contains(b.menuText(), "Teleport") }, 5*time.Second, 50*time.Millisecond, "the confirmation opens")
	var xs, zs float64
	_, _ = fmt.Sscanf(strings.SplitN(b.menuText(), "To ", 2)[1], "%f / %f", &xs, &zs)
	x1, _ := b.atPx(t, 1.5, 0) // 1.5 px in metres: the press is at the icon's centre, not at a whole pixel
	x0, _ := b.atPx(t, 0, 0)
	tol := x1 - x0
	require.InDelta(t, x, xs, tol, "the drop is where the pointer let go (x), menu: %q", b.menuText())
	require.InDelta(t, z, zs, tol, "the drop is where the pointer let go (z), menu: %q", b.menuText())
}

func TestE2EDragAcrossAVehicleFollowsThePointer(t *testing.T) {
	_, b := dragEnv(t)
	x, z := b.atPx(t, 200, 0)
	b.drag(t, ".pm-wrap", [][2]float64{{200, 0}}, 20)
	b.wantDropAt(t, x, z)
}

func TestE2EUpdateBeforeDragStartDoesNotBreakTheDrag(t *testing.T) {
	e, b := dragEnv(t)
	x, z := b.atPx(t, 200, 0)
	b.press(t, ".pm-wrap")
	// the next sync moves her and changes her health bucket, which swaps the icon under the pointer
	px, pz := b.atPx(t, 0, 0)
	before := b.markerStyle(".vm-wrap")
	e.hub.Sync(admin.SyncRequest{Proto: 1,
		Players:  []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, Health: 30, X: px + 50, Z: pz}},
		Vehicles: []admin.Vehicle{{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback", X: px + 40, Z: pz + 40}},
	})
	require.Eventually(t, func() bool { return b.markerStyle(".vm-wrap") != before }, 10*time.Second, 50*time.Millisecond)
	b.moveBy(t, [][2]float64{{200, 0}}, 20)
	b.run(t, chromedp.Sleep(100*time.Millisecond))
	b.release(t)
	b.wantDropAt(t, x, z)
}

// A flick: the button goes up in the frame after the last move, when the icon is a few pixels behind the
// pointer. The click that follows lands on the map, not on the icon, and must not cancel the drop.
func TestE2EFastReleaseKeepsTheDrop(t *testing.T) {
	_, b := dragEnv(t)
	b.press(t, ".pm-wrap")
	b.moveBy(t, [][2]float64{{200, 0}}, 20)
	b.release(t)
	require.Never(t, func() bool { return b.menuText() == "" }, 800*time.Millisecond, 50*time.Millisecond, "the drop is confirmed or cancelled by the user, not by the click that ends the drag")
	require.Contains(t, b.menuText(), "Teleport")
}

func TestE2EDropStaysUntilConfirmed(t *testing.T) {
	e, b := dragEnv(t)
	b.drag(t, ".pm-wrap", [][2]float64{{200, 0}}, 20)
	require.Eventually(t, func() bool { return strings.Contains(b.menuText(), "Teleport") }, 5*time.Second, 50*time.Millisecond)
	dropped := b.markerStyle(".pm-wrap")
	px, pz := b.atPx(t, 0, 0)
	for i := 0; i < 2; i++ {
		before := b.markerStyle(".vm-wrap")
		e.hub.Sync(admin.SyncRequest{Proto: 1,
			Players:  []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, Health: 100, X: px, Z: pz}},
			Vehicles: []admin.Vehicle{{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback", X: px + float64(500*(i+1)), Z: pz + 80}},
		})
		require.Eventually(t, func() bool { return b.markerStyle(".vm-wrap") != before }, 10*time.Second, 50*time.Millisecond)
	}
	require.Equal(t, dropped, b.markerStyle(".pm-wrap"), "live updates do not move the icon while the confirmation asks")
	require.Contains(t, b.menuText(), "Teleport")
}

func TestE2EZoomKeepsAPendingDrop(t *testing.T) {
	e, b := dragEnv(t)
	defer e.answerCommands(true, "teleported")()
	x, z := b.atPx(t, 200, 0)
	b.drag(t, ".pm-wrap", [][2]float64{{200, 0}}, 20)
	require.Eventually(t, func() bool { return strings.Contains(b.menuText(), "Teleport") }, 5*time.Second, 50*time.Millisecond)
	var zoom float64
	b.run(t, chromedp.Evaluate(`document.getElementById('map')._dzoMap.getZoom()`, &zoom))
	b.mapZoom(t, zoom+1)
	require.Never(t, func() bool { return b.menuText() == "" }, 600*time.Millisecond, 50*time.Millisecond, "zooming to look at the drop point keeps the confirmation")
	b.run(t, chromedp.Click(`.ctx form button:not([type=button])`, chromedp.ByQuery))
	require.Eventually(t, func() bool { return e.commandsOf("teleport") == 1 }, 10*time.Second, 50*time.Millisecond)
	_, _ = x, z
}

func TestE2EDragAtMaxZoom(t *testing.T) {
	e, b := dragEnv(t)
	var maxZ float64
	b.run(t, chromedp.Evaluate(`document.getElementById('map')._dzoMap.getMaxZoom()`, &maxZ))
	b.mapZoom(t, maxZ)
	b.placeAliceAndACar(t, e, 100, 100)
	x, z := b.atPx(t, 200, 0)
	b.drag(t, ".pm-wrap", [][2]float64{{200, 0}}, 20)
	b.wantDropAt(t, x, z)
}

func TestE2EPlainClickLeavesTheIconLive(t *testing.T) {
	e, b := dragEnv(t)
	b.press(t, ".pm-wrap")
	b.release(t)
	before := b.markerStyle(".pm-wrap")
	px, pz := b.atPx(t, 0, 0)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, Health: 100, X: px + 3000, Z: pz}}})
	require.Eventually(t, func() bool { return b.markerStyle(".pm-wrap") != before }, 10*time.Second, 50*time.Millisecond, "after a click the icon follows the stream again")
}

func TestE2EPlayerLeavingClosesTheConfirmation(t *testing.T) {
	e, b := dragEnv(t)
	b.drag(t, ".pm-wrap", [][2]float64{{200, 0}}, 20)
	require.Eventually(t, func() bool { return strings.Contains(b.menuText(), "Teleport") }, 5*time.Second, 50*time.Millisecond)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{}})
	require.Eventually(t, func() bool { return b.menuText() == "" && b.count(".pm-wrap") == 0 }, 10*time.Second, 50*time.Millisecond, "nothing is left to confirm")
}

func TestE2EViewerCannotDragAndTheIconStaysLive(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.EmulateViewport(1280, 800), chromedp.Navigate(e.url("/b/main/i/alpha/map")), chromedp.WaitVisible(`.leaflet-container`))
	require.Eventually(t, func() bool { return b.count(".pm-wrap") == 1 }, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, b.count(".leaflet-marker-draggable"))
	b.press(t, ".pm-wrap")
	before := b.markerStyle(".pm-wrap")
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, X: 9000, Z: 9000}}})
	require.Eventually(t, func() bool { return b.markerStyle(".pm-wrap") != before }, 10*time.Second, 50*time.Millisecond, "a viewer's pressed icon still follows the stream")
	b.release(t)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

func TestE2EPlayerPageFromThePanel(t *testing.T) {
	e, st, f := playerSetup(t, api.RoleModerator)
	useRealAssets(t, e.assets)
	seedPlayer(t, st, webSteam, "Fritz", time.Now())
	g, _ := moderation.DeriveGUID(webSteam)
	f.set = func(string) {
		_ = st.SaveBEStatus(t.Context(), g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "Global Ban #760490", CheckedAt: time.Now()})
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "Fritz", Alive: true}}})
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha")), chromedp.WaitVisible(`button[data-rowpanel]`))
	b.clickUntil(t, `button[data-rowpanel]`, func() bool { return b.count("tr.rowpanel") == 1 })
	b.run(t, chromedp.WaitVisible(`.playerpanel a[href^="/b/main/players/"]`), chromedp.Click(`.playerpanel a[href^="/b/main/players/"]`), chromedp.WaitVisible(`.verdictrow`))
	var path string
	b.run(t, chromedp.Evaluate(`location.pathname`, &path))
	require.Equal(t, "/b/main/players/"+webSteam, path)
	require.Equal(t, 3, b.count(".verdict"))
	require.Zero(t, b.count(".verdict.bad"))

	b.clickUntil(t, `button[hx-post$="/lookup"]`, func() bool { return b.count(".verdict.bad") == 1 })
	require.Contains(t, b.textOf(".verdict.bad"), "Global Ban #760490", "Check now swaps the verdict row")
	require.Equal(t, 1, b.count("#verdicts"), "the row is replaced, not nested")
}

func TestE2EVerdictsStackOnAPhone(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	useRealAssets(t, e.assets)
	seedPlayer(t, st, webSteam, "Fritz the Cat with a very long name that has to wrap somewhere on a phone", time.Now())
	b := newBrowser(t)
	b.run(t, chromedp.EmulateViewport(360, 740), chromedp.Navigate(e.url("/b/main/players/"+webSteam)), chromedp.WaitVisible(`.verdictrow`))
	var w int
	var tops []float64
	b.run(t,
		chromedp.Evaluate(`document.documentElement.scrollWidth`, &w),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.verdict')).map(function(e){return e.getBoundingClientRect().top;})`, &tops),
	)
	require.LessOrEqual(t, w, 360, "no horizontal scroll")
	require.Len(t, tops, 3)
	require.Less(t, tops[0], tops[1], "the cells stack: BattlEye, Steam, Here")
	require.Less(t, tops[1], tops[2])
	b.run(t, chromedp.Navigate(e.url("/b/main/players")), chromedp.WaitReady("body"), chromedp.Evaluate(`document.documentElement.scrollWidth`, &w))
	require.LessOrEqual(t, w, 360, "the list too")
}

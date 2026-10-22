// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"testing"

	"github.com/bzed/dayz-server-operator/internal/api"
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
		chromedp.WaitVisible(`td.who`), // filled by the htmx "load" poll
		chromedp.Text(`td.who`, &who),
		chromedp.Evaluate(`document.querySelectorAll("td.who b").length`, &bold),
	)
	require.Contains(t, who, "<b>alice</b>")
	require.Zero(t, bold)
}

func TestE2ESwitcherReachesAnotherBackendsInstance(t *testing.T) {
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
		chromedp.Click(`.switchmenu a[href="/b/other/i/alpha"]`),
		chromedp.WaitVisible(`.instbar`),
		chromedp.Evaluate(`location.pathname`, &path),
	)
	require.Equal(t, "/b/other/i/alpha", path)
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

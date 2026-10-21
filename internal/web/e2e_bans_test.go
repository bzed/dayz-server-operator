// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"net/url"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestE2EBanFormFansOut(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t,
		chromedp.Navigate(e.url("/b/main/bans/new?steam_id="+webSteam+"&instance=alpha")),
		chromedp.WaitVisible(`form.banform`),
		chromedp.SendKeys(`textarea[name=reason]`, "aimbot"),
		chromedp.SetValue(`select[name=duration]`, "7d"),
		chromedp.Click(`input[name=also][value=other]`),
		chromedp.Click(`form.banform button.danger`),
		chromedp.WaitVisible(`#ban-result .flash`),
	)
	require.Eventually(t, func() bool { return b.count("#ban-result .flash.ok") == 2 }, 10*time.Second, 100*time.Millisecond,
		"one result per backend: %s", b.textOf("#ban-result"))
	result := b.textOf("#ban-result")
	require.Contains(t, result, "main: ban #1 created")
	require.Contains(t, result, "other: ban #1 created")

	for _, backend := range []string{"main", "other"} {
		code, list, _ := e.get("/b/" + backend + "/bans?state=active")
		require.Equal(t, 200, code)
		require.Contains(t, list, "aimbot", "backend %s holds the ban", backend)
	}
}

func TestE2ERevokeAsksForConfirmation(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	code, body := e.postForm("/b/main/bans", url.Values{"steam_id": {webSteam}, "reason": {"first"}, "duration": {"permanent"}, "scope": {"global"}}, true)
	require.Equal(t, 200, code, "create: %s", body)
	b := newBrowser(t)
	revoke := `form[hx-post$="/revoke"]`
	b.run(t,
		chromedp.Navigate(e.url("/b/main/bans/1")),
		chromedp.WaitVisible(revoke+` input[name=reason]`),
		chromedp.SendKeys(revoke+` input[name=reason]`, "appeal"),
	)
	answered := func() bool { return len(b.dialogs) == 0 }

	b.dialogs <- false
	b.clickUntil(t, revoke+` button`, answered)
	require.Never(t, func() bool { return b.textOf("#flash") != "" }, 500*time.Millisecond, 50*time.Millisecond, "dismissing the confirmation revokes nothing")
	_, page, _ := e.get("/b/main/bans/1")
	require.NotContains(t, page, "revoked")

	b.dialogs <- true
	b.clickUntil(t, revoke+` button`, answered)
	require.Eventually(t, func() bool { return b.textOf("#flash") != "" }, 10*time.Second, 50*time.Millisecond)
	require.Contains(t, b.textOf("#flash"), "revoked")
	_, page, _ = e.get("/b/main/bans/1")
	require.Contains(t, page, "revoked")
}

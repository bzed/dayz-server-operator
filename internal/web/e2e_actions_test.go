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

// textOf is the text of the first element that matches sel, or "" while there is none.
func (b *browser) textOf(sel string) string {
	var text string
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`(document.querySelector(`+jsString(sel)+`)||{}).textContent||""`, &text))
	return text
}

// valueOf is the value of the first form field that matches sel.
func (b *browser) valueOf(sel string) string {
	var v string
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`(document.querySelector(`+jsString(sel)+`)||{}).value||""`, &v))
	return v
}

func (b *browser) count(sel string) int {
	var n int
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`document.querySelectorAll(`+jsString(sel)+`).length`, &n))
	return n
}

func jsString(s string) string { return "'" + s + "'" }

// openPlayerPanel opens the Actions row under alice's row.
func (b *browser) openPlayerPanel(t *testing.T, e *env) {
	t.Helper()
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha")), chromedp.WaitVisible(`button[data-rowpanel]`))
	b.clickUntil(t, `button[data-rowpanel]`, func() bool { return b.count("tr.rowpanel") == 1 })
}

func TestE2EKickDialogSendsTheKick(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	resetKicked()
	b := newBrowser(t)
	b.openPlayerPanel(t, e)
	b.clickUntil(t, `button[data-dialog="kick-7656"]`, func() bool { return b.count("dialog#kick-7656[open]") == 1 })
	b.run(t,
		chromedp.WaitVisible(`dialog#kick-7656 input[name=reason]`),
		chromedp.SendKeys(`dialog#kick-7656 input[name=reason]`, "afk"),
		chromedp.Click(`dialog#kick-7656 form button:not([type=button])`),
	)
	require.Eventually(t, func() bool { return b.textOf("#panel-flash") != "" }, 10*time.Second, 50*time.Millisecond, "the result of the kick shows in the panel")
	require.Equal(t, []string{"alpha 7656 afk"}, kickedList())
	require.Eventually(t, func() bool { return b.count("dialog#kick-7656[open]") == 0 }, 5*time.Second, 50*time.Millisecond, "the kick dialog closes after the request")
}

func TestE2EKillAsksForConfirmation(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	defer e.answerCommands(true, "killed")()
	b := newBrowser(t)
	b.openPlayerPanel(t, e)
	kill := `button[hx-post$="/kill"]`

	answered := func() bool { return len(b.dialogs) == 0 }
	b.dialogs <- false
	b.clickUntil(t, kill, answered)
	require.Never(t, func() bool { return e.commandsOf("player_kill") > 0 }, 300*time.Millisecond, 20*time.Millisecond, "dismissing the confirmation sends nothing")

	b.dialogs <- true
	b.clickUntil(t, kill, answered)
	require.Eventually(t, func() bool { return e.commandsOf("player_kill") == 1 }, 10*time.Second, 50*time.Millisecond)
}

func TestE2EPollingWaitsWhileAPanelIsOpen(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.openPlayerPanel(t, e)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{
		{SteamID: "7656", Name: "<b>alice</b>", Alive: true, X: 1, Z: 2},
		{SteamID: "7657", Name: "bob", Alive: true, X: 3, Z: 4},
	}})
	// longer than the 5 s of the player table's poll
	require.Never(t, func() bool {
		return b.count("tr.rowpanel") == 0 || b.count("td.who") != 1
	}, 6*time.Second, 200*time.Millisecond, "the open panel stays and the table is not reloaded under it")

	b.clickUntil(t, `tr.rowpanel button[data-close]`, func() bool { return b.count("tr.rowpanel") == 0 })
	require.Eventually(t, func() bool { return b.count("td.who") == 2 }, 10*time.Second, 200*time.Millisecond, "the new player appears once the panel is closed")
}

func TestE2EMessageFormResets(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	defer e.answerCommands(true, "sent")()
	b := newBrowser(t)

	b.run(t,
		chromedp.Navigate(e.url("/b/main/i/alpha")),
		chromedp.WaitVisible(`section form[hx-post$="/message"] input[name=text]`),
		chromedp.SendKeys(`section form[hx-post$="/message"] input[name=text]`, "hello all"),
		chromedp.Click(`section form[hx-post$="/message"] button`),
	)
	require.Eventually(t, func() bool { return e.commandsOf("message") == 1 }, 10*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return b.valueOf(`section form[hx-post$="/message"] input[name=text]`) == "" }, 5*time.Second, 50*time.Millisecond, "the broadcast form resets after sending")

	b.openPlayerPanel(t, e)
	b.run(t,
		chromedp.SendKeys(`.playerpanel form[hx-post$="/message"] input[name=text]`, "hello alice"),
		chromedp.Click(`.playerpanel form[hx-post$="/message"] button`),
	)
	require.Eventually(t, func() bool { return e.commandsOf("message") == 2 }, 10*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return b.valueOf(`.playerpanel form[hx-post$="/message"] input[name=text]`) == "" }, 5*time.Second, 50*time.Millisecond, "the direct message form resets after sending")
}

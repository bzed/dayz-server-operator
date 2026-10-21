// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
)

func (b *browser) firstID() string {
	var id string
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`(document.querySelector('#evlog li')||{getAttribute:()=>''}).getAttribute('data-id')`, &id))
	return id
}

func TestE2EEventsPageTailsTheFeed(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	seedFeed(e)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha/events")), chromedp.WaitVisible(`#evlog li`))
	require.Equal(t, 3, b.count("#evlog li"), "the backlog is there at once")
	require.Equal(t, "<script>alert(1)</script>", b.textOf("#evlog .say"), "chat is text")
	require.Zero(t, b.count("#evlog script"))

	// An event added after the page opened appears without a reload.
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "Bob", Text: "live"})
	require.Eventually(t, func() bool { return b.count("#evlog li") == 4 }, 10*time.Second, 50*time.Millisecond)

	// The log keeps at most 1000 rows: the oldest go.
	for i := 0; i < 1000; i++ {
		e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "Bob", Text: fmt.Sprint("line ", i)})
	}
	require.Eventually(t, func() bool { return b.count("#evlog li") == 1000 && b.firstID() == "5" }, 20*time.Second, 100*time.Millisecond)

	// The stream drops: what came in meanwhile appears once.
	b.expect("ERR_INCOMPLETE_CHUNKED_ENCODING")
	e.web.CloseClientConnections()
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "Bob", Text: "while away one"})
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "Bob", Text: "while away two"})
	require.Eventually(t, func() bool {
		var n int
		_ = chromedp.Run(b.ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('#evlog .say')).filter(e => e.textContent.startsWith('while away')).length`, &n))
		return n == 2
	}, 20*time.Second, 100*time.Millisecond)
	require.Never(t, func() bool { return b.count("#evlog li") > 1000 }, time.Second, 100*time.Millisecond)
}

func TestE2EEventsFilterHidesAGroup(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	seedFeed(e)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha/events")), chromedp.WaitVisible(`#evlog li`), chromedp.Click(`input[data-group=chat]`))
	var shown int
	b.run(t, chromedp.Evaluate(`Array.from(document.querySelectorAll('#evlog li')).filter(e => e.offsetParent !== null).length`, &shown))
	require.Equal(t, 2, shown, "the chat line is hidden")
}

func TestE2EEventsInputSendsChat(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	defer e.answerCommands(true, "sent")()
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha/events")), chromedp.WaitVisible(`.evinput input[name=text]`),
		chromedp.SendKeys(`.evinput input[name=text]`, "hello world\n"))
	require.Eventually(t, func() bool { return e.commandsOf("message") == 1 }, 10*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return b.valueOf(".evinput input[name=text]") == "" }, 5*time.Second, 50*time.Millisecond, "the line is cleared")
	var focused bool
	b.run(t, chromedp.Evaluate(`document.activeElement === document.querySelector('.evinput input[name=text]')`, &focused))
	require.True(t, focused, "the focus stays in the field")
	entries, _ := e.api.Audit.Tail("alpha", 1)
	require.Contains(t, entries[0].Detail, "hello world")
}

// inView reports whether the first element that matches sel is inside the window.
func (b *browser) inView(sel string) bool {
	var ok bool
	_ = chromedp.Run(b.ctx, chromedp.Evaluate(`(function(){var e=document.querySelector('`+sel+`');if(!e){return false;}var r=e.getBoundingClientRect();return r.top>=0&&r.bottom<=window.innerHeight;})()`, &ok))
	return ok
}

func TestE2EPlayerLinkHighlightsTheRow(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha#player-7656")))
	require.Eventually(t, func() bool { return b.count("tr#player-7656.hl") == 1 }, 10*time.Second, 100*time.Millisecond, "the row is highlighted after the first poll")
	require.True(t, b.inView("tr#player-7656"), "and scrolled into view")
	require.Empty(t, b.textOf("#player-note"))
	require.Eventually(t, func() bool { return b.count("tr#player-7656.hl") == 0 }, 15*time.Second, 200*time.Millisecond, "the highlight fades")
}

func TestE2EPlayerLinkOfAnOfflinePlayer(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha#player-9999")))
	require.Eventually(t, func() bool { return b.textOf("#player-note") == "This player is not online." }, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, b.count("tr.hl"))
}

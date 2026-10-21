// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
)

const aliceID = "76561198000000001"

func seedFeed(e *env) {
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, SteamID: aliceID, Name: "Alice", Channel: "Global", Text: "<script>alert(1)</script>"})
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedKill, SteamID: aliceID, Name: "Alice", TargetName: "AI (East)", TargetAI: true,
		Weapon: "M4-A1", Distance: 123, HasPos: true, X: 7500, Z: 7600})
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedConnect, Name: "Ghost"})
}

func TestEventsPage(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	seedFeed(e)
	code, body, _ := e.get("/b/main/i/alpha/events")
	require.Equal(t, 200, code, body)
	require.Contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;", "chat text is escaped")
	require.NotContains(t, body, "<script>alert")
	require.Contains(t, body, `href="/b/main/i/alpha#player-`+aliceID+`"`, "a player with a Steam ID links to the players list")
	require.NotContains(t, body, `#player-"`, "no link without a Steam ID")
	require.Contains(t, body, "Ghost")
	require.Contains(t, body, `href="/b/main/i/alpha/map?x=7500&amp;z=7600&amp;label=`, "a position links to the map")
	require.Contains(t, body, `data-kind="kill"`)
	require.Contains(t, body, "123 m")
	require.Contains(t, body, `data-stream="/b/main/i/alpha/stream?only=feed"`)
	require.Contains(t, body, `data-last="3"`)
	require.Contains(t, body, `src="/static/events.js"`)
	require.NotContains(t, body, "<script>", "CSP: no inline scripts")
	c, js, _ := e.get("/static/events.js")
	require.Equal(t, 200, c)
	require.Contains(t, js, "EventSource")
}

func TestEventsPageInputNeedsBroadcast(t *testing.T) {
	viewer := newEnv(t, api.RoleViewer)
	_, body, _ := viewer.get("/b/main/i/alpha/events")
	require.NotContains(t, body, `/b/main/i/alpha/message`, "a viewer has no input line")
	require.NotContains(t, body, `name="text"`)

	op := newEnv(t, api.RoleOperator)
	_, body, _ = op.get("/b/main/i/alpha/events")
	require.Contains(t, body, `hx-post="/b/main/i/alpha/message"`)
	require.Contains(t, body, `name="style" value="chat"`)
	require.Contains(t, body, `name="text"`)
}

func TestEventsPageUnknown(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	c, _, _ := e.get("/b/nope/i/alpha/events")
	require.Equal(t, 404, c)
	_, body, _ := e.get("/b/main/i/zzz/events")
	require.Contains(t, body, "unknown instance")
}

func TestFeedFragmentAfter(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	seedFeed(e)
	code, body, _ := e.get("/b/main/i/alpha/feed?after=2")
	require.Equal(t, 200, code)
	require.Contains(t, body, "Ghost")
	require.NotContains(t, body, "M4-A1", "only the events after id 2")
	require.NotContains(t, body, "<html", "a fragment")
	_, body, _ = e.get("/b/main/i/alpha/feed")
	require.Contains(t, body, "M4-A1", "without after: the newest events")
	for _, bad := range []string{"x", "-1", "1.5"} {
		code, _, _ = e.get("/b/main/i/alpha/feed?after=" + bad)
		require.Equal(t, 400, code, bad)
	}
	c, _, _ := e.get("/b/nope/i/alpha/feed")
	require.Equal(t, 404, c)
}

func TestEventsPageBacklogIsBounded(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	for i := 0; i < 250; i++ {
		e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "Bob", Text: "line"})
	}
	_, body, _ := e.get("/b/main/i/alpha/events")
	require.Equal(t, 200, strings.Count(body, `data-kind="chat"`))
	require.Contains(t, body, `data-last="250"`)
}

func TestStreamOnlyPassed(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.web.URL+"/b/main/i/alpha/stream?only=feed", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	done := make(chan struct{})
	go func() { // the relay connects to the API in the background: add events until one arrives
		defer close(done)
		for ctx.Err() == nil {
			e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: "ping"})
			time.Sleep(10 * time.Millisecond)
		}
	}()
	var kinds []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			kinds = append(kinds, v)
			break
		}
	}
	cancel()
	<-done
	require.Equal(t, []string{"feed"}, kinds, "no snapshot of players, vehicles, ...")

	code, _, _ := e.get("/b/main/i/alpha/stream?only=players")
	require.Equal(t, 400, code)
}

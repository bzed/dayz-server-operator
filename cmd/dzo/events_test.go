// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
)

// eventsAPI is a real API over a hub with a few events, and the request URIs it saw.
type eventsAPI struct {
	hub  *admin.Hub
	args []string // --api and --token-file

	mu   sync.Mutex
	uris []string
}

func newEventsAPI(t *testing.T) *eventsAPI {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	e := &eventsAPI{hub: hubs.Add(admin.NewHub("alpha"))}
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.uris = append(e.uris, r.URL.RequestURI())
		e.mu.Unlock()
		srv.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	secret, _, err := srv.Tokens.Create("cli", api.RoleViewer, nil, false, time.Hour)
	require.NoError(t, err)
	tokenFile := filepath.Join(dir, "token")
	writeFile(t, tokenFile, secret+"\n")
	e.args = []string{"--api", ts.URL, "--token-file", tokenFile}
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedConnect, SteamID: "76561198000000001", Name: "Alice"})
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "Alice", Channel: "Global", Text: "hi"})
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedKill, Name: "Alice", TargetName: "Bob", Weapon: "M4-A1", Distance: 123.4, HasPos: true, X: 7512, Z: 7633})
	return e
}

func (e *eventsAPI) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runCmd(t, append(append([]string{"events"}, args...), e.args...)...)
}

func (e *eventsAPI) seen() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.uris...)
}

func TestEventsLastN(t *testing.T) {
	e := newEventsAPI(t)
	out, err := e.run(t, "alpha", "-n", "2")
	require.NoError(t, err, out)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "chat")
	require.Contains(t, lines[0], "[Global] Alice: hi")
	require.Regexp(t, `^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d kill        Alice killed Bob with M4-A1 \(123 m\)  @ 7512 / 7633$`, lines[1])
	require.Contains(t, e.seen(), "/api/v1/instances/alpha/feed?after=0&limit=2")
}

func TestEventsDefaultsToTwentyLines(t *testing.T) {
	e := newEventsAPI(t)
	_, err := e.run(t, "alpha")
	require.NoError(t, err)
	require.Contains(t, e.seen(), "/api/v1/instances/alpha/feed?after=0&limit=20")
	_, err = e.run(t, "alpha", "-n", "-1")
	require.Error(t, err)
}

func TestEventsKindFilter(t *testing.T) {
	e := newEventsAPI(t)
	out, err := e.run(t, "alpha", "--kind", "kills,chat")
	require.NoError(t, err, out)
	require.Contains(t, out, "killed Bob")
	require.Contains(t, out, "hi")
	require.NotContains(t, out, "joined")
	out, err = e.run(t, "alpha", "--kind", "connect")
	require.NoError(t, err, out)
	require.Contains(t, out, "joined")
	require.NotContains(t, out, "killed")
	out, err = e.run(t, "alpha", "--kind", "nope")
	require.Error(t, err)
	require.Contains(t, err.Error()+out, "kills", "the error lists the valid names")
	require.Contains(t, err.Error()+out, `"nope"`)
}

func TestEventsJSON(t *testing.T) {
	e := newEventsAPI(t)
	out, err := e.run(t, "alpha", "--json")
	require.NoError(t, err, out)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	for i, l := range lines {
		var ev admin.FeedEvent
		require.NoError(t, json.Unmarshal([]byte(l), &ev), l)
		require.EqualValues(t, i+1, ev.ID)
	}
}

func TestEventsNoColorWhenNotTerminal(t *testing.T) {
	e := newEventsAPI(t)
	out, err := e.run(t, "alpha")
	require.NoError(t, err)
	require.NotContains(t, out, "\x1b")
}

func TestEventsStripsTerminalEscapes(t *testing.T) {
	e := newEventsAPI(t)
	e.hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Name: "evil\x1b[2J", Text: "x\x9b31m\rboom"})
	out, err := e.run(t, "alpha")
	require.NoError(t, err)
	require.NotContains(t, out, "\x1b")
	require.NotContains(t, out, "\r")
	require.NotContains(t, out, "\u009b")
	require.Contains(t, out, "evil?[2J")
}

func TestEventsUnknownInstance(t *testing.T) {
	e := newEventsAPI(t)
	_, err := e.run(t, "nowhere")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nowhere")
}

func TestColorFor(t *testing.T) {
	for kind, want := range map[string]string{
		admin.FeedKill: "31", admin.FeedDeath: "31", admin.FeedDestroy: "35", admin.FeedLockCut: "35", admin.FeedChat: "36",
		admin.FeedConnect: "32", admin.FeedKick: "32", admin.FeedAdmin: "33", admin.FeedBuild: "", "weird": "",
	} {
		require.Equal(t, want, colorFor(kind), kind)
	}
	var buf bytes.Buffer
	require.False(t, useColor(&buf, false), "a buffer is not a terminal")
}

// fakeFeed serves a stream that sends frames and closes, and the feed after an id from a fixed list.
type fakeFeed struct {
	mu      sync.Mutex
	events  []admin.FeedEvent // everything the feed has
	streams [][]int64         // per connection: ids to send, then close
	conns   int
	after   []int64 // the after= of every feed request
	cancel  context.CancelFunc
}

func (f *fakeFeed) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/instances/alpha/feed", func(w http.ResponseWriter, r *http.Request) {
		var after int64
		_, _ = fmt.Sscanf(r.URL.Query().Get("after"), "%d", &after)
		f.mu.Lock()
		f.after = append(f.after, after)
		var out []admin.FeedEvent
		for _, e := range f.events {
			if e.ID > after {
				out = append(out, e)
			}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /api/v1/instances/alpha/stream", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		var ids []int64
		if f.conns < len(f.streams) {
			ids = f.streams[f.conns]
		}
		f.conns++
		last := f.conns >= len(f.streams)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, id := range ids {
			b, _ := json.Marshal(admin.FeedEvent{ID: id, Kind: admin.FeedChat, Name: "n", Text: fmt.Sprint("event ", id)})
			_, _ = fmt.Fprintf(w, "event: feed\ndata: %s\n\n", b)
		}
		w.(http.Flusher).Flush()
		if last {
			<-r.Context().Done() // the last connection stays open until the command is stopped
		}
	})
	return mux
}

func chatEvent(id int64) admin.FeedEvent {
	return admin.FeedEvent{ID: id, Kind: admin.FeedChat, Name: "n", Text: fmt.Sprint("event ", id)}
}

func runFollow(t *testing.T, f *fakeFeed, wantLines int, backlog int) (string, *fakeFeed) {
	t.Helper()
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	var out lockedBuffer
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := &eventsRunner{c: apiclient.New(ts.URL, "tok"), instance: "alpha", follow: true, out: &out, errOut: &out,
		minBackoff: time.Millisecond, maxBackoff: 5 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- r.run(ctx, backlog) }()
	require.Eventually(t, func() bool { return strings.Count(out.String(), "\n") >= wantLines }, 10*time.Second, 5*time.Millisecond, out.String())
	cancel()
	require.NoError(t, <-done, "cancelling ends the command with exit 0")
	return out.String(), f
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestEventsFollowReconnects(t *testing.T) {
	f := &fakeFeed{
		events:  []admin.FeedEvent{chatEvent(1), chatEvent(2), chatEvent(3), chatEvent(4), chatEvent(5), chatEvent(6)},
		streams: [][]int64{{3}, {5, 6}},
	}
	// The backlog is empty (n 0): everything comes through the stream and the catch-up after the drop.
	out, f := runFollow(t, f, 4, 0)
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		ids = append(ids, l[strings.LastIndex(l, "event ")+6:])
	}
	require.Equal(t, []string{"3", "4", "5", "6"}, ids, "each event once, in order")
	require.Contains(t, f.after, int64(3), "the catch-up asks for the events after the last one printed")
	require.NotContains(t, out, "missed")
}

func TestEventsFollowMarksAGap(t *testing.T) {
	// The ring moved on while the stream was down: ids 4 and 5 are gone.
	f := &fakeFeed{events: []admin.FeedEvent{chatEvent(1), chatEvent(2), chatEvent(3), chatEvent(6)}, streams: [][]int64{{3}, {}}}
	out, _ := runFollow(t, f, 3, 0)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Contains(t, lines[0], "event 3")
	require.Contains(t, lines[1], "-- events missed --")
	require.Contains(t, lines[2], "event 6")
}

func TestEventsFollowFillsAGapInTheStream(t *testing.T) {
	// A frame was dropped on the way (slow consumer): id 4 is fetched before 5 is printed.
	f := &fakeFeed{events: []admin.FeedEvent{chatEvent(3), chatEvent(4), chatEvent(5)}, streams: [][]int64{{3, 5}}}
	out, _ := runFollow(t, f, 3, 0)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Contains(t, lines[0], "event 3")
	require.Contains(t, lines[1], "event 4")
	require.Contains(t, lines[2], "event 5")
}

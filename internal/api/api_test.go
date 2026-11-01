// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

type env struct {
	t     testing.TB
	srv   *Server
	ts    *httptest.Server
	hubs  *admin.Registry
	alpha *admin.Hub
}

func newEnv(t testing.TB) *env {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	alpha := hubs.Add(admin.NewHub("alpha"))
	hubs.Add(admin.NewHub("beta"))
	srv := New("test-site", "v0", hubs, NewTokenStore(filepath.Join(dir, "tokens.json")), admin.NewAudit(filepath.Join(dir, "audit.jsonl")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{t: t, srv: srv, ts: ts, hubs: hubs, alpha: alpha}
}

func (e *env) token(role Role, instances []string, delegate bool) string {
	e.t.Helper()
	s, _, err := e.srv.Tokens.Create("t-"+string(role), role, instances, delegate, false, time.Hour)
	require.NoError(e.t, err)
	return s
}

func (e *env) do(method, path, token, body string, hdr ...string) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

// modAnswers plays the mod: connects, then answers every command with ok.
func (e *env) modAnswers(failWith string) (stop func()) {
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.1.0", World: "chernarusplus",
		Players:  []admin.Player{{SteamID: "7656", Name: "alice", X: 100, Z: 200}},
		Vehicles: []admin.Vehicle{{ID: "v1", Type: "OffroadHatchback"}},
		Markers: []admin.Marker{
			{Layer: "ufo", ID: "1", Visibility: "viewer"},
			{Layer: "secret", ID: "2"},
		},
		Layers: []admin.Layer{{Name: "ufo", Visibility: "viewer"}, {Name: "secret"}},
		Events: []admin.Event{{ID: "e", Name: "StaticHeliCrash"}},
	})
	ctx, cancel := context.WithCancel(e.t.Context())
	go func() {
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		for {
			rep := e.alpha.Sync(admin.SyncRequest{Proto: 1})
			var res []admin.Result
			for _, c := range rep.Commands {
				res = append(res, admin.Result{ID: c.ID, OK: failWith == "", Message: failWith})
			}
			if len(res) > 0 {
				e.alpha.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return cancel
}

func TestAuthAndInfo(t *testing.T) {
	e := newEnv(t)
	c, _ := e.do("GET", "/api/v1", "", "")
	require.Equal(t, 401, c)
	c, _ = e.do("GET", "/api/v1", "dzo_nope", "")
	require.Equal(t, 401, c)
	tok := e.token(RoleViewer, nil, false)
	c, b := e.do("GET", "/api/v1", tok, "")
	var info Info
	err := json.Unmarshal(b, &info)
	require.NoError(t, err, "info = %s (%d)", b, c)
	require.Equal(t, 200, c, "info = %s (%d)", b, c)
	require.Equal(t, "test-site", info.Installation, "info = %s (%d)", b, c)
	require.Equal(t, 1, info.APIVersion, "info = %s (%d)", b, c)
	require.Equal(t, RoleViewer, info.Role, "info = %s (%d)", b, c)
	c, _ = e.do("GET", "/healthz", "", "")
	require.Equal(t, 200, c, "healthz must be public")
}

func TestInstanceScope(t *testing.T) {
	e := newEnv(t)
	tok := e.token(RoleViewer, []string{"beta"}, false)
	_, b := e.do("GET", "/api/v1/instances", tok, "")
	var list []admin.Status
	_ = json.Unmarshal(b, &list)
	require.Len(t, list, 1, "list = %s", b)
	require.Equal(t, "beta", list[0].Instance)
	c, _ := e.do("GET", "/api/v1/instances/alpha/players", tok, "")
	require.Equal(t, 404, c)
	c, _ = e.do("GET", "/api/v1/instances/zzz", tok, "")
	require.Equal(t, 404, c)
	c, _ = e.do("GET", "/api/v1/instances/beta", tok, "")
	require.Equal(t, 200, c)
}

func TestReadEndpointsAndVisibility(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	viewer, admn := e.token(RoleViewer, nil, false), e.token(RoleAdmin, nil, false)
	get := func(tok, path string, v any) {
		t.Helper()
		c, b := e.do("GET", path, tok, "")
		require.Equal(t, 200, c)
		err := json.Unmarshal(b, v)
		require.NoError(t, err)
	}
	var players []admin.Player
	get(viewer, "/api/v1/instances/alpha/players", &players)
	var vehicles []admin.Vehicle
	get(viewer, "/api/v1/instances/alpha/vehicles", &vehicles)
	var events []admin.Event
	get(viewer, "/api/v1/instances/alpha/events", &events)
	require.Len(t, players, 1)
	require.Len(t, vehicles, 1)
	require.Len(t, events, 1)
	var vm, am []admin.Marker
	get(viewer, "/api/v1/instances/alpha/map/markers", &vm)
	get(admn, "/api/v1/instances/alpha/map/markers", &am)
	require.Len(t, vm, 1, "visibility: viewer %d admin %d", len(vm), len(am))
	require.Len(t, am, 2, "visibility: viewer %d admin %d", len(vm), len(am))
	get(admn, "/api/v1/instances/alpha/map/markers?layer=secret", &am)
	require.Len(t, am, 1, "layer filter")
	var vl, al []admin.Layer
	get(viewer, "/api/v1/instances/alpha/layers", &vl)
	get(admn, "/api/v1/instances/alpha/layers", &al)
	require.Len(t, vl, 1, "layers: viewer %d admin %d", len(vl), len(al))
	require.Len(t, al, 2, "layers: viewer %d admin %d", len(vl), len(al))
	var st admin.Status
	get(viewer, "/api/v1/instances/alpha", &st)
	require.True(t, st.Connected, "status")
	var names []string
	get(viewer, "/api/v1/instances/alpha/types?q=x&limit=3", &names)
	require.NotNil(t, names, "types must be a list")
}

func TestActions(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	op := e.token(RoleOperator, nil, true)
	cases := []struct{ method, path, body string }{
		{"POST", "/api/v1/instances/alpha/message", `{"text":"restart soon","style":"important"}`},
		{"POST", "/api/v1/instances/alpha/players/7656/message", `{"text":"hi"}`},
		{"POST", "/api/v1/instances/alpha/players/7656/teleport", `{"x":100,"z":200}`},
		{"POST", "/api/v1/instances/alpha/players/7656/teleport", `{"to_steam_id":"7657"}`},
		{"POST", "/api/v1/instances/alpha/players/7656/teleport", `{"x":1,"z":2,"y":30}`},
		{"POST", "/api/v1/instances/alpha/players/7656/give", `{"type":"AKM","quantity":1,"health":1,"target":"hands"}`},
		{"POST", "/api/v1/instances/alpha/spawn", `{"type":"AKM","quantity":20,"x":100,"z":200}`},
		{"POST", "/api/v1/instances/alpha/spawn-vehicle", `{"type":"AKM","x":100,"z":200}`},
		{"POST", "/api/v1/instances/alpha/vehicles/v1/repair", `{"scope":"wheels"}`},
		{"POST", "/api/v1/instances/alpha/vehicles/v1/repair", ``},
		{"POST", "/api/v1/instances/alpha/vehicles/v1/flip", ``},
		{"POST", "/api/v1/instances/alpha/vehicles/v1/move", `{"x":100,"z":200}`},
		{"POST", "/api/v1/instances/alpha/flags/f1/raise", ``},
		{"POST", "/api/v1/instances/alpha/players/7656/heal", ``},
		{"POST", "/api/v1/instances/alpha/players/7656/fixgear", ``},
		{"POST", "/api/v1/instances/alpha/players/7656/kill", ``},
		{"DELETE", "/api/v1/instances/alpha/vehicles/v1?force=1", ``},
	}
	for _, tc := range cases {
		c, b := e.do(tc.method, tc.path, op, tc.body, "X-Dzo-Actor", "alice", "X-Dzo-Source", "web")
		var r ActionResult
		err := json.Unmarshal(b, &r)
		require.NoError(t, err, "%s %s: %d %s", tc.method, tc.path, c, b)
		require.Equal(t, 200, c, "%s %s: %d %s", tc.method, tc.path, c, b)
		require.True(t, r.OK, "%s %s: %d %s", tc.method, tc.path, c, b)
		require.NotEmpty(t, r.ID, "%s %s: %d %s", tc.method, tc.path, c, b)
	}
	entries, _ := e.srv.Audit.Tail("alpha", 100)
	require.Len(t, entries, len(cases))
	require.Equal(t, "t-operator:alice", entries[0].Actor)
	require.Equal(t, "web", entries[0].Source)
	require.True(t, entries[0].OK, "audit = %+v", entries)
	require.Equal(t, "vehicle_delete", entries[0].Action)
	// The audit endpoint is admin only.
	c, _ := e.do("GET", "/api/v1/audit", op, "")
	require.Equal(t, 403, c)
	c, b := e.do("GET", "/api/v1/audit?instance=alpha&limit=2", e.token(RoleAdmin, nil, false), "")
	var got []admin.AuditEntry
	err := json.Unmarshal(b, &got)
	require.NoError(t, err, "audit endpoint: %d %s", c, b)
	require.Equal(t, 200, c, "audit endpoint: %d %s", c, b)
	require.Len(t, got, 2, "audit endpoint: %d %s", c, b)
}

func TestActionFailures(t *testing.T) {
	e := newEnv(t)
	op := e.token(RoleOperator, nil, false)
	// Mod not connected.
	c, _ := e.do("POST", "/api/v1/instances/alpha/message", op, `{"text":"x"}`)
	require.Equal(t, http.StatusServiceUnavailable, c)
	stop := e.modAnswers("player not online")
	defer stop()
	c, b := e.do("POST", "/api/v1/instances/alpha/players/1/teleport", op, `{"x":1,"z":1}`)
	require.Equal(t, http.StatusUnprocessableEntity, c)
	require.Contains(t, string(b), "player not online", "mod failure: %d %s", c, b)
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/v1/instances/alpha/message", `{"text":""}`, 400},
		{"/api/v1/instances/alpha/message", `{"txt":"x"}`, 400},
		{"/api/v1/instances/alpha/players/1/give", `{"type":"bad class"}`, 400},
		{"/api/v1/instances/alpha/spawn", `{"type":"AKM"}`, 400},
		{"/api/v1/instances/alpha/spawn-vehicle", `{"x":1,"z":2}`, 400},
		{"/api/v1/instances/alpha/message?wait=abc", `{"text":"x"}`, 400},
	} {
		c, _ := e.do("POST", tc.path, op, tc.body)
		require.Equal(t, tc.want, c)
	}
	c, _ = e.do("POST", "/api/v1/instances/alpha/vehicles/v/repair", op, `{"scope":"x","extra":1}`)
	require.Equal(t, 400, c)
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	mod, view := e.token(RoleModerator, nil, false), e.token(RoleViewer, nil, false)
	c, _ := e.do("POST", "/api/v1/instances/alpha/players/7656/message", mod, `{"text":"hi"}`)
	require.Equal(t, 200, c)
	for _, p := range []string{"/message", "/players/7656/teleport", "/players/7656/give", "/players/7656/kill", "/players/7656/heal", "/players/7656/fixgear", "/spawn", "/spawn-vehicle", "/vehicles/v1/repair"} {
		c, _ := e.do("POST", "/api/v1/instances/alpha"+p, mod, `{"text":"x"}`)
		require.Equal(t, 403, c)
	}
	c, _ = e.do("DELETE", "/api/v1/instances/alpha/vehicles/v1", view, "")
	require.Equal(t, 403, c)
	// A non-delegate token cannot change who the audit blames.
	e.do("POST", "/api/v1/instances/alpha/message", e.token(RoleOperator, nil, false), `{"text":"x"}`, "X-Dzo-Actor", "mallory")
	entries, _ := e.srv.Audit.Tail("alpha", 1)
	require.NotContains(t, entries[0].Actor, "mallory")
}

func TestAsyncAndCommandLookup(t *testing.T) {
	e := newEnv(t)
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true})
	op := e.token(RoleOperator, nil, false)
	c, b := e.do("POST", "/api/v1/instances/alpha/message?wait=0", op, `{"text":"x"}`)
	var r ActionResult
	_ = json.Unmarshal(b, &r)
	require.Equal(t, http.StatusAccepted, c, "async: %d %s", c, b)
	require.True(t, r.Pending, "async: %d %s", c, b)
	require.NotEmpty(t, r.ID, "async: %d %s", c, b)
	c, _ = e.do("GET", "/api/v1/instances/alpha/commands/"+r.ID, op, "")
	require.Equal(t, http.StatusAccepted, c)
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Results: []admin.Result{{ID: r.ID, OK: true}}})
	c, b = e.do("GET", "/api/v1/instances/alpha/commands/"+r.ID, op, "")
	r = ActionResult{}
	_ = json.Unmarshal(b, &r)
	require.Equal(t, 200, c, "done lookup: %d %s", c, b)
	require.True(t, r.OK, "done lookup: %d %s", c, b)
	require.False(t, r.Pending, "done lookup: %d %s", c, b)
	c, _ = e.do("GET", "/api/v1/instances/alpha/commands/nope", op, "")
	require.Equal(t, http.StatusNotFound, c, "an unknown or forgotten command id")
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	e.srv.targets = admin.NewLimiter(2, time.Minute)
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true})
	op := e.token(RoleOperator, nil, false)
	var codes []int
	for i := 0; i < 3; i++ {
		c, _ := e.do("POST", "/api/v1/instances/alpha/players/1/message?wait=0", op, `{"text":"x"}`)
		codes = append(codes, c)
	}
	require.Equal(t, 202, codes[0])
	require.Equal(t, 202, codes[1])
	require.Equal(t, http.StatusTooManyRequests, codes[2])
	entries, _ := e.srv.Audit.Tail("alpha", 1)
	require.False(t, entries[0].OK, "denied actions must be audited: %+v", entries[0])
	require.Contains(t, entries[0].Result, "rate limit")
}

func TestStream(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	tok := e.token(RoleViewer, nil, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/instances/alpha/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	ct := resp.Header.Get("Content-Type")
	require.Equal(t, "text/event-stream", ct)
	got := map[string]string{}
	sc := bufio.NewScanner(resp.Body)
	var kind string
	deadline := time.AfterFunc(5*time.Second, cancel)
	defer deadline.Stop()
	pushed := false
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			kind = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			got[kind] = v
			if kind == "events" && !pushed {
				pushed = true
				e.alpha.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: "new"}}, Markers: []admin.Marker{{Layer: "ufo", ID: "9", Visibility: "viewer"}, {Layer: "x", ID: "8"}}})
			}
			if kind == "markers" && strings.Contains(v, `"9"`) {
				break
			}
		}
	}
	require.True(t, strings.Contains(got["players"], "new") || strings.Contains(got["markers"], `"9"`), "stream = %v", got)
	require.NotContains(t, got["markers"], `"8"`, "the stream leaked an admin-only marker to a viewer")
	for _, k := range []string{"status", "players", "vehicles", "markers", "events"} {
		require.NotEmpty(t, got[k])
	}
}

func TestQuantityOf(t *testing.T) {
	zero, five := 0.0, 5.0
	for _, tc := range []struct {
		in   *float64
		want float64
	}{{nil, admin.QuantityDefault}, {&zero, 0}, {&five, 5}} {
		got := quantityOf(tc.in)
		assert.InDelta(t, tc.want, got, 1e-9)
	}
}

func TestMessagePassesNotificationFieldsToTheMod(t *testing.T) {
	e := newEnv(t)
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true, World: "w", Features: []string{"expansion"}})
	op := e.token(RoleOperator, nil, false)
	c, b := e.do("POST", "/api/v1/instances/alpha/message?wait=0", op, `{"text":"x","style":"expansion","notify_type":"baguette","title":"T","seconds":5}`)
	require.Equal(t, 202, c, "%s", b)
	cmds := e.alpha.Sync(admin.SyncRequest{Proto: 1}).Commands
	require.Len(t, cmds, 1)
	require.Equal(t, "baguette", cmds[0].NotifyType)
	require.Equal(t, "T", cmds[0].Title)
	require.Equal(t, 5, cmds[0].Seconds)
	require.Equal(t, "expansion", cmds[0].Style)
}

func TestMessageRConStyleUsesSay(t *testing.T) {
	e := newEnv(t)
	var got []string
	e.srv.Say = func(_ context.Context, instance, steamID, text string) error {
		got = append(got, instance+"|"+steamID+"|"+text)
		return nil
	}
	op := e.token(RoleOperator, nil, false)
	// no mod connected: rcon does not need it
	c, _ := e.do("POST", "/api/v1/instances/alpha/message", op, `{"text":"hello","style":"rcon"}`)
	require.Equal(t, 200, c)
	require.Len(t, got, 1)
	require.Equal(t, "alpha||hello", got[0])
	_, b := e.do("GET", "/api/v1/audit?instance=alpha", e.token(RoleAdmin, nil, false), "")
	assert.Contains(t, string(b), `"target":"all"`)
	e.srv.Say = func(context.Context, string, string, string) error { return errors.New("boom") }
	c, _ = e.do("POST", "/api/v1/instances/alpha/message", op, `{"text":"hello","style":"rcon"}`)
	assert.Equal(t, 502, c)
}

func TestMessageRConStyleWithoutHookIs503(t *testing.T) {
	e := newEnv(t)
	op := e.token(RoleOperator, nil, false)
	c, _ := e.do("POST", "/api/v1/instances/alpha/message", op, `{"text":"hello","style":"rcon"}`)
	require.Equal(t, 503, c)
}

func TestMessageRConStyleToOnePlayerIsABadRequest(t *testing.T) {
	e := newEnv(t)
	e.srv.Say = func(context.Context, string, string, string) error {
		assert.Fail(t, "Say called for one player")
		return nil
	}
	op := e.token(RoleOperator, nil, false)
	c, b := e.do("POST", "/api/v1/instances/alpha/players/7656/message", op, `{"text":"hi","style":"rcon"}`)
	require.Equal(t, 400, c, "rcon to one player = %d %s, want 400", c, b)
	require.Contains(t, string(b), "everyone", "rcon to one player = %d %s, want 400", c, b)
}

// Flags and territories are for operators: below that role neither the markers nor their layers are listed.
func TestFlagMarkersAreOperatorOnly(t *testing.T) {
	e := newEnv(t)
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.4.0", World: "chernarusplus",
		Markers: []admin.Marker{
			{Layer: "Flags", ID: "f1", Shape: "point", Icon: "flag", TTL: 86400, Source: "flags", Visibility: "operator"},
			{Layer: "Territories", ID: "t1", Shape: "circle", Radius: 150, Label: "Camp", Source: "territory", Visibility: "operator"},
		},
		Layers: []admin.Layer{{Name: "Flags", Visibility: "operator"}, {Name: "Territories", Visibility: "operator"}},
	})
	count := func(role Role, path string) int {
		t.Helper()
		c, b := e.do("GET", "/api/v1/instances/alpha/"+path, e.token(role, nil, false), "")
		require.Equal(t, 200, c)
		var list []json.RawMessage
		err := json.Unmarshal(b, &list)
		require.NoError(t, err)
		return len(list)
	}
	for role, want := range map[Role]int{RoleViewer: 0, RoleModerator: 0, RoleOperator: 2, RoleAdmin: 2} {
		got := count(role, "map/markers")
		assert.Equal(t, want, got)
		got = count(role, "layers")
		assert.Equal(t, want, got)
	}
}

// Raising a flag needs the flag.raise permission: moderators have it not, operators do.
func TestRaiseFlagNeedsOperator(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	c, _ := e.do("POST", "/api/v1/instances/alpha/flags/f1/raise", e.token(RoleModerator, nil, false), "")
	require.Equal(t, 403, c)
	c, b := e.do("POST", "/api/v1/instances/alpha/flags/f1/raise", e.token(RoleOperator, nil, false), "")
	require.Equal(t, 200, c, "%s", b)
}

func TestFeedEndpoint(t *testing.T) {
	e := newEnv(t)
	for _, txt := range []string{"a", "b", "c"} {
		e.alpha.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: txt})
	}
	viewer := e.token(RoleViewer, nil, false)
	feed := func(q string) []admin.FeedEvent {
		code, body := e.do("GET", "/api/v1/instances/alpha/feed"+q, viewer, "")
		require.Equal(t, 200, code, string(body))
		var out []admin.FeedEvent
		require.NoError(t, json.Unmarshal(body, &out))
		return out
	}
	require.Len(t, feed(""), 3)
	got := feed("?after=1&limit=1")
	require.Len(t, got, 1)
	require.Equal(t, "b", got[0].Text)
	got = feed("?limit=2")
	require.Equal(t, "b", got[0].Text, "no after: the newest ones")
	require.Empty(t, feed("?after=3"))
	for _, bad := range []string{"?after=x", "?after=-1", "?limit=x", "?limit=-2"} {
		code, _ := e.do("GET", "/api/v1/instances/alpha/feed"+bad, viewer, "")
		require.Equal(t, 400, code, bad)
	}
	onlyBeta := e.token(RoleViewer, []string{"beta"}, false)
	code, _ := e.do("GET", "/api/v1/instances/alpha/feed", onlyBeta, "")
	require.Equal(t, 404, code)
	code, _ = e.do("GET", "/api/v1/instances/alpha/feed", "", "")
	require.Equal(t, 401, code)
}

func TestStreamOnlyFeed(t *testing.T) {
	e := newEnv(t)
	e.modAnswers("")
	tok := e.token(RoleViewer, nil, false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/instances/alpha/stream?only=feed", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	e.alpha.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: "later"})
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: "x"}}}) // not a feed event
	e.alpha.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: "last"})
	var kinds, data []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			kinds = append(kinds, v)
		}
		if v, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			data = append(data, v)
			if strings.Contains(v, "last") {
				break
			}
		}
	}
	require.Equal(t, []string{"feed", "feed"}, kinds)
	require.Contains(t, data[0], "later")
}

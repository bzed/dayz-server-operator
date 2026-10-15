// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

type env struct {
	t     *testing.T
	srv   *Server
	ts    *httptest.Server
	hubs  *admin.Registry
	alpha *admin.Hub
}

func newEnv(t *testing.T) *env {
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
	s, _, err := e.srv.Tokens.Create("t-"+string(role), role, instances, delegate, time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
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
	if err != nil {
		e.t.Fatal(err)
	}
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
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for ctx.Err() == nil {
			rep := e.alpha.Sync(admin.SyncRequest{Proto: 1})
			var res []admin.Result
			for _, c := range rep.Commands {
				res = append(res, admin.Result{ID: c.ID, OK: failWith == "", Message: failWith})
			}
			if len(res) > 0 {
				e.alpha.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return cancel
}

func TestAuthAndInfo(t *testing.T) {
	e := newEnv(t)
	if c, _ := e.do("GET", "/api/v1", "", ""); c != 401 {
		t.Fatalf("no token: %d", c)
	}
	if c, _ := e.do("GET", "/api/v1", "dzo_nope", ""); c != 401 {
		t.Fatalf("bad token: %d", c)
	}
	tok := e.token(RoleViewer, nil, false)
	c, b := e.do("GET", "/api/v1", tok, "")
	var info Info
	if err := json.Unmarshal(b, &info); err != nil || c != 200 || info.Installation != "test-site" || info.APIVersion != 1 || info.Role != RoleViewer {
		t.Fatalf("info = %s (%d)", b, c)
	}
	if c, _ := e.do("GET", "/healthz", "", ""); c != 200 {
		t.Fatal("healthz must be public")
	}
}

func TestInstanceScope(t *testing.T) {
	e := newEnv(t)
	tok := e.token(RoleViewer, []string{"beta"}, false)
	_, b := e.do("GET", "/api/v1/instances", tok, "")
	var list []admin.Status
	_ = json.Unmarshal(b, &list)
	if len(list) != 1 || list[0].Instance != "beta" {
		t.Fatalf("list = %s", b)
	}
	if c, _ := e.do("GET", "/api/v1/instances/alpha/players", tok, ""); c != 404 {
		t.Fatalf("out of scope: %d", c)
	}
	if c, _ := e.do("GET", "/api/v1/instances/zzz", tok, ""); c != 404 {
		t.Fatalf("unknown: %d", c)
	}
	if c, _ := e.do("GET", "/api/v1/instances/beta", tok, ""); c != 200 {
		t.Fatalf("in scope: %d", c)
	}
}

func TestReadEndpointsAndVisibility(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	viewer, admn := e.token(RoleViewer, nil, false), e.token(RoleAdmin, nil, false)
	get := func(tok, path string, v any) {
		t.Helper()
		c, b := e.do("GET", path, tok, "")
		if c != 200 {
			t.Fatalf("%s: %d %s", path, c, b)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	var players []admin.Player
	get(viewer, "/api/v1/instances/alpha/players", &players)
	var vehicles []admin.Vehicle
	get(viewer, "/api/v1/instances/alpha/vehicles", &vehicles)
	var events []admin.Event
	get(viewer, "/api/v1/instances/alpha/events", &events)
	if len(players) != 1 || len(vehicles) != 1 || len(events) != 1 {
		t.Fatal("state endpoints")
	}
	var vm, am []admin.Marker
	get(viewer, "/api/v1/instances/alpha/map/markers", &vm)
	get(admn, "/api/v1/instances/alpha/map/markers", &am)
	if len(vm) != 1 || len(am) != 2 {
		t.Fatalf("visibility: viewer %d admin %d", len(vm), len(am))
	}
	get(admn, "/api/v1/instances/alpha/map/markers?layer=secret", &am)
	if len(am) != 1 {
		t.Fatal("layer filter")
	}
	var vl, al []admin.Layer
	get(viewer, "/api/v1/instances/alpha/layers", &vl)
	get(admn, "/api/v1/instances/alpha/layers", &al)
	if len(vl) != 1 || len(al) != 2 {
		t.Fatalf("layers: viewer %d admin %d", len(vl), len(al))
	}
	var st admin.Status
	get(viewer, "/api/v1/instances/alpha", &st)
	if !st.Connected {
		t.Fatal("status")
	}
	var names []string
	get(viewer, "/api/v1/instances/alpha/types?q=x&limit=3", &names)
	if names == nil {
		t.Fatal("types must be a list")
	}
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
		{"POST", "/api/v1/instances/alpha/vehicles/v1/repair", `{"scope":"wheels"}`},
		{"POST", "/api/v1/instances/alpha/vehicles/v1/repair", ``},
		{"POST", "/api/v1/instances/alpha/vehicles/v1/flip", ``},
		{"DELETE", "/api/v1/instances/alpha/vehicles/v1?force=1", ``},
	}
	for _, tc := range cases {
		c, b := e.do(tc.method, tc.path, op, tc.body, "X-Dzo-Actor", "alice", "X-Dzo-Source", "web")
		var r ActionResult
		if err := json.Unmarshal(b, &r); err != nil || c != 200 || !r.OK || r.ID == "" {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, c, b)
		}
	}
	entries, _ := e.srv.Audit.Tail("alpha", 100)
	if len(entries) != len(cases) || entries[0].Actor != "t-operator:alice" || entries[0].Source != "web" || !entries[0].OK || entries[0].Action != "vehicle_delete" {
		t.Fatalf("audit = %+v", entries)
	}
	// The audit endpoint is admin only.
	if c, _ := e.do("GET", "/api/v1/audit", op, ""); c != 403 {
		t.Fatalf("operator reading audit: %d", c)
	}
	c, b := e.do("GET", "/api/v1/audit?instance=alpha&limit=2", e.token(RoleAdmin, nil, false), "")
	var got []admin.AuditEntry
	if err := json.Unmarshal(b, &got); err != nil || c != 200 || len(got) != 2 {
		t.Fatalf("audit endpoint: %d %s", c, b)
	}
}

func TestActionFailures(t *testing.T) {
	e := newEnv(t)
	op := e.token(RoleOperator, nil, false)
	// Mod not connected.
	if c, _ := e.do("POST", "/api/v1/instances/alpha/message", op, `{"text":"x"}`); c != http.StatusServiceUnavailable {
		t.Fatalf("offline: %d", c)
	}
	stop := e.modAnswers("player not online")
	defer stop()
	c, b := e.do("POST", "/api/v1/instances/alpha/players/1/teleport", op, `{"x":1,"z":1}`)
	if c != http.StatusUnprocessableEntity || !strings.Contains(string(b), "player not online") {
		t.Fatalf("mod failure: %d %s", c, b)
	}
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/v1/instances/alpha/message", `{"text":""}`, 400},
		{"/api/v1/instances/alpha/message", `{"txt":"x"}`, 400},
		{"/api/v1/instances/alpha/players/1/give", `{"type":"bad class"}`, 400},
		{"/api/v1/instances/alpha/message?wait=abc", `{"text":"x"}`, 400},
	} {
		if c, b := e.do("POST", tc.path, op, tc.body); c != tc.want {
			t.Fatalf("%s: %d %s", tc.path, c, b)
		}
	}
	if c, _ := e.do("POST", "/api/v1/instances/alpha/vehicles/v/repair", op, `{"scope":"x","extra":1}`); c != 400 {
		t.Fatalf("unknown field: %d", c)
	}
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	mod, view := e.token(RoleModerator, nil, false), e.token(RoleViewer, nil, false)
	if c, _ := e.do("POST", "/api/v1/instances/alpha/players/7656/message", mod, `{"text":"hi"}`); c != 200 {
		t.Fatalf("moderator message: %d", c)
	}
	for _, p := range []string{"/message", "/players/7656/teleport", "/players/7656/give", "/vehicles/v1/repair"} {
		if c, _ := e.do("POST", "/api/v1/instances/alpha"+p, mod, `{"text":"x"}`); c != 403 {
			t.Fatalf("moderator %s: %d", p, c)
		}
	}
	if c, _ := e.do("DELETE", "/api/v1/instances/alpha/vehicles/v1", view, ""); c != 403 {
		t.Fatalf("viewer delete: %d", c)
	}
	// A non-delegate token cannot change who the audit blames.
	e.do("POST", "/api/v1/instances/alpha/message", e.token(RoleOperator, nil, false), `{"text":"x"}`, "X-Dzo-Actor", "mallory")
	entries, _ := e.srv.Audit.Tail("alpha", 1)
	if strings.Contains(entries[0].Actor, "mallory") {
		t.Fatalf("actor spoofed: %q", entries[0].Actor)
	}
}

func TestAsyncAndCommandLookup(t *testing.T) {
	e := newEnv(t)
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true})
	op := e.token(RoleOperator, nil, false)
	c, b := e.do("POST", "/api/v1/instances/alpha/message?wait=0", op, `{"text":"x"}`)
	var r ActionResult
	_ = json.Unmarshal(b, &r)
	if c != http.StatusAccepted || !r.Pending || r.ID == "" {
		t.Fatalf("async: %d %s", c, b)
	}
	if c, _ := e.do("GET", "/api/v1/instances/alpha/commands/"+r.ID, op, ""); c != http.StatusAccepted {
		t.Fatalf("pending lookup: %d", c)
	}
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Results: []admin.Result{{ID: r.ID, OK: true}}})
	c, b = e.do("GET", "/api/v1/instances/alpha/commands/"+r.ID, op, "")
	r = ActionResult{}
	_ = json.Unmarshal(b, &r)
	if c != 200 || !r.OK || r.Pending {
		t.Fatalf("done lookup: %d %s", c, b)
	}
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
	if codes[0] != 202 || codes[1] != 202 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v", codes)
	}
	entries, _ := e.srv.Audit.Tail("alpha", 1)
	if entries[0].OK || !strings.Contains(entries[0].Result, "rate limit") {
		t.Fatalf("denied actions must be audited: %+v", entries[0])
	}
}

func TestStream(t *testing.T) {
	e := newEnv(t)
	defer e.modAnswers("")()
	tok := e.token(RoleViewer, nil, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/instances/alpha/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	got := map[string]string{}
	sc := bufio.NewScanner(resp.Body)
	var kind string
	deadline := time.After(5 * time.Second)
	go func() {
		<-deadline
		cancel()
	}()
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
	if !strings.Contains(got["players"], "new") && !strings.Contains(got["markers"], `"9"`) {
		t.Fatalf("stream = %v", got)
	}
	if strings.Contains(got["markers"], `"8"`) {
		t.Fatal("the stream leaked an admin-only marker to a viewer")
	}
	for _, k := range []string{"status", "players", "vehicles", "markers", "events"} {
		if got[k] == "" {
			t.Fatalf("missing initial %s event", k)
		}
	}
}

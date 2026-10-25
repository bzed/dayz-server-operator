// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/geoip"
	"github.com/bzed/dayz-server-operator/internal/geoip/geoiptest"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type env struct {
	t      *testing.T
	web    *httptest.Server
	srv    *Server
	hub    *admin.Hub
	api    *api.Server
	assets string

	mu   sync.Mutex
	sent []string // the kinds of the commands answerCommands has seen, in order
}

func newEnv(t *testing.T, role api.Role) *env {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hub := hubs.Add(admin.NewHub("alpha"))
	apiSrv := api.New("site-a", "v0", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(t.Context(), filepath.Join(dir, "dzo.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	apiSrv.Moderation = webMod{st}
	ats := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ats.Close)
	secret, _, err := apiSrv.Tokens.Create("web", role, nil, true, time.Hour)
	require.NoError(t, err)
	// A second working backend, with its own bans, for the fan-out of a ban form.
	otherDir := t.TempDir()
	otherHubs := admin.NewRegistry(otherDir)
	otherHubs.Add(admin.NewHub("alpha"))
	otherAPI := api.New("site-b", "v0", otherHubs, api.NewTokenStore(filepath.Join(otherDir, "t.json")), admin.NewAudit(filepath.Join(otherDir, "a.jsonl")))
	otherStore, err := moderation.Open(t.Context(), filepath.Join(otherDir, "dzo.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = otherStore.Close() })
	otherAPI.Moderation = webMod{otherStore}
	otherTS := httptest.NewServer(otherAPI.Handler())
	t.Cleanup(otherTS.Close)
	otherSecret, _, err := otherAPI.Tokens.Create("web", role, nil, true, time.Hour)
	require.NoError(t, err)
	assets, docs := filepath.Join(dir, "assets"), filepath.Join(dir, "docs")
	for _, d := range []string{filepath.Join(assets, "htmx"), docs} {
		require.NoError(t, os.MkdirAll(d, 0o750))
	}
	_ = os.WriteFile(filepath.Join(assets, "htmx", "htmx.min.js"), []byte("//htmx"), 0o600)
	flags := filepath.Join(dir, "flags")
	require.NoError(t, os.MkdirAll(flags, 0o750))
	_ = os.WriteFile(filepath.Join(flags, "de.svg"), []byte("<svg/>"), 0o600)
	apiSrv.GeoIP = geoiptest.Legacy(t, map[string]string{"198.51.100.0/24": "DE"})
	_ = os.WriteFile(filepath.Join(docs, "index.html"), []byte("<h1>docs</h1>"), 0o600)
	srv, err := New([]*Backend{
		{Name: "main", Client: apiclient.New(ats.URL, secret)},
		{Name: "other", Client: apiclient.New(otherTS.URL, otherSecret)},
		{Name: "down", Client: apiclient.New("http://127.0.0.1:1", "x")},
	}, Options{Version: "v0", Assets: assets, DocsDir: docs, FlagsDir: flags, UserHeader: "X-Forwarded-User"})
	require.NoError(t, err)
	ws := httptest.NewServer(srv.Handler())
	t.Cleanup(ws.Close)
	hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.1.0", World: "enoch",
		Players:  []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, X: 1, Z: 2}},
		Vehicles: []admin.Vehicle{{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback", Health: 0.5, Fuel: 0.25}, {ID: "5.6.7.8", Kind: "motorbike", Type: "Motorbike_01"}, {ID: "9.9.9.9", Kind: "bicycle", Type: "Old_Bike_Red"}, {ID: "b.b.b.b", Kind: "boat", Type: "Boat_01_Orange"}, {ID: "c.c.c.c", Kind: "car", Type: "Sedan_02", Owner: "Rita", Covered: true}},
		Types:    &admin.TypesChunk{Hash: "h", Total: 2, Names: []string{"AKM", "AK74"}},
	})
	return &env{t: t, web: ws, srv: srv, hub: hub, api: apiSrv, assets: assets}
}

func (e *env) get(path string) (int, string, http.Header) {
	e.t.Helper()
	resp, err := http.Get(e.web.URL + path)
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (e *env) post(path string, form url.Values, hdr map[string]string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.web.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", e.srv.csrf)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// commandsOf counts the commands of a kind that answerCommands has answered so far.
func (e *env) commandsOf(kind string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Count(strings.Join(e.sent, ","), kind)
}

func (e *env) answerCommands(ok bool, msg string) func() {
	ctx, cancel := context.WithCancel(e.t.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			var res []admin.Result
			for _, c := range e.hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
				e.mu.Lock()
				e.sent = append(e.sent, c.Kind)
				e.mu.Unlock()
				res = append(res, admin.Result{ID: c.ID, OK: admin.Flag(ok), Message: msg})
			}
			if len(res) > 0 {
				e.hub.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

func TestDashboard(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body, hdr := e.get("/")
	require.Equal(t, 200, code, "dashboard: %s", body)
	require.Contains(t, body, "site-a")
	require.Contains(t, body, `href="/b/main/i/alpha"`)
	require.Contains(t, body, "connected")
	// Every page loads the icon font, and the buttons keep their text next to the icon.
	// The tab has an icon: an SVG, and a PNG for the browsers that cannot take one.
	for _, f := range []string{"favicon.svg", "favicon-32.png", "apple-touch-icon.png"} {
		assert.Contains(t, body, "/static/"+f, "the page does not link /static/%s", f)
		c, _, h := e.get("/static/" + f)
		assert.Equal(t, 200, c, "/static/%s", f)
		assert.True(t, strings.HasPrefix(h.Get("Content-Type"), "image/"), "/static/%s: %q", f, h.Get("Content-Type"))
	}
	require.Contains(t, body, "/vendor/fork-awesome/css/fork-awesome.css", "dashboard icons")
	require.Contains(t, body, `fa-map`, "dashboard icons")
	require.Contains(t, body, `aria-label="Map of alpha"`, "dashboard icons")
	require.True(t, strings.Contains(body, "api: ") || strings.Contains(body, "down"), "an unreachable backend must be shown with its error")
	require.Contains(t, hdr.Get("Content-Security-Policy"), "default-src 'self'")
	require.Equal(t, "DENY", hdr.Get("X-Frame-Options"))
	require.Equal(t, "nosniff", hdr.Get("X-Content-Type-Options"))
	require.Contains(t, body, e.srv.csrf, "page must carry the CSRF token")
	c, _, _ := e.get("/nope")
	require.Equal(t, 404, c, "unknown path")
}

func TestInstanceAndFragments(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha")
	for _, want := range []string{"dzo-admin connected", "world enoch", `hx-get="/b/main/i/alpha/players"`} {
		require.Contains(t, body, want, "instance page")
	}
	_, body, _ = e.get("/b/main/i/alpha/players")
	require.Contains(t, body, "&lt;b&gt;alice&lt;/b&gt;", "player names must be escaped")
	require.NotContains(t, body, "<b>alice", "player names must be escaped")
	_, body, _ = e.get("/b/main/i/alpha/vehicles")
	for _, want := range []string{"OffroadHatchback", "50%", "/vehicles/1.2.3.4/panel", "Motorbike_01", "fa-motorcycle", "Old_Bike_Red", "fa-bicycle", "Boat_01_Orange", "fa-ship", "Sedan_02 <em>(covered)</em>", "Rita"} {
		require.Contains(t, body, want, "vehicles")
	}
	require.NotContains(t, body, "/vehicles/c.c.c.c/panel", "vehicles")
	_, body, _ = e.get("/b/main/i/alpha/players/7656/panel")
	require.Contains(t, body, `hx-post="/b/main/i/alpha/players/7656/teleport"`, "panel")
	_, body, _ = e.get("/b/main/i/alpha/vehicles/1.2.3.4/panel")
	require.Contains(t, body, `hx-post="/b/main/i/alpha/vehicles/1.2.3.4/flip"`, "vehicle panel")
	require.Contains(t, body, `"scope": "missing"`, "vehicle panel")
	_, body, _ = e.get("/b/main/i/alpha/vehicles/nope/panel")
	require.Contains(t, body, "not found", "panel for an absent vehicle")
	_, body, _ = e.get("/b/main/i/alpha/players/9999/panel")
	require.Contains(t, body, "not online", "panel for an absent player")
	_, body, _ = e.get("/b/main/i/alpha/types?type=ak")
	require.Contains(t, body, `<option value="AKM">`, "types")
	_, body, _ = e.get("/b/main/i/alpha/vehicle-types?type=zz")
	require.NotContains(t, body, "AKM", "vehicle types")
	_, body, _ = e.get("/b/main/i/zzz")
	require.Contains(t, body, "unknown instance")
	_, body, _ = e.get("/b/main/i/zzz/players")
	require.Contains(t, body, "unknown instance", "fragment error")
	for _, p := range []string{"/b/nope/i/alpha", "/b/nope/audit", "/b/nope/i/alpha/map", "/b/nope/i/alpha/players", "/b/nope/i/alpha/vehicles", "/b/nope/i/alpha/types", "/b/nope/i/alpha/stream", "/b/nope/i/alpha/players/1/panel"} {
		c, _, _ := e.get(p)
		require.Equal(t, 404, c, p)
	}
}

func TestMapAndAudit(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/map")
	require.Contains(t, body, `data-size="12800"`, "map page")
	require.Contains(t, body, `data-stream="/b/main/i/alpha/stream"`, "map page")
	require.NotContains(t, body, "<script>", "map page")
	require.Equal(t, defaultMapSize, mapSize("unknown"))
	require.Equal(t, 15360, mapSize("ChernarusPlus"))
	c, js, _ := e.get("/static/map.js")
	require.Equal(t, 200, c, "map.js")
	require.Contains(t, js, "EventSource", "map.js")
	// Flags show the time they have left, Expansion territories are drawn thin.
	for _, want := range []string{"function ttlText", "Raise flag", "/raise", "goes down in", "goes down now", "m.source === 'territory'"} {
		_, js, _ := e.get("/static/map.js")
		assert.Contains(t, js, want, "map.js lacks %q", want)
	}
	c, svg, _ := e.get("/static/icons/helicopter.svg")
	require.Equal(t, 200, c, "the helicopter icon is not served")
	require.Contains(t, svg, "Font Awesome Free", "the helicopter icon is not served")
	c, _, _ = e.get("/static/app.css")
	require.Equal(t, 200, c, "app.css")
	_, body, _ = e.get("/b/main/audit")
	require.Contains(t, body, "No entries", "empty audit")
	e2 := newEnv(t, api.RoleViewer)
	_, body, _ = e2.get("/b/main/audit")
	require.Contains(t, body, "audit.view", "a viewer must see why the audit is closed")
}

func TestVendorAndDocs(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	c, b, _ := e.get("/vendor/htmx/htmx.min.js")
	require.Equal(t, 200, c, "vendor")
	require.Equal(t, "//htmx", b, "vendor")
	c, b, _ = e.get("/docs/")
	require.Equal(t, 200, c, "docs")
	require.Contains(t, b, "docs")
	srv, _ := New([]*Backend{{Name: "x", Client: apiclient.New("http://127.0.0.1:1", "t")}}, Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	r, err := http.Get(ts.URL + "/docs/")
	require.NoError(t, err)
	_ = r.Body.Close()
	require.Equal(t, 404, r.StatusCode, "docs without a dir")
	_, err = New(nil, Options{})
	require.Error(t, err, "no backends must fail")
}

func TestActions(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	defer e.answerCommands(true, "")()
	for path, form := range map[string]url.Values{
		"/b/main/i/alpha/message":                 {"text": {"hi"}, "style": {"chat"}},
		"/b/main/i/alpha/players/7656/message":    {"text": {"hello"}},
		"/b/main/i/alpha/players/7656/teleport":   {"x": {"10"}, "z": {"20"}},
		"/b/main/i/alpha/players/7656/heal":       {},
		"/b/main/i/alpha/players/7656/fixgear":    {},
		"/b/main/i/alpha/players/7656/kill":       {},
		"/b/main/i/alpha/players/7656/give":       {"type": {"AKM"}, "quantity": {"1"}, "health": {"0.5"}, "target": {"hands"}},
		"/b/main/i/alpha/spawn":                   {"type": {"AKM"}, "quantity": {"20"}, "x": {"10"}, "z": {"20"}},
		"/b/main/i/alpha/spawn-vehicle":           {"type": {"AKM"}, "x": {"10"}, "z": {"20"}},
		"/b/main/i/alpha/vehicles/1.2.3.4/repair": {"scope": {"missing"}},
		"/b/main/i/alpha/vehicles/1.2.3.4/flip":   {},
		"/b/main/i/alpha/vehicles/1.2.3.4/move":   {"x": {"10"}, "z": {"20"}},
		"/b/main/i/alpha/flags/f1.2/raise":        {},
		"/b/main/i/alpha/vehicles/1.2.3.4/delete": {"force": {"1"}},
	} {
		code, body := e.post(path, form, map[string]string{"X-Forwarded-User": "alice@example.invalid"})
		require.Equal(t, 200, code, "%s: %s", path, body)
		require.Contains(t, body, "flash ok", path)
	}
	entries, _ := e.api.Audit.Tail("alpha", 1)
	require.Len(t, entries, 1)
	require.Equal(t, "web:alice@example.invalid", entries[0].Actor)
	require.Equal(t, "web", entries[0].Source)
	// Without the proxy header the actor is plain "web".
	e.post("/b/main/i/alpha/message", url.Values{"text": {"x"}}, nil)
	entries, _ = e.api.Audit.Tail("alpha", 1)
	require.NotEmpty(t, entries)
	require.Equal(t, "web:web", entries[0].Actor)
}

func TestQuantityMaxAndInvalid(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	defer e.answerCommands(true, "")()
	for _, q := range []string{"max", "MAX", "", " 5 "} {
		_, body := e.post("/b/main/i/alpha/spawn", url.Values{"type": {"AKM"}, "quantity": {q}, "x": {"1"}, "z": {"2"}}, nil)
		assert.Contains(t, body, "flash ok", "quantity %q", q)
	}
	for _, q := range []string{"lots", "-3", "NaN", "Inf"} {
		_, body := e.post("/b/main/i/alpha/players/7656/give", url.Values{"type": {"AKM"}, "quantity": {q}}, nil)
		assert.Contains(t, body, "quantity must be a number, default or max", "quantity %q", q)
	}
}

func TestActionErrors(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	_, body := e.post("/b/main/i/alpha/message", url.Values{"text": {"x"}}, nil)
	require.Contains(t, body, "flash err", "forbidden")
	require.Contains(t, body, "lacks", "forbidden")
	e2 := newEnv(t, api.RoleAdmin)
	stop := e2.answerCommands(false, "player not online")
	defer stop()
	_, body = e2.post("/b/main/i/alpha/players/1/teleport", url.Values{"x": {"1"}, "z": {"1"}}, nil)
	require.Contains(t, body, "refused: player not online")
}

func TestCSRF(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	defer e.answerCommands(true, "")()
	post := func(hdr map[string]string, token string) int {
		req, _ := http.NewRequest(http.MethodPost, e.web.URL+"/b/main/i/alpha/message", strings.NewReader("text=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, 403, post(nil, ""), "no token")
	require.Equal(t, 403, post(nil, "wrong"), "wrong token")
	require.Equal(t, 403, post(map[string]string{"Origin": "https://evil.example"}, e.srv.csrf), "foreign origin")
	require.NotEqual(t, 403, post(map[string]string{"Origin": e.web.URL}, e.srv.csrf), "same origin must pass")
	require.Equal(t, 403, post(map[string]string{"Origin": "::bad"}, e.srv.csrf), "unparsable origin")
}

func TestStreamRelay(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.web.URL+"/b/main/i/alpha/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	sc := bufio.NewScanner(resp.Body)
	seen := map[string]bool{}
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			seen[v] = true
			if seen["players"] && seen["vehicles"] && seen["markers"] {
				break
			}
		}
	}
	require.True(t, seen["players"] && seen["status"], "relayed events = %v", seen)
	cancel()
	// A backend that is down answers 502.
	c, _, _ := e.get("/b/down/i/alpha/stream")
	require.Equal(t, http.StatusBadGateway, c, "down backend")
}

func TestMapTiles(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	_, body, _ := e.get("/b/main/i/alpha/map")
	require.NotContains(t, body, "data-tiles", "a map without tiles must not announce any")
	_, body, _ = e.get("/b/main/i/alpha/map")
	require.Contains(t, body, "no map imagery", "a map without tiles says how to get them")
	require.Contains(t, body, "dzo map tiles build enoch", "a map without tiles says how to get them")
	root := t.TempDir()
	set := filepath.Join(root, "enoch", "abcdef0123456789")
	require.NoError(t, os.MkdirAll(filepath.Join(set, "0", "0"), 0o750))
	_ = os.WriteFile(filepath.Join(set, "metadata.json"), []byte(`{"map":"enoch","hash":"abcdef0123456789"}`), 0o600)
	_ = os.WriteFile(filepath.Join(set, "0", "0", "0.jpg"), []byte("JPEG"), 0o600)
	require.NoError(t, os.Symlink("abcdef0123456789", filepath.Join(root, "enoch", "current")))
	e.api.TilesDir = root

	_, body, _ = e.get("/b/main/i/alpha/map")
	require.NotContains(t, body, "no map imagery", "a map with tiles needs no hint")
	require.Contains(t, body, `data-tiles="/b/main/tiles/enoch"`, "map page")
	require.Contains(t, body, `data-meta="{&#34;map&#34;:&#34;enoch&#34;`, "map page")
	code, b, hdr := e.get("/b/main/tiles/enoch/abcdef0123456789/0/0/0.jpg")
	require.Equal(t, 200, code, "tile: %v", hdr)
	require.Equal(t, "JPEG", b, "tile")
	require.Equal(t, "image/jpeg", hdr.Get("Content-Type"))
	require.Contains(t, hdr.Get("Cache-Control"), "immutable")
	for _, p := range []string{"/b/nope/tiles/enoch/metadata.json", "/b/main/tiles/enoch/abcdef0123456789/0/0/0.png", "/b/main/tiles/enoch/abcdef0123456789/0/0/9.jpg"} {
		code, _, _ := e.get(p)
		assert.Equal(t, 404, code, p)
	}
}

// The player panel acts on the player; banning is a link to the ban site's form, not fields in the panel.
func TestPlayerPanelLinksToTheBanSite(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	require.Contains(t, body, `href="/b/main/bans/new?steam_id=7656&amp;instance=alpha"`, "no link to the ban form")
	for _, field := range []string{`name="duration"`, `name="scope"`, `name="message"`} { // the kick dialog has its own reason
		assert.NotContains(t, body, field, "the panel has the ban field")
	}
	for _, group := range []string{"Tell", "Move", "Remove", "Give"} {
		assert.Contains(t, body, `class="gname">`+group, "no %q group", group)
	}
}

// The name cell keeps room for a country flag and offers the connection info.
func TestPlayerRowHasFlagSlotAndInfoButton(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players")
	for _, want := range []string{`class="flag"`, `data-info`, `class="pop"`, `/players/7656/info`} {
		assert.Contains(t, body, want, "player table lacks %s", want)
	}
}

func TestPlayerInfoShowsTheSteamIDAndOnlyTheCurrentAddress(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	guid, _ := moderation.DeriveGUID(webSteam)
	st := e.api.Moderation.(webMod).st
	for i, ip := range []string{"192.0.2.7", "198.51.100.1"} {
		require.NoError(t, st.Seen(t.Context(), "alpha", guid, ip, "Alice <i>", time.Now().Add(time.Duration(i)*time.Hour)))
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "Alice <i>", Alive: true}}})
	_, body, _ := e.get("/b/main/i/alpha/players/" + webSteam + "/info")
	require.Contains(t, body, webSteam, "info")
	require.Contains(t, body, "198.51.100.1", "info")
	require.NotContains(t, body, "192.0.2.7", "info")
	require.NotContains(t, body, "<i>", "info")
	code, _, _ := e.get("/b/nope/i/alpha/players/" + webSteam + "/info")
	require.Equal(t, 404, code, "unknown backend")
}

func TestPlayerInfoWithoutThePlayerIPPermissionHasNoAddress(t *testing.T) {
	e := newEnv(t, api.RoleModerator)
	guid, _ := moderation.DeriveGUID(webSteam)
	_ = e.api.Moderation.(webMod).st.Seen(t.Context(), "alpha", guid, "192.0.2.7", "alice", time.Now())
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "alice", Alive: true}}})
	_, body, _ := e.get("/b/main/i/alpha/players/" + webSteam + "/info")
	require.NotContains(t, body, "192.0.2.7", "info")
	require.Contains(t, body, webSteam, "info")
}

func TestPlayerTableHasNoSteamIDColumn(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players")
	require.NotContains(t, body, "Steam ID", "the table shows the Steam ID")
	require.NotContains(t, body, "<td>7656</td>", "the table shows the Steam ID")
}

func TestKickDialogAndAction(t *testing.T) {
	e := newEnv(t, api.RoleModerator)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	for _, want := range []string{`data-dialog="kick-7656"`, `<dialog id="kick-7656"`, `hx-post="/b/main/i/alpha/players/7656/kick"`, `name="reason" required maxlength="64"`} {
		assert.Contains(t, body, want, "panel lacks %s", want)
	}
	resetKicked()
	_, out := e.post("/b/main/i/alpha/players/7656/kick", url.Values{"reason": {" afk "}}, nil)
	require.Contains(t, out, "done", "kick")
	require.Equal(t, []string{"alpha 7656 afk"}, kickedList())
	_, out = e.post("/b/main/i/alpha/players/7656/kick", url.Values{"reason": {strings.Repeat("r", 65)}}, nil)
	require.Contains(t, out, "64", "long reason")
}

func TestPlayerPanelHasHealFixGearAndKillWithConfirmation(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	for _, want := range []string{`hx-post="/b/main/i/alpha/players/7656/heal"`, `hx-post="/b/main/i/alpha/players/7656/fixgear"`,
		`hx-post="/b/main/i/alpha/players/7656/kill"`, `hx-confirm="Kill &lt;b&gt;alice&lt;/b&gt;?`} {
		assert.Contains(t, body, want, "panel lacks %s", want)
	}
}

func seenFrom(t testing.TB, e *env, ip string) {
	t.Helper()
	guid, _ := moderation.DeriveGUID(webSteam)
	require.NoError(t, e.api.Moderation.(webMod).st.Seen(t.Context(), "alpha", guid, ip, "alice", time.Now()))
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "alice", Alive: true}, {SteamID: "7656", Name: "bob", Alive: true}}})
}

func TestPlayerListShowsTheFlag(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	seenFrom(t, e, "198.51.100.7")
	_, body, _ := e.get("/b/main/i/alpha/players")
	require.Contains(t, body, `<span class="flag"><img src="/flags/DE.svg" alt="DE"`, "no flag")
	require.Equal(t, 1, strings.Count(body, "<img"), "a player without a country got a flag: %s", body)
}

func TestPlayerInfoShowsCountryButCityAndProviderNeedThePlayerIP(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	seenFrom(t, e, "198.51.100.7")
	_, body, _ := e.get("/b/main/i/alpha/players/" + webSteam + "/info")
	require.Contains(t, body, "<dt>Country</dt>", "no country")
	require.Contains(t, body, "/flags/DE.svg", "no country")
	require.Contains(t, body, "</span>DE</dd>", "no country")
	// The legacy database holds no city, location or provider, so none are shown.
	for _, no := range []string{"<dt>City</dt>", "<dt>Location</dt>", "<dt>Provider</dt>"} {
		assert.NotContains(t, body, no, "with a country-only database")
	}
}

func TestInfoRowsForCityLocationAndProvider(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	lat, lon := 48.137, 11.575
	var out strings.Builder
	err := e.srv.tmpl.ExecuteTemplate(&out, "playerinfo", playerInfoView{Name: "<x>", SteamID: webSteam,
		Geo: geoip.Info{Country: "DE", CountryName: "Germany", City: "München", Lat: &lat, Lon: &lon, ASN: 6805, ASOrg: "Telefonica <Germany>"}})
	require.NoError(t, err)
	body := out.String()
	for _, want := range []string{"Germany (DE)", "<dd>München</dd>", "48.14, 11.57", "approximate", "AS6805 Telefonica &lt;Germany&gt;", "&lt;x&gt;"} {
		assert.Contains(t, body, want)
	}
}

func TestFlagsAreServedOnlyForTwoLetterCodes(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	code, body, h := e.get("/flags/DE.svg")
	require.Equal(t, 200, code, "de")
	require.Equal(t, "<svg/>", body, "de")
	require.Contains(t, h.Get("Cache-Control"), "max-age", "de")
	for _, p := range []string{"/flags/xx.svg", "/flags/d1.svg", "/flags/deu.svg", "/flags/%2e%2e%2fa"} {
		code, _, _ := e.get(p)
		assert.Contains(t, []int{404, 400, 301}, code, p)
	}
	e.srv.opts.FlagsDir = ""
	code, _, _ = e.get("/flags/de.svg")
	require.Equal(t, 404, code, "no flags dir")
}

func TestInstancePageOffersExpansionOnlyWithTheFeature(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	types := []string{"toast", "baguette", "activity", "killfeed", "market", "garage"}
	for _, path := range []string{"/b/main/i/alpha", "/b/main/i/alpha/players/7656/panel"} {
		_, body, _ := e.get(path)
		assert.NotContains(t, body, `value="expansion"`, "%s offers expansion without the feature", path)
		assert.NotContains(t, body, `name="notify_type"`, "%s offers expansion without the feature", path)
		for _, want := range []string{`value="notification"`, `name="title"`, `name="seconds"`} {
			assert.Contains(t, body, want, "%s lacks the notification fields", path)
		}
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, World: "enoch", Features: []string{"expansion"}})
	for _, path := range []string{"/b/main/i/alpha", "/b/main/i/alpha/players/7656/panel"} {
		_, body, _ := e.get(path)
		assert.Contains(t, body, `value="expansion"`, "%s: no expansion way with the feature", path)
		for _, ty := range types {
			assert.Contains(t, body, `<option value="`+ty+`"`, "%s: no %s type", path, ty)
		}
	}
}

func TestOnlyTheBroadcastFormOffersRCon(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha")
	assert.Contains(t, body, `value="rcon"`, "the broadcast form lacks rcon")
	_, body, _ = e.get("/b/main/i/alpha/players/7656/panel")
	assert.NotContains(t, body, `value="rcon"`, "the player form offers rcon, which is for everyone only")
}

func TestBroadcastFormPostsNotifyFields(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, World: "enoch", Features: []string{"expansion"}})
	got := make(chan admin.Command, 1)
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()
	defer cancel()
	go func() {
		defer wg.Done()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			var res []admin.Result
			for _, c := range e.hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
				select {
				case got <- c:
				default:
				}
				res = append(res, admin.Result{ID: c.ID, OK: true})
			}
			if len(res) > 0 {
				e.hub.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	code, body := e.post("/b/main/i/alpha/message", url.Values{"text": {"Restart"}, "style": {"expansion"}, "notify_type": {"baguette"}, "title": {"T"}, "seconds": {"5"}}, nil)
	require.Equal(t, 200, code, "post: %s", body)
	require.Contains(t, body, "flash ok", "post")
	select {
	case c := <-got:
		assert.Equal(t, "expansion", c.Style)
		assert.Equal(t, "baguette", c.NotifyType)
		assert.Equal(t, "T", c.Title)
		assert.Equal(t, 5, c.Seconds)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the mod got no command")
	}
}

func TestForcedExpansionWithoutFeatureShowsAFlash(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body := e.post("/b/main/i/alpha/message", url.Values{"text": {"x"}, "style": {"expansion"}, "notify_type": {"toast"}}, nil)
	require.Equal(t, 200, code, "post: %s", body)
	require.Contains(t, body, "not available", "post")
	require.NotContains(t, body, "flash ok", "post")
}

func TestPlayersRowsHaveAnchors(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players")
	require.Contains(t, body, `<tr id="player-7656"`, "an event links to this row")
	_, body, _ = e.get("/b/main/i/alpha")
	require.Contains(t, body, `id="player-note"`, "the page has a place for the not-online note")
}

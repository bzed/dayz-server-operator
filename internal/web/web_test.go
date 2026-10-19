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
	"testing"
	"time"

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
}

func newEnv(t *testing.T, role api.Role) *env {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hub := hubs.Add(admin.NewHub("alpha"))
	apiSrv := api.New("site-a", "v0", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(context.Background(), filepath.Join(dir, "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	apiSrv.Moderation = webMod{st}
	ats := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ats.Close)
	secret, _, err := apiSrv.Tokens.Create("web", role, nil, true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// A second working backend, with its own bans, for the fan-out of a ban form.
	otherDir := t.TempDir()
	otherHubs := admin.NewRegistry(otherDir)
	otherHubs.Add(admin.NewHub("alpha"))
	otherAPI := api.New("site-b", "v0", otherHubs, api.NewTokenStore(filepath.Join(otherDir, "t.json")), admin.NewAudit(filepath.Join(otherDir, "a.jsonl")))
	otherStore, err := moderation.Open(context.Background(), filepath.Join(otherDir, "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherStore.Close() })
	otherAPI.Moderation = webMod{otherStore}
	otherTS := httptest.NewServer(otherAPI.Handler())
	t.Cleanup(otherTS.Close)
	otherSecret, _, err := otherAPI.Tokens.Create("web", role, nil, true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	assets, docs := filepath.Join(dir, "assets"), filepath.Join(dir, "docs")
	for _, d := range []string{filepath.Join(assets, "htmx"), docs} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(assets, "htmx", "htmx.min.js"), []byte("//htmx"), 0o600)
	flags := filepath.Join(dir, "flags")
	if err := os.MkdirAll(flags, 0o750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(flags, "de.svg"), []byte("<svg/>"), 0o600)
	apiSrv.GeoIP = geoiptest.Legacy(t, map[string]string{"198.51.100.0/24": "DE"})
	_ = os.WriteFile(filepath.Join(docs, "index.html"), []byte("<h1>docs</h1>"), 0o600)
	srv, err := New([]*Backend{
		{Name: "main", Client: apiclient.New(ats.URL, secret)},
		{Name: "other", Client: apiclient.New(otherTS.URL, otherSecret)},
		{Name: "down", Client: apiclient.New("http://127.0.0.1:1", "x")},
	}, Options{Version: "v0", Assets: assets, DocsDir: docs, FlagsDir: flags, UserHeader: "X-Forwarded-User"})
	if err != nil {
		t.Fatal(err)
	}
	ws := httptest.NewServer(srv.Handler())
	t.Cleanup(ws.Close)
	hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.1.0", World: "enoch",
		Players:  []admin.Player{{SteamID: "7656", Name: "<b>alice</b>", Alive: true, X: 1, Z: 2}},
		Vehicles: []admin.Vehicle{{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback", Health: 0.5, Fuel: 0.25}, {ID: "5.6.7.8", Kind: "motorbike", Type: "Motorbike_01"}, {ID: "9.9.9.9", Kind: "bicycle", Type: "Old_Bike_Red"}, {ID: "c.c.c.c", Kind: "car", Type: "Sedan_02", Owner: "Rita", Covered: true}},
		Types:    &admin.TypesChunk{Hash: "h", Total: 2, Names: []string{"AKM", "AK74"}},
	})
	return &env{t: t, web: ws, srv: srv, hub: hub, api: apiSrv, assets: assets}
}

func (e *env) get(path string) (int, string, http.Header) {
	e.t.Helper()
	resp, err := http.Get(e.web.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
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
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *env) answerCommands(ok bool, msg string) func() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for ctx.Err() == nil {
			var res []admin.Result
			for _, c := range e.hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
				res = append(res, admin.Result{ID: c.ID, OK: admin.Flag(ok), Message: msg})
			}
			if len(res) > 0 {
				e.hub.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return cancel
}

func TestDashboard(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body, hdr := e.get("/")
	if code != 200 || !strings.Contains(body, "site-a") || !strings.Contains(body, `href="/b/main/i/alpha"`) || !strings.Contains(body, "connected") {
		t.Fatalf("dashboard: %d %s", code, body)
	}
	// Every page loads the icon font, and the buttons keep their text next to the icon.
	// The tab has an icon: an SVG, and a PNG for the browsers that cannot take one.
	for _, f := range []string{"favicon.svg", "favicon-32.png", "apple-touch-icon.png"} {
		if !strings.Contains(body, "/static/"+f) {
			t.Errorf("the page does not link /static/%s", f)
		}
		if c, _, h := e.get("/static/" + f); c != 200 || !strings.HasPrefix(h.Get("Content-Type"), "image/") {
			t.Errorf("/static/%s: %d %q", f, c, h.Get("Content-Type"))
		}
	}
	if !strings.Contains(body, "/vendor/fork-awesome/css/fork-awesome.css") || !strings.Contains(body, `fa-map`) || !strings.Contains(body, "Open map") {
		t.Fatalf("dashboard icons: %s", body)
	}
	if !strings.Contains(body, "api: ") && !strings.Contains(body, "down") {
		t.Fatal("an unreachable backend must be shown with its error")
	}
	if !strings.Contains(hdr.Get("Content-Security-Policy"), "default-src 'self'") || hdr.Get("X-Frame-Options") != "DENY" || hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers: %v", hdr)
	}
	if !strings.Contains(body, e.srv.csrf) {
		t.Fatal("page must carry the CSRF token")
	}
	if c, _, _ := e.get("/nope"); c != 404 {
		t.Fatalf("unknown path: %d", c)
	}
}

func TestInstanceAndFragments(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha")
	if !strings.Contains(body, "dzo-admin connected") || !strings.Contains(body, "world enoch") || !strings.Contains(body, `hx-get="/b/main/i/alpha/players"`) {
		t.Fatalf("instance page: %s", body)
	}
	_, body, _ = e.get("/b/main/i/alpha/players")
	if !strings.Contains(body, "&lt;b&gt;alice&lt;/b&gt;") || strings.Contains(body, "<b>alice") {
		t.Fatalf("player names must be escaped: %s", body)
	}
	_, body, _ = e.get("/b/main/i/alpha/vehicles")
	if !strings.Contains(body, "OffroadHatchback") || !strings.Contains(body, "50%") || !strings.Contains(body, "/vehicles/1.2.3.4/panel") || !strings.Contains(body, "Motorbike_01") || !strings.Contains(body, "fa-motorcycle") || !strings.Contains(body, "Old_Bike_Red") || !strings.Contains(body, "fa-bicycle") || !strings.Contains(body, "Sedan_02 <em>(covered)</em>") || !strings.Contains(body, "Rita") || strings.Contains(body, "/vehicles/c.c.c.c/panel") {
		t.Fatalf("vehicles: %s", body)
	}
	_, body, _ = e.get("/b/main/i/alpha/players/7656/panel")
	if !strings.Contains(body, `hx-post="/b/main/i/alpha/players/7656/teleport"`) {
		t.Fatalf("panel: %s", body)
	}
	_, body, _ = e.get("/b/main/i/alpha/vehicles/1.2.3.4/panel")
	if !strings.Contains(body, `hx-post="/b/main/i/alpha/vehicles/1.2.3.4/flip"`) || !strings.Contains(body, `"scope": "missing"`) {
		t.Fatalf("vehicle panel: %s", body)
	}
	if _, body, _ = e.get("/b/main/i/alpha/vehicles/nope/panel"); !strings.Contains(body, "not found") {
		t.Fatalf("panel for an absent vehicle: %s", body)
	}
	if _, body, _ = e.get("/b/main/i/alpha/players/9999/panel"); !strings.Contains(body, "not online") {
		t.Fatalf("panel for an absent player: %s", body)
	}
	_, body, _ = e.get("/b/main/i/alpha/types?type=ak")
	if !strings.Contains(body, `<option value="AKM">`) {
		t.Fatalf("types: %s", body)
	}
	if _, body, _ = e.get("/b/main/i/alpha/vehicle-types?type=zz"); strings.Contains(body, "AKM") {
		t.Fatalf("vehicle types: %s", body)
	}
	if _, body, _ = e.get("/b/main/i/zzz"); !strings.Contains(body, "unknown instance") {
		t.Fatalf("unknown instance: %s", body)
	}
	if _, body, _ = e.get("/b/main/i/zzz/players"); !strings.Contains(body, "unknown instance") {
		t.Fatalf("fragment error: %s", body)
	}
	for _, p := range []string{"/b/nope/i/alpha", "/b/nope/audit", "/b/nope/i/alpha/map", "/b/nope/i/alpha/players", "/b/nope/i/alpha/vehicles", "/b/nope/i/alpha/types", "/b/nope/i/alpha/stream", "/b/nope/i/alpha/players/1/panel"} {
		if c, _, _ := e.get(p); c != 404 {
			t.Fatalf("%s: %d", p, c)
		}
	}
}

func TestMapAndAudit(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/map")
	if !strings.Contains(body, `data-size="12800"`) || !strings.Contains(body, `data-stream="/b/main/i/alpha/stream"`) || strings.Contains(body, "<script>") {
		t.Fatalf("map page: %s", body)
	}
	if mapSize("unknown") != defaultMapSize || mapSize("ChernarusPlus") != 15360 {
		t.Fatal("mapSize")
	}
	if c, js, _ := e.get("/static/map.js"); c != 200 || !strings.Contains(js, "EventSource") {
		t.Fatalf("map.js: %d", c)
	}
	// Flags show the time they have left, Expansion territories are drawn thin.
	for _, want := range []string{"function ttlText", "Raise flag", "/raise", "goes down in", "goes down now", "m.source === 'territory'"} {
		if _, js, _ := e.get("/static/map.js"); !strings.Contains(js, want) {
			t.Errorf("map.js lacks %q", want)
		}
	}
	if c, svg, _ := e.get("/static/icons/helicopter.svg"); c != 200 || !strings.Contains(svg, "Font Awesome Free") {
		t.Fatalf("the helicopter icon is not served: %d", c)
	}
	if c, _, _ := e.get("/static/app.css"); c != 200 {
		t.Fatal("app.css")
	}
	if _, body, _ := e.get("/b/main/audit"); !strings.Contains(body, "No entries") {
		t.Fatalf("empty audit: %s", body)
	}
	e2 := newEnv(t, api.RoleViewer)
	if _, body, _ := e2.get("/b/main/audit"); !strings.Contains(body, "audit.view") {
		t.Fatalf("a viewer must see why the audit is closed: %s", body)
	}
}

func TestVendorAndDocs(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	if c, b, _ := e.get("/vendor/htmx/htmx.min.js"); c != 200 || b != "//htmx" {
		t.Fatalf("vendor: %d %q", c, b)
	}
	if c, b, _ := e.get("/docs/"); c != 200 || !strings.Contains(b, "docs") {
		t.Fatalf("docs: %d", c)
	}
	srv, _ := New([]*Backend{{Name: "x", Client: apiclient.New("http://127.0.0.1:1", "t")}}, Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	r, err := http.Get(ts.URL + "/docs/")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatalf("docs without a dir: %d", r.StatusCode)
	}
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("no backends must fail")
	}
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
		"/b/main/i/alpha/flags/f1.2/raise":        {},
		"/b/main/i/alpha/vehicles/1.2.3.4/delete": {"force": {"1"}},
	} {
		code, body := e.post(path, form, map[string]string{"X-Forwarded-User": "alice@example.invalid"})
		if code != 200 || !strings.Contains(body, "flash ok") {
			t.Fatalf("%s: %d %s", path, code, body)
		}
	}
	entries, _ := e.api.Audit.Tail("alpha", 1)
	if len(entries) != 1 || entries[0].Actor != "web:alice@example.invalid" || entries[0].Source != "web" {
		t.Fatalf("audit = %+v", entries)
	}
	// Without the proxy header the actor is plain "web".
	e.post("/b/main/i/alpha/message", url.Values{"text": {"x"}}, nil)
	entries, _ = e.api.Audit.Tail("alpha", 1)
	if entries[0].Actor != "web:web" {
		t.Fatalf("actor = %q", entries[0].Actor)
	}
}

func TestQuantityMaxAndInvalid(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	defer e.answerCommands(true, "")()
	for _, q := range []string{"max", "MAX", "", " 5 "} {
		_, body := e.post("/b/main/i/alpha/spawn", url.Values{"type": {"AKM"}, "quantity": {q}, "x": {"1"}, "z": {"2"}}, nil)
		if !strings.Contains(body, "flash ok") {
			t.Errorf("quantity %q: %s", q, body)
		}
	}
	for _, q := range []string{"lots", "-3", "NaN", "Inf"} {
		_, body := e.post("/b/main/i/alpha/players/7656/give", url.Values{"type": {"AKM"}, "quantity": {q}}, nil)
		if !strings.Contains(body, "quantity must be a number, default or max") {
			t.Errorf("quantity %q: %s", q, body)
		}
	}
}

func TestActionErrors(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	_, body := e.post("/b/main/i/alpha/message", url.Values{"text": {"x"}}, nil)
	if !strings.Contains(body, "flash err") || !strings.Contains(body, "lacks") {
		t.Fatalf("forbidden: %s", body)
	}
	e2 := newEnv(t, api.RoleAdmin)
	stop := e2.answerCommands(false, "player not online")
	defer stop()
	_, body = e2.post("/b/main/i/alpha/players/1/teleport", url.Values{"x": {"1"}, "z": {"1"}}, nil)
	if !strings.Contains(body, "refused: player not online") {
		t.Fatalf("refused: %s", body)
	}
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
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if c := post(nil, ""); c != 403 {
		t.Fatalf("no token: %d", c)
	}
	if c := post(nil, "wrong"); c != 403 {
		t.Fatalf("wrong token: %d", c)
	}
	if c := post(map[string]string{"Origin": "https://evil.example"}, e.srv.csrf); c != 403 {
		t.Fatalf("foreign origin: %d", c)
	}
	if c := post(map[string]string{"Origin": e.web.URL}, e.srv.csrf); c == 403 {
		t.Fatalf("same origin must pass: %d", c)
	}
	if c := post(map[string]string{"Origin": "::bad"}, e.srv.csrf); c != 403 {
		t.Fatalf("unparsable origin: %d", c)
	}
}

func TestStreamRelay(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.web.URL+"/b/main/i/alpha/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
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
	if !seen["players"] || !seen["status"] {
		t.Fatalf("relayed events = %v", seen)
	}
	cancel()
	// A backend that is down answers 502.
	if c, _, _ := e.get("/b/down/i/alpha/stream"); c != http.StatusBadGateway {
		t.Fatalf("down backend: %d", c)
	}
}

func TestMapTiles(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	if _, body, _ := e.get("/b/main/i/alpha/map"); strings.Contains(body, "data-tiles") {
		t.Fatalf("a map without tiles must not announce any: %s", body)
	}
	if _, body, _ := e.get("/b/main/i/alpha/map"); !strings.Contains(body, "no map imagery") || !strings.Contains(body, "dzo map tiles build enoch") {
		t.Fatalf("a map without tiles says how to get them: %s", body)
	}
	root := t.TempDir()
	set := filepath.Join(root, "enoch", "abcdef0123456789")
	if err := os.MkdirAll(filepath.Join(set, "0", "0"), 0o750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(set, "metadata.json"), []byte(`{"map":"enoch","hash":"abcdef0123456789"}`), 0o600)
	_ = os.WriteFile(filepath.Join(set, "0", "0", "0.jpg"), []byte("JPEG"), 0o600)
	if err := os.Symlink("abcdef0123456789", filepath.Join(root, "enoch", "current")); err != nil {
		t.Fatal(err)
	}
	e.api.TilesDir = root

	_, body, _ := e.get("/b/main/i/alpha/map")
	if strings.Contains(body, "no map imagery") {
		t.Fatalf("a map with tiles needs no hint: %s", body)
	}
	if !strings.Contains(body, `data-tiles="/b/main/tiles/enoch"`) || !strings.Contains(body, `data-meta="{&#34;map&#34;:&#34;enoch&#34;`) {
		t.Fatalf("map page: %s", body)
	}
	code, b, hdr := e.get("/b/main/tiles/enoch/abcdef0123456789/0/0/0.jpg")
	if code != 200 || b != "JPEG" || hdr.Get("Content-Type") != "image/jpeg" || !strings.Contains(hdr.Get("Cache-Control"), "immutable") {
		t.Fatalf("tile: %d %q %v", code, b, hdr)
	}
	for _, p := range []string{"/b/nope/tiles/enoch/metadata.json", "/b/main/tiles/enoch/abcdef0123456789/0/0/0.png", "/b/main/tiles/enoch/abcdef0123456789/0/0/9.jpg"} {
		if code, _, _ := e.get(p); code != 404 {
			t.Errorf("%s: want 404, got %d", p, code)
		}
	}
}

// The player panel acts on the player; banning is a link to the ban site's form, not fields in the panel.
func TestPlayerPanelLinksToTheBanSite(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	if !strings.Contains(body, `href="/b/main/bans/new?steam_id=7656&amp;instance=alpha"`) {
		t.Fatalf("no link to the ban form: %s", body)
	}
	for _, field := range []string{`name="duration"`, `name="scope"`, `name="message"`} { // the kick dialog has its own reason
		if strings.Contains(body, field) {
			t.Errorf("the panel has the ban field %s", field)
		}
	}
	for _, group := range []string{"Tell", "Move", "Remove", "Give"} {
		if !strings.Contains(body, `class="gname">`+group) {
			t.Errorf("no %q group", group)
		}
	}
}

// The name cell keeps room for a country flag and offers the connection info.
func TestPlayerRowHasFlagSlotAndInfoButton(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players")
	for _, want := range []string{`class="flag"`, `data-info`, `class="pop"`, `/players/7656/info`} {
		if !strings.Contains(body, want) {
			t.Errorf("player table lacks %s: %s", want, body)
		}
	}
}

func TestPlayerInfoShowsTheSteamIDAndOnlyTheCurrentAddress(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	guid, _ := moderation.DeriveGUID(webSteam)
	st := e.api.Moderation.(webMod).st
	for i, ip := range []string{"192.0.2.7", "198.51.100.1"} {
		if err := st.Seen(context.Background(), "alpha", guid, ip, "Alice <i>", time.Now().Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "Alice <i>", Alive: true}}})
	_, body, _ := e.get("/b/main/i/alpha/players/" + webSteam + "/info")
	if !strings.Contains(body, webSteam) || !strings.Contains(body, "198.51.100.1") || strings.Contains(body, "192.0.2.7") || strings.Contains(body, "<i>") {
		t.Fatalf("info: %s", body)
	}
	if code, _, _ := e.get("/b/nope/i/alpha/players/" + webSteam + "/info"); code != 404 {
		t.Fatalf("unknown backend: %d", code)
	}
}

func TestPlayerInfoWithoutThePlayerIPPermissionHasNoAddress(t *testing.T) {
	e := newEnv(t, api.RoleModerator)
	guid, _ := moderation.DeriveGUID(webSteam)
	_ = e.api.Moderation.(webMod).st.Seen(context.Background(), "alpha", guid, "192.0.2.7", "alice", time.Now())
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "alice", Alive: true}}})
	_, body, _ := e.get("/b/main/i/alpha/players/" + webSteam + "/info")
	if strings.Contains(body, "192.0.2.7") || !strings.Contains(body, webSteam) {
		t.Fatalf("info: %s", body)
	}
}

func TestPlayerPanelHasAProfileButtonThatDoesNothingYet(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	if !strings.Contains(body, `class="profile" disabled`) {
		t.Fatalf("no disabled profile button: %s", body)
	}
}

func TestPlayerTableHasNoSteamIDColumn(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players")
	if strings.Contains(body, "Steam ID") || strings.Contains(body, "<td>7656</td>") {
		t.Fatalf("the table shows the Steam ID: %s", body)
	}
}

func TestKickDialogAndAction(t *testing.T) {
	e := newEnv(t, api.RoleModerator)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	for _, want := range []string{`data-dialog="kick-7656"`, `<dialog id="kick-7656"`, `hx-post="/b/main/i/alpha/players/7656/kick"`, `name="reason" required maxlength="64"`} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %s", want)
		}
	}
	kicked = nil
	_, out := e.post("/b/main/i/alpha/players/7656/kick", url.Values{"reason": {" afk "}}, nil)
	if !strings.Contains(out, "done") || len(kicked) != 1 || kicked[0] != "alpha 7656 afk" {
		t.Fatalf("kick: %s %v", out, kicked)
	}
	if _, out := e.post("/b/main/i/alpha/players/7656/kick", url.Values{"reason": {strings.Repeat("r", 65)}}, nil); !strings.Contains(out, "64") {
		t.Fatalf("long reason: %s", out)
	}
}

func TestPlayerPanelHasHealFixGearAndKillWithConfirmation(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	for _, want := range []string{`hx-post="/b/main/i/alpha/players/7656/heal"`, `hx-post="/b/main/i/alpha/players/7656/fixgear"`,
		`hx-post="/b/main/i/alpha/players/7656/kill"`, `hx-confirm="Kill &lt;b&gt;alice&lt;/b&gt;?`} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %s: %s", want, body)
		}
	}
}

func seenFrom(t *testing.T, e *env, ip string) {
	t.Helper()
	guid, _ := moderation.DeriveGUID(webSteam)
	if err := e.api.Moderation.(webMod).st.Seen(context.Background(), "alpha", guid, ip, "alice", time.Now()); err != nil {
		t.Fatal(err)
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "alice", Alive: true}, {SteamID: "7656", Name: "bob", Alive: true}}})
}

func TestPlayerListShowsTheFlag(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	seenFrom(t, e, "198.51.100.7")
	_, body, _ := e.get("/b/main/i/alpha/players")
	if !strings.Contains(body, `<span class="flag"><img src="/flags/DE.svg" alt="DE"`) {
		t.Fatalf("no flag: %s", body)
	}
	if strings.Count(body, "<img") != 1 {
		t.Fatalf("a player without a country got a flag: %s", body)
	}
}

func TestPlayerInfoShowsCountryButCityAndProviderNeedThePlayerIP(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	seenFrom(t, e, "198.51.100.7")
	_, body, _ := e.get("/b/main/i/alpha/players/" + webSteam + "/info")
	if !strings.Contains(body, "<dt>Country</dt>") || !strings.Contains(body, "/flags/DE.svg") || !strings.Contains(body, "</span>DE</dd>") {
		t.Fatalf("no country: %s", body)
	}
	// The legacy database holds no city, location or provider, so none are shown.
	for _, no := range []string{"<dt>City</dt>", "<dt>Location</dt>", "<dt>Provider</dt>"} {
		if strings.Contains(body, no) {
			t.Errorf("%s with a country-only database: %s", no, body)
		}
	}
}

func TestInfoRowsForCityLocationAndProvider(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	lat, lon := 48.137, 11.575
	var out strings.Builder
	err := e.srv.tmpl.ExecuteTemplate(&out, "playerinfo", playerInfoView{Name: "<x>", SteamID: webSteam,
		Geo: geoip.Info{Country: "DE", CountryName: "Germany", City: "München", Lat: &lat, Lon: &lon, ASN: 6805, ASOrg: "Telefonica <Germany>"}})
	if err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{"Germany (DE)", "<dd>München</dd>", "48.14, 11.57", "approximate", "AS6805 Telefonica &lt;Germany&gt;", "&lt;x&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q: %s", want, body)
		}
	}
}

func TestFlagsAreServedOnlyForTwoLetterCodes(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	if code, body, h := e.get("/flags/DE.svg"); code != 200 || body != "<svg/>" || !strings.Contains(h.Get("Cache-Control"), "max-age") {
		t.Fatalf("de: %d %q %v", code, body, h)
	}
	for _, p := range []string{"/flags/xx.svg", "/flags/d1.svg", "/flags/deu.svg", "/flags/%2e%2e%2fa"} {
		if code, _, _ := e.get(p); code != 404 && code != 400 && code != 301 {
			t.Errorf("%s: %d", p, code)
		}
	}
	e.srv.opts.FlagsDir = ""
	if code, _, _ := e.get("/flags/de.svg"); code != 404 {
		t.Fatalf("no flags dir: %d", code)
	}
}

func TestInstancePageOffersExpansionOnlyWithTheFeature(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	types := []string{"toast", "baguette", "activity", "killfeed", "market", "garage"}
	for _, path := range []string{"/b/main/i/alpha", "/b/main/i/alpha/players/7656/panel"} {
		_, body, _ := e.get(path)
		if strings.Contains(body, `value="expansion"`) || strings.Contains(body, `name="notify_type"`) {
			t.Errorf("%s offers expansion without the feature: %s", path, body)
		}
		if !strings.Contains(body, `value="notification"`) || !strings.Contains(body, `name="title"`) || !strings.Contains(body, `name="seconds"`) {
			t.Errorf("%s lacks the notification fields: %s", path, body)
		}
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, World: "enoch", Features: []string{"expansion"}})
	for _, path := range []string{"/b/main/i/alpha", "/b/main/i/alpha/players/7656/panel"} {
		_, body, _ := e.get(path)
		if !strings.Contains(body, `value="expansion"`) {
			t.Errorf("%s: no expansion way with the feature: %s", path, body)
		}
		for _, ty := range types {
			if !strings.Contains(body, `<option value="`+ty+`"`) {
				t.Errorf("%s: no %s type: %s", path, ty, body)
			}
		}
	}
}

func TestOnlyTheBroadcastFormOffersRCon(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	if _, body, _ := e.get("/b/main/i/alpha"); !strings.Contains(body, `value="rcon"`) {
		t.Errorf("the broadcast form lacks rcon: %s", body)
	}
	if _, body, _ := e.get("/b/main/i/alpha/players/7656/panel"); strings.Contains(body, `value="rcon"`) {
		t.Errorf("the player form offers rcon, which is for everyone only: %s", body)
	}
}

func TestBroadcastFormPostsNotifyFields(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, World: "enoch", Features: []string{"expansion"}})
	got := make(chan admin.Command, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
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
			time.Sleep(time.Millisecond)
		}
	}()
	code, body := e.post("/b/main/i/alpha/message", url.Values{"text": {"Restart"}, "style": {"expansion"}, "notify_type": {"baguette"}, "title": {"T"}, "seconds": {"5"}}, nil)
	if code != 200 || !strings.Contains(body, "flash ok") {
		t.Fatalf("post: %d %s", code, body)
	}
	select {
	case c := <-got:
		if c.Style != "expansion" || c.NotifyType != "baguette" || c.Title != "T" || c.Seconds != 5 {
			t.Errorf("command = %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the mod got no command")
	}
}

func TestForcedExpansionWithoutFeatureShowsAFlash(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body := e.post("/b/main/i/alpha/message", url.Values{"text": {"x"}, "style": {"expansion"}, "notify_type": {"toast"}}, nil)
	if code != 200 || !strings.Contains(body, "not available") || strings.Contains(body, "flash ok") {
		t.Fatalf("post: %d %s", code, body)
	}
}

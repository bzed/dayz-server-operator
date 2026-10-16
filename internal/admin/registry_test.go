// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r := NewRegistry(dir)
	r.Add(NewHub("alpha"))
	r.Add(NewHub("beta"))
	return r, dir
}

func post(r *Registry, body any, remote string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mod/v1/sync", bytes.NewReader(b))
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	r.ModHandler().ServeHTTP(rec, req)
	return rec
}

func TestModHandlerAuthAndSync(t *testing.T) {
	r, dir := testRegistry(t)
	if err := os.WriteFile(filepath.Join(dir, "alpha.token"), []byte("tok-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta.token"), []byte("tok-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := post(r, SyncRequest{Token: "tok-b", Proto: 1, Hello: true, Players: []Player{{SteamID: "9"}}}, "127.0.0.1:5000")
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	var rep SyncReply
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || !rep.OK || rep.Commands == nil {
		t.Fatalf("reply %+v err %v", rep, err)
	}
	beta, _ := r.Hub("beta")
	alpha, _ := r.Hub("alpha")
	if len(beta.Players()) != 1 || len(alpha.Players()) != 0 {
		t.Fatal("state went to the wrong instance")
	}

	// The token file is re-read when it changes (rotation at render time).
	if err := os.WriteFile(filepath.Join(dir, "beta.token"), []byte("tok-b2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "beta.token"), future, future)
	if post(r, SyncRequest{Token: "tok-b", Proto: 1}, "127.0.0.1:5000").Code != 401 {
		t.Fatal("rotated token still accepted")
	}
	if post(r, SyncRequest{Token: "tok-b2", Proto: 1}, "127.0.0.1:5000").Code != 200 {
		t.Fatal("new token rejected")
	}

	rec = post(r, SyncRequest{Token: "tok-a", Proto: 99}, "127.0.0.1:5000")
	if !strings.Contains(rec.Body.String(), "protocol mismatch") {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestModHandlerRejects(t *testing.T) {
	r, _ := testRegistry(t)
	req := httptest.NewRequest(http.MethodPost, "/mod/v1/sync", strings.NewReader("{nope"))
	rec := httptest.NewRecorder()
	r.ModHandler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad json: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	r.ModHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mod/v1/sync", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	// Ten bad tokens block the address, even for a valid one afterwards.
	for i := 0; i < 10; i++ {
		if c := post(r, SyncRequest{Token: "bad", Proto: 1}, "10.0.0.1:1").Code; c != 401 {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	if c := post(r, SyncRequest{Token: "bad", Proto: 1}, "10.0.0.1:2").Code; c != http.StatusTooManyRequests {
		t.Fatalf("not blocked: %d", c)
	}
	if c := post(r, SyncRequest{Token: "bad", Proto: 1}, "10.0.0.2:2").Code; c != 401 {
		t.Fatalf("other address blocked: %d", c)
	}
	if _, ok := r.Authenticate(""); ok {
		t.Fatal("empty token accepted")
	}
}

func TestRegistryAddKeepsState(t *testing.T) {
	r, _ := testRegistry(t)
	h, _ := r.Hub("alpha")
	h.Sync(SyncRequest{Proto: 1, Hello: true, Players: []Player{{SteamID: "1"}}})
	again := r.Add(&Hub{Name: "alpha", Deny: []string{"X*"}, MarkerDir: "/m"})
	if again != h || len(again.Players()) != 1 || again.Deny[0] != "X*" || again.MarkerDir != "/m" {
		t.Fatal("re-adding must keep the live state and take the new limits")
	}
	if got := r.Names(); len(got) != 2 || got[0] != "alpha" {
		t.Fatalf("names = %v", got)
	}
	if _, ok := r.Hub("zzz"); ok {
		t.Fatal("unknown hub")
	}
}

func TestFileMarkers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("convoy.json", `[{"id":"a","x":1,"z":2},{"layer":"other","id":"b"},{"x":1}]`)
	write("wrapped.json", `{"markers":[{"id":"w","shape":"circle","radius":50}]}`)
	write("broken.json", `{"markers": [`)

	h, c := testHub()
	h.MarkerDir = dir
	h.Sync(SyncRequest{Proto: 1, Hello: true, Markers: []Marker{{Layer: "ufo", ID: "1"}}})
	got := h.Markers("")
	if len(got) != 4 {
		t.Fatalf("markers = %+v", got)
	}
	if n := len(h.Markers("convoy")); n != 1 {
		t.Fatalf("convoy markers = %d", n)
	}
	for _, m := range got {
		if m.Layer == "wrapped" && (m.Source != "file" || m.Shape != "circle") {
			t.Fatalf("wrapped marker = %+v", m)
		}
	}
	// Cached for a few seconds, re-read afterwards.
	write("late.json", `[{"id":"l"}]`)
	if len(h.Markers("")) != 4 {
		t.Fatal("must serve the cache")
	}
	c.advance(fileMarkerTTL + time.Second)
	if len(h.Markers("")) != 5 {
		t.Fatal("must re-read after the TTL")
	}
	names := map[string]bool{}
	for _, l := range h.Layers() {
		names[l.Name] = true
	}
	if !names["late"] || !names["ufo"] {
		t.Fatalf("layers = %v", names)
	}
	h.MarkerDir = ""
	if len(h.Markers("")) != 1 {
		t.Fatal("no marker dir")
	}
}

func TestAudit(t *testing.T) {
	a := NewAudit(filepath.Join(t.TempDir(), "audit.jsonl"))
	if got, err := a.Tail("", 10); err != nil || len(got) != 0 {
		t.Fatalf("empty tail: %v %v", got, err)
	}
	for i, inst := range []string{"a", "b", "a"} {
		if err := a.Log(AuditEntry{Actor: "x", Source: "api", Instance: inst, Action: "message", Detail: string(rune('0' + i)), OK: true}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.Tail("a", 10)
	if err != nil || len(got) != 2 || got[0].Detail != "2" || got[0].Time.IsZero() {
		t.Fatalf("tail a = %+v %v", got, err)
	}
	if got, _ := a.Tail("", 2); len(got) != 2 || got[0].Detail != "2" {
		t.Fatalf("limit = %+v", got)
	}
	info, _ := os.Stat(a.Path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	nested := NewAudit(filepath.Join(t.TempDir(), "no", "dir", "x"))
	if err := nested.Log(AuditEntry{}); err != nil {
		t.Fatalf("a missing directory must be created: %v", err)
	}
	blocked := NewAudit(filepath.Join(a.Path, "x"))
	if blocked.Log(AuditEntry{}) == nil {
		t.Fatal("an unusable path must fail")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	c := &clock{t: time.Unix(0, 0)}
	l.now = c.now
	first, second, third := l.Allow("k"), l.Allow("k"), l.Allow("k")
	if !first || !second || third || !l.Blocked("k") {
		t.Fatal("limit of 2")
	}
	if !l.Allow("other") {
		t.Fatal("keys are independent")
	}
	c.advance(time.Minute + time.Second)
	if l.Blocked("k") || !l.Allow("k") {
		t.Fatal("window must slide")
	}
}

func TestWriteModConfig(t *testing.T) {
	profiles, tokens := t.TempDir(), t.TempDir()
	in := ModConfig{Endpoint: "http://127.0.0.1:2400/", DenyClasses: []string{"Land_*"}, Watch: []WatchRule{{Layer: "fire", Classes: []string{"FireplaceBase"}}}}
	if err := WriteModConfig(profiles, tokens, "alpha", in); err != nil {
		t.Fatal(err)
	}
	var got ModConfig
	b, err := os.ReadFile(filepath.Join(profiles, "dzo", "admin", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(tokens, "alpha.token"))
	if got.Version != 1 || got.SyncMS != 1000 || got.Token == "" || strings.TrimSpace(string(tok)) != got.Token || got.AllowClasses == nil || len(got.Watch) != 1 {
		t.Fatalf("config = %+v", got)
	}
	if !strings.Contains(string(b), `"allow_classes": []`) {
		t.Fatal("empty lists must be [] not null, the mod's JSON reader needs arrays")
	}
	if err := WriteModConfig(profiles, tokens, "alpha", in); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(profiles, "dzo", "admin", "config.json"))
	if string(b2) == string(b) {
		t.Fatal("token must rotate on every write")
	}
	if MarkerDir("/p") != "/p/dzo/admin/markers" {
		t.Fatal("MarkerDir")
	}
	if err := WriteModConfig(filepath.Join(tokens, "alpha.token", "x"), tokens, "b", in); err == nil {
		t.Fatal("unwritable profiles dir must fail")
	}
}

func TestWatchRuleValidate(t *testing.T) {
	if (WatchRule{}).Validate() == nil || (WatchRule{Layer: "l", Classes: []string{"A"}, OnlyIf: "burning"}).Validate() == nil {
		t.Fatal("must reject empty rule and only_if")
	}
	if err := (WatchRule{Layer: "l", Classes: []string{"A"}, Cluster: 50}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// The mod's JsonSerializer writes bool as 0/1, a missing array as [] and a
// missing object as {}; this is the shape captured from a real boot.
func TestModWireShape(t *testing.T) {
	r, dir := testRegistry(t)
	if err := os.WriteFile(filepath.Join(dir, "alpha.token"), []byte("tok"), 0o600); err != nil {
		t.Fatal(err)
	}
	send := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/mod/v1/sync", strings.NewReader(body))
		rec := httptest.NewRecorder()
		r.ModHandler().ServeHTTP(rec, req)
		return rec.Code
	}
	hub, _ := r.Hub("alpha")
	first := `{"token":"tok","protocol":1,"mod_version":"0.1.0","world":"chernarusplus","seq":0,"hello":1,"has":["players","vehicles"],
	 "players":[{"steam_id":"1","name":"a","alive":1,"unconscious":0}],"vehicles":[{"id":"v","ruined":0,"engine":1,"occupants":["1"]}],
	 "markers":[],"layers":[],"events":[],"types":{"hash":"h","offset":0,"total":1,"names":["AKM"]},"results":[{"id":"x","ok":1,"message":""}]}`
	if c := send(first); c != 200 {
		t.Fatalf("first sync: %d", c)
	}
	p := hub.Players()
	if len(p) != 1 || !bool(p[0].Alive) || bool(p[0].Unconscious) || p[0].Vehicle != "v" || !bool(hub.Vehicles()[0].Engine) || !hub.Status().Connected {
		t.Fatalf("state = %+v", p)
	}
	// Not due: empty arrays and an empty types object must not wipe state.
	later := `{"token":"tok","protocol":1,"seq":1,"hello":0,"has":[],"players":[],"vehicles":[],"markers":[],"layers":[],"events":[],"types":{},"results":[]}`
	if c := send(later); c != 200 {
		t.Fatalf("second sync: %d", c)
	}
	if len(hub.Players()) != 1 || len(hub.Vehicles()) != 1 || len(hub.Types("", 0)) != 1 {
		t.Fatal("a sync that lists no parts must keep the state")
	}
	// A listed part with nothing in it does clear the state.
	if send(`{"token":"tok","protocol":1,"has":["players"],"players":[]}`); len(hub.Players()) != 0 {
		t.Fatal("an included empty part must clear it")
	}
}

func TestFlag(t *testing.T) {
	var v struct {
		A, B, C, D Flag
	}
	if err := json.Unmarshal([]byte(`{"A":true,"B":1,"C":0,"D":null}`), &v); err != nil || !bool(v.A) || !bool(v.B) || bool(v.C) || bool(v.D) {
		t.Fatalf("%+v %v", v, err)
	}
	if json.Unmarshal([]byte(`{"A":"yes"}`), &v) == nil || json.Unmarshal([]byte(`{"A":2}`), &v) == nil {
		t.Fatal("non-boolean values must fail")
	}
	b, _ := json.Marshal(struct{ A Flag }{true})
	if string(b) != `{"A":true}` {
		t.Fatalf("marshal = %s", b)
	}
}

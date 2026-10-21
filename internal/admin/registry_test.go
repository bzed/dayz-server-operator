// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRegistry(t testing.TB) (*Registry, string) {
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
	err := os.WriteFile(filepath.Join(dir, "alpha.token"), []byte("tok-a\n"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "beta.token"), []byte("tok-b\n"), 0o600)
	require.NoError(t, err)

	rec := post(r, SyncRequest{Token: "tok-b", Proto: 1, Hello: true, Players: []Player{{SteamID: "9"}}}, "127.0.0.1:5000")
	require.Equal(t, 200, rec.Code)
	var rep SyncReply
	err = json.Unmarshal(rec.Body.Bytes(), &rep)
	require.NoError(t, err)
	require.True(t, rep.OK)
	require.NotNil(t, rep.Commands)
	beta, _ := r.Hub("beta")
	alpha, _ := r.Hub("alpha")
	require.Len(t, beta.Players(), 1)
	require.Empty(t, alpha.Players())

	// The token file is re-read when it changes (rotation at render time).
	err = os.WriteFile(filepath.Join(dir, "beta.token"), []byte("tok-b2\n"), 0o600)
	require.NoError(t, err)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "beta.token"), future, future)
	require.Equal(t, 401, post(r, SyncRequest{Token: "tok-b", Proto: 1}, "127.0.0.1:5000").Code)
	require.Equal(t, 200, post(r, SyncRequest{Token: "tok-b2", Proto: 1}, "127.0.0.1:5000").Code)

	rec = post(r, SyncRequest{Token: "tok-a", Proto: 99}, "127.0.0.1:5000")
	require.Contains(t, rec.Body.String(), "protocol mismatch")
}

func TestModHandlerRejects(t *testing.T) {
	r, _ := testRegistry(t)
	req := httptest.NewRequest(http.MethodPost, "/mod/v1/sync", strings.NewReader("{nope"))
	rec := httptest.NewRecorder()
	r.ModHandler().ServeHTTP(rec, req)
	require.Equal(t, 400, rec.Code)
	rec = httptest.NewRecorder()
	r.ModHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mod/v1/sync", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	// Ten bad tokens block the address, even for a valid one afterwards.
	for i := 0; i < 10; i++ {
		c := post(r, SyncRequest{Token: "bad", Proto: 1}, "10.0.0.1:1").Code
		require.Equal(t, 401, c)
	}
	c := post(r, SyncRequest{Token: "bad", Proto: 1}, "10.0.0.1:2").Code
	require.Equal(t, http.StatusTooManyRequests, c)
	c = post(r, SyncRequest{Token: "bad", Proto: 1}, "10.0.0.2:2").Code
	require.Equal(t, 401, c)
	_, ok := r.Authenticate("")
	require.False(t, ok)
}

func TestRegistryAddKeepsState(t *testing.T) {
	r, _ := testRegistry(t)
	h, _ := r.Hub("alpha")
	h.Sync(SyncRequest{Proto: 1, Hello: true, Players: []Player{{SteamID: "1"}}})
	again := r.Add(&Hub{Name: "alpha", Deny: []string{"X*"}, MarkerDir: "/m"})
	require.Same(t, h, again)
	require.Len(t, again.Players(), 1)
	require.Equal(t, "X*", again.Deny[0])
	require.Equal(t, "/m", again.MarkerDir)
	got := r.Names()
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0])
	_, ok := r.Hub("zzz")
	require.False(t, ok)
}

func TestFileMarkers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
		require.NoError(t, err)
	}
	write("convoy.json", `[{"id":"a","x":1,"z":2},{"layer":"other","id":"b"},{"x":1}]`)
	write("wrapped.json", `{"markers":[{"id":"w","shape":"circle","radius":50}]}`)
	write("broken.json", `{"markers": [`)

	h, c := testHub()
	h.MarkerDir = dir
	h.Sync(SyncRequest{Proto: 1, Hello: true, Markers: []Marker{{Layer: "ufo", ID: "1"}}})
	got := h.Markers("")
	require.Len(t, got, 4)
	n := len(h.Markers("convoy"))
	require.Equal(t, 1, n)
	for _, m := range got {
		if m.Layer == "wrapped" {
			require.Equal(t, "file", m.Source, "wrapped marker = %+v", m)
			require.Equal(t, "circle", m.Shape, "wrapped marker = %+v", m)
		}
	}
	// Cached for a few seconds, re-read afterwards.
	write("late.json", `[{"id":"l"}]`)
	require.Len(t, h.Markers(""), 4)
	c.advance(fileMarkerTTL + time.Second)
	require.Len(t, h.Markers(""), 5)
	names := map[string]bool{}
	for _, l := range h.Layers() {
		names[l.Name] = true
	}
	require.True(t, names["late"])
	require.True(t, names["ufo"])
	h.MarkerDir = ""
	require.Len(t, h.Markers(""), 1)
}

func TestAudit(t *testing.T) {
	a := NewAudit(filepath.Join(t.TempDir(), "audit.jsonl"))
	got, err := a.Tail("", 10)
	require.NoError(t, err)
	require.Empty(t, got)
	for i, inst := range []string{"a", "b", "a"} {
		err := a.Log(AuditEntry{Actor: "x", Source: "api", Instance: inst, Action: "message", Detail: string(rune('0' + i)), OK: true})
		require.NoError(t, err)
	}
	got, err = a.Tail("a", 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "2", got[0].Detail)
	require.False(t, got[0].Time.IsZero())
	got, _ = a.Tail("", 2)
	require.Len(t, got, 2)
	require.Equal(t, "2", got[0].Detail)
	info, _ := os.Stat(a.Path)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	nested := NewAudit(filepath.Join(t.TempDir(), "no", "dir", "x"))
	err = nested.Log(AuditEntry{})
	require.NoError(t, err, "a missing directory must be created")
	blocked := NewAudit(filepath.Join(a.Path, "x"))
	require.Error(t, blocked.Log(AuditEntry{}))
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	c := &clock{t: time.Unix(0, 0)}
	l.now = c.now
	first, second, third := l.Allow("k"), l.Allow("k"), l.Allow("k")
	require.True(t, first)
	require.True(t, second)
	require.False(t, third)
	require.True(t, l.Blocked("k"))
	require.True(t, l.Allow("other"), "keys are independent")
	c.advance(time.Minute + time.Second)
	require.False(t, l.Blocked("k"))
	require.True(t, l.Allow("k"))
}

func TestWriteModConfig(t *testing.T) {
	profiles, tokens := t.TempDir(), t.TempDir()
	in := ModConfig{Endpoint: "http://127.0.0.1:2400/", DenyClasses: []string{"Land_*"}, Watch: []WatchRule{{Layer: "fire", Classes: []string{"FireplaceBase"}}}}
	err := WriteModConfig(profiles, tokens, "alpha", in)
	require.NoError(t, err)
	var got ModConfig
	b, err := os.ReadFile(filepath.Join(profiles, "dzo", "admin", "config.json"))
	require.NoError(t, err)
	err = json.Unmarshal(b, &got)
	require.NoError(t, err)
	tok, _ := os.ReadFile(filepath.Join(tokens, "alpha.token"))
	require.Equal(t, 1, got.Version)
	require.Equal(t, 1000, got.SyncMS)
	require.NotEmpty(t, got.Token)
	require.Equal(t, got.Token, strings.TrimSpace(string(tok)))
	require.NotNil(t, got.AllowClasses)
	require.Len(t, got.Watch, 1)
	require.Contains(t, string(b), `"allow_classes": []`)
	err = WriteModConfig(profiles, tokens, "alpha", in)
	require.NoError(t, err)
	b2, _ := os.ReadFile(filepath.Join(profiles, "dzo", "admin", "config.json"))
	require.NotEqual(t, string(b), string(b2))
	require.Equal(t, "/p/dzo/admin/markers", MarkerDir("/p"))
	err = WriteModConfig(filepath.Join(tokens, "alpha.token", "x"), tokens, "b", in)
	require.Error(t, err, "unwritable profiles dir must fail")
}

func TestWatchRuleValidate(t *testing.T) {
	require.Error(t, (WatchRule{}).Validate())
	require.Error(t, (WatchRule{Layer: "l", Classes: []string{"A"}, OnlyIf: "burning"}).Validate())
	err := (WatchRule{Layer: "l", Classes: []string{"A"}, Cluster: 50}).Validate()
	require.NoError(t, err)
}

// The mod's JsonSerializer writes bool as 0/1, a missing array as [] and a
// missing object as {}; this is the shape captured from a real boot.
func TestModWireShape(t *testing.T) {
	r, dir := testRegistry(t)
	err := os.WriteFile(filepath.Join(dir, "alpha.token"), []byte("tok"), 0o600)
	require.NoError(t, err)
	send := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/mod/v1/sync", strings.NewReader(body))
		rec := httptest.NewRecorder()
		r.ModHandler().ServeHTTP(rec, req)
		return rec.Code
	}
	hub, _ := r.Hub("alpha")
	first := `{"token":"tok","protocol":1,"mod_version":"0.1.0","world":"chernarusplus","seq":0,"hello":1,"has":["players","vehicles"],
	 "players":[{"steam_id":"1","name":"a","alive":1,"unconscious":0}],"vehicles":[{"id":"v","ruined":0,"engine":1,"occupants":["1"]},{"id":"c","type":"Sedan_02","covered":1,"owner":"Rita"}],
	 "markers":[],"layers":[],"events":[],"types":{"hash":"h","offset":0,"total":1,"names":["AKM"]},"results":[{"id":"x","ok":1,"message":""}]}`
	c := send(first)
	require.Equal(t, 200, c)
	p := hub.Players()
	require.Len(t, p, 1)
	require.True(t, bool(p[0].Alive))
	require.False(t, bool(p[0].Unconscious))
	require.Equal(t, "v", p[0].Vehicle)
	require.True(t, bool(hub.Vehicles()[0].Engine))
	require.Len(t, hub.Vehicles(), 2)
	require.True(t, bool(hub.Vehicles()[1].Covered))
	require.Equal(t, "Rita", hub.Vehicles()[1].Owner)
	require.True(t, hub.Status().Connected)
	// Not due: empty arrays and an empty types object must not wipe state.
	later := `{"token":"tok","protocol":1,"seq":1,"hello":0,"has":[],"players":[],"vehicles":[],"markers":[],"layers":[],"events":[],"types":{},"results":[]}`
	c = send(later)
	require.Equal(t, 200, c)
	require.Len(t, hub.Players(), 1)
	require.Len(t, hub.Vehicles(), 2)
	require.Len(t, hub.Types("", 0), 1)
	// A listed part with nothing in it does clear the state.
	send(`{"token":"tok","protocol":1,"has":["players"],"players":[]}`)
	require.Empty(t, hub.Players())
}

func TestFlag(t *testing.T) {
	var v struct {
		A, B, C, D Flag
	}
	err := json.Unmarshal([]byte(`{"A":true,"B":1,"C":0,"D":null}`), &v)
	require.NoError(t, err)
	require.True(t, bool(v.A))
	require.True(t, bool(v.B))
	require.False(t, bool(v.C))
	require.False(t, bool(v.D))
	require.Error(t, json.Unmarshal([]byte(`{"A":"yes"}`), &v))
	require.Error(t, json.Unmarshal([]byte(`{"A":2}`), &v))
	b, _ := json.Marshal(struct{ A Flag }{true})
	require.Equal(t, `{"A":true}`, string(b))
}

func logRegistry(t testing.TB) (*Registry, string, *[]string) {
	t.Helper()
	r, dir := testRegistry(t)
	var lines []string
	r.Logf = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	return r, dir, &lines
}

func TestConnectionLog(t *testing.T) {
	r, dir, lines := logRegistry(t)
	err := os.WriteFile(filepath.Join(dir, "alpha.token"), []byte("tok-a\n"), 0o600)
	require.NoError(t, err)
	hub, _ := r.Hub("alpha")
	clock := time.Now()
	hub.now = func() time.Time { return clock }

	r.LogTokens()
	post(r, SyncRequest{Token: "tok-a", Proto: 1, Hello: true, ModVersion: "0.1.0", World: "enoch"}, "10.0.0.1:1")
	post(r, SyncRequest{Token: "tok-a", Proto: 1}, "10.0.0.1:1")
	clock = clock.Add(time.Minute)
	r.CheckLost()
	r.CheckLost() // logged once
	post(r, SyncRequest{Token: "tok-a", Proto: 1}, "10.0.0.1:1")
	post(r, SyncRequest{Token: "tok-a", Proto: 1, Hello: true}, "10.0.0.1:1")

	out := strings.Join(*lines, "\n")
	for _, want := range []string{
		"instance alpha: waiting for its dzo-admin mod",
		"instance beta: no token file",
		"instance alpha: mod connected from 10.0.0.1 (mod 0.1.0, protocol 1, world enoch)",
		"instance alpha: mod lost, no sync since",
		"instance alpha: mod reconnected from 10.0.0.1 after",
		"instance alpha: mod restarted",
	} {
		assert.Contains(t, out, want)
	}
	n := strings.Count(out, "mod lost")
	assert.Equal(t, 1, n)
	assert.NotContains(t, out, "tok-a")
}

func TestRefusalsAreLoggedOncePerAddress(t *testing.T) {
	r, dir, lines := logRegistry(t)
	err := os.WriteFile(filepath.Join(dir, "alpha.token"), []byte("tok-a\n"), 0o600)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		post(r, SyncRequest{Token: "wrong-secret", Proto: 1}, "10.0.0.2:1")
	}
	post(r, SyncRequest{Token: "tok-a", Proto: 99}, "10.0.0.2:1")
	out := strings.Join(*lines, "\n")
	n := strings.Count(out, "token matches no instance")
	assert.Equal(t, 1, n)
	assert.Contains(t, out, "instances without a token file: beta")
	assert.Contains(t, out, "instance alpha speaks protocol 99")
	assert.NotContains(t, out, "wrong-secret")
}

func TestAuditOnLog(t *testing.T) {
	a := NewAudit(filepath.Join(t.TempDir(), "audit.jsonl"))
	var got []AuditEntry
	a.OnLog = func(e AuditEntry) { got = append(got, e) }
	require.NoError(t, a.Log(AuditEntry{Actor: "x", Instance: "a", Action: "message", OK: true}))
	require.Len(t, got, 1)
	require.False(t, got[0].Time.IsZero(), "the callback sees the stamped entry")
	blocked := NewAudit(filepath.Join(a.Path, "x"))
	blocked.OnLog = func(AuditEntry) { t.Fatal("called for a failed write") }
	require.Error(t, blocked.Log(AuditEntry{}))
}

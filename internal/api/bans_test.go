// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const steamA = "76561198000000001"

var banNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

type fakeMod struct {
	st *moderation.Store
	mu sync.Mutex
	n  int

	kicks   []string
	kickErr error
}

func (f *fakeMod) Kick(_ context.Context, instance, steamID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kickErr != nil {
		return f.kickErr
	}
	f.kicks = append(f.kicks, instance+" "+steamID+" "+reason)
	return nil
}

func (f *fakeMod) Store() *moderation.Store { return f.st }

func (f *fakeMod) Changed(_ context.Context, b moderation.Ban, _ *moderation.Ban) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return map[string]string{"alpha": "ok"}
}

func newBanEnv(t *testing.T) (*env, *fakeMod) {
	t.Helper()
	e := newEnv(t)
	st, err := moderation.Open(context.Background(), filepath.Join(t.TempDir(), "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := &fakeMod{st: st}
	e.srv.Moderation, e.srv.now = m, func() time.Time { return banNow }
	return e, m
}

func banBody(scope string, instances []string, dur time.Duration, ips ...string) string {
	in := moderation.BanInput{SteamID: steamA, Reason: "aimbot", Scope: moderation.Scope(scope), Instances: instances, IPs: ips}
	if dur > 0 {
		x := banNow.Add(dur)
		in.ExpiresAt = &x
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func TestBanRightsMatrix(t *testing.T) {
	e, _ := newBanEnv(t)
	day := 24 * time.Hour
	cases := []struct {
		role Role
		body string
		want int
	}{
		{RoleViewer, banBody("instances", []string{"alpha"}, day), 403},
		{RoleModerator, banBody("instances", []string{"alpha"}, day), 201},
		{RoleModerator, banBody("instances", []string{"alpha"}, 8*day), 403},
		{RoleModerator, banBody("instances", []string{"alpha"}, 0), 403},
		{RoleModerator, banBody("instances", []string{"alpha"}, day, "192.0.2.7"), 403},
		{RoleModerator, banBody("global", nil, day), 403},
		{RoleOperator, banBody("instances", []string{"alpha"}, 0, "192.0.2.7"), 201},
		{RoleOperator, banBody("global", nil, day), 403},
		{RoleOperator, banBody("instances", []string{"nosuch"}, day), 400},
		{RoleAdmin, banBody("global", nil, 0), 201},
		{RoleAdmin, `{"reason":"x","scope":"global"}`, 400},
		{RoleAdmin, `{"bogus":1}`, 400},
	}
	for i, c := range cases {
		code, b := e.do("POST", "/api/v1/bans", e.token(c.role, nil, false), c.body)
		if code != c.want {
			t.Errorf("case %d (%s): %d %s, want %d", i, c.role, code, b, c.want)
		}
	}
}

func TestBanLifecycleOverAPI(t *testing.T) {
	e, m := newBanEnv(t)
	mod := e.token(RoleModerator, nil, true)
	code, b := e.do("POST", "/api/v1/bans", mod, banBody("instances", []string{"alpha"}, time.Hour), "X-Dzo-Actor", "alice")
	if code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	var res BanResult
	_ = json.Unmarshal(b, &res)
	if res.Sync["alpha"] != "ok" || res.Ban.CreatedBy != "t-moderator:alice" || m.n != 1 {
		t.Fatalf("result %+v, changed %d", res, m.n)
	}
	id := res.Ban.ID
	if code, b := e.do("GET", fmt.Sprintf("/api/v1/bans?state=active&q=%s", steamA), mod, ""); code != 200 || !strings.Contains(string(b), `"aimbot"`) {
		t.Fatalf("list: %d %s", code, b)
	}
	in := res.Ban.Input()
	in.Reason = "aimbot (confirmed)"
	body, _ := json.Marshal(in)
	if code, b := e.do("PUT", fmt.Sprintf("/api/v1/bans/%d", id), mod, string(body), "X-Dzo-Actor", "alice"); code != 200 || !strings.Contains(string(b), "confirmed") {
		t.Fatalf("edit: %d %s", code, b)
	}
	// Another moderator may not revoke alice's ban; an admin may.
	if code, _ := e.do("POST", fmt.Sprintf("/api/v1/bans/%d/revoke", id), mod, `{"reason":"no"}`, "X-Dzo-Actor", "bob"); code != 403 {
		t.Fatalf("bob revoked alice's ban: %d", code)
	}
	if code, b := e.do("POST", fmt.Sprintf("/api/v1/bans/%d/revoke", id), e.token(RoleAdmin, nil, false), `{"reason":"appeal"}`); code != 200 {
		t.Fatalf("admin revoke: %d %s", code, b)
	}
	if code, b := e.do("GET", fmt.Sprintf("/api/v1/bans/%d/history", id), mod, ""); code != 200 || strings.Count(string(b), `"action"`) != 3 {
		t.Fatalf("history: %d %s", code, b)
	}
	if code, _ := e.do("GET", "/api/v1/bans/999", mod, ""); code != 404 {
		t.Fatalf("missing ban: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/bans/abc", mod, ""); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/bans?state=bogus", mod, ""); code != 400 {
		t.Fatalf("bad state: %d", code)
	}
}

func TestRestrictedTokenSeesOnlyItsBans(t *testing.T) {
	e, m := newBanEnv(t)
	st := m.st
	beta, _ := st.CreateBan(context.Background(), "x", moderation.BanInput{SteamID: steamA, Reason: "beta only", Scope: moderation.ScopeInstances, Instances: []string{"beta"}}, banNow)
	glob, _ := st.CreateBan(context.Background(), "x", moderation.BanInput{GUIDs: []string{"0123456789abcdef0123456789abcdef"}, Reason: "everywhere", Scope: moderation.ScopeGlobal}, banNow)
	tok := e.token(RoleAdmin, []string{"alpha"}, false)
	code, b := e.do("GET", "/api/v1/bans", tok, "")
	if code != 200 || strings.Contains(string(b), "beta only") || !strings.Contains(string(b), "everywhere") {
		t.Fatalf("list: %d %s", code, b)
	}
	if code, _ := e.do("GET", fmt.Sprintf("/api/v1/bans/%d", beta.ID), tok, ""); code != 404 {
		t.Fatalf("beta ban visible: %d", code)
	}
	if code, _ := e.do("GET", fmt.Sprintf("/api/v1/bans/%d", glob.ID), tok, ""); code != 200 {
		t.Fatalf("global ban hidden: %d", code)
	}
	if code, _ := e.do("POST", "/api/v1/bans", tok, banBody("global", nil, 0)); code != 403 {
		t.Fatalf("a restricted token made a global ban: %d", code)
	}
	if code, _ := e.do("POST", "/api/v1/bans", tok, banBody("instances", []string{"beta"}, time.Hour)); code != 400 {
		t.Fatalf("a restricted token banned on beta: %d", code)
	}
}

func TestBansWithoutDatabase(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do("GET", "/api/v1/bans", e.token(RoleAdmin, nil, false), ""); code != 503 {
		t.Fatalf("no moderation: %d", code)
	}
}

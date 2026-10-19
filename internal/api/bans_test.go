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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func newBanEnv(t testing.TB) (*env, *fakeMod) {
	t.Helper()
	e := newEnv(t)
	st, err := moderation.Open(t.Context(), filepath.Join(t.TempDir(), "dzo.sqlite"))
	require.NoError(t, err)
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
		assert.Equal(t, c.want, code, "case %d (%s): %s", i, c.role, b)
	}
}

func TestBanLifecycleOverAPI(t *testing.T) {
	e, m := newBanEnv(t)
	mod := e.token(RoleModerator, nil, true)
	code, b := e.do("POST", "/api/v1/bans", mod, banBody("instances", []string{"alpha"}, time.Hour), "X-Dzo-Actor", "alice")
	require.Equal(t, 201, code)
	var res BanResult
	_ = json.Unmarshal(b, &res)
	require.Equal(t, "ok", res.Sync["alpha"], "result %+v, changed %d", res, m.n)
	require.Equal(t, "t-moderator:alice", res.Ban.CreatedBy, "result %+v, changed %d", res, m.n)
	require.Equal(t, 1, m.n, "result %+v, changed %d", res, m.n)
	id := res.Ban.ID
	code, b = e.do("GET", fmt.Sprintf("/api/v1/bans?state=active&q=%s", steamA), mod, "")
	require.Equal(t, 200, code, "list: %d %s", code, b)
	require.Contains(t, string(b), `"aimbot"`, "list: %d %s", code, b)
	in := res.Ban.Input()
	in.Reason = "aimbot (confirmed)"
	body, _ := json.Marshal(in)
	code, b = e.do("PUT", fmt.Sprintf("/api/v1/bans/%d", id), mod, string(body), "X-Dzo-Actor", "alice")
	require.Equal(t, 200, code, "edit: %d %s", code, b)
	require.Contains(t, string(b), "confirmed", "edit: %d %s", code, b)
	// Another moderator may not revoke alice's ban; an admin may.
	code, _ = e.do("POST", fmt.Sprintf("/api/v1/bans/%d/revoke", id), mod, `{"reason":"no"}`, "X-Dzo-Actor", "bob")
	require.Equal(t, 403, code)
	code, b = e.do("POST", fmt.Sprintf("/api/v1/bans/%d/revoke", id), e.token(RoleAdmin, nil, false), `{"reason":"appeal"}`)
	require.Equal(t, 200, code)
	code, b = e.do("GET", fmt.Sprintf("/api/v1/bans/%d/history", id), mod, "")
	require.Equal(t, 200, code, "history: %d %s", code, b)
	require.Equal(t, 3, strings.Count(string(b), `"action"`), "history: %d %s", code, b)
	code, _ = e.do("GET", "/api/v1/bans/999", mod, "")
	require.Equal(t, 404, code)
	code, _ = e.do("GET", "/api/v1/bans/abc", mod, "")
	require.Equal(t, 400, code)
	code, _ = e.do("GET", "/api/v1/bans?state=bogus", mod, "")
	require.Equal(t, 400, code)
}

func TestRestrictedTokenSeesOnlyItsBans(t *testing.T) {
	e, m := newBanEnv(t)
	st := m.st
	beta, _ := st.CreateBan(t.Context(), "x", moderation.BanInput{SteamID: steamA, Reason: "beta only", Scope: moderation.ScopeInstances, Instances: []string{"beta"}}, banNow)
	glob, _ := st.CreateBan(t.Context(), "x", moderation.BanInput{GUIDs: []string{"0123456789abcdef0123456789abcdef"}, Reason: "everywhere", Scope: moderation.ScopeGlobal}, banNow)
	tok := e.token(RoleAdmin, []string{"alpha"}, false)
	code, b := e.do("GET", "/api/v1/bans", tok, "")
	require.Equal(t, 200, code, "list: %d %s", code, b)
	require.NotContains(t, string(b), "beta only", "list: %d %s", code, b)
	require.Contains(t, string(b), "everywhere", "list: %d %s", code, b)
	code, _ = e.do("GET", fmt.Sprintf("/api/v1/bans/%d", beta.ID), tok, "")
	require.Equal(t, 404, code)
	code, _ = e.do("GET", fmt.Sprintf("/api/v1/bans/%d", glob.ID), tok, "")
	require.Equal(t, 200, code)
	code, _ = e.do("POST", "/api/v1/bans", tok, banBody("global", nil, 0))
	require.Equal(t, 403, code)
	code, _ = e.do("POST", "/api/v1/bans", tok, banBody("instances", []string{"beta"}, time.Hour))
	require.Equal(t, 400, code)
}

func TestBansWithoutDatabase(t *testing.T) {
	e := newEnv(t)
	code, _ := e.do("GET", "/api/v1/bans", e.token(RoleAdmin, nil, false), "")
	require.Equal(t, 503, code)
}

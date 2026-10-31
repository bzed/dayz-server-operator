// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const steamC = "76561198000000003"

// webEnv is a ban environment whose server also has user logins: people live in the same store.
type webEnv struct {
	*env
	m *fakeMod
}

func newWebEnv(t *testing.T) *webEnv {
	t.Helper()
	e, m := newBanEnv(t)
	e.srv.Accounts = m.st
	return &webEnv{env: e, m: m}
}

// webToken makes a web token with the given ceiling.
func (w *webEnv) webToken(role Role, instances ...string) string {
	w.t.Helper()
	s, _, err := w.srv.Tokens.Create("web", role, instances, false, true, time.Hour)
	require.NoError(w.t, err)
	return s
}

// login makes a person with the grants and a session for them; the secret is what X-Dzo-Session carries.
func (w *webEnv) login(steamID string, grants ...moderation.Grant) string {
	w.t.Helper()
	require.NoError(w.t, w.m.st.PutPerson(w.t.Context(), moderation.Person{SteamID: steamID, Name: "P" + steamID[len(steamID)-1:], Grants: grants}, banNow))
	secret, hash, err := auth.NewSecret("dzs_")
	require.NoError(w.t, err)
	require.NoError(w.t, w.m.st.CreateSession(w.t.Context(), hash, steamID, "", banNow))
	return secret
}

func (w *webEnv) as(method, path, token, session, body string, hdr ...string) (int, []byte) {
	w.t.Helper()
	return w.do(method, path, token, body, append([]string{"X-Dzo-Session", session}, hdr...)...)
}

func TestWebTokenNeedsSession(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	for _, path := range []string{"/api/v1", "/api/v1/instances", "/api/v1/instances/alpha", "/api/v1/bans", "/api/v1/audit"} {
		code, b := w.do("GET", path, tok, "")
		assert.Equal(t, 403, code, path)
		assert.Contains(t, string(b), "needs a user session", path)
	}
}

func TestWebTokenWithoutAccountsIs503(t *testing.T) {
	w := newWebEnv(t)
	w.srv.Accounts = nil
	code, b := w.as("GET", "/api/v1", w.webToken(RoleAdmin), "dzs_x", "")
	assert.Equal(t, 503, code)
	assert.Contains(t, string(b), "logins are not configured")
}

func TestSessionOnlyWorksWithAWebToken(t *testing.T) {
	w := newWebEnv(t)
	session := w.login(steamA, g("*", RoleAdmin))
	plain := w.token(RoleViewer, nil, false)
	code, b := w.as("GET", "/api/v1", plain, session, "")
	require.Equal(t, 200, code)
	var info Info
	require.NoError(t, json.Unmarshal(b, &info))
	assert.Equal(t, RoleViewer, info.Role, "the header is ignored on a token that is not a web token: it acts as itself")
}

func TestSessionExpired401(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	code, b := w.as("GET", "/api/v1", tok, "dzs_unknown", "")
	assert.Equal(t, 401, code)
	assert.Contains(t, string(b), "session expired")

	session := w.login(steamA, g("*", RoleAdmin))
	code, _ = w.as("GET", "/api/v1", tok, session, "")
	require.Equal(t, 200, code)
	w.srv.now = func() time.Time { return banNow.Add(13 * time.Hour) }
	code, b = w.as("GET", "/api/v1", tok, session, "")
	assert.Equal(t, 401, code, "idle for longer than the default 12 hours")
	assert.Contains(t, string(b), "session expired")
	w.srv.Login.SessionIdle = 24 * time.Hour
	other := w.login(steamB, g("*", RoleAdmin))
	code, _ = w.as("GET", "/api/v1", tok, other, "")
	assert.Equal(t, 200, code, "serve.login.session_idle_hours is honoured")
}

func TestDisabledPerson403(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	session := w.login(steamA, g("*", RoleAdmin))
	require.NoError(t, w.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamA, Disabled: true, Grants: []moderation.Grant{g("*", RoleAdmin)}}, banNow))
	// disabling deletes the sessions, so put one back to see that a disabled person is refused on its own
	secret := "dzs_manual"
	require.NoError(t, w.m.st.CreateSession(t.Context(), auth.HashSecret(secret), steamA, "", banNow))
	code, b := w.as("GET", "/api/v1", tok, secret, "")
	assert.Equal(t, 403, code)
	assert.Contains(t, string(b), "disabled")
	code, _ = w.as("GET", "/api/v1", tok, session, "")
	assert.Equal(t, 401, code, "the old session is gone")
}

func TestPersonWithoutAnyRole403(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	w.login(steamA, g("gone", RoleAdmin))
	secret := "dzs_manual2"
	require.NoError(t, w.m.st.CreateSession(t.Context(), auth.HashSecret(secret), steamA, "", banNow))
	code, b := w.as("GET", "/api/v1", tok, secret, "")
	assert.Equal(t, 403, code, "a grant for an instance that no longer exists gives nothing")
	assert.Contains(t, string(b), "no access")
}

func TestGrantForRemovedInstance(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	session := w.login(steamA, g("alpha", RoleViewer), g("gone", RoleAdmin))
	code, b := w.as("GET", "/api/v1/instances", tok, session, "")
	require.Equal(t, 200, code)
	var list []struct {
		Instance string `json:"instance"`
	}
	require.NoError(t, json.Unmarshal(b, &list))
	require.Len(t, list, 1)
	assert.Equal(t, "alpha", list[0].Instance)
	code, _ = w.as("GET", "/api/v1/instances/gone", tok, session, "")
	assert.Equal(t, 404, code)
	code, _ = w.as("GET", "/api/v1/instances/beta", tok, session, "")
	assert.Equal(t, 404, code, "no grant on beta")
}

func TestInfoReportsTheTopRole(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	session := w.login(steamA, g("alpha", RoleModerator), g("beta", RoleViewer))
	code, b := w.as("GET", "/api/v1", tok, session, "")
	require.Equal(t, 200, code)
	var info Info
	require.NoError(t, json.Unmarshal(b, &info))
	assert.Equal(t, RoleModerator, info.Role)
	assert.Equal(t, "web:"+steamA, info.Principal)
}

func TestModeratorOnANotOnB(t *testing.T) {
	e, m, _ := playerEnv(t)
	e.srv.Accounts = m.st
	w := &webEnv{env: e, m: m}
	tok := w.webToken(RoleAdmin)
	session := w.login(steamC, g("alpha", RoleModerator))
	admin := e.token(RoleAdmin, nil, false)

	// a ban scoped to B is refused (B does not exist for this person), one scoped to A is fine
	code, b := w.as("POST", "/api/v1/bans", tok, session, banBody("instances", []string{"beta"}, 24*time.Hour))
	assert.Equal(t, 400, code, string(b))
	viewerOnB := w.login(steamB, g("alpha", RoleModerator), g("beta", RoleViewer))
	code, b = w.as("POST", "/api/v1/bans", tok, viewerOnB, banBody("instances", []string{"beta"}, 24*time.Hour))
	assert.Equal(t, 403, code, "a role on B without ban.instance: %s", b)
	code, b = w.as("POST", "/api/v1/bans", tok, session, banBody("instances", []string{"alpha"}, 24*time.Hour))
	require.Equal(t, 201, code, string(b))
	code, b = w.as("POST", "/api/v1/bans", tok, session, banBody("instances", []string{"alpha", "beta"}, 24*time.Hour))
	assert.Equal(t, 400, code, "a ban for A and B needs the permission on both: %s", b)
	code, b = w.as("POST", "/api/v1/bans", tok, viewerOnB, banBody("instances", []string{"alpha", "beta"}, 24*time.Hour))
	assert.Equal(t, 403, code, "a ban for A and B needs the permission on both: %s", b)

	// bans on B exist, the moderator does not see them
	code, _ = e.do("POST", "/api/v1/bans", admin, banBody("instances", []string{"beta"}, 24*time.Hour))
	require.Equal(t, 201, code)
	code, b = w.as("GET", "/api/v1/bans", tok, session, "")
	require.Equal(t, 200, code)
	var bans []moderation.Ban
	require.NoError(t, json.Unmarshal(b, &bans))
	require.Len(t, bans, 1, "only the ban on A")
	assert.Equal(t, []string{"alpha"}, bans[0].Instances)
	code, _ = w.as("GET", "/api/v1/bans/2", tok, session, "")
	assert.Equal(t, 404, code, "the ban on B")
	code, _ = w.as("POST", "/api/v1/bans/2/revoke", tok, session, `{"reason":"x"}`)
	assert.Equal(t, 404, code)

	// the player lists show only A
	code, b = w.as("GET", "/api/v1/players", tok, session, "")
	require.Equal(t, 200, code)
	assert.Contains(t, string(b), steamA)
	assert.NotContains(t, string(b), steamB, "bob plays on B only")
	code, _ = w.as("GET", "/api/v1/players/"+steamB, tok, session, "")
	assert.Equal(t, 404, code)
	code, b = w.as("GET", "/api/v1/players/"+steamA, tok, session, "")
	require.Equal(t, 200, code)
	assert.NotContains(t, string(b), `"beta"`)

	// and the instance routes
	code, _ = w.as("GET", "/api/v1/instances/beta/players", tok, session, "")
	assert.Equal(t, 404, code)
	code, _ = w.as("POST", "/api/v1/instances/alpha/players/"+steamA+"/kill", tok, session, "")
	assert.Equal(t, 403, code, "a moderator may not kill")
}

func TestIPsPerInstance(t *testing.T) {
	w := newWebEnv(t)
	g0, _ := moderation.DeriveGUID(steamA)
	require.NoError(t, w.m.st.Seen(t.Context(), "alpha", g0, "192.0.2.7", "alice", banNow))
	require.NoError(t, w.m.st.Seen(t.Context(), "beta", g0, "192.0.2.8", "alice", banNow))
	tok := w.webToken(RoleAdmin)
	session := w.login(steamC, g("alpha", RoleAdmin), g("beta", RoleModerator))
	code, b := w.as("GET", "/api/v1/players/"+steamA+"/identities", tok, session, "")
	require.Equal(t, 200, code)
	var ids []moderation.Sighting
	require.NoError(t, json.Unmarshal(b, &ids))
	require.Len(t, ids, 2)
	got := map[string]string{}
	for _, x := range ids {
		got[x.Instance] = x.IP
	}
	assert.Equal(t, "192.0.2.7", got["alpha"], "ip.history holds on A")
	assert.Empty(t, got["beta"], "but not on B")
}

func TestIPsInABanNeedThePermissionOnEveryInstance(t *testing.T) {
	w := newWebEnv(t)
	adminTok := w.token(RoleAdmin, nil, false)
	code, b := w.do("POST", "/api/v1/bans", adminTok, banBody("instances", []string{"alpha", "beta"}, 0, "192.0.2.7"))
	require.Equal(t, 201, code, string(b))
	tok := w.webToken(RoleAdmin)
	both := w.login(steamC, g("alpha", RoleAdmin), g("beta", RoleAdmin))
	onlyA := w.login(steamB, g("alpha", RoleAdmin), g("beta", RoleViewer))
	_, b = w.as("GET", "/api/v1/bans/1", tok, both, "")
	assert.Contains(t, string(b), "192.0.2.7")
	code, b = w.as("GET", "/api/v1/bans/1", tok, onlyA, "")
	require.Equal(t, 200, code, "ban.view on one of its instances shows the ban")
	assert.NotContains(t, string(b), "192.0.2.7", "ip.history is missing on beta")
}

func TestGlobalBanNeedsAdminStar(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	onA := w.login(steamC, g("alpha", RoleAdmin))
	star := w.login(steamB, g("*", RoleAdmin))
	opStar := w.login(steamA, g("*", RoleOperator))
	body := banBody("global", nil, 0)
	code, _ := w.as("POST", "/api/v1/bans", tok, onA, body)
	assert.Equal(t, 403, code, "admin on A only")
	code, _ = w.as("POST", "/api/v1/bans", tok, opStar, body)
	assert.Equal(t, 403, code, "operator on star")
	code, b := w.as("POST", "/api/v1/bans", tok, star, body)
	assert.Equal(t, 201, code, string(b))
	// an admin token limited to alpha is no use either
	limited := w.webToken(RoleAdmin, "alpha")
	code, _ = w.as("POST", "/api/v1/bans", limited, star, body)
	assert.Equal(t, 403, code)
	// the audit log is installation-wide too
	code, _ = w.as("GET", "/api/v1/audit", tok, onA, "")
	assert.Equal(t, 403, code)
	code, _ = w.as("GET", "/api/v1/audit", tok, star, "")
	assert.Equal(t, 200, code)
}

func TestTokenCapsTheGrant(t *testing.T) {
	w := newWebEnv(t)
	session := w.login(steamA, g("*", RoleAdmin))
	opTok := w.webToken(RoleOperator)
	code, _ := w.as("GET", "/api/v1/audit", opTok, session, "")
	assert.Equal(t, 403, code, "an admin grant through an operator token is an operator")
	code, _ = w.as("POST", "/api/v1/instances/alpha/players/"+steamB+"/kill?wait=0", opTok, session, "")
	assert.NotEqual(t, 403, code, "operators may kill")
	limited := w.webToken(RoleAdmin, "alpha")
	code, _ = w.as("GET", "/api/v1/instances/beta", limited, session, "")
	assert.Equal(t, 404, code, "the token does not allow beta")
	code, _ = w.as("GET", "/api/v1/instances/alpha", limited, session, "")
	assert.Equal(t, 200, code)
}

func TestWebActorIsTheSteamID(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	session := w.login(steamC, g("alpha", RoleModerator))
	code, b := w.as("POST", "/api/v1/instances/alpha/players/"+steamA+"/kick", tok, session, `{"reason":"bye"}`, "X-Dzo-Actor", "mallory")
	require.Equal(t, 200, code, string(b))
	entries, err := w.srv.Audit.Tail("alpha", 10)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	last := entries[len(entries)-1]
	assert.Equal(t, "web:"+steamC, last.Actor, "X-Dzo-Actor is ignored for a web token")
	assert.NotContains(t, last.Actor, "mallory")
}

func TestDelegateTokenUnchanged(t *testing.T) {
	w := newWebEnv(t)
	tok := w.token(RoleOperator, nil, true)
	code, b := w.do("POST", "/api/v1/instances/alpha/players/"+steamA+"/kick", tok, `{"reason":"bye"}`, "X-Dzo-Actor", "ann", "X-Dzo-Source", "cli")
	require.Equal(t, 200, code, string(b))
	entries, err := w.srv.Audit.Tail("alpha", 10)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	last := entries[len(entries)-1]
	assert.Equal(t, "t-operator:ann", last.Actor)
	assert.Equal(t, "cli", last.Source)
	// and a session header means nothing to it
	code, _ = w.do("GET", "/api/v1", tok, "", "X-Dzo-Session", "dzs_whatever")
	assert.Equal(t, 200, code)
}

func TestPlainTokenKeepsItsOrderOfErrors(t *testing.T) {
	w := newWebEnv(t)
	viewer := w.token(RoleViewer, nil, false)
	code, _ := w.do("POST", "/api/v1/instances/nope/players/"+steamA+"/kick", viewer, `{"reason":"x"}`)
	assert.Equal(t, 403, code, "the permission is checked before the instance exists, as it always was")
	op := w.token(RoleOperator, nil, false)
	code, _ = w.do("POST", "/api/v1/instances/nope/players/"+steamA+"/kick", op, `{"reason":"x"}`)
	assert.Equal(t, 404, code)
}

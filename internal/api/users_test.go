// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

func putBody(name string, disabled bool, grants ...moderation.Grant) string {
	b, _ := json.Marshal(map[string]any{"name": name, "disabled": disabled, "grants": grants})
	return string(b)
}

func TestUsersNeedAdminStar(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	for name, grants := range map[string][]moderation.Grant{
		"viewer on star":      {g("*", RoleViewer)},
		"operator on star":    {g("*", RoleOperator)},
		"admin on one":        {g("alpha", RoleAdmin)},
		"admin on one, mod *": {g("alpha", RoleAdmin), g("*", RoleModerator)},
	} {
		session := w.login(steamB, grants...)
		code, _ := w.as("GET", "/api/v1/users", tok, session, "")
		assert.Equal(t, 403, code, name)
		code, _ = w.as("PUT", "/api/v1/users/"+steamA, tok, session, putBody("x", false))
		assert.Equal(t, 403, code, name)
	}
	star := w.login(steamC, g("*", RoleAdmin))
	code, _ := w.as("GET", "/api/v1/users", tok, star, "")
	assert.Equal(t, 200, code)
	code, _ = w.as("GET", "/api/v1/users", w.webToken(RoleOperator), star, "")
	assert.Equal(t, 403, code, "an operator token caps an admin grant")
	code, _ = w.do("GET", "/api/v1/users", w.token(RoleAdmin, nil, false), "")
	assert.Equal(t, 200, code, "a plain admin token may, as it may for everything")
	code, _ = w.do("GET", "/api/v1/users", w.token(RoleOperator, nil, false), "")
	assert.Equal(t, 403, code)
}

func TestListUsersHasNoSecrets(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("*", RoleAdmin))
	session := l.loggedIn()
	code, b := l.as("GET", "/api/v1/users", l.tok, session, "")
	require.Equal(t, 200, code)
	var people []moderation.Person
	require.NoError(t, json.Unmarshal(b, &people))
	require.Len(t, people, 1)
	assert.Equal(t, "ann", people[0].Username)
	assert.Equal(t, []moderation.Grant{g("*", RoleAdmin)}, people[0].Grants)
	for _, secret := range []string{"argon2id", string(totpSecret), "password"} {
		assert.NotContains(t, string(b), secret)
	}
}

func TestPutUserCreatesAndAudits(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	star := w.login(steamC, g("*", RoleAdmin))
	code, b := w.as("PUT", "/api/v1/users/"+steamA, tok, star, putBody("Ann", false, g("alpha", RoleOperator), g("*", RoleViewer)))
	require.Equal(t, 200, code, string(b))
	p, err := w.m.st.Person(t.Context(), steamA)
	require.NoError(t, err)
	assert.Equal(t, "Ann", p.Name)
	assert.Equal(t, []moderation.Grant{g("*", RoleViewer), g("alpha", RoleOperator)}, p.Grants)
	entries, _ := w.srv.Audit.Tail("", 5)
	require.NotEmpty(t, entries)
	assert.Equal(t, "users.put", entries[0].Action)
	assert.Equal(t, steamA, entries[0].Target)
	assert.Equal(t, "web:"+steamC, entries[0].Actor)
	assert.Contains(t, entries[0].Detail, "alpha=operator")
	assert.Contains(t, entries[0].Detail, "*=viewer")

	code, _ = w.as("PUT", "/api/v1/users/"+steamA, tok, star, putBody("Ann", true))
	require.Equal(t, 200, code)
	p, _ = w.m.st.Person(t.Context(), steamA)
	assert.True(t, p.Disabled)
	assert.Empty(t, p.Grants)
}

func TestPutUserValidates(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	star := w.login(steamC, g("*", RoleAdmin))
	put := func(id, body string) int {
		code, _ := w.as("PUT", "/api/v1/users/"+id, tok, star, body)
		return code
	}
	assert.Equal(t, 400, put("123", putBody("x", false)), "not a Steam ID")
	assert.Equal(t, 400, put(steamA, putBody("x", false, g("alpha", "root"))), "unknown role")
	assert.Equal(t, 400, put(steamA, putBody("x", false, g("alpha", "detector"))), "a role that is no person's")
	assert.Equal(t, 400, put(steamA, putBody("x", false, g("a b", RoleViewer))), "instance name")
	assert.Equal(t, 400, put(steamA, putBody("x", false, g("", RoleViewer))), "instance name")
	assert.Equal(t, 400, put(steamA, putBody("x", false, g("alpha", RoleViewer), g("alpha", RoleAdmin))), "twice")
	assert.Equal(t, 400, put(steamA, `{"name":"x","bogus":1}`), "unknown field")
	assert.Equal(t, 400, put(steamA, `{"name":`), "bad JSON")
	assert.Equal(t, 400, put(steamA, putBody(strings.Repeat("x", 200), false)), "a name that is too long")
	assert.Equal(t, 200, put(steamA, putBody("ok", false, g("not-yet-existing", RoleViewer))), "a grant may name an instance that does not exist yet")
}

func TestCannotChangeOwnAccess(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	me := w.login(steamA, g("*", RoleAdmin))
	for name, body := range map[string]string{
		"demote":   putBody("A", false, g("*", RoleModerator)),
		"promote":  putBody("A", false, g("*", RoleAdmin), g("alpha", RoleAdmin)),
		"drop":     putBody("A", false),
		"disable":  putBody("A", true, g("*", RoleAdmin)),
		"narrow":   putBody("A", false, g("alpha", RoleAdmin)),
		"same but": putBody("A", false, g("*", RoleAdmin), g("beta", RoleViewer)),
	} {
		code, b := w.as("PUT", "/api/v1/users/"+steamA, tok, me, body)
		assert.Equal(t, 409, code, name)
		assert.Contains(t, string(b), "you cannot change your own access", name)
	}
	p, _ := w.m.st.Person(t.Context(), steamA)
	assert.Equal(t, []moderation.Grant{g("*", RoleAdmin)}, p.Grants, "nothing changed")
	assert.False(t, p.Disabled)

	code, b := w.as("PUT", "/api/v1/users/"+steamA, tok, me, putBody("Anna", false, g("*", RoleAdmin)))
	assert.Equal(t, 200, code, "only the display name changes: "+string(b))
	p, _ = w.m.st.Person(t.Context(), steamA)
	assert.Equal(t, "Anna", p.Name)

	code, _ = w.as("DELETE", "/api/v1/users/"+steamA, tok, me, "")
	assert.Equal(t, 409, code)
	code, _ = w.as("POST", "/api/v1/users/"+steamA+"/reset", tok, me, "")
	assert.Equal(t, 409, code)
	_, err := w.m.st.Person(t.Context(), steamA)
	require.NoError(t, err)

	// another admin may
	other := w.login(steamB, g("*", RoleAdmin))
	code, _ = w.as("PUT", "/api/v1/users/"+steamA, tok, other, putBody("A", false, g("alpha", RoleViewer)))
	assert.Equal(t, 200, code)
}

func TestResetEndsSessions(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	annSession := l.loggedIn()
	boss := l.login(steamB, g("*", RoleAdmin))
	code, _ := l.as("POST", "/api/v1/users/"+steamA+"/reset", l.tok, boss, "")
	require.Equal(t, 204, code)
	code, _ = l.as("GET", "/api/v1", l.tok, annSession, "")
	assert.Equal(t, 401, code, "her sessions are gone")
	creds, _ := l.m.st.Credentials(t.Context(), steamA)
	assert.Equal(t, moderation.Credentials{}, creds)
	p, _ := l.m.st.Person(t.Context(), steamA)
	assert.Equal(t, []moderation.Grant{g("alpha", RoleModerator)}, p.Grants, "the grants stay")
	code, _ = l.as("POST", "/api/v1/users/"+steamC+"/reset", l.tok, boss, "")
	assert.Equal(t, 404, code)
	entries, _ := l.srv.Audit.Tail("", 5)
	assert.Equal(t, "users.reset", entries[0].Action)
}

func TestDeleteUser(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleAdmin)
	boss := w.login(steamB, g("*", RoleAdmin))
	victim := w.login(steamA, g("alpha", RoleViewer))
	code, _ := w.as("DELETE", "/api/v1/users/"+steamA, tok, boss, "")
	assert.Equal(t, 204, code)
	code, _ = w.as("GET", "/api/v1", tok, victim, "")
	assert.Equal(t, 401, code)
	code, _ = w.as("DELETE", "/api/v1/users/"+steamA, tok, boss, "")
	assert.Equal(t, 404, code)
}

func TestInfoReportsRolesPerInstance(t *testing.T) {
	w := newWebEnv(t)
	tok := w.webToken(RoleOperator)
	session := w.login(steamA, g("*", RoleAdmin), g("beta", RoleViewer))
	code, b := w.as("GET", "/api/v1", tok, session, "")
	require.Equal(t, 200, code, string(b))
	var info Info
	require.NoError(t, json.Unmarshal(b, &info))
	require.NotNil(t, info.User)
	assert.Equal(t, steamA, info.User.SteamID)
	assert.Equal(t, map[string]Role{"alpha": RoleOperator, "beta": RoleOperator}, info.User.Roles, "capped by the operator token")
	assert.Empty(t, info.User.Installation, "no installation-wide permission through an operator token")

	admin := w.login(steamB, g("alpha", RoleModerator), g("beta", RoleViewer))
	_, b = w.as("GET", "/api/v1", w.webToken(RoleAdmin), admin, "")
	require.NoError(t, json.Unmarshal(b, &info))
	assert.Equal(t, map[string]Role{"alpha": RoleModerator, "beta": RoleViewer}, info.User.Roles)

	star := w.login(steamC, g("*", RoleAdmin))
	_, b = w.as("GET", "/api/v1", w.webToken(RoleAdmin), star, "")
	info = Info{}
	require.NoError(t, json.Unmarshal(b, &info))
	assert.ElementsMatch(t, []string{PermAuditView, PermBanGlobal, PermBanRevokeAny, PermUsersManage}, info.User.Installation)

	_, b = w.do("GET", "/api/v1", w.token(RoleOperator, nil, false), "")
	info = Info{}
	require.NoError(t, json.Unmarshal(b, &info))
	assert.Nil(t, info.User, "a token without a person has no user part")
	assert.Equal(t, RoleOperator, info.Role)
}

// streamEnds opens the stream of alpha as the session and reports whether the server closes it within a while.
func streamEnds(t *testing.T, w *webEnv, tok, session string, trigger func()) bool {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", w.ts.URL+"/api/v1/instances/alpha/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Dzo-Session", session)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
	ended := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
		}
		close(ended)
	}()
	trigger()
	select {
	case <-ended:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

func TestStreamClosesAfterRevoke(t *testing.T) {
	w := newWebEnv(t)
	w.srv.Recheck = 10 * time.Millisecond
	tok := w.webToken(RoleAdmin)
	session := w.login(steamA, g("alpha", RoleViewer), g("beta", RoleViewer))
	assert.True(t, streamEnds(t, w, tok, session, func() {
		// the grant on alpha goes, the session stays: the stream has to notice the grant
		require.NoError(t, w.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamA, Grants: []moderation.Grant{g("beta", RoleViewer)}}, banNow))
	}), "revoked grant")
}

func TestStreamClosesAfterDisable(t *testing.T) {
	w := newWebEnv(t)
	w.srv.Recheck = 10 * time.Millisecond
	tok := w.webToken(RoleAdmin)
	session := w.login(steamA, g("alpha", RoleViewer))
	assert.True(t, streamEnds(t, w, tok, session, func() {
		require.NoError(t, w.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamA, Disabled: true, Grants: []moderation.Grant{g("alpha", RoleViewer)}}, banNow))
	}))
}

func TestStreamClosesAfterLogout(t *testing.T) {
	w := newWebEnv(t)
	w.srv.Recheck = 10 * time.Millisecond
	tok := w.webToken(RoleAdmin)
	session := w.login(steamA, g("alpha", RoleViewer))
	assert.True(t, streamEnds(t, w, tok, session, func() {
		require.NoError(t, w.m.st.DeleteSession(t.Context(), auth.HashSecret(session)))
	}))
}

func TestStreamKeepsRunningWhileAllowed(t *testing.T) {
	w := newWebEnv(t)
	w.srv.Recheck = 10 * time.Millisecond
	tok := w.webToken(RoleAdmin)
	session := w.login(steamA, g("alpha", RoleViewer))
	w.srv.Login.SessionIdle = time.Hour
	ctxDone := make(chan struct{})
	req, _ := http.NewRequestWithContext(t.Context(), "GET", w.ts.URL+"/api/v1/instances/alpha/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Dzo-Session", session)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	go func() { defer close(ctxDone); _, _ = bufio.NewReader(resp.Body).ReadString(0) }()
	require.Never(t, func() bool {
		select {
		case <-ctxDone:
			return true
		default:
			return false
		}
	}, 200*time.Millisecond, 10*time.Millisecond, "the stream closed although the person is still allowed")
}

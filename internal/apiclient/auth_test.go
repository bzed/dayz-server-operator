// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"encoding/base32"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const (
	steamA   = "76561198000000001"
	steamB   = "76561198000000002"
	webURL   = "https://dzo.example"
	returnTo = webURL + "/login/steam/return?state=x"
	secretPW = "correct horse battery"
)

var totpSecret = []byte("12345678901234567890")

type webEnv struct {
	t     *testing.T
	st    *moderation.Store
	srv   *api.Server
	steam *httptest.Server
	c     *Client // the web token, no session
	nonce int
}

func newWebEnv(t *testing.T) *webEnv {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("site", "v0", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(t.Context(), filepath.Join(dir, "dzo.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	steam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ns:http://specs.openid.net/auth/2.0\nis_valid:true\n"))
	}))
	t.Cleanup(steam.Close)
	srv.Accounts = st
	srv.Login = api.LoginSettings{WebURL: webURL, Steam: auth.Steam{Endpoint: steam.URL, HTTP: steam.Client()}}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	secret, _, err := srv.Tokens.Create("web", api.RoleAdmin, nil, false, true, time.Hour)
	require.NoError(t, err)
	return &webEnv{t: t, st: st, srv: srv, steam: steam, c: New(ts.URL, secret)}
}

func (w *webEnv) ann(grants ...moderation.Grant) {
	w.t.Helper()
	ctx := w.t.Context()
	require.NoError(w.t, w.st.PutPerson(ctx, moderation.Person{SteamID: steamA, Name: "Ann", Grants: grants}, time.Now()))
	h, err := auth.HashPassword(secretPW)
	require.NoError(w.t, err)
	require.NoError(w.t, w.st.SetPassword(ctx, steamA, "ann", h))
	require.NoError(w.t, w.st.SetTOTP(ctx, steamA, totpSecret))
	_, hashes, err := auth.NewRecoveryCodes()
	require.NoError(w.t, err)
	require.NoError(w.t, w.st.SetRecoveryCodes(ctx, steamA, hashes))
}

func (w *webEnv) steamParams(id string) map[string][]string {
	w.nonce++
	return map[string][]string{
		"openid.ns": {"http://specs.openid.net/auth/2.0"}, "openid.mode": {"id_res"}, "openid.op_endpoint": {w.steam.URL},
		"openid.claimed_id": {"https://steamcommunity.com/openid/id/" + id}, "openid.identity": {"https://steamcommunity.com/openid/id/" + id},
		"openid.return_to": {returnTo}, "openid.response_nonce": {"n" + id + strconv.Itoa(w.nonce)},
		"openid.signed": {"signed,op_endpoint,claimed_id,identity,return_to,response_nonce"}, "openid.sig": {"c2ln"},
	}
}

func TestForUserSendsTheHeaders(t *testing.T) {
	var mu sync.Mutex
	var got http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Clone()
		mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(ts.Close)
	base := New(ts.URL, "tok")
	u := base.ForUser("dzs_abc", "203.0.113.7")
	require.NoError(t, u.get(t.Context(), "/api/v1", &struct{}{}))
	mu.Lock()
	assert.Equal(t, "dzs_abc", got.Get("X-Dzo-Session"))
	assert.Equal(t, "203.0.113.7", got.Get("X-Dzo-Client"))
	assert.Equal(t, "Bearer tok", got.Get("Authorization"))
	assert.Empty(t, got.Get("X-Dzo-Actor"), "the person is named by the session, not by a header")
	mu.Unlock()

	require.NoError(t, base.get(t.Context(), "/api/v1", &struct{}{}))
	mu.Lock()
	assert.Empty(t, got.Get("X-Dzo-Session"), "the original client is unchanged")
	assert.Empty(t, got.Get("X-Dzo-Client"))
	mu.Unlock()

	proxied := u.ForProxy("10.0.0.1")
	require.NoError(t, proxied.get(t.Context(), "/api/v1", &struct{}{}))
	mu.Lock()
	assert.Equal(t, "10.0.0.1", got.Get("X-Dzo-Proxy"))
	assert.Equal(t, "dzs_abc", got.Get("X-Dzo-Session"))
	mu.Unlock()
}

func TestLoginAndAccountAgainstTheServer(t *testing.T) {
	w := newWebEnv(t)
	w.ann(moderation.Grant{Instance: "*", Role: "admin"})
	ctx := t.Context()
	c := w.c.ForUser("", "198.51.100.1")

	p, err := c.LoginPassword(ctx, "ann", secretPW)
	require.NoError(t, err)
	assert.Equal(t, []string{"totp", "recovery"}, p.Factors)
	sess, err := c.LoginTOTP(ctx, p.Pending, auth.TOTPCode(totpSecret, time.Now()))
	require.NoError(t, err)
	assert.NotEmpty(t, sess.Session)

	u := w.c.ForUser(sess.Session, "198.51.100.1")
	info, err := u.Info(ctx)
	require.NoError(t, err)
	require.NotNil(t, info.User)
	assert.Equal(t, steamA, info.User.SteamID)
	assert.Equal(t, api.RoleAdmin, info.User.Roles["alpha"])

	acc, err := u.Account(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann", acc.Username)
	assert.True(t, acc.TOTP)

	// users
	_, err = u.PutUser(ctx, steamB, api.UserBody{Name: "Bob", Grants: []moderation.Grant{{Instance: "alpha", Role: "viewer"}}})
	require.NoError(t, err)
	people, err := u.Users(ctx)
	require.NoError(t, err)
	assert.Len(t, people, 2)
	require.NoError(t, u.ResetUser(ctx, steamB))
	require.NoError(t, u.DeleteUser(ctx, steamB))
	_, err = u.PutUser(ctx, steamA, api.UserBody{Name: "Ann"})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 409, apiErr.Status, "nobody changes their own access")

	// account
	require.NoError(t, u.SetPassword(ctx, "ann", secretPW, "another horse battery"))
	require.NoError(t, u.RemovePassword(ctx))
	codes, err := u.NewRecoveryCodes(ctx)
	require.NoError(t, err)
	assert.Len(t, codes, 10)
	require.NoError(t, u.LogoutOthers(ctx))
	err = u.RemoveTOTP(ctx)
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 409, apiErr.Status, "the last second factor")

	// replace TOTP
	setup, err := u.TOTPBegin(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, setup.Pending)
	assert.NotEmpty(t, setup.Secret)
	assert.Contains(t, setup.URI, "otpauth://")
	err = u.TOTPFinish(ctx, setup.Pending, "000000")
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 401, apiErr.Status)

	// recovery code login, and logout
	p, err = c.LoginPassword(ctx, "ann", "another horse battery")
	require.Error(t, err, "the password was removed")
	require.NoError(t, u.Logout(ctx))
	_, err = u.Info(ctx)
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 401, apiErr.Status, "a logged-out session")
	_ = p
}

func TestSteamLoginAndEnrolment(t *testing.T) {
	w := newWebEnv(t)
	require.NoError(t, w.st.PutPerson(t.Context(), moderation.Person{SteamID: steamA, Name: "Ann", Grants: []moderation.Grant{{Instance: "alpha", Role: "viewer"}}}, time.Now()))
	ctx := t.Context()
	c := w.c.ForUser("", "198.51.100.1")

	u, err := c.SteamURL(ctx, returnTo)
	require.NoError(t, err)
	assert.Contains(t, u, w.steam.URL)
	_, err = c.SteamURL(ctx, "https://evil.example/")
	require.Error(t, err)

	p, err := c.LoginSteam(ctx, w.steamParams(steamA), returnTo)
	require.NoError(t, err)
	assert.True(t, p.Enrol)

	setup, err := c.EnrolTOTPBegin(ctx, p.Pending)
	require.NoError(t, err)
	secret, err := decodeBase32(setup.Secret)
	require.NoError(t, err)
	sess, err := c.EnrolTOTPFinish(ctx, p.Pending, auth.TOTPCode(secret, time.Now()))
	require.NoError(t, err)
	assert.NotEmpty(t, sess.Session)
	assert.Len(t, sess.RecoveryCodes, 10)

	// sign in again and finish with a recovery code
	p, err = c.LoginSteam(ctx, w.steamParams(steamA), returnTo)
	require.NoError(t, err)
	assert.False(t, p.Enrol)
	sess2, err := c.LoginRecovery(ctx, p.Pending, sess.RecoveryCodes[0])
	require.NoError(t, err)
	assert.NotEmpty(t, sess2.Session)
}

func TestTooManyFailuresIsAnError429(t *testing.T) {
	w := newWebEnv(t)
	w.ann(moderation.Grant{Instance: "*", Role: "admin"})
	c := w.c.ForUser("", "198.51.100.1")
	var last error
	for i := 0; i < 7; i++ {
		_, last = c.LoginPassword(t.Context(), "ann", "wrong horse battery")
	}
	var apiErr *Error
	require.ErrorAs(t, last, &apiErr)
	assert.Equal(t, 429, apiErr.Status)
}

// TestPasskeyCallsPassTheJSONThrough checks the paths, bodies and answers of the WebAuthn calls against a stand-in.
func TestPasskeyCallsPassTheJSONThrough(t *testing.T) {
	type seen struct {
		method, path, body string
	}
	var mu sync.Mutex
	var last seen
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = seen{r.Method, r.URL.Path, string(b)}
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/auth/passkey/begin", "/api/v1/auth/enrol/passkey/begin":
			_, _ = io.WriteString(w, `{"publicKey":{"challenge":"abc"}}`)
		case "/api/v1/auth/passkey/finish", "/api/v1/auth/enrol/passkey/finish":
			_, _ = io.WriteString(w, `{"session":"dzs_x","expires":"2026-10-30T12:00:00Z"}`)
		case "/api/v1/account/passkeys/begin":
			_, _ = io.WriteString(w, `{"pending":"dzp_p","options":{"publicKey":{"challenge":"def"}}}`)
		case "/api/v1/account/passkeys/finish":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"aWQ","name":"phone","created":"2026-10-28T12:00:00Z"}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "tok").ForUser("dzs_s", "")
	ctx := t.Context()
	get := func() seen { mu.Lock(); defer mu.Unlock(); return last }

	opts, err := c.PasskeyLoginBegin(ctx, "dzp_1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"publicKey":{"challenge":"abc"}}`, string(opts))
	assert.Equal(t, seen{"POST", "/api/v1/auth/passkey/begin", `{"pending":"dzp_1"}`}, get())

	resp := json.RawMessage(`{"id":"x","response":{"a":1}}`)
	sess, err := c.PasskeyLoginFinish(ctx, "dzp_1", resp)
	require.NoError(t, err)
	assert.Equal(t, "dzs_x", sess.Session)
	l := get()
	assert.Equal(t, "/api/v1/auth/passkey/finish", l.path)
	assert.JSONEq(t, `{"pending":"dzp_1","response":{"id":"x","response":{"a":1}}}`, l.body)

	opts, err = c.EnrolPasskeyBegin(ctx, "dzp_2")
	require.NoError(t, err)
	assert.Contains(t, string(opts), "challenge")
	assert.Equal(t, "/api/v1/auth/enrol/passkey/begin", get().path)
	_, err = c.EnrolPasskeyFinish(ctx, "dzp_2", "laptop", resp)
	require.NoError(t, err)
	assert.JSONEq(t, `{"pending":"dzp_2","response":{"id":"x","response":{"a":1}},"name":"laptop"}`, get().body)

	pending, opts, err := c.PasskeyBegin(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dzp_p", pending)
	assert.JSONEq(t, `{"publicKey":{"challenge":"def"}}`, string(opts))
	info, err := c.PasskeyFinish(ctx, "dzp_p", "phone", resp)
	require.NoError(t, err)
	assert.Equal(t, "phone", info.Name)

	require.NoError(t, c.RemovePasskey(ctx, "aWQ"))
	assert.Equal(t, seen{"DELETE", "/api/v1/account/passkeys/aWQ", ""}, get())
}

func decodeBase32(s string) ([]byte, error) {
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
}

func TestAuthInfo(t *testing.T) {
	w := newWebEnv(t)
	info, err := w.c.AuthInfo(t.Context())
	require.NoError(t, err)
	assert.True(t, info.Logins)
	assert.Equal(t, webURL, info.WebURL)
	assert.Equal(t, "site", info.Installation)
}

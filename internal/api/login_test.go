// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const (
	webURL   = "https://dzo.example"
	returnTo = webURL + "/login/steam/return?state=abc"
	password = "correct horse battery"
)

var totpSecret = []byte("12345678901234567890")

type loginEnv struct {
	*webEnv
	tok      string
	steam    *httptest.Server
	logs     *lockedLines
	recovery []string
	nonce    int
}

type lockedLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *lockedLines) add(line string) { l.mu.Lock(); l.lines = append(l.lines, line); l.mu.Unlock() }
func (l *lockedLines) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newLoginEnv(t *testing.T) *loginEnv {
	t.Helper()
	w := newWebEnv(t)
	steam := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		valid := r.PostForm.Get("openid.sig") == "good"
		_, _ = rw.Write([]byte("ns:http://specs.openid.net/auth/2.0\nis_valid:" + strconv.FormatBool(valid) + "\n"))
	}))
	t.Cleanup(steam.Close)
	logs := &lockedLines{}
	w.srv.Logf = func(f string, a ...any) { logs.add(strings.TrimSpace(fmt.Sprintf(f, a...))) }
	w.srv.Login = LoginSettings{WebURL: webURL, Steam: auth.Steam{Endpoint: steam.URL, HTTP: steam.Client()}}
	return &loginEnv{webEnv: w, tok: w.webToken(RoleAdmin), steam: steam, logs: logs}
}

// ann is a person who can log in with a password and TOTP.
func (l *loginEnv) ann(grants ...moderation.Grant) {
	l.t.Helper()
	ctx := l.t.Context()
	require.NoError(l.t, l.m.st.PutPerson(ctx, moderation.Person{SteamID: steamA, Name: "Ann", Grants: grants}, banNow))
	h, err := auth.HashPassword(password)
	require.NoError(l.t, err)
	require.NoError(l.t, l.m.st.SetPassword(ctx, steamA, "ann", h))
	require.NoError(l.t, l.m.st.SetTOTP(ctx, steamA, totpSecret))
	codes, hashes, err := auth.NewRecoveryCodes()
	require.NoError(l.t, err)
	l.recovery = codes
	require.NoError(l.t, l.m.st.SetRecoveryCodes(ctx, steamA, hashes))
}

func (l *loginEnv) post(path, body string, hdr ...string) (int, map[string]any, http.Header) {
	l.t.Helper()
	req, _ := http.NewRequest("POST", l.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+l.tok)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(l.t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

func (l *loginEnv) passwordStep(user, pw string, hdr ...string) (int, map[string]any, http.Header) {
	b, _ := json.Marshal(map[string]string{"username": user, "password": pw})
	return l.post("/api/v1/auth/password", string(b), hdr...)
}

func (l *loginEnv) totpCode() string {
	return auth.TOTPCode(totpSecret, l.srv.now())
}

func (l *loginEnv) second(kind, pending, code string) (int, map[string]any, http.Header) {
	b, _ := json.Marshal(map[string]string{"pending": pending, "code": code})
	return l.post("/api/v1/auth/"+kind, string(b))
}

// loggedIn runs password and TOTP and returns the session.
func (l *loginEnv) loggedIn() string {
	l.t.Helper()
	code, out, _ := l.passwordStep("ann", password)
	require.Equal(l.t, 200, code, "%v", out)
	code, out, _ = l.second("totp", out["pending"].(string), l.totpCode())
	require.Equal(l.t, 200, code, "%v", out)
	return out["session"].(string)
}

func TestPasswordThenTOTP(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	code, out, _ := l.passwordStep("Ann", password)
	require.Equal(t, 200, code, "%v", out)
	assert.Regexp(t, `^dzp_[0-9a-f]{64}$`, out["pending"])
	assert.Equal(t, []any{"totp", "recovery"}, out["factors"])
	assert.Equal(t, false, out["enrol"])

	code, out, _ = l.second("totp", out["pending"].(string), l.totpCode())
	require.Equal(t, 200, code, "%v", out)
	session := out["session"].(string)
	assert.Regexp(t, `^dzs_[0-9a-f]{64}$`, session)
	exp, err := time.Parse(time.RFC3339, out["expires"].(string))
	require.NoError(t, err)
	assert.Equal(t, banNow.Add(7*24*time.Hour).Unix(), exp.Unix(), "the maximum lifetime")

	code, b := l.as("GET", "/api/v1", l.tok, session, "")
	assert.Equal(t, 200, code, string(b))
}

func TestPasswordAloneGivesNoSession(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	_, out, _ := l.passwordStep("ann", password)
	pending := out["pending"].(string)
	code, _ := l.as("GET", "/api/v1", l.tok, pending, "")
	assert.Equal(t, 401, code, "a pending login is not a session")
	code, _, _ = l.second("totp", pending, "000000")
	assert.Equal(t, 401, code, "a wrong code")
	code, _ = l.as("GET", "/api/v1", l.tok, pending, "")
	assert.Equal(t, 401, code)
}

func TestPasswordWrongAndRefusedPeople(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	for name, c := range map[string][2]string{"wrong password": {"ann", "wrong horse battery"}, "unknown user": {"nobody", password}} {
		code, out, _ := l.passwordStep(c[0], c[1])
		assert.Equal(t, 401, code, name)
		assert.Equal(t, "invalid username or password", out["error"], name)
	}
	require.NoError(t, l.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamA, Disabled: true, Grants: []moderation.Grant{g("alpha", RoleModerator)}}, banNow))
	code, out, _ := l.passwordStep("ann", password)
	assert.Equal(t, 401, code, "a disabled person looks like a wrong password")
	assert.Equal(t, "invalid username or password", out["error"])
	require.NoError(t, l.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamA}, banNow))
	code, _, _ = l.passwordStep("ann", password)
	assert.Equal(t, 401, code, "so does one without a grant")
}

func TestTOTPReplayRefused(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	code := l.totpCode()
	_, out, _ := l.passwordStep("ann", password)
	st, _, _ := l.second("totp", out["pending"].(string), code)
	require.Equal(t, 200, st)
	_, out, _ = l.passwordStep("ann", password)
	st, _, _ = l.second("totp", out["pending"].(string), code)
	assert.Equal(t, 401, st, "the same code twice")
}

func TestTOTPDoubleSubmitGivesOneSession(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	_, o1, _ := l.passwordStep("ann", password)
	_, o2, _ := l.passwordStep("ann", password)
	code := l.totpCode()
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, p := range []string{o1["pending"].(string), o2["pending"].(string)} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _, _ := l.second("totp", p, code)
			results <- st
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for st := range results {
		if st == 200 {
			ok++
		}
	}
	assert.Equal(t, 1, ok)
}

func TestRecoveryCodeOnce(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	_, out, _ := l.passwordStep("ann", password)
	st, res, _ := l.second("recovery", out["pending"].(string), strings.ToUpper(l.recovery[0]))
	require.Equal(t, 200, st, "%v", res)
	assert.NotEmpty(t, res["session"])
	_, out, _ = l.passwordStep("ann", password)
	st, _, _ = l.second("recovery", out["pending"].(string), l.recovery[0])
	assert.Equal(t, 401, st, "a recovery code works once")
	st, _, _ = l.second("recovery", out["pending"].(string), l.recovery[1])
	assert.Equal(t, 200, st, "another one works")
}

func (l *loginEnv) steamQuery(id string) map[string][]string {
	l.nonce++
	return map[string][]string{
		"openid.ns":             {"http://specs.openid.net/auth/2.0"},
		"openid.mode":           {"id_res"},
		"openid.op_endpoint":    {l.steam.URL},
		"openid.claimed_id":     {"https://steamcommunity.com/openid/id/" + id},
		"openid.identity":       {"https://steamcommunity.com/openid/id/" + id},
		"openid.return_to":      {returnTo},
		"openid.response_nonce": {"2026-10-16T12:00:00Z" + id + strconv.Itoa(l.nonce)},
		"openid.assoc_handle":   {"1"},
		"openid.signed":         {"signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle"},
		"openid.sig":            {"good"},
	}
}

func (l *loginEnv) steamStep(id string) (int, map[string]any, http.Header) {
	return l.steamStepQ(l.steamQuery(id))
}

func (l *loginEnv) steamStepQ(q map[string][]string) (int, map[string]any, http.Header) {
	b, _ := json.Marshal(map[string]any{"params": q, "return_to": returnTo})
	return l.post("/api/v1/auth/steam", string(b))
}

func (l *loginEnv) get(path string) (int, map[string]any) {
	l.t.Helper()
	req, _ := http.NewRequest("GET", l.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+l.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(l.t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestSteamLoginKnownPerson(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	code, out := l.get("/api/v1/auth/steam?return_to=" + url.QueryEscape(returnTo))
	require.Equal(t, 200, code, "%v", out)
	u, err := url.Parse(out["url"].(string))
	require.NoError(t, err)
	assert.Equal(t, returnTo, u.Query().Get("openid.return_to"))
	assert.Equal(t, webURL, u.Query().Get("openid.realm"))

	code, out, _ = l.steamStep(steamA)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, false, out["enrol"])
	assert.Equal(t, []any{"totp", "recovery"}, out["factors"])
	code, out, _ = l.second("totp", out["pending"].(string), l.totpCode())
	require.Equal(t, 200, code, "%v", out)
	assert.NotEmpty(t, out["session"])
}

func TestSteamLoginWithoutSecondFactorMustEnrol(t *testing.T) {
	l := newLoginEnv(t)
	require.NoError(t, l.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamA, Grants: []moderation.Grant{g("alpha", RoleViewer)}}, banNow))
	code, out, _ := l.steamStep(steamA)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, true, out["enrol"])
	assert.Empty(t, out["factors"])
	code, _, _ = l.second("totp", out["pending"].(string), "123456")
	assert.Equal(t, 403, code, "an enrolment is not a login")
}

func TestSteamLoginUnknownNoRow(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	code, out, _ := l.steamStep(steamB)
	assert.Equal(t, 403, code, "%v", out)
	people, err := l.m.st.People(t.Context())
	require.NoError(t, err)
	assert.Len(t, people, 1, "no row for an unknown Steam ID")
	require.NoError(t, l.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamC}, banNow))
	code, _, _ = l.steamStep(steamC)
	assert.Equal(t, 403, code, "a person without any grant gets nothing either")
}

func TestSteamBadSignature(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	q := l.steamQuery(steamA)
	q["openid.sig"] = []string{"forged"}
	b, _ := json.Marshal(map[string]any{"params": q, "return_to": returnTo})
	code, _, _ := l.post("/api/v1/auth/steam", string(b))
	assert.Equal(t, 401, code)
}

func TestSteamNonceReplay(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	q := l.steamQuery(steamA)
	code, _, _ := l.steamStepQ(q)
	require.Equal(t, 200, code)
	code, _, _ = l.steamStepQ(q)
	assert.Equal(t, 401, code, "the same Steam response twice")
}

func TestSteamReturnToOutsideWebURL(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	for _, bad := range []string{"https://evil.example/login", "http://dzo.example/login", "https://dzo.example.evil.example/", "/login", "javascript:alert(1)", ""} {
		code, _ := l.get("/api/v1/auth/steam?return_to=" + url.QueryEscape(bad))
		assert.Equal(t, 400, code, bad)
		b, _ := json.Marshal(map[string]any{"params": l.steamQuery(steamA), "return_to": bad})
		code, _, _ = l.post("/api/v1/auth/steam", string(b))
		assert.Equal(t, 400, code, bad)
	}
}

func TestPendingExpires(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	_, out, _ := l.passwordStep("ann", password)
	l.srv.now = func() time.Time { return banNow.Add(5*time.Minute + time.Second) }
	code, _, _ := l.second("totp", out["pending"].(string), l.totpCode())
	assert.Equal(t, 401, code, "five minutes is all a half-finished login gets")
}

func TestBackoff429(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	for i := 0; i < 5; i++ {
		code, _, _ := l.passwordStep("ann", "wrong horse battery", "X-Dzo-Client", "198.51.100."+strconv.Itoa(i))
		require.Equal(t, 401, code, "free failure %d", i)
	}
	code, _, _ := l.passwordStep("ann", "wrong horse battery", "X-Dzo-Client", "198.51.100.9")
	assert.Equal(t, 401, code, "the sixth is counted, and slows the next")
	code, bodyKnown, h := l.passwordStep("ann", password, "X-Dzo-Client", "198.51.100.10")
	assert.Equal(t, 429, code, "now even the right password waits: %v", bodyKnown)
	assert.Equal(t, "1", h.Get("Retry-After"))

	for i := 0; i < 6; i++ {
		_, _, _ = l.passwordStep("ghost", "wrong horse battery", "X-Dzo-Client", "203.0.113."+strconv.Itoa(i))
	}
	code, bodyUnknown, h2 := l.passwordStep("ghost", password, "X-Dzo-Client", "203.0.113.50")
	assert.Equal(t, 429, code)
	assert.Equal(t, bodyKnown, bodyUnknown, "the same message whether or not the username exists")
	assert.Equal(t, h.Get("Retry-After"), h2.Get("Retry-After"))

	l.srv.now = func() time.Time { return banNow.Add(2 * time.Second) }
	code, _, _ = l.passwordStep("ann", password, "X-Dzo-Client", "198.51.100.11")
	assert.Equal(t, 200, code, "after the wait")
}

func TestBackoffPerClientAddress(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	for i := 0; i < 6; i++ {
		_, _, _ = l.passwordStep("user"+strconv.Itoa(i), "wrong horse battery", "X-Dzo-Client", "192.0.2.66")
	}
	code, _, _ := l.passwordStep("ann", password, "X-Dzo-Client", "192.0.2.66")
	assert.Equal(t, 429, code, "one address guessing many accounts is slowed")
	code, _, _ = l.passwordStep("ann", password, "X-Dzo-Client", "192.0.2.67")
	assert.Equal(t, 200, code, "another address is not")
}

func TestClientHeaderOnlyFromWebToken(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, b := l.as("POST", "/api/v1/instances/alpha/players/"+steamB+"/kick", l.tok, session, `{"reason":"bye"}`, "X-Dzo-Client", "203.0.113.7")
	require.Equal(t, 200, code, string(b))
	entries, err := l.srv.Audit.Tail("alpha", 10)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.7", entries[0].Client, "a web token names the browser's address")

	del := l.token(RoleOperator, nil, true)
	code, b = l.do("POST", "/api/v1/instances/alpha/players/"+steamB+"/kick", del, `{"reason":"bye"}`, "X-Dzo-Client", "203.0.113.8")
	require.Equal(t, 200, code, string(b))
	entries, _ = l.srv.Audit.Tail("alpha", 10)
	assert.Empty(t, entries[0].Client, "any other token cannot")

	code, _ = l.do("POST", "/api/v1/auth/password", del, `{"username":"ann","password":"x"}`)
	assert.Equal(t, 403, code, "the login routes are for web tokens")
	code, _ = l.do("POST", "/api/v1/auth/password", "", `{}`)
	assert.Equal(t, 401, code)
}

func TestLoginLogHasNoSecrets(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	_, bad, _ := l.passwordStep("ann", "wrong horse battery")
	_ = bad
	_, out, _ := l.passwordStep("ann", password, "X-Dzo-Client", "198.51.100.1", "X-Dzo-Proxy", "10.0.0.1")
	pending := out["pending"].(string)
	code := l.totpCode()
	_, res, _ := l.second("totp", pending, code)
	session := res["session"].(string)
	_, _, _ = l.second("recovery", pending, l.recovery[0])
	log := l.logs.all()
	require.NotEmpty(t, log)
	for _, secret := range []string{password, "wrong horse battery", code, session, pending, l.recovery[0], "dzs_", "dzp_", string(totpSecret)} {
		assert.NotContains(t, log, secret)
	}
	assert.Contains(t, log, "login: password failed steamid=")
	assert.Contains(t, log, "login: password ok steamid="+steamA+" client=198.51.100.1 proxy=10.0.0.1")
	assert.Contains(t, log, "login: totp ok steamid="+steamA)
	entries, err := l.srv.Audit.Tail("", 10)
	require.NoError(t, err)
	var seen bool
	for _, e := range entries {
		seen = seen || e.Action == "auth.login"
		assert.NotContains(t, e.Detail+e.Result+e.Target, "dzs_")
	}
	assert.True(t, seen, "auth.login is audited")
}

func TestLogout(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, _ := l.as("POST", "/api/v1/auth/logout", l.tok, session, "")
	assert.Equal(t, 204, code)
	code, _ = l.as("GET", "/api/v1", l.tok, session, "")
	assert.Equal(t, 401, code)
	entries, _ := l.srv.Audit.Tail("", 10)
	var seen bool
	for _, e := range entries {
		seen = seen || e.Action == "auth.logout"
	}
	assert.True(t, seen)
}

func TestLoginsOffWithoutWebURL(t *testing.T) {
	l := newLoginEnv(t)
	l.srv.Login.WebURL = ""
	code, out, _ := l.passwordStep("ann", password)
	assert.Equal(t, 503, code)
	assert.Contains(t, out["error"], "serve.web_url")
	l.srv.Accounts = nil
	code, _, _ = l.passwordStep("ann", password)
	assert.Equal(t, 503, code)
}

func TestSecondFactorBadInput(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	code, _, _ := l.second("totp", "", "123456")
	assert.Equal(t, 401, code)
	code, _, _ = l.second("totp", "dzp_nope", "123456")
	assert.Equal(t, 401, code)
	code, _, _ = l.post("/api/v1/auth/totp", `{"pending":`)
	assert.Equal(t, 400, code)
	code, _, _ = l.post("/api/v1/auth/password", `{"username":"","password":""}`)
	assert.Equal(t, 401, code)
}

func TestAuthInfo(t *testing.T) {
	l := newLoginEnv(t)
	code, out := l.get("/api/v1/auth/info")
	require.Equal(t, 200, code)
	assert.Equal(t, "test-site", out["installation"])
	assert.Equal(t, true, out["logins"])
	assert.Equal(t, webURL, out["web_url"])

	l.srv.Login.WebURL = ""
	code, out = l.get("/api/v1/auth/info")
	require.Equal(t, 200, code, "the web interface can ask while logins are off")
	assert.Equal(t, false, out["logins"])
	assert.Empty(t, out["web_url"])

	code, _ = l.do("GET", "/api/v1/auth/info", l.token(RoleAdmin, nil, true), "")
	assert.Equal(t, 403, code, "web tokens only")
	code, _ = l.do("GET", "/api/v1/auth/info", "", "")
	assert.Equal(t, 401, code)
}

// A person whose password was set on the command line has no second factor yet: the password opens the
// enrolment of one, never a session.
func TestPasswordWithoutSecondFactorMustEnrol(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	require.NoError(t, l.m.st.ClearTOTP(t.Context(), steamA))
	require.NoError(t, l.m.st.SetRecoveryCodes(t.Context(), steamA, nil))
	code, out, _ := l.passwordStep("ann", password)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, true, out["enrol"])
	assert.Empty(t, out["factors"])
	pending := out["pending"].(string)
	st, _, _ := l.second("totp", pending, "123456")
	assert.Equal(t, 403, st, "no login without a second factor")
	st, _ = l.as("GET", "/api/v1", l.tok, pending, "")
	assert.Equal(t, 401, st)
	session, _, recovery := l.enrolTOTP(pending)
	assert.Len(t, recovery, 10)
	st, _ = l.as("GET", "/api/v1", l.tok, session, "")
	assert.Equal(t, 200, st, "enrolling ends in a session")
}

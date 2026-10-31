// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"encoding/base32"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const (
	loginSteamA = "76561198000000001"
	loginSteamB = "76561198000000002"
	loginPW     = "correct horse battery"
)

var loginTOTP = []byte("12345678901234567890")

// loginEnv is the web interface in front of a real API with user logins, without a session injected: the
// tests log in through the pages.
type loginEnv struct {
	t     *testing.T
	web   *httptest.Server
	srv   *Server
	api   *api.Server
	st    *moderation.Store
	steam *httptest.Server
	nonce int
	// signIn is the Steam ID the fake Steam signs the browser in as when it is sent there (browser tests).
	signIn atomic.Value
}

func newLoginEnv(t *testing.T, opts ...func(*Options)) *loginEnv {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	apiSrv := api.New("site-a", "v0", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(t.Context(), filepath.Join(dir, "dzo.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	apiSrv.Moderation = webMod{st}
	apiSrv.Accounts = st
	l := &loginEnv{t: t}
	l.signIn.Store(loginSteamA)
	var steam *httptest.Server
	steam = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("openid.mode") == "checkid_setup" { // the sign-in page: send the browser back
			id := l.signIn.Load().(string)
			back, err := url.Parse(r.URL.Query().Get("openid.return_to"))
			if err != nil {
				http.Error(w, "bad return_to", http.StatusBadRequest)
				return
			}
			q := back.Query()
			for k, v := range map[string]string{
				"openid.ns": "http://specs.openid.net/auth/2.0", "openid.mode": "id_res", "openid.op_endpoint": steam.URL,
				"openid.claimed_id": "https://steamcommunity.com/openid/id/" + id, "openid.identity": "https://steamcommunity.com/openid/id/" + id,
				"openid.return_to": r.URL.Query().Get("openid.return_to"), "openid.response_nonce": "n" + id + randomToken(),
				"openid.assoc_handle": "1", "openid.signed": "signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle", "openid.sig": "good",
			} {
				q.Set(k, v)
			}
			back.RawQuery = q.Encode()
			http.Redirect(w, r, back.String(), http.StatusFound)
			return
		}
		_ = r.ParseForm()
		_, _ = w.Write([]byte("ns:http://specs.openid.net/auth/2.0\nis_valid:" + strconv.FormatBool(r.PostForm.Get("openid.sig") == "good") + "\n"))
	}))
	t.Cleanup(steam.Close)
	ats := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ats.Close)
	secret, _, err := apiSrv.Tokens.Create("web", api.RoleAdmin, nil, false, true, time.Hour)
	require.NoError(t, err)
	o := Options{Version: "v0"}
	for _, f := range opts {
		f(&o)
	}
	srv, err := New(&Backend{Name: "main", Client: apiclient.New(ats.URL, secret)}, o)
	require.NoError(t, err)
	ws := httptest.NewServer(srv.Handler())
	t.Cleanup(ws.Close)
	apiSrv.Login = api.LoginSettings{WebURL: ws.URL, Steam: auth.Steam{Endpoint: steam.URL, HTTP: steam.Client()}}
	l.web, l.srv, l.api, l.st, l.steam = ws, srv, apiSrv, st, steam
	return l
}

// ann is a person who logs in with a password and TOTP.
func (l *loginEnv) ann(role string) {
	l.t.Helper()
	ctx := l.t.Context()
	require.NoError(l.t, l.st.PutPerson(ctx, moderation.Person{SteamID: loginSteamA, Name: "Ann", Grants: []moderation.Grant{{Instance: "*", Role: role}}}, time.Now()))
	h, err := auth.HashPassword(loginPW)
	require.NoError(l.t, err)
	require.NoError(l.t, l.st.SetPassword(ctx, loginSteamA, "ann", h))
	require.NoError(l.t, l.st.SetTOTP(ctx, loginSteamA, loginTOTP))
	_, hashes, err := auth.NewRecoveryCodes()
	require.NoError(l.t, err)
	require.NoError(l.t, l.st.SetRecoveryCodes(ctx, loginSteamA, hashes))
}

// visitor is a browser-like client with cookies that does not follow redirects.
type visitor struct {
	t   *testing.T
	l   *loginEnv
	c   *http.Client
	hdr map[string]string
}

func (l *loginEnv) visitor() *visitor {
	jar, err := cookiejar.New(nil)
	require.NoError(l.t, err)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 10 * time.Second}
	return &visitor{t: l.t, l: l, c: c, hdr: map[string]string{}}
}

func (b *visitor) do(method, path string, form url.Values, hdr ...string) (*result, string) {
	b.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(context.Background(), method, b.l.web.URL+path, body)
	require.NoError(b.t, err)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range b.hdr {
		req.Header.Set(k, v)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := b.c.Do(req)
	require.NoError(b.t, err)
	defer func() { _ = resp.Body.Close() }()
	var sb strings.Builder
	_, _ = io.Copy(&sb, resp.Body)
	return &result{StatusCode: resp.StatusCode, Header: resp.Header, cookies: resp.Cookies()}, sb.String()
}

// result is what a visitor keeps of a response; its body is read and closed already.
type result struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r *result) Cookies() []*http.Cookie { return r.cookies }

func (b *visitor) get(path string, hdr ...string) (*result, string) {
	b.t.Helper()
	return b.do("GET", path, nil, hdr...)
}

var (
	csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	csrfMeta  = regexp.MustCompile(`<meta name="csrf" content="([^"]+)"`)
)

// formCSRF reads the hidden token of a login form.
func formCSRF(t *testing.T, body string) string {
	t.Helper()
	m := csrfField.FindStringSubmatch(body)
	require.NotNil(t, m, "no csrf field in: %s", body)
	return m[1]
}

func (b *visitor) cookie(name string) *http.Cookie {
	u, _ := url.Parse(b.l.web.URL)
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login runs password and TOTP and ends with a session cookie.
func (b *visitor) login(next string) {
	b.t.Helper()
	path := "/login"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	_, body := b.get(path)
	resp, body2 := b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {formCSRF(b.t, body)}})
	require.Equal(b.t, 303, resp.StatusCode, body2)
	require.Equal(b.t, "/login/second", resp.Header.Get("Location"))
	_, body = b.get("/login/second")
	resp, body2 = b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {auth.TOTPCode(loginTOTP, time.Now())}, "csrf": {formCSRF(b.t, body)}})
	require.Equal(b.t, 303, resp.StatusCode, body2)
}

func TestRedirectsToLogin(t *testing.T) {
	l := newLoginEnv(t)
	b := l.visitor()
	resp, _ := b.get("/b/main/bans?state=active")
	require.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/login?next=%2Fb%2Fmain%2Fbans%3Fstate%3Dactive", resp.Header.Get("Location"))
	resp, _ = b.get("/")
	assert.Equal(t, "/login", resp.Header.Get("Location"), "no next for the front page")
	resp, _ = b.do("POST", "/b/main/i/alpha/message", url.Values{"text": {"x"}})
	assert.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
	resp, _ = b.get("/b/main/i/alpha/players", "HX-Request", "true")
	assert.Equal(t, 401, resp.StatusCode, "a fragment cannot follow a redirect")
	assert.Equal(t, "/login", resp.Header.Get("HX-Redirect"))

	for _, public := range []string{"/static/app.css", "/login"} {
		resp, _ = b.get(public)
		assert.Equal(t, 200, resp.StatusCode, public)
	}
}

func TestNextOnlyLocal(t *testing.T) {
	for next, want := range map[string]string{
		"/b/main/bans":         "/b/main/bans",
		"/b/main/bans?x=1":     "/b/main/bans?x=1",
		"https://evil.example": "/",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"javascript:alert(1)":  "/",
		"b/main":               "/",
		"/login":               "/",
		"":                     "/",
	} {
		l := newLoginEnv(t)
		l.ann("admin")
		b := l.visitor()
		_, body := b.get("/login?next=" + url.QueryEscape(next))
		resp, _ := b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {formCSRF(t, body)}})
		require.Equal(t, 303, resp.StatusCode, next)
		_, body = b.get("/login/second")
		resp, _ = b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {auth.TOTPCode(loginTOTP, time.Now())}, "csrf": {formCSRF(t, body)}})
		require.Equal(t, 303, resp.StatusCode, next)
		assert.Equal(t, want, resp.Header.Get("Location"), "next=%q", next)
	}
}

func TestPasswordTOTPLogin(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	_, body := b.get("/login")
	assert.Contains(t, body, "Sign in through Steam")
	assert.Contains(t, body, `name="password"`)

	resp, body2 := b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {"wrong horse battery"}, "csrf": {formCSRF(t, body)}})
	assert.Equal(t, 401, resp.StatusCode)
	assert.Contains(t, body2, "invalid username or password")
	assert.NotContains(t, body2, "wrong horse battery", "the password is not echoed")

	resp, _ = b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {"forged"}})
	assert.Equal(t, 403, resp.StatusCode, "a login form needs its token")

	resp, _ = b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {formCSRF(t, body)}})
	require.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/login/second", resp.Header.Get("Location"))
	assert.Nil(t, b.cookie("dzo_session"), "a password alone gives no session")
	resp, _ = b.get("/")
	assert.Equal(t, 303, resp.StatusCode, "and no access")

	_, body = b.get("/login/second")
	assert.Contains(t, body, `name="code"`)
	assert.NotContains(t, body, "passkey", "ann has none")
	resp, body2 = b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {"000000"}, "csrf": {formCSRF(t, body)}})
	assert.Equal(t, 401, resp.StatusCode)
	assert.Contains(t, body2, "invalid code")
	resp, _ = b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {auth.TOTPCode(loginTOTP, time.Now())}, "csrf": {formCSRF(t, body)}})
	require.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/", resp.Header.Get("Location"))

	var sc *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "dzo_session" {
			sc = c
		}
	}
	require.NotNil(t, sc, "the session cookie")
	assert.True(t, sc.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, sc.SameSite)
	assert.Equal(t, "/", sc.Path)
	assert.Empty(t, sc.Domain)
	assert.Positive(t, sc.MaxAge)
	assert.Regexp(t, `^dzs_[0-9a-f]{64}$`, sc.Value)
	assert.Nil(t, b.cookie("dzo_login"), "the login state is gone")

	resp, body = b.get("/")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, body, "Servers")
}

func TestLoginWithRecoveryCode(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	codes, hashes, err := auth.NewRecoveryCodes()
	require.NoError(t, err)
	require.NoError(t, l.st.SetRecoveryCodes(t.Context(), loginSteamA, hashes))
	b := l.visitor()
	_, body := b.get("/login")
	b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {formCSRF(t, body)}})
	_, body = b.get("/login/second")
	assert.Contains(t, body, "recovery")
	resp, _ := b.do("POST", "/login/second", url.Values{"kind": {"recovery"}, "code": {codes[0]}, "csrf": {formCSRF(t, body)}})
	assert.Equal(t, 303, resp.StatusCode)
	assert.NotNil(t, b.cookie("dzo_session"))
}

func TestSecondStepWithoutFirst(t *testing.T) {
	l := newLoginEnv(t)
	b := l.visitor()
	resp, _ := b.get("/login/second")
	assert.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
	resp, _ = b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {"123456"}, "csrf": {"x"}})
	assert.Equal(t, 303, resp.StatusCode)
	resp, _ = b.do("POST", "/login/passkey/begin", nil)
	assert.Equal(t, 401, resp.StatusCode)
	resp, _ = b.get("/login/enrol")
	assert.Equal(t, 303, resp.StatusCode)
}

func TestBackoffMessage(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	_, body := b.get("/login")
	csrf := formCSRF(t, body)
	var last int
	var text string
	for i := 0; i < 7; i++ {
		resp, body := b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {"wrong horse battery"}, "csrf": {csrf}})
		last, text = resp.StatusCode, body
	}
	assert.Equal(t, 429, last)
	assert.Contains(t, text, "too many failed logins")
}

func (l *loginEnv) steamParams(id, state string) url.Values {
	l.nonce++
	returnTo := l.web.URL + "/login/steam/return?state=" + state
	return url.Values{
		"state":                 {state},
		"openid.ns":             {"http://specs.openid.net/auth/2.0"},
		"openid.mode":           {"id_res"},
		"openid.op_endpoint":    {l.steam.URL},
		"openid.claimed_id":     {"https://steamcommunity.com/openid/id/" + id},
		"openid.identity":       {"https://steamcommunity.com/openid/id/" + id},
		"openid.return_to":      {returnTo},
		"openid.response_nonce": {"2026-10-29T12:00:00Z" + id + strconv.Itoa(l.nonce)},
		"openid.assoc_handle":   {"1"},
		"openid.signed":         {"signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle"},
		"openid.sig":            {"good"},
	}
}

// steamStart follows the "Sign in through Steam" button and returns the state of the round trip.
func (b *visitor) steamStart() string {
	b.t.Helper()
	resp, _ := b.get("/login/steam")
	require.Equal(b.t, 303, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(b.t, err)
	assert.Equal(b.t, b.l.steam.URL, loc.Scheme+"://"+loc.Host, "the browser is sent to Steam")
	rt, err := url.Parse(loc.Query().Get("openid.return_to"))
	require.NoError(b.t, err)
	assert.Equal(b.t, b.l.web.URL+"/login/steam/return", rt.Scheme+"://"+rt.Host+rt.Path)
	state := rt.Query().Get("state")
	require.NotEmpty(b.t, state)
	c := b.cookie("dzo_login")
	require.NotNil(b.t, c)
	return state
}

func TestSteamReturnNeedsState(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	state := b.steamStart()

	resp, _ := b.get("/login/steam/return?" + l.steamParams(loginSteamA, "forged").Encode())
	assert.Equal(t, 400, resp.StatusCode, "a state that is not ours")
	resp, _ = b.get("/login/steam/return?" + l.steamParams(loginSteamA, "").Encode())
	assert.Equal(t, 400, resp.StatusCode, "no state")
	other := l.visitor() // someone else's browser: no cookie at all
	resp, _ = other.get("/login/steam/return?" + l.steamParams(loginSteamA, state).Encode())
	assert.Equal(t, 400, resp.StatusCode, "an attacker's link followed by a victim has no state cookie")
	assert.Nil(t, other.cookie("dzo_session"))
	assert.Nil(t, b.cookie("dzo_session"))
}

func TestSteamLoginFullFlow(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	state := b.steamStart()
	resp, body := b.get("/login/steam/return?" + l.steamParams(loginSteamA, state).Encode())
	require.Equal(t, 200, resp.StatusCode, body)
	assert.Contains(t, body, `http-equiv="refresh"`, "a page that moves on by itself, so the next request is same-site")
	assert.Contains(t, body, "/login/second")
	assert.Nil(t, b.cookie("dzo_session"), "the second factor is still to come")

	resp, body = b.get("/login/second")
	require.Equal(t, 200, resp.StatusCode, body)
	resp, _ = b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {auth.TOTPCode(loginTOTP, time.Now())}, "csrf": {formCSRF(t, body)}})
	require.Equal(t, 303, resp.StatusCode)
	resp, _ = b.get("/")
	assert.Equal(t, 200, resp.StatusCode)
}

func TestSteamUnknownPersonGetsNoAccessPage(t *testing.T) {
	l := newLoginEnv(t)
	b := l.visitor()
	state := b.steamStart()
	resp, body := b.get("/login/steam/return?" + l.steamParams(loginSteamB, state).Encode())
	assert.Equal(t, 403, resp.StatusCode)
	assert.Contains(t, body, "no access")
	people, err := l.st.People(t.Context())
	require.NoError(t, err)
	assert.Empty(t, people, "nothing is stored")
}

func TestSteamForgedSignatureIsRefused(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	state := b.steamStart()
	q := l.steamParams(loginSteamA, state)
	q.Set("openid.sig", "forged")
	resp, _ := b.get("/login/steam/return?" + q.Encode())
	assert.Equal(t, 401, resp.StatusCode)
}

func TestEnrolmentThroughSteam(t *testing.T) {
	l := newLoginEnv(t)
	require.NoError(t, l.st.PutPerson(t.Context(), moderation.Person{SteamID: loginSteamB, Name: "Bob", Grants: []moderation.Grant{{Instance: "*", Role: "viewer"}}}, time.Now()))
	b := l.visitor()
	state := b.steamStart()
	resp, body := b.get("/login/steam/return?" + l.steamParams(loginSteamB, state).Encode())
	require.Equal(t, 200, resp.StatusCode, body)
	assert.Contains(t, body, "/login/enrol")

	resp, body = b.get("/login/enrol")
	require.Equal(t, 200, resp.StatusCode, body)
	assert.Regexp(t, `<img[^>]+src="data:image/png;base64,`, body, "the QR code")
	secretM := regexp.MustCompile(`<code id="totp-secret">([A-Z2-7]+)</code>`).FindStringSubmatch(body)
	require.NotNil(t, secretM, body)
	assert.Contains(t, body, "passkey.js", "a passkey can be registered instead")

	resp, body2 := b.do("POST", "/login/enrol", url.Values{"code": {"000000"}, "csrf": {formCSRF(t, body)}})
	assert.Equal(t, 401, resp.StatusCode)
	assert.Contains(t, body2, "invalid code")
	_, body = b.get("/login/enrol")
	secretM = regexp.MustCompile(`<code id="totp-secret">([A-Z2-7]+)</code>`).FindStringSubmatch(body)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretM[1])
	require.NoError(t, err)
	resp, body = b.do("POST", "/login/enrol", url.Values{"code": {auth.TOTPCode(secret, time.Now())}, "csrf": {formCSRF(t, body)}})
	require.Equal(t, 200, resp.StatusCode, body)
	pre := regexp.MustCompile(`(?s)<pre id="recovery-codes">(.*?)</pre>`).FindStringSubmatch(body)
	require.NotNil(t, pre, body)
	codes := regexp.MustCompile(`[a-z2-7]{5}-[a-z2-7]{5}`).FindAllString(pre[1], -1)
	assert.Len(t, codes, 10, "the recovery codes are shown once")
	assert.NotNil(t, b.cookie("dzo_session"), "enrolling ends in a session")
	resp, _ = b.get("/")
	assert.Equal(t, 200, resp.StatusCode)
	resp, _ = b.get("/login/enrol")
	assert.Equal(t, 303, resp.StatusCode, "the enrolment is over")
}

func TestCSRFPerSession(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	a, b := l.visitor(), l.visitor()
	a.login("")
	// a second login needs a fresh TOTP step
	l.srv.now = func() time.Time { return time.Now().Add(31 * time.Second) }
	_, bodyB := b.get("/login")
	resp, _ := b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {formCSRF(t, bodyB)}})
	require.Equal(t, 303, resp.StatusCode)
	_, bodyB = b.get("/login/second")
	resp, body := b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {auth.TOTPCode(loginTOTP, time.Now().Add(30*time.Second))}, "csrf": {formCSRF(t, bodyB)}})
	require.Equal(t, 303, resp.StatusCode, body)

	_, pageA := a.get("/")
	_, pageB := b.get("/")
	tokA, tokB := csrfMeta.FindStringSubmatch(pageA), csrfMeta.FindStringSubmatch(pageB)
	require.NotNil(t, tokA)
	require.NotNil(t, tokB)
	assert.NotEqual(t, tokA[1], tokB[1], "every session has its own token")

	post := func(br *visitor, tok string) int {
		resp, _ := br.do("POST", "/b/main/i/alpha/message", url.Values{"text": {"hi"}, "style": {"chat"}}, "X-CSRF-Token", tok)
		return resp.StatusCode
	}
	assert.Equal(t, 403, post(b, tokA[1]), "the token of the other session")
	assert.Equal(t, 403, post(b, ""), "no token")
	assert.NotEqual(t, 403, post(b, tokB[1]), "its own token")
}

func TestExpiredSessionRedirects(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	b.login("")
	resp, _ := b.get("/")
	require.Equal(t, 200, resp.StatusCode)
	sess := b.cookie("dzo_session")
	require.NotNil(t, sess)
	require.NoError(t, l.st.DeleteSession(t.Context(), auth.HashSecret(sess.Value)))
	l.srv.now = func() time.Time { return time.Now().Add(time.Minute) } // past the short cache of the session check
	resp, _ = b.get("/b/main/bans")
	require.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/login?next=%2Fb%2Fmain%2Fbans", resp.Header.Get("Location"))
	var cleared bool
	for _, c := range resp.Cookies() {
		cleared = cleared || (c.Name == "dzo_session" && c.MaxAge < 0)
	}
	assert.True(t, cleared, "the dead cookie is cleared")
}

func TestLogoutClearsCookie(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	b.login("")
	_, page := b.get("/")
	tok := csrfMeta.FindStringSubmatch(page)[1]
	sess := b.cookie("dzo_session").Value

	resp, _ := b.do("POST", "/logout", nil)
	assert.Equal(t, 403, resp.StatusCode, "logging out needs the token too")
	resp, _ = b.do("POST", "/logout", nil, "X-CSRF-Token", tok)
	require.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
	var cleared bool
	for _, c := range resp.Cookies() {
		cleared = cleared || (c.Name == "dzo_session" && c.MaxAge < 0)
	}
	assert.True(t, cleared)
	_, err := l.st.Session(t.Context(), auth.HashSecret(sess), time.Now(), time.Hour, time.Hour)
	require.ErrorIs(t, err, moderation.ErrNotFound, "the session is gone at the API too")
}

func TestClientAddrForwarded(t *testing.T) {
	proxies, err := auth.ParseProxies([]string{"127.0.0.1"})
	require.NoError(t, err)
	l := newLoginEnv(t, func(o *Options) { o.TrustedProxies = proxies })
	l.ann("admin")
	b := l.visitor()
	b.hdr["X-Forwarded-For"] = "203.0.113.9"
	b.login("")
	entries, err := l.api.Audit.Tail("", 10)
	require.NoError(t, err)
	var seen bool
	for _, e := range entries {
		if e.Action == "auth.login" {
			seen = true
			assert.Equal(t, "203.0.113.9", e.Client, "the browser's address behind the trusted proxy")
		}
	}
	assert.True(t, seen)

	// without a trusted proxy the header means nothing
	l2 := newLoginEnv(t)
	l2.ann("admin")
	b2 := l2.visitor()
	b2.hdr["X-Forwarded-For"] = "203.0.113.9"
	b2.login("")
	entries, _ = l2.api.Audit.Tail("", 10)
	for _, e := range entries {
		if e.Action == "auth.login" {
			assert.Equal(t, netip.MustParseAddr("127.0.0.1").String(), e.Client)
		}
	}
}

func TestSessionCookieIsHostPrefixedOverHTTPS(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	l.api.Login.WebURL = "https://dzo.example"
	l.srv.webURLCache.Store(nil) // the web interface asks again
	b := l.visitor()
	_, body := b.get("/login")
	b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {formCSRF(t, body)}})
	_, body = b.get("/login/second")
	resp, _ := b.do("POST", "/login/second", url.Values{"kind": {"totp"}, "code": {auth.TOTPCode(loginTOTP, time.Now())}, "csrf": {formCSRF(t, body)}})
	require.Equal(t, 303, resp.StatusCode)
	var sc *http.Cookie
	for _, c := range resp.Cookies() {
		if strings.HasSuffix(c.Name, "dzo_session") {
			sc = c
		}
	}
	require.NotNil(t, sc)
	assert.Equal(t, "__Host-dzo_session", sc.Name)
	assert.True(t, sc.Secure)
	assert.True(t, sc.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, sc.SameSite)
	assert.Empty(t, sc.Domain)
	assert.Equal(t, "/", sc.Path)
}

func TestLoginPageWhenLoginsAreOff(t *testing.T) {
	l := newLoginEnv(t)
	l.api.Login.WebURL = ""
	b := l.visitor()
	resp, body := b.get("/login")
	assert.Equal(t, 503, resp.StatusCode)
	assert.Contains(t, body, "serve.web_url")
}

func TestPasswordWithoutSecondFactorGoesToEnrolment(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	require.NoError(t, l.st.ClearTOTP(t.Context(), loginSteamA))
	require.NoError(t, l.st.SetRecoveryCodes(t.Context(), loginSteamA, nil))
	b := l.visitor()
	_, body := b.get("/login")
	resp, _ := b.do("POST", "/login", url.Values{"username": {"ann"}, "password": {loginPW}, "csrf": {formCSRF(t, body)}})
	require.Equal(t, 303, resp.StatusCode)
	assert.Equal(t, "/login/enrol", resp.Header.Get("Location"))
	assert.Nil(t, b.cookie("dzo_session"))
	resp, body = b.get("/login/enrol")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, body, `id="totp-secret"`)
}

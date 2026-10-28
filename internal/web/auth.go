// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/auth"
)

const (
	sessionCookie     = "dzo_session"
	hostSessionCookie = "__Host-dzo_session" // the name when the site is on HTTPS: only a host-only, Secure cookie sticks
	loginCookie       = "dzo_login"
	loginLife         = 5 * time.Minute
	infoLife          = 5 * time.Second // how long the answer of GET /api/v1 is reused per session
	webURLLife        = 30 * time.Second
	maxJSONBody       = 64 << 10
)

// ---- the per-session facts of a request ----

type ctxKey struct{}

// sessionData is what the session check found for a request.
type sessionData struct {
	secret string
	info   api.Info
}

func sessionFrom(ctx context.Context) *sessionData {
	d, _ := ctx.Value(ctxKey{}).(*sessionData)
	return d
}

// csrfFor is the token of a session: derived from the session secret, so a token of another session is no good
// and the web interface needs no state for it.
func csrfFor(session string) string {
	m := hmac.New(sha256.New, []byte(session))
	m.Write([]byte("dzo csrf"))
	return hex.EncodeToString(m.Sum(nil))
}

// requestCSRF is the token a request carries: the header htmx and the scripts send, or the field of a form.
func requestCSRF(r *http.Request) string {
	if v := r.Header.Get("X-CSRF-Token"); v != "" {
		return v
	}
	return r.PostFormValue("csrf")
}

func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

// ---- the address of the browser ----

// clientAddrs works out the browser's address (through trusted proxies) and the proxy it came through, or "".
func (s *Server) clientAddrs(r *http.Request) (client, proxy string) {
	addr := s.opts.TrustedProxies.ClientAddr(r)
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if peer, err := netip.ParseAddr(host); err == nil && peer.Unmap() != addr {
			proxy = peer.Unmap().String()
		}
	}
	return addr.String(), proxy
}

// userClient is the API client for a request that acts for a session ("" for none, at login).
func (s *Server) userClient(b *Backend, r *http.Request, session string) *apiclient.Client {
	addr, proxy := s.clientAddrs(r)
	c := b.Client.WithActor("", "web").ForUser(session, addr)
	if proxy != "" {
		c = c.ForProxy(proxy)
	}
	return c
}

// client is the API client for a request of a logged-in person.
func (s *Server) client(b *Backend, r *http.Request) *apiclient.Client {
	session := ""
	if d := sessionFrom(r.Context()); d != nil {
		session = d.secret
	}
	return s.userClient(b, r, session)
}

// ---- what the API says about logins ----

type webURLInfo struct {
	info api.AuthInfo
	at   time.Time
}

// authInfo asks the API whether logins are on and where the web interface lives, at most every 30 seconds.
func (s *Server) authInfo(ctx context.Context) (api.AuthInfo, error) {
	if e := s.webURLCache.Load(); e != nil && s.now().Sub(e.at) < webURLLife {
		return e.info, nil
	}
	info, err := s.backends[0].Client.AuthInfo(ctx)
	if err != nil {
		return api.AuthInfo{}, err
	}
	s.webURLCache.Store(&webURLInfo{info: info, at: s.now()})
	return info, nil
}

// secure reports whether the site is served over HTTPS (serve.web_url says so, or the connection is TLS).
func (s *Server) secure(r *http.Request) bool {
	if info, err := s.authInfo(r.Context()); err == nil && info.WebURL != "" {
		return strings.HasPrefix(info.WebURL, "https://")
	}
	return r.TLS != nil
}

// ---- cookies ----

func (s *Server) sessionCookieName(r *http.Request) string {
	if s.secure(r) {
		return hostSessionCookie
	}
	return sessionCookie
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, sess api.LoginSession) {
	maxAge := int(time.Until(sess.Expires).Seconds())
	if d := sess.Expires.Sub(s.now()); d > 0 {
		maxAge = int(d.Seconds())
	}
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookieName(r), Value: sess.Session, Path: "/", MaxAge: max(maxAge, 1), //nolint:gosec // G124: Secure follows serve.web_url, which is https except on loopback
		HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteStrictMode})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	for _, name := range []string{sessionCookie, hostSessionCookie} {
		secure := name == hostSessionCookie                                                                                                              // clearing a __Host- cookie needs the same attributes
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}) //nolint:gosec // G124: a __Host- cookie is Secure by name, the plain one is cleared as it was set
	}
}

func sessionOf(r *http.Request) string {
	for _, name := range []string{hostSessionCookie, sessionCookie} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// loginState is the half-finished login in the browser: the form token, the pending login at the API and,
// for the Steam round trip, the state that has to come back.
type loginState struct {
	CSRF    string   `json:"c"`
	Pending string   `json:"p,omitempty"`
	Steam   string   `json:"s,omitempty"`
	Factors []string `json:"f,omitempty"`
	Enrol   bool     `json:"e,omitempty"`
	Next    string   `json:"n,omitempty"`
}

func randomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system has no randomness; nothing safe can be done
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) readState(r *http.Request) (loginState, bool) {
	var st loginState
	c, err := r.Cookie(loginCookie)
	if err != nil {
		return st, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || json.Unmarshal(raw, &st) != nil || st.CSRF == "" {
		return loginState{}, false
	}
	return st, true
}

// saveState stores the login state. SameSite is Lax, not Strict: Steam sends the browser back with a
// cross-site navigation, and a Strict cookie would not come along.
func (s *Server) saveState(w http.ResponseWriter, r *http.Request, st loginState) {
	raw, _ := json.Marshal(st)                                                                                     //nolint:errcheck // a struct of plain fields
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: base64.RawURLEncoding.EncodeToString(raw), Path: "/", //nolint:gosec // G124: Secure follows serve.web_url, which is https except on loopback
		MaxAge: int(loginLife.Seconds()), HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearState(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode}) //nolint:gosec // G124: Secure follows serve.web_url, which is https except on loopback
}

// formOK checks a login form: the state cookie has to exist, the form carries its token, and the request is
// same-origin.
func formOK(r *http.Request, st loginState, have bool) bool {
	return have && sameOrigin(r) && subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(st.CSRF)) == 1
}

// safeNext returns where to go after login: a local path, never another site, never the login pages.
func safeNext(n string) string {
	if n == "" || len(n) > 2000 || n[0] != '/' || strings.HasPrefix(n, "//") || strings.HasPrefix(n, "/\\") || strings.ContainsAny(n, "\r\n\\") {
		return "/"
	}
	u, err := url.Parse(n)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "/login" || strings.HasPrefix(u.Path, "/login/") {
		return "/"
	}
	return n
}

// ---- the session check ----

type cachedInfo struct {
	info api.Info
	at   time.Time
}

type infoCache struct {
	mu sync.Mutex
	m  map[string]cachedInfo
}

// sessionInfo asks the API who the session is, reusing an answer for a few seconds.
func (s *Server) sessionInfo(r *http.Request, session string) (api.Info, error) {
	key := auth.HashSecret(session)
	now := s.now()
	s.infos.mu.Lock()
	if e, ok := s.infos.m[key]; ok && now.Sub(e.at) < infoLife {
		s.infos.mu.Unlock()
		return e.info, nil
	}
	s.infos.mu.Unlock()
	info, err := s.userClient(s.backends[0], r, session).Info(r.Context())
	s.infos.mu.Lock()
	defer s.infos.mu.Unlock()
	if s.infos.m == nil {
		s.infos.m = map[string]cachedInfo{}
	}
	for k, e := range s.infos.m { // forget the old ones
		if now.Sub(e.at) > time.Minute {
			delete(s.infos.m, k)
		}
	}
	if err != nil {
		delete(s.infos.m, key)
		return api.Info{}, err
	}
	s.infos.m[key] = cachedInfo{info: info, at: now}
	return info, nil
}

func (s *Server) forgetInfo(session string) {
	s.infos.mu.Lock()
	delete(s.infos.m, auth.HashSecret(session))
	s.infos.mu.Unlock()
}

// toLogin sends a request without a session to the login page. A fragment that htmx asked for cannot follow a
// redirect, so it is told to move the whole page.
func (s *Server) toLogin(w http.ResponseWriter, r *http.Request, clear bool) {
	if clear {
		s.clearSession(w)
	}
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/login")
		http.Error(w, "login needed", http.StatusUnauthorized)
		return
	}
	target := "/login"
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.RequestURI() != "/" {
		target += "?next=" + url.QueryEscape(r.URL.RequestURI())
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// requireSession lets only logged-in people through to next.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := sessionOf(r)
		if session == "" {
			s.toLogin(w, r, false)
			return
		}
		info, err := s.sessionInfo(r, session)
		var ae *apiclient.Error
		switch {
		case errors.As(err, &ae) && ae.Status == http.StatusUnauthorized:
			s.toLogin(w, r, true)
			return
		case errors.As(err, &ae) && ae.Status == http.StatusForbidden:
			s.renderCode(w, http.StatusForbidden, "loginmessage", loginView{Title: "No access", Message: ae.Message, CSRF: csrfFor(session), LoggedIn: true})
			return
		case err != nil:
			s.renderCode(w, http.StatusBadGateway, "loginmessage", loginView{Title: "Unreachable", Message: "The API of this installation is not reachable: " + err.Error(), CSRF: csrfFor(session), LoggedIn: true})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &sessionData{secret: session, info: info})))
	})
}

// ---- the login pages ----

// loginView is the data of the login pages.
type loginView struct {
	Title, CSRF, Error, Message string
	Next                        string
	Factors                     []string
	HasTOTP, HasRecovery        bool
	HasPasskey                  bool
	QR                          template.URL
	Secret                      string
	Codes                       []string
	Refresh                     string
	LoggedIn                    bool
	Version                     string
}

func (v loginView) Has(f string) bool {
	for _, x := range v.Factors {
		if x == f {
			return true
		}
	}
	return false
}

func (s *Server) loginView(title string, st loginState) loginView {
	return loginView{Title: title, CSRF: st.CSRF, Next: st.Next, Factors: st.Factors, Version: s.opts.Version}
}

// loginClient is the API client of a login step: the web token without a session.
func (s *Server) loginClient(r *http.Request) *apiclient.Client {
	return s.userClient(s.backends[0], r, "")
}

// loginsOn answers the request itself, with the explanation, when user logins are not set up.
func (s *Server) loginsOn(w http.ResponseWriter, r *http.Request) (api.AuthInfo, bool) {
	info, err := s.authInfo(r.Context())
	switch {
	case err != nil:
		s.renderCode(w, http.StatusBadGateway, "loginmessage", loginView{Title: "Unreachable", Message: "The API of this installation is not reachable: " + err.Error()})
	case !info.Logins:
		s.renderCode(w, http.StatusServiceUnavailable, "loginmessage", loginView{Title: "Logins are off", Message: "Logins are not configured: set serve.web_url in config.yaml on the host of dzo serve and restart it."})
	default:
		return info, true
	}
	return info, false
}

// apiStatus is the HTTP status and message of an error from the API, for the login pages.
func apiStatus(err error) (int, string) {
	var ae *apiclient.Error
	if errors.As(err, &ae) {
		return ae.Status, ae.Message
	}
	return http.StatusBadGateway, "The API is not reachable: " + err.Error()
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.loginsOn(w, r); !ok {
		return
	}
	st, have := s.readState(r)
	if !have {
		st = loginState{CSRF: randomToken()}
	}
	st.Pending, st.Steam, st.Factors, st.Enrol = "", "", nil, false
	st.Next = safeNext(r.URL.Query().Get("next"))
	if st.Next == "/" {
		st.Next = ""
	}
	s.saveState(w, r, st)
	s.render(w, "login", s.loginView("Sign in", st))
}

func (s *Server) loginPassword(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.loginsOn(w, r); !ok {
		return
	}
	st, have := s.readState(r)
	if err := r.ParseForm(); err != nil || !formOK(r, st, have) {
		http.Error(w, "bad form token, reload the page", http.StatusForbidden)
		return
	}
	p, err := s.loginClient(r).LoginPassword(r.Context(), r.PostFormValue("username"), r.PostFormValue("password"))
	if err != nil {
		code, msg := apiStatus(err)
		v := s.loginView("Sign in", st)
		v.Error = msg
		s.renderCode(w, code, "login", v)
		return
	}
	s.afterFirstFactor(w, r, st, p)
}

// afterFirstFactor stores the pending login in the state and moves on to the second factor or the enrolment.
func (s *Server) afterFirstFactor(w http.ResponseWriter, r *http.Request, st loginState, p api.LoginPending) {
	st.Pending, st.Factors, st.Enrol, st.Steam = p.Pending, p.Factors, p.Enrol, ""
	s.saveState(w, r, st)
	http.Redirect(w, r, nextStep(st), http.StatusSeeOther)
}

func nextStep(st loginState) string {
	if st.Enrol {
		return "/login/enrol"
	}
	return "/login/second"
}

func (s *Server) loginSteam(w http.ResponseWriter, r *http.Request) {
	info, ok := s.loginsOn(w, r)
	if !ok {
		return
	}
	st, have := s.readState(r)
	if !have {
		st = loginState{CSRF: randomToken()}
	}
	st.Steam = randomToken()
	u, err := s.loginClient(r).SteamURL(r.Context(), s.steamReturn(info, st.Steam))
	if err != nil {
		code, msg := apiStatus(err)
		v := s.loginView("Sign in", st)
		v.Error = msg
		s.renderCode(w, code, "login", v)
		return
	}
	s.saveState(w, r, st)
	http.Redirect(w, r, u, http.StatusSeeOther) //nolint:gosec // G710: the address of Steam's sign-in page, made by our own API
}

func (s *Server) steamReturn(info api.AuthInfo, state string) string {
	return strings.TrimRight(info.WebURL, "/") + "/login/steam/return?state=" + url.QueryEscape(state)
}

// loginSteamReturn is where Steam sends the browser back. The state in the query has to be the one this browser
// made, or an attacker could log a victim into the attacker's account by handing them a link. It answers 200
// with a page that moves on by itself instead of redirecting: that next request starts on this site.
func (s *Server) loginSteamReturn(w http.ResponseWriter, r *http.Request) {
	info, ok := s.loginsOn(w, r)
	if !ok {
		return
	}
	st, have := s.readState(r)
	got := r.URL.Query().Get("state")
	if !have || st.Steam == "" || subtle.ConstantTimeCompare([]byte(got), []byte(st.Steam)) != 1 {
		s.renderCode(w, http.StatusBadRequest, "loginmessage", loginView{Title: "Sign in failed", Message: "This sign-in was not started in this browser, or it took too long. Start again.", Version: s.opts.Version})
		return
	}
	params := map[string][]string{}
	for k, v := range r.URL.Query() {
		if strings.HasPrefix(k, "openid.") {
			params[k] = v
		}
	}
	p, err := s.loginClient(r).LoginSteam(r.Context(), params, s.steamReturn(info, st.Steam))
	if err != nil {
		code, msg := apiStatus(err)
		if code == http.StatusForbidden {
			msg = "No access: " + msg
		}
		s.renderCode(w, code, "loginmessage", loginView{Title: "Sign in failed", Message: msg, Version: s.opts.Version})
		return
	}
	st.Pending, st.Factors, st.Enrol, st.Steam = p.Pending, p.Factors, p.Enrol, ""
	s.saveState(w, r, st)
	s.render(w, "loginrefresh", loginView{Title: "Signed in with Steam", Refresh: nextStep(st), Version: s.opts.Version})
}

func (s *Server) loginSecondPage(w http.ResponseWriter, r *http.Request) {
	st, have := s.readState(r)
	if !have || st.Pending == "" || st.Enrol {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, "loginsecond", s.loginView("Second factor", st))
}

func (s *Server) loginSecond(w http.ResponseWriter, r *http.Request) {
	st, have := s.readState(r)
	if err := r.ParseForm(); err != nil || !have || st.Pending == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !formOK(r, st, have) {
		http.Error(w, "bad form token, reload the page", http.StatusForbidden)
		return
	}
	var sess api.LoginSession
	var err error
	code := strings.TrimSpace(r.PostFormValue("code"))
	switch r.PostFormValue("kind") {
	case "recovery":
		sess, err = s.loginClient(r).LoginRecovery(r.Context(), st.Pending, code)
	default:
		sess, err = s.loginClient(r).LoginTOTP(r.Context(), st.Pending, code)
	}
	if err != nil {
		status, msg := apiStatus(err)
		if status == http.StatusUnauthorized && strings.Contains(msg, "expired") {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		v := s.loginView("Second factor", st)
		v.Error = msg
		s.renderCode(w, status, "loginsecond", v)
		return
	}
	s.finishLogin(w, r, st, sess)
}

// finishLogin hands the browser its session and leaves the login pages.
func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, st loginState, sess api.LoginSession) {
	s.setSession(w, r, sess)
	s.clearState(w, r)
	next := safeNext(st.Next)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// jsonOut writes a JSON answer for passkey.js.
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error) {
	code, msg := apiStatus(err)
	jsonOut(w, code, map[string]string{"error": msg})
}

// scriptOK checks a request from passkey.js: the state cookie, and its token in X-CSRF-Token.
func (s *Server) scriptOK(w http.ResponseWriter, r *http.Request) (loginState, bool) {
	st, have := s.readState(r)
	switch {
	case !have || st.Pending == "":
		jsonOut(w, http.StatusUnauthorized, map[string]string{"error": "this login has expired, start again"})
	case !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(st.CSRF)) != 1:
		jsonOut(w, http.StatusForbidden, map[string]string{"error": "bad token, reload the page"})
	default:
		return st, true
	}
	return st, false
}

// writeRawJSON passes on JSON that our own API made (WebAuthn options) as it is.
func writeRawJSON(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b) //nolint:gosec // G705: JSON from our own API, served as application/json with nosniff
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody)).Decode(v); err != nil {
		jsonOut(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return false
	}
	return true
}

func (s *Server) passkeyBegin(w http.ResponseWriter, r *http.Request) {
	st, ok := s.scriptOK(w, r)
	if !ok || st.Enrol {
		if ok {
			jsonOut(w, http.StatusForbidden, map[string]string{"error": "set up a second factor first"})
		}
		return
	}
	opts, err := s.loginClient(r).PasskeyLoginBegin(r.Context(), st.Pending)
	if err != nil {
		jsonErr(w, err)
		return
	}
	writeRawJSON(w, opts)
}

type passkeyAnswer struct {
	Response json.RawMessage `json:"response"`
	Name     string          `json:"name"`
}

func (s *Server) passkeyFinish(w http.ResponseWriter, r *http.Request) {
	st, ok := s.scriptOK(w, r)
	if !ok {
		return
	}
	var body passkeyAnswer
	if !readJSON(w, r, &body) {
		return
	}
	sess, err := s.loginClient(r).PasskeyLoginFinish(r.Context(), st.Pending, body.Response)
	if err != nil {
		jsonErr(w, err)
		return
	}
	s.setSession(w, r, sess)
	s.clearState(w, r)
	jsonOut(w, http.StatusOK, map[string]string{"next": safeNext(st.Next)})
}

func (s *Server) enrolPage(w http.ResponseWriter, r *http.Request) {
	st, have := s.readState(r)
	if !have || st.Pending == "" || !st.Enrol {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderEnrol(w, r, st, "", http.StatusOK)
}

// renderEnrol makes a TOTP secret and shows it as a QR code, next to the option of a passkey.
func (s *Server) renderEnrol(w http.ResponseWriter, r *http.Request, st loginState, errMsg string, status int) {
	setup, err := s.loginClient(r).EnrolTOTPBegin(r.Context(), st.Pending)
	if err != nil {
		code, msg := apiStatus(err)
		if code == http.StatusUnauthorized {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		s.renderCode(w, code, "loginmessage", loginView{Title: "Second factor", Message: msg, Version: s.opts.Version})
		return
	}
	v := s.loginView("Set up your second factor", st)
	v.Secret, v.Error = setup.Secret, errMsg
	if q, err := qr.Encode(setup.URI, qr.M); err == nil {
		q.Scale = 5
		v.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(q.PNG())) //nolint:gosec // G203: a data URL we built from our own PNG
	}
	s.renderCode(w, status, "loginenrol", v)
}

func (s *Server) enrol(w http.ResponseWriter, r *http.Request) {
	st, have := s.readState(r)
	if err := r.ParseForm(); err != nil || !have || st.Pending == "" || !st.Enrol {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !formOK(r, st, have) {
		http.Error(w, "bad form token, reload the page", http.StatusForbidden)
		return
	}
	sess, err := s.loginClient(r).EnrolTOTPFinish(r.Context(), st.Pending, strings.TrimSpace(r.PostFormValue("code")))
	if err != nil {
		code, msg := apiStatus(err)
		s.renderEnrol(w, r, st, msg, code)
		return
	}
	s.setSession(w, r, sess)
	s.clearState(w, r)
	v := s.loginView("Your recovery codes", st)
	v.Codes, v.Next = sess.RecoveryCodes, safeNext(st.Next)
	s.render(w, "recovery", v)
}

func (s *Server) enrolPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	st, ok := s.scriptOK(w, r)
	if !ok {
		return
	}
	opts, err := s.loginClient(r).EnrolPasskeyBegin(r.Context(), st.Pending)
	if err != nil {
		jsonErr(w, err)
		return
	}
	writeRawJSON(w, opts)
}

func (s *Server) enrolPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	st, ok := s.scriptOK(w, r)
	if !ok {
		return
	}
	var body passkeyAnswer
	if !readJSON(w, r, &body) {
		return
	}
	sess, err := s.loginClient(r).EnrolPasskeyFinish(r.Context(), st.Pending, body.Name, body.Response)
	if err != nil {
		jsonErr(w, err)
		return
	}
	s.setSession(w, r, sess)
	s.clearState(w, r)
	jsonOut(w, http.StatusOK, map[string]any{"next": safeNext(st.Next), "recovery_codes": sess.RecoveryCodes})
}

// logout ends the session at the API and in the browser.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	d := sessionFrom(r.Context())
	_ = s.userClient(s.backends[0], r, d.secret).Logout(r.Context()) // the cookie goes either way; a session left behind expires by itself
	s.forgetInfo(d.secret)
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

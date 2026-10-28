// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// pendingLife is how long a login stays open between its two steps.
const pendingLife = 5 * time.Minute

// LoginPending answers a first factor: the id of the half-finished login, and what can finish it. With Enrol
// the person has no second factor yet and has to set one up first.
type LoginPending struct {
	Pending string   `json:"pending"`
	Factors []string `json:"factors"`
	Enrol   bool     `json:"enrol"`
}

// LoginSession answers a finished login.
type LoginSession struct {
	Session string    `json:"session"`
	Expires time.Time `json:"expires"`
	// RecoveryCodes are shown once, when a person has set up their first second factor.
	RecoveryCodes []string `json:"recovery_codes,omitempty"`
}

// loginCall is a login request: a web token without a session.
type loginCall struct {
	token  Token
	client string // the browser's address
	proxy  string // the proxy it came through, or "-"
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// addrOf returns a valid address as a string, or "".
func addrOf(v string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	return a.WithZone("").Unmap().String()
}

// clientHeader is the browser's address a web token reports in X-Dzo-Client, or "".
func clientHeader(r *http.Request) string { return addrOf(r.Header.Get("X-Dzo-Client")) }

// loginRoutes registers the routes that start a session. They take a web token and no session.
func (s *Server) loginRoutes(mux *http.ServeMux, base string) {
	login := func(pattern string, h func(http.ResponseWriter, *http.Request, *loginCall)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			who, found := s.Tokens.Lookup(secret)
			if !ok || !found {
				w.Header().Set("WWW-Authenticate", `Bearer realm="dzo"`)
				fail(w, http.StatusUnauthorized, "missing or invalid token")
				return
			}
			if !who.Token.Web {
				fail(w, http.StatusForbidden, "the login routes need a web token")
				return
			}
			if s.Accounts == nil || s.Login.WebURL == "" {
				fail(w, http.StatusServiceUnavailable, "logins are not configured: set serve.web_url")
				return
			}
			lc := &loginCall{token: who.Token, client: clientHeader(r), proxy: "-"}
			if lc.client == "" {
				host, _, err := net.SplitHostPort(r.RemoteAddr)
				if lc.client = addrOf(host); err != nil || lc.client == "" {
					lc.client = "unknown"
				}
			}
			if p := addrOf(r.Header.Get("X-Dzo-Proxy")); p != "" {
				lc.proxy = p
			}
			h(w, r, lc)
		})
	}
	login("POST "+base+"/auth/password", s.loginPassword)
	login("GET "+base+"/auth/steam", s.steamURL)
	login("POST "+base+"/auth/steam", s.loginSteam)
	login("POST "+base+"/auth/totp", func(w http.ResponseWriter, r *http.Request, lc *loginCall) { s.loginSecond(w, r, lc, "totp") })
	login("POST "+base+"/auth/recovery", func(w http.ResponseWriter, r *http.Request, lc *loginCall) { s.loginSecond(w, r, lc, "recovery") })
	login("POST "+base+"/auth/passkey/begin", s.passkeyLoginBegin)
	login("POST "+base+"/auth/passkey/finish", s.passkeyLoginFinish)
	login("POST "+base+"/auth/enrol/totp/begin", s.enrolTOTPBegin)
	login("POST "+base+"/auth/enrol/totp/finish", s.enrolTOTPFinish)
	login("POST "+base+"/auth/enrol/passkey/begin", s.enrolPasskeyBegin)
	login("POST "+base+"/auth/enrol/passkey/finish", s.enrolPasskeyFinish)
}

// logStep writes the log line of a login step. steamID is "" when unknown.
func (s *Server) logStep(lc *loginCall, step, outcome, steamID string) {
	if steamID == "" {
		steamID = "unknown"
	}
	s.logf("login: %s %s steamid=%s client=%s proxy=%s", step, outcome, steamID, lc.client, lc.proxy)
}

// tooMany answers a login that has to wait. The message does not depend on the account.
func tooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	fail(w, http.StatusTooManyRequests, "too many failed logins, try again later")
}

var dummyHash = sync.OnceValue(func() string {
	h, _ := auth.HashPassword("a password nobody has") //nolint:errcheck // a fixed password of valid length
	return h
})

type passwordBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginPassword is the first factor with a password.
func (s *Server) loginPassword(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body passwordBody
	if !decode(w, r, &body) {
		return
	}
	keys := []string{"acct:" + strings.ToLower(strings.TrimSpace(body.Username)), "ip:" + lc.client}
	if wait := s.login.Wait(keys...); wait > 0 {
		s.logStep(lc, "password", "refused", "")
		tooMany(w, wait)
		return
	}
	ctx := r.Context()
	person, perr := s.Accounts.PersonByUsername(ctx, body.Username)
	hash := dummyHash() // one hash is always computed, so that the time does not tell whether the name exists
	if perr == nil {
		if creds, err := s.Accounts.Credentials(ctx, person.SteamID); err == nil && creds.PasswordHash != "" {
			hash = creds.PasswordHash
		}
	}
	matches := auth.CheckPassword(hash, body.Password)
	if perr != nil && !errors.Is(perr, moderation.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	if perr != nil || !matches || hash == dummyHash() || person.Disabled || len(person.Grants) == 0 {
		id := ""
		if perr == nil {
			id = person.SteamID
		}
		s.login.Fail(keys...)
		s.logStep(lc, "password", "failed", id)
		fail(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.logStep(lc, "password", "ok", person.SteamID)
	s.startPending(w, r, lc, person, "password")
}

// startPending stores the half-finished login of a person and answers what can finish it.
func (s *Server) startPending(w http.ResponseWriter, r *http.Request, lc *loginCall, person moderation.Person, via string) {
	ctx := r.Context()
	creds, err := s.Accounts.Credentials(ctx, person.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	out := LoginPending{Factors: []string{}, Enrol: creds.TOTPSecret == nil && len(creds.Passkeys) == 0}
	if !out.Enrol {
		if creds.TOTPSecret != nil {
			out.Factors = append(out.Factors, "totp")
		}
		if len(creds.Passkeys) > 0 {
			out.Factors = append(out.Factors, "passkey")
		}
		if creds.RecoveryLeft > 0 {
			out.Factors = append(out.Factors, "recovery")
		}
	}
	id, hash, err := auth.NewSecret("dzp_")
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not make a login id")
		return
	}
	err = s.Accounts.CreatePending(ctx, hash, moderation.PendingLogin{SteamID: person.SteamID, Via: via, Enrol: out.Enrol, Expires: s.now().Add(pendingLife)})
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	out.Pending = id
	writeJSON(w, http.StatusOK, out)
}

// returnToOK reports whether a Steam return address is under serve.web_url.
func (s *Server) returnToOK(returnTo string) bool {
	base, err := url.Parse(s.Login.WebURL)
	if err != nil || base.Host == "" {
		return false
	}
	u, err := url.Parse(returnTo)
	return err == nil && u.Scheme == base.Scheme && u.Host == base.Host && u.User == nil && u.Fragment == ""
}

// steamURL is GET /auth/steam?return_to=: where to send the browser to sign in.
func (s *Server) steamURL(w http.ResponseWriter, r *http.Request, _ *loginCall) {
	returnTo := r.URL.Query().Get("return_to")
	if !s.returnToOK(returnTo) {
		fail(w, http.StatusBadRequest, "return_to must be an address under serve.web_url")
		return
	}
	realm := strings.TrimRight(s.Login.WebURL, "/")
	writeJSON(w, http.StatusOK, map[string]string{"url": s.Login.Steam.RedirectURL(returnTo, realm)})
}

type steamBody struct {
	Params   map[string][]string `json:"params"`
	ReturnTo string              `json:"return_to"`
}

// loginSteam is the first factor with Steam: the query Steam sent the browser back with.
func (s *Server) loginSteam(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body steamBody
	if !decode(w, r, &body) {
		return
	}
	if !s.returnToOK(body.ReturnTo) {
		fail(w, http.StatusBadRequest, "return_to must be an address under serve.web_url")
		return
	}
	keys := []string{"ip:" + lc.client}
	if wait := s.login.Wait(keys...); wait > 0 {
		s.logStep(lc, "steam", "refused", "")
		tooMany(w, wait)
		return
	}
	ctx := r.Context()
	steamID, nonce, err := s.Login.Steam.Verify(ctx, url.Values(body.Params), body.ReturnTo)
	if err == nil {
		var fresh bool
		if fresh, err = s.Accounts.UseSteamNonce(ctx, nonce, s.now()); err == nil && !fresh {
			err = errors.New("nonce used before")
		}
	}
	if err != nil {
		s.login.Fail(keys...)
		s.logStep(lc, "steam", "failed", "")
		fail(w, http.StatusUnauthorized, "steam sign-in failed")
		return
	}
	person, err := s.Accounts.Person(ctx, steamID)
	switch {
	case errors.Is(err, moderation.ErrNotFound) || (err == nil && (person.Disabled || len(person.Grants) == 0)):
		s.logStep(lc, "steam", "refused", steamID)
		fail(w, http.StatusForbidden, "this Steam account has no access; ask an admin to grant it")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.logStep(lc, "steam", "ok", steamID)
	s.startPending(w, r, lc, person, "steam")
}

type secondBody struct {
	Pending string `json:"pending"`
	Code    string `json:"code"`
}

// loginSecond is the second factor with a TOTP code or a recovery code.
func (s *Server) loginSecond(w http.ResponseWriter, r *http.Request, lc *loginCall, kind string) {
	var body secondBody
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	pendingHash := auth.HashSecret(body.Pending)
	pend, err := s.Accounts.Pending(ctx, pendingHash, s.now())
	if errors.Is(err, moderation.ErrNotFound) {
		s.logStep(lc, kind, "failed", "")
		fail(w, http.StatusUnauthorized, "this login has expired, start again")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	if pend.Enrol {
		fail(w, http.StatusForbidden, "set up a second factor first")
		return
	}
	keys := []string{"acct:" + pend.SteamID, "ip:" + lc.client}
	if wait := s.login.Wait(keys...); wait > 0 {
		s.logStep(lc, kind, "refused", pend.SteamID)
		tooMany(w, wait)
		return
	}
	person, err := s.Accounts.Person(ctx, pend.SteamID)
	if err != nil || person.Disabled || len(person.Grants) == 0 {
		s.logStep(lc, kind, "failed", pend.SteamID)
		fail(w, http.StatusForbidden, "no access")
		return
	}
	ok, err := s.checkSecond(ctx, kind, pend.SteamID, body.Code)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	if !ok {
		s.login.Fail(keys...)
		s.logStep(lc, kind, "failed", pend.SteamID)
		fail(w, http.StatusUnauthorized, "invalid code")
		return
	}
	s.logStep(lc, kind, "ok", pend.SteamID)
	s.issueSession(w, r, lc, person, pend, pendingHash, kind)
	s.login.Success("acct:" + pend.SteamID)
	if person.Username != "" {
		s.login.Success("acct:" + person.Username)
	}
}

// checkSecond verifies a code. A TOTP step and a recovery code are used up in one conditional statement, so
// two requests with the same code cannot both pass.
func (s *Server) checkSecond(ctx context.Context, kind, steamID, code string) (bool, error) {
	switch kind {
	case "totp":
		creds, err := s.Accounts.Credentials(ctx, steamID)
		if err != nil {
			return false, err
		}
		if creds.TOTPSecret == nil {
			return false, nil
		}
		step, ok := auth.TOTPStep(creds.TOTPSecret, strings.TrimSpace(code), s.now())
		if !ok {
			return false, nil
		}
		return s.Accounts.UseTOTPStep(ctx, steamID, step)
	case "recovery":
		return s.Accounts.UseRecoveryCode(ctx, steamID, auth.HashRecoveryCode(code))
	}
	return false, nil
}

// issueSession ends a pending login with a new session.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, lc *loginCall, person moderation.Person, pend moderation.PendingLogin, pendingHash, kind string, recovery ...string) {
	ctx := r.Context()
	secret, hash, err := auth.NewSecret("dzs_")
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not make a session")
		return
	}
	now := s.now()
	if err := s.Accounts.CreateSession(ctx, hash, person.SteamID, lc.client, now); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	_ = s.Accounts.DeletePending(ctx, pendingHash) // it expires by itself otherwise
	_ = s.Audit.Log(admin.AuditEntry{Actor: lc.token.Name + ":" + person.SteamID, Source: "web", Client: lc.client, Action: "auth.login",
		Target: person.SteamID, Detail: fmt.Sprintf("%s + %s", pend.Via, kind), OK: true}) // the session exists either way
	out := LoginSession{Session: secret, Expires: now.Add(s.Login.max())}
	if len(recovery) > 0 {
		out.RecoveryCodes = recovery
	}
	writeJSON(w, http.StatusOK, out)
}

// logout ends the session of the request.
func (s *Server) logout(w http.ResponseWriter, r *http.Request, c *call) {
	if err := s.Accounts.DeleteSession(r.Context(), c.session); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Action: "auth.logout", Target: c.who.User.SteamID, OK: true})
	w.WriteHeader(http.StatusNoContent)
}

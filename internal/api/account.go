// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"cmp"
	"encoding/base32"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

var usernameRE = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

// errLastFactor is answered when removing a second factor would leave none.
const errLastFactor = "the last second factor cannot be removed"

// totpSecretText is a TOTP secret as an authenticator app takes it.
func totpSecretText(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// beginTOTP makes a secret and the otpauth address for it.
func (s *Server) beginTOTP(person moderation.Person) (secret []byte, out map[string]string, err error) {
	if secret, err = auth.NewTOTPSecret(); err != nil {
		return nil, nil, err
	}
	account := cmp.Or(person.Username, person.Name, person.SteamID)
	return secret, map[string]string{"secret": totpSecretText(secret), "uri": auth.TOTPURI(secret, "dzo "+s.Installation, account)}, nil
}

// finishEnrol ends an enrolment: it makes the recovery codes (the first second factor gets them) and a session.
func (s *Server) finishEnrol(w http.ResponseWriter, r *http.Request, lc *loginCall, person moderation.Person, pend moderation.PendingLogin, hash, kind string) {
	codes, hashes, err := auth.NewRecoveryCodes()
	if err == nil {
		err = s.Accounts.SetRecoveryCodes(r.Context(), person.SteamID, hashes)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not make recovery codes")
		return
	}
	s.issueSession(w, r, lc, person, pend, hash, "enrol "+kind, codes...)
	s.login.Success("acct:" + person.SteamID)
}

// enrolTOTPBegin makes the TOTP secret of a person who has no second factor yet.
func (s *Server) enrolTOTPBegin(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body pendingBody
	if !decode(w, r, &body) {
		return
	}
	hash, pend, person, ok := s.pendingFor(w, r, lc, body.Pending, true)
	if !ok {
		return
	}
	secret, out, err := s.beginTOTP(person)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not make a secret")
		return
	}
	pend.TOTPSecret = secret
	if err := s.Accounts.UpdatePending(r.Context(), hash, pend); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// enrolTOTPFinish checks a first code of the new secret, stores it, and ends the login with a session.
func (s *Server) enrolTOTPFinish(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body secondBody
	if !decode(w, r, &body) {
		return
	}
	hash, pend, person, ok := s.pendingFor(w, r, lc, body.Pending, true)
	if !ok {
		return
	}
	if len(pend.TOTPSecret) == 0 {
		fail(w, http.StatusBadRequest, "ask for a secret first")
		return
	}
	keys, ok := s.waitFor(w, lc, "enrol", person.SteamID)
	if !ok {
		return
	}
	step, ok := auth.TOTPStep(pend.TOTPSecret, strings.TrimSpace(body.Code), s.now())
	if !ok {
		s.login.Fail(keys...)
		s.logStep(lc, "enrol", "failed", person.SteamID)
		fail(w, http.StatusUnauthorized, "invalid code")
		return
	}
	ctx := r.Context()
	err := s.Accounts.SetTOTP(ctx, person.SteamID, pend.TOTPSecret)
	if err == nil {
		_, err = s.Accounts.UseTOTPStep(ctx, person.SteamID, step) // this code is not good for a login any more
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.logStep(lc, "enrol", "ok", person.SteamID)
	s.finishEnrol(w, r, lc, person, pend, hash, "totp")
}

// ---- the account of the logged-in person ----

// accountAudit records a change of the account, never a secret.
func (s *Server) accountAudit(c *call, action, detail string) {
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Action: action, Target: c.who.User.SteamID, Detail: detail, OK: true}) // the change is made either way
}

// PasskeyInfo is a passkey in GET /account.
type PasskeyInfo struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used,omitempty"`
}

// Account is GET /account.
type Account struct {
	SteamID      string        `json:"steam_id"`
	Name         string        `json:"name"`
	Username     string        `json:"username,omitempty"`
	HasPassword  bool          `json:"has_password"`
	TOTP         bool          `json:"totp"`
	Passkeys     []PasskeyInfo `json:"passkeys"`
	RecoveryLeft int           `json:"recovery_left"`
}

func (s *Server) account(w http.ResponseWriter, r *http.Request, c *call) {
	person, err := s.Accounts.Person(r.Context(), c.who.User.SteamID)
	creds, err2 := s.Accounts.Credentials(r.Context(), c.who.User.SteamID)
	if err != nil || err2 != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	out := Account{SteamID: person.SteamID, Name: person.Name, Username: person.Username, HasPassword: creds.PasswordHash != "",
		TOTP: creds.TOTPSecret != nil, Passkeys: []PasskeyInfo{}, RecoveryLeft: creds.RecoveryLeft}
	for _, k := range creds.Passkeys {
		pi := PasskeyInfo{ID: b64url(k.ID), Name: k.Name, Created: k.Created}
		if !k.LastUsed.IsZero() {
			t := k.LastUsed
			pi.LastUsed = &t
		}
		out.Passkeys = append(out.Passkeys, pi)
	}
	writeJSON(w, http.StatusOK, out)
}

type passwordChange struct {
	Username string `json:"username"`
	Current  string `json:"current"`
	New      string `json:"new"`
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request, c *call) {
	var body passwordChange
	if !decode(w, r, &body) {
		return
	}
	ctx, id := r.Context(), c.who.User.SteamID
	person, err := s.Accounts.Person(ctx, id)
	creds, err2 := s.Accounts.Credentials(ctx, id)
	if err != nil || err2 != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	if creds.PasswordHash != "" {
		keys := []string{"acct:" + id, "ip:" + c.client}
		if wait := s.login.Wait(keys...); wait > 0 {
			tooMany(w, wait)
			return
		}
		if !auth.CheckPassword(creds.PasswordHash, body.Current) {
			s.login.Fail(keys...)
			fail(w, http.StatusForbidden, "the current password is wrong")
			return
		}
	}
	username := strings.ToLower(strings.TrimSpace(body.Username))
	switch {
	case username == "" && person.Username == "":
		fail(w, http.StatusBadRequest, "a username is needed")
		return
	case username != "" && !usernameRE.MatchString(username):
		fail(w, http.StatusBadRequest, "a username has 3 to 32 characters: letters, digits, dot, dash and underscore")
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	switch err := s.Accounts.SetPassword(ctx, id, username, hash); {
	case errors.Is(err, moderation.ErrUsernameTaken):
		fail(w, http.StatusConflict, "that username is taken")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.accountAudit(c, "account.password", "changed")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removePassword(w http.ResponseWriter, r *http.Request, c *call) {
	if err := s.Accounts.ClearPassword(r.Context(), c.who.User.SteamID); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.accountAudit(c, "account.password", "removed")
	w.WriteHeader(http.StatusNoContent)
}

// startAccountPending stores a pending change of the account for the logged-in person.
func (s *Server) startAccountPending(w http.ResponseWriter, r *http.Request, c *call, p moderation.PendingLogin) (string, bool) {
	id, hash, err := auth.NewSecret("dzp_")
	if err == nil {
		p.SteamID, p.Via, p.Expires = c.who.User.SteamID, "account", s.now().Add(pendingLife)
		err = s.Accounts.CreatePending(r.Context(), hash, p)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return "", false
	}
	return id, true
}

// accountPending loads the pending change of this person, answering 401 for any other.
func (s *Server) accountPending(w http.ResponseWriter, r *http.Request, c *call, id string) (string, moderation.PendingLogin, bool) {
	hash := auth.HashSecret(id)
	p, err := s.Accounts.Pending(r.Context(), hash, s.now())
	if err != nil || p.Via != "account" || p.SteamID != c.who.User.SteamID {
		fail(w, http.StatusUnauthorized, "this change has expired, start again")
		return "", p, false
	}
	return hash, p, true
}

func (s *Server) accountTOTPBegin(w http.ResponseWriter, r *http.Request, c *call) {
	person, err := s.Accounts.Person(r.Context(), c.who.User.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	secret, out, err := s.beginTOTP(person)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not make a secret")
		return
	}
	id, ok := s.startAccountPending(w, r, c, moderation.PendingLogin{TOTPSecret: secret})
	if !ok {
		return
	}
	out["pending"] = id
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) accountTOTPFinish(w http.ResponseWriter, r *http.Request, c *call) {
	var body secondBody
	if !decode(w, r, &body) {
		return
	}
	hash, p, ok := s.accountPending(w, r, c, body.Pending)
	if !ok {
		return
	}
	step, ok := auth.TOTPStep(p.TOTPSecret, strings.TrimSpace(body.Code), s.now())
	if len(p.TOTPSecret) == 0 || !ok {
		fail(w, http.StatusUnauthorized, "invalid code")
		return
	}
	ctx, id := r.Context(), c.who.User.SteamID
	err := s.Accounts.SetTOTP(ctx, id, p.TOTPSecret)
	if err == nil {
		_, err = s.Accounts.UseTOTPStep(ctx, id, step)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	_ = s.Accounts.DeletePending(ctx, hash) // it expires by itself otherwise
	s.accountAudit(c, "account.totp", "set")
	w.WriteHeader(http.StatusNoContent)
}

// factors counts the second factors of the logged-in person.
func (s *Server) factors(w http.ResponseWriter, r *http.Request, c *call) (moderation.Credentials, bool) {
	creds, err := s.Accounts.Credentials(r.Context(), c.who.User.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return creds, false
	}
	return creds, true
}

func (s *Server) removeTOTP(w http.ResponseWriter, r *http.Request, c *call) {
	creds, ok := s.factors(w, r, c)
	if !ok {
		return
	}
	if len(creds.Passkeys) == 0 {
		fail(w, http.StatusConflict, errLastFactor)
		return
	}
	if err := s.Accounts.ClearTOTP(r.Context(), c.who.User.SteamID); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.accountAudit(c, "account.totp", "removed")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) accountPasskeyBegin(w http.ResponseWriter, r *http.Request, c *call) {
	user, err := s.userOf(r.Context(), c.who.User.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	creation, ceremony, err := s.beginRegistration(user)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not start the registration")
		return
	}
	id, ok := s.startAccountPending(w, r, c, moderation.PendingLogin{Ceremony: ceremony})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": id, "options": creation})
}

func (s *Server) accountPasskeyFinish(w http.ResponseWriter, r *http.Request, c *call) {
	var body passkeyBody
	if !decode(w, r, &body) {
		return
	}
	hash, p, ok := s.accountPending(w, r, c, body.Pending)
	if !ok {
		return
	}
	user, err := s.userOf(r.Context(), c.who.User.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	cred, err := s.finishRegistration(user, p.Ceremony, body.Response)
	if err != nil {
		fail(w, http.StatusUnauthorized, "the passkey could not be registered")
		return
	}
	key := newPasskey(cred, body.Name, s.now())
	if err := s.Accounts.AddPasskey(r.Context(), c.who.User.SteamID, key); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	_ = s.Accounts.DeletePending(r.Context(), hash) // it expires by itself otherwise
	s.accountAudit(c, "account.passkey", "added "+key.Name)
	writeJSON(w, http.StatusCreated, PasskeyInfo{ID: b64url(key.ID), Name: key.Name, Created: key.Created})
}

func (s *Server) removePasskey(w http.ResponseWriter, r *http.Request, c *call) {
	id, err := b64urlDecode(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid passkey id")
		return
	}
	creds, ok := s.factors(w, r, c)
	if !ok {
		return
	}
	known := false
	for _, k := range creds.Passkeys {
		known = known || string(k.ID) == string(id)
	}
	switch {
	case !known:
		fail(w, http.StatusNotFound, "no such passkey")
		return
	case creds.TOTPSecret == nil && len(creds.Passkeys) == 1:
		fail(w, http.StatusConflict, errLastFactor)
		return
	}
	if err := s.Accounts.DeletePasskey(r.Context(), c.who.User.SteamID, id); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.accountAudit(c, "account.passkey", "removed")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) newRecoveryCodes(w http.ResponseWriter, r *http.Request, c *call) {
	codes, hashes, err := auth.NewRecoveryCodes()
	if err == nil {
		err = s.Accounts.SetRecoveryCodes(r.Context(), c.who.User.SteamID, hashes)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not make recovery codes")
		return
	}
	s.accountAudit(c, "account.recovery", "new set")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Server) logoutOthers(w http.ResponseWriter, r *http.Request, c *call) {
	if err := s.Accounts.DeleteSessions(r.Context(), c.who.User.SteamID, c.session); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.accountAudit(c, "account.sessions", "ended the other sessions")
	w.WriteHeader(http.StatusNoContent)
}

// accountRoutes registers the routes of the logged-in person's own account.
func (s *Server) accountRoutes(route func(pattern, perm string, instance bool, h handler), base string) {
	for _, r := range []struct {
		pattern string
		h       handler
	}{
		{"GET " + base + "/account", s.account},
		{"PUT " + base + "/account/password", s.setPassword},
		{"DELETE " + base + "/account/password", s.removePassword},
		{"POST " + base + "/account/totp/begin", s.accountTOTPBegin},
		{"POST " + base + "/account/totp/finish", s.accountTOTPFinish},
		{"DELETE " + base + "/account/totp", s.removeTOTP},
		{"POST " + base + "/account/passkeys/begin", s.accountPasskeyBegin},
		{"POST " + base + "/account/passkeys/finish", s.accountPasskeyFinish},
		{"DELETE " + base + "/account/passkeys/{id}", s.removePasskey},
		{"POST " + base + "/account/recovery", s.newRecoveryCodes},
		{"DELETE " + base + "/account/sessions", s.logoutOthers},
	} {
		route(r.pattern, PermView, false, r.h)
	}
}

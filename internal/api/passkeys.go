// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// webUser adapts a person and their passkeys to the WebAuthn library.
type webUser struct {
	person moderation.Person
	keys   []moderation.Passkey
}

func (u webUser) WebAuthnID() []byte          { return []byte(u.person.SteamID) }
func (u webUser) WebAuthnName() string        { return cmp.Or(u.person.Username, u.person.SteamID) }
func (u webUser) WebAuthnDisplayName() string { return cmp.Or(u.person.Name, u.person.SteamID) }
func (u webUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(u.keys))
	for _, k := range u.keys {
		var c webauthn.Credential
		if json.Unmarshal(k.Data, &c) == nil {
			c.ID = k.ID
			out = append(out, c)
		}
	}
	return out
}

// relyingParty is the WebAuthn relying party of this installation: the host of serve.web_url is the ID and its
// origin the only origin accepted.
func (s *Server) relyingParty() (*webauthn.WebAuthn, error) {
	u, err := url.Parse(s.Login.WebURL)
	if err != nil {
		return nil, err
	}
	return webauthn.New(&webauthn.Config{
		RPID: u.Hostname(), RPDisplayName: "dzo " + s.Installation, RPOrigins: []string{u.Scheme + "://" + u.Host},
	})
}

// userOf loads a person with their passkeys.
func (s *Server) userOf(ctx context.Context, steamID string) (webUser, error) {
	p, err := s.Accounts.Person(ctx, steamID)
	if err != nil {
		return webUser{}, err
	}
	c, err := s.Accounts.Credentials(ctx, steamID)
	if err != nil {
		return webUser{}, err
	}
	return webUser{person: p, keys: c.Passkeys}, nil
}

// credentialJSON is how a credential is stored.
func credentialJSON(c *webauthn.Credential) []byte {
	b, _ := json.Marshal(c) //nolint:errcheck // a struct of plain fields
	return b
}

func ceremonyOf(pend moderation.PendingLogin) (webauthn.SessionData, bool) {
	var sd webauthn.SessionData
	if len(pend.Ceremony) == 0 || json.Unmarshal(pend.Ceremony, &sd) != nil {
		return sd, false
	}
	return sd, true
}

// pendingFor loads the pending login an endpoint works on, answering the request itself when it is unknown,
// expired or not allowed. enrol says what the caller needs: a login that has to enrol (true) or an ordinary one.
func (s *Server) pendingFor(w http.ResponseWriter, r *http.Request, lc *loginCall, id string, enrol bool) (string, moderation.PendingLogin, moderation.Person, bool) {
	hash := auth.HashSecret(id)
	pend, err := s.Accounts.Pending(r.Context(), hash, s.now())
	if errors.Is(err, moderation.ErrNotFound) {
		fail(w, http.StatusUnauthorized, "this login has expired, start again")
		return "", pend, moderation.Person{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return "", pend, moderation.Person{}, false
	}
	if pend.Via == "account" || pend.Enrol != enrol {
		if enrol {
			fail(w, http.StatusForbidden, "this person already has a second factor")
		} else {
			fail(w, http.StatusForbidden, "set up a second factor first")
		}
		return "", pend, moderation.Person{}, false
	}
	person, err := s.Accounts.Person(r.Context(), pend.SteamID)
	if err != nil || person.Disabled || len(person.Grants) == 0 {
		fail(w, http.StatusForbidden, "no access")
		return "", pend, moderation.Person{}, false
	}
	return hash, pend, person, true
}

// waitFor refuses a request while the account or address has to wait.
func (s *Server) waitFor(w http.ResponseWriter, lc *loginCall, step, steamID string) ([]string, bool) {
	keys := []string{"acct:" + steamID, "ip:" + lc.client}
	if wait := s.login.Wait(keys...); wait > 0 {
		s.logStep(lc, step, "refused", steamID)
		tooMany(w, wait)
		return keys, false
	}
	return keys, true
}

type pendingBody struct {
	Pending string `json:"pending"`
}

type passkeyBody struct {
	Pending  string          `json:"pending"`
	Response json.RawMessage `json:"response"`
	Name     string          `json:"name"`
}

// passkeyLoginBegin starts the passkey as a second factor: the options for the browser.
func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body pendingBody
	if !decode(w, r, &body) {
		return
	}
	hash, pend, person, ok := s.pendingFor(w, r, lc, body.Pending, false)
	if !ok {
		return
	}
	if _, ok := s.waitFor(w, lc, "passkey", person.SteamID); !ok {
		return
	}
	user, err := s.userOf(r.Context(), person.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	if len(user.keys) == 0 {
		fail(w, http.StatusBadRequest, "this person has no passkey")
		return
	}
	rp, err := s.relyingParty()
	if err != nil {
		fail(w, http.StatusInternalServerError, "passkeys are not configured: %v", err)
		return
	}
	assertion, session, err := rp.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not start the passkey login")
		return
	}
	pend.Ceremony, _ = json.Marshal(session) //nolint:errcheck // a struct of plain fields
	if err := s.Accounts.UpdatePending(r.Context(), hash, pend); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, assertion)
}

// passkeyLoginFinish checks the browser's answer and ends the login with a session.
func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body passkeyBody
	if !decode(w, r, &body) {
		return
	}
	hash, pend, person, ok := s.pendingFor(w, r, lc, body.Pending, false)
	if !ok {
		return
	}
	keys, ok := s.waitFor(w, lc, "passkey", person.SteamID)
	if !ok {
		return
	}
	session, ok := ceremonyOf(pend)
	pend.Ceremony = nil // an answer is tried once; the options are not good a second time
	_ = s.Accounts.UpdatePending(r.Context(), hash, pend)
	refuse := func() {
		s.login.Fail(keys...)
		s.logStep(lc, "passkey", "failed", person.SteamID)
		fail(w, http.StatusUnauthorized, "invalid passkey")
	}
	if !ok {
		refuse()
		return
	}
	user, err := s.userOf(r.Context(), person.SteamID)
	rp, rerr := s.relyingParty()
	parsed, perr := protocol.ParseCredentialRequestResponseBytes(body.Response)
	if err != nil || rerr != nil || perr != nil {
		refuse()
		return
	}
	cred, err := rp.ValidateLogin(user, session, parsed)
	if err != nil || cred.Authenticator.CloneWarning {
		refuse()
		return
	}
	if err := s.Accounts.UpdatePasskey(r.Context(), person.SteamID, cred.ID, credentialJSON(cred), s.now()); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.logStep(lc, "passkey", "ok", person.SteamID)
	s.issueSession(w, r, lc, person, pend, hash, "passkey")
	s.login.Success("acct:" + person.SteamID)
	if person.Username != "" {
		s.login.Success("acct:" + person.Username)
	}
}

// beginRegistration makes the creation options for a person.
func (s *Server) beginRegistration(user webUser) (*protocol.CredentialCreation, []byte, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return nil, nil, err
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range user.WebAuthnCredentials() {
		exclude = append(exclude, protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: c.ID})
	}
	creation, session, err := rp.BeginRegistration(user,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{UserVerification: protocol.VerificationPreferred}),
		webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, nil, err
	}
	raw, _ := json.Marshal(session) //nolint:errcheck // a struct of plain fields
	return creation, raw, nil
}

// finishRegistration verifies the browser's answer and returns the credential to store.
func (s *Server) finishRegistration(user webUser, ceremony, response []byte) (*webauthn.Credential, error) {
	var session webauthn.SessionData
	if len(ceremony) == 0 || json.Unmarshal(ceremony, &session) != nil {
		return nil, errors.New("no registration was started")
	}
	rp, err := s.relyingParty()
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return nil, err
	}
	return rp.CreateCredential(user, session, parsed)
}

// passkeyName cleans the name a person gives a passkey.
func passkeyName(n string) string {
	n = cleanHeader(n)
	if n = strings.TrimSpace(n); n == "" {
		return "passkey"
	}
	return n
}

// newPasskey is the stored form of a registered credential.
func newPasskey(cred *webauthn.Credential, name string, now time.Time) moderation.Passkey {
	return moderation.Passkey{ID: cred.ID, Name: passkeyName(name), Data: credentialJSON(cred), Created: now}
}

// enrolPasskeyBegin starts the registration of the first passkey of a person who has no second factor yet.
func (s *Server) enrolPasskeyBegin(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body pendingBody
	if !decode(w, r, &body) {
		return
	}
	hash, pend, person, ok := s.pendingFor(w, r, lc, body.Pending, true)
	if !ok {
		return
	}
	user, err := s.userOf(r.Context(), person.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	creation, ceremony, err := s.beginRegistration(user)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not start the registration")
		return
	}
	pend.Ceremony = ceremony
	if err := s.Accounts.UpdatePending(r.Context(), hash, pend); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, creation)
}

// enrolPasskeyFinish stores the passkey, makes the recovery codes and ends the login with a session.
func (s *Server) enrolPasskeyFinish(w http.ResponseWriter, r *http.Request, lc *loginCall) {
	var body passkeyBody
	if !decode(w, r, &body) {
		return
	}
	hash, pend, person, ok := s.pendingFor(w, r, lc, body.Pending, true)
	if !ok {
		return
	}
	keys, ok := s.waitFor(w, lc, "enrol", person.SteamID)
	if !ok {
		return
	}
	user, err := s.userOf(r.Context(), person.SteamID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	cred, err := s.finishRegistration(user, pend.Ceremony, body.Response)
	if err != nil {
		s.login.Fail(keys...)
		s.logStep(lc, "enrol", "failed", person.SteamID)
		fail(w, http.StatusUnauthorized, "the passkey could not be registered")
		return
	}
	if err := s.Accounts.AddPasskey(r.Context(), person.SteamID, newPasskey(cred, body.Name, s.now())); err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.logStep(lc, "enrol", "ok", person.SteamID)
	s.finishEnrol(w, r, lc, person, pend, hash, "passkey")
}

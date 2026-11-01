// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"rsc.io/qr"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
)

// accountView is the account page.
type accountView struct {
	Account api.Account
	Note    string
	Error   string
	// Setup is a TOTP secret waiting for its first code.
	Setup *totpView
	// Codes are recovery codes, shown once.
	Codes []string
}

type totpView struct {
	Pending, Secret string
	QR              template.URL
}

func qrURL(uri string) template.URL {
	q, err := qr.Encode(uri, qr.M)
	if err != nil {
		return ""
	}
	q.Scale = 5
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(q.PNG())) //nolint:gosec // G203: a data URL we built from our own PNG
}

// accountPage renders the account page; note/errMsg are the result of an action, setup a TOTP in the making.
func (s *Server) accountPage(w http.ResponseWriter, r *http.Request, note, errMsg string, setup *totpView, codes []string) {
	b := s.backends[0]
	acc, err := s.client(b, r).Account(r.Context())
	v := accountView{Account: acc, Note: note, Error: errMsg, Setup: setup, Codes: codes}
	if err != nil {
		v.Error = err.Error()
	}
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	s.renderCode(w, status, "account", s.page(r, "Account", b.Name, "", "account", v))
}

// act runs an account action and shows the page again with its result. Account forms are plain forms: they send
// the token of the session as a field.
func (s *Server) accountAction(do func(c *apiclient.Client, r *http.Request) (note string, codes []string, err error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.csrfOK(r) {
			http.Error(w, "bad CSRF token", http.StatusForbidden)
			return
		}
		note, codes, err := do(s.client(s.backends[0], r), r)
		if err != nil {
			s.accountPage(w, r, "", err.Error(), nil, nil)
			return
		}
		if d := sessionFrom(r.Context()); d != nil {
			s.forgetInfo(d.secret) // the header shows the name or the username: read it again
			if info, err := s.sessionInfo(r, d.secret); err == nil {
				d.info = info
			}
		}
		s.accountPage(w, r, note, "", nil, codes)
	}
}

func (s *Server) accountGet(w http.ResponseWriter, r *http.Request) {
	s.accountPage(w, r, "", "", nil, nil)
}

func (s *Server) accountTOTPSetup(w http.ResponseWriter, r *http.Request) {
	setup, err := s.client(s.backends[0], r).TOTPBegin(r.Context())
	if err != nil {
		s.accountPage(w, r, "", err.Error(), nil, nil)
		return
	}
	s.accountPage(w, r, "", "", &totpView{Pending: setup.Pending, Secret: setup.Secret, QR: qrURL(setup.URI)}, nil)
}

func (s *Server) accountTOTPFinish(w http.ResponseWriter, r *http.Request) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	c := s.client(s.backends[0], r)
	if err := c.TOTPFinish(r.Context(), r.PostFormValue("pending"), strings.TrimSpace(r.PostFormValue("code"))); err != nil {
		// the secret stays valid for another try: show the same one again
		setup, serr := c.TOTPBegin(r.Context())
		if serr != nil {
			s.accountPage(w, r, "", err.Error(), nil, nil)
			return
		}
		s.accountPageSetupError(w, r, err.Error(), &totpView{Pending: setup.Pending, Secret: setup.Secret, QR: qrURL(setup.URI)})
		return
	}
	s.accountPage(w, r, "The authenticator app was replaced.", "", nil, nil)
}

// accountPageSetupError shows a failed first code next to a fresh secret.
func (s *Server) accountPageSetupError(w http.ResponseWriter, r *http.Request, msg string, setup *totpView) {
	s.accountPage(w, r, "", msg, setup, nil)
}

// ---- JSON endpoints for passkey.js ----

// accountScript checks a request from passkey.js on the account page.
func (s *Server) accountScript(w http.ResponseWriter, r *http.Request) bool {
	if !s.csrfOK(r) {
		jsonOut(w, http.StatusForbidden, map[string]string{"error": "bad token, reload the page"})
		return false
	}
	return true
}

func (s *Server) accountPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !s.accountScript(w, r) {
		return
	}
	pending, opts, err := s.client(s.backends[0], r).PasskeyBegin(r.Context())
	if err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"pending": pending, "options": json.RawMessage(opts)})
}

func (s *Server) accountPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	if !s.accountScript(w, r) {
		return
	}
	var body struct {
		Pending  string          `json:"pending"`
		Response json.RawMessage `json:"response"`
		Name     string          `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if _, err := s.client(s.backends[0], r).PasskeyFinish(r.Context(), body.Pending, body.Name, body.Response); err != nil {
		jsonErr(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]string{"next": "/account"})
}

// accountRoutes registers the account pages (inside the login check).
func (s *Server) accountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /account", s.accountGet)
	mux.HandleFunc("POST /account/name", s.accountAction(func(c *apiclient.Client, r *http.Request) (string, []string, error) {
		return "Your name was saved.", nil, c.SetName(r.Context(), r.PostFormValue("name"))
	}))
	mux.HandleFunc("POST /account/password", s.accountAction(func(c *apiclient.Client, r *http.Request) (string, []string, error) {
		err := c.SetPassword(r.Context(), r.PostFormValue("username"), r.PostFormValue("current"), r.PostFormValue("new"))
		return "Username and password saved.", nil, err
	}))
	mux.HandleFunc("POST /account/password/delete", s.accountAction(func(c *apiclient.Client, r *http.Request) (string, []string, error) {
		return "The password was removed; you sign in through Steam.", nil, c.RemovePassword(r.Context())
	}))
	mux.HandleFunc("GET /account/totp", s.accountTOTPSetup)
	mux.HandleFunc("POST /account/totp", s.accountTOTPFinish)
	mux.HandleFunc("POST /account/totp/delete", s.accountAction(func(c *apiclient.Client, r *http.Request) (string, []string, error) {
		return "The authenticator app was removed.", nil, c.RemoveTOTP(r.Context())
	}))
	mux.HandleFunc("POST /account/passkeys/delete", s.accountAction(func(c *apiclient.Client, r *http.Request) (string, []string, error) {
		return "The passkey was removed.", nil, c.RemovePasskey(r.Context(), r.PostFormValue("id"))
	}))
	mux.HandleFunc("POST /account/passkeys/begin", s.accountPasskeyBegin)
	mux.HandleFunc("POST /account/passkeys/finish", s.accountPasskeyFinish)
	mux.HandleFunc("POST /account/recovery", s.accountAction(func(c *apiclient.Client, r *http.Request) (string, []string, error) {
		codes, err := c.NewRecoveryCodes(r.Context())
		return "These are your new recovery codes. The old ones no longer work.", codes, err
	}))
	mux.HandleFunc("POST /account/sessions/delete", s.accountAction(func(c *apiclient.Client, r *http.Request) (string, []string, error) {
		return "Every other session was ended.", nil, c.LogoutOthers(r.Context())
	}))
}

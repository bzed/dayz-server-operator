// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getWithCookie sends GET / with one cookie and no jar, and returns the status and the redirect.
func (l *loginEnv) getWithCookie(name, value string) (status int, location string) {
	l.t.Helper()
	req, err := http.NewRequestWithContext(l.t.Context(), "GET", l.web.URL+"/", nil)
	require.NoError(l.t, err)
	req.AddCookie(&http.Cookie{Name: name, Value: value})
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	require.NoError(l.t, err)
	require.NoError(l.t, resp.Body.Close())
	return resp.StatusCode, resp.Header.Get("Location")
}

// Over HTTPS only the __Host- cookie logs in: a plain dzo_session cookie, which another party can plant,
// does not.
func TestSessionCookieNameFollowsTransport(t *testing.T) {
	l := newLoginEnv(t)
	l.ann("admin")
	b := l.visitor()
	b.login("")
	secret := b.cookie("dzo_session").Value

	code, _ := l.getWithCookie("dzo_session", secret)
	assert.Equal(t, 200, code, "plain HTTP issues and reads the plain name")
	code, _ = l.getWithCookie("__Host-dzo_session", secret)
	assert.Equal(t, 303, code, "and ignores the __Host- name")

	l.api.Login.WebURL = "https://dzo.example"
	l.srv.webURLCache.Store(nil) // the web interface asks again
	code, _ = l.getWithCookie("__Host-dzo_session", secret)
	assert.Equal(t, 200, code, "HTTPS reads the __Host- name")
	code, loc := l.getWithCookie("dzo_session", secret)
	assert.Equal(t, 303, code, "a planted plain cookie is no session")
	assert.Equal(t, "/login", loc)
}

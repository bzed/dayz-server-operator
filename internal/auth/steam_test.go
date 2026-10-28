// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
)

const (
	steamID  = "76561198000000001"
	returnTo = "https://dzo.example/login/steam/return?state=abc"
)

// fakeSteam answers check_authentication; valid says what it answers.
func fakeSteam(t *testing.T, valid bool, posted *atomic.Pointer[url.Values]) auth.Steam {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "post", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		if posted != nil {
			f := r.PostForm
			posted.Store(&f)
		}
		if r.PostForm.Get("openid.mode") != "check_authentication" {
			valid = false
		}
		w.Header().Set("Content-Type", "text/plain")
		if valid {
			_, _ = w.Write([]byte("ns:http://specs.openid.net/auth/2.0\nis_valid:true\n"))
			return
		}
		_, _ = w.Write([]byte("ns:http://specs.openid.net/auth/2.0\nis_valid:false\n"))
	}))
	t.Cleanup(ts.Close)
	return auth.Steam{Endpoint: ts.URL, HTTP: ts.Client()}
}

func goodQuery(endpoint string) url.Values {
	return url.Values{
		"openid.ns":             {"http://specs.openid.net/auth/2.0"},
		"openid.mode":           {"id_res"},
		"openid.op_endpoint":    {endpoint},
		"openid.claimed_id":     {"https://steamcommunity.com/openid/id/" + steamID},
		"openid.identity":       {"https://steamcommunity.com/openid/id/" + steamID},
		"openid.return_to":      {returnTo},
		"openid.response_nonce": {"2026-10-28T20:00:00Zabc123"},
		"openid.assoc_handle":   {"1234567890"},
		"openid.signed":         {"signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle"},
		"openid.sig":            {"c2ln"},
	}
}

func TestSteamVerify(t *testing.T) {
	var posted atomic.Pointer[url.Values]
	s := fakeSteam(t, true, &posted)
	id, nonce, err := s.Verify(t.Context(), goodQuery(s.Endpoint), returnTo)
	require.NoError(t, err)
	assert.Equal(t, steamID, id)
	assert.Equal(t, "2026-10-28T20:00:00Zabc123", nonce)
	got := posted.Load()
	require.NotNil(t, got)
	assert.Equal(t, "check_authentication", got.Get("openid.mode"))
	assert.Equal(t, "c2ln", got.Get("openid.sig"), "the received fields are posted back")
	assert.Equal(t, steamID, strings.TrimPrefix(got.Get("openid.claimed_id"), "https://steamcommunity.com/openid/id/"))
}

func TestSteamVerifyRefusals(t *testing.T) {
	good := fakeSteam(t, true, nil)
	bad := fakeSteam(t, false, nil)
	for name, c := range map[string]struct {
		s      auth.Steam
		mutate func(url.Values)
	}{
		"is_valid false":        {bad, func(url.Values) {}},
		"claimed id not steam":  {good, func(q url.Values) { q.Set("openid.claimed_id", "https://evil.example/openid/id/"+steamID) }},
		"claimed id http":       {good, func(q url.Values) { q.Set("openid.claimed_id", "http://steamcommunity.com/openid/id/"+steamID) }},
		"claimed id short":      {good, func(q url.Values) { q.Set("openid.claimed_id", "https://steamcommunity.com/openid/id/1234") }},
		"claimed id suffix":     {good, func(q url.Values) { q.Set("openid.claimed_id", "https://steamcommunity.com/openid/id/"+steamID+"1") }},
		"claimed id extra path": {good, func(q url.Values) { q.Set("openid.claimed_id", "https://steamcommunity.com/openid/id/"+steamID+"/x") }},
		"identity differs":      {good, func(q url.Values) { q.Set("openid.identity", "https://steamcommunity.com/openid/id/76561198000000002") }},
		"return_to differs":     {good, func(q url.Values) { q.Set("openid.return_to", "https://evil.example/") }},
		"op_endpoint differs":   {good, func(q url.Values) { q.Set("openid.op_endpoint", "https://evil.example/openid/login") }},
		"mode not id_res":       {good, func(q url.Values) { q.Set("openid.mode", "cancel") }},
		"no nonce":              {good, func(q url.Values) { q.Del("openid.response_nonce") }},
		"nonce not signed":      {good, func(q url.Values) { q.Set("openid.signed", "signed,op_endpoint,claimed_id,identity,return_to") }},
		"claimed id not signed": {good, func(q url.Values) { q.Set("openid.signed", "signed,op_endpoint,identity,return_to,response_nonce") }},
		"return_to not signed":  {good, func(q url.Values) { q.Set("openid.signed", "signed,op_endpoint,claimed_id,identity,response_nonce") }},
		"no signature":          {good, func(q url.Values) { q.Del("openid.sig") }},
		"two claimed ids": {good, func(q url.Values) {
			q.Add("openid.claimed_id", "https://steamcommunity.com/openid/id/76561198000000002")
		}},
	} {
		t.Run(name, func(t *testing.T) {
			q := goodQuery(c.s.Endpoint)
			c.mutate(q)
			id, nonce, err := c.s.Verify(t.Context(), q, returnTo)
			require.Error(t, err)
			assert.Empty(t, id)
			assert.Empty(t, nonce)
		})
	}
}

func TestSteamVerifyTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(ts.Close)
	s := auth.Steam{Endpoint: ts.URL, HTTP: &http.Client{Timeout: 50 * time.Millisecond}}
	_, _, err := s.Verify(t.Context(), goodQuery(ts.URL), returnTo)
	require.Error(t, err)
}

func TestSteamVerifyServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	s := auth.Steam{Endpoint: ts.URL, HTTP: ts.Client()}
	_, _, err := s.Verify(t.Context(), goodQuery(ts.URL), returnTo)
	require.Error(t, err)
}

func TestSteamRedirectURL(t *testing.T) {
	s := auth.Steam{}
	u, err := url.Parse(s.RedirectURL(returnTo, "https://dzo.example"))
	require.NoError(t, err)
	assert.Equal(t, "https://steamcommunity.com/openid/login", u.Scheme+"://"+u.Host+u.Path)
	q := u.Query()
	assert.Equal(t, "http://specs.openid.net/auth/2.0", q.Get("openid.ns"))
	assert.Equal(t, "checkid_setup", q.Get("openid.mode"))
	assert.Equal(t, returnTo, q.Get("openid.return_to"))
	assert.Equal(t, "https://dzo.example", q.Get("openid.realm"))
	assert.Equal(t, "http://specs.openid.net/auth/2.0/identifier_select", q.Get("openid.identity"))
	assert.Equal(t, "http://specs.openid.net/auth/2.0/identifier_select", q.Get("openid.claimed_id"))
}

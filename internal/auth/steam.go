// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	steamEndpoint = "https://steamcommunity.com/openid/login"
	openidNS      = "http://specs.openid.net/auth/2.0"
	identSelect   = "http://specs.openid.net/auth/2.0/identifier_select"
)

var claimedID = regexp.MustCompile(`^https://steamcommunity\.com/openid/id/([0-9]{17})$`)

// Steam verifies "Sign in through Steam" (OpenID 2.0).
type Steam struct {
	// Endpoint is Steam's OpenID address; empty means the real one. Tests
	// point it at a server of their own.
	Endpoint string
	// HTTP asks Steam to check a response; nil means a client with a 10 s timeout.
	HTTP *http.Client
}

func (s Steam) endpoint() string {
	if s.Endpoint == "" {
		return steamEndpoint
	}
	return s.Endpoint
}

// RedirectURL is where the browser is sent to sign in; Steam sends it back to
// returnTo, which must be under realm.
func (s Steam) RedirectURL(returnTo, realm string) string {
	q := url.Values{
		"openid.ns": {openidNS}, "openid.mode": {"checkid_setup"},
		"openid.return_to": {returnTo}, "openid.realm": {realm},
		"openid.identity": {identSelect}, "openid.claimed_id": {identSelect},
	}
	return s.endpoint() + "?" + q.Encode()
}

// steamIDFromClaimed takes the Steam ID out of a claimed_id.
func steamIDFromClaimed(claimed string) (string, bool) {
	m := claimedID.FindStringSubmatch(claimed)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// signedCovers reports whether the response signs the fields the checks rely
// on, so that none of them can be swapped by the one who sends the browser back.
func signedCovers(q url.Values) bool {
	have := map[string]bool{}
	for _, f := range strings.Split(q.Get("openid.signed"), ",") {
		have[f] = true
	}
	for _, f := range []string{"op_endpoint", "claimed_id", "identity", "return_to", "response_nonce"} {
		if !have[f] {
			return false
		}
	}
	return true
}

// single returns the one value of a field, or false if it is missing or repeated.
func single(q url.Values, field string) (string, bool) {
	v := q[field]
	if len(v) != 1 || v[0] == "" {
		return "", false
	}
	return v[0], true
}

// Verify checks the query Steam sent the browser back with: the field values
// are the expected ones, the interesting fields are signed, and Steam itself
// says the signature is valid (check_authentication). It returns the Steam ID
// and the response nonce, which the caller must accept only once.
func (s Steam) Verify(ctx context.Context, q url.Values, returnTo string) (steamID, nonce string, err error) {
	if mode, _ := single(q, "openid.mode"); mode != "id_res" {
		return "", "", errors.New("steam: not a positive assertion")
	}
	if ep, _ := single(q, "openid.op_endpoint"); ep != s.endpoint() {
		return "", "", errors.New("steam: the assertion is from another endpoint")
	}
	if rt, _ := single(q, "openid.return_to"); rt != returnTo {
		return "", "", errors.New("steam: the assertion is for another address")
	}
	claimed, ok := single(q, "openid.claimed_id")
	if !ok {
		return "", "", errors.New("steam: no claimed id")
	}
	if ident, _ := single(q, "openid.identity"); ident != claimed {
		return "", "", errors.New("steam: identity and claimed id differ")
	}
	id, ok := steamIDFromClaimed(claimed)
	if !ok {
		return "", "", errors.New("steam: the claimed id is not a Steam ID")
	}
	nonce, ok = single(q, "openid.response_nonce")
	if !ok {
		return "", "", errors.New("steam: no nonce")
	}
	if _, ok := single(q, "openid.sig"); !ok || !signedCovers(q) {
		return "", "", errors.New("steam: the assertion is not signed as needed")
	}

	form := url.Values{}
	for k, v := range q {
		if strings.HasPrefix(k, "openid.") {
			form[k] = v
		}
	}
	form.Set("openid.mode", "check_authentication")
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("steam: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("steam: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("steam: check_authentication answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", "", fmt.Errorf("steam: %w", err)
	}
	valid := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "is_valid:true" {
			valid = true
		}
	}
	if !valid {
		return "", "", errors.New("steam: Steam does not vouch for the assertion")
	}
	return id, nonce, nil
}

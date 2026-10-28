// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// call sends a request as the web token with a session (empty for none) and decodes the JSON answer.
func (l *loginEnv) call(method, path, session, body string, hdr ...string) (int, map[string]any) {
	l.t.Helper()
	code, b := l.as(method, path, l.tok, session, body, hdr...)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return code, out
}

// steamEnrol makes a person with a grant and no second factor, signs in through Steam and returns the
// pending login that has to enrol.
func (l *loginEnv) steamEnrol() string {
	l.t.Helper()
	require.NoError(l.t, l.m.st.PutPerson(l.t.Context(), moderation.Person{SteamID: steamA, Name: "Ann", Grants: []moderation.Grant{g("alpha", RoleModerator)}}, banNow))
	code, out, _ := l.steamStep(steamA)
	require.Equal(l.t, 200, code, "%v", out)
	require.Equal(l.t, true, out["enrol"])
	return out["pending"].(string)
}

func (l *loginEnv) enrolTOTP(pending string) (session string, secret []byte, recovery []string) {
	l.t.Helper()
	code, out, _ := l.post("/api/v1/auth/enrol/totp/begin", `{"pending":"`+pending+`"}`)
	require.Equal(l.t, 200, code, "%v", out)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(out["secret"].(string))
	require.NoError(l.t, err)
	assert.True(l.t, strings.HasPrefix(out["uri"].(string), "otpauth://totp/"))
	body, _ := json.Marshal(map[string]string{"pending": pending, "code": auth.TOTPCode(secret, l.srv.now())})
	code, out, _ = l.post("/api/v1/auth/enrol/totp/finish", string(body))
	require.Equal(l.t, 200, code, "%v", out)
	for _, c := range out["recovery_codes"].([]any) {
		recovery = append(recovery, c.(string))
	}
	return out["session"].(string), secret, recovery
}

func TestEnrolTOTPGivesRecoveryCodes(t *testing.T) {
	l := newLoginEnv(t)
	pending := l.steamEnrol()
	session, secret, recovery := l.enrolTOTP(pending)
	assert.Len(t, recovery, 10)
	code, _ := l.as("GET", "/api/v1", l.tok, session, "")
	assert.Equal(t, 200, code, "the session works")

	creds, err := l.m.st.Credentials(t.Context(), steamA)
	require.NoError(t, err)
	assert.Equal(t, secret, creds.TOTPSecret)
	assert.Equal(t, 10, creds.RecoveryLeft)

	// the code that enrolled cannot log in a second time
	st, out, _ := l.steamStep(steamA)
	require.Equal(t, 200, st)
	assert.Equal(t, false, out["enrol"])
	st, _, _ = l.second("totp", out["pending"].(string), auth.TOTPCode(secret, l.srv.now()))
	assert.Equal(t, 401, st, "the step was used when enrolling")
	st, _, _ = l.second("recovery", out["pending"].(string), recovery[0])
	assert.Equal(t, 200, st, "a recovery code works")

	// the pending login was used up
	code, _, _ = l.post("/api/v1/auth/enrol/totp/begin", `{"pending":"`+pending+`"}`)
	assert.Equal(t, 401, code)
}

func TestEnrolWrongCodeKeepsPending(t *testing.T) {
	l := newLoginEnv(t)
	pending := l.steamEnrol()
	_, out, _ := l.post("/api/v1/auth/enrol/totp/begin", `{"pending":"`+pending+`"}`)
	require.NotEmpty(t, out["secret"])
	code, _, _ := l.post("/api/v1/auth/enrol/totp/finish", `{"pending":"`+pending+`","code":"000000"}`)
	assert.Equal(t, 401, code)
	creds, _ := l.m.st.Credentials(t.Context(), steamA)
	assert.Nil(t, creds.TOTPSecret, "nothing is stored for a wrong code")
	code, _, _ = l.post("/api/v1/auth/enrol/totp/finish", `{"pending":"`+l.steamEnrolAgain()+`","code":"000000"}`)
	assert.Equal(t, 400, code, "finishing without begin")
}

// steamEnrolAgain signs in again as the person of steamEnrol.
func (l *loginEnv) steamEnrolAgain() string {
	_, out, _ := l.steamStep(steamA)
	return out["pending"].(string)
}

func TestEnrolOnlyWhenPendingSaysSo(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	_, out, _ := l.steamStep(steamA)
	pending := out["pending"].(string)
	for _, path := range []string{"totp/begin", "passkey/begin"} {
		code, _, _ := l.post("/api/v1/auth/enrol/"+path, `{"pending":"`+pending+`"}`)
		assert.Equal(t, 403, code, path+": a person with a second factor cannot add one at login")
	}
	code, _, _ := l.post("/api/v1/auth/enrol/totp/finish", `{"pending":"`+pending+`","code":"123456"}`)
	assert.Equal(t, 403, code)
}

func newRP(t *testing.T) *authenticator {
	t.Helper()
	a := newAuthenticator(t, "dzo.example", "https://dzo.example")
	a.handle = []byte(steamA)
	return a
}

func (l *loginEnv) enrolPasskey(pending string, a *authenticator) (int, map[string]any) {
	l.t.Helper()
	code, b := l.rawPost("/api/v1/auth/enrol/passkey/begin", `{"pending":"`+pending+`"}`)
	require.Equal(l.t, 200, code, string(b))
	body, _ := json.Marshal(map[string]any{"pending": pending, "response": a.register(b), "name": "laptop"})
	code, out, _ := l.post("/api/v1/auth/enrol/passkey/finish", string(body))
	return code, out
}

// rawPost is post that returns the body unparsed (WebAuthn options).
func (l *loginEnv) rawPost(path, body string) (int, []byte) {
	l.t.Helper()
	req, _ := http.NewRequest("POST", l.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+l.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(l.t, err)
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func (l *loginEnv) passkeyLogin(pending string, a *authenticator) (int, map[string]any) {
	l.t.Helper()
	code, b := l.rawPost("/api/v1/auth/passkey/begin", `{"pending":"`+pending+`"}`)
	if code != 200 {
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return code, out
	}
	body, _ := json.Marshal(map[string]any{"pending": pending, "response": a.assert(b)})
	st, out, _ := l.post("/api/v1/auth/passkey/finish", string(body))
	return st, out
}

func TestEnrolPasskeyThenLogin(t *testing.T) {
	l := newLoginEnv(t)
	a := newRP(t)
	code, out := l.enrolPasskey(l.steamEnrol(), a)
	require.Equal(t, 200, code, "%v", out)
	assert.Len(t, out["recovery_codes"], 10)
	session := out["session"].(string)
	st, _ := l.as("GET", "/api/v1", l.tok, session, "")
	assert.Equal(t, 200, st)

	creds, err := l.m.st.Credentials(t.Context(), steamA)
	require.NoError(t, err)
	require.Len(t, creds.Passkeys, 1)
	assert.Equal(t, "laptop", creds.Passkeys[0].Name)
	assert.Equal(t, a.credID, creds.Passkeys[0].ID)

	// and sign in again with it as the second factor
	_, out, _ = l.steamStep(steamA)
	assert.Equal(t, []any{"passkey", "recovery"}, out["factors"])
	code, out = l.passkeyLogin(out["pending"].(string), a)
	require.Equal(t, 200, code, "%v", out)
	assert.NotEmpty(t, out["session"])
	creds, _ = l.m.st.Credentials(t.Context(), steamA)
	assert.False(t, creds.Passkeys[0].LastUsed.IsZero(), "last use is noted")
}

func TestPasskeyIsNeverAFirstFactor(t *testing.T) {
	l := newLoginEnv(t)
	a := newRP(t)
	code, _ := l.enrolPasskey(l.steamEnrol(), a)
	require.Equal(t, 200, code)
	code, _ = l.passkeyLogin("dzp_nonexistent", a)
	assert.Equal(t, 401, code, "no pending login, no passkey login")
	code, b := l.rawPost("/api/v1/auth/passkey/begin", `{}`)
	assert.Equal(t, 401, code, string(b))
}

func TestPasskeyWrongOrigin(t *testing.T) {
	l := newLoginEnv(t)
	evil := newAuthenticator(t, "dzo.example", "https://evil.example")
	evil.handle = []byte(steamA)
	code, out := l.enrolPasskey(l.steamEnrol(), evil)
	assert.Equal(t, 401, code, "%v", out)
	creds, _ := l.m.st.Credentials(t.Context(), steamA)
	assert.Empty(t, creds.Passkeys)

	good := newRP(t)
	code, out = l.enrolPasskey(l.steamEnrolAgain(), good)
	require.Equal(t, 200, code, "%v", out)
	_, out, _ = l.steamStep(steamA)
	good.origin = "https://evil.example"
	code, _ = l.passkeyLogin(out["pending"].(string), good)
	assert.Equal(t, 401, code, "an assertion for another origin")
}

func TestPasskeyCounterUpdated(t *testing.T) {
	l := newLoginEnv(t)
	a := newRP(t)
	code, _ := l.enrolPasskey(l.steamEnrol(), a)
	require.Equal(t, 200, code)
	signCount := func() int {
		creds, err := l.m.st.Credentials(t.Context(), steamA)
		require.NoError(t, err)
		var c struct {
			Authenticator struct {
				SignCount float64 `json:"signCount"`
			} `json:"authenticator"`
		}
		require.NoError(t, json.Unmarshal(creds.Passkeys[0].Data, &c))
		return int(c.Authenticator.SignCount)
	}
	for want := 1; want <= 2; want++ {
		_, out, _ := l.steamStep(steamA)
		st, res := l.passkeyLogin(out["pending"].(string), a)
		require.Equal(t, 200, st, "%v", res)
		assert.Equal(t, want, signCount())
	}
	// a clone starts again from a low counter and is refused
	a.counter = 0
	_, out, _ := l.steamStep(steamA)
	st, _ := l.passkeyLogin(out["pending"].(string), a)
	assert.Equal(t, 401, st, "the counter went back")
}

func TestPasskeyBeginNeedsAPasskey(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	_, out, _ := l.steamStep(steamA)
	code, _ := l.passkeyLogin(out["pending"].(string), newRP(t))
	assert.Equal(t, 400, code, "TOTP only")
}

func TestAccountGet(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, out := l.call("GET", "/api/v1/account", session, "")
	require.Equal(t, 200, code)
	assert.Equal(t, steamA, out["steam_id"])
	assert.Equal(t, "Ann", out["name"])
	assert.Equal(t, "ann", out["username"])
	assert.Equal(t, true, out["has_password"])
	assert.Equal(t, true, out["totp"])
	assert.Empty(t, out["passkeys"])
	assert.EqualValues(t, 10, out["recovery_left"])
	assert.NotContains(t, out, "password_hash")
	code, _ = l.call("GET", "/api/v1/account", "", "")
	assert.Equal(t, 403, code, "a web token needs a session")
}

func TestAccountPasswordNeedsCurrent(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	body := func(cur, nw string) string {
		b, _ := json.Marshal(map[string]string{"username": "ann", "current": cur, "new": nw})
		return string(b)
	}
	const newPW = "another horse battery"
	code, _ := l.call("PUT", "/api/v1/account/password", session, body("", newPW))
	assert.Equal(t, 403, code, "no current password")
	code, _ = l.call("PUT", "/api/v1/account/password", session, body("wrong horse battery", newPW))
	assert.Equal(t, 403, code, "wrong current password")
	code, out := l.call("PUT", "/api/v1/account/password", session, body(password, "short"))
	assert.Equal(t, 400, code, "too short: %v", out)
	code, out = l.call("PUT", "/api/v1/account/password", session, body(password, newPW))
	require.Equal(t, 204, code, "%v", out)
	code, _, _ = l.passwordStep("ann", password)
	assert.Equal(t, 401, code, "the old password is gone")
	code, _, _ = l.passwordStep("ann", newPW)
	assert.Equal(t, 200, code)
}

func TestAccountSetsFirstPasswordWithoutCurrent(t *testing.T) {
	l := newLoginEnv(t)
	pending := l.steamEnrol()
	session, _, _ := l.enrolTOTP(pending)
	b, _ := json.Marshal(map[string]string{"username": "Ann.Smith", "new": password})
	code, out := l.call("PUT", "/api/v1/account/password", session, string(b))
	require.Equal(t, 204, code, "%v", out)
	code, _, _ = l.passwordStep("ann.smith", password)
	assert.Equal(t, 200, code)
	b, _ = json.Marshal(map[string]string{"new": password})
	l2 := l
	require.NoError(t, l2.m.st.ClearPassword(t.Context(), steamA))
	code, _ = l.call("PUT", "/api/v1/account/password", session, string(b))
	assert.Equal(t, 400, code, "a first password needs a username")
	for _, bad := range []string{"ab", "has space", "a/b/c", strings.Repeat("x", 33), "ünï"} {
		b, _ = json.Marshal(map[string]string{"username": bad, "new": password})
		code, _ = l.call("PUT", "/api/v1/account/password", session, string(b))
		assert.Equal(t, 400, code, bad)
	}
}

func TestUsernameTaken409(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	require.NoError(t, l.m.st.PutPerson(t.Context(), moderation.Person{SteamID: steamB, Name: "Bob", Grants: []moderation.Grant{g("alpha", RoleViewer)}}, banNow))
	h, err := auth.HashPassword(password)
	require.NoError(t, err)
	require.NoError(t, l.m.st.SetPassword(t.Context(), steamB, "bob", h))
	require.NoError(t, l.m.st.SetTOTP(t.Context(), steamB, totpSecret))
	session := l.loggedIn()
	b, _ := json.Marshal(map[string]string{"username": "BOB", "current": password, "new": "yet another horse"})
	code, _ := l.call("PUT", "/api/v1/account/password", session, string(b))
	assert.Equal(t, 409, code)
}

func TestAccountRemovePassword(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, _ := l.call("DELETE", "/api/v1/account/password", session, "")
	assert.Equal(t, 204, code)
	code, _, _ = l.passwordStep("ann", password)
	assert.Equal(t, 401, code)
}

func (l *loginEnv) accountPasskey(session string, a *authenticator) (int, map[string]any) {
	l.t.Helper()
	code, out := l.call("POST", "/api/v1/account/passkeys/begin", session, "")
	require.Equal(l.t, 200, code, "%v", out)
	opts, _ := json.Marshal(out["options"])
	body, _ := json.Marshal(map[string]any{"pending": out["pending"], "response": a.register(opts), "name": "phone"})
	return l.call("POST", "/api/v1/account/passkeys/finish", session, string(body))
}

func TestLastSecondFactorKept(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, out := l.call("DELETE", "/api/v1/account/totp", session, "")
	assert.Equal(t, 409, code)
	assert.Contains(t, out["error"], "last second factor")

	a := newRP(t)
	code, out = l.accountPasskey(session, a)
	require.Equal(t, 201, code, "%v", out)
	code, _ = l.call("DELETE", "/api/v1/account/totp", session, "")
	assert.Equal(t, 204, code, "a passkey is left")
	code, out = l.call("DELETE", "/api/v1/account/passkeys/"+b64.EncodeToString(a.credID), session, "")
	assert.Equal(t, 409, code, "%v", out)
	code, _ = l.call("DELETE", "/api/v1/account/passkeys/"+b64.EncodeToString([]byte("nope")), session, "")
	assert.Equal(t, 404, code)
	code, _ = l.call("DELETE", "/api/v1/account/passkeys/!!!", session, "")
	assert.Equal(t, 400, code)
}

func TestAccountTOTPReplace(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, out := l.call("POST", "/api/v1/account/totp/begin", session, "")
	require.Equal(t, 200, code, "%v", out)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(out["secret"].(string))
	require.NoError(t, err)
	pending := out["pending"].(string)
	body, _ := json.Marshal(map[string]string{"pending": pending, "code": "000000"})
	code, _ = l.call("POST", "/api/v1/account/totp/finish", session, string(body))
	assert.Equal(t, 401, code, "the new app has to prove itself")
	creds, _ := l.m.st.Credentials(t.Context(), steamA)
	assert.Equal(t, totpSecret, creds.TOTPSecret, "the old secret stays until then")
	body, _ = json.Marshal(map[string]string{"pending": pending, "code": auth.TOTPCode(secret, l.srv.now())})
	code, out = l.call("POST", "/api/v1/account/totp/finish", session, string(body))
	require.Equal(t, 204, code, "%v", out)
	creds, _ = l.m.st.Credentials(t.Context(), steamA)
	assert.Equal(t, secret, creds.TOTPSecret)
	// a login pending cannot finish an account change
	_, o, _ := l.passwordStep("ann", password)
	body, _ = json.Marshal(map[string]string{"pending": o["pending"].(string), "code": "123456"})
	code, _ = l.call("POST", "/api/v1/account/totp/finish", session, string(body))
	assert.Equal(t, 401, code)
}

func TestAccountNewRecoveryCodes(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	old := l.recovery[0]
	code, out := l.call("POST", "/api/v1/account/recovery", session, "")
	require.Equal(t, 200, code, "%v", out)
	assert.Len(t, out["recovery_codes"], 10)
	_, o, _ := l.passwordStep("ann", password)
	st, _, _ := l.second("recovery", o["pending"].(string), old)
	assert.Equal(t, 401, st, "a new set replaces the old one")
}

func TestLogoutOtherSessions(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	one := l.loggedIn()
	l.srv.now = func() time.Time { return banNow.Add(40 * time.Second) } // a fresh TOTP step
	two := l.loggedIn()
	code, _ := l.call("DELETE", "/api/v1/account/sessions", two, "")
	assert.Equal(t, 204, code)
	code, _ = l.as("GET", "/api/v1", l.tok, one, "")
	assert.Equal(t, 401, code, "the other session is gone")
	code, _ = l.as("GET", "/api/v1", l.tok, two, "")
	assert.Equal(t, 200, code, "this one stays")
}

func TestAccountChangesAreAudited(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, _ := l.call("POST", "/api/v1/account/recovery", session, "")
	require.Equal(t, 200, code)
	entries, err := l.srv.Audit.Tail("", 20)
	require.NoError(t, err)
	var seen bool
	for _, e := range entries {
		if e.Action == "account.recovery" {
			seen = true
			assert.Equal(t, "web:"+steamA, e.Actor)
			assert.NotContains(t, e.Detail+e.Target+e.Result, "-", "no code in the log") // a recovery code looks like abcde-fghij
		}
	}
	assert.True(t, seen)
}

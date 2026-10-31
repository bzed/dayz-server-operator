// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/base32"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

func star(role string) moderation.Grant { return moderation.Grant{Instance: "*", Role: role} }

func TestViewerSeesNoActionButtons(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	_, body, _ := e.get("/b/main/i/alpha")
	assert.NotContains(t, body, "Broadcast", "a viewer cannot broadcast")
	_, body, _ = e.get("/b/main/i/alpha/players/7656/panel")
	assert.Contains(t, body, "Your role has no actions on this instance")
	for _, control := range []string{"Kick", "Kill", "Heal", "Fix gear", "Teleport", "Message", "Ban…", "Give"} {
		assert.NotContains(t, body, control, control)
	}
	_, body, _ = e.get("/b/main/i/alpha/vehicles/1.2.3.4/panel")
	assert.Contains(t, body, "Your role has no actions")
	for _, control := range []string{"Repair", "Flip", "Delete"} {
		assert.NotContains(t, body, control, control)
	}
	_, body, _ = e.get("/b/main/bans")
	assert.NotContains(t, body, "New ban")

	// hiding is not the check: a forged request gets the API's refusal
	code, body := e.post("/b/main/i/alpha/players/7656/kick", url.Values{"reason": {"bye"}}, nil)
	assert.Equal(t, 200, code)
	assert.Contains(t, body, "flash err")
	assert.Contains(t, body, "lacks")
}

func TestOperatorOnASeesButtonsOnlyOnA(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	beta := e.api.Hubs.Add(admin.NewHub("beta"))
	beta.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.1.0", World: "enoch", Players: []admin.Player{{SteamID: "7656", Name: "bob"}}})
	e.grant(moderation.Grant{Instance: "alpha", Role: "operator"}, moderation.Grant{Instance: "beta", Role: "viewer"})

	_, body, _ := e.get("/b/main/i/alpha")
	assert.Contains(t, body, "Broadcast")
	_, body, _ = e.get("/b/main/i/alpha/players/7656/panel")
	for _, control := range []string{"Kick", "Kill", "Heal", "Teleport", "Give"} {
		assert.Contains(t, body, control, "operator on alpha: "+control)
	}
	_, body, _ = e.get("/b/main/i/beta")
	assert.NotContains(t, body, "Broadcast", "viewer on beta")
	_, body, _ = e.get("/b/main/i/beta/players/7656/panel")
	assert.Contains(t, body, "Your role has no actions")
	assert.NotContains(t, body, "Kill")

	// the switcher lists the instances the person has a role on, and the pages of the others are not found
	e.grant(moderation.Grant{Instance: "alpha", Role: "operator"})
	_, body, _ = e.get("/")
	assert.Contains(t, body, `href="/b/main/i/alpha"`)
	assert.NotContains(t, body, `href="/b/main/i/beta"`)
	code, _, _ := e.get("/b/main/i/beta")
	assert.Equal(t, 200, code, "the page renders its own error")
	_, body, _ = e.get("/b/main/i/beta")
	assert.Contains(t, body, "unknown instance")
}

func TestAccountPageShowsTheLogin(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	h, err := auth.HashPassword("correct horse battery")
	require.NoError(t, err)
	require.NoError(t, e.st.SetPassword(t.Context(), fixturePerson, "fix", h))
	code, body, _ := e.get("/account")
	require.Equal(t, 200, code)
	assert.Contains(t, body, "Your account")
	assert.Contains(t, body, ">fix<", "the username")
	assert.Contains(t, body, "Current password")
	assert.NotContains(t, body, "argon2", "no hash")
	assert.Contains(t, body, `href="/account"`, "the header links it")
}

func TestAccountNeedsTheToken(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	req := url.Values{"username": {"fix"}, "new": {"correct horse battery"}}
	code, _ := e.postForm("/account/password", req, false)
	assert.Equal(t, 403, code)
	code, _ = e.postForm("/account/password", req, true)
	assert.Equal(t, 200, code)
}

func TestAccountPasswordForm(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body := e.post("/account/password", url.Values{"username": {"fix"}, "new": {"short"}}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "12 to 256")
	code, body = e.post("/account/password", url.Values{"username": {"Fix.Admin"}, "new": {"correct horse battery"}}, nil)
	require.Equal(t, 200, code, body)
	assert.Contains(t, body, "saved")
	p, err := e.st.PersonByUsername(t.Context(), "fix.admin")
	require.NoError(t, err)
	assert.Equal(t, fixturePerson, p.SteamID)
	code, body = e.post("/account/password", url.Values{"username": {"fix.admin"}, "current": {"wrong horse battery"}, "new": {"yet another horse"}}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "current password is wrong")
	code, body = e.post("/account/password/delete", url.Values{}, nil)
	assert.Equal(t, 200, code, body)
	_, err = e.st.PersonByUsername(t.Context(), "fix.admin")
	require.ErrorIs(t, err, moderation.ErrNotFound)
}

func TestAccountTOTPSetupShowsQR(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	require.NoError(t, e.st.SetTOTP(t.Context(), fixturePerson, []byte("12345678901234567890")))
	code, body, _ := e.get("/account/totp")
	require.Equal(t, 200, code, body)
	assert.Regexp(t, `<img class="qr" src="data:image/png;base64,`, body, "the QR code")
	m := regexp.MustCompile(`<code id="totp-secret">([A-Z2-7]+)</code>`).FindStringSubmatch(body)
	require.NotNil(t, m, body)
	pending := regexp.MustCompile(`name="pending" value="([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, pending)

	code, body = e.post("/account/totp", url.Values{"pending": {pending[1]}, "code": {"000000"}}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "invalid code")
	assert.Contains(t, body, `id="totp-secret"`, "the setup stays for another try")
	_, body, _ = e.get("/account/totp")
	pending = regexp.MustCompile(`name="pending" value="([^"]+)"`).FindStringSubmatch(body)
	m = regexp.MustCompile(`<code id="totp-secret">([A-Z2-7]+)</code>`).FindStringSubmatch(body)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(m[1])
	require.NoError(t, err)
	code, body = e.post("/account/totp", url.Values{"pending": {pending[1]}, "code": {auth.TOTPCode(secret, time.Now())}}, nil)
	require.Equal(t, 200, code, body)
	assert.Contains(t, body, "replaced")
	creds, _ := e.st.Credentials(t.Context(), fixturePerson)
	assert.Equal(t, secret, creds.TOTPSecret)
}

func TestAccountRemoveLastFactorShowsError(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	require.NoError(t, e.st.SetTOTP(t.Context(), fixturePerson, []byte("12345678901234567890")))
	code, body := e.post("/account/totp/delete", url.Values{}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "last second factor")
	creds, _ := e.st.Credentials(t.Context(), fixturePerson)
	assert.NotNil(t, creds.TOTPSecret, "nothing was removed")
}

func TestAccountRecoveryCodesAndSessions(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body := e.post("/account/recovery", url.Values{}, nil)
	require.Equal(t, 200, code, body)
	pre := regexp.MustCompile(`(?s)<pre id="recovery-codes">(.*?)</pre>`).FindStringSubmatch(body)
	require.NotNil(t, pre, body)
	assert.Len(t, regexp.MustCompile(`[a-z2-7]{5}-[a-z2-7]{5}`).FindAllString(pre[1], -1), 10)
	_, hash, _ := auth.NewSecret("dzs_")
	require.NoError(t, e.st.CreateSession(t.Context(), hash, fixturePerson, "", time.Now()))
	code, body = e.post("/account/sessions/delete", url.Values{}, nil)
	assert.Equal(t, 200, code, body)
	_, err := e.st.Session(t.Context(), hash, time.Now(), time.Hour, time.Hour)
	require.ErrorIs(t, err, moderation.ErrNotFound, "the other session is gone")
	code, _, _ = e.get("/account")
	assert.Equal(t, 200, code, "and this one stays")
}

func TestUsersPageHiddenWithoutManage(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	code, body, _ := e.get("/users")
	assert.Equal(t, 403, code)
	assert.Contains(t, body, "admin role on every instance")
	_, body, _ = e.get("/")
	assert.NotContains(t, body, `href="/users"`, "no link for those who cannot use it")
	code, _ = e.post("/users/76561198000000001", url.Values{"grants": {"*=admin"}}, nil)
	assert.Equal(t, 403, code, "and no way around it")

	e.grant(moderation.Grant{Instance: "alpha", Role: "admin"})
	code, _, _ = e.get("/users")
	assert.Equal(t, 403, code, "admin on one instance is not enough")

	e.grant(star("admin"))
	code, body, _ = e.get("/users")
	assert.Equal(t, 200, code)
	assert.Contains(t, body, "(you)")
	_, body, _ = e.get("/")
	assert.Contains(t, body, `href="/users"`)
}

func TestUsersPagePutsGrants(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	const bob = "76561198000000002"
	code, body := e.post("/users", url.Values{"steam_id": {bob}, "name": {"Bob"}, "grants": {"alpha=operator, *=viewer"}}, nil)
	require.Equal(t, 200, code, body)
	assert.Contains(t, body, "Saved "+bob)
	assert.Contains(t, body, "*=viewer, alpha=operator")
	p, err := e.st.Person(t.Context(), bob)
	require.NoError(t, err)
	assert.Equal(t, []moderation.Grant{{Instance: "*", Role: "viewer"}, {Instance: "alpha", Role: "operator"}}, p.Grants)

	code, body = e.post("/users/"+bob, url.Values{"name": {"Bobby"}, "grants": {"alpha=moderator"}, "disabled": {"1"}}, nil)
	require.Equal(t, 200, code, body)
	p, _ = e.st.Person(t.Context(), bob)
	assert.Equal(t, "Bobby", p.Name)
	assert.True(t, p.Disabled)
	assert.Equal(t, []moderation.Grant{{Instance: "alpha", Role: "moderator"}}, p.Grants)

	code, body = e.post("/users/"+bob, url.Values{"grants": {"alpha operator"}}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "instance=role")
	code, body = e.post("/users/"+bob, url.Values{"grants": {"alpha=root"}}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "role")

	require.NoError(t, e.st.SetPassword(t.Context(), bob, "bob", "h"))
	code, body = e.post("/users/"+bob+"/reset", url.Values{}, nil)
	require.Equal(t, 200, code, body)
	_, err = e.st.PersonByUsername(t.Context(), "bob")
	require.ErrorIs(t, err, moderation.ErrNotFound)
	code, body = e.post("/users/"+bob+"/delete", url.Values{}, nil)
	require.Equal(t, 200, code, body)
	_, err = e.st.Person(t.Context(), bob)
	require.ErrorIs(t, err, moderation.ErrNotFound)
}

func TestUsersPageDoesNotOfferChangesToOneself(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/users")
	assert.NotContains(t, body, `action="/users/`+fixturePerson, "no form for your own row")
	// a forged request is refused by the API
	code, body := e.post("/users/"+fixturePerson, url.Values{"grants": {"alpha=viewer"}}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "you cannot change your own access")
	code, body = e.post("/users/"+fixturePerson+"/delete", url.Values{}, nil)
	assert.Equal(t, 422, code)
	assert.Contains(t, body, "you cannot change your own access")
}

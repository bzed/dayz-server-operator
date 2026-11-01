// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/webauthn"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// newBrowserLogin is a login environment a real browser can use: the assets are in place, and the public address
// is localhost, because a passkey needs a domain name and not an IP address as its relying party.
func newBrowserLogin(t *testing.T) (*loginEnv, string) {
	t.Helper()
	assets := t.TempDir()
	useRealAssets(t, assets)
	l := newLoginEnv(t, func(o *Options) { o.Assets = assets })
	u, err := url.Parse(l.web.URL)
	require.NoError(t, err)
	base := "http://localhost:" + u.Port()
	l.api.Login.WebURL = base
	l.srv.webURLCache.Store(nil)
	return l, base
}

// waitPath waits until the browser is on a page with the path.
func (b *browser) waitPath(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var loc string
		if err := chromedp.Run(b.ctx, chromedp.Location(&loc)); err != nil {
			return false
		}
		u, err := url.Parse(loc)
		return err == nil && u.Path == path
	}, 15*time.Second, 100*time.Millisecond, "the browser did not get to %s", path)
}

func TestE2EPasswordTOTPLogin(t *testing.T) {
	l, base := newBrowserLogin(t)
	l.ann("admin")
	b := newBrowser(t)
	b.run(t,
		chromedp.Navigate(base+"/b/main/bans"),
		chromedp.WaitVisible(`input[name=username]`),
	)
	b.waitPath(t, "/login")
	b.run(t,
		chromedp.SendKeys(`input[name=username]`, "ann"),
		chromedp.SendKeys(`input[name=password]`, loginPW),
		chromedp.Click(`form button.primary`),
		chromedp.WaitVisible(`input[name=code]`),
	)
	b.waitPath(t, "/login/second")
	b.run(t,
		chromedp.SendKeys(`input[name=code]`, auth.TOTPCode(loginTOTP, time.Now())),
		chromedp.Click(`form button.primary`),
		chromedp.WaitVisible(`.banform, .filters`),
	)
	b.waitPath(t, "/b/main/bans") // the page it was going to
	var who string
	b.run(t, chromedp.Text(`a.account`, &who))
	assert.Contains(t, who, "ann", "the header shows the username, not the name")
}

func TestE2ESteamLogin(t *testing.T) {
	l, base := newBrowserLogin(t)
	l.ann("admin")
	b := newBrowser(t)
	b.run(t,
		chromedp.Navigate(base+"/login"),
		chromedp.Click(`a[href="/login/steam"]`),
		chromedp.WaitVisible(`input[name=code]`), // Steam, the return, the refresh page, the second step
	)
	b.waitPath(t, "/login/second")
	b.run(t,
		chromedp.SendKeys(`input[name=code]`, auth.TOTPCode(loginTOTP, time.Now())),
		chromedp.Click(`form button.primary`),
		chromedp.WaitVisible(`a.account`),
	)
	b.waitPath(t, "/")
}

// addAuthenticator gives the browser a virtual passkey (CTAP2, internal, resident key, user verification).
func (b *browser) addAuthenticator(t *testing.T) {
	t.Helper()
	b.run(t,
		webauthn.Enable(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := webauthn.AddVirtualAuthenticator(&webauthn.VirtualAuthenticatorOptions{
				Protocol: webauthn.AuthenticatorProtocolCtap2, Ctap2version: webauthn.Ctap2versionCtap21, Transport: webauthn.AuthenticatorTransportInternal,
				HasResidentKey: true, HasUserVerification: true, IsUserVerified: true, AutomaticPresenceSimulation: true,
			}).Do(ctx)
			return err
		}),
	)
}

func TestE2EPasskeyEnrolAndLogin(t *testing.T) {
	l, base := newBrowserLogin(t)
	require.NoError(t, l.st.PutPerson(t.Context(), moderation.Person{SteamID: loginSteamA, Name: "Ann", Grants: []moderation.Grant{{Instance: "*", Role: "admin"}}}, time.Now()))
	b := newBrowser(t)
	b.addAuthenticator(t)

	// first visit: Steam, then enrol a passkey instead of an app, then the recovery codes
	b.run(t,
		chromedp.Navigate(base+"/login"),
		chromedp.Click(`a[href="/login/steam"]`),
		chromedp.WaitVisible(`#passkey-enrol`),
		chromedp.Click(`#passkey-enrol`),
		chromedp.WaitVisible(`#recovery-after`),
	)
	var codes string
	b.run(t, chromedp.Text(`#recovery-codes`, &codes))
	assert.Len(t, strings.Fields(codes), 10, "the recovery codes are shown once")
	b.run(t, chromedp.Click(`#recovery-continue`), chromedp.WaitVisible(`a.account`))

	// log out, sign in again: Steam, then the passkey as the second step
	b.run(t, chromedp.Click(`form.logout button`), chromedp.WaitVisible(`input[name=username]`))
	b.waitPath(t, "/login")
	b.run(t,
		chromedp.Click(`a[href="/login/steam"]`),
		chromedp.WaitVisible(`#passkey-login`),
		chromedp.Click(`#passkey-login`),
		chromedp.WaitVisible(`a.account`),
	)
	b.waitPath(t, "/")
	creds, err := l.st.Credentials(t.Context(), loginSteamA)
	require.NoError(t, err)
	require.Len(t, creds.Passkeys, 1)
	assert.False(t, creds.Passkeys[0].LastUsed.IsZero())
}

func TestE2EViewerCannotKick(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	useRealAssets(t, e.assets)
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha")), chromedp.WaitReady("body"))
	await := func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }
	var text string
	b.run(t, chromedp.Evaluate(`fetch('/b/main/i/alpha/players/7656/panel').then(r => r.text())`, &text, await))
	assert.NotContains(t, text, "Kick", "no button for a viewer")
	assert.Contains(t, text, "no actions")

	// a forged request, as a script on the page would send it, gets the API's refusal
	var answer string
	b.run(t, chromedp.Evaluate(`fetch('/b/main/i/alpha/players/7656/kick', {method: 'POST', headers: {'X-CSRF-Token': `+jsString(e.csrf)+`, 'Content-Type': 'application/x-www-form-urlencoded'}, body: 'reason=bye'}).then(r => r.text())`, &answer, await))
	assert.Contains(t, answer, "flash err")
	assert.Contains(t, answer, "lacks")
}

func TestE2ELogout(t *testing.T) {
	l, base := newBrowserLogin(t)
	l.ann("admin")
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(base+"/login"), chromedp.WaitVisible(`input[name=username]`),
		chromedp.SendKeys(`input[name=username]`, "ann"), chromedp.SendKeys(`input[name=password]`, loginPW),
		chromedp.Click(`form button.primary`), chromedp.WaitVisible(`input[name=code]`),
		chromedp.SendKeys(`input[name=code]`, auth.TOTPCode(loginTOTP, time.Now())), chromedp.Click(`form button.primary`),
		chromedp.WaitVisible(`form.logout button`))
	b.run(t, chromedp.Click(`form.logout button`), chromedp.WaitVisible(`input[name=username]`))
	b.waitPath(t, "/login")
	b.run(t, chromedp.Navigate(base+"/b/main/bans"), chromedp.WaitVisible(`input[name=username]`))
	b.waitPath(t, "/login")
}

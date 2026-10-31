// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/bzed/dayz-server-operator/internal/api"
)

// call runs a JSON request that must answer want, and decodes the answer into out when there is one.
func (c *Client) call(ctx context.Context, method, path string, body any, want int, out any) error {
	status, b, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status != want {
		return apiErr(status, b)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.call(ctx, http.MethodPost, path, body, http.StatusOK, out)
}

type pendingReq struct {
	Pending string `json:"pending"`
}

type codeReq struct {
	Pending string `json:"pending"`
	Code    string `json:"code"`
}

type responseReq struct {
	Pending  string          `json:"pending"`
	Response json.RawMessage `json:"response"`
	Name     string          `json:"name,omitempty"`
}

// ---- login (a web token without a session) ----

// AuthInfo asks whether logins are on and where the web interface is meant to be reached (needs no session).
func (c *Client) AuthInfo(ctx context.Context) (out api.AuthInfo, err error) {
	return out, c.get(ctx, "/api/v1/auth/info", &out)
}

// LoginPassword is the first factor with a password.
func (c *Client) LoginPassword(ctx context.Context, username, password string) (out api.LoginPending, err error) {
	return out, c.post(ctx, "/api/v1/auth/password", map[string]string{"username": username, "password": password}, &out)
}

// SteamURL is where to send the browser to sign in through Steam; returnTo is where Steam sends it back.
func (c *Client) SteamURL(ctx context.Context, returnTo string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	err := c.get(ctx, "/api/v1/auth/steam?return_to="+url.QueryEscape(returnTo), &out)
	return out.URL, err
}

// LoginSteam is the first factor with Steam: the query Steam sent the browser back with.
func (c *Client) LoginSteam(ctx context.Context, params map[string][]string, returnTo string) (out api.LoginPending, err error) {
	return out, c.post(ctx, "/api/v1/auth/steam", map[string]any{"params": params, "return_to": returnTo}, &out)
}

// LoginTOTP is the second factor with a TOTP code.
func (c *Client) LoginTOTP(ctx context.Context, pending, code string) (out api.LoginSession, err error) {
	return out, c.post(ctx, "/api/v1/auth/totp", codeReq{pending, code}, &out)
}

// LoginRecovery is the second factor with a recovery code.
func (c *Client) LoginRecovery(ctx context.Context, pending, code string) (out api.LoginSession, err error) {
	return out, c.post(ctx, "/api/v1/auth/recovery", codeReq{pending, code}, &out)
}

// PasskeyLoginBegin returns the WebAuthn request options for the browser.
func (c *Client) PasskeyLoginBegin(ctx context.Context, pending string) (out json.RawMessage, err error) {
	return out, c.post(ctx, "/api/v1/auth/passkey/begin", pendingReq{pending}, &out)
}

// PasskeyLoginFinish gives the browser's answer and gets the session.
func (c *Client) PasskeyLoginFinish(ctx context.Context, pending string, response json.RawMessage) (out api.LoginSession, err error) {
	return out, c.post(ctx, "/api/v1/auth/passkey/finish", responseReq{Pending: pending, Response: response}, &out)
}

// TOTPSetup is a new TOTP secret: Secret in base32, URI for the QR code. Pending is set for an account change.
type TOTPSetup struct {
	Pending string `json:"pending,omitempty"`
	Secret  string `json:"secret"`
	URI     string `json:"uri"`
}

// EnrolTOTPBegin makes the TOTP secret of a person who has no second factor yet.
func (c *Client) EnrolTOTPBegin(ctx context.Context, pending string) (out TOTPSetup, err error) {
	return out, c.post(ctx, "/api/v1/auth/enrol/totp/begin", pendingReq{pending}, &out)
}

// EnrolTOTPFinish proves the app with a first code and gets the session and the recovery codes.
func (c *Client) EnrolTOTPFinish(ctx context.Context, pending, code string) (out api.LoginSession, err error) {
	return out, c.post(ctx, "/api/v1/auth/enrol/totp/finish", codeReq{pending, code}, &out)
}

// EnrolPasskeyBegin returns the WebAuthn creation options for the browser.
func (c *Client) EnrolPasskeyBegin(ctx context.Context, pending string) (out json.RawMessage, err error) {
	return out, c.post(ctx, "/api/v1/auth/enrol/passkey/begin", pendingReq{pending}, &out)
}

// EnrolPasskeyFinish gives the browser's answer and gets the session and the recovery codes.
func (c *Client) EnrolPasskeyFinish(ctx context.Context, pending, name string, response json.RawMessage) (out api.LoginSession, err error) {
	return out, c.post(ctx, "/api/v1/auth/enrol/passkey/finish", responseReq{Pending: pending, Response: response, Name: name}, &out)
}

// Logout ends the session of this client.
func (c *Client) Logout(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent, nil)
}

// ---- the account of the person of the session ----

// Account returns the person's login settings.
func (c *Client) Account(ctx context.Context) (out api.Account, err error) {
	return out, c.get(ctx, "/api/v1/account", &out)
}

// SetPassword sets the username and password; current is needed when there is a password.
func (c *Client) SetPassword(ctx context.Context, username, current, newPassword string) error {
	return c.call(ctx, http.MethodPut, "/api/v1/account/password", map[string]string{"username": username, "current": current, "new": newPassword}, http.StatusNoContent, nil)
}

// RemovePassword removes the password and username.
func (c *Client) RemovePassword(ctx context.Context) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/account/password", nil, http.StatusNoContent, nil)
}

// TOTPBegin makes a secret for the app that is to replace or add the TOTP of the person.
func (c *Client) TOTPBegin(ctx context.Context) (out TOTPSetup, err error) {
	return out, c.post(ctx, "/api/v1/account/totp/begin", nil, &out)
}

// TOTPFinish stores the secret once the app showed a right code.
func (c *Client) TOTPFinish(ctx context.Context, pending, code string) error {
	return c.call(ctx, http.MethodPost, "/api/v1/account/totp/finish", codeReq{pending, code}, http.StatusNoContent, nil)
}

// RemoveTOTP removes the TOTP secret (409 if it is the last second factor).
func (c *Client) RemoveTOTP(ctx context.Context) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/account/totp", nil, http.StatusNoContent, nil)
}

// PasskeyBegin returns the id of the registration and the WebAuthn creation options.
func (c *Client) PasskeyBegin(ctx context.Context) (pending string, options json.RawMessage, err error) {
	var out struct {
		Pending string          `json:"pending"`
		Options json.RawMessage `json:"options"`
	}
	err = c.post(ctx, "/api/v1/account/passkeys/begin", nil, &out)
	return out.Pending, out.Options, err
}

// PasskeyFinish gives the browser's answer and registers the passkey.
func (c *Client) PasskeyFinish(ctx context.Context, pending, name string, response json.RawMessage) (out api.PasskeyInfo, err error) {
	return out, c.call(ctx, http.MethodPost, "/api/v1/account/passkeys/finish", responseReq{Pending: pending, Response: response, Name: name}, http.StatusCreated, &out)
}

// RemovePasskey removes a passkey by its id (409 if it is the last second factor).
func (c *Client) RemovePasskey(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/account/passkeys/"+url.PathEscape(id), nil, http.StatusNoContent, nil)
}

// NewRecoveryCodes makes a new set of recovery codes, replacing the old one.
func (c *Client) NewRecoveryCodes(ctx context.Context) ([]string, error) {
	var out struct {
		Codes []string `json:"recovery_codes"`
	}
	err := c.post(ctx, "/api/v1/account/recovery", nil, &out)
	return out.Codes, err
}

// LogoutOthers ends all other sessions of the person.
func (c *Client) LogoutOthers(ctx context.Context) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/account/sessions", nil, http.StatusNoContent, nil)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"net/http"
	"net/url"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

func userPath(steamID string) string { return "/api/v1/users/" + url.PathEscape(steamID) }

// Users lists the people with their grants (needs users.manage).
func (c *Client) Users(ctx context.Context) (out []moderation.Person, err error) {
	return out, c.get(ctx, "/api/v1/users", &out)
}

// PutUser creates a person or replaces their name, disabled flag and grants.
func (c *Client) PutUser(ctx context.Context, steamID string, body api.UserBody) (out moderation.Person, err error) {
	return out, c.call(ctx, http.MethodPut, userPath(steamID), body, http.StatusOK, &out)
}

// DeleteUser deletes a person.
func (c *Client) DeleteUser(ctx context.Context, steamID string) error {
	return c.call(ctx, http.MethodDelete, userPath(steamID), nil, http.StatusNoContent, nil)
}

// ResetUser removes the credentials and sessions of a person.
func (c *Client) ResetUser(ctx context.Context, steamID string) error {
	return c.call(ctx, http.MethodPost, userPath(steamID)+"/reset", nil, http.StatusNoContent, nil)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// PlayerHistoryFilter selects players of the player database; empty fields do not filter.
type PlayerHistoryFilter struct {
	Query, Instance string
	Flagged         bool
	Limit, Offset   int
}

// PlayerHistory lists the players who ever connected, most recently seen first, and how many match in all.
func (c *Client) PlayerHistory(ctx context.Context, f PlayerHistoryFilter) ([]moderation.PlayerSummary, int, error) {
	q := url.Values{}
	if f.Query != "" {
		q.Set("q", f.Query)
	}
	if f.Instance != "" {
		q.Set("instance", f.Instance)
	}
	if f.Flagged {
		q.Set("flagged", "1")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", strconv.Itoa(f.Offset))
	}
	var out struct {
		Players []moderation.PlayerSummary `json:"players"`
		Total   int                        `json:"total"`
	}
	if err := c.get(ctx, "/api/v1/players?"+q.Encode(), &out); err != nil {
		return nil, 0, err
	}
	return out.Players, out.Total, nil
}

// PlayerDetail returns everything known about one player.
func (c *Client) PlayerDetail(ctx context.Context, steamID string) (out api.PlayerDetail, err error) {
	return out, c.get(ctx, "/api/v1/players/"+url.PathEscape(steamID), &out)
}

// LookupPlayer asks BattlEye and Steam about a player now and returns the fresh record. It answers a
// 429 *Error for a player looked up a few minutes ago.
func (c *Client) LookupPlayer(ctx context.Context, steamID string) (out api.PlayerDetail, err error) {
	return out, c.send(ctx, http.MethodPost, "/api/v1/players/"+url.PathEscape(steamID)+"/lookup", nil, http.StatusOK, &out)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// BanFilter selects bans; empty fields do not filter.
type BanFilter struct {
	State, Instance, Query string
	Limit                  int
}

// send runs a JSON request and decodes the answer when its status is want.
func (c *Client) send(ctx context.Context, method, path string, body any, want int, out any) error {
	status, b, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status != want {
		return apiErr(status, b)
	}
	return json.Unmarshal(b, out)
}

func banPath(id int64) string { return "/api/v1/bans/" + strconv.FormatInt(id, 10) }

// Bans lists bans, newest first.
func (c *Client) Bans(ctx context.Context, f BanFilter) (out []moderation.Ban, err error) {
	q := url.Values{}
	for k, v := range map[string]string{"state": f.State, "instance": f.Instance, "q": f.Query} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return out, c.get(ctx, "/api/v1/bans?"+q.Encode(), &out)
}

// Ban returns one ban.
func (c *Client) Ban(ctx context.Context, id int64) (out moderation.Ban, err error) {
	return out, c.get(ctx, banPath(id), &out)
}

// BanHistory returns the changes of a ban.
func (c *Client) BanHistory(ctx context.Context, id int64) (out []moderation.HistoryEntry, err error) {
	return out, c.get(ctx, banPath(id)+"/history", &out)
}

// CreateBan makes a ban.
func (c *Client) CreateBan(ctx context.Context, in moderation.BanInput) (out api.BanResult, err error) {
	return out, c.send(ctx, http.MethodPost, "/api/v1/bans", in, http.StatusCreated, &out)
}

// UpdateBan replaces a ban's editable fields.
func (c *Client) UpdateBan(ctx context.Context, id int64, in moderation.BanInput) (out api.BanResult, err error) {
	return out, c.send(ctx, http.MethodPut, banPath(id), in, http.StatusOK, &out)
}

// RevokeBan ends a ban.
func (c *Client) RevokeBan(ctx context.Context, id int64, reason string) (out api.BanResult, err error) {
	return out, c.send(ctx, http.MethodPost, banPath(id)+"/revoke", api.RevokeBody{Reason: reason}, http.StatusOK, &out)
}

// Evidence lists the evidence filed against a player.
func (c *Client) Evidence(ctx context.Context, who moderation.Who) (out []moderation.Evidence, err error) {
	q := url.Values{}
	for k, v := range map[string]string{"steam_id": who.SteamID, "guid": who.GUID, "ip": who.IP} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return out, c.get(ctx, "/api/v1/evidence?"+q.Encode(), &out)
}

// EvidenceItem returns one evidence item.
func (c *Client) EvidenceItem(ctx context.Context, id int64) (out moderation.Evidence, err error) {
	return out, c.get(ctx, "/api/v1/evidence/"+strconv.FormatInt(id, 10), &out)
}

// AddEvidence files log lines or a note.
func (c *Client) AddEvidence(ctx context.Context, in moderation.EvidenceInput) (out moderation.Evidence, err error) {
	return out, c.send(ctx, http.MethodPost, "/api/v1/evidence", in, http.StatusCreated, &out)
}

// AddEvidenceImage uploads a screenshot with a caption (in.Body).
func (c *Client) AddEvidenceImage(ctx context.Context, in moderation.EvidenceInput, image []byte) (out moderation.Evidence, err error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{"steam_id": in.SteamID, "guid": in.GUID, "ip": in.IP, "instance": in.Instance, "body": in.Body} {
		if err := mw.WriteField(k, v); err != nil {
			return out, err
		}
	}
	fw, err := mw.CreateFormFile("file", "evidence")
	if err != nil {
		return out, err
	}
	if _, err := fw.Write(image); err != nil {
		return out, err
	}
	if err := mw.Close(); err != nil {
		return out, err
	}
	r, err := c.newReq(ctx, http.MethodPost, "/api/v1/evidence", &body, mw.FormDataContentType())
	if err != nil {
		return out, err
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return out, err
	}
	if resp.StatusCode != http.StatusCreated {
		return out, apiErr(resp.StatusCode, b)
	}
	return out, json.Unmarshal(b, &out)
}

// EvidenceFile fetches an image; the caller closes the body.
func (c *Client) EvidenceFile(ctx context.Context, id int64) (*http.Response, error) {
	r, err := c.req(ctx, http.MethodGet, fmt.Sprintf("/api/v1/evidence/%d/file", id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, apiErr(resp.StatusCode, b)
	}
	return resp, nil
}

// Identities returns where a Steam ID's GUID was seen, with how many others share each IP.
func (c *Client) Identities(ctx context.Context, steamID string) (out []moderation.Sighting, err error) {
	return out, c.get(ctx, "/api/v1/players/"+url.PathEscape(steamID)+"/identities", &out)
}

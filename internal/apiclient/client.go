// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apiclient is the Go client of /api/v1 (internal/api). The web
// interface and the `dzo player|vehicle` commands use only this package to
// reach an installation, so one web interface can serve several installations:
// it holds one Client per installation.
package apiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
)

// Client talks to one installation.
type Client struct {
	Base  string // e.g. http://127.0.0.1:8080
	Token string
	// Actor and Source go into the audit log (delegate tokens only): the
	// human the web interface acts for, and where the action came from.
	Actor, Source string
	HTTP          *http.Client
}

// New returns a client with a sane timeout. Streams use their own context.
func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 40 * time.Second}}
}

// WithActor returns a copy that reports the given actor and source.
func (c *Client) WithActor(actor, source string) *Client {
	d := *c
	d.Actor, d.Source = actor, source
	return &d
}

// Error is a non-2xx answer of the API.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("api: %d %s", e.Status, e.Message) }

func (c *Client) newReq(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if c.Actor != "" {
		r.Header.Set("X-Dzo-Actor", c.Actor)
	}
	if c.Source != "" {
		r.Header.Set("X-Dzo-Source", c.Source)
	}
	return r, nil
}

func (c *Client) req(ctx context.Context, method, path string, body any) (*http.Request, error) {
	if body == nil {
		return c.newReq(ctx, method, path, nil, "")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.newReq(ctx, method, path, bytes.NewReader(b), "application/json")
}

func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	r, err := c.req(ctx, method, path, body)
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.StatusCode, b, err
}

func apiErr(status int, b []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) != nil || e.Error == "" {
		e.Error = strings.TrimSpace(string(b))
	}
	return &Error{Status: status, Message: e.Error}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	status, b, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiErr(status, b)
	}
	return json.Unmarshal(b, out)
}

// action runs a command route. The API answers 200 (done), 202 (queued) or
// 422 (the mod refused) with an ActionResult; the caller checks OK.
func (c *Client) action(ctx context.Context, method, path string, body any) (api.ActionResult, error) {
	status, b, err := c.do(ctx, method, path, body)
	if err != nil {
		return api.ActionResult{}, err
	}
	switch status {
	case http.StatusOK, http.StatusAccepted, http.StatusUnprocessableEntity:
		var r api.ActionResult
		return r, json.Unmarshal(b, &r)
	}
	return api.ActionResult{}, apiErr(status, b)
}

func inst(name string) string { return "/api/v1/instances/" + url.PathEscape(name) }

// Info returns the installation's identity and the caller's role.
func (c *Client) Info(ctx context.Context) (info api.Info, err error) {
	return info, c.get(ctx, "/api/v1", &info)
}

// Instances lists the instances the token may see.
func (c *Client) Instances(ctx context.Context) (out []admin.Status, err error) {
	return out, c.get(ctx, "/api/v1/instances", &out)
}

// Status returns one instance's connection state.
func (c *Client) Status(ctx context.Context, name string) (out admin.Status, err error) {
	return out, c.get(ctx, inst(name), &out)
}

// Players returns the online players.
func (c *Client) Players(ctx context.Context, name string) (out []admin.Player, err error) {
	return out, c.get(ctx, inst(name)+"/players", &out)
}

// Vehicles returns the persistent vehicles.
func (c *Client) Vehicles(ctx context.Context, name string) (out []admin.Vehicle, err error) {
	return out, c.get(ctx, inst(name)+"/vehicles", &out)
}

// Events returns the active in-game events.
func (c *Client) Events(ctx context.Context, name string) (out []admin.Event, err error) {
	return out, c.get(ctx, inst(name)+"/events", &out)
}

// Layers returns the marker layers visible to the token.
func (c *Client) Layers(ctx context.Context, name string) (out []admin.Layer, err error) {
	return out, c.get(ctx, inst(name)+"/layers", &out)
}

// Markers returns the markers of a layer (all if empty).
func (c *Client) Markers(ctx context.Context, name, layer string) (out []admin.Marker, err error) {
	return out, c.get(ctx, inst(name)+"/map/markers?layer="+url.QueryEscape(layer), &out)
}

// Types searches the spawnable class list.
func (c *Client) Types(ctx context.Context, name, q string, limit int) (out []string, err error) {
	return out, c.get(ctx, fmt.Sprintf("%s/types?q=%s&limit=%d", inst(name), url.QueryEscape(q), limit), &out)
}

// Audit returns the newest audit entries (needs the audit.view permission).
func (c *Client) Audit(ctx context.Context, instance string, limit int) (out []admin.AuditEntry, err error) {
	return out, c.get(ctx, fmt.Sprintf("/api/v1/audit?instance=%s&limit=%d", url.QueryEscape(instance), limit), &out)
}

// Message sends text to one player, or to everyone if steamID is empty.
func (c *Client) Message(ctx context.Context, name, steamID, text, style string) (api.ActionResult, error) {
	path := inst(name) + "/message"
	if steamID != "" {
		path = inst(name) + "/players/" + url.PathEscape(steamID) + "/message"
	}
	return c.action(ctx, http.MethodPost, path, map[string]string{"text": text, "style": style})
}

// Teleport moves a player to a position, or to another player if to is set.
func (c *Client) Teleport(ctx context.Context, name, steamID string, x, z float64, to string) (api.ActionResult, error) {
	body := map[string]any{"x": x, "z": z}
	if to != "" {
		body = map[string]any{"to_steam_id": to}
	}
	return c.action(ctx, http.MethodPost, inst(name)+"/players/"+url.PathEscape(steamID)+"/teleport", body)
}

// VehicleTypes searches the vehicle classes the map can spawn.
func (c *Client) VehicleTypes(ctx context.Context, name, q string, limit int) (out []string, err error) {
	return out, c.get(ctx, fmt.Sprintf("%s/vehicle-types?q=%s&limit=%d", inst(name), url.QueryEscape(q), limit), &out)
}

// Spawn puts an item on the ground at a position.
func (c *Client) Spawn(ctx context.Context, name, class string, quantity, health, x, z float64, attachments bool) (api.ActionResult, error) {
	body := map[string]any{"type": class, "quantity": quantity, "health": health, "x": x, "z": z, "attachments": attachments}
	return c.action(ctx, http.MethodPost, inst(name)+"/spawn", body)
}

// SpawnVehicle puts a vehicle on the ground at a position.
func (c *Client) SpawnVehicle(ctx context.Context, name, class string, x, z float64) (api.ActionResult, error) {
	return c.action(ctx, http.MethodPost, inst(name)+"/spawn-vehicle", map[string]any{"type": class, "x": x, "z": z})
}

// Give spawns an item for a player.
func (c *Client) Give(ctx context.Context, name, steamID, class string, quantity, health float64, target string, attachments bool) (api.ActionResult, error) {
	body := map[string]any{"type": class, "quantity": quantity, "health": health, "target": target, "attachments": attachments}
	return c.action(ctx, http.MethodPost, inst(name)+"/players/"+url.PathEscape(steamID)+"/give", body)
}

// Repair repairs a vehicle ("" scope = everything).
func (c *Client) Repair(ctx context.Context, name, vehicle, scope string) (api.ActionResult, error) {
	return c.action(ctx, http.MethodPost, inst(name)+"/vehicles/"+url.PathEscape(vehicle)+"/repair", map[string]string{"scope": scope})
}

// Flip stands a vehicle upright on its wheels.
func (c *Client) Flip(ctx context.Context, name, vehicle string) (api.ActionResult, error) {
	return c.action(ctx, http.MethodPost, inst(name)+"/vehicles/"+url.PathEscape(vehicle)+"/flip", nil)
}

// DeleteVehicle deletes a vehicle; force also removes it with crew inside.
func (c *Client) DeleteVehicle(ctx context.Context, name, vehicle string, force bool) (api.ActionResult, error) {
	path := inst(name) + "/vehicles/" + url.PathEscape(vehicle)
	if force {
		path += "?force=1"
	}
	return c.action(ctx, http.MethodDelete, path, nil)
}

// Stream calls fn for every server-sent event of an instance until ctx ends
// or the connection drops; it returns the reason.
func (c *Client) Stream(ctx context.Context, name string, fn func(kind string, data json.RawMessage)) error {
	r, err := c.req(ctx, http.MethodGet, inst(name)+"/stream", nil)
	if err != nil {
		return err
	}
	hc := *c.HTTP
	hc.Timeout = 0 // streams are long-lived; ctx ends them
	resp, err := hc.Do(r)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return apiErr(resp.StatusCode, b)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	var kind string
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			kind = v
		} else if v, ok := strings.CutPrefix(line, "data: "); ok {
			fn(kind, json.RawMessage(v))
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sc.Err()
}

// Tile fetches a map tile or a tile set's metadata.json (rest is
// "metadata.json" or "<hash>/<z>/<x>/<y>.jpg"). The caller closes the body of
// the response, whatever its status.
func (c *Client) Tile(ctx context.Context, name, rest string) (*http.Response, error) {
	if !api.ValidTilePath(name, rest) {
		return nil, fmt.Errorf("apiclient: bad tile path")
	}
	r, err := c.req(ctx, http.MethodGet, "/api/v1/tiles/"+name+"/"+rest, nil)
	if err != nil {
		return nil, err
	}
	return c.HTTP.Do(r)
}

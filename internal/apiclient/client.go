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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	// Session is the login session of the person a web token acts for (X-Dzo-Session). ClientAddr is
	// the browser's address and ProxyAddr the proxy it came through (X-Dzo-Client, X-Dzo-Proxy); only
	// web tokens may report them.
	Session, ClientAddr, ProxyAddr string
	HTTP                           *http.Client
}

// New returns a client with a sane timeout. Streams use their own context.
func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 40 * time.Second}}
}

// NewTLS is New with its own TLS configuration, for an API served over HTTPS
// with a private CA (web.api.ca_file) or that asks for a client certificate.
func NewTLS(base, token string, tlsConf *tls.Config) *Client {
	c := New(base, token)
	c.HTTP.Transport = &http.Transport{TLSClientConfig: tlsConf, Proxy: http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second}
	return c
}

// WithActor returns a copy that reports the given actor and source.
func (c *Client) WithActor(actor, source string) *Client {
	d := *c
	d.Actor, d.Source = actor, source
	return &d
}

// ForUser returns a copy that acts for the person of a login session, reporting the browser's address.
// It replaces WithActor for the web interface; WithActor stays for other delegate tools.
func (c *Client) ForUser(session, clientAddr string) *Client {
	d := *c
	d.Session, d.ClientAddr = session, clientAddr
	return &d
}

// ForProxy returns a copy that also reports the reverse proxy the browser came through.
func (c *Client) ForProxy(addr string) *Client {
	d := *c
	d.ProxyAddr = addr
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
	for k, v := range map[string]string{"X-Dzo-Session": c.Session, "X-Dzo-Client": c.ClientAddr, "X-Dzo-Proxy": c.ProxyAddr} {
		if v != "" {
			r.Header.Set(k, v)
		}
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

// Feed returns the game events with an id above after, oldest first: at most limit, the newest ones when
// after is 0. A limit of 0 asks for everything the hub keeps.
func (c *Client) Feed(ctx context.Context, name string, after int64, limit int) (out []admin.FeedEvent, err error) {
	return out, c.get(ctx, fmt.Sprintf("%s/feed?after=%d&limit=%d", inst(name), after, limit), &out)
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
func (c *Client) Message(ctx context.Context, name, steamID string, m api.MessageBody) (api.ActionResult, error) {
	path := inst(name) + "/message"
	if steamID != "" {
		path = inst(name) + "/players/" + url.PathEscape(steamID) + "/message"
	}
	return c.action(ctx, http.MethodPost, path, m)
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

// MoveVehicle puts a vehicle, with its crew, at a position on the ground.
func (c *Client) MoveVehicle(ctx context.Context, name, vehicle string, x, z float64) (api.ActionResult, error) {
	return c.action(ctx, http.MethodPost, inst(name)+"/vehicles/"+url.PathEscape(vehicle)+"/move", map[string]float64{"x": x, "z": z})
}

// RaiseFlag raises a territory flag and fills its refresher time.
func (c *Client) RaiseFlag(ctx context.Context, name, flag string) (api.ActionResult, error) {
	return c.action(ctx, http.MethodPost, inst(name)+"/flags/"+url.PathEscape(flag)+"/raise", nil)
}

// DeleteVehicle deletes a vehicle; force also removes it with crew inside.
func (c *Client) DeleteVehicle(ctx context.Context, name, vehicle string, force bool) (api.ActionResult, error) {
	path := inst(name) + "/vehicles/" + url.PathEscape(vehicle)
	if force {
		path += "?force=1"
	}
	return c.action(ctx, http.MethodDelete, path, nil)
}

// UnusedVehicles lists the vehicles nobody has driven for days days (the preview of a cleanup).
func (c *Client) UnusedVehicles(ctx context.Context, name string, days int) (out api.UnusedVehicles, err error) {
	return out, c.get(ctx, inst(name)+"/vehicles/unused?days="+strconv.Itoa(days), &out)
}

// CleanupVehicles deletes the vehicles of a confirmed preview that are still unused; the result says
// which were queued for deletion and which were skipped, and why.
func (c *Client) CleanupVehicles(ctx context.Context, name string, days int, ids []string) (api.CleanupResult, error) {
	status, b, err := c.do(ctx, http.MethodPost, inst(name)+"/vehicles/cleanup", api.CleanupRequest{Days: days, IDs: ids})
	if err != nil {
		return api.CleanupResult{}, err
	}
	if status != http.StatusOK {
		return api.CleanupResult{}, apiErr(status, b)
	}
	var out api.CleanupResult
	return out, json.Unmarshal(b, &out)
}

// Stream calls fn for every server-sent event of an instance until ctx ends
// or the connection drops; it returns the reason.
func (c *Client) Stream(ctx context.Context, name string, fn func(kind string, data json.RawMessage)) error {
	return c.StreamOnly(ctx, name, "", fn)
}

// StreamOnly is Stream limited to one kind of event ("feed"): no snapshot of the rest. An empty only is
// the whole stream.
func (c *Client) StreamOnly(ctx context.Context, name, only string, fn func(kind string, data json.RawMessage)) error {
	path := inst(name) + "/stream"
	if only != "" {
		path += "?only=" + url.QueryEscape(only)
	}
	r, err := c.req(ctx, http.MethodGet, path, nil)
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

// Kick removes an online player; the reason is shown to them (at most 64 bytes).
func (c *Client) Kick(ctx context.Context, name, steamID, reason string) (api.ActionResult, error) {
	return c.action(ctx, http.MethodPost, inst(name)+"/players/"+url.PathEscape(steamID)+"/kick", api.KickBody{Reason: reason})
}

func (c *Client) playerAction(ctx context.Context, name, steamID, what string) (api.ActionResult, error) {
	return c.action(ctx, http.MethodPost, inst(name)+"/players/"+url.PathEscape(steamID)+"/"+what, nil)
}

// Heal restores a player's health, blood, shock and bones and cures them.
func (c *Client) Heal(ctx context.Context, name, steamID string) (api.ActionResult, error) {
	return c.playerAction(ctx, name, steamID, "heal")
}

// FixGear repairs everything a player carries and wears.
func (c *Client) FixGear(ctx context.Context, name, steamID string) (api.ActionResult, error) {
	return c.playerAction(ctx, name, steamID, "fixgear")
}

// Kill kills a player.
func (c *Client) Kill(ctx context.Context, name, steamID string) (api.ActionResult, error) {
	return c.playerAction(ctx, name, steamID, "kill")
}

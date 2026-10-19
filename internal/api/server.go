// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/geoip"
)

// APIVersion is the version of the /api/v1 contract (additive changes keep
// it; a breaking change is a new prefix).
const APIVersion = 1

const (
	maxBody         = 64 << 10
	defaultWait     = 10 * time.Second
	maxWait         = 25 * time.Second
	actionsPerMin   = 120
	perTargetPerMin = 30
)

// Server serves /api/v1 for one installation.
type Server struct {
	// Installation names this dzo installation in listings, so a client
	// holding several can tell them apart. Version is the dzo build.
	Installation string
	Version      string

	Hubs   *admin.Registry
	Tokens *TokenStore
	Audit  *admin.Audit
	// TilesDir holds the map tile sets (see internal/maptiles); empty serves none.
	TilesDir string
	// Moderation serves the ban and evidence routes; nil answers them 503.
	Moderation Moderation
	// GeoIP locates the players' addresses (flags and the connection info); nil shows none.
	GeoIP *geoip.DB
	// Say sends text over the instance's RCon (style rcon of a message): to everyone when steamID
	// is empty. nil answers the rcon style 503.
	Say func(ctx context.Context, instance, steamID, text string) error

	now     func() time.Time
	actions *admin.Limiter
	targets *admin.Limiter
}

// New returns a Server over the given hubs, tokens and audit log.
func New(installation, version string, hubs *admin.Registry, tokens *TokenStore, audit *admin.Audit) *Server {
	return &Server{
		Installation: installation, Version: version, Hubs: hubs, Tokens: tokens, Audit: audit,
		now: time.Now, actions: admin.NewLimiter(actionsPerMin, time.Minute), targets: admin.NewLimiter(perTargetPerMin, time.Minute),
	}
}

// Info is GET /api/v1.
type Info struct {
	Installation string `json:"installation"`
	Version      string `json:"version"`
	APIVersion   int    `json:"api_version"`
	Principal    string `json:"principal"`
	Role         Role   `json:"role"`
}

// ActionResult is the answer to every action. ID is the command id; when the
// caller did not wait (wait=0) OK is false and Pending true.
type ActionResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Pending bool   `json:"pending,omitempty"`
	Message string `json:"message,omitempty"`
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, format string, a ...any) {
	writeJSON(w, code, apiError{Error: fmt.Sprintf(format, a...)})
}

// handler is a route handler with the caller and instance resolved.
type handler func(w http.ResponseWriter, r *http.Request, c *call)

type call struct {
	who   Principal
	actor string // who is blamed in the audit log
	src   string
	hub   *admin.Hub // nil for non-instance routes
}

// Handler returns the http.Handler of /api/v1 and /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprintln(w, "ok") })

	route := func(pattern, perm string, instance bool, h handler) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			c, ok := s.authorize(w, r, perm, instance)
			if ok {
				h(w, r, c)
			}
		})
	}
	const base = "/api/v1"
	route("GET "+base, PermView, false, s.info)
	route("GET "+base+"/instances", PermView, false, s.instances)
	route("GET "+base+"/audit", PermAuditView, false, s.audit)
	route("GET "+base+"/tiles/{map}/{rest...}", PermView, false, s.tile)
	route("GET "+base+"/instances/{instance}", PermView, true, func(w http.ResponseWriter, _ *http.Request, c *call) { writeJSON(w, 200, c.hub.Status()) })
	route("GET "+base+"/instances/{instance}/players", PermView, true, s.players)
	route("GET "+base+"/instances/{instance}/vehicles", PermView, true, func(w http.ResponseWriter, _ *http.Request, c *call) { writeJSON(w, 200, c.hub.Vehicles()) })
	route("GET "+base+"/instances/{instance}/events", PermView, true, func(w http.ResponseWriter, _ *http.Request, c *call) { writeJSON(w, 200, c.hub.Events()) })
	route("GET "+base+"/instances/{instance}/layers", PermView, true, func(w http.ResponseWriter, _ *http.Request, c *call) {
		writeJSON(w, 200, filterLayers(c.hub.Layers(), c.who.Token.Role))
	})
	route("GET "+base+"/instances/{instance}/map/markers", PermView, true, func(w http.ResponseWriter, r *http.Request, c *call) {
		writeJSON(w, 200, filterMarkers(c.hub.Markers(r.URL.Query().Get("layer")), c.who.Token.Role))
	})
	route("GET "+base+"/instances/{instance}/types", PermView, true, s.types)
	route("GET "+base+"/instances/{instance}/vehicle-types", PermView, true, s.vehicleTypes)
	route("GET "+base+"/instances/{instance}/stream", PermView, true, s.stream)
	route("GET "+base+"/instances/{instance}/commands/{id}", PermView, true, s.command)

	route("POST "+base+"/instances/{instance}/message", PermBroadcast, true, s.message)
	route("POST "+base+"/instances/{instance}/players/{steamid}/message", PermMessage, true, s.message)
	route("GET "+base+"/instances/{instance}/players/{steamid}/geo", PermView, true, s.geo)
	route("GET "+base+"/instances/{instance}/players/{steamid}/connection", PermPlayerIP, true, s.connection)
	route("POST "+base+"/instances/{instance}/players/{steamid}/kick", PermKick, true, s.kick)
	route("POST "+base+"/instances/{instance}/players/{steamid}/kill", PermKill, true, s.playerAction(admin.KindPlayerKill, "player.kill"))
	route("POST "+base+"/instances/{instance}/players/{steamid}/heal", PermHeal, true, s.playerAction(admin.KindPlayerHeal, "player.heal"))
	route("POST "+base+"/instances/{instance}/players/{steamid}/fixgear", PermFixGear, true, s.playerAction(admin.KindPlayerFixGear, "player.fixgear"))
	route("POST "+base+"/instances/{instance}/players/{steamid}/teleport", PermTeleport, true, s.teleport)
	route("POST "+base+"/instances/{instance}/players/{steamid}/give", PermSpawn, true, s.give)
	route("POST "+base+"/instances/{instance}/spawn", PermSpawn, true, s.spawn)
	route("POST "+base+"/instances/{instance}/spawn-vehicle", PermSpawn, true, s.spawnVehicle)
	route("POST "+base+"/instances/{instance}/vehicles/{id}/repair", PermRepair, true, s.repair)
	route("POST "+base+"/instances/{instance}/vehicles/{id}/flip", PermRepair, true, s.flip)
	route("DELETE "+base+"/instances/{instance}/vehicles/{id}", PermDelete, true, s.deleteVehicle)
	route("GET "+base+"/bans", PermBanView, false, s.listBans)
	route("POST "+base+"/bans", PermBanInstance, false, s.createBan)
	route("GET "+base+"/bans/{id}", PermBanView, false, s.getBan)
	route("PUT "+base+"/bans/{id}", PermBanInstance, false, s.updateBan)
	route("POST "+base+"/bans/{id}/revoke", PermBanInstance, false, s.revokeBan)
	route("GET "+base+"/bans/{id}/history", PermBanView, false, s.banHistory)
	route("GET "+base+"/evidence", PermBanView, false, s.listEvidence)
	route("POST "+base+"/evidence", PermEvidenceAdd, false, s.addEvidence)
	route("GET "+base+"/evidence/{id}", PermBanView, false, s.getEvidence)
	route("GET "+base+"/evidence/{id}/file", PermBanView, false, s.evidenceFile)
	route("GET "+base+"/players/{steamid}/identities", PermBanView, false, s.identities)
	return mux
}

// authorize authenticates the request, checks the permission and instance
// access, and resolves the actor (§C12: API tokens are scoped, delegate tokens
// name the human).
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, perm string, instance bool) (*call, bool) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	who, found := s.Tokens.Lookup(secret)
	if !ok || !found {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dzo"`)
		fail(w, http.StatusUnauthorized, "missing or invalid token")
		return nil, false
	}
	if !who.Token.Role.Can(perm) {
		fail(w, http.StatusForbidden, "role %s lacks %s", who.Token.Role, perm)
		return nil, false
	}
	c := &call{who: who, actor: who.Token.Name, src: "api"}
	if who.Token.Delegate {
		if a := cleanHeader(r.Header.Get("X-Dzo-Actor")); a != "" {
			c.actor = who.Token.Name + ":" + a
		}
		if src := cleanHeader(r.Header.Get("X-Dzo-Source")); src != "" {
			c.src = src
		}
	}
	if instance {
		name := r.PathValue("instance")
		hub, ok := s.Hubs.Hub(name)
		if !ok || !who.Allowed(name) {
			fail(w, http.StatusNotFound, "unknown instance %q", name)
			return nil, false
		}
		c.hub = hub
	}
	return c, true
}

func cleanHeader(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if len(v) > 64 {
		v = v[:64]
	}
	return strings.TrimSpace(v)
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request, c *call) {
	writeJSON(w, 200, Info{Installation: s.Installation, Version: s.Version, APIVersion: APIVersion, Principal: c.who.Token.Name, Role: c.who.Token.Role})
}

func (s *Server) instances(w http.ResponseWriter, _ *http.Request, c *call) {
	out := []admin.Status{}
	for _, n := range s.Hubs.Names() {
		if hub, ok := s.Hubs.Hub(n); ok && c.who.Allowed(n) {
			out = append(out, hub.Status())
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request, _ *call) {
	entries, err := s.Audit.Tail(r.URL.Query().Get("instance"), queryInt(r, "limit", 100, 1000))
	if err != nil {
		fail(w, http.StatusInternalServerError, "reading the audit log: %v", err)
		return
	}
	writeJSON(w, 200, entries)
}

func (s *Server) types(w http.ResponseWriter, r *http.Request, c *call) {
	writeJSON(w, 200, c.hub.Types(r.URL.Query().Get("q"), queryInt(r, "limit", 50, 500)))
}

func (s *Server) vehicleTypes(w http.ResponseWriter, r *http.Request, c *call) {
	writeJSON(w, 200, c.hub.VehicleTypes(r.URL.Query().Get("q"), queryInt(r, "limit", 50, 500)))
}

func queryInt(r *http.Request, key string, def, max int) int {
	var n int
	if _, err := fmt.Sscanf(r.URL.Query().Get(key), "%d", &n); err != nil || n < 1 {
		return def
	}
	return min(n, max)
}

// visible reports whether a marker is shown to a role. Without a visibility
// a marker is admin-only (§C16).
func visible(vis string, role Role) bool {
	need := Role(vis)
	if !need.Valid() {
		need = RoleAdmin
	}
	return role.Rank() >= need.Rank()
}

func filterMarkers(in []admin.Marker, role Role) []admin.Marker {
	out := []admin.Marker{}
	for _, m := range in {
		if visible(m.Visibility, role) {
			out = append(out, m)
		}
	}
	return out
}

func filterLayers(in []admin.Layer, role Role) []admin.Layer {
	out := []admin.Layer{}
	for _, l := range in {
		if visible(l.Visibility, role) {
			out = append(out, l)
		}
	}
	return out
}

// stream is the SSE endpoint: the current state first, then every change.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, c *call) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := c.hub.Subscribe(r.Context())
	send := func(kind string, data any) bool {
		b, err := json.Marshal(data)
		if err != nil {
			return true
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
		fl.Flush()
		return err == nil
	}
	send("status", c.hub.Status())
	send("players", c.hub.Players())
	send("vehicles", c.hub.Vehicles())
	send("markers", filterMarkers(c.hub.Markers(""), c.who.Token.Role))
	send("events", c.hub.Events())
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			data := ev.Data
			if ev.Kind == "markers" {
				// The mod's markers plus the file drop, per the role's visibility.
				data = filterMarkers(c.hub.Markers(""), c.who.Token.Role)
			}
			if !send(ev.Kind, data) {
				return
			}
		}
	}
}

func (s *Server) command(w http.ResponseWriter, r *http.Request, c *call) {
	res, done := c.hub.Result(r.PathValue("id"))
	if !done {
		writeJSON(w, http.StatusAccepted, ActionResult{ID: res.ID, Pending: true})
		return
	}
	writeJSON(w, 200, ActionResult{ID: res.ID, OK: bool(res.OK), Message: res.Message})
}

// run submits a command, audits it and writes the answer. wait=0 queues it
// and returns 202 with the command id.
func (s *Server) run(w http.ResponseWriter, r *http.Request, c *call, cmd admin.Command, action, target string) {
	entry := admin.AuditEntry{Actor: c.actor, Source: c.src, Instance: c.hub.Name, Action: action, Target: target, Detail: detail(cmd)}
	deny := func(code int, err error) {
		entry.Result = err.Error()
		_ = s.Audit.Log(entry)
		fail(w, code, "%v", err)
	}
	if !s.actions.Allow(c.who.Token.ID) || !s.targets.Allow(c.hub.Name+"/"+target) {
		deny(http.StatusTooManyRequests, errors.New("rate limit reached, try again in a minute"))
		return
	}
	wait := defaultWait
	if v := r.URL.Query().Get("wait"); v != "" {
		var secs int
		if _, err := fmt.Sscanf(v, "%d", &secs); err != nil || secs < 0 {
			deny(http.StatusBadRequest, errors.New("wait must be a number of seconds"))
			return
		}
		wait = min(time.Duration(secs)*time.Second, maxWait)
	}
	if wait == 0 {
		id, err := c.hub.Enqueue(cmd)
		if err != nil {
			deny(statusFor(err), err)
			return
		}
		entry.OK, entry.Result = true, "queued "+id
		_ = s.Audit.Log(entry)
		writeJSON(w, http.StatusAccepted, ActionResult{ID: id, Pending: true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), wait)
	defer cancel()
	res, err := c.hub.Submit(ctx, cmd)
	if err != nil {
		deny(statusFor(err), err)
		return
	}
	entry.OK, entry.Result = bool(res.OK), res.Message
	_ = s.Audit.Log(entry)
	code := http.StatusOK
	if !res.OK {
		code = http.StatusUnprocessableEntity
	}
	writeJSON(w, code, ActionResult{ID: res.ID, OK: bool(res.OK), Message: res.Message})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, admin.ErrOffline):
		return http.StatusServiceUnavailable
	case errors.Is(err, admin.ErrTimeout):
		return http.StatusGatewayTimeout
	}
	return http.StatusBadRequest
}

// detail is a short audit description of a command's parameters.
func detail(c admin.Command) string {
	switch c.Kind {
	case admin.KindMessage:
		return fmt.Sprintf("%s: %s", c.Style, c.Text)
	case admin.KindTeleport:
		if c.ToSteamID != "" {
			return "to player " + c.ToSteamID
		}
		return fmt.Sprintf("to %.0f/%.0f", c.X, c.Z)
	case admin.KindSpawnVehicle:
		return fmt.Sprintf("%s at %.0f/%.0f", c.Type, c.X, c.Z)
	case admin.KindSpawnItem:
		if c.SteamID == "" {
			return fmt.Sprintf("%s x%g at %.0f/%.0f", c.Type, c.Quantity, c.X, c.Z)
		}
		if c.Attachments {
			return fmt.Sprintf("%s x%g into %s, with attachments", c.Type, c.Quantity, c.Target)
		}
		return fmt.Sprintf("%s x%g into %s", c.Type, c.Quantity, c.Target)
	case admin.KindVehicleRepair:
		return "scope " + c.Scope
	case admin.KindVehicleDelete:
		return fmt.Sprintf("force=%t", c.Force)
	}
	return ""
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad request body: %v", err)
		return false
	}
	return true
}

// MessageBody is the body of the message routes.
type MessageBody struct {
	Text       string `json:"text"`
	Style      string `json:"style"`
	NotifyType string `json:"notify_type,omitempty"`
	Title      string `json:"title,omitempty"`
	Seconds    int    `json:"seconds,omitempty"`
}

func (s *Server) message(w http.ResponseWriter, r *http.Request, c *call) {
	var b MessageBody
	if !decode(w, r, &b) {
		return
	}
	id := r.PathValue("steamid") // empty on the broadcast route
	target := id
	if id == "" {
		target = "all"
	}
	cmd := admin.Command{Kind: admin.KindMessage, SteamID: id, Text: b.Text, Style: b.Style, NotifyType: b.NotifyType, Title: b.Title, Seconds: b.Seconds}
	if b.Style == "rcon" {
		s.say(w, r, c, cmd, target)
		return
	}
	s.run(w, r, c, cmd, "message", target)
}

// say is the rcon way of a message: no mod, no queue, the answer is the RCon command's.
func (s *Server) say(w http.ResponseWriter, r *http.Request, c *call, cmd admin.Command, target string) {
	entry := admin.AuditEntry{Actor: c.actor, Source: c.src, Instance: c.hub.Name, Action: "message", Target: target, Detail: detail(cmd)}
	deny := func(code int, err error) {
		entry.Result = err.Error()
		_ = s.Audit.Log(entry)
		fail(w, code, "%v", err)
	}
	switch {
	case s.Say == nil:
		deny(http.StatusServiceUnavailable, errors.New("rcon is not available"))
	case cmd.Text == "" || len(cmd.Text) > 400:
		deny(http.StatusBadRequest, errors.New("message needs text of at most 400 bytes"))
	case !s.actions.Allow(c.who.Token.ID) || !s.targets.Allow(c.hub.Name+"/"+target):
		deny(http.StatusTooManyRequests, errors.New("rate limit reached, try again in a minute"))
	default:
		ctx, cancel := context.WithTimeout(r.Context(), maxWait)
		defer cancel()
		if err := s.Say(ctx, c.hub.Name, cmd.SteamID, cmd.Text); err != nil {
			deny(http.StatusBadGateway, err)
			return
		}
		entry.OK = true
		_ = s.Audit.Log(entry)
		writeJSON(w, http.StatusOK, ActionResult{OK: true})
	}
}

// playerAction runs a command that needs only the player: kill, heal and fix gear.
func (s *Server) playerAction(kind, action string) handler {
	return func(w http.ResponseWriter, r *http.Request, c *call) {
		id := r.PathValue("steamid")
		s.run(w, r, c, admin.Command{Kind: kind, SteamID: id}, action, id)
	}
}

type teleportBody struct {
	X         float64  `json:"x"`
	Z         float64  `json:"z"`
	Y         *float64 `json:"y"`
	ToSteamID string   `json:"to_steam_id"`
}

func (s *Server) teleport(w http.ResponseWriter, r *http.Request, c *call) {
	var b teleportBody
	if !decode(w, r, &b) {
		return
	}
	cmd := admin.Command{Kind: admin.KindTeleport, SteamID: r.PathValue("steamid"), ToSteamID: b.ToSteamID, X: b.X, Z: b.Z}
	if b.Y != nil {
		cmd.Y, cmd.HasY = *b.Y, true
	}
	s.run(w, r, c, cmd, "teleport", cmd.SteamID)
}

// quantityOf is the quantity of a spawn body: absent means the game's default.
func quantityOf(q *float64) float64 {
	if q == nil {
		return admin.QuantityDefault
	}
	return *q
}

type giveBody struct {
	Type     string   `json:"type"`
	Quantity *float64 `json:"quantity"` // absent = admin.QuantityDefault
	Health   float64  `json:"health"`
	Target   string   `json:"target"`
	// Attachments: with the kit the item comes with when the game debug-spawns it.
	Attachments bool `json:"attachments"`
}

func (s *Server) give(w http.ResponseWriter, r *http.Request, c *call) {
	var b giveBody
	if !decode(w, r, &b) {
		return
	}
	cmd := admin.Command{Kind: admin.KindSpawnItem, SteamID: r.PathValue("steamid"), Type: b.Type, Quantity: quantityOf(b.Quantity), Health: b.Health, Target: b.Target, Attachments: b.Attachments}
	s.run(w, r, c, cmd, "spawn_item", cmd.SteamID)
}

type spawnBody struct {
	Type        string   `json:"type"`
	Quantity    *float64 `json:"quantity"` // absent = admin.QuantityDefault
	Health      float64  `json:"health"`
	X           float64  `json:"x"`
	Z           float64  `json:"z"`
	Attachments bool     `json:"attachments"`
}

// spawn puts an item on the ground at a position, the same as give with target ground does at a player.
func (s *Server) spawn(w http.ResponseWriter, r *http.Request, c *call) {
	var b spawnBody
	if !decode(w, r, &b) {
		return
	}
	cmd := admin.Command{Kind: admin.KindSpawnItem, Type: b.Type, Quantity: quantityOf(b.Quantity), Health: b.Health, X: b.X, Z: b.Z, Attachments: b.Attachments}
	s.run(w, r, c, cmd, "spawn_item", fmt.Sprintf("%.0f/%.0f", b.X, b.Z))
}

type spawnVehicleBody struct {
	Type string  `json:"type"`
	X    float64 `json:"x"`
	Z    float64 `json:"z"`
}

func (s *Server) spawnVehicle(w http.ResponseWriter, r *http.Request, c *call) {
	var b spawnVehicleBody
	if !decode(w, r, &b) {
		return
	}
	cmd := admin.Command{Kind: admin.KindSpawnVehicle, Type: b.Type, X: b.X, Z: b.Z}
	s.run(w, r, c, cmd, "spawn_vehicle", fmt.Sprintf("%.0f/%.0f", b.X, b.Z))
}

type repairBody struct {
	Scope string `json:"scope"`
}

func (s *Server) repair(w http.ResponseWriter, r *http.Request, c *call) {
	var b repairBody
	if r.ContentLength != 0 && !decode(w, r, &b) {
		return
	}
	cmd := admin.Command{Kind: admin.KindVehicleRepair, Vehicle: r.PathValue("id"), Scope: b.Scope}
	s.run(w, r, c, cmd, "vehicle_repair", cmd.Vehicle)
}

func (s *Server) flip(w http.ResponseWriter, r *http.Request, c *call) {
	cmd := admin.Command{Kind: admin.KindVehicleFlip, Vehicle: r.PathValue("id")}
	s.run(w, r, c, cmd, "vehicle_flip", cmd.Vehicle)
}

func (s *Server) deleteVehicle(w http.ResponseWriter, r *http.Request, c *call) {
	cmd := admin.Command{Kind: admin.KindVehicleDelete, Vehicle: r.PathValue("id"), Force: r.URL.Query().Get("force") == "1"}
	s.run(w, r, c, cmd, "vehicle_delete", cmd.Vehicle)
}

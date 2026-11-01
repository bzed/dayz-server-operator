// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/geoip"
	"github.com/bzed/dayz-server-operator/internal/moderation"
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
	// Vehicles holds the last driver of each vehicle (the vehicle records); nil lists the vehicles as the mod reports them.
	Vehicles VehicleRecords
	// Lookups asks BattlEye and Steam about one player on request (the Check now button); nil answers 503.
	Lookups Lookups
	// GeoIP locates the players' addresses (flags and the connection info); nil shows none.
	GeoIP *geoip.DB
	// Accounts resolves web sessions to people; nil turns user logins off (web tokens get 503).
	Accounts Accounts
	// Login holds the session lifetimes (serve.login.*); zero means the defaults.
	Login LoginSettings
	// Say sends text over the instance's RCon (style rcon of a message): to everyone when steamID
	// is empty. nil answers the rcon style 503.
	Say func(ctx context.Context, instance, steamID, text string) error
	// Recheck is how often an open stream checks that the person behind it is still allowed (default 60 s).
	Recheck time.Duration
	// Logf receives the login log lines (one per step, never a secret); nil logs nothing.
	Logf func(format string, args ...any)

	now     func() time.Time
	login   *auth.Backoff
	actions *admin.Limiter
	targets *admin.Limiter
}

// Accounts is what the API needs of the people and their sessions (moderation.Store).
type Accounts interface {
	Session(ctx context.Context, hash string, now time.Time, idle, max time.Duration) (moderation.LoginSession, error)
	CreateSession(ctx context.Context, hash, steamID, client string, now time.Time) error
	DeleteSession(ctx context.Context, hash string) error
	DeleteSessions(ctx context.Context, steamID, keepHash string) error
	SetPassword(ctx context.Context, steamID, username, hash string) error
	ClearPassword(ctx context.Context, steamID string) error
	SetTOTP(ctx context.Context, steamID string, secret []byte) error
	ClearTOTP(ctx context.Context, steamID string) error
	AddPasskey(ctx context.Context, steamID string, p moderation.Passkey) error
	UpdatePasskey(ctx context.Context, steamID string, id, data []byte, used time.Time) error
	DeletePasskey(ctx context.Context, steamID string, id []byte) error
	SetRecoveryCodes(ctx context.Context, steamID string, hashes []string) error
	UpdatePending(ctx context.Context, hash string, p moderation.PendingLogin) error
	People(ctx context.Context) ([]moderation.Person, error)
	PutPerson(ctx context.Context, p moderation.Person, now time.Time) error
	DeletePerson(ctx context.Context, steamID string) error
	ResetCredentials(ctx context.Context, steamID string) error
	Person(ctx context.Context, steamID string) (moderation.Person, error)
	PersonByUsername(ctx context.Context, username string) (moderation.Person, error)
	Credentials(ctx context.Context, steamID string) (moderation.Credentials, error)
	UseTOTPStep(ctx context.Context, steamID string, step int64) (bool, error)
	UseRecoveryCode(ctx context.Context, steamID, hash string) (bool, error)
	CreatePending(ctx context.Context, hash string, p moderation.PendingLogin) error
	Pending(ctx context.Context, hash string, now time.Time) (moderation.PendingLogin, error)
	DeletePending(ctx context.Context, hash string) error
	UseSteamNonce(ctx context.Context, nonce string, now time.Time) (bool, error)
}

// LoginSettings are the settings of user logins.
type LoginSettings struct {
	// WebURL is serve.web_url; logins are off without it.
	WebURL                  string
	SessionIdle, SessionMax time.Duration
	// Steam verifies "Sign in through Steam"; the zero value talks to the real one.
	Steam auth.Steam
}

func (l LoginSettings) idle() time.Duration {
	if l.SessionIdle <= 0 {
		return 12 * time.Hour
	}
	return l.SessionIdle
}

func (l LoginSettings) max() time.Duration {
	if l.SessionMax <= 0 {
		return 7 * 24 * time.Hour
	}
	return l.SessionMax
}

// New returns a Server over the given hubs, tokens and audit log.
func New(installation, version string, hubs *admin.Registry, tokens *TokenStore, audit *admin.Audit) *Server {
	s := &Server{
		Installation: installation, Version: version, Hubs: hubs, Tokens: tokens, Audit: audit,
		now: time.Now, actions: admin.NewLimiter(actionsPerMin, time.Minute), targets: admin.NewLimiter(perTargetPerMin, time.Minute),
	}
	s.login = auth.NewBackoff(func() time.Time { return s.now() }) // s.now is replaced by tests
	return s
}

// Info is GET /api/v1.
type Info struct {
	Installation string `json:"installation"`
	Version      string `json:"version"`
	APIVersion   int    `json:"api_version"`
	Principal    string `json:"principal"`
	// Role is the token's role, or for a person the highest role they have anywhere.
	Role Role `json:"role"`
	// User is set when the request acts for a logged-in person: what they may do where.
	User *UserInfo `json:"user,omitempty"`
}

// UserInfo is the person behind a request and what they may do: their effective role per instance (the
// instances they have no role on are left out) and the installation-wide permissions they hold.
type UserInfo struct {
	SteamID      string          `json:"steam_id"`
	Name         string          `json:"name"`
	Username     string          `json:"username,omitempty"`
	Roles        map[string]Role `json:"roles"`
	Installation []string        `json:"installation"`
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

// VehicleRecords reads dzo's record of each vehicle (the last driver and first sighting).
type VehicleRecords interface {
	VehicleRecords(ctx context.Context, instance string) (map[string]moderation.VehicleRecord, error)
}

// vehiclesWithRecords is the hub's vehicle list with the last driver, last drive and first sighting of each
// live vehicle filled in from the records. Without a database it is the hub's list unchanged.
func (s *Server) vehiclesWithRecords(ctx context.Context, hub *admin.Hub) ([]admin.Vehicle, error) {
	list := hub.Vehicles()
	if s.Vehicles == nil {
		return list, nil
	}
	recs, err := s.Vehicles.VehicleRecords(ctx, hub.Name)
	if err != nil {
		return nil, fmt.Errorf("vehicle records: %w", err)
	}
	for i := range list {
		rec, ok := recs[list[i].ID]
		if !ok {
			continue
		}
		if !rec.FirstSeen.IsZero() {
			t := rec.FirstSeen
			list[i].FirstSeen = &t
		}
		if !rec.DrivenAt.IsZero() {
			t := rec.DrivenAt
			list[i].LastDriven = &t
			list[i].LastDriver, list[i].LastDriverName = rec.Driver, rec.DriverName
		}
	}
	return list, nil
}

func (s *Server) vehicles(w http.ResponseWriter, r *http.Request, c *call) {
	list, err := s.vehiclesWithRecords(r.Context(), c.hub)
	if err != nil {
		fail(w, http.StatusInternalServerError, "the vehicle records could not be read")
		return
	}
	writeJSON(w, 200, list)
}

// handler is a route handler with the caller and instance resolved.
type handler func(w http.ResponseWriter, r *http.Request, c *call)

type call struct {
	who   Principal
	actor string // who is blamed in the audit log
	src   string
	// client is the browser's address behind a web action (X-Dzo-Client, web tokens only); session is the
	// hash of the login session it acts in.
	client, session string
	hub             *admin.Hub // nil for non-instance routes
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
	s.loginRoutes(mux, base)
	route("POST "+base+"/auth/logout", PermView, false, s.logout)
	route("GET "+base+"/users", PermUsersManage, false, s.listUsers)
	route("PUT "+base+"/users/{steamid}", PermUsersManage, false, s.putUser)
	route("DELETE "+base+"/users/{steamid}", PermUsersManage, false, s.deleteUser)
	route("POST "+base+"/users/{steamid}/reset", PermUsersManage, false, s.resetUser)
	s.accountRoutes(route, base)
	route("GET "+base+"/audit", PermAuditView, false, s.audit)
	route("GET "+base+"/tiles/{map}/{rest...}", PermView, false, s.tile)
	route("GET "+base+"/instances/{instance}", PermView, true, func(w http.ResponseWriter, _ *http.Request, c *call) { writeJSON(w, 200, c.hub.Status()) })
	route("GET "+base+"/instances/{instance}/players", PermView, true, s.players)
	route("GET "+base+"/instances/{instance}/vehicles", PermView, true, s.vehicles)
	route("GET "+base+"/instances/{instance}/events", PermView, true, func(w http.ResponseWriter, _ *http.Request, c *call) { writeJSON(w, 200, c.hub.Events()) })
	route("GET "+base+"/instances/{instance}/layers", PermView, true, func(w http.ResponseWriter, _ *http.Request, c *call) {
		writeJSON(w, 200, filterLayers(c.hub.Layers(), c.role()))
	})
	route("GET "+base+"/instances/{instance}/map/markers", PermView, true, func(w http.ResponseWriter, r *http.Request, c *call) {
		writeJSON(w, 200, filterMarkers(c.hub.Markers(r.URL.Query().Get("layer")), c.role()))
	})
	route("GET "+base+"/instances/{instance}/types", PermView, true, s.types)
	route("GET "+base+"/instances/{instance}/vehicle-types", PermView, true, s.vehicleTypes)
	route("GET "+base+"/instances/{instance}/stream", PermView, true, s.stream)
	route("GET "+base+"/instances/{instance}/feed", PermView, true, s.feed)
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
	route("POST "+base+"/instances/{instance}/vehicles/{id}/move", PermTeleport, true, s.moveVehicle)
	route("POST "+base+"/instances/{instance}/flags/{id}/raise", PermRaise, true, s.raiseFlag)
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
	route("GET "+base+"/players", PermPlayerHistory, false, s.listPlayers)
	route("GET "+base+"/players/{steamid}", PermPlayerHistory, false, s.getPlayer)
	route("POST "+base+"/players/{steamid}/lookup", PermPlayerHistory, false, s.lookupPlayer)
	return mux
}

// authorize authenticates the request, checks the permission and instance
// access, and resolves the actor (§C12: API tokens are scoped, delegate tokens
// name the human, web tokens act for the person of a login session).
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, perm string, instance bool) (*call, bool) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	who, found := s.Tokens.Lookup(secret)
	if !ok || !found {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dzo"`)
		fail(w, http.StatusUnauthorized, "missing or invalid token")
		return nil, false
	}
	c := &call{who: who, actor: who.Token.Name, src: "api"}
	switch {
	case who.Token.Web:
		if !s.resolveSession(w, r, c) {
			return nil, false
		}
		if src := cleanHeader(r.Header.Get("X-Dzo-Source")); src != "" {
			c.src = src
		}
		c.client = clientHeader(r)
	case who.Token.Delegate:
		if a := cleanHeader(r.Header.Get("X-Dzo-Actor")); a != "" {
			c.actor = who.Token.Name + ":" + a
		}
		if src := cleanHeader(r.Header.Get("X-Dzo-Source")); src != "" {
			c.src = src
		}
	}
	if instance {
		name := r.PathValue("instance")
		hub, found := s.Hubs.Hub(name)
		if !found || !c.who.Allowed(name) {
			if !c.who.CanAny(perm) {
				forbidden(w, c.who.TopRole(), perm)
				return nil, false
			}
			fail(w, http.StatusNotFound, "unknown instance %q", name)
			return nil, false
		}
		if !c.who.Can(name, perm) {
			forbidden(w, c.who.RoleOn(name), perm)
			return nil, false
		}
		c.hub = hub
		return c, true
	}
	allowed := c.who.CanAny(perm)
	if installationWide(perm) {
		allowed = c.who.CanAll(perm)
	}
	if !allowed {
		forbidden(w, c.who.TopRole(), perm)
		return nil, false
	}
	return c, true
}

func forbidden(w http.ResponseWriter, role Role, perm string) {
	if role == "" {
		fail(w, http.StatusForbidden, "no role that grants %s", perm)
		return
	}
	fail(w, http.StatusForbidden, "role %s lacks %s", role, perm)
}

// resolveSession finds the person a web token acts for, from X-Dzo-Session. It answers the request itself
// when there is none: 503 without logins, 403 without a session, 401 for an expired one, 403 for a person who
// is disabled or has no role on any instance.
func (s *Server) resolveSession(w http.ResponseWriter, r *http.Request, c *call) bool {
	if s.Accounts == nil {
		fail(w, http.StatusServiceUnavailable, "logins are not configured: set serve.web_url")
		return false
	}
	secret := r.Header.Get("X-Dzo-Session")
	if secret == "" {
		fail(w, http.StatusForbidden, "this token needs a user session")
		return false
	}
	c.session = auth.HashSecret(secret)
	user, code, msg := s.loadUser(r.Context(), c.who, c.session)
	if user == nil {
		fail(w, code, "%s", msg)
		return false
	}
	c.who.User = user
	c.actor = c.who.Token.Name + ":" + user.SteamID
	return true
}

// loadUser finds the person of a session hash and checks they may still act through the token. It is used
// when a request starts and again by open streams. Without a person it returns the status and message to answer.
func (s *Server) loadUser(ctx context.Context, who Principal, sessionHash string) (*User, int, string) {
	sess, err := s.Accounts.Session(ctx, sessionHash, s.now(), s.Login.idle(), s.Login.max())
	var person moderation.Person
	if err == nil {
		person, err = s.Accounts.Person(ctx, sess.SteamID)
	}
	switch {
	case errors.Is(err, moderation.ErrNotFound):
		return nil, http.StatusUnauthorized, "session expired"
	case err != nil:
		return nil, http.StatusInternalServerError, "database error"
	case person.Disabled:
		return nil, http.StatusForbidden, "this account is disabled"
	}
	who.User = &User{SteamID: person.SteamID, Name: person.Name, Username: person.Username, Grants: person.Grants}
	if len(who.Instances(s.Hubs.Names())) == 0 {
		return nil, http.StatusForbidden, "no access: this account has no role on any instance"
	}
	return who.User, 0, ""
}

// role is the caller's effective role on the instance of the request.
func (c *call) role() Role { return c.who.RoleOn(c.hub.Name) }

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
	name := c.who.Token.Name
	if c.who.User != nil {
		name = c.actor
	}
	info := Info{Installation: s.Installation, Version: s.Version, APIVersion: APIVersion, Principal: name, Role: c.who.TopRole()}
	if u := c.who.User; u != nil {
		info.User = &UserInfo{SteamID: u.SteamID, Name: u.Name, Username: u.Username, Roles: map[string]Role{}, Installation: []string{}}
		for _, n := range s.Hubs.Names() {
			if r := c.who.RoleOn(n); r != "" {
				info.User.Roles[n] = r
			}
		}
		for _, perm := range []string{PermAuditView, PermBanGlobal, PermBanRevokeAny, PermUsersManage} {
			if c.who.CanAll(perm) {
				info.User.Installation = append(info.User.Installation, perm)
			}
		}
	}
	writeJSON(w, 200, info)
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

// feed is GET .../feed?after=<id>&limit=<n>: the events with an id above after, oldest first.
func (s *Server) feed(w http.ResponseWriter, r *http.Request, c *call) {
	q := r.URL.Query()
	var after int64
	var limit int
	if v := q.Get("after"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &after); err != nil || after < 0 {
			fail(w, http.StatusBadRequest, "after must be an event id")
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit < 0 {
			fail(w, http.StatusBadRequest, "limit must be a number")
			return
		}
	}
	writeJSON(w, 200, c.hub.Feed(after, limit))
}

// stream is the SSE endpoint: the current state first, then every change. With only=feed it carries
// just the feed events and no snapshot.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, c *call) {
	only := r.URL.Query().Get("only")
	if only != "" && only != "feed" {
		fail(w, http.StatusBadRequest, "only must be feed")
		return
	}
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
	if only == "" {
		send("status", c.hub.Status())
		send("players", c.hub.Players())
		send("vehicles", c.hub.Vehicles())
		send("markers", filterMarkers(c.hub.Markers(""), c.role()))
		send("events", c.hub.Events())
	} else {
		// Headers go out now, so the client knows it is subscribed.
		_, _ = fmt.Fprint(w, ": feed\n\n")
		fl.Flush()
	}
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	recheck := time.NewTicker(cmp.Or(s.Recheck, time.Minute))
	defer recheck.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-recheck.C:
			if !s.stillAllowed(r.Context(), c) {
				return
			}
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			if only != "" && ev.Kind != only {
				continue
			}
			data := ev.Data
			if ev.Kind == "markers" {
				// The mod's markers plus the file drop, per the role's visibility.
				data = filterMarkers(c.hub.Markers(""), c.role())
			}
			if !send(ev.Kind, data) {
				return
			}
		}
	}
}

// stillAllowed re-checks a stream that acts for a person: the session must be alive, the person enabled
// and their role on the instance must still allow viewing. It also takes over a changed role.
func (s *Server) stillAllowed(ctx context.Context, c *call) bool {
	if c.session == "" {
		return true
	}
	user, _, _ := s.loadUser(ctx, c.who, c.session)
	if user == nil {
		return false
	}
	c.who.User = user
	return c.who.Can(c.hub.Name, PermView)
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
	entry := admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Instance: c.hub.Name, Action: action, Target: target, Detail: detail(cmd)}
	deny := func(code int, err error) {
		entry.Result = err.Error()
		_ = s.Audit.Log(entry)
		fail(w, code, "%v", err)
	}
	if !s.actions.Allow(c.who.Key()) || !s.targets.Allow(c.hub.Name+"/"+target) {
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
	case admin.KindVehicleMove:
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
	entry := admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Instance: c.hub.Name, Action: "message", Target: target, Detail: detail(cmd)}
	deny := func(code int, err error) {
		entry.Result = err.Error()
		_ = s.Audit.Log(entry)
		fail(w, code, "%v", err)
	}
	switch {
	case s.Say == nil:
		deny(http.StatusServiceUnavailable, errors.New("rcon is not available"))
	case cmd.SteamID != "":
		deny(http.StatusBadRequest, errors.New("rcon says to everyone only"))
	case cmd.Text == "" || len(cmd.Text) > 400:
		deny(http.StatusBadRequest, errors.New("message needs text of at most 400 bytes"))
	case !s.actions.Allow(c.who.Key()) || !s.targets.Allow(c.hub.Name+"/"+target):
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

type moveVehicleBody struct {
	X float64 `json:"x"`
	Z float64 `json:"z"`
}

// moveVehicle puts a vehicle, with its crew, at a position on the ground.
func (s *Server) moveVehicle(w http.ResponseWriter, r *http.Request, c *call) {
	var b moveVehicleBody
	if !decode(w, r, &b) {
		return
	}
	cmd := admin.Command{Kind: admin.KindVehicleMove, Vehicle: r.PathValue("id"), X: b.X, Z: b.Z}
	s.run(w, r, c, cmd, "vehicle_move", cmd.Vehicle)
}

// raiseFlag raises a territory flag to the top of its pole and fills its refresher time.
func (s *Server) raiseFlag(w http.ResponseWriter, r *http.Request, c *call) {
	cmd := admin.Command{Kind: admin.KindFlagRaise, Flag: r.PathValue("id")}
	s.run(w, r, c, cmd, "flag_raise", cmd.Flag)
}

func (s *Server) deleteVehicle(w http.ResponseWriter, r *http.Request, c *call) {
	cmd := admin.Command{Kind: admin.KindVehicleDelete, Vehicle: r.PathValue("id"), Force: r.URL.Query().Get("force") == "1"}
	s.run(w, r, c, cmd, "vehicle_delete", cmd.Vehicle)
}

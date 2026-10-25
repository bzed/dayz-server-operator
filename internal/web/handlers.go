// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/geoip"
)

type backendView struct {
	Name      string
	Info      *api.Info
	Instances []admin.Status
	Err       string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	var views []backendView
	for _, b := range s.backends {
		v := backendView{Name: b.Name}
		c := s.client(b, r)
		if info, err := c.Info(r.Context()); err != nil {
			v.Err = err.Error()
		} else {
			v.Info = &info
			if v.Instances, err = c.Instances(r.Context()); err != nil {
				v.Err = err.Error()
			}
		}
		views = append(views, v)
	}
	s.render(w, "dashboard", s.page(r, "Dashboard", "", "", "dashboard", views))
}

// target resolves the backend and instance of a request, or answers 404.
func (s *Server) target(w http.ResponseWriter, r *http.Request) (b *Backend, c *apiclient.Client, inst string, ok bool) {
	b = s.backend(r.PathValue("backend"))
	if b == nil {
		http.NotFound(w, r)
		return nil, nil, "", false
	}
	return b, s.client(b, r), r.PathValue("instance"), true
}

type instView struct {
	Backend, Instance string
	Status            admin.Status
	Role              api.Role
	Err               string
	Expansion         bool // the mod reports Expansion: its notification types are offered
	Rcon              bool // the broadcast form may use RCon say
}

func (s *Server) instance(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	v := instView{Backend: b.Name, Instance: inst}
	var err error
	if v.Status, err = c.Status(r.Context(), inst); err != nil {
		v.Err = err.Error()
	}
	if info, err := c.Info(r.Context()); err == nil {
		v.Role = info.Role
	}
	v.Expansion, v.Rcon = v.Status.Hello.Has("expansion"), true
	pg := s.page(r, inst, b.Name, inst, "overview", v)
	pg.setFacts(v.Status, err)
	s.render(w, "instance", pg)
}

type listView[T any] struct {
	Backend, Instance string
	Items             []T
	Err               string
	Flagged           map[string]string // Steam ID -> why the player is flagged, for the player list
}

func (s *Server) playersFragment(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	v := listView[admin.Player]{Backend: b.Name, Instance: inst}
	var err error
	if v.Items, err = c.Players(r.Context(), inst); err != nil {
		v.Err = err.Error()
	}
	v.Flagged = s.flagged(r, c, inst)
	s.render(w, "players", v)
}

func (s *Server) vehiclesFragment(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	v := listView[admin.Vehicle]{Backend: b.Name, Instance: inst}
	var err error
	if v.Items, err = c.Vehicles(r.Context(), inst); err != nil {
		v.Err = err.Error()
	}
	s.render(w, "vehicles", v)
}

type panelView struct {
	Backend, Instance string
	Player            admin.Player
	Others            []admin.Player // the other online players, as teleport targets
	Err               string
	Expansion         bool          // the mod reports Expansion
	Rcon              bool          // always false: RCon says to everyone only
	Cells             []verdictCell // the verdicts of the player, for those who may read the player database
}

// playerPanel is the action form block for one player; it is loaded on
// demand so the polled player table does not reset open forms.
func (s *Server) playerPanel(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	v := panelView{Backend: b.Name, Instance: inst}
	if st, err := c.Status(r.Context(), inst); err == nil {
		v.Expansion = st.Hello.Has("expansion")
	}
	players, err := c.Players(r.Context(), inst)
	if err != nil {
		v.Err = err.Error()
	}
	for _, p := range players {
		if p.SteamID == r.PathValue("steamid") {
			v.Player = p
		}
	}
	for _, p := range players {
		if p.SteamID != v.Player.SteamID {
			v.Others = append(v.Others, p)
		}
	}
	if v.Player.SteamID == "" && v.Err == "" {
		v.Err = "player is not online"
	}
	if v.Player.SteamID != "" {
		if d, err := c.PlayerDetail(r.Context(), v.Player.SteamID); err == nil { // without player.history or a history there is nothing to show
			v.Cells = verdicts(d, b.Name, time.Now())
		}
	}
	s.render(w, "panel", v)
}

type playerInfoView struct {
	Name, SteamID string
	Ping          int
	IP            string // the address of the current connection, for those who may see it
	Geo           geoip.Info
	Err           string
}

// playerInfo is the connection info shown over a player's name: the Steam ID, the ping, where the player is
// (the country; with the player.ip permission also the city, location and provider) and, with the same
// permission, the address the player is connected from. It is loaded on demand. The history of
// addresses is not shown here.
func (s *Server) playerInfo(w http.ResponseWriter, r *http.Request) {
	_, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	id := r.PathValue("steamid")
	v := playerInfoView{Name: id, SteamID: id}
	players, err := c.Players(r.Context(), inst)
	if err != nil {
		v.Err = err.Error()
	}
	for _, p := range players {
		if p.SteamID == id {
			v.Name, v.Ping = p.Name, p.Ping
		}
	}
	if v.Err == "" {
		v.Geo, _ = c.Geo(r.Context(), inst, id)                           // without a GeoIP database there is nothing to show
		if conn, err := c.Connection(r.Context(), inst, id); err == nil { // without player.ip there is no address
			v.IP = conn.IP
		}
	}
	s.render(w, "playerinfo", v)
}

type vpanelView struct {
	Backend, Instance string
	Vehicle           admin.Vehicle
	Err               string
}

// vehiclePanel is the action block for one vehicle, shown under its row.
func (s *Server) vehiclePanel(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	v := vpanelView{Backend: b.Name, Instance: inst}
	vehicles, err := c.Vehicles(r.Context(), inst)
	if err != nil {
		v.Err = err.Error()
	}
	for _, x := range vehicles {
		if x.ID == r.PathValue("id") {
			v.Vehicle = x
		}
	}
	if v.Vehicle.ID == "" && v.Err == "" {
		v.Err = "vehicle not found"
	}
	s.render(w, "vpanel", v)
}

// typesFragment answers the item picker's search with <option>s for a datalist.
func (s *Server) typesFragment(w http.ResponseWriter, r *http.Request) {
	_, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	q := r.URL.Query().Get("type")
	names, _ := c.Types(r.Context(), inst, q, 30)
	s.render(w, "types", names)
}

// vehicleTypesFragment is typesFragment for the vehicle picker.
func (s *Server) vehicleTypesFragment(w http.ResponseWriter, r *http.Request) {
	_, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	names, _ := c.VehicleTypes(r.Context(), inst, r.URL.Query().Get("type"), 30)
	s.render(w, "types", names)
}

// mapSizes are the world sizes in metres of the vanilla maps. Unverified for
// maps other than chernarusplus and enoch; unknown worlds use the default.
var mapSizes = map[string]int{"chernarusplus": 15360, "enoch": 12800, "sakhal": 15360}

const defaultMapSize = 15360

func mapSize(world string) int {
	if n, ok := mapSizes[strings.ToLower(world)]; ok {
		return n
	}
	return defaultMapSize
}

type mapView struct {
	Backend, Instance string
	Size              int
	World             string
	Role              api.Role
	TileMap           string // the map's name in the tile route, when it has tiles
	NoTiles           string // the tile set the map would use if it were built (the lower-case world name)
	Meta              string // the tile set's metadata.json
	Err               string
}

// tiles relays map tiles from a backend, so the browser never holds an API token.
func (s *Server) tiles(w http.ResponseWriter, r *http.Request) {
	b := s.backend(r.PathValue("backend"))
	if b == nil || !api.ValidTilePath(r.PathValue("map"), r.PathValue("rest")) {
		http.NotFound(w, r)
		return
	}
	resp, err := s.client(b, r).Tile(r.Context(), r.PathValue("map"), r.PathValue("rest"))
	if err != nil {
		http.Error(w, "map tiles are not available", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, h := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified", "Content-Length"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// tileMeta returns the metadata of a map's tile set, or "" if it has none.
func (s *Server) tileMeta(r *http.Request, c *apiclient.Client, name string) string {
	resp, err := c.Tile(r.Context(), name, "metadata.json")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil || resp.StatusCode != http.StatusOK || !json.Valid(b) {
		return ""
	}
	return string(b)
}

func (s *Server) mapPage(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	v := mapView{Backend: b.Name, Instance: inst, Size: defaultMapSize}
	st, err := c.Status(r.Context(), inst)
	if err != nil {
		v.Err = err.Error()
	} else if st.Hello != nil {
		v.World, v.Size = st.Hello.World, mapSize(st.Hello.World)
		if name := strings.ToLower(st.Hello.World); api.ValidTilePath(name, "metadata.json") {
			if v.Meta = s.tileMeta(r, c, name); v.Meta != "" {
				v.TileMap = name
			} else {
				v.NoTiles = name
			}
		}
	}
	if info, err := c.Info(r.Context()); err == nil {
		v.Role = info.Role
	}
	pg := s.page(r, inst+" map", b.Name, inst, "map", v)
	pg.setFacts(st, err)
	pg.Class = "map"
	s.render(w, "map", pg)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	b := s.backend(r.PathValue("backend"))
	if b == nil {
		http.NotFound(w, r)
		return
	}
	v := struct {
		Backend string
		Entries []admin.AuditEntry
		Err     string
	}{Backend: b.Name}
	var err error
	if v.Entries, err = s.client(b, r).Audit(r.Context(), r.URL.Query().Get("instance"), 200); err != nil {
		v.Err = err.Error()
	}
	s.render(w, "audit", s.page(r, "Audit log", b.Name, "", "audit", v))
}

// stream relays an instance's event stream to the browser, so the browser
// never holds an API token.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	_, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	only := r.URL.Query().Get("only")
	if only != "" && only != "feed" {
		http.Error(w, "only must be feed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	started := false
	if only != "" {
		// A feed stream has no snapshot to start with: open it now, so the browser knows it is connected.
		started = true
		w.WriteHeader(http.StatusOK)
		fl.Flush()
	}
	err := c.StreamOnly(r.Context(), inst, only, func(kind string, data json.RawMessage) {
		started = true
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		fl.Flush()
	})
	if err != nil && !started {
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

// ---- actions ----

type flash struct {
	OK   bool
	Text string
}

type actFn func(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error)

// act wraps an action: CSRF check, call the API, answer with a flash box.
func (s *Server) act(f actFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.csrfOK(r) {
			http.Error(w, "bad CSRF token", http.StatusForbidden)
			return
		}
		_, c, inst, ok := s.target(w, r)
		if !ok {
			return
		}
		if err := r.ParseForm(); err != nil {
			s.render(w, "flash", flash{Text: err.Error()})
			return
		}
		res, err := f(r, c, inst)
		switch {
		case err != nil:
			s.render(w, "flash", flash{Text: err.Error()})
		case res.Pending:
			s.render(w, "flash", flash{OK: true, Text: "queued"})
		case !res.OK:
			s.render(w, "flash", flash{Text: "the game server refused: " + res.Message})
		default:
			s.render(w, "flash", flash{OK: true, Text: "done"})
		}
	}
}

func num(r *http.Request, key string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue(key)), 64)
	return f
}

// quantity reads the quantity of a spawn form: a number, "default" or "max"; an empty field means max.
func quantity(r *http.Request) (float64, error) {
	s := strings.TrimSpace(r.PostFormValue("quantity"))
	if s == "" {
		s = "max"
	}
	return admin.ParseQuantity(s)
}

func actMessage(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	seconds, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("seconds"))) // empty or bad: 0, the default of the mod
	return c.Message(r.Context(), inst, r.PathValue("steamid"), api.MessageBody{
		Text: r.PostFormValue("text"), Style: r.PostFormValue("style"), NotifyType: r.PostFormValue("notify_type"),
		Title: strings.TrimSpace(r.PostFormValue("title")), Seconds: seconds})
}

func actTeleport(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.Teleport(r.Context(), inst, r.PathValue("steamid"), num(r, "x"), num(r, "z"), strings.TrimSpace(r.PostFormValue("to")))
}

func actGive(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	q, err := quantity(r)
	if err != nil {
		return api.ActionResult{}, err
	}
	return c.Give(r.Context(), inst, r.PathValue("steamid"), strings.TrimSpace(r.PostFormValue("type")), q, num(r, "health"), r.PostFormValue("target"), r.PostFormValue("attachments") != "")
}

func actSpawn(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	q, err := quantity(r)
	if err != nil {
		return api.ActionResult{}, err
	}
	return c.Spawn(r.Context(), inst, strings.TrimSpace(r.PostFormValue("type")), q, num(r, "health"), num(r, "x"), num(r, "z"), r.PostFormValue("attachments") != "")
}

func actSpawnVehicle(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.SpawnVehicle(r.Context(), inst, strings.TrimSpace(r.PostFormValue("type")), num(r, "x"), num(r, "z"))
}

func actKick(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.Kick(r.Context(), inst, r.PathValue("steamid"), strings.TrimSpace(r.PostFormValue("reason")))
}

func actHeal(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.Heal(r.Context(), inst, r.PathValue("steamid"))
}

func actFixGear(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.FixGear(r.Context(), inst, r.PathValue("steamid"))
}

func actKill(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.Kill(r.Context(), inst, r.PathValue("steamid"))
}

func actRepair(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.Repair(r.Context(), inst, r.PathValue("id"), r.PostFormValue("scope"))
}

func actFlip(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.Flip(r.Context(), inst, r.PathValue("id"))
}

func actMoveVehicle(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.MoveVehicle(r.Context(), inst, r.PathValue("id"), num(r, "x"), num(r, "z"))
}

func actRaise(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.RaiseFlag(r.Context(), inst, r.PathValue("id"))
}

func actDelete(r *http.Request, c *apiclient.Client, inst string) (api.ActionResult, error) {
	return c.DeleteVehicle(r.Context(), inst, r.PathValue("id"), r.PostFormValue("force") == "1")
}

// flag serves the flag of a country, <flags_dir>/<code>.svg. Only a two-letter code is looked up.
func (s *Server) flag(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(strings.TrimSuffix(r.PathValue("code"), ".svg"))
	if s.opts.FlagsDir == "" || len(code) != 2 || code[0] < 'a' || code[0] > 'z' || code[1] < 'a' || code[1] > 'z' {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFileFS(w, r, os.DirFS(s.opts.FlagsDir), code+".svg") //nolint:gosec // G703: code is two ASCII letters (checked above) and the file system is rooted at the flags directory
}

// setFacts fills the instance bar from an instance's status; without one (the request failed) the bar has the
// name and the tabs only.
func (p *page) setFacts(st admin.Status, err error) {
	if err != nil {
		return
	}
	f := factsOf(st, p.Now)
	p.Facts = &f
}

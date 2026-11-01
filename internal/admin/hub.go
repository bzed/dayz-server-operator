// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Timing of the command queue. A command the mod got but never answered is
// handed out again (the mod answers a repeated id from memory, so nothing
// runs twice); after commandTimeout it fails.
const (
	resendAfter    = 3 * time.Second
	commandTimeout = 30 * time.Second
	onlineWindow   = 10 * time.Second
)

// Errors returned to callers of Submit.
var (
	ErrOffline = errors.New("admin: dzo-admin is not connected for this instance")
	ErrTimeout = errors.New("admin: the mod did not answer in time")
)

// StreamEvent is something that changed in the world state, for the stream.
type StreamEvent struct {
	Kind string // players, vehicles, markers, events, result, status
	Data any
}

type pending struct {
	cmd    Command
	sentAt time.Time // zero while still queued
	queued time.Time
	result *Result
	done   chan struct{}
}

// Hello is what the mod announced about itself.
type Hello struct {
	ModVersion string    `json:"mod_version"`
	Proto      int       `json:"protocol"`
	World      string    `json:"world"`
	Since      time.Time `json:"since"`
	// Features lists what the mod can do beyond the basics; "expansion" when Expansion is loaded.
	Features []string `json:"features,omitempty"`
}

// Has reports whether the mod announced feature. A nil Hello has none.
func (h *Hello) Has(feature string) bool {
	return h != nil && slices.Contains(h.Features, feature)
}

// ReportsDrivers tells whether the mod announced that it reports the last driver of a vehicle.
func (h *Hub) ReportsDrivers() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hello.Has(FeatureDriver)
}

// NotifyTypes are the Expansion notification types a message can use.
var NotifyTypes = []string{"toast", "baguette", "activity", "killfeed", "market", "garage"}

// Status is the connection state of one instance's mod.
type Status struct {
	Instance  string    `json:"instance"`
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
	Hello     *Hello    `json:"hello,omitempty"`
	Players   int       `json:"players"`
	Vehicles  int       `json:"vehicles"`
	Markers   int       `json:"markers"`
	Events    int       `json:"events"`
	Types     int       `json:"types"`
	// GameTime is the in-game time of day ("14:05"), when the mod reports it.
	GameTime string `json:"game_time,omitempty"`
	// NextRestart is the next scheduled restart of the instance, when it has a schedule.
	NextRestart *time.Time `json:"next_restart,omitempty"`
}

// Hub holds one instance's world state and command queue. The mod side is
// Sync; everything else is for operators (API, CLI).
type Hub struct {
	Name string
	// Allow and Deny are the class-name globs (trailing *) that limit item
	// spawns on top of the mod's own check (§C16).
	Allow, Deny []string
	// Kinds are the class-name globs of the cars that are bicycles or helicopters (see DefaultVehicleKinds):
	// the game has them as cars, the hub reports them with their own kind.
	Kinds map[string][]string
	// MarkerDir is the file-drop directory ($profile:dzo/admin/markers).
	MarkerDir string
	// Styles are the icon and colour the site config gives a layer (the watch rules); they fill in
	// what a marker leaves unset, before the defaults of the layers the mod defines do.
	Styles map[string]Layer
	// NextRestart returns the next scheduled restart after a time, or the zero time; nil without a schedule.
	NextRestart func(time.Time) time.Time
	// Logf receives problems that must not stop the hub (a feed log that cannot be written). Nil logs nothing.
	Logf func(format string, args ...any)

	now func() time.Time

	mu       sync.Mutex
	hello    *Hello
	lastSeen time.Time
	// sentPlayers and sentRestart are what the last status event said, so a change is announced.
	sentPlayers int
	sentRestart time.Time
	sentClock   string
	// wantTypes asks the mod to send the class list again from the start.
	wantTypes bool
	gameTime  string
	players   []Player
	vehicles  []Vehicle
	markers   []Marker
	layers    []Layer
	events    []Event
	types     typeList
	vtypes    typeList
	queue     []*pending
	byID      map[string]*pending
	subs      map[chan StreamEvent]struct{}
	files     fileMarkers
	// feed is the ring of the newest FeedKeep events, feedID the last ID given, lastSeq the last mod
	// sequence number stored (a resend is not stored twice).
	feed    []FeedEvent
	feedID  int64
	lastSeq int
	feedLog *FeedLog
}

// NewHub returns an empty hub for an instance.
func NewHub(name string) *Hub {
	return &Hub{Name: name, now: time.Now, byID: map[string]*pending{}, subs: map[chan StreamEvent]struct{}{}}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Sync applies one mod request and returns the commands due for it.
func (h *Hub) Sync(req SyncRequest) SyncReply {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	h.lastSeen = now
	if h.hello == nil || req.Hello {
		h.hello = &Hello{ModVersion: req.ModVersion, Proto: req.Proto, World: req.World, Since: now, Features: req.Features}
		h.emit("status", h.statusLocked())
	}
	if slices.Contains(req.Has, "clock") && req.Clock >= 0 && req.Clock < 24*60 {
		h.gameTime = fmt.Sprintf("%02d:%02d", req.Clock/60, req.Clock%60)
	}
	if req.Players != nil {
		h.players = req.Players
		h.linkVehiclesLocked()
		h.emit("players", append([]Player(nil), h.players...))
		// The status carries the player count and the next restart, which the map shows.
		s := h.statusLocked()
		var next time.Time
		if s.NextRestart != nil {
			next = *s.NextRestart
		}
		if s.Players != h.sentPlayers || !next.Equal(h.sentRestart) {
			h.sentPlayers, h.sentRestart = s.Players, next
			h.sentClock = s.GameTime
			h.emit("status", s)
		}
	}
	if h.gameTime != h.sentClock {
		h.sentClock = h.gameTime
		h.emit("status", h.statusLocked())
	}
	if req.Vehicles != nil {
		h.vehicles = req.Vehicles
		for i := range h.vehicles {
			h.vehicles[i].checkDrive(now)
			if h.vehicles[i].Kind == "car" {
				if k := kindOf(h.Kinds, h.vehicles[i].Type); k != "" {
					h.vehicles[i].Kind = k
				}
			}
		}
		h.linkVehiclesLocked()
		h.emit("vehicles", append([]Vehicle(nil), h.vehicles...))
	}
	if req.Markers != nil {
		h.markers = req.Markers
		h.layers = req.Layers
		h.emit("markers", h.styledLocked(h.markers))
	}
	if req.Events != nil {
		h.events = req.Events
		h.emit("events", append([]Event(nil), h.events...))
	}
	if req.Types != nil {
		if !h.types.add(*req.Types) {
			h.wantTypes = true
		}
	}
	if req.VehicleTypes != nil {
		if !h.vtypes.add(*req.VehicleTypes) {
			h.wantTypes = true
		}
	}
	h.syncFeedLocked(req)
	for i := range req.Results {
		h.finishLocked(req.Results[i])
	}

	reply := SyncReply{OK: true, Proto: ProtocolVersion, Commands: []Command{}, Hello: h.hello != nil && h.hello.World == "", ResendTypes: h.wantTypes}
	h.wantTypes = false
	live := h.queue[:0]
	for _, p := range h.queue {
		switch {
		case p.result != nil:
			continue
		case now.Sub(p.queued) > commandTimeout:
			h.finishLocked(Result{ID: p.cmd.ID, Message: ErrTimeout.Error()})
			continue
		}
		if p.sentAt.IsZero() || now.Sub(p.sentAt) >= resendAfter {
			p.sentAt = now
			reply.Commands = append(reply.Commands, p.cmd)
		}
		live = append(live, p)
	}
	h.queue = live
	return reply
}

const (
	maxDriveAge   = 400 * 24 * time.Hour // a drive older than this is not believed
	maxDriverName = 64                   // runes of a driver name kept
)

// checkDrive validates the driver data the mod sent: a bad driver ID or drive age drops the drive (the
// vehicle stays), otherwise DrivenAt is set from DrivenAgo and the name is clipped.
func (v *Vehicle) checkDrive(now time.Time) {
	v.DrivenAt = time.Time{}
	age := v.DrivenAgo
	if v.Driver == "" || !validSteamID(v.Driver) || math.IsNaN(age) || math.IsInf(age, 0) ||
		age < 0 || age > maxDriveAge.Seconds() {
		v.Driver, v.DriverName, v.DrivenAgo = "", "", 0
		return
	}
	v.DrivenAt = now.Add(-time.Duration(age * float64(time.Second)))
	if r := []rune(v.DriverName); len(r) > maxDriverName {
		v.DriverName = string(r[:maxDriverName]) + "…"
	}
}

// validSteamID is moderation.ValidSteamID (moderation imports admin, so admin cannot import it); a test
// in moderation checks that the two agree.
func validSteamID(s string) bool {
	if len(s) != 17 || !strings.HasPrefix(s, "7656119") {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// linkVehiclesLocked sets Player.Vehicle from the vehicles' occupants.
func (h *Hub) linkVehiclesLocked() {
	in := map[string]string{}
	for _, v := range h.vehicles {
		for _, o := range v.Occupants {
			in[o] = v.ID
		}
	}
	for i := range h.players {
		h.players[i].Vehicle = in[h.players[i].SteamID]
	}
}

func (h *Hub) finishLocked(r Result) {
	p := h.byID[r.ID]
	if p == nil || p.result != nil {
		return
	}
	p.result = &r
	close(p.done)
	h.emit("result", r)
}

func (h *Hub) emit(kind string, data any) {
	for ch := range h.subs {
		select {
		case ch <- StreamEvent{Kind: kind, Data: data}:
		default: // slow consumer: it refetches the snapshot
		}
	}
}

// Subscribe streams state changes until ctx is done.
func (h *Hub) Subscribe(ctx context.Context) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
		close(ch)
	}()
	return ch
}

func (h *Hub) connectedLocked() bool {
	return h.hello != nil && h.now().Sub(h.lastSeen) < onlineWindow
}

func (h *Hub) statusLocked() Status {
	s := Status{
		Instance: h.Name, Connected: h.connectedLocked(), LastSeen: h.lastSeen, Hello: h.hello,
		Players: len(h.players), Vehicles: len(h.vehicles), Markers: len(h.markers), Events: len(h.events), Types: len(h.types.names),
	}
	s.GameTime = h.gameTime
	if h.NextRestart != nil {
		if t := h.NextRestart(h.now()); !t.IsZero() {
			s.NextRestart = &t
		}
	}
	return s
}

// Status reports whether the mod is connected and how much state it pushed.
func (h *Hub) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusLocked()
}

// Players returns the last pushed player list.
func (h *Hub) Players() []Player {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Player(nil), h.players...)
}

// Vehicles returns the last pushed vehicle list.
func (h *Hub) Vehicles() []Vehicle {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Vehicle(nil), h.vehicles...)
}

// Events returns the active events.
func (h *Hub) Events() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]Event(nil), h.events...)
	for i := range out {
		if out[i].Icon == "" {
			s := StyleOfEvent(out[i].Category)
			out[i].Icon, out[i].Color, out[i].Label = s.Icon, s.Color, s.Label
		}
	}
	return out
}

// Layers returns the defined marker layers, plus the layers markers use
// without defining them.
func (h *Hub) Layers() []Layer {
	markers := h.Markers("")
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	out := []Layer{}
	for _, l := range h.layers {
		seen[l.Name] = true
		out = append(out, l)
	}
	for _, m := range markers {
		if !seen[m.Layer] {
			seen[m.Layer] = true
			out = append(out, Layer{Name: m.Layer, Visibility: h.Styles[m.Layer].Visibility})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// styledLocked returns a copy of ms in which the icon and colour a marker leaves unset are those
// of its layer: from the site config (watch rules), else from the layer the mod defined.
func (h *Hub) styledLocked(ms []Marker) []Marker {
	out := append([]Marker(nil), ms...)
	for i := range out {
		m := &out[i]
		site := h.Styles[m.Layer]
		// The mod marks every marker admin-only; the rule of a watched layer says who sees it.
		if m.Source == "watch" && site.Visibility != "" {
			m.Visibility = site.Visibility
		}
		if m.Icon != "" && m.Color != "" {
			continue
		}
		var mod Layer
		for _, l := range h.layers {
			if l.Name == m.Layer {
				mod = l
			}
		}
		if m.Icon == "" {
			m.Icon = firstNonEmpty(site.Icon, mod.Icon)
		}
		if m.Color == "" {
			m.Color = firstNonEmpty(site.Color, mod.Color)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Markers returns the markers of one layer (all layers if empty): what the
// mod pushed plus the file-drop markers.
func (h *Hub) Markers(layer string) []Marker {
	h.mu.Lock()
	dir, mods := h.MarkerDir, h.styledLocked(h.markers)
	h.mu.Unlock()
	all := append(mods, h.files.load(dir, h.now())...)
	if layer == "" {
		return all
	}
	out := []Marker{}
	for _, m := range all {
		if m.Layer == layer {
			out = append(out, m)
		}
	}
	return out
}

// Types searches the spawnable class list (case-insensitive substring),
// returning at most limit names.
func (h *Hub) Types(q string, limit int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.types.search(q, limit)
}

// VehicleTypes searches the list of vehicle classes the mod can spawn, like Types.
func (h *Hub) VehicleTypes(q string, limit int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vtypes.search(q, limit)
}

func (t *typeList) search(q string, limit int) []string {
	q = strings.ToLower(q)
	out := []string{}
	for _, n := range t.names {
		if q == "" || strings.Contains(strings.ToLower(n), q) {
			out = append(out, n)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

// Submit validates cmd, queues it for the mod and waits for the result.
// Without a connected mod it fails right away with ErrOffline.
func (h *Hub) Submit(ctx context.Context, cmd Command) (Result, error) {
	p, err := h.enqueue(cmd)
	if err != nil {
		return Result{}, err
	}
	select {
	case <-p.done:
		return *p.result, nil
	case <-ctx.Done():
		return Result{ID: p.cmd.ID}, ErrTimeout
	}
}

// Enqueue is Submit without waiting: it returns the command id, and the
// result is read with Result.
func (h *Hub) Enqueue(cmd Command) (string, error) {
	p, err := h.enqueue(cmd)
	if err != nil {
		return "", err
	}
	return p.cmd.ID, nil
}

func (h *Hub) enqueue(cmd Command) (*pending, error) {
	if err := h.validate(&cmd); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.connectedLocked() {
		return nil, ErrOffline
	}
	if cmd.Kind == KindMessage && cmd.Style == "expansion" && !h.hello.Has("expansion") {
		return nil, errors.New("admin: expansion notifications are not available (the mod does not report Expansion)")
	}
	cmd.ID = newID()
	p := &pending{cmd: cmd, queued: h.now(), done: make(chan struct{})}
	h.queue = append(h.queue, p)
	h.byID[cmd.ID] = p
	return p, nil
}

// Wait waits for the result of a command that Enqueue returned, until ctx is done (ErrTimeout, with the id in
// the result). An unknown id is an error.
func (h *Hub) Wait(ctx context.Context, id string) (Result, error) {
	h.mu.Lock()
	p := h.byID[id]
	h.mu.Unlock()
	if p == nil {
		return Result{ID: id}, fmt.Errorf("admin: unknown command %s", id)
	}
	select {
	case <-p.done:
		return *p.result, nil
	case <-ctx.Done():
		return Result{ID: id}, ErrTimeout
	}
}

// Result returns the result of a command id, and whether it has arrived.
func (h *Hub) Result(id string) (Result, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.byID[id]
	if p == nil || p.result == nil {
		return Result{ID: id}, false
	}
	return *p.result, true
}

// validate checks a command's required fields and the spawn allow/deny
// lists; it normalises defaults.
func (h *Hub) validate(c *Command) error {
	need := func(name, v string) error {
		if v == "" {
			return fmt.Errorf("admin: %s needs %s", c.Kind, name)
		}
		return nil
	}
	switch c.Kind {
	case KindMessage:
		if err := need("text", c.Text); err != nil {
			return err
		}
		if len(c.Text) > 400 {
			return errors.New("admin: message is longer than 400 bytes")
		}
		switch c.Style {
		case "":
			c.Style = "chat"
		case "chat", "blue", "important", "friendly", "action", "notification", "expansion":
		default:
			return fmt.Errorf("admin: unknown message style %q", c.Style)
		}
		if len(c.Title) > 64 {
			return errors.New("admin: message title is longer than 64 bytes")
		}
		if c.Title == "" {
			c.Title = "Server"
		}
		if c.Seconds < 0 || c.Seconds > 60 {
			return errors.New("admin: message seconds must be between 0 and 60")
		}
		if c.Seconds == 0 {
			c.Seconds = 10
		}
		if c.Style == "expansion" {
			if c.NotifyType == "" {
				c.NotifyType = "toast"
			}
			if !slices.Contains(NotifyTypes, c.NotifyType) {
				return fmt.Errorf("admin: unknown expansion type %q (want %s)", c.NotifyType, strings.Join(NotifyTypes, ", "))
			}
		}
	case KindPlayerKill, KindPlayerHeal, KindPlayerFixGear:
		return need("steam_id", c.SteamID)
	case KindTeleport:
		if err := need("steam_id", c.SteamID); err != nil {
			return err
		}
		if c.ToSteamID == "" && c.X == 0 && c.Z == 0 {
			return errors.New("admin: teleport needs a position or to_steam_id")
		}
	case KindSpawnItem:
		if err := need("type", c.Type); err != nil {
			return err
		}
		if err := h.checkClass(c.Type, &h.types); err != nil {
			return err
		}
		if c.SteamID == "" {
			// No player: the item lies on the ground at the position.
			if c.X == 0 && c.Z == 0 {
				return errors.New("admin: spawn_item needs steam_id or a position")
			}
			if c.Target != "" && c.Target != "ground" {
				return fmt.Errorf("admin: an item spawned at a position lies on the ground, not in %q", c.Target)
			}
			c.Target = "ground"
		}
		switch c.Target {
		case "":
			c.Target = "inventory"
		case "inventory", "hands", "ground":
		default:
			return fmt.Errorf("admin: unknown spawn target %q", c.Target)
		}
		if c.Health < 0 || c.Health > 1 || (c.Quantity < 0 && c.Quantity != QuantityMax && c.Quantity != QuantityDefault) {
			return errors.New("admin: health must be 0..1 and quantity a number, default or max")
		}
	case KindSpawnVehicle:
		if err := need("type", c.Type); err != nil {
			return err
		}
		if err := h.checkClass(c.Type, &h.vtypes); err != nil {
			return err
		}
		if c.X == 0 && c.Z == 0 {
			return errors.New("admin: spawn_vehicle needs a position")
		}
	case KindVehicleRepair:
		if err := need("vehicle", c.Vehicle); err != nil {
			return err
		}
		switch c.Scope {
		case "":
			c.Scope = "all"
		case "all", "engine", "parts", "wheels", "fluids", "missing":
		default:
			return fmt.Errorf("admin: unknown repair scope %q", c.Scope)
		}
	case KindVehicleFlip, KindVehicleDelete:
		return need("vehicle", c.Vehicle)
	case KindVehicleMove:
		if err := need("vehicle", c.Vehicle); err != nil {
			return err
		}
		if c.X == 0 && c.Z == 0 {
			return errors.New("admin: vehicle_move needs a position")
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, v := range h.vehicles {
			if v.ID == c.Vehicle && v.Covered {
				return errors.New("admin: the vehicle is covered (stored by Expansion) and cannot be moved")
			}
		}
	case KindFlagRaise:
		return need("flag", c.Flag)
	default:
		return fmt.Errorf("admin: unknown command kind %q", c.Kind)
	}
	return nil
}

func (h *Hub) checkClass(name string, list *typeList) error {
	if !validClass(name) {
		return fmt.Errorf("admin: %q is not a valid class name", name)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if MatchGlobs(h.Deny, name) {
		return fmt.Errorf("admin: class %q is denied on this instance", name)
	}
	if len(h.Allow) > 0 && !MatchGlobs(h.Allow, name) {
		return fmt.Errorf("admin: class %q is not in the allow list of this instance", name)
	}
	if list.complete() && !list.has(name) {
		return fmt.Errorf("admin: unknown class %q", name)
	}
	return nil
}

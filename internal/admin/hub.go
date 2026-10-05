// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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
}

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
}

// Hub holds one instance's world state and command queue. The mod side is
// Sync; everything else is for operators (API, CLI).
type Hub struct {
	Name string
	// Allow and Deny are the class-name globs (trailing *) that limit item
	// spawns on top of the mod's own check (§C16).
	Allow, Deny []string
	// MarkerDir is the file-drop directory ($profile:dzo-admin/markers).
	MarkerDir string

	now func() time.Time

	mu       sync.Mutex
	hello    *Hello
	lastSeen time.Time
	players  []Player
	vehicles []Vehicle
	markers  []Marker
	layers   []Layer
	events   []Event
	types    typeList
	queue    []*pending
	byID     map[string]*pending
	subs     map[chan StreamEvent]struct{}
	files    fileMarkers
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
		h.hello = &Hello{ModVersion: req.ModVersion, Proto: req.Proto, World: req.World, Since: now}
		h.emit("status", h.statusLocked())
	}
	if req.Players != nil {
		h.players = req.Players
		h.linkVehiclesLocked()
		h.emit("players", append([]Player(nil), h.players...))
	}
	if req.Vehicles != nil {
		h.vehicles = req.Vehicles
		h.linkVehiclesLocked()
		h.emit("vehicles", append([]Vehicle(nil), h.vehicles...))
	}
	if req.Markers != nil {
		h.markers = req.Markers
		h.layers = req.Layers
		h.emit("markers", append([]Marker(nil), h.markers...))
	}
	if req.Events != nil {
		h.events = req.Events
		h.emit("events", append([]Event(nil), h.events...))
	}
	if req.Types != nil {
		h.types.add(*req.Types)
	}
	for i := range req.Results {
		h.finishLocked(req.Results[i])
	}

	reply := SyncReply{OK: true, Proto: ProtocolVersion, Commands: []Command{}, Hello: h.hello != nil && h.hello.World == ""}
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
	return Status{
		Instance: h.Name, Connected: h.connectedLocked(), LastSeen: h.lastSeen, Hello: h.hello,
		Players: len(h.players), Vehicles: len(h.vehicles), Markers: len(h.markers), Events: len(h.events), Types: len(h.types.names),
	}
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
	return append([]Event(nil), h.events...)
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
			out = append(out, Layer{Name: m.Layer})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Markers returns the markers of one layer (all layers if empty): what the
// mod pushed plus the file-drop markers.
func (h *Hub) Markers(layer string) []Marker {
	h.mu.Lock()
	dir, mods := h.MarkerDir, append([]Marker(nil), h.markers...)
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
	q = strings.ToLower(q)
	out := []string{}
	for _, n := range h.types.names {
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
	cmd.ID = newID()
	p := &pending{cmd: cmd, queued: h.now(), done: make(chan struct{})}
	h.queue = append(h.queue, p)
	h.byID[cmd.ID] = p
	return p, nil
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
		case "chat", "important", "notification":
		default:
			return fmt.Errorf("admin: unknown message style %q", c.Style)
		}
	case KindTeleport:
		if err := need("steam_id", c.SteamID); err != nil {
			return err
		}
		if c.ToSteamID == "" && c.X == 0 && c.Z == 0 {
			return errors.New("admin: teleport needs a position or to_steam_id")
		}
	case KindSpawnItem:
		if err := need("steam_id", c.SteamID); err != nil {
			return err
		}
		if err := need("type", c.Type); err != nil {
			return err
		}
		if err := h.checkClass(c.Type); err != nil {
			return err
		}
		switch c.Target {
		case "":
			c.Target = "inventory"
		case "inventory", "hands", "ground":
		default:
			return fmt.Errorf("admin: unknown spawn target %q", c.Target)
		}
		if c.Health < 0 || c.Health > 1 || c.Quantity < 0 {
			return errors.New("admin: health must be 0..1 and quantity not negative")
		}
	case KindVehicleRepair:
		if err := need("vehicle", c.Vehicle); err != nil {
			return err
		}
		switch c.Scope {
		case "":
			c.Scope = "all"
		case "all", "engine", "parts", "wheels", "fluids":
		default:
			return fmt.Errorf("admin: unknown repair scope %q", c.Scope)
		}
	case KindVehicleDelete:
		return need("vehicle", c.Vehicle)
	default:
		return fmt.Errorf("admin: unknown command kind %q", c.Kind)
	}
	return nil
}

func (h *Hub) checkClass(name string) error {
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
	if h.types.complete() && !h.types.has(name) {
		return fmt.Errorf("admin: unknown class %q", name)
	}
	return nil
}

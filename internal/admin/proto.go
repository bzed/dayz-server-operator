// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package admin is the operator side of the dzo-admin servermod (§C16): the
// versioned protocol the mod speaks, one Hub per instance (world state for the
// map, a command queue with timeouts and idempotent ids), the endpoint the mod
// calls, an audit log, and the limits on what admins may do. It knows nothing
// about HTTP APIs or web pages; internal/api and the CLI are thin adapters on
// top of Hub.
package admin

import "fmt"

// ProtocolVersion is the dzo <-> dzo-admin protocol version; the mod sends
// its own in every sync (DZOADMIN_PROTOCOL_VERSION in the mod).
const ProtocolVersion = 1

// Flag is a boolean that also reads the 0/1 the mod's JsonSerializer writes
// for bool fields; it is written as true/false.
type Flag bool

// UnmarshalJSON accepts true, false, 0 and 1.
func (f *Flag) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", "1":
		*f = true
	case "false", "0", "null":
		*f = false
	default:
		return fmt.Errorf("admin: %s is not a boolean", b)
	}
	return nil
}

// The structs below mirror the mod's Proto.c field by field (the JSON keys
// are the Enforce field names), and double as the API's JSON for the map.

// Player is one online player. The Steam ID is the only identity (D27).
type Player struct {
	SteamID     string  `json:"steam_id"`
	Name        string  `json:"name"`
	Slot        int     `json:"slot"`
	Ping        int     `json:"ping"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Z           float64 `json:"z"`
	Yaw         float64 `json:"yaw"`
	Health      float64 `json:"health"`
	Blood       float64 `json:"blood"`
	Shock       float64 `json:"shock"`
	Alive       Flag    `json:"alive"`
	Unconscious Flag    `json:"unconscious"`
	// Vehicle is the id of the vehicle the player sits in, derived from the
	// vehicles' occupant lists.
	Vehicle string `json:"vehicle,omitempty"`
}

// Vehicle is one persistent vehicle.
type Vehicle struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	X         float64  `json:"x"`
	Y         float64  `json:"y"`
	Z         float64  `json:"z"`
	Yaw       float64  `json:"yaw"`
	Health    float64  `json:"health"`
	Fuel      float64  `json:"fuel"`
	Ruined    Flag     `json:"ruined"`
	Engine    Flag     `json:"engine"`
	Occupants []string `json:"occupants"`
}

// KV is one tooltip entry of a marker.
type KV struct {
	K string `json:"k"`
	V string `json:"v"`
}

// Point is a world position (x east, z north) of a polygon or polyline.
type Point struct {
	X float64 `json:"x"`
	Z float64 `json:"z"`
}

// Marker is one map marker (§C16): the same model in the script API, the
// file drop and the web UI.
type Marker struct {
	Layer      string  `json:"layer"`
	ID         string  `json:"id"`
	Shape      string  `json:"shape"` // point (default), circle, polygon, polyline
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Z          float64 `json:"z"`
	Radius     float64 `json:"radius,omitempty"`
	Points     []Point `json:"points,omitempty"`
	Icon       string  `json:"icon,omitempty"`
	Color      string  `json:"color,omitempty"`
	Label      string  `json:"label,omitempty"`
	Props      []KV    `json:"props,omitempty"`
	TTL        int     `json:"ttl,omitempty"`
	Visibility string  `json:"visibility,omitempty"` // lowest role that sees it, default admin
	Source     string  `json:"source,omitempty"`     // api, tracked, watch, file
}

// Layer is the default style of a marker layer.
type Layer struct {
	Name       string `json:"name"`
	Icon       string `json:"icon,omitempty"`
	Color      string `json:"color,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// Event is an active in-game event (effect areas, marker-API events).
type Event struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	Radius   float64 `json:"radius"`
	Source   string  `json:"source"`
}

// TypesChunk is one piece of the spawnable class list.
type TypesChunk struct {
	Hash   string   `json:"hash"`
	Offset int      `json:"offset"`
	Total  int      `json:"total"`
	Names  []string `json:"names"`
}

// Command kinds.
const (
	KindMessage       = "message"
	KindTeleport      = "teleport"
	KindSpawnItem     = "spawn_item"
	KindVehicleRepair = "vehicle_repair"
	KindVehicleDelete = "vehicle_delete"
)

// Command is one action for the mod. All fields are always serialised (the
// mod's JSON reader keeps zero values for the ones a kind does not use).
type Command struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	SteamID   string  `json:"steam_id"`
	ToSteamID string  `json:"to_steam_id"`
	Text      string  `json:"text"`
	Style     string  `json:"style"`
	Type      string  `json:"type"`
	Quantity  float64 `json:"quantity"`
	Health    float64 `json:"health"`
	Target    string  `json:"target"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
	HasY      bool    `json:"has_y"`
	Vehicle   string  `json:"vehicle"`
	Scope     string  `json:"scope"`
	Force     bool    `json:"force"`
}

// Result is the mod's answer to a Command.
type Result struct {
	ID      string `json:"id"`
	OK      Flag   `json:"ok"`
	Message string `json:"message"`
}

// SyncRequest is one POST /mod/v1/sync body. Slices are nil when the mod did
// not send that part (a part that is due but empty is a non-nil empty slice).
type SyncRequest struct {
	Token      string `json:"token"`
	Proto      int    `json:"protocol"`
	ModVersion string `json:"mod_version"`
	World      string `json:"world"`
	Seq        int    `json:"seq"`
	Hello      Flag   `json:"hello"`
	// Has names the parts the mod included. Its JSON writer turns a missing
	// array into [], so an empty array alone does not mean "none".
	Has      []string    `json:"has"`
	Players  []Player    `json:"players"`
	Vehicles []Vehicle   `json:"vehicles"`
	Markers  []Marker    `json:"markers"`
	Layers   []Layer     `json:"layers"`
	Events   []Event     `json:"events"`
	Types    *TypesChunk `json:"types"` // {} (Total 0) when there is no chunk
	Results  []Result    `json:"results"`
}

// SyncReply is the answer to a sync: the commands waiting for the mod.
type SyncReply struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Proto    int       `json:"protocol"`
	Commands []Command `json:"commands"`
	// Hello asks the mod to send its hello again (with the world name): set while the hub
	// does not know the world, for example after dzo serve restarted under a running server.
	Hello bool `json:"hello,omitempty"`
}

// Normalise drops the parts a mod request lists as not included. A request
// without a Has list (from Go callers) is taken as it is.
func (r *SyncRequest) Normalise() {
	if r.Has == nil {
		return
	}
	has := map[string]bool{}
	for _, h := range r.Has {
		has[h] = true
	}
	if !has["players"] {
		r.Players = nil
	}
	if !has["vehicles"] {
		r.Vehicles = nil
	}
	if !has["markers"] {
		r.Markers, r.Layers = nil, nil
	}
	if !has["events"] {
		r.Events = nil
	}
	if r.Types != nil && r.Types.Total == 0 {
		r.Types = nil
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package admin is the operator side of the dzo-admin servermod (§C16): the
// versioned protocol the mod speaks, one Hub per instance (world state for the
// map, a command queue with timeouts and idempotent ids), the endpoint the mod
// calls, an audit log, and the limits on what admins may do. It knows nothing
// about HTTP APIs or web pages; internal/api and the CLI are thin adapters on
// top of Hub.
package admin

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

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
	// Country is the ISO country code of the address the player connected from, for the flag. The API
	// fills it in (internal/geoip); the mod does not send it.
	Country string `json:"country,omitempty"`
}

// Vehicle is one persistent vehicle.
type Vehicle struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"` // car, motorbike, boat, bicycle or helicopter (the last two the hub's); an older mod sends none and is taken as car
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
	Owner     string   `json:"owner,omitempty"`   // DayZ Expansion: the name on the key chain
	Covered   Flag     `json:"covered,omitempty"` // DayZ Expansion: stored under a cover; only class and place are known

	// Driver, DriverName and DrivenAgo come from the mod (feature "driver"): the last player to take the
	// driver's seat and the seconds since. The hub drops all three when the drive is not plausible.
	Driver     string  `json:"driver,omitempty"`
	DriverName string  `json:"driver_name,omitempty"`
	DrivenAgo  float64 `json:"driven_ago,omitempty"`
	// DrivenAt is set by the hub from DrivenAgo (zero when there is no valid drive).
	DrivenAt time.Time `json:"-"`
	// LastDriver, LastDriverName, LastDriven and FirstSeen are filled by the API from the vehicle records.
	LastDriver     string     `json:"last_driver,omitempty"`
	LastDriverName string     `json:"last_driver_name,omitempty"`
	LastDriven     *time.Time `json:"last_driven,omitempty"`
	FirstSeen      *time.Time `json:"first_seen,omitempty"`
}

// FeatureDriver is the hello feature of a mod that reports the last driver of a vehicle.
const FeatureDriver = "driver"

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
	Source     string  `json:"source,omitempty"`     // api, tracked, watch, file, flags, territory, rebuilt, sandstorm
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
	// Icon, Color and Label come from the event style (dzo fills them in, the mod does not send them).
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
	Label string `json:"label,omitempty"`
}

// TypesChunk is one piece of the spawnable class list.
type TypesChunk struct {
	Hash   string   `json:"hash"`
	Offset int      `json:"offset"`
	Total  int      `json:"total"`
	Names  []string `json:"names"`
}

// Quantity of a spawn_item command: a number is what the mod passes to SetQuantity (0 makes an item that is
// empty, or none at all when the class cannot exist empty), more than a stack holds makes more stacks. The two
// negative values are not numbers.
const (
	QuantityDefault = -1 // leave it at what the game makes of a new item
	QuantityMax     = -2 // one full stack, filled to the class's own GetQuantityMax()
)

// ParseQuantity reads a quantity given by a person: "default" (also an empty text and "-1"), "max" (also
// "-2") or a number of 0 or more.
func ParseQuantity(s string) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default", "-1":
		return QuantityDefault, nil
	case "max", "-2":
		return QuantityMax, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, errors.New("quantity must be a number, default or max")
	}
	return f, nil
}

// Command kinds.
const (
	KindMessage       = "message"
	KindTeleport      = "teleport"
	KindPlayerKill    = "player_kill"
	KindPlayerHeal    = "player_heal"
	KindPlayerFixGear = "player_fixgear"
	KindSpawnItem     = "spawn_item"
	KindSpawnVehicle  = "spawn_vehicle"
	KindVehicleRepair = "vehicle_repair"
	KindVehicleFlip   = "vehicle_flip"
	KindVehicleMove   = "vehicle_move"
	KindVehicleDelete = "vehicle_delete"
	KindFlagRaise     = "flag_raise"
)

// Command is one action for the mod. All fields are always serialised (the
// mod's JSON reader keeps zero values for the ones a kind does not use).
type Command struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	SteamID   string `json:"steam_id"`
	ToSteamID string `json:"to_steam_id"`
	Text      string `json:"text"`
	Style     string `json:"style"`
	// NotifyType, Title and Seconds shape a notification message: the Expansion type (style
	// expansion), the heading and how long it stays on screen.
	NotifyType string  `json:"notify_type"`
	Title      string  `json:"title"`
	Seconds    int     `json:"seconds"`
	Type       string  `json:"type"`
	Quantity   float64 `json:"quantity"`
	Health     float64 `json:"health"`
	Target     string  `json:"target"`
	// Attachments asks for the item the way the game's own debug spawn makes it: with the kit it
	// needs to be used (a flashlight with its battery, a weapon with its parts).
	Attachments bool    `json:"attachments,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Z           float64 `json:"z"`
	HasY        bool    `json:"has_y"`
	Vehicle     string  `json:"vehicle"`
	// Flag is the id of the territory flag (the id of its marker on the Flags layer) of flag_raise.
	Flag  string `json:"flag,omitempty"`
	Scope string `json:"scope"`
	Force bool   `json:"force"`
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
	// Features lists what the mod can do beyond the basics; "expansion" when Expansion is loaded.
	Features []string `json:"features"`
	// Clock is the in-game time of day in minutes since midnight; it counts when Has lists "clock".
	Clock int `json:"clock"`
	// Has names the parts the mod included. Its JSON writer turns a missing
	// array into [], so an empty array alone does not mean "none".
	Has      []string    `json:"has"`
	Players  []Player    `json:"players"`
	Vehicles []Vehicle   `json:"vehicles"`
	Markers  []Marker    `json:"markers"`
	Layers   []Layer     `json:"layers"`
	Events   []Event     `json:"events"`
	Types    *TypesChunk `json:"types"` // {} (Total 0) when there is no chunk
	// VehicleTypes is the same for the vehicle classes the map can spawn.
	VehicleTypes *TypesChunk `json:"vehicle_types"`
	Results      []Result    `json:"results"`
	// Feed holds the game events since the last sync, numbered by Seq.
	Feed []FeedEvent `json:"feed"`
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
	// ResendTypes asks the mod to send the class list again from the start: a chunk arrived that
	// does not follow the ones the hub has.
	ResendTypes bool `json:"resend_types,omitempty"`
}

// Normalise drops the parts a mod request lists as not included. A request
// without a Has list (from Go callers) is taken as it is.
func (r *SyncRequest) Normalise() {
	for i := range r.Vehicles {
		if r.Vehicles[i].Kind == "" {
			r.Vehicles[i].Kind = "car"
		}
	}
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
	if !has["feed"] {
		r.Feed = nil
	}
	if r.Types != nil && r.Types.Total == 0 {
		r.Types = nil
	}
	if r.VehicleTypes != nil && r.VehicleTypes.Total == 0 {
		r.VehicleTypes = nil
	}
}

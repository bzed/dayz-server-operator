// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// The vehicle list keeps the vehicles of the Expansion virtual garage apart from the ones in the
// world: they are not on the map, and their panel offers the garage actions instead.
func TestVehiclesFragmentListsStoredVehiclesApart(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	require.NoError(t, e.st.PlayerJoined(t.Context(), "alpha", "76561198000000001", "alice", time.Now()))
	guid, err := moderation.DeriveBIGUID("76561198000000001")
	require.NoError(t, err)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback", Health: 0.5, Fuel: 0.25},
		{ID: "g1.2.3.4", Type: "ExpansionOlga", Stored: true, StoredBy: guid,
			Owners: []string{guid}, X: 100.4, Z: 200.6},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles")
	live := strings.Index(body, "OffroadHatchback")
	at := strings.Index(body, "Stored in the Expansion garage")
	stored := strings.Index(body, "ExpansionOlga")
	require.Greater(t, at, -1, "no stored section: %s", body)
	require.Greater(t, stored, at, "the stored vehicle is not in its own section: %s", body)
	require.Greater(t, live, -1, "no live row: %s", body)
	require.Less(t, live, at, "the live table must come before the stored section")
	// The stored-by and the owners cells show the player the GUID resolved to, and link their page.
	require.Equal(t, 2, strings.Count(body, `href="/b/main/players/76561198000000001"`),
		"stored by and owners must link the player page: %s", body)
	require.Equal(t, 2, strings.Count(body, ">alice</a>"), "both cells show the name: %s", body)
	require.Equal(t, 2, strings.Count(body, `title="`+guid+`"`), "the GUID is the tooltip: %s", body)
	// The position link opens the map on the stored vehicle (which the map does not draw otherwise).
	require.Contains(t, body, `sv=g1.2.3.4`, "the position link does not name the stored vehicle")
	require.Contains(t, body, `label=ExpansionOlga&#43;%28stored&#43;by&#43;alice%29`)
	require.Contains(t, body, ">100 / 201</a>")
	// The live table keeps its columns and its panel button.
	require.Contains(t, body, `<th data-sort="num">Health</th>`)
	require.Contains(t, body, `/vehicles/1.2.3.4/panel`)
}

// A garage owner who never connected to these servers cannot be resolved: the GUID stays, as
// "unknown" with the GUID in the tooltip.
func TestVehiclesFragmentUnknownGarageOwner(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	stranger, err := moderation.DeriveBIGUID("76561198000000002")
	require.NoError(t, err)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "g1.2.3.4", Type: "ExpansionOlga", Stored: true, StoredBy: stranger, Owners: []string{stranger}},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles")
	require.Equal(t, 2, strings.Count(body, ">unknown</span>"), "both cells say unknown: %s", body)
	require.Equal(t, 2, strings.Count(body, `title="`+stranger+`"`), "the GUID is the tooltip: %s", body)
	require.NotContains(t, body, `href="/b/main/players/`, "no link without a known player: %s", body)
}

func TestVehiclesFragmentNotesABrokenStoredVehicle(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "g1.2.3.4", Type: "ExpansionOlga", Stored: true, StoredBy: "F-bFI9I7fZCOna700tjPMHdAujllgWJwcr6VGoYrJvE=", Broken: true},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles")
	require.Contains(t, body, "no storage file", "a broken stored vehicle cannot come back: %s", body)
}

func TestVehiclesFragmentShowsTheTerritoryOfAGarage(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "g9.9.9.9", Type: "ExpansionOlga", Stored: true, TerritoryID: 3},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles")
	require.Contains(t, body, "territory #3", body)
	require.Contains(t, body, "<td>-</td>", "no stored-by player and no owners")
}

func TestVehiclesFragmentWithoutStoredVehicles(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback", Health: 0.5, Fuel: 0.25},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles")
	require.NotContains(t, body, "Stored in the Expansion garage")
	require.Contains(t, body, `<th data-sort="num">Health</th>`, "the live table keeps its columns")
	require.Contains(t, body, `/vehicles/1.2.3.4/panel`, "the live table keeps its panel button")
}

func TestVehiclesPanelOffersTheGarageActionsForAStoredVehicle(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "g1.2.3.4", Type: "ExpansionOlga", Stored: true, StoredBy: "F-bFI9I7fZCOna700tjPMHdAujllgWJwcr6VGoYrJvE="},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles/g1.2.3.4/panel")
	for _, want := range []string{
		`hx-post="/b/main/i/alpha/vehicles/g1.2.3.4/retrieve"`,
		`hx-post="/b/main/i/alpha/vehicles/g1.2.3.4/spawnmove"`,
	} {
		require.Contains(t, body, want, "no garage action: %s", body)
	}
	assert.Equal(t, 2, strings.Count(body, `name="x"`), "both forms take a position: %s", body)
	assert.Equal(t, 2, strings.Count(body, `name="z"`), "both forms take a position: %s", body)
	assert.Equal(t, 2, strings.Count(body, `hx-target="#panel-flash"`), "both actions flash into the panel: %s", body)
	// A stored vehicle has no health, fuel or parts: the actions on a vehicle in the world make no sense.
	for _, no := range []string{"/repair", "/flip", "/delete"} {
		assert.NotContains(t, body, no, "the panel of a stored vehicle offers %s", no)
	}
}

func TestVehiclesPanelKeepsTheLiveActions(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "1.2.3.4", Kind: "car", Type: "OffroadHatchback"},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles/1.2.3.4/panel")
	require.Contains(t, body, `hx-post="/b/main/i/alpha/vehicles/1.2.3.4/repair"`, "panel: %s", body)
	require.Contains(t, body, `hx-post="/b/main/i/alpha/vehicles/1.2.3.4/flip"`, "panel: %s", body)
	require.NotContains(t, body, "/retrieve", "a vehicle in the world is not in the garage")
	require.NotContains(t, body, "/spawnmove", "a vehicle in the world has no garage spawn point")
}

func TestVehiclesPanelGarageActionsNeedTheTeleportPermission(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Vehicles: []admin.Vehicle{
		{ID: "g1.2.3.4", Type: "ExpansionOlga", Stored: true, StoredBy: "F-bFI9I7fZCOna700tjPMHdAujllgWJwcr6VGoYrJvE="},
	}})
	_, body, _ := e.get("/b/main/i/alpha/vehicles/g1.2.3.4/panel")
	require.NotContains(t, body, "/retrieve")
	require.NotContains(t, body, "/spawnmove")
	require.Contains(t, body, "no actions", "the panel says why there is nothing to do: %s", body)
}

// The panel forms and the map menu post to the same two routes: they reach the mod as
// storedvehicle_retrieve and storedvehicle_move.
func TestVehicleGarageActionsReachTheAPI(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, Features: []string{admin.FeatureGarage}})
	defer e.answerCommands(true, "done")()
	for _, path := range []string{
		"/b/main/i/alpha/vehicles/g1.2.3.4/retrieve",
		"/b/main/i/alpha/vehicles/g1.2.3.4/spawnmove",
	} {
		code, body := e.post(path, url.Values{"x": {"100"}, "z": {"200"}}, nil)
		require.Equal(t, 200, code, "%s: %s", path, body)
		require.Contains(t, body, "flash ok", path)
	}
	require.Equal(t, 1, e.commandsOf("storedvehicle_retrieve"), "the retrieve reached the mod")
	require.Equal(t, 1, e.commandsOf("storedvehicle_move"), "the spawn move reached the mod")
}

func TestVehicleGarageActionsNeedTheTeleportPermission(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, Features: []string{admin.FeatureGarage}})
	for _, path := range []string{
		"/b/main/i/alpha/vehicles/g1.2.3.4/retrieve",
		"/b/main/i/alpha/vehicles/g1.2.3.4/spawnmove",
	} {
		_, body := e.post(path, url.Values{"x": {"100"}, "z": {"200"}}, nil)
		require.Contains(t, body, "flash err", path)
		require.Contains(t, body, "lacks", path)
	}
}

func TestVehiclesMapLinkForStoredVehicles(t *testing.T) {
	query := func(v admin.Vehicle) url.Values {
		u, err := url.Parse(mapLink("main", "alpha", v))
		require.NoError(t, err)
		return u.Query()
	}
	got := query(admin.Vehicle{ID: "g1.2.3.4", Type: "ExpansionOlga", Stored: true,
		StoredBy: "F-bFI9I7fZCOna700tjPMHdAujllgWJwcr6VGoYrJvE=", StoredBySteamID: "76561198000000001", StoredByName: "alice"})
	require.Equal(t, "g1.2.3.4", got.Get("sv"), "the map needs sv to draw the one stored vehicle")
	require.Equal(t, "ExpansionOlga (stored by alice)", got.Get("label"))
	// Without a known player the label falls back to the GUID.
	got = query(admin.Vehicle{ID: "g2.2.2.2", Type: "ExpansionOlga", Stored: true, StoredBy: "F-bFI9I7fZCOna700tjPMHdAujllgWJwcr6VGoYrJvE="})
	require.Equal(t, "ExpansionOlga (stored by F-bFI9I7fZCOna700tjPMHdAujllgWJwcr6VGoYrJvE=)", got.Get("label"))
	// A garage of a territory has no player to name.
	got = query(admin.Vehicle{ID: "g9.9.9.9", Type: "ExpansionOlga", Stored: true, TerritoryID: 3})
	require.Equal(t, "ExpansionOlga", got.Get("label"))
	require.Equal(t, "g9.9.9.9", got.Get("sv"))
	// A vehicle in the world is not a stored one.
	got = query(admin.Vehicle{ID: "1.2.3.4", Type: "Sedan_02"})
	require.Empty(t, got.Get("sv"))
	require.Equal(t, "Sedan_02", got.Get("label"))
}

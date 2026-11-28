// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fullSync is a sync at every count limit, written the way the mod's JsonSerializer writes it: every
// field present, floats at full precision, Expansion vehicles with owners and a garage.
func fullSync(t *testing.T) string {
	t.Helper()
	const f = 7512.345703
	guid := strings.Repeat("A", 44)
	rep := func(n int, v any) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = v
		}
		return out
	}
	player := map[string]any{"steam_id": "76561198000000000", "name": "SomePlayerName_x16", "slot": 12, "ping": 45,
		"x": f, "y": f, "z": f, "yaw": f, "health": f, "blood": f, "shock": f, "alive": true, "unconscious": false}
	vehicle := map[string]any{"id": "1234567890.1234567890.12345678.123456789", "kind": "car", "type": "OffroadHatchback_White",
		"x": f, "y": f, "z": f, "yaw": f, "health": f, "fuel": f, "ruined": false, "engine": true, "occupants": []string{},
		"owner": "SomePlayerName_x16", "covered": false, "stored": false, "stored_by": guid, "territory_id": 123,
		"owners": []string{guid, guid, guid, guid}, "broken": false, "driver": "76561198000000000",
		"driver_name": "SomePlayerName_x16", "driven_ago": f}
	marker := map[string]any{"layer": "territories", "id": "territory_12345", "shape": "circle", "x": f, "y": f, "z": f,
		"radius": f, "points": []any{}, "icon": "flag", "color": "#ff8800", "label": "Some Territory Name", "ttl": 0,
		"visibility": "admin", "source": "expansion"}
	event := map[string]any{"id": "contaminated_area_12345", "name": "Contaminated Area", "category": "contaminated",
		"x": f, "y": f, "z": f, "radius": f, "source": "vanilla"}
	feed := map[string]any{"seq": 123456, "kind": "kill", "steam_id": "76561198000000000", "name": "SomePlayerName_x16",
		"ai": false, "target_steam_id": "76561198000000001", "target_name": "OtherPlayerName", "target_ai": false,
		"has_pos": true, "x": f, "y": f, "z": f, "weapon": "M4-A1 Assault Rifle", "distance": f, "object": "M4A1_Green",
		"cause": "", "channel": "", "text": ""}
	names := make([]string, maxSyncNames)
	for i := range names {
		names[i] = "Some_Long_ClassName_" + strings.Repeat("x", 20)
	}
	b, err := json.Marshal(map[string]any{"token": "tok-a", "protocol": 1, "mod_version": "1.2.3", "world": "chernarusplus",
		"players": rep(maxSyncPlayers, player), "vehicles": rep(maxSyncVehicles, vehicle), "markers": rep(maxSyncMarkers, marker),
		"events": rep(maxSyncEvents, event), "feed": rep(maxSyncFeed, feed),
		"types": map[string]any{"hash": "h", "offset": 0, "total": maxSyncNames, "names": names}})
	require.NoError(t, err)
	return string(b)
}

func TestTheBodyLimitHoldsAFullSync(t *testing.T) {
	body := fullSync(t)
	t.Logf("a sync at every count limit is %d bytes", len(body))
	require.Less(t, len(body), maxSyncBody, "the count limits refuse a sync before the byte limit does")
}

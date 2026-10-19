// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/geoip"
	"github.com/bzed/dayz-server-operator/internal/geoip/geoiptest"
)

func geoEnv(t *testing.T) *env {
	t.Helper()
	e, m := newBanEnv(t)
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true, Players: []admin.Player{{SteamID: steamA, Name: "alice", Country: "ZZ"}, {SteamID: "76561198000000002", Name: "bob"}}})
	seenAlice(t, m, "198.51.100.7")
	e.srv.GeoIP = geoiptest.Legacy(t, map[string]string{"198.51.100.0/24": "DE"})
	return e
}

func TestPlayerListCarriesTheCountry(t *testing.T) {
	e := geoEnv(t)
	code, b := e.do("GET", "/api/v1/instances/alpha/players", e.token(RoleViewer, nil, false), "")
	var ps []admin.Player
	err := json.Unmarshal(b, &ps)
	require.NoError(t, err)
	require.Equal(t, 200, code)
	require.Len(t, ps, 2)
	require.Equal(t, "DE", ps[0].Country, "countries = %q %q (what the mod sent must be overwritten)", ps[0].Country, ps[1].Country)
	require.Empty(t, ps[1].Country, "countries = %q %q (what the mod sent must be overwritten)", ps[0].Country, ps[1].Country)
}

func TestPlayerListWithoutGeoIP(t *testing.T) {
	e := geoEnv(t)
	e.srv.GeoIP = nil
	_, b := e.do("GET", "/api/v1/instances/alpha/players", e.token(RoleViewer, nil, false), "")
	var ps []admin.Player
	err := json.Unmarshal(b, &ps)
	require.NoError(t, err)
	require.Contains(t, []string{"ZZ", ""}, ps[0].Country)
}

func TestGeoLookupOfAPlayer(t *testing.T) {
	e := geoEnv(t)
	path := "/api/v1/instances/alpha/players/" + steamA + "/geo"
	for _, role := range []Role{RoleViewer, RoleOperator} {
		code, b := e.do("GET", path, e.token(role, nil, false), "")
		var info geoip.Info
		err := json.Unmarshal(b, &info)
		require.NoError(t, err, "%s: %d %s", role, code, b)
		require.Equal(t, 200, code, "%s: %d %s", role, code, b)
		require.Equal(t, "DE", info.Country, "%s: %d %s", role, code, b)
	}
	// A player never seen has nothing, and so does a server with no database.
	_, b := e.do("GET", "/api/v1/instances/alpha/players/76561198000000002/geo", e.token(RoleOperator, nil, false), "")
	require.Equal(t, "{}\n", string(b))
	e.srv.GeoIP = nil
	_, b = e.do("GET", path, e.token(RoleOperator, nil, false), "")
	require.Equal(t, "{}\n", string(b))
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"testing"

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
	if err := json.Unmarshal(b, &ps); err != nil || code != 200 || len(ps) != 2 {
		t.Fatalf("%d %s %v", code, b, err)
	}
	if ps[0].Country != "DE" || ps[1].Country != "" {
		t.Fatalf("countries = %q %q (what the mod sent must be overwritten)", ps[0].Country, ps[1].Country)
	}
}

func TestPlayerListWithoutGeoIP(t *testing.T) {
	e := geoEnv(t)
	e.srv.GeoIP = nil
	_, b := e.do("GET", "/api/v1/instances/alpha/players", e.token(RoleViewer, nil, false), "")
	var ps []admin.Player
	if err := json.Unmarshal(b, &ps); err != nil || ps[0].Country != "ZZ" && ps[0].Country != "" {
		t.Fatalf("%s %v", b, err)
	}
}

func TestGeoLookupOfAPlayer(t *testing.T) {
	e := geoEnv(t)
	path := "/api/v1/instances/alpha/players/" + steamA + "/geo"
	for _, role := range []Role{RoleViewer, RoleOperator} {
		code, b := e.do("GET", path, e.token(role, nil, false), "")
		var info geoip.Info
		if err := json.Unmarshal(b, &info); err != nil || code != 200 || info.Country != "DE" {
			t.Fatalf("%s: %d %s", role, code, b)
		}
	}
	// A player never seen has nothing, and so does a server with no database.
	if _, b := e.do("GET", "/api/v1/instances/alpha/players/76561198000000002/geo", e.token(RoleOperator, nil, false), ""); string(b) != "{}\n" {
		t.Fatalf("unknown player: %q", b)
	}
	e.srv.GeoIP = nil
	if _, b := e.do("GET", path, e.token(RoleOperator, nil, false), ""); string(b) != "{}\n" {
		t.Fatalf("no database: %q", b)
	}
}

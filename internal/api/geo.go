// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"

	"github.com/bzed/dayz-server-operator/internal/geoip"
)

// connectionIP is the address a player is connected from, "" when it is not known or there is no ban
// database to remember it in.
func (s *Server) connectionIP(ctx context.Context, instance, steamID string) string {
	if s.Moderation == nil {
		return ""
	}
	ip, _, err := s.Moderation.Store().CurrentIP(ctx, instance, steamID)
	if err != nil {
		return ""
	}
	return ip
}

// players answers the player list, each with the country of the address they connected from.
func (s *Server) players(w http.ResponseWriter, r *http.Request, c *call) {
	ps := c.hub.Players()
	if s.GeoIP.Available() {
		inst := r.PathValue("instance")
		for i := range ps {
			ps[i].Country = s.GeoIP.Lookup(s.connectionIP(r.Context(), inst, ps[i].SteamID)).Country
		}
	}
	writeJSON(w, 200, ps)
}

// geo answers where a player is connecting from. The country is for everyone who sees the player list;
// the city, location and autonomous system narrow the address down and need the player.ip permission.
func (s *Server) geo(w http.ResponseWriter, r *http.Request, c *call) {
	var info geoip.Info
	if s.GeoIP.Available() {
		info = s.GeoIP.Lookup(s.connectionIP(r.Context(), r.PathValue("instance"), r.PathValue("steamid")))
		if !c.who.Token.Role.Can(PermPlayerIP) {
			info = info.CountryOnly()
		}
	}
	writeJSON(w, 200, info)
}

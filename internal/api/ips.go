// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// seesIPsOn reports whether the caller may see stored IP addresses of an instance: those in sightings,
// evidence and the history of a player's addresses. An empty instance asks about any.
func seesIPsOn(c *call, instance string) bool {
	if instance == "" {
		return c.who.CanAny(PermIPHistory)
	}
	return c.who.Can(instance, PermIPHistory)
}

// seesBanIPs reports whether the addresses of a ban may be shown: the permission has to hold on every
// instance the ban applies to, on all of them for a global one.
func seesBanIPs(c *call, b moderation.Ban) bool {
	return canEvery(c.who, b.Scope == moderation.ScopeGlobal, b.Instances, PermIPHistory)
}

// maskBan hides the addresses of a ban's targets from a caller who may not see them. Each keeps its Ref,
// which is enough to keep it when the ban is edited.
func maskBan(c *call, b moderation.Ban) moderation.Ban {
	if seesBanIPs(c, b) {
		return b
	}
	b.Targets = slices.Clone(b.Targets)
	for i := range b.Targets {
		if b.Targets[i].Kind == moderation.TargetIP {
			b.Targets[i].Value = ""
		}
	}
	return b
}

func maskBans(c *call, bans []moderation.Ban) []moderation.Ban {
	for i := range bans {
		bans[i] = maskBan(c, bans[i])
	}
	return bans
}

func maskEvidence(c *call, e moderation.Evidence) moderation.Evidence {
	if !seesIPsOn(c, e.Instance) {
		e.IP = ""
	}
	return e
}

// maskHistory removes the addresses from the old and new versions of a ban kept in its history.
func maskHistory(c *call, b moderation.Ban, h []moderation.HistoryEntry) []moderation.HistoryEntry {
	if seesBanIPs(c, b) {
		return h
	}
	for i := range h {
		var v any
		if json.Unmarshal(h[i].Change, &v) != nil {
			continue
		}
		dropIPValues(v)
		if raw, err := json.Marshal(v); err == nil {
			h[i].Change = raw
		}
	}
	return h
}

// dropIPValues deletes "value" from every {"kind": "ip", ...} object in a decoded JSON value.
func dropIPValues(v any) {
	switch x := v.(type) {
	case map[string]any:
		if x["kind"] == string(moderation.TargetIP) {
			delete(x, "value")
		}
		for _, y := range x {
			dropIPValues(y)
		}
	case []any:
		for _, y := range x {
			dropIPValues(y)
		}
	}
}

// Connection is the address a player is connected from now.
type Connection struct {
	IP    string    `json:"ip"`
	Since time.Time `json:"since"`
}

// connection answers the address of the current connection: operational data, for the player.ip permission.
func (s *Server) connection(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	ip, since, err := st.CurrentIP(r.Context(), r.PathValue("instance"), r.PathValue("steamid"))
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, 200, Connection{IP: ip, Since: since})
}

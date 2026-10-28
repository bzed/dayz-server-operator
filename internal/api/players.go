// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/steamweb"
)

// Lookups asks BattlEye and Steam about one player now (internal/lookups.Refresher). It returns
// moderation.ErrLookupTooSoon for a player looked up a few minutes ago.
type Lookups interface {
	LookupNow(ctx context.Context, steamID string) error
}

// PlayerDetail is the answer of GET /players/{steamid}: the record of the player database plus what the
// moderation database says about the same player.
type PlayerDetail struct {
	moderation.PlayerRecord
	// Bans are the bans of ours that name the player, in any state.
	Bans []moderation.Ban `json:"bans"`
	// Identities are where the player was seen; the addresses are empty without ip.history.
	Identities []moderation.Sighting `json:"identities"`
	// FriendsSeen are the player's public Steam friends who also played on these servers (key only).
	FriendsSeen []moderation.PlayerSummary `json:"friends_seen"`
	ProfileURL  string                     `json:"profile_url"`
}

// maxFriendsSeen caps the friends in an answer.
const maxFriendsSeen = 10

// restrict cuts a summary down to the instances whose player history the caller may read.
func restrict(who Principal, p *moderation.PlayerSummary) {
	if all, _ := who.Scope(PermPlayerHistory); !all {
		p.Instances = slices.DeleteFunc(slices.Clone(p.Instances), func(i string) bool { return !who.Can(i, PermPlayerHistory) })
	}
}

func visibleTo(who Principal, p moderation.PlayerSummary) bool {
	if all, _ := who.Scope(PermPlayerHistory); all {
		return true
	}
	return slices.ContainsFunc(p.Instances, func(i string) bool { return who.Can(i, PermPlayerHistory) })
}

func (s *Server) listPlayers(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	if inst := q.Get("instance"); inst != "" && !c.who.Can(inst, PermPlayerHistory) {
		fail(w, http.StatusNotFound, "unknown instance %q", inst)
		return
	}
	sort := q.Get("sort")
	if _, ok := moderation.PlayerSorts[sort]; sort != "" && !ok {
		fail(w, http.StatusBadRequest, "unknown sort %q (name, last, first, be, steam or here)", sort)
		return
	}
	all, only := c.who.Scope(PermPlayerHistory)
	if !all && len(only) == 0 { // the filter takes no list to mean everything
		writeJSON(w, http.StatusOK, struct {
			Players []moderation.PlayerSummary `json:"players"`
			Total   int                        `json:"total"`
		}{[]moderation.PlayerSummary{}, 0})
		return
	}
	f := moderation.PlayerFilter{Sort: sort, Desc: q.Get("dir") == "desc", Query: q.Get("q"), Instance: q.Get("instance"), Flagged: q.Get("flagged") == "1" || q.Get("flagged") == "true", Online: q.Get("online") == "1" || q.Get("online") == "true",
		Limit: queryInt(r, "limit", 50, 200), Offset: max(queryInt(r, "offset", 0, 1<<30), 0), Only: only, Now: s.now()}
	list, total, err := st.Players(r.Context(), f)
	if err != nil {
		storeFail(w, err)
		return
	}
	out := []moderation.PlayerSummary{}
	for _, p := range list {
		restrict(c.who, &p)
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, struct {
		Players []moderation.PlayerSummary `json:"players"`
		Total   int                        `json:"total"`
	}{out, total})
}

// player loads the record of {steamid}, 404 for a player the token may not see.
func (s *Server) player(w http.ResponseWriter, r *http.Request, c *call) (*moderation.Store, moderation.PlayerRecord, bool) {
	st, ok := s.store(w)
	if !ok {
		return nil, moderation.PlayerRecord{}, false
	}
	id := r.PathValue("steamid")
	if !moderation.ValidSteamID(id) {
		fail(w, http.StatusBadRequest, "invalid Steam ID %q", id)
		return nil, moderation.PlayerRecord{}, false
	}
	rec, err := st.Player(r.Context(), id)
	if err == nil && !visibleTo(c.who, rec.PlayerSummary) {
		err = moderation.ErrNotFound
	}
	if err != nil {
		storeFail(w, err)
		return nil, moderation.PlayerRecord{}, false
	}
	return st, rec, true
}

// detail assembles the answer for a record.
func (s *Server) detail(r *http.Request, c *call, st *moderation.Store, rec moderation.PlayerRecord) (PlayerDetail, error) {
	d := PlayerDetail{PlayerRecord: rec, Bans: []moderation.Ban{}, Identities: []moderation.Sighting{}, FriendsSeen: []moderation.PlayerSummary{}, ProfileURL: steamweb.ProfileURL(rec.SteamID)}
	restrict(c.who, &d.PlayerSummary)
	d.PerInstance = slices.DeleteFunc(slices.Clone(d.PerInstance), func(x moderation.PlayerInstance) bool { return !c.who.Can(x.Instance, PermPlayerHistory) })
	d.Sessions = slices.DeleteFunc(slices.Clone(d.Sessions), func(x moderation.Session) bool { return !c.who.Can(x.Instance, PermPlayerHistory) })

	bans, err := st.ListBans(r.Context(), moderation.BanFilter{Query: rec.SteamID, Now: s.now()})
	if err != nil {
		return d, err
	}
	for _, b := range bans {
		named := slices.ContainsFunc(b.Targets, func(t moderation.Target) bool {
			return (t.Kind == moderation.TargetSteam && t.Value == rec.SteamID) || (t.Kind == moderation.TargetGUID && t.Value == rec.GUID)
		})
		if named && canSee(c.who, b) {
			d.Bans = append(d.Bans, maskBan(c, b))
		}
	}
	ids, err := st.Identities(r.Context(), rec.SteamID)
	if err != nil {
		return d, err
	}
	shown := false
	for _, x := range ids {
		if !c.who.Can(x.Instance, PermPlayerHistory) {
			continue
		}
		if seesIPsOn(c, x.Instance) {
			shown = shown || x.IP != ""
		} else {
			x.IP = ""
		}
		d.Identities = append(d.Identities, x)
	}
	if shown { // an address history is personal data: who read it is recorded, without the addresses
		_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Action: "ip.history", Target: rec.SteamID, OK: true}) // the read goes on if the line cannot be written
	}
	friends, err := st.FriendsSeen(r.Context(), rec.Steam.FriendIDs)
	if err != nil {
		return d, err
	}
	for _, f := range friends {
		if visibleTo(c.who, f) && len(d.FriendsSeen) < maxFriendsSeen {
			restrict(c.who, &f)
			d.FriendsSeen = append(d.FriendsSeen, f)
		}
	}
	d.Steam.FriendIDs = nil // the whole list is large and not for the reader; the friends seen here are
	return d, nil
}

func (s *Server) getPlayer(w http.ResponseWriter, r *http.Request, c *call) {
	st, rec, ok := s.player(w, r, c)
	if !ok {
		return
	}
	d, err := s.detail(r, c, st, rec)
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// lookupPlayer asks BattlEye and Steam about the player now and answers the fresh record.
func (s *Server) lookupPlayer(w http.ResponseWriter, r *http.Request, c *call) {
	st, rec, ok := s.player(w, r, c)
	if !ok {
		return
	}
	if s.Lookups == nil {
		fail(w, http.StatusServiceUnavailable, "lookups are not available on this dzo serve")
		return
	}
	err := s.Lookups.LookupNow(r.Context(), rec.SteamID)
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Action: "player.lookup", Target: rec.SteamID, OK: err == nil}) // the lookup itself is what matters
	switch {
	case errors.Is(err, moderation.ErrLookupTooSoon):
		fail(w, http.StatusTooManyRequests, "this player was looked up a moment ago; try again in a few minutes")
		return
	case err != nil:
		fail(w, http.StatusBadGateway, "lookup failed: %v", err)
		return
	}
	if rec, err = st.Player(r.Context(), rec.SteamID); err != nil {
		storeFail(w, err)
		return
	}
	d, err := s.detail(r, c, st, rec)
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

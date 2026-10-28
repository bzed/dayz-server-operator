// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// shortBan is the longest ban a moderator may give.
const shortBan = 7 * 24 * time.Hour

// Moderation is what the ban routes need from dzo serve (internal/moderation.Service).
type Moderation interface {
	Store() *moderation.Store
	// Changed syncs the instances a ban (and its previous version) applies to and returns "ok" or
	// "pending: <why>" per instance.
	Changed(ctx context.Context, b moderation.Ban, prev *moderation.Ban) map[string]string
	// Kick removes an online player from an instance; moderation.ErrNotOnline when they are not there.
	Kick(ctx context.Context, instance, steamID, reason string) error
}

// BanResult answers a ban change: the ban and the sync state per instance.
type BanResult struct {
	Ban  moderation.Ban    `json:"ban"`
	Sync map[string]string `json:"sync,omitempty"`
}

// RevokeBody is the body of POST /bans/{id}/revoke.
type RevokeBody struct {
	Reason string `json:"reason"`
}

func (s *Server) store(w http.ResponseWriter) (*moderation.Store, bool) {
	if s.Moderation == nil {
		fail(w, http.StatusServiceUnavailable, "bans are not available: this dzo serve has no ban database")
		return nil, false
	}
	return s.Moderation.Store(), true
}

// storeFail answers an error of the store.
func storeFail(w http.ResponseWriter, err error) {
	var inv *moderation.InvalidError
	switch {
	case errors.As(err, &inv):
		fail(w, http.StatusBadRequest, "%s", inv.Msg)
	case errors.Is(err, moderation.ErrNotFound):
		fail(w, http.StatusNotFound, "not found")
	default:
		fail(w, http.StatusInternalServerError, "database error")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		fail(w, http.StatusBadRequest, "invalid id %q", r.PathValue("id"))
		return 0, false
	}
	return id, true
}

// canSee reports whether a principal may see a ban: a global one if it may view bans anywhere, an instance
// one if it may on at least one of its instances.
func canSee(who Principal, b moderation.Ban) bool {
	if b.Scope == moderation.ScopeGlobal {
		return who.CanAny(PermBanView)
	}
	return slices.ContainsFunc(b.Instances, func(i string) bool { return who.Can(i, PermBanView) })
}

// canEvery reports whether a principal holds a permission on every instance of a ban input; for a global ban
// that is every instance there is, and for one without instances at least one.
func canEvery(who Principal, global bool, instances []string, perm string) bool {
	switch {
	case global:
		return who.CanAll(perm)
	case len(instances) == 0:
		return who.CanAny(perm)
	}
	return !slices.ContainsFunc(instances, func(i string) bool { return !who.Can(i, perm) })
}

// banRights checks a ban's input against the caller's role and token. code is 403 for missing rights
// and 400 for instances it cannot use.
func (s *Server) banRights(c *call, in moderation.BanInput) (code int, err error) {
	global := in.Scope == moderation.ScopeGlobal
	switch {
	case global:
		if !c.who.CanAll(PermBanGlobal) {
			return http.StatusForbidden, fmt.Errorf("global bans need the admin role and a token for every instance")
		}
	default:
		for _, i := range in.Instances {
			if _, ok := s.Hubs.Hub(i); !ok || !c.who.Allowed(i) {
				return http.StatusBadRequest, fmt.Errorf("unknown instance %q", i)
			}
		}
		if !canEvery(c.who, false, in.Instances, PermBanInstance) {
			return http.StatusForbidden, fmt.Errorf("you may not ban on every one of these instances")
		}
	}
	if (in.ExpiresAt == nil || in.ExpiresAt.Sub(s.now()) > shortBan) && !canEvery(c.who, global, in.Instances, PermBanLong) {
		return http.StatusForbidden, fmt.Errorf("bans longer than 7 days need the operator role")
	}
	if len(in.IPs)+len(in.IPRefs) > 0 && !canEvery(c.who, global, in.Instances, PermBanIP) {
		return http.StatusForbidden, fmt.Errorf("IP bans need the operator role")
	}
	return 0, nil
}

func targetsOf(b moderation.Ban) string {
	var v []string
	for _, t := range b.Targets {
		switch {
		case t.Kind == moderation.TargetIP:
			v = append(v, "ip") // an address stays out of the audit log, which outlives any retention of sightings
		case !t.Derived:
			v = append(v, t.Value)
		}
	}
	return strings.Join(v, ",")
}

func (s *Server) logBan(c *call, action string, b moderation.Ban, ok bool, detail string) {
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Instance: strings.Join(b.Instances, ","), Action: action,
		Target: targetsOf(b), Detail: detail, OK: ok}) // the ban itself is stored; a failed audit line must not undo it
}

func (s *Server) listBans(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	bans, err := st.ListBans(r.Context(), moderation.BanFilter{State: q.Get("state"), Instance: q.Get("instance"),
		Query: q.Get("q"), Limit: queryInt(r, "limit", 100, 1000), Now: s.now()})
	if err != nil {
		storeFail(w, err)
		return
	}
	out := []moderation.Ban{}
	for _, b := range bans {
		if canSee(c.who, b) {
			out = append(out, b)
		}
	}
	writeJSON(w, 200, maskBans(c, out))
}

// loadBan reads the {id} ban, answering 404 for one the caller may not see.
func (s *Server) loadBan(w http.ResponseWriter, r *http.Request, c *call) (*moderation.Store, moderation.Ban, bool) {
	st, ok := s.store(w)
	if !ok {
		return nil, moderation.Ban{}, false
	}
	id, ok := pathID(w, r)
	if !ok {
		return nil, moderation.Ban{}, false
	}
	b, err := st.GetBan(r.Context(), id)
	if err == nil && !canSee(c.who, b) {
		err = moderation.ErrNotFound
	}
	if err != nil {
		storeFail(w, err)
		return nil, moderation.Ban{}, false
	}
	return st, b, true
}

func (s *Server) getBan(w http.ResponseWriter, r *http.Request, c *call) {
	if _, b, ok := s.loadBan(w, r, c); ok {
		writeJSON(w, 200, maskBan(c, b))
	}
}

func (s *Server) banHistory(w http.ResponseWriter, r *http.Request, c *call) {
	st, b, ok := s.loadBan(w, r, c)
	if !ok {
		return
	}
	h, err := st.History(r.Context(), b.ID)
	if err != nil {
		storeFail(w, err)
		return
	}
	writeJSON(w, 200, maskHistory(c, b, h))
}

func (s *Server) createBan(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	var in moderation.BanInput
	if !decode(w, r, &in) {
		return
	}
	if code, err := s.banRights(c, in); err != nil {
		fail(w, code, "%v", err)
		return
	}
	b, err := st.CreateBan(r.Context(), c.actor, in, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	s.logBan(c, "ban.create", b, true, fmt.Sprintf("ban #%d %s until %s: %s", b.ID, b.Scope, moderation.FormatExpiry(b.ExpiresAt), b.Reason))
	writeJSON(w, http.StatusCreated, BanResult{Ban: maskBan(c, b), Sync: s.Moderation.Changed(r.Context(), b, nil)})
}

// mayChange: the ban's creator (who must still be allowed to make it) or whoever may revoke any ban.
func (s *Server) mayChange(c *call, b moderation.Ban) (int, error) {
	if c.who.CanAll(PermBanRevokeAny) {
		return 0, nil
	}
	if b.CreatedBy != c.actor {
		return http.StatusForbidden, fmt.Errorf("only %s or an admin may change ban %d", b.CreatedBy, b.ID)
	}
	return s.banRights(c, b.Input())
}

func (s *Server) updateBan(w http.ResponseWriter, r *http.Request, c *call) {
	st, old, ok := s.loadBan(w, r, c)
	if !ok {
		return
	}
	var in moderation.BanInput
	if !decode(w, r, &in) {
		return
	}
	if code, err := s.mayChange(c, old); err != nil {
		fail(w, code, "%v", err)
		return
	}
	if code, err := s.banRights(c, in); err != nil {
		fail(w, code, "%v", err)
		return
	}
	b, err := st.UpdateBan(r.Context(), old.ID, c.actor, in, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	s.logBan(c, "ban.edit", b, true, fmt.Sprintf("ban #%d", b.ID))
	writeJSON(w, 200, BanResult{Ban: maskBan(c, b), Sync: s.Moderation.Changed(r.Context(), b, &old)})
}

func (s *Server) revokeBan(w http.ResponseWriter, r *http.Request, c *call) {
	st, old, ok := s.loadBan(w, r, c)
	if !ok {
		return
	}
	var body RevokeBody
	if !decode(w, r, &body) {
		return
	}
	if code, err := s.mayChange(c, old); err != nil {
		fail(w, code, "%v", err)
		return
	}
	b, err := st.RevokeBan(r.Context(), old.ID, c.actor, body.Reason, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	s.logBan(c, "ban.revoke", b, true, fmt.Sprintf("ban #%d: %s", b.ID, body.Reason))
	writeJSON(w, 200, BanResult{Ban: maskBan(c, b), Sync: s.Moderation.Changed(r.Context(), b, &old)})
}

// KickBody is the body of POST /instances/{instance}/players/{steamid}/kick.
type KickBody struct {
	Reason string `json:"reason"`
}

// kick removes a player; the reason is shown to them, so it is as short as a ban message.
func (s *Server) kick(w http.ResponseWriter, r *http.Request, c *call) {
	if s.Moderation == nil {
		fail(w, http.StatusServiceUnavailable, "kicking is not available: this dzo serve has no BattlEye connection")
		return
	}
	var b KickBody
	if !decode(w, r, &b) {
		return
	}
	reason := strings.TrimSpace(b.Reason)
	switch {
	case reason == "":
		fail(w, http.StatusBadRequest, "a kick needs a reason, which the player is shown")
		return
	case len(reason) > moderation.MaxMessage:
		fail(w, http.StatusBadRequest, "the reason is %d bytes, BattlEye shows at most %d", len(reason), moderation.MaxMessage)
		return
	}
	id := r.PathValue("steamid")
	entry := admin.AuditEntry{Actor: c.actor, Source: c.src, Instance: c.hub.Name, Action: "player.kick", Target: id, Detail: reason}
	err := s.Moderation.Kick(r.Context(), c.hub.Name, id, reason)
	entry.OK = err == nil
	if err != nil {
		entry.Result = err.Error()
	}
	_ = s.Audit.Log(entry) // the kick happened, or not, either way
	var inv *moderation.InvalidError
	switch {
	case err == nil:
		writeJSON(w, 200, ActionResult{OK: true})
	case errors.Is(err, moderation.ErrNotOnline):
		fail(w, http.StatusNotFound, "the player is not online")
	case errors.As(err, &inv):
		fail(w, http.StatusBadRequest, "%s", inv.Msg)
	default:
		fail(w, http.StatusBadGateway, "the kick failed: %v", err)
	}
}

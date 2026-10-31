// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"maps"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const maxPersonName = 64

// errOwnAccess is answered when someone tries to change their own access.
const errOwnAccess = "you cannot change your own access; another admin or dzo user on the serve host can"

// UserBody is the body of PUT /users/{steamid}.
type UserBody struct {
	Name     string             `json:"name"`
	Disabled bool               `json:"disabled"`
	Grants   []moderation.Grant `json:"grants"`
}

// cleanName removes control characters from a display name and trims it.
func cleanName(v string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, v))
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ *call) {
	people, err := s.Accounts.People(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	if people == nil {
		people = []moderation.Person{}
	}
	writeJSON(w, http.StatusOK, people)
}

// isSelf reports whether the request is about the person making it.
func isSelf(c *call, steamID string) bool { return c.who.User != nil && c.who.User.SteamID == steamID }

// grantMap turns grants into instance -> role, for comparing.
func grantMap(gs []moderation.Grant) map[string]string {
	m := make(map[string]string, len(gs))
	for _, g := range gs {
		m[g.Instance] = g.Role
	}
	return m
}

// grantsText writes grants for the audit log, sorted.
func grantsText(gs []moderation.Grant) string {
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		parts = append(parts, g.Instance+"="+g.Role)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (s *Server) putUser(w http.ResponseWriter, r *http.Request, c *call) {
	id := r.PathValue("steamid")
	var body UserBody
	if !decode(w, r, &body) {
		return
	}
	name := cleanName(body.Name)
	if utf8.RuneCountInString(name) > maxPersonName {
		fail(w, http.StatusBadRequest, "the name is longer than %d characters", maxPersonName)
		return
	}
	if !moderation.ValidSteamID(id) {
		fail(w, http.StatusBadRequest, "%q is not a Steam ID", id)
		return
	}
	if isSelf(c, id) {
		old, err := s.Accounts.Person(r.Context(), id)
		if err != nil {
			fail(w, http.StatusInternalServerError, "database error")
			return
		}
		if body.Disabled != old.Disabled || !maps.Equal(grantMap(body.Grants), grantMap(old.Grants)) {
			fail(w, http.StatusConflict, errOwnAccess)
			return
		}
	}
	p := moderation.Person{SteamID: id, Name: name, Disabled: body.Disabled, Grants: body.Grants}
	if err := s.Accounts.PutPerson(r.Context(), p, s.now()); err != nil {
		var inv *moderation.InvalidError
		if errors.As(err, &inv) {
			fail(w, http.StatusBadRequest, "%s", inv.Msg)
			return
		}
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	detail := "grants: " + grantsText(p.Grants)
	if p.Disabled {
		detail += "; disabled"
	}
	s.userAudit(c, "users.put", id, detail)
	got, err := s.Accounts.People(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	for _, x := range got {
		if x.SteamID == id {
			writeJSON(w, http.StatusOK, x)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userAudit(c *call, action, target, detail string) {
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Action: action, Target: target, Detail: detail, OK: true}) // the change is made either way
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, c *call) {
	id := r.PathValue("steamid")
	if isSelf(c, id) {
		fail(w, http.StatusConflict, errOwnAccess)
		return
	}
	switch err := s.Accounts.DeletePerson(r.Context(), id); {
	case errors.Is(err, moderation.ErrNotFound):
		fail(w, http.StatusNotFound, "no such person")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.userAudit(c, "users.delete", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resetUser(w http.ResponseWriter, r *http.Request, c *call) {
	id := r.PathValue("steamid")
	if isSelf(c, id) {
		fail(w, http.StatusConflict, errOwnAccess)
		return
	}
	switch err := s.Accounts.ResetCredentials(r.Context(), id); {
	case errors.Is(err, moderation.ErrNotFound):
		fail(w, http.StatusNotFound, "no such person")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "database error")
		return
	}
	s.userAudit(c, "users.reset", id, "password, second factors and sessions removed")
	w.WriteHeader(http.StatusNoContent)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// usersView is the users page.
type usersView struct {
	People []userRow
	Roles  []string
	Note   string
	Error  string
}

type userRow struct {
	moderation.Person
	GrantsText string
	Self       bool // the person looking at the page: the API refuses changes to one's own access, so none is offered
}

var userRoles = []string{"viewer", "moderator", "operator", "admin"}

func grantsText(gs []moderation.Grant) string {
	parts := make([]string, len(gs))
	for i, g := range gs {
		parts[i] = g.Instance + "=" + g.Role
	}
	return strings.Join(parts, ", ")
}

// parseGrants reads "alpha=operator, *=viewer" (commas, spaces or lines between the grants).
func parseGrants(text string) ([]moderation.Grant, error) {
	var out []moderation.Grant
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == ';' }) {
		inst, role, ok := strings.Cut(f, "=")
		if !ok || inst == "" || role == "" {
			return nil, fmt.Errorf("%q is not instance=role (for example alpha=operator or *=viewer)", f)
		}
		out = append(out, moderation.Grant{Instance: inst, Role: role})
	}
	return out, nil
}

// requireManage answers the request itself unless the person may manage users.
func (s *Server) requireManage(w http.ResponseWriter, r *http.Request) bool {
	if holds(r, api.PermUsersManage) {
		return true
	}
	s.renderCode(w, http.StatusForbidden, "loginmessage", loginView{Title: "No access", Message: "Managing people needs the admin role on every instance.", CSRF: csrfFor(sessionFrom(r.Context()).secret), LoggedIn: true, Version: s.opts.Version})
	return false
}

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request, note, errMsg string) {
	if !s.requireManage(w, r) {
		return
	}
	b := s.backends[0]
	v := usersView{Roles: userRoles, Note: note, Error: errMsg}
	people, err := s.client(b, r).Users(r.Context())
	if err != nil {
		v.Error = err.Error()
	}
	self := ""
	if u := infoOf(r).User; u != nil {
		self = u.SteamID
	}
	for _, p := range people {
		v.People = append(v.People, userRow{Person: p, GrantsText: grantsText(p.Grants), Self: p.SteamID == self})
	}
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	s.renderCode(w, status, "users", s.page(r, "People", b.Name, "", "users", v))
}

func (s *Server) usersGet(w http.ResponseWriter, r *http.Request) { s.usersPage(w, r, "", "") }

// usersPut creates or updates a person: the form of the add row (steam_id in the form) or of a row (in the path).
func (s *Server) usersPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireManage(w, r) {
		return
	}
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	id := r.PathValue("steamid")
	if id == "" {
		id = strings.TrimSpace(r.PostFormValue("steam_id"))
	}
	grants, err := parseGrants(r.PostFormValue("grants"))
	if err != nil {
		s.usersPage(w, r, "", err.Error())
		return
	}
	body := api.UserBody{Name: strings.TrimSpace(r.PostFormValue("name")), Disabled: r.PostFormValue("disabled") != "", Grants: grants}
	if _, err := s.client(s.backends[0], r).PutUser(r.Context(), id, body); err != nil {
		s.usersPage(w, r, "", err.Error())
		return
	}
	s.usersPage(w, r, "Saved "+id+".", "")
}

// usersAct runs delete or reset for the person in the path.
func (s *Server) usersAct(note string, do func(s *Server, r *http.Request, id string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireManage(w, r) {
			return
		}
		if !s.csrfOK(r) {
			http.Error(w, "bad CSRF token", http.StatusForbidden)
			return
		}
		id := r.PathValue("steamid")
		if err := do(s, r, id); err != nil {
			s.usersPage(w, r, "", err.Error())
			return
		}
		s.usersPage(w, r, id+": "+note, "")
	}
}

// usersRoutes registers the users page (inside the login check).
func (s *Server) usersRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /users", s.usersGet)
	mux.HandleFunc("POST /users", s.usersPut)
	mux.HandleFunc("POST /users/{steamid}", s.usersPut)
	mux.HandleFunc("POST /users/{steamid}/delete", s.usersAct("deleted.", func(s *Server, r *http.Request, id string) error {
		return s.client(s.backends[0], r).DeleteUser(r.Context(), id)
	}))
	mux.HandleFunc("POST /users/{steamid}/reset", s.usersAct("password, second factors and sessions removed; the person signs in through Steam again.", func(s *Server, r *http.Request, id string) error {
		return s.client(s.backends[0], r).ResetUser(r.Context(), id)
	}))
}

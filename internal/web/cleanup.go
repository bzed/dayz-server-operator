// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
)

const (
	maxCleanupDays = 3650
	maxCleanupIDs  = 500
	errBadDays     = "days must be a whole number from 1 to 3650"
)

// cleanupView is the cleanup step: the choice of a threshold and, once one is chosen, the preview.
type cleanupView struct {
	Backend, Instance string
	Days              int // 0: no threshold chosen yet
	Err               string
	Items             []admin.Vehicle
	Since             *time.Time // the oldest first_seen of a live vehicle
	Young             bool       // dzo has tracked the instance for less than Days days
	Now               time.Time
}

// cleanupResultView is what a confirmed cleanup did.
type cleanupResultView struct {
	Backend, Instance string
	Deleted           int
	Skipped           []api.CleanupSkipped
	Err               string
}

// cleanupDays reads a threshold: a whole number from 1 to 3650.
func cleanupDays(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1 && n <= maxCleanupDays
}

// cleanupStep shows the threshold choice and, with ?days=N, the vehicles not driven for N days.
func (s *Server) cleanupStep(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	if !canOn(r, inst).Delete {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	v := cleanupView{Backend: b.Name, Instance: inst, Now: s.now()}
	if raw := r.URL.Query().Get("days"); raw != "" {
		days, ok := cleanupDays(raw)
		if !ok {
			v.Err = errBadDays
			s.render(w, "cleanup", v)
			return
		}
		v.Days = days
		res, err := c.UnusedVehicles(r.Context(), inst, days)
		if err != nil {
			v.Err = err.Error()
		} else {
			v.Items, v.Since = res.Vehicles, res.Since
			v.Young = res.Since != nil && v.Now.Sub(*res.Since) < time.Duration(days)*24*time.Hour
		}
	}
	s.render(w, "cleanup", v)
}

// cleanupConfirm deletes exactly the previewed vehicles; the API checks each one again.
func (s *Server) cleanupConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	if !canOn(r, inst).Delete {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	v := cleanupResultView{Backend: b.Name, Instance: inst}
	if err := r.ParseForm(); err != nil {
		v.Err = err.Error()
		s.render(w, "cleanupResult", v)
		return
	}
	days, okDays := cleanupDays(r.PostFormValue("days"))
	ids := r.PostForm["id"]
	switch {
	case !okDays:
		v.Err = errBadDays
	case len(ids) == 0:
		v.Err = "no vehicles to delete"
	case len(ids) > maxCleanupIDs:
		v.Err = "at most " + strconv.Itoa(maxCleanupIDs) + " vehicles per cleanup"
	}
	if v.Err == "" {
		res, err := c.CleanupVehicles(r.Context(), inst, days, ids)
		if err != nil {
			v.Err = err.Error()
		} else {
			v.Deleted, v.Skipped = len(res.Deleted), res.Skipped
		}
	}
	s.render(w, "cleanupResult", v)
}

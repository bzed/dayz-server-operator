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
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

// noAnswer is the audit result of a delete the mod did not answer in time: it stays queued and may
// still be carried out.
const noAnswer = "no answer yet, the mod may still delete it (command %s)"

const (
	maxCleanupDays = 3650
	maxCleanupIDs  = 500
)

// UnusedVehicles is the preview of a cleanup: the vehicles nobody has driven for Days days.
type UnusedVehicles struct {
	Days     int             `json:"days"`
	Since    *time.Time      `json:"tracked_since,omitempty"` // the oldest first_seen of a live vehicle
	Vehicles []admin.Vehicle `json:"vehicles"`
}

// CleanupRequest is the body of the cleanup route: the vehicles of a preview that were confirmed.
type CleanupRequest struct {
	Days int      `json:"days"`
	IDs  []string `json:"ids"`
}

// CleanupResult says what the mod deleted, what was left alone, with the reason (ours or the mod's), and
// what the mod has not answered yet.
type CleanupResult struct {
	Deleted []string         `json:"deleted"`
	Skipped []CleanupSkipped `json:"skipped"`
	Pending []CleanupPending `json:"pending"`
}

// CleanupPending is a delete the mod did not answer in time. It stays queued and may still be carried
// out; Command is its id for GET /instances/<i>/commands/<id>.
type CleanupPending struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

// CleanupSkipped is a vehicle the cleanup did not delete.
type CleanupSkipped struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

var errBadDays = errors.New("days must be a whole number from 1 to 3650")

// lastUse is the time a vehicle was last driven, else first seen; false when neither is known.
func lastUse(v admin.Vehicle) (time.Time, bool) {
	switch {
	case v.LastDriven != nil:
		return *v.LastDriven, true
	case v.FirstSeen != nil:
		return *v.FirstSeen, true
	}
	return time.Time{}, false
}

// unusedOf returns the vehicles without occupants, not covered, whose last use is more than days days
// before now, oldest first. A vehicle without any known time is never unused.
func unusedOf(vs []admin.Vehicle, days int, now time.Time) []admin.Vehicle {
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	out := []admin.Vehicle{}
	for _, v := range vs {
		if len(v.Occupants) > 0 || bool(v.Covered) {
			continue
		}
		if t, ok := lastUse(v); ok && t.Before(cutoff) {
			out = append(out, v)
		}
	}
	slices.SortStableFunc(out, func(a, b admin.Vehicle) int {
		ta, _ := lastUse(a)
		tb, _ := lastUse(b)
		return ta.Compare(tb)
	})
	return out
}

// cleanupReady refuses a cleanup on an instance that cannot support it: the mod offline, or too old to
// report drivers (every vehicle would look never driven).
func cleanupReady(w http.ResponseWriter, c *call) bool {
	st := c.hub.Status()
	if !st.Connected {
		fail(w, statusFor(admin.ErrOffline), "%v", admin.ErrOffline)
		return false
	}
	if !st.Hello.Has(admin.FeatureDriver) {
		fail(w, http.StatusConflict, "the dzo-admin mod of this instance does not report drivers; update it to 0.8.0")
		return false
	}
	return true
}

func validDays(days int) bool { return days >= 1 && days <= maxCleanupDays }

func (s *Server) unusedVehicles(w http.ResponseWriter, r *http.Request, c *call) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || !validDays(days) {
		fail(w, http.StatusBadRequest, "%v", errBadDays)
		return
	}
	if !cleanupReady(w, c) {
		return
	}
	list, err := s.vehiclesWithRecords(r.Context(), c.hub)
	if err != nil {
		fail(w, http.StatusInternalServerError, "the vehicle records could not be read")
		return
	}
	out := UnusedVehicles{Days: days, Vehicles: unusedOf(list, days, s.now())}
	for _, v := range list {
		if v.FirstSeen != nil && (out.Since == nil || v.FirstSeen.Before(*out.Since)) {
			t := *v.FirstSeen
			out.Since = &t
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// cleanupVehicles deletes the confirmed vehicles that are still unused. Every id is checked again against
// the current list: a vehicle used or boarded since the preview is skipped.
func (s *Server) cleanupVehicles(w http.ResponseWriter, r *http.Request, c *call) {
	var req CleanupRequest
	if !decode(w, r, &req) {
		return
	}
	switch {
	case !validDays(req.Days):
		fail(w, http.StatusBadRequest, "%v", errBadDays)
		return
	case len(req.IDs) == 0:
		fail(w, http.StatusBadRequest, "no vehicles given")
		return
	case len(req.IDs) > maxCleanupIDs:
		fail(w, http.StatusBadRequest, "at most %d vehicles per cleanup", maxCleanupIDs)
		return
	}
	if !cleanupReady(w, c) {
		return
	}
	if !s.actions.Allow(c.who.Key()) {
		const msg = "rate limit reached, try again in a minute"
		_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Instance: c.hub.Name,
			Action: "vehicle_delete", Target: fmt.Sprintf("%d vehicles", len(req.IDs)),
			Detail: fmt.Sprintf("cleanup: not driven for %d days", req.Days), Result: msg})
		fail(w, http.StatusTooManyRequests, msg)
		return
	}
	list, err := s.vehiclesWithRecords(r.Context(), c.hub)
	if err != nil {
		fail(w, http.StatusInternalServerError, "the vehicle records could not be read")
		return
	}
	live := map[string]admin.Vehicle{}
	for _, v := range list {
		live[v.ID] = v
	}
	unused := map[string]bool{}
	for _, v := range unusedOf(list, req.Days, s.now()) {
		unused[v.ID] = true
	}
	res := CleanupResult{Deleted: []string{}, Skipped: []CleanupSkipped{}, Pending: []CleanupPending{}}
	type queued struct {
		id, cmdID string
		entry     admin.AuditEntry
	}
	var sent []queued
	seen := map[string]bool{}
	for _, id := range req.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		entry := admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Instance: c.hub.Name,
			Action: "vehicle_delete", Target: id, Detail: fmt.Sprintf("cleanup: not driven for %d days", req.Days)}
		skip := func(reason string) {
			entry.Result = reason
			_ = s.Audit.Log(entry)
			res.Skipped = append(res.Skipped, CleanupSkipped{ID: id, Reason: reason})
		}
		v, ok := live[id]
		switch {
		case !ok:
			skip("not on the map")
		case len(v.Occupants) > 0:
			skip("crew inside")
		case bool(v.Covered):
			skip("covered by Expansion")
		case !unused[id]:
			skip("used since the preview")
		default:
			cmdID, err := c.hub.Enqueue(admin.Command{Kind: admin.KindVehicleDelete, Vehicle: id})
			if err != nil {
				skip(err.Error())
				continue
			}
			sent = append(sent, queued{id, cmdID, entry})
		}
	}
	// All commands are queued; one deadline covers the wait for every answer.
	wait := s.cleanupWait
	if wait <= 0 {
		wait = maxWait
	}
	ctx, cancel := context.WithTimeout(r.Context(), wait)
	defer cancel()
	for _, q := range sent {
		ans, err := c.hub.Wait(ctx, q.cmdID)
		switch {
		case err != nil:
			q.entry.Result = fmt.Sprintf(noAnswer, q.cmdID)
			_ = s.Audit.Log(q.entry)
			res.Pending = append(res.Pending, CleanupPending{ID: q.id, Command: q.cmdID})
			continue
		case !bool(ans.OK) && ans.Message == "":
			ans.Message = "the mod refused"
		}
		q.entry.OK, q.entry.Result = bool(ans.OK), ans.Message
		_ = s.Audit.Log(q.entry)
		if ans.OK {
			res.Deleted = append(res.Deleted, q.id)
		} else {
			res.Skipped = append(res.Skipped, CleanupSkipped{ID: q.id, Reason: ans.Message})
		}
	}
	writeJSON(w, http.StatusOK, res)
}

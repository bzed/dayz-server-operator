// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

// backlogRows is how many events the events page opens with.
const backlogRows = 200

var feedIcons = map[string]string{
	admin.FeedKill: "crosshairs", admin.FeedDeath: "heartbeat", admin.FeedConnect: "sign-in", admin.FeedDisconnect: "sign-out",
	admin.FeedChat: "comment", admin.FeedKick: "ban", admin.FeedBuild: "cube", admin.FeedDismantle: "wrench",
	admin.FeedDestroy: "bomb", admin.FeedLockCut: "unlock", admin.FeedFlagRaised: "flag", admin.FeedFlagLowered: "flag-o",
	admin.FeedStashBuried: "archive", admin.FeedStashDugUp: "archive", admin.FeedAdmin: "user-secret", admin.FeedDropped: "exclamation-triangle",
}

// who is a player in a row: a link to the player's row on the instance page when the Steam ID is known.
type who struct {
	Href, Name string
	AI         bool
}

// feedRow is one event of the events page, with what the template needs around it.
type feedRow struct {
	Backend, Instance string
	E                 admin.FeedEvent
}

func feedRows(backend, instance string, evs []admin.FeedEvent) []feedRow {
	out := make([]feedRow, len(evs))
	for i, e := range evs {
		out[i] = feedRow{backend, instance, e}
	}
	return out
}

func (r feedRow) Group() string { return admin.FeedGroupOf(r.E.Kind) }
func (r feedRow) Icon() string {
	if i, ok := feedIcons[r.E.Kind]; ok {
		return i
	}
	return "circle-o"
}
func (r feedRow) Clock() string    { return r.E.Time.UTC().Format("15:04:05") }
func (r feedRow) Stamp() string    { return r.E.Time.UTC().Format(time.RFC3339) }
func (r feedRow) Actor() who       { return r.who(r.E.SteamID, r.E.Name, bool(r.E.AI)) }
func (r feedRow) Target() who      { return r.who(r.E.TargetSteamID, r.E.TargetName, bool(r.E.TargetAI)) }
func (r feedRow) Distance() string { return fmt.Sprintf("%.0f", r.E.Distance) }
func (r feedRow) HasDistance() bool {
	return r.E.Distance >= 0.5
}

func (r feedRow) who(steamID, name string, ai bool) who {
	w := who{Name: name, AI: ai}
	if w.Name == "" {
		w.Name = "someone"
	}
	if isSteamID(steamID) {
		w.Href = "/b/" + url.PathEscape(r.Backend) + "/i/" + url.PathEscape(r.Instance) + "#player-" + steamID
	}
	return w
}

// DeathText says how a death without a killer happened.
func (r feedRow) DeathText() string {
	switch r.E.Cause {
	case "infected":
		return "was killed by infected"
	case "animal":
		return "was killed by an animal"
	case "explosion":
		return "was blown up"
	case "vehicle":
		return "was run over"
	}
	return "died"
}

// MapHref opens the map with the place marked.
func (r feedRow) MapHref() string {
	var label string
	switch r.E.Kind {
	case admin.FeedKill:
		label = r.E.Name + " killed " + r.E.TargetName
	case admin.FeedDeath:
		label = r.E.TargetName + " died"
	default:
		label = r.E.Kind + " " + r.E.Name
	}
	if rs := []rune(label); len(rs) > 80 {
		label = string(rs[:80])
	}
	return fmt.Sprintf("/b/%s/i/%s/map?x=%.0f&z=%.0f&label=%s", url.PathEscape(r.Backend), url.PathEscape(r.Instance), r.E.X, r.E.Z, url.QueryEscape(label))
}

func isSteamID(s string) bool {
	if len(s) != 17 {
		return false
	}
	return strings.Trim(s, "0123456789") == ""
}

type eventsView struct {
	Backend, Instance string
	Rows              []feedRow
	Groups            []admin.FeedGroup
	Last              int64
	CanBroadcast      bool
	Expansion         bool
	Err               string
}

// eventsPage is the live "tail -f" page of the instance's event feed.
func (s *Server) eventsPage(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	v := eventsView{Backend: b.Name, Instance: inst, Groups: admin.FeedGroups}
	evs, err := c.Feed(r.Context(), inst, 0, backlogRows)
	if err != nil {
		v.Err = err.Error()
	}
	v.Rows = feedRows(b.Name, inst, evs)
	if len(evs) > 0 {
		v.Last = evs[len(evs)-1].ID
	}
	v.CanBroadcast = canOn(r, inst).Broadcast
	pg := s.page(r, inst+" events", b.Name, inst, "events", v)
	st, err := c.Status(r.Context(), inst)
	pg.setFacts(st, err)
	s.render(w, "events", pg)
}

// feedFragment answers with the rows after an event id, for the page to append; without after, with the
// newest ones.
func (s *Server) feedFragment(w http.ResponseWriter, r *http.Request) {
	b, c, inst, ok := s.target(w, r)
	if !ok {
		return
	}
	var after int64
	limit := backlogRows
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "after must be an event id", http.StatusBadRequest)
			return
		}
		after, limit = n, 0
	}
	evs, err := c.Feed(r.Context(), inst, after, limit)
	if err != nil {
		s.render(w, "feederr", err.Error())
		return
	}
	s.render(w, "feedrows", eventsView{Backend: b.Name, Instance: inst, Rows: feedRows(b.Name, inst, evs)})
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// A verdict cell answers one question about a player in a few plain sentences and says how old the answer is.
type verdictLine struct {
	Text string
	Bad  bool
}

type verdictCell struct {
	Title    string
	Icon     string
	Bad      bool // the source reports a ban
	Lines    []verdictLine
	Foot     string
	FootHref string
}

var beGameNames = map[moderation.BEGame]string{moderation.BEDayZ: "DayZ", moderation.BEArma3: "Arma 3", moderation.BEArma2OA: "Arma 2"}

func ago(now, t time.Time) string { return span(now.Sub(t)) + " ago" }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// verdicts are the three cells of a player: BattlEye, Steam and our own bans. A cell is a ban cell (Bad) only
// when its source reports a ban; "not banned" is plain, not green, because the sources cannot promise more.
func verdicts(d api.PlayerDetail, backend string, now time.Time) []verdictCell {
	return []verdictCell{beCell(d, now), steamCell(d, now), hereCell(d, backend, now)}
}

func beCell(d api.PlayerDetail, now time.Time) verdictCell {
	c := verdictCell{Title: "BattlEye", Icon: "shield"}
	if len(d.BE) == 0 {
		c.Lines = []verdictLine{{Text: "Not asked yet"}}
		return c
	}
	var newest time.Time
	for _, b := range d.BE {
		name := beGameNames[b.Game]
		if name == "" {
			name = string(b.Game)
		}
		switch {
		case b.Banned:
			reason := b.Reason
			if reason == "" {
				reason = "banned"
			}
			c.Lines = append(c.Lines, verdictLine{Text: name + ": " + reason, Bad: true})
			c.Bad = true
		case b.State == moderation.LookupUnknown:
			c.Lines = append(c.Lines, verdictLine{Text: name + ": no answer"})
		default:
			c.Lines = append(c.Lines, verdictLine{Text: name + ": not banned"})
		}
		if b.CheckedAt.After(newest) {
			newest = b.CheckedAt
		}
	}
	c.Foot = "asked " + ago(now, newest)
	return c
}

func steamCell(d api.PlayerDetail, now time.Time) verdictCell {
	c := verdictCell{Title: "Steam", Icon: "steam"}
	in := d.Steam
	if in.State == "" && in.CheckedAt.IsZero() {
		c.Lines = []verdictLine{{Text: "Not asked yet"}}
		return c
	}
	if in.State == moderation.LookupUnknown && in.Persona == "" && in.Privacy == "" {
		c.Lines = []verdictLine{{Text: "No answer from Steam"}}
		c.Foot = "checked " + ago(now, in.CheckedAt)
		return c
	}
	var bans []string
	if in.VACBans != nil { // Web API: the counts
		if n := *in.VACBans; n > 0 {
			bans = append(bans, plural(n, "VAC ban", "VAC bans"))
		}
		if in.GameBans != nil && *in.GameBans > 0 {
			bans = append(bans, plural(*in.GameBans, "game ban", "game bans"))
		}
		if len(bans) > 0 && in.DaysSinceBan != nil && *in.DaysSinceBan > 0 {
			bans[0] += fmt.Sprintf(", last %d days ago", *in.DaysSinceBan)
		}
	} else if in.VACBanned {
		bans = append(bans, "VAC banned")
	}
	if in.CommunityBanned {
		bans = append(bans, "Community ban")
	}
	if e := in.EconomyBan; e != "" && e != "none" {
		bans = append(bans, "Economy ban: "+e)
	}
	for _, b := range bans {
		c.Lines = append(c.Lines, verdictLine{Text: b, Bad: true})
		c.Bad = true
	}
	if t := in.TradeBan; t != "" && t != "None" {
		c.Lines = append(c.Lines, verdictLine{Text: "Trade ban"})
	}
	if len(c.Lines) == 0 {
		if in.Privacy == "private" && in.VACBans == nil {
			c.Lines = []verdictLine{{Text: "Private profile, no ban data"}}
		} else {
			c.Lines = []verdictLine{{Text: "No bans"}}
		}
	}
	c.Foot = "checked " + ago(now, in.CheckedAt)
	return c
}

func banScope(b moderation.Ban) string {
	if b.Scope == moderation.ScopeGlobal {
		return "all instances"
	}
	return strings.Join(b.Instances, ", ")
}

func hereCell(d api.PlayerDetail, backend string, now time.Time) verdictCell {
	c := verdictCell{Title: "Here", Icon: "ban"}
	var pick *moderation.Ban
	for i := range d.Bans {
		b := &d.Bans[i]
		switch {
		case pick == nil:
			pick = b
		case b.Active(now) && !pick.Active(now):
			pick = b
		case b.Active(now) == pick.Active(now) && b.ID > pick.ID && (!b.Active(now) || pick.ExpiresAt != nil):
			pick = b // the newer of two of a kind; of two active ones a permanent one stays
		}
	}
	if pick == nil {
		c.Lines = []verdictLine{{Text: "No bans"}}
		return c
	}
	var text string
	switch pick.State(now) {
	case "active":
		c.Bad = true
		if pick.ExpiresAt == nil {
			text = "Banned permanently"
		} else {
			text = "Banned until " + pick.ExpiresAt.UTC().Format("2006-01-02")
		}
	case "revoked":
		text = "Ban revoked " + pick.RevokedAt.UTC().Format("2006-01-02")
	default:
		text = "Ban expired " + pick.ExpiresAt.UTC().Format("2006-01-02")
	}
	c.Lines = []verdictLine{{Text: text, Bad: c.Bad}}
	c.Foot = "#" + strconv.FormatInt(pick.ID, 10) + ", " + banScope(*pick)
	c.FootHref = "/b/" + url.PathEscape(backend) + "/bans/" + strconv.FormatInt(pick.ID, 10)
	return c
}

// steamFacts is the one line of what Steam told about the account, only what the source gave.
func steamFacts(d api.PlayerDetail) string {
	in := d.Steam
	if in.Persona == "" && in.Privacy == "" && in.Created == nil && in.MemberSince == "" && in.Location == "" && in.Country == "" && in.Level == nil && in.DayZHours == nil {
		return ""
	}
	var p []string
	if in.Persona != "" {
		p = append(p, "“"+in.Persona+"”")
	}
	if in.Privacy != "" {
		p = append(p, in.Privacy+" profile")
	}
	switch {
	case in.Created != nil:
		p = append(p, "since "+in.Created.UTC().Format("January 2006"))
	case in.MemberSince != "":
		p = append(p, "since "+in.MemberSince)
	}
	switch {
	case in.Country != "":
		p = append(p, in.Country)
	case in.Location != "":
		p = append(p, in.Location)
	}
	if in.Level != nil {
		p = append(p, fmt.Sprintf("level %d", *in.Level))
	}
	if in.DayZHours != nil {
		p = append(p, fmt.Sprintf("%.0f h of DayZ", *in.DayZHours))
	}
	return strings.Join(p, ", ")
}

// flaggedWhy is the reason a player is flagged in a list, or "".
func flaggedWhy(p moderation.PlayerSummary) string {
	var w []string
	if p.BEBanned {
		w = append(w, "BattlEye global ban")
	}
	if p.SteamBanned {
		w = append(w, "Steam ban")
	}
	if p.HasBan {
		w = append(w, "banned here")
	}
	return strings.Join(w, ", ")
}

// roleErr is the page for a role that may not read the player database.
const roleMsg = "Your role may not see the player database (it needs the moderator role or higher)."

type playerNote struct {
	OK   bool
	Text string
}

type friendView struct {
	SteamID, Name, Instances string
	LastSeen                 time.Time
}

type playerView struct {
	Backend     string
	D           api.PlayerDetail
	Cells       []verdictCell
	Facts       string
	Aliases     string
	OnlineOn    []string
	Friends     []friendView
	MoreFriends int
	Now         time.Time
	Note        *playerNote
	Err         string
	CanBan      bool
	ShowAddr    bool
}

func (s *Server) playerView(b *Backend, d api.PlayerDetail, now time.Time) playerView {
	v := playerView{Backend: b.Name, D: d, Cells: verdicts(d, b.Name, now), Facts: steamFacts(d), Now: now}
	var others []string
	for _, n := range d.Names {
		if n.Name != d.Name {
			others = append(others, n.Name)
		}
	}
	switch {
	case len(others) > 3:
		v.Aliases = strings.Join(others[:3], ", ") + fmt.Sprintf(" and %d more", len(others)-3)
	case len(others) > 1:
		v.Aliases = strings.Join(others[:len(others)-1], ", ") + " and " + others[len(others)-1]
	case len(others) == 1:
		v.Aliases = others[0]
	}
	for _, x := range d.Sessions {
		if x.End == nil && !slices.Contains(v.OnlineOn, x.Instance) {
			v.OnlineOn = append(v.OnlineOn, x.Instance)
		}
	}
	for _, f := range d.FriendsSeen {
		v.Friends = append(v.Friends, friendView{SteamID: f.SteamID, Name: f.Name, Instances: strings.Join(f.Instances, ", "), LastSeen: f.LastSeen})
	}
	for _, i := range d.Identities {
		if i.IP != "" {
			v.ShowAddr = true
		}
	}
	return v
}

// playerErr answers a failed API call of a player page: a readable message with the right status.
func (s *Server) playerErr(w http.ResponseWriter, r *http.Request, b *Backend, title string, err error) {
	var ae *apiclient.Error
	msg, code := err.Error(), http.StatusBadGateway
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusForbidden:
			msg, code = roleMsg, http.StatusForbidden
		case http.StatusNotFound:
			msg, code = "No player with this Steam ID ever connected to this installation (or only to instances your token may not see): never connected.", http.StatusNotFound
		case http.StatusBadRequest:
			msg, code = ae.Message, http.StatusBadRequest
		}
	}
	s.renderCode(w, code, "player", s.page(r, title, b.Name, "", "players", playerView{Backend: b.Name, Err: msg}))
}

func (s *Server) playerPage(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id := r.PathValue("steamid")
	d, err := c.PlayerDetail(r.Context(), id)
	if err != nil {
		s.playerErr(w, r, b, "Player "+id, err)
		return
	}
	v := s.playerView(b, d, time.Now())
	v.CanBan = true
	s.render(w, "player", s.page(r, d.Name, b.Name, "", "players", v))
}

// lookupPage answers the Check now button with the fresh verdict row, as an htmx fragment.
func (s *Server) lookupPlayer(w http.ResponseWriter, r *http.Request) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id := r.PathValue("steamid")
	d, err := c.LookupPlayer(r.Context(), id)
	var note *playerNote
	if err != nil {
		var ae *apiclient.Error
		note = &playerNote{Text: err.Error()}
		if errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests {
			note.Text = "This player was looked up a moment ago. Try again in a few minutes."
		} else if errors.As(err, &ae) {
			note.Text = ae.Message
		}
		if d, err = c.PlayerDetail(r.Context(), id); err != nil {
			s.render(w, "flash", flash{Text: err.Error()})
			return
		}
	}
	v := s.playerView(b, d, time.Now())
	v.Note = note
	s.render(w, "verdicts", v)
}

const playersPerPage = 50

type playerListView struct {
	Backend, Query, Instance string
	Flagged                  bool
	Instances                []string
	Players                  []moderation.PlayerSummary
	Total, Offset            int
	PrevHref, NextHref       string
	Now                      time.Time
	Err                      string
}

func (s *Server) playersPage(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	v := playerListView{Backend: b.Name, Query: strings.TrimSpace(q.Get("q")), Instance: q.Get("instance"), Flagged: q.Get("flagged") == "1", Now: time.Now()}
	v.Offset, _ = strconv.Atoi(q.Get("offset"))
	v.Offset = max(v.Offset, 0)
	for _, nb := range s.nav(r.Context()) {
		if nb.Name == b.Name {
			for _, i := range nb.Instances {
				v.Instances = append(v.Instances, i.Name)
			}
		}
	}
	list, total, err := c.PlayerHistory(r.Context(), apiclient.PlayerHistoryFilter{Query: v.Query, Instance: v.Instance, Flagged: v.Flagged, Limit: playersPerPage, Offset: v.Offset})
	if err != nil {
		var ae *apiclient.Error
		if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			s.renderCode(w, http.StatusForbidden, "playerdb", s.page(r, "Players", b.Name, "", "players", playerListView{Backend: b.Name, Err: roleMsg}))
			return
		}
		v.Err = err.Error()
	}
	v.Players, v.Total = list, total
	link := func(off int) string {
		p := url.Values{}
		if v.Query != "" {
			p.Set("q", v.Query)
		}
		if v.Instance != "" {
			p.Set("instance", v.Instance)
		}
		if v.Flagged {
			p.Set("flagged", "1")
		}
		if off > 0 {
			p.Set("offset", strconv.Itoa(off))
		}
		return "/b/" + url.PathEscape(b.Name) + "/players?" + p.Encode()
	}
	if v.Offset > 0 {
		v.PrevHref = link(max(v.Offset-playersPerPage, 0))
	}
	if v.Offset+len(list) < total {
		v.NextHref = link(v.Offset + playersPerPage)
	}
	s.render(w, "playerdb", s.page(r, "Players", b.Name, "", "players", v))
}

// flagged maps the Steam IDs among the players of an instance that something flags to the reason, for the
// marker in the live player list. A role without player.history, or any failure, gives none.
func (s *Server) flagged(r *http.Request, c *apiclient.Client, inst string) map[string]string {
	list, _, err := c.PlayerHistory(r.Context(), apiclient.PlayerHistoryFilter{Instance: inst, Flagged: true, Limit: 200})
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, p := range list {
		out[p.SteamID] = flaggedWhy(p)
	}
	return out
}

// hours formats a time played: "96 h", or "<1 h" for less.
func hours(d time.Duration) string {
	if d < time.Hour {
		return "<1 h"
	}
	return fmt.Sprintf("%.0f h", d.Hours())
}

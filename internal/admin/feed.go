// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"fmt"
	"strings"
	"time"
)

// FeedKeep is how many feed events a hub keeps (and its log file after compaction).
const FeedKeep = 1000

// Feed kinds: the mod's plus the ones dzo adds.
const (
	FeedKill        = "kill"
	FeedDeath       = "death"
	FeedConnect     = "connect"
	FeedDisconnect  = "disconnect"
	FeedBuild       = "build"
	FeedDismantle   = "dismantle"
	FeedDestroy     = "destroy"
	FeedLockCut     = "lock_cut"
	FeedFlagRaised  = "flag_raised"
	FeedFlagLowered = "flag_lowered"
	FeedStashBuried = "stash_buried"
	FeedStashDugUp  = "stash_dug_up"
	FeedDropped     = "dropped"
	FeedChat        = "chat"
	FeedKick        = "kick"
	FeedAdmin       = "admin"
)

// FeedEvent is one thing that happened in the game. The mod sends the fields up to Text; dzo sets ID and
// Time, and adds the chat, kick and admin kinds itself.
type FeedEvent struct {
	ID            int64     `json:"id"`            // dzo's, increasing per hub
	Time          time.Time `json:"time"`          // when dzo got it
	Seq           int       `json:"seq,omitempty"` // the mod's
	Kind          string    `json:"kind"`
	SteamID       string    `json:"steam_id,omitempty"`
	Name          string    `json:"name,omitempty"`
	AI            Flag      `json:"ai,omitempty"`
	TargetSteamID string    `json:"target_steam_id,omitempty"`
	TargetName    string    `json:"target_name,omitempty"`
	TargetAI      Flag      `json:"target_ai,omitempty"`
	HasPos        Flag      `json:"has_pos,omitempty"`
	X             float64   `json:"x,omitempty"`
	Y             float64   `json:"y,omitempty"`
	Z             float64   `json:"z,omitempty"`
	Weapon        string    `json:"weapon,omitempty"`
	Distance      float64   `json:"distance,omitempty"`
	Object        string    `json:"object,omitempty"`
	Cause         string    `json:"cause,omitempty"`
	Channel       string    `json:"channel,omitempty"`
	Text          string    `json:"text,omitempty"`
}

// AddFeed stores an event, giving it the next ID and the current time, and announces it on the stream.
func (h *Hub) AddFeed(ev FeedEvent) FeedEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.addFeedLocked(ev)
}

func (h *Hub) addFeedLocked(ev FeedEvent) FeedEvent {
	h.feedID++
	ev.ID, ev.Time = h.feedID, h.now()
	ev.Name, ev.TargetName = delocalize(ev.Name), delocalize(ev.TargetName)
	ev.Weapon, ev.Object = delocalize(ev.Weapon), delocalize(ev.Object)
	h.feed = append(h.feed, ev)
	if len(h.feed) > FeedKeep {
		h.feed = append([]FeedEvent(nil), h.feed[len(h.feed)-FeedKeep:]...)
	}
	if h.feedLog != nil {
		if err := h.feedLog.Append(ev); err != nil && h.Logf != nil {
			h.Logf("instance %s: feed log: %v", h.Name, err)
		}
	}
	h.emit("feed", ev)
	return ev
}

// syncFeedLocked stores the events of a sync that were not stored before: the mod resends a batch it got
// no reply for, and numbers its events from 1 after a restart (a hello).
func (h *Hub) syncFeedLocked(req SyncRequest) {
	if req.Hello {
		h.lastSeq = 0
	}
	for _, ev := range req.Feed {
		if ev.Seq != 0 {
			if ev.Seq <= h.lastSeq {
				continue
			}
			h.lastSeq = ev.Seq
		}
		h.addFeedLocked(ev)
	}
}

// Feed returns the events with an ID above after, oldest first, at most limit of them: the first ones
// after a given ID (a reader catching up), the newest ones for after 0. A limit of 0 or more than
// FeedKeep means FeedKeep.
func (h *Hub) Feed(after int64, limit int) []FeedEvent {
	if limit <= 0 || limit > FeedKeep {
		limit = FeedKeep
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	i := len(h.feed)
	for i > 0 && h.feed[i-1].ID > after {
		i--
	}
	out := h.feed[i:]
	if len(out) > limit {
		if after == 0 {
			out = out[len(out)-limit:]
		} else {
			out = out[:limit]
		}
	}
	return append([]FeedEvent{}, out...)
}

// PlayerByName returns the online player with that name, when there is exactly one.
func (h *Hub) PlayerByName(name string) (Player, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var found Player
	n := 0
	for _, p := range h.players {
		if p.Name == name {
			found = p
			n++
		}
	}
	return found, n == 1
}

// FeedGroup is a set of kinds the web page and `dzo events` filter by.
type FeedGroup struct {
	ID, Label string
	Kinds     []string
}

// FeedGroups are the filter groups; every kind is in exactly one.
var FeedGroups = []FeedGroup{
	{"kills", "Kills & deaths", []string{FeedKill, FeedDeath}},
	{"chat", "Chat", []string{FeedChat}},
	{"players", "Players", []string{FeedConnect, FeedDisconnect, FeedKick}},
	{"building", "Base building", []string{FeedBuild, FeedDismantle}},
	{"raids", "Raids", []string{FeedDestroy, FeedLockCut}},
	{"flags", "Flags", []string{FeedFlagRaised, FeedFlagLowered}},
	{"stashes", "Stashes", []string{FeedStashBuried, FeedStashDugUp}},
	{"admin", "Admin", []string{FeedAdmin, FeedDropped}},
}

// FeedGroupOf is the ID of the group of a kind, or "other".
func FeedGroupOf(kind string) string {
	for _, g := range FeedGroups {
		for _, k := range g.Kinds {
			if k == kind {
				return g.ID
			}
		}
	}
	return "other"
}

// plain makes untrusted text safe to print on a terminal: control characters (C0, DEL and C1, which
// includes ESC) become '?', newlines a space.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return '?'
		}
		return r
	}, s)
}

// Summary is the event as one plain-text line without time and kind, for the console. Everything that
// came from a player or the mod has its control characters replaced, so a name cannot steer the terminal.
func (e FeedEvent) Summary() string {
	actor, target := plain(e.Name), plain(e.TargetName)
	if actor == "" {
		actor = "someone"
	}
	if target == "" {
		target = "someone"
	}
	object, text, weapon := plain(e.Object), plain(e.Text), plain(e.Weapon)
	on := func(joiner string) string {
		if text == "" {
			return ""
		}
		return " " + joiner + " " + text
	}
	switch e.Kind {
	case FeedKill:
		s := actor + " killed " + target
		if weapon != "" {
			s += " with " + weapon
			if e.Distance >= 0.5 {
				s += fmt.Sprintf(" (%.0f m)", e.Distance)
			}
		}
		return s
	case FeedDeath:
		s := target + " died"
		if e.Cause != "" {
			s += " (" + plain(e.Cause) + ")"
		}
		return s
	case FeedConnect:
		return actor + " joined"
	case FeedDisconnect:
		return actor + " left"
	case FeedChat:
		if e.Channel != "" {
			return "[" + plain(e.Channel) + "] " + actor + ": " + text
		}
		return actor + ": " + text
	case FeedKick:
		s := actor + " was kicked"
		if text != "" {
			s += ": " + text
		}
		return s
	case FeedBuild:
		return actor + " built " + object + on("on")
	case FeedDismantle:
		return actor + " dismantled " + object + on("of")
	case FeedDestroy:
		if e.Name == "" && e.SteamID == "" {
			return object + on("of") + " was destroyed"
		}
		return actor + " destroyed " + object + on("of")
	case FeedLockCut:
		s := actor + " cut the combination lock of " + text
		if weapon != "" {
			s += " with " + weapon
		}
		return s
	case FeedFlagRaised:
		return actor + " raised " + text
	case FeedFlagLowered:
		return actor + " lowered " + text
	case FeedStashBuried, FeedStashDugUp:
		s := actor + " buried a stash"
		if e.Kind == FeedStashDugUp {
			s = actor + " dug up a stash"
		}
		if object != "" {
			s += " (" + object + ")"
		}
		return s
	case FeedAdmin:
		s := actor + " " + text
		if object != "" {
			s += " [refused: " + object + "]"
		}
		return s
	case FeedDropped:
		return text + " events were lost"
	}
	return strings.TrimSpace(plain(e.Kind) + " " + text)
}

// KindText is the kind as plain text for the console: a mod's kind is untrusted like the rest.
func (e FeedEvent) KindText() string { return plain(e.Kind) }

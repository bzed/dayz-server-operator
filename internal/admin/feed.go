// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import "time"

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

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"strings"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
)

// feedFromRCon puts the chat and the kicks BattlEye reports into the instance's event feed. The mod
// reports joins and leaves itself, with Steam IDs, so those are ignored here. A chat line carries only the
// name; the Steam ID is filled in when exactly one online player has it and no other online
// player's name could be the sender of the same line (see Hub.PlayerBySender).
func feedFromRCon(reg *admin.Registry) func(string, battleye.Event) {
	return func(instance string, ev battleye.Event) {
		h, ok := reg.Hub(instance)
		if !ok {
			return
		}
		switch ev.Kind {
		case battleye.EventChat:
			fe := admin.FeedEvent{Kind: admin.FeedChat, Name: ev.PlayerName, Channel: ev.Channel, Text: ev.Message}
			if p, ok := h.PlayerBySender(ev.PlayerName, ev.Message); ok {
				fe.SteamID = p.SteamID
			}
			h.AddFeed(fe)
		case battleye.EventKick:
			h.AddFeed(admin.FeedEvent{Kind: admin.FeedKick, Name: ev.PlayerName, Text: ev.Message})
		}
	}
}

// feedActions are the audit actions that go into the event feed: what an operator does to the world and
// to players (bans and kicks, with their reasons), which every viewer may see. Messages, private or
// broadcast, evidence and lookups need more than the view permission of the feed, so they stay in the
// audit log (audit.view).
var feedActions = map[string]bool{
	"teleport": true, "spawn_item": true, "spawn_vehicle": true,
	"vehicle_repair": true, "vehicle_flip": true, "vehicle_move": true, "vehicle_delete": true,
	"storedvehicle_retrieve": true, "storedvehicle_move": true, "flag_raise": true,
	"player.kill": true, "player.heal": true, "player.fixgear": true,
	"player.kick": true, "ban.create": true, "ban.edit": true, "ban.revoke": true,
}

func feedWorthy(e admin.AuditEntry) bool {
	return feedActions[e.Action]
}

// feedFromAudit puts the world-affecting admin actions of an instance into its event feed.
func feedFromAudit(reg *admin.Registry) func(admin.AuditEntry) {
	return func(e admin.AuditEntry) {
		if e.Instance == "" || !feedWorthy(e) {
			return
		}
		h, ok := reg.Hub(e.Instance)
		if !ok {
			return
		}
		text := strings.TrimSpace(e.Action + " " + e.Target)
		if e.Detail != "" {
			text += " (" + e.Detail + ")"
		}
		fe := admin.FeedEvent{Kind: admin.FeedAdmin, Name: e.Actor, Text: text}
		if !e.OK {
			fe.Object = e.Result
		}
		if isSteamID(e.Target) {
			fe.TargetSteamID = e.Target
		}
		h.AddFeed(fe)
	}
}

func isSteamID(s string) bool {
	if len(s) != 17 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

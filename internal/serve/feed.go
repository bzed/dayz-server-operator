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
// name; the Steam ID is filled in when exactly one online player has it.
func feedFromRCon(reg *admin.Registry) func(string, battleye.Event) {
	return func(instance string, ev battleye.Event) {
		h, ok := reg.Hub(instance)
		if !ok {
			return
		}
		switch ev.Kind {
		case battleye.EventChat:
			fe := admin.FeedEvent{Kind: admin.FeedChat, Name: ev.PlayerName, Channel: ev.Channel, Text: ev.Message}
			if p, ok := h.PlayerByName(ev.PlayerName); ok {
				fe.SteamID = p.SteamID
			}
			h.AddFeed(fe)
		case battleye.EventKick:
			h.AddFeed(admin.FeedEvent{Kind: admin.FeedKick, Name: ev.PlayerName, Text: ev.Message})
		}
	}
}

// feedFromAudit puts the admin actions of an instance into its event feed.
func feedFromAudit(reg *admin.Registry) func(admin.AuditEntry) {
	return func(e admin.AuditEntry) {
		if e.Instance == "" {
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

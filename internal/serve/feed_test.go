// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
)

func feedReg(t *testing.T) (*admin.Registry, *admin.Hub) {
	t.Helper()
	reg := admin.NewRegistry(t.TempDir())
	h := reg.Add(admin.NewHub("alpha"))
	h.Sync(admin.SyncRequest{Proto: 1, Hello: true, Players: []admin.Player{{SteamID: "76561198000000001", Name: "Bob"}}})
	return reg, h
}

func TestFeedFromRConChat(t *testing.T) {
	reg, h := feedReg(t)
	f := feedFromRCon(reg)
	f("alpha", battleye.Event{Kind: battleye.EventChat, Channel: "Global", PlayerName: "Bob", Message: "hi"})
	f("alpha", battleye.Event{Kind: battleye.EventChat, Channel: "Side", PlayerName: "Ghost", Message: "boo"})
	f("nowhere", battleye.Event{Kind: battleye.EventChat, PlayerName: "Bob", Message: "lost"})
	got := h.Feed(0, 0)
	require.Len(t, got, 2)
	require.Equal(t, admin.FeedChat, got[0].Kind)
	require.Equal(t, "Bob", got[0].Name)
	require.Equal(t, "Global", got[0].Channel)
	require.Equal(t, "hi", got[0].Text)
	require.Equal(t, "76561198000000001", got[0].SteamID)
	require.Empty(t, got[1].SteamID)
}

// "(Global) Victim: x: text" could come from the player "Victim" or from the player "Victim: x". The
// feed keeps the text and links neither.
func TestFeedFromRConChatWithAmbiguousSender(t *testing.T) {
	reg := admin.NewRegistry(t.TempDir())
	h := reg.Add(admin.NewHub("alpha"))
	h.Sync(admin.SyncRequest{Proto: 1, Hello: true, Players: []admin.Player{
		{SteamID: "76561198000000001", Name: "Victim"}, {SteamID: "76561198000000002", Name: "Victim: x"}, {SteamID: "76561198000000003", Name: "Bob"}}})
	f := feedFromRCon(reg)
	f("alpha", battleye.ParseEvent("(Global) Victim: x: dummy text"))
	f("alpha", battleye.ParseEvent("(Global) Victim: hello: there"))
	f("alpha", battleye.ParseEvent("(Global) Bob: Victim: x: hi"))
	got := h.Feed(0, 0)
	require.Len(t, got, 3)
	require.Equal(t, "Victim", got[0].Name)
	require.Equal(t, "x: dummy text", got[0].Text)
	require.Empty(t, got[0].SteamID, "the sender may be Victim or Victim: x")
	require.Equal(t, "76561198000000001", got[1].SteamID, "no online name starts the line: Victim sent it")
	require.Equal(t, "76561198000000003", got[2].SteamID, "Bob is no prefix ambiguity")
}

func TestFeedFromRConKick(t *testing.T) {
	reg, h := feedReg(t)
	feedFromRCon(reg)("alpha", battleye.Event{Kind: battleye.EventKick, PlayerName: "Bob", Message: "Banned"})
	got := h.Feed(0, 0)
	require.Len(t, got, 1)
	require.Equal(t, admin.FeedKick, got[0].Kind)
	require.Equal(t, "Banned", got[0].Text)
}

func TestFeedFromRConIgnoresConnect(t *testing.T) {
	reg, h := feedReg(t)
	f := feedFromRCon(reg)
	f("alpha", battleye.Event{Kind: battleye.EventConnect, PlayerName: "Bob"})
	f("alpha", battleye.Event{Kind: battleye.EventDisconnect, PlayerName: "Bob"})
	require.Empty(t, h.Feed(0, 0))
}

func TestFeedFromAudit(t *testing.T) {
	reg, h := feedReg(t)
	f := feedFromAudit(reg)
	f(admin.AuditEntry{Actor: "web:bernd", Instance: "alpha", Action: "teleport", Target: "76561198000000001", Detail: "to 100/200", OK: true})
	f(admin.AuditEntry{Actor: "api", Instance: "alpha", Action: "player.kick", Target: "bob", Detail: "afk", OK: false, Result: "offline"})
	f(admin.AuditEntry{Actor: "dzo", Instance: "", Action: "ban.sync"})
	f(admin.AuditEntry{Actor: "dzo", Instance: "nowhere", Action: "teleport"})
	got := h.Feed(0, 0)
	require.Len(t, got, 2)
	require.Equal(t, admin.FeedAdmin, got[0].Kind)
	require.Equal(t, "web:bernd", got[0].Name)
	require.Equal(t, "teleport 76561198000000001 (to 100/200)", got[0].Text)
	require.Equal(t, "76561198000000001", got[0].TargetSteamID)
	require.Empty(t, got[0].Object)
	require.Empty(t, got[1].TargetSteamID)
	require.Equal(t, "offline", got[1].Object)
}

// A viewer reads the feed and the stream with the view permission only. A viewer may see bans and kicks
// with their reasons, but no message (private or broadcast) and no evidence: those stay in the audit log.
func TestFeedFromAuditKeepsMessagesOut(t *testing.T) {
	reg, h := feedReg(t)
	f := feedFromAudit(reg)
	const id = "76561198000000001"
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "message", Target: id, Detail: "chat: SECRET-PRIVATE-DM", OK: false, Result: "SECRET-RESULT"})
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "message", Target: "all", Detail: "chat: SECRET-BROADCAST", OK: true})
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "evidence.add", Target: id, Detail: "SECRET-EVIDENCE", OK: true})
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "ip.history", Target: id, OK: true})
	require.Empty(t, h.Feed(0, 0), "messages, evidence and lookups stay in the audit log")
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "ban.create", Target: id, Detail: "ban #1 instance until never: cheating", OK: true})
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "ban.edit", Target: id, Detail: "ban #1", OK: true})
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "ban.revoke", Target: id, Detail: "ban #1: appeal", OK: true})
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "player.kick", Target: id, Detail: "spawn camping", OK: true})
	f(admin.AuditEntry{Actor: "mod", Instance: "alpha", Action: "vehicle_repair", Target: "v1", Detail: "scope all", OK: true})
	feed := h.Feed(0, 0)
	require.Len(t, feed, 5, "bans, kicks and world actions are in the feed")
	require.Contains(t, feed[0].Text, "cheating", "a viewer sees the ban reason")
	require.Contains(t, feed[3].Text, "spawn camping", "and the kick reason")
}

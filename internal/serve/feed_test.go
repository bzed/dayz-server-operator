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
	f(admin.AuditEntry{Actor: "web:bernd", Instance: "alpha", Action: "player.kick", Target: "76561198000000001", Detail: "spam", OK: true})
	f(admin.AuditEntry{Actor: "api", Instance: "alpha", Action: "message", Target: "all", Detail: "hi", OK: false, Result: "offline"})
	f(admin.AuditEntry{Actor: "dzo", Instance: "", Action: "ban.sync"})
	f(admin.AuditEntry{Actor: "dzo", Instance: "nowhere", Action: "x"})
	got := h.Feed(0, 0)
	require.Len(t, got, 2)
	require.Equal(t, admin.FeedAdmin, got[0].Kind)
	require.Equal(t, "web:bernd", got[0].Name)
	require.Equal(t, "player.kick 76561198000000001 spam", got[0].Text)
	require.Equal(t, "76561198000000001", got[0].TargetSteamID)
	require.Empty(t, got[0].Object)
	require.Empty(t, got[1].TargetSteamID)
	require.Equal(t, "offline", got[1].Object)
}

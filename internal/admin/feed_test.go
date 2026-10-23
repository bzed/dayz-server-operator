// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ids(evs []FeedEvent) []int64 {
	out := make([]int64, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}

func feedSync(h *Hub, hello bool, seqs ...int) {
	var evs []FeedEvent
	for _, s := range seqs {
		evs = append(evs, FeedEvent{Seq: s, Kind: FeedConnect, Name: "n"})
	}
	h.Sync(SyncRequest{Proto: 1, Hello: Flag(hello), Feed: evs})
}

func TestSyncFeedAssignsIDs(t *testing.T) {
	h, c := testHub()
	feedSync(h, true, 1, 2, 3)
	got := h.Feed(0, 0)
	require.Equal(t, []int64{1, 2, 3}, ids(got))
	require.Equal(t, c.t, got[0].Time)
	require.Equal(t, 1, got[0].Seq)
}

func TestSyncFeedResendCountedOnce(t *testing.T) {
	h, _ := testHub()
	feedSync(h, true, 1, 2)
	feedSync(h, false, 1, 2, 3)
	require.Equal(t, []int64{1, 2, 3}, ids(h.Feed(0, 0)))
}

func TestSyncFeedHelloResetsSeq(t *testing.T) {
	h, _ := testHub()
	feedSync(h, true, 1, 2, 3, 4, 5)
	feedSync(h, true, 1)
	require.Len(t, h.Feed(0, 0), 6)
}

func TestFeedRingKeepsNewest(t *testing.T) {
	h, _ := testHub()
	for i := 0; i < FeedKeep+10; i++ {
		h.AddFeed(FeedEvent{Kind: FeedChat})
	}
	got := h.Feed(0, 0)
	require.Len(t, got, FeedKeep)
	require.EqualValues(t, 11, got[0].ID)
	got = h.Feed(5, 0)
	require.EqualValues(t, 11, got[0].ID)
}

func TestFeedAfterAndLimit(t *testing.T) {
	h, _ := testHub()
	for i := 0; i < 4; i++ {
		h.AddFeed(FeedEvent{Kind: FeedChat})
	}
	require.Equal(t, []int64{3}, ids(h.Feed(2, 1)))
	require.Equal(t, []int64{3, 4}, ids(h.Feed(0, 2)))
	require.Empty(t, h.Feed(4, 0))
}

func TestAddFeedEmits(t *testing.T) {
	h, _ := testHub()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ch := h.Subscribe(ctx)
	stored := h.AddFeed(FeedEvent{Kind: FeedChat, Text: "hi"})
	select {
	case ev := <-ch:
		require.Equal(t, "feed", ev.Kind)
		require.Equal(t, stored, ev.Data)
	case <-time.After(time.Second):
		require.Fail(t, "no feed event")
	}
}

func TestSyncFeedWithoutHasIgnored(t *testing.T) {
	h, _ := testHub()
	req := SyncRequest{Proto: 1, Has: []string{"players"}, Players: []Player{}, Feed: []FeedEvent{{Seq: 1, Kind: FeedConnect}}}
	req.Normalise()
	h.Sync(req)
	require.Empty(t, h.Feed(0, 0))
}

func TestPlayerByName(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Players: []Player{{SteamID: "1", Name: "Bob"}, {SteamID: "2", Name: "Twin"}, {SteamID: "3", Name: "Twin"}}})
	p, ok := h.PlayerByName("Bob")
	require.True(t, ok)
	require.Equal(t, "1", p.SteamID)
	_, ok = h.PlayerByName("Twin")
	require.False(t, ok)
	_, ok = h.PlayerByName("Ghost")
	require.False(t, ok)
}

func TestSummary(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   FeedEvent
		want string
	}{
		{"kill", FeedEvent{Kind: FeedKill, Name: "Alice", TargetName: "Bob", Weapon: "M4-A1", Distance: 123.4}, "Alice killed Bob with M4-A1 (123 m)"},
		{"ai kill", FeedEvent{Kind: FeedKill, Name: "AI (East)", AI: true, TargetName: "Bob", Weapon: "AKM", Distance: 54}, "AI (East) killed Bob with AKM (54 m)"},
		{"melee", FeedEvent{Kind: FeedKill, Name: "Alice", TargetName: "Bob", Weapon: "fists"}, "Alice killed Bob with fists"},
		{"death", FeedEvent{Kind: FeedDeath, TargetName: "Bob", Cause: "infected"}, "Bob died (infected)"},
		{"death without cause", FeedEvent{Kind: FeedDeath, TargetName: "Bob"}, "Bob died"},
		{"connect", FeedEvent{Kind: FeedConnect, Name: "Alice"}, "Alice joined"},
		{"disconnect", FeedEvent{Kind: FeedDisconnect, Name: "Alice"}, "Alice left"},
		{"chat", FeedEvent{Kind: FeedChat, Name: "Alice", Channel: "Global", Text: "hi"}, "[Global] Alice: hi"},
		{"chat without channel", FeedEvent{Kind: FeedChat, Name: "Alice", Text: "hi"}, "Alice: hi"},
		{"kick", FeedEvent{Kind: FeedKick, Name: "Bob", Text: "Banned"}, "Bob was kicked: Banned"},
		{"build", FeedEvent{Kind: FeedBuild, Name: "Alice", Object: "wall_base_down", Text: "Fence"}, "Alice built wall_base_down on Fence"},
		{"dismantle", FeedEvent{Kind: FeedDismantle, Name: "Alice", Object: "wall_base_down", Text: "Fence"}, "Alice dismantled wall_base_down of Fence"},
		{"destroy", FeedEvent{Kind: FeedDestroy, Name: "Alice", Object: "wall_base_down", Text: "Fence"}, "Alice destroyed wall_base_down of Fence"},
		{"destroy by nobody", FeedEvent{Kind: FeedDestroy, Object: "wall_base_down", Text: "Fence"}, "wall_base_down of Fence was destroyed"},
		{"lock cut", FeedEvent{Kind: FeedLockCut, Name: "Alice", Text: "Fence", Weapon: "Hacksaw"}, "Alice cut the combination lock of Fence with Hacksaw"},
		{"flag raised", FeedEvent{Kind: FeedFlagRaised, Name: "Alice", Text: "Flag_Chernarus"}, "Alice raised Flag_Chernarus"},
		{"flag lowered", FeedEvent{Kind: FeedFlagLowered, Name: "Alice", Text: "Flag_Chernarus"}, "Alice lowered Flag_Chernarus"},
		{"stash buried", FeedEvent{Kind: FeedStashBuried, Name: "Alice", Object: "AKM"}, "Alice buried a stash (AKM)"},
		{"stash dug up", FeedEvent{Kind: FeedStashDugUp, Name: "Alice"}, "Alice dug up a stash"},
		{"admin", FeedEvent{Kind: FeedAdmin, Name: "web:bernd", Text: "player.kick 76561198000000001 (spam)"}, "web:bernd player.kick 76561198000000001 (spam)"},
		{"admin refused", FeedEvent{Kind: FeedAdmin, Name: "web:bernd", Text: "player.kick 1", Object: "offline"}, "web:bernd player.kick 1 [refused: offline]"},
		{"dropped", FeedEvent{Kind: FeedDropped, Text: "12"}, "12 events were lost"},
		{"unknown", FeedEvent{Kind: "weird", Text: "x"}, "weird x"},
		{"no name", FeedEvent{Kind: FeedConnect}, "someone joined"},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, tc.ev.Summary()) })
	}
}

func TestSummaryStripsControls(t *testing.T) {
	for _, ev := range []FeedEvent{
		{Kind: FeedKill, Name: "a\x1b[2Jb\rc\x9bd", TargetName: "x\u009by\x7fz", Weapon: "w\x00"},
		{Kind: FeedChat, Name: "n", Channel: "c\x1b", Text: "line1\nline2\r\n\x1b]0;title\x07"},
		{Kind: "k\x1b", Text: "t\u0085"},
	} {
		got := ev.Summary()
		for _, r := range got {
			require.False(t, r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f), "control character %U in %q", r, got)
		}
	}
	require.Equal(t, "n: line1 line2? ?]0;title?", (FeedEvent{Kind: FeedChat, Name: "n", Text: "line1\nline2\r\n\x1b]0;title\x07"}).Summary())
}

func TestFeedGroups(t *testing.T) {
	seen := map[string]string{}
	for _, g := range FeedGroups {
		for _, k := range g.Kinds {
			require.Empty(t, seen[k], "kind %s is in two groups", k)
			seen[k] = g.ID
		}
	}
	for _, k := range []string{FeedKill, FeedDeath, FeedConnect, FeedDisconnect, FeedBuild, FeedDismantle, FeedDestroy, FeedLockCut, FeedFlagRaised,
		FeedFlagLowered, FeedStashBuried, FeedStashDugUp, FeedDropped, FeedChat, FeedKick, FeedAdmin} {
		require.NotEmpty(t, seen[k], "kind %s is in no group", k)
	}
	require.Equal(t, "raids", FeedGroupOf(FeedLockCut))
	require.Equal(t, "other", FeedGroupOf("weird"))
}

func TestAddFeedTranslatesAIFactionKeys(t *testing.T) {
	h, _ := testHub()
	ev := h.AddFeed(FeedEvent{
		Kind: FeedKill, Name: "fam.tonini", TargetName: "AI (#STR_EXPANSION_AI_FACTION_WEST)", TargetAI: true,
	})
	require.Equal(t, "AI (West)", ev.TargetName)
	require.Equal(t, "fam.tonini", ev.Name)
	ev = h.AddFeed(FeedEvent{Kind: FeedKill, Name: "AI (#STR_EXPANSION_AI_FACTION_MERCENARY_GUARDS)"})
	require.Equal(t, "AI (Mercenary Guards)", ev.Name)
}

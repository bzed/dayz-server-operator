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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := h.Subscribe(ctx)
	stored := h.AddFeed(FeedEvent{Kind: FeedChat, Text: "hi"})
	select {
	case ev := <-ch:
		require.Equal(t, "feed", ev.Kind)
		require.Equal(t, stored, ev.Data)
	case <-time.After(time.Second):
		t.Fatal("no feed event")
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

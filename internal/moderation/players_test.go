// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJoinLeaveMakesASession(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	p, err := s.Player(ctx, steamA)
	require.NoError(t, err)
	assert.True(t, p.Online)
	assert.Equal(t, guidOf(t, steamA), p.GUID)
	assert.Equal(t, []string{"alpha"}, p.Instances)

	require.NoError(t, s.PlayerLeft(ctx, "alpha", steamA, t0.Add(90*time.Minute)))
	p, err = s.Player(ctx, steamA)
	require.NoError(t, err)
	assert.False(t, p.Online)
	require.Len(t, p.PerInstance, 1)
	assert.Equal(t, 90*time.Minute, p.PerInstance[0].Played)
	assert.Equal(t, 1, p.PerInstance[0].Sessions)
	assert.Equal(t, t0.Add(90*time.Minute), p.LastSeen)
	assert.Equal(t, t0, p.FirstSeen)
	require.Len(t, p.Sessions, 1)
	assert.NotNil(t, p.Sessions[0].End)
}

func TestPlayerUnknownIsNotFound(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Player(t.Context(), steamA)
	require.ErrorIs(t, err, ErrNotFound)
	require.Error(t, s.PlayerJoined(t.Context(), "alpha", "nope", "x", t0))
}

func TestRenameKeepsBothNames(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	require.NoError(t, s.PlayerLeft(ctx, "alpha", steamA, t0.Add(time.Hour)))
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "xX_Fritz_Xx", t0.Add(48*time.Hour)))
	p, err := s.Player(ctx, steamA)
	require.NoError(t, err)
	assert.Equal(t, "xX_Fritz_Xx", p.Name)
	require.Len(t, p.Names, 2)
	assert.Equal(t, "xX_Fritz_Xx", p.Names[0].Name, "newest first")
	assert.Equal(t, "Fritz", p.Names[1].Name)
	assert.Equal(t, t0, p.Names[1].FirstSeen)
}

func TestJoinTwiceClosesTheOpenSession(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0.Add(time.Hour)))
	p, err := s.Player(ctx, steamA)
	require.NoError(t, err)
	require.Len(t, p.Sessions, 2)
	open := 0
	for _, x := range p.Sessions {
		if x.End == nil {
			open++
		}
	}
	assert.Equal(t, 1, open, "never two open sessions for the same player and instance")
}

func TestCloseSessionsClosesOnlyThatInstance(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	require.NoError(t, s.PlayerJoined(ctx, "beta", steamB, "Bob", t0))
	require.NoError(t, s.CloseSessions(ctx, "alpha", t0.Add(time.Hour)))
	a, _ := s.Player(ctx, steamA)
	b, _ := s.Player(ctx, steamB)
	assert.False(t, a.Online)
	assert.True(t, b.Online)
	assert.Equal(t, t0.Add(time.Hour), a.LastSeen)
}

func TestPlayersSearch(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Kater", t0.Add(time.Hour)))
	require.NoError(t, s.PlayerJoined(ctx, "beta", steamB, "100%_sure", t0.Add(2*time.Hour)))
	search := func(f PlayerFilter) []string {
		got, _, err := s.Players(ctx, f)
		require.NoError(t, err)
		var ids []string
		for _, p := range got {
			ids = append(ids, p.SteamID)
		}
		return ids
	}
	assert.Equal(t, []string{steamB, steamA}, search(PlayerFilter{}), "newest first")
	assert.Equal(t, []string{steamA}, search(PlayerFilter{Query: "kat"}), "current name")
	assert.Equal(t, []string{steamA}, search(PlayerFilter{Query: "fritz"}), "an old name")
	assert.Equal(t, []string{steamB}, search(PlayerFilter{Query: steamB}), "Steam ID")
	assert.Equal(t, []string{steamA}, search(PlayerFilter{Query: guidOf(t, steamA)}), "GUID")
	assert.Equal(t, []string{steamB}, search(PlayerFilter{Query: "100%"}), "% is literal")
	assert.Empty(t, search(PlayerFilter{Query: "%zzz"}))
	assert.Equal(t, []string{steamB}, search(PlayerFilter{Instance: "beta"}))
	_, total, err := s.Players(ctx, PlayerFilter{Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "the total ignores the page")
	page, _, _ := s.Players(ctx, PlayerFilter{Limit: 1, Offset: 1})
	require.Len(t, page, 1)
	assert.Equal(t, steamA, page[0].SteamID)
}

func TestFlaggedFilter(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	ids := []string{"76561198000000001", "76561198000000002", "76561198000000003", "76561198000000004"}
	for _, id := range ids {
		require.NoError(t, s.PlayerJoined(ctx, "alpha", id, "p"+id[12:], t0))
	}
	require.NoError(t, s.SaveBEStatus(ctx, guidOf(t, ids[0]), BEStatus{Game: BEDayZ, State: LookupOK, Banned: true, Reason: "Global Ban #1", CheckedAt: t0}))
	require.NoError(t, s.SaveSteamInfo(ctx, ids[1], SteamInfo{State: LookupOK, CheckedAt: t0, VACBanned: true}))
	_, err := s.CreateBan(ctx, "admin", BanInput{SteamID: ids[2], Reason: "cheating", Scope: ScopeGlobal}, t0)
	require.NoError(t, err)
	got, total, err := s.Players(ctx, PlayerFilter{Flagged: true, Now: t0.Add(time.Hour)})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	flags := map[string][3]bool{}
	for _, p := range got {
		flags[p.SteamID] = [3]bool{p.BEBanned, p.SteamBanned, p.HasBan}
	}
	assert.Equal(t, map[string][3]bool{ids[0]: {true, false, false}, ids[1]: {false, true, false}, ids[2]: {false, false, true}}, flags)
}

func TestUnknownKeepsTheKnownAnswer(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	g := guidOf(t, steamA)
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	require.NoError(t, s.SaveBEStatus(ctx, g, BEStatus{Game: BEDayZ, State: LookupOK, Banned: true, Reason: "Global Ban #760490", CheckedAt: t0}))
	require.NoError(t, s.SaveBEStatus(ctx, g, BEStatus{Game: BEDayZ, State: LookupUnknown, CheckedAt: t0.Add(time.Hour)}))
	p, err := s.Player(ctx, steamA)
	require.NoError(t, err)
	require.Len(t, p.BE, 1)
	assert.True(t, p.BE[0].Banned, "an unanswered lookup does not clear a ban")
	assert.Equal(t, "Global Ban #760490", p.BE[0].Reason)
	assert.Equal(t, LookupUnknown, p.BE[0].State)
	assert.Equal(t, t0.Add(time.Hour), p.BE[0].CheckedAt)

	d := 5
	require.NoError(t, s.SaveSteamInfo(ctx, steamA, SteamInfo{State: LookupOK, CheckedAt: t0, Persona: "fritz", VACBanned: true, VACBans: &d, FriendIDs: []string{steamB}}))
	require.NoError(t, s.SaveSteamInfo(ctx, steamA, SteamInfo{State: LookupUnknown, CheckedAt: t0.Add(time.Hour)}))
	p, _ = s.Player(ctx, steamA)
	assert.Equal(t, "fritz", p.Steam.Persona)
	assert.True(t, p.SteamBanned)
	assert.Equal(t, LookupUnknown, p.Steam.State)
	assert.Equal(t, t0.Add(time.Hour), p.Steam.CheckedAt)
}

func TestBEStatusOrderAndOverwrite(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	g := guidOf(t, steamA)
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	for _, game := range []BEGame{BEArma2OA, BEDayZ, BEArma3} {
		require.NoError(t, s.SaveBEStatus(ctx, g, BEStatus{Game: game, State: LookupOK, CheckedAt: t0}))
	}
	p, _ := s.Player(ctx, steamA)
	var order []BEGame
	for _, b := range p.BE {
		order = append(order, b.Game)
	}
	assert.Equal(t, []BEGame{BEDayZ, BEArma3, BEArma2OA}, order)
	require.NoError(t, s.SaveBEStatus(ctx, g, BEStatus{Game: BEArma3, State: LookupOK, Banned: true, Reason: "x", CheckedAt: t0.Add(time.Hour)}))
	p, _ = s.Player(ctx, steamA)
	assert.True(t, p.BEBanned)
	assert.Len(t, p.BE, 3)
}

func TestBEReasonIsTruncated(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	long := make([]byte, 1024)
	for i := range long {
		long[i] = 'x'
	}
	require.NoError(t, s.SaveBEStatus(ctx, guidOf(t, steamA), BEStatus{Game: BEDayZ, State: LookupOK, Banned: true, Reason: string(long), CheckedAt: t0}))
	p, _ := s.Player(ctx, steamA)
	assert.Len(t, p.BE[0].Reason, 256)
}

func TestDueLookups(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	fresh, stale, never, old := "76561198000000001", "76561198000000002", "76561198000000003", "76561198000000004"
	for _, id := range []string{fresh, stale, never} {
		require.NoError(t, s.PlayerJoined(ctx, "alpha", id, "p", t0))
	}
	require.NoError(t, s.PlayerJoined(ctx, "alpha", old, "p", t0.Add(-100*24*time.Hour)))
	for _, g := range []BEGame{BEDayZ, BEArma3, BEArma2OA} {
		require.NoError(t, s.SaveBEStatus(ctx, guidOf(t, fresh), BEStatus{Game: g, State: LookupOK, CheckedAt: t0}))
		require.NoError(t, s.SaveBEStatus(ctx, guidOf(t, stale), BEStatus{Game: g, State: LookupOK, CheckedAt: t0.Add(-10 * 24 * time.Hour)}))
	}
	require.NoError(t, s.SaveSteamInfo(ctx, fresh, SteamInfo{State: LookupOK, CheckedAt: t0}))
	require.NoError(t, s.SaveSteamInfo(ctx, stale, SteamInfo{State: LookupOK, CheckedAt: t0.Add(-48 * time.Hour)}))

	be, err := s.DueLookups(ctx, "be", t0.Add(-7*24*time.Hour), t0.Add(-30*24*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, []string{guidOf(t, never), guidOf(t, stale)}, be, "never asked first, then stale; fresh and long-gone players are not due")
	st, err := s.DueLookups(ctx, "steam", t0.Add(-24*time.Hour), t0.Add(-30*24*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, []string{never, stale}, st)
	one, _ := s.DueLookups(ctx, "steam", t0.Add(-24*time.Hour), t0.Add(-30*24*time.Hour), 1)
	assert.Len(t, one, 1, "limit")
	_, err = s.DueLookups(ctx, "x", t0, t0, 1)
	require.Error(t, err)
}

func TestFriendsSeen(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamB, "Bob", t0))
	got, err := s.FriendsSeen(ctx, []string{steamB, "76561198000000009"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, steamB, got[0].SteamID)
	got, err = s.FriendsSeen(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSteamFriendsAreReplaced(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamB, "Bob", t0))
	require.NoError(t, s.SaveSteamInfo(ctx, steamA, SteamInfo{State: LookupOK, CheckedAt: t0, FriendIDs: []string{steamB}}))
	p, _ := s.Player(ctx, steamA)
	assert.Equal(t, []string{steamB}, p.Steam.FriendIDs)
	require.NoError(t, s.SaveSteamInfo(ctx, steamA, SteamInfo{State: LookupOK, CheckedAt: t0.Add(time.Hour)}))
	p, _ = s.Player(ctx, steamA)
	assert.Empty(t, p.Steam.FriendIDs, "a friend list that turned private is cleared")
}

func TestPurgeSessionsKeepsNamesAndFirstSeen(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0))
	require.NoError(t, s.PlayerLeft(ctx, "alpha", steamA, t0.Add(time.Hour)))
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "Fritz", t0.Add(400*24*time.Hour)))
	n, err := s.PurgeSessions(ctx, t0.Add(200*24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "the old closed session goes, the open one stays")
	p, _ := s.Player(ctx, steamA)
	assert.Len(t, p.Sessions, 1)
	assert.Equal(t, t0, p.FirstSeen)
	assert.Len(t, p.Names, 1)
}

func TestNamesAreTruncated(t *testing.T) {
	s, _ := newStore(t)
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'n'
	}
	require.NoError(t, s.PlayerJoined(t.Context(), "alpha", steamA, string(long), t0))
	p, _ := s.Player(t.Context(), steamA)
	assert.Len(t, p.Name, 128)
}

func TestPlayersOnlySeenOnTheGivenInstances(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PlayerJoined(ctx, "alpha", steamA, "A", t0))
	require.NoError(t, s.PlayerJoined(ctx, "beta", steamB, "B", t0))
	got, total, err := s.Players(ctx, PlayerFilter{Only: []string{"alpha"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, steamA, got[0].SteamID)
	assert.Equal(t, []string{"alpha"}, got[0].Instances)
	got, _, _ = s.Players(ctx, PlayerFilter{Only: []string{"alpha", "beta"}})
	assert.Len(t, got, 2)
	got, _, _ = s.Players(ctx, PlayerFilter{Only: []string{}})
	assert.Len(t, got, 2, "no restriction")
}

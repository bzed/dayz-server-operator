// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCurrentIPIsTheLatestSightingOnThatInstance(t *testing.T) {
	s, _ := newStore(t)
	ga := guidOf(t, steamA)
	_ = s.Seen(t.Context(), "alpha", ga, "192.0.2.7", "alice", t0)
	_ = s.Seen(t.Context(), "alpha", ga, "198.51.100.1", "alice", t0.Add(time.Hour))
	_ = s.Seen(t.Context(), "beta", ga, "203.0.113.9", "alice", t0.Add(2*time.Hour))
	ip, since, err := s.CurrentIP(t.Context(), "alpha", steamA)
	require.NoError(t, err)
	require.Equal(t, "198.51.100.1", ip)
	require.True(t, since.Equal(t0.Add(time.Hour)))
	ip, _, err = s.CurrentIP(t.Context(), "alpha", steamB)
	require.NoError(t, err)
	require.Empty(t, ip)
}

func TestSightingsAndIPTargetsCarryAnOpaqueRef(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Seen(t.Context(), "alpha", guidOf(t, steamA), "192.0.2.7", "alice", t0)
	ids, _ := s.Identities(t.Context(), steamA)
	require.Len(t, ids, 1)
	require.NotEmpty(t, ids[0].Ref)
	require.NotContains(t, ids[0].Ref, "192")
	b, err := s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: ScopeGlobal}, t0)
	require.NoError(t, err)
	var ip Target
	for _, tg := range b.Targets {
		if tg.Kind == TargetIP {
			ip = tg
		}
	}
	require.Equal(t, "192.0.2.7", ip.Value)
	require.NotEmpty(t, ip.Ref)
	require.NotEqual(t, ids[0].Ref, ip.Ref)
	// A ban whose IPs are shown by ref only keeps them when it is edited with those refs.
	in := b.Input()
	in.IPs = nil
	in.IPRefs = []string{ip.Ref}
	b2, err := s.UpdateBan(t.Context(), b.ID, "a", in, t0.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, b2.Targets, len(b.Targets))
}

func TestIPRefsOfAnotherPlayerAreRefused(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Seen(t.Context(), "alpha", guidOf(t, steamB), "198.51.100.1", "bob", t0)
	ids, _ := s.Identities(t.Context(), steamB)
	_, err := s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: ScopeGlobal}, t0)
	require.Error(t, err, "a ref of another player's address was accepted")
	_, err = s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, IPRefs: []string{"s99999", "bogus"}, Reason: "x", Scope: ScopeGlobal}, t0)
	require.Error(t, err, "unknown refs were accepted")
}

func TestBanInputUsesRefsForHiddenIPs(t *testing.T) {
	b := Ban{Targets: []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetIP, Ref: "t5"}, {Kind: TargetIP, Value: "192.0.2.7", Ref: "t6"}}}
	in := b.Input()
	require.Len(t, in.IPs, 1)
	require.Equal(t, "192.0.2.7", in.IPs[0])
	require.Len(t, in.IPRefs, 1)
	require.Equal(t, "t5", in.IPRefs[0])
}

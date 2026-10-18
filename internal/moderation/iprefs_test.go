// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"strings"
	"testing"
	"time"
)

func TestCurrentIPIsTheLatestSightingOnThatInstance(t *testing.T) {
	s, _ := newStore(t)
	ga := guidOf(t, steamA)
	_ = s.Seen(ctx, "alpha", ga, "192.0.2.7", "alice", t0)
	_ = s.Seen(ctx, "alpha", ga, "198.51.100.1", "alice", t0.Add(time.Hour))
	_ = s.Seen(ctx, "beta", ga, "203.0.113.9", "alice", t0.Add(2*time.Hour))
	ip, since, err := s.CurrentIP(ctx, "alpha", steamA)
	if err != nil || ip != "198.51.100.1" || !since.Equal(t0.Add(time.Hour)) {
		t.Fatalf("CurrentIP = %q %v %v", ip, since, err)
	}
	if ip, _, err := s.CurrentIP(ctx, "alpha", steamB); err != nil || ip != "" {
		t.Fatalf("a player never seen: %q %v", ip, err)
	}
}

func TestSightingsAndIPTargetsCarryAnOpaqueRef(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Seen(ctx, "alpha", guidOf(t, steamA), "192.0.2.7", "alice", t0)
	ids, _ := s.Identities(ctx, steamA)
	if len(ids) != 1 || ids[0].Ref == "" || strings.Contains(ids[0].Ref, "192") {
		t.Fatalf("sighting ref: %+v", ids)
	}
	b, err := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: ScopeGlobal}, t0)
	if err != nil {
		t.Fatal(err)
	}
	var ip Target
	for _, tg := range b.Targets {
		if tg.Kind == TargetIP {
			ip = tg
		}
	}
	if ip.Value != "192.0.2.7" || ip.Ref == "" || ip.Ref == ids[0].Ref {
		t.Fatalf("ban IP target %+v", ip)
	}
	// A ban whose IPs are shown by ref only keeps them when it is edited with those refs.
	in := b.Input()
	in.IPs = nil
	in.IPRefs = []string{ip.Ref}
	b2, err := s.UpdateBan(ctx, b.ID, "a", in, t0.Add(time.Minute))
	if err != nil || len(b2.Targets) != len(b.Targets) {
		t.Fatalf("edit by ref: %+v %v", b2, err)
	}
}

func TestIPRefsOfAnotherPlayerAreRefused(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Seen(ctx, "alpha", guidOf(t, steamB), "198.51.100.1", "bob", t0)
	ids, _ := s.Identities(ctx, steamB)
	_, err := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: ScopeGlobal}, t0)
	if err == nil {
		t.Fatal("a ref of another player's address was accepted")
	}
	if _, err := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, IPRefs: []string{"s99999", "bogus"}, Reason: "x", Scope: ScopeGlobal}, t0); err == nil {
		t.Fatal("unknown refs were accepted")
	}
}

func TestBanInputUsesRefsForHiddenIPs(t *testing.T) {
	b := Ban{Targets: []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetIP, Ref: "t5"}, {Kind: TargetIP, Value: "192.0.2.7", Ref: "t6"}}}
	in := b.Input()
	if len(in.IPs) != 1 || in.IPs[0] != "192.0.2.7" || len(in.IPRefs) != 1 || in.IPRefs[0] != "t5" {
		t.Fatalf("Input = %+v", in)
	}
}

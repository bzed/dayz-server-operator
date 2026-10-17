// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func TestOpenCreatesAPrivateFileAndMigratesOnce(t *testing.T) {
	s, path := newStore(t)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", info.Mode().Perm(), err)
	}
	_ = s.Close()
	s2, err := Open(ctx, path) // the second open applies nothing again
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
}

func TestCreateBanAddsTheDerivedGUID(t *testing.T) {
	s, _ := newStore(t)
	b, err := s.CreateBan(ctx, "alice", BanInput{SteamID: steamA, Reason: "aimbot", Scope: ScopeGlobal, ExpiresAt: at(time.Hour)}, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetGUID, Value: guidOf(t, steamA), Derived: true}}
	if len(b.Targets) != 2 || b.Targets[0] != want[0] || b.Targets[1] != want[1] {
		t.Fatalf("targets %+v", b.Targets)
	}
	if b.Message != DefaultMessage || b.CreatedBy != "alice" || !b.CreatedAt.Equal(t0) || !b.Active(t0) || b.Active(t0.Add(2*time.Hour)) {
		t.Fatalf("ban %+v", b)
	}
	if b.Input().SteamID != steamA || len(b.Input().GUIDs) != 0 {
		t.Fatalf("Input %+v", b.Input())
	}
}

func TestCreateBanValidates(t *testing.T) {
	s, _ := newStore(t)
	for name, in := range map[string]BanInput{
		"no target":          {Reason: "x", Scope: ScopeGlobal},
		"no reason":          {SteamID: steamA, Scope: ScopeGlobal},
		"bad steam":          {SteamID: "123", Reason: "x", Scope: ScopeGlobal},
		"bad guid":           {GUIDs: []string{"XYZ"}, Reason: "x", Scope: ScopeGlobal},
		"bad ip":             {IPs: []string{"10.0.0.0/8"}, Reason: "x", Scope: ScopeGlobal},
		"past expiry":        {SteamID: steamA, Reason: "x", Scope: ScopeGlobal, ExpiresAt: at(-time.Minute)},
		"no scope":           {SteamID: steamA, Reason: "x"},
		"global + instances": {SteamID: steamA, Reason: "x", Scope: ScopeGlobal, Instances: []string{"a"}},
		"instances, none":    {SteamID: steamA, Reason: "x", Scope: ScopeInstances},
		"long reason":        {SteamID: steamA, Reason: strings.Repeat("r", MaxReason+1), Scope: ScopeGlobal},
		"long message":       {SteamID: steamA, Reason: "x", Message: strings.Repeat("m", MaxMessage+1), Scope: ScopeGlobal},
		"unknown evidence":   {SteamID: steamA, Reason: "x", Scope: ScopeGlobal, Evidence: []int64{42}},
	} {
		_, err := s.CreateBan(ctx, "a", in, t0)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
}

func TestUpdateRevokeAndHistory(t *testing.T) {
	s, _ := newStore(t)
	b, _ := s.CreateBan(ctx, "alice", BanInput{SteamID: steamA, Reason: "aimbot", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	in := b.Input()
	in.Reason, in.IPs, in.Instances = "aimbot, confirmed", []string{"192.0.2.7"}, []string{"alpha", "beta"}
	b2, err := s.UpdateBan(ctx, b.ID, "bob", in, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if b2.Reason != "aimbot, confirmed" || len(b2.Instances) != 2 || len(b2.Targets) != 3 {
		t.Fatalf("updated %+v", b2)
	}
	if _, err := s.RevokeBan(ctx, b.ID, "carol", "", t0.Add(2*time.Minute)); err == nil {
		t.Fatal("revoke without a reason accepted")
	}
	b3, err := s.RevokeBan(ctx, b.ID, "carol", "appeal accepted", t0.Add(2*time.Minute))
	if err != nil || b3.State(t0.Add(3*time.Minute)) != "revoked" || b3.RevokedBy != "carol" {
		t.Fatalf("revoked %+v, %v", b3, err)
	}
	if _, err := s.UpdateBan(ctx, b.ID, "bob", in, t0.Add(3*time.Minute)); err == nil {
		t.Fatal("a revoked ban was edited")
	}
	h, err := s.History(ctx, b.ID)
	if err != nil || len(h) != 3 || h[0].Action != "create" || h[1].Action != "edit" || h[1].Actor != "bob" || h[2].Action != "revoke" {
		t.Fatalf("history %+v, %v", h, err)
	}
	if !strings.Contains(string(h[1].Change), `"aimbot, confirmed"`) || !strings.Contains(string(h[1].Change), `"old"`) {
		t.Fatalf("edit change %s", h[1].Change)
	}
	if _, err := s.GetBan(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBan(999) = %v", err)
	}
}

func TestListAndFindActive(t *testing.T) {
	s, _ := newStore(t)
	g := guidOf(t, steamA)
	perm, _ := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "cheat", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	timed, _ := s.CreateBan(ctx, "a", BanInput{IPs: []string{"192.0.2.7"}, Reason: "50% toxic_chat", Scope: ScopeGlobal, ExpiresAt: at(time.Hour)}, t0)
	gone, _ := s.CreateBan(ctx, "a", BanInput{SteamID: steamB, Reason: "x", Scope: ScopeGlobal}, t0)
	_, _ = s.RevokeBan(ctx, gone.ID, "a", "mistake", t0)
	later := t0.Add(2 * time.Hour)

	ids := func(f BanFilter) []int64 {
		t.Helper()
		bs, err := s.ListBans(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, b := range bs {
			out = append(out, b.ID)
		}
		return out
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", name, got, want)
			}
		}
	}
	eq("active now", ids(BanFilter{State: "active", Now: t0}), timed.ID, perm.ID)
	eq("active later", ids(BanFilter{State: "active", Now: later}), perm.ID)
	eq("expired later", ids(BanFilter{State: "expired", Now: later}), timed.ID)
	eq("revoked", ids(BanFilter{State: "revoked", Now: t0}), gone.ID)
	eq("beta sees global only", ids(BanFilter{State: "active", Instance: "beta", Now: t0}), timed.ID)
	eq("query by steam finds the guid target", ids(BanFilter{Query: steamA, Now: t0}), perm.ID)
	eq("query by guid", ids(BanFilter{Query: g, Now: t0}), perm.ID)
	eq("LIKE wildcards are literal", ids(BanFilter{Query: "50%", Now: t0}), timed.ID)
	eq("underscore is literal", ids(BanFilter{Query: "c_eat", Now: t0}))
	eq("limit", ids(BanFilter{Limit: 1, Now: t0}), gone.ID)

	if b, err := s.FindActive(ctx, "alpha", g, "", t0); err != nil || b == nil || b.ID != perm.ID {
		t.Fatalf("FindActive guid alpha = %+v, %v", b, err)
	}
	if b, _ := s.FindActive(ctx, "beta", g, "", t0); b != nil {
		t.Fatalf("an alpha ban applied on beta: %+v", b)
	}
	if b, _ := s.FindActive(ctx, "beta", "", "192.0.2.7", t0); b == nil || b.ID != timed.ID {
		t.Fatalf("FindActive ip = %+v", b)
	}
	if b, _ := s.FindActive(ctx, "beta", "", "192.0.2.7", later); b != nil {
		t.Fatal("an expired ban was found")
	}
	if b, _ := s.FindActive(ctx, "alpha", guidOf(t, steamB), "", t0); b != nil {
		t.Fatal("a revoked ban was found")
	}
}

func TestOpenReadOnly(t *testing.T) {
	s, path := newStore(t)
	_, _ = s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	ro, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if bs, err := ro.ActiveBans(ctx, "alpha", t0); err != nil || len(bs) != 1 {
		t.Fatalf("ActiveBans = %v, %v", bs, err)
	}
	if _, err := ro.CreateBan(ctx, "a", BanInput{SteamID: steamB, Reason: "x", Scope: ScopeGlobal}, t0); err == nil {
		t.Fatal("a read-only store wrote")
	}
	if _, err := OpenReadOnly(ctx, path+".missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database: %v", err)
	}
}

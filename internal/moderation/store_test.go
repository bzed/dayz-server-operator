// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCreatesAPrivateFileAndMigratesOnce(t *testing.T) {
	s, path := newStore(t)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	_ = s.Close()
	s2, err := Open(t.Context(), path) // the second open applies nothing again
	require.NoError(t, err)
	_ = s2.Close()
}

func TestCreateBanAddsTheDerivedGUID(t *testing.T) {
	s, _ := newStore(t)
	b, err := s.CreateBan(t.Context(), "alice", BanInput{SteamID: steamA, Reason: "aimbot", Scope: ScopeGlobal, ExpiresAt: at(time.Hour)}, t0)
	require.NoError(t, err)
	want := []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetGUID, Value: guidOf(t, steamA), Derived: true}}
	require.Len(t, b.Targets, 2)
	require.Equal(t, want[0], b.Targets[0])
	require.Equal(t, want[1], b.Targets[1])
	require.Equal(t, DefaultMessage, b.Message)
	require.Equal(t, "alice", b.CreatedBy)
	require.True(t, b.CreatedAt.Equal(t0), "ban %+v", b)
	require.True(t, b.Active(t0), "ban %+v", b)
	require.False(t, b.Active(t0.Add(2*time.Hour)), "ban %+v", b)
	require.Equal(t, steamA, b.Input().SteamID)
	require.Empty(t, b.Input().GUIDs)
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
		_, err := s.CreateBan(t.Context(), "a", in, t0)
		var inv *InvalidError
		require.ErrorAs(t, err, &inv, name)
	}
}

func TestUpdateRevokeAndHistory(t *testing.T) {
	s, _ := newStore(t)
	b, _ := s.CreateBan(t.Context(), "alice", BanInput{SteamID: steamA, Reason: "aimbot", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	in := b.Input()
	in.Reason, in.IPs, in.Instances = "aimbot, confirmed", []string{"192.0.2.7"}, []string{"alpha", "beta"}
	b2, err := s.UpdateBan(t.Context(), b.ID, "bob", in, t0.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, "aimbot, confirmed", b2.Reason)
	require.Len(t, b2.Instances, 2)
	require.Len(t, b2.Targets, 3)
	_, err = s.RevokeBan(t.Context(), b.ID, "carol", "", t0.Add(2*time.Minute))
	require.Error(t, err, "revoke without a reason accepted")
	b3, err := s.RevokeBan(t.Context(), b.ID, "carol", "appeal accepted", t0.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, "revoked", b3.State(t0.Add(3*time.Minute)))
	require.Equal(t, "carol", b3.RevokedBy)
	_, err = s.UpdateBan(t.Context(), b.ID, "bob", in, t0.Add(3*time.Minute))
	require.Error(t, err, "a revoked ban was edited")
	h, err := s.History(t.Context(), b.ID)
	require.NoError(t, err)
	require.Len(t, h, 3)
	require.Equal(t, "create", h[0].Action)
	require.Equal(t, "edit", h[1].Action)
	require.Equal(t, "bob", h[1].Actor)
	require.Equal(t, "revoke", h[2].Action)
	require.Contains(t, string(h[1].Change), `"aimbot, confirmed"`)
	require.Contains(t, string(h[1].Change), `"old"`)
	_, err = s.GetBan(t.Context(), 999)
	require.ErrorIs(t, err, ErrNotFound, "GetBan(999)")
}

func TestListAndFindActive(t *testing.T) {
	s, _ := newStore(t)
	g := guidOf(t, steamA)
	perm, _ := s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "cheat", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	timed, _ := s.CreateBan(t.Context(), "a", BanInput{IPs: []string{"192.0.2.7"}, Reason: "50% toxic_chat", Scope: ScopeGlobal, ExpiresAt: at(time.Hour)}, t0)
	gone, _ := s.CreateBan(t.Context(), "a", BanInput{SteamID: steamB, Reason: "x", Scope: ScopeGlobal}, t0)
	_, _ = s.RevokeBan(t.Context(), gone.ID, "a", "mistake", t0)
	later := t0.Add(2 * time.Hour)

	ids := func(f BanFilter) []int64 {
		t.Helper()
		bs, err := s.ListBans(t.Context(), f)
		require.NoError(t, err)
		var out []int64
		for _, b := range bs {
			out = append(out, b.ID)
		}
		return out
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		require.Len(t, got, len(want))
		for i := range got {
			require.Equal(t, want[i], got[i])
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

	b, err := s.FindActive(t.Context(), "alpha", g, "", t0)
	require.NoError(t, err)
	require.NotNil(t, b)
	require.Equal(t, perm.ID, b.ID)
	b, _ = s.FindActive(t.Context(), "beta", g, "", t0)
	require.Nil(t, b)
	b, _ = s.FindActive(t.Context(), "beta", "", "192.0.2.7", t0)
	require.NotNil(t, b)
	require.Equal(t, timed.ID, b.ID)
	b, _ = s.FindActive(t.Context(), "beta", "", "192.0.2.7", later)
	require.Nil(t, b, "an expired ban was found")
	b, _ = s.FindActive(t.Context(), "alpha", guidOf(t, steamB), "", t0)
	require.Nil(t, b, "a revoked ban was found")
}

func TestOpenReadOnly(t *testing.T) {
	s, path := newStore(t)
	_, _ = s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	ro, err := OpenReadOnly(t.Context(), path)
	require.NoError(t, err)
	defer func() { _ = ro.Close() }()
	bs, err := ro.ActiveBans(t.Context(), "alpha", t0)
	require.NoError(t, err)
	require.Len(t, bs, 1)
	_, err = ro.CreateBan(t.Context(), "a", BanInput{SteamID: steamB, Reason: "x", Scope: ScopeGlobal}, t0)
	require.Error(t, err, "a read-only store wrote")
	_, err = OpenReadOnly(t.Context(), path+".missing")
	require.ErrorIs(t, err, os.ErrNotExist)
}

// BattlEye keeps 64 bytes of the message, so a longer one is refused instead of being cut silently.
func TestMessageLimitIsBattlEyesByteLimit(t *testing.T) {
	s, _ := newStore(t)
	ok := BanInput{SteamID: steamA, Reason: strings.Repeat("long internal reason ", 20), Message: strings.Repeat("m", 64), Scope: ScopeGlobal}
	b, err := s.CreateBan(t.Context(), "a", ok, t0)
	require.NoError(t, err, "64 bytes with a long reason")
	require.Equal(t, strings.TrimSpace(ok.Reason), b.Reason, "reason %q or message %q was changed", b.Reason, b.Message)
	assert.Equal(t, b.Message, BEReason(b.Message), "reason %q or message %q was changed", b.Reason, b.Message)
	// 33 two-byte runes are 66 bytes: few characters, too many bytes.
	_, err = s.CreateBan(t.Context(), "a", BanInput{SteamID: steamB, Reason: "x", Message: strings.Repeat("ä", 33), Scope: ScopeGlobal}, t0)
	var inv *InvalidError
	require.ErrorAs(t, err, &inv)
	assert.Contains(t, err.Error(), "64")
}

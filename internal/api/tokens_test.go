// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tokens.json")
	s := NewTokenStore(path)
	_, ok := s.Lookup("x")
	require.False(t, ok, "empty store accepted a token")
	secret, tok, err := s.Create("bot", RoleOperator, []string{"alpha"}, false, time.Hour)
	require.NoError(t, err, "create: %v %q", err, secret)
	require.True(t, strings.HasPrefix(secret, "dzo_"), "create: %v %q", err, secret)
	raw, _ := os.ReadFile(path)
	require.NotContains(t, string(raw), secret, "the secret must not be stored")
	info, _ := os.Stat(path)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	p, ok := s.Lookup(secret)
	require.True(t, ok, "lookup: %+v %v", p, ok)
	require.Equal(t, tok.ID, p.Token.ID)
	require.True(t, p.Allowed("alpha"), "lookup: %+v %v", p, ok)
	require.False(t, p.Allowed("beta"), "lookup: %+v %v", p, ok)
	// Another process (dzo token create) sees tokens through the file.
	other := NewTokenStore(path)
	_, ok = other.Lookup(secret)
	require.True(t, ok, "second store must read the file")
	list, err := other.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	err = s.Revoke(tok.ID)
	require.NoError(t, err)
	_, ok = s.Lookup(secret)
	require.False(t, ok, "revoked token still works")
	require.NotNil(t, s.Revoke("nope"), "revoking an unknown id must fail")
}

func TestTokenExpiryAndValidation(t *testing.T) {
	s := NewTokenStore(filepath.Join(t.TempDir(), "t.json"))
	now := time.Now()
	s.now = func() time.Time { return now }
	secret, _, _ := s.Create("a", RoleViewer, nil, false, time.Minute)
	s.now = func() time.Time { return now.Add(2 * time.Minute) }
	_, ok := s.Lookup(secret)
	require.False(t, ok, "expired token accepted")
	_, _, err := s.Create("", RoleViewer, nil, false, 0)
	require.Error(t, err, "empty name")
	_, _, err = s.Create("a", "root", nil, false, 0)
	require.Error(t, err, "unknown role")
	_, long, _ := s.Create("long", RoleViewer, nil, false, 100000*time.Hour)
	require.LessOrEqual(t, long.Expires.Sub(long.Created), 366*24*time.Hour, "ttl must be capped")
	err = os.WriteFile(s.Path, []byte("{broken"), 0o600)
	require.NoError(t, err)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(s.Path, future, future)
	_, err = s.List()
	require.Error(t, err, "a broken token file must be an error")
	_, ok = s.Lookup(secret)
	require.False(t, ok, "broken file must fail closed")
}

func TestRoles(t *testing.T) {
	require.True(t, RoleAdmin.Can(PermAuditView), "permissions")
	require.False(t, RoleOperator.Can(PermAuditView), "permissions")
	require.False(t, RoleViewer.Can(PermMessage), "permissions")
	require.True(t, RoleModerator.Can(PermMessage), "permissions")
	require.False(t, Role("x").Valid(), "ranks")
	require.False(t, Role("x").Can(PermView), "ranks")
	require.Less(t, RoleViewer.Rank(), RoleAdmin.Rank())
	require.True(t, visible("", RoleAdmin), "visibility")
	require.False(t, visible("", RoleOperator), "visibility")
	require.True(t, visible("viewer", RoleViewer), "visibility")
	require.False(t, visible("bogus", RoleViewer), "visibility")
	require.NotEmpty(t, cleanHeader("a\x00b\n"+strings.Repeat("x", 100)))
	require.Len(t, cleanHeader(strings.Repeat("x", 100)), 64)
}

func TestBanPermissions(t *testing.T) {
	cases := map[Role][]string{
		RoleDetector:  {PermEvidenceAdd},
		RoleViewer:    {PermView},
		RoleModerator: {PermBanView, PermBanInstance, PermEvidenceAdd},
		RoleOperator:  {PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermEvidenceAdd},
		RoleAdmin:     {PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermBanGlobal, PermBanRevokeAny, PermEvidenceAdd},
	}
	all := []string{PermView, PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermBanGlobal, PermBanRevokeAny, PermEvidenceAdd}
	for role, want := range cases {
		for _, p := range all {
			got := role.Can(p)
			if p != PermView || role == RoleDetector {
				assert.Equal(t, slices.Contains(want, p), got, "%s.Can(%s)", role, p)
			}
		}
	}
	require.False(t, RoleDetector.Can(PermView), "detector: valid, below viewer, sees nothing")
	require.True(t, RoleDetector.Valid(), "detector: valid, below viewer, sees nothing")
	assert.Less(t, RoleDetector.Rank(), RoleViewer.Rank())
}

func TestAnnouncerRoleOnlyViewsAndBroadcasts(t *testing.T) {
	r := RoleAnnouncer
	require.True(t, r.Valid(), "announcer is not a valid role")
	for _, perm := range []string{PermView, PermBroadcast} {
		assert.True(t, r.Can(perm), "announcer cannot %s", perm)
	}
	for _, perm := range []string{PermMessage, PermKick, PermKill, PermSpawn, PermDelete, PermTeleport, PermBanInstance, PermPlayerIP, PermAuditView} {
		assert.False(t, r.Can(perm), "announcer can %s", perm)
	}
}

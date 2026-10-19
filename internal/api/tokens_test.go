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
)

func TestTokenLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tokens.json")
	s := NewTokenStore(path)
	if _, ok := s.Lookup("x"); ok {
		t.Fatal("empty store accepted a token")
	}
	secret, tok, err := s.Create("bot", RoleOperator, []string{"alpha"}, false, time.Hour)
	if err != nil || !strings.HasPrefix(secret, "dzo_") {
		t.Fatalf("create: %v %q", err, secret)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), secret) {
		t.Fatal("the secret must not be stored")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	p, ok := s.Lookup(secret)
	if !ok || p.Token.ID != tok.ID || !p.Allowed("alpha") || p.Allowed("beta") {
		t.Fatalf("lookup: %+v %v", p, ok)
	}
	// Another process (dzo token create) sees tokens through the file.
	other := NewTokenStore(path)
	if _, ok := other.Lookup(secret); !ok {
		t.Fatal("second store must read the file")
	}
	list, err := other.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if err := s.Revoke(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup(secret); ok {
		t.Fatal("revoked token still works")
	}
	if s.Revoke("nope") == nil {
		t.Fatal("revoking an unknown id must fail")
	}
}

func TestTokenExpiryAndValidation(t *testing.T) {
	s := NewTokenStore(filepath.Join(t.TempDir(), "t.json"))
	now := time.Now()
	s.now = func() time.Time { return now }
	secret, _, _ := s.Create("a", RoleViewer, nil, false, time.Minute)
	s.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, ok := s.Lookup(secret); ok {
		t.Fatal("expired token accepted")
	}
	if _, _, err := s.Create("", RoleViewer, nil, false, 0); err == nil {
		t.Fatal("empty name")
	}
	if _, _, err := s.Create("a", "root", nil, false, 0); err == nil {
		t.Fatal("unknown role")
	}
	_, long, _ := s.Create("long", RoleViewer, nil, false, 100000*time.Hour)
	if long.Expires.Sub(long.Created) > 366*24*time.Hour {
		t.Fatal("ttl must be capped")
	}
	if err := os.WriteFile(s.Path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(s.Path, future, future)
	if _, err := s.List(); err == nil {
		t.Fatal("a broken token file must be an error")
	}
	if _, ok := s.Lookup(secret); ok {
		t.Fatal("broken file must fail closed")
	}
}

func TestRoles(t *testing.T) {
	if !RoleAdmin.Can(PermAuditView) || RoleOperator.Can(PermAuditView) || RoleViewer.Can(PermMessage) || !RoleModerator.Can(PermMessage) {
		t.Fatal("permissions")
	}
	if Role("x").Valid() || Role("x").Can(PermView) || RoleViewer.Rank() >= RoleAdmin.Rank() {
		t.Fatal("ranks")
	}
	if !visible("", RoleAdmin) || visible("", RoleOperator) || !visible("viewer", RoleViewer) || visible("bogus", RoleViewer) {
		t.Fatal("visibility")
	}
	if cleanHeader("a\x00b\n"+strings.Repeat("x", 100)) == "" || len(cleanHeader(strings.Repeat("x", 100))) != 64 {
		t.Fatal("cleanHeader")
	}
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
			if got := role.Can(p); got != slices.Contains(want, p) && (p != PermView || role == RoleDetector) {
				t.Errorf("%s.Can(%s) = %t", role, p, got)
			}
		}
	}
	if RoleDetector.Can(PermView) || !RoleDetector.Valid() || RoleDetector.Rank() >= RoleViewer.Rank() {
		t.Error("detector: valid, below viewer, sees nothing")
	}
}

func TestAnnouncerRoleOnlyViewsAndBroadcasts(t *testing.T) {
	r := RoleAnnouncer
	if !r.Valid() {
		t.Fatal("announcer is not a valid role")
	}
	for _, perm := range []string{PermView, PermBroadcast} {
		if !r.Can(perm) {
			t.Errorf("announcer cannot %s", perm)
		}
	}
	for _, perm := range []string{PermMessage, PermKick, PermKill, PermSpawn, PermDelete, PermTeleport, PermBanInstance, PermPlayerIP, PermAuditView} {
		if r.Can(perm) {
			t.Errorf("announcer can %s", perm)
		}
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Role is a set of permissions, ordered by power.
type Role string

const (
	RoleDetector Role = "detector"
	RoleViewer   Role = "viewer"
	// RoleAnnouncer is for the restart countdown: it reads the instance's status and sends
	// broadcasts, nothing else.
	RoleAnnouncer Role = "announcer"
	RoleModerator Role = "moderator"
	RoleOperator  Role = "operator"
	RoleAdmin     Role = "admin"
)

// Permissions checked by the handlers.
const (
	PermView      = "view"
	PermMessage   = "player.message"
	PermKick      = "player.kick"
	PermKill      = "player.kill"
	PermHeal      = "player.heal"
	PermFixGear   = "player.fixgear"
	PermBroadcast = "broadcast"
	PermTeleport  = "player.teleport"
	PermSpawn     = "player.spawn_item"
	PermRepair    = "vehicle.repair"
	PermDelete    = "vehicle.delete"
	PermAuditView = "audit.view"

	PermBanView      = "ban.view"
	PermBanInstance  = "ban.instance"
	PermBanLong      = "ban.permanent"
	PermBanIP        = "ban.ip"
	PermBanGlobal    = "ban.global"
	PermBanRevokeAny = "ban.revoke_any"
	PermEvidenceAdd  = "evidence.add"

	// IP addresses are personal data (GDPR). The address a player is connected from is operational
	// (support, abuse, bans) and is shown to operators; the history of addresses, and any address in a
	// ban or in evidence, needs a permission of its own. Matching players by address (ban hits, "shared
	// with N other players") works with neither.
	PermPlayerIP  = "player.ip"
	PermIPHistory = "ip.history"

	secretPrefix   = "dzo_"
	secretBytes    = 24
	defaultMaxDays = 365
)

var rolePerms = map[Role][]string{
	RoleDetector:  {PermEvidenceAdd},
	RoleViewer:    {PermView},
	RoleAnnouncer: {PermView, PermBroadcast},
	RoleModerator: {PermView, PermMessage, PermKick, PermBanView, PermBanInstance, PermEvidenceAdd},
	RoleOperator:  {PermView, PermMessage, PermKick, PermKill, PermHeal, PermFixGear, PermBroadcast, PermTeleport, PermSpawn, PermRepair, PermDelete, PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermEvidenceAdd, PermPlayerIP},
	RoleAdmin: {PermView, PermMessage, PermKick, PermKill, PermHeal, PermFixGear, PermBroadcast, PermTeleport, PermSpawn, PermRepair, PermDelete, PermAuditView,
		PermBanView, PermBanInstance, PermBanLong, PermBanIP, PermBanGlobal, PermBanRevokeAny, PermEvidenceAdd, PermPlayerIP, PermIPHistory},
}

// Rank orders roles; a marker is visible to roles of at least its rank.
func (r Role) Rank() int {
	switch r {
	case RoleDetector:
		return 1
	case RoleViewer, RoleAnnouncer:
		return 2
	case RoleModerator:
		return 3
	case RoleOperator:
		return 4
	case RoleAdmin:
		return 5
	}
	return 0
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r.Rank() > 0 }

// Can reports whether the role grants a permission.
func (r Role) Can(perm string) bool {
	for _, p := range rolePerms[r] {
		if p == perm {
			return true
		}
	}
	return false
}

// Token is one stored API token. Only the hash of the secret is kept.
type Token struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	Role      Role      `json:"role"`
	Instances []string  `json:"instances,omitempty"` // empty = all
	Delegate  bool      `json:"delegate,omitempty"`  // may name the acting user (web interface)
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
}

// Principal is an authenticated caller.
type Principal struct {
	Token Token
}

// Allowed reports whether the principal may touch an instance.
func (p Principal) Allowed(instance string) bool {
	if len(p.Token.Instances) == 0 {
		return true
	}
	for _, i := range p.Token.Instances {
		if i == instance {
			return true
		}
	}
	return false
}

// TokenStore is the token file: JSON, 0600, re-read when it changes so that
// `dzo token create` takes effect in a running `dzo serve`.
type TokenStore struct {
	Path string
	now  func() time.Time

	mu     sync.Mutex
	tokens []Token
	mtime  time.Time
}

// NewTokenStore returns a store backed by path.
func NewTokenStore(path string) *TokenStore { return &TokenStore{Path: path, now: time.Now} }

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (s *TokenStore) loadLocked() error {
	info, err := os.Stat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		s.tokens, s.mtime = nil, time.Time{}
		return nil
	}
	if err != nil {
		return err
	}
	if info.ModTime().Equal(s.mtime) && s.tokens != nil {
		return nil
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return err
	}
	var toks []Token
	if err := json.Unmarshal(b, &toks); err != nil {
		return fmt.Errorf("api: %s: %w", s.Path, err)
	}
	s.tokens, s.mtime = toks, info.ModTime()
	return nil
}

func (s *TokenStore) saveLocked() error {
	b, err := json.MarshalIndent(s.tokens, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o750); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return err
	}
	if info, err := os.Stat(s.Path); err == nil {
		s.mtime = info.ModTime()
	}
	return nil
}

// Create adds a token and returns its secret, which is shown once and never
// stored. ttl is bounded to a year; a token always expires.
func (s *TokenStore) Create(name string, role Role, instances []string, delegate bool, ttl time.Duration) (secret string, tok Token, err error) {
	if name == "" {
		return "", Token{}, errors.New("api: token name is required")
	}
	if !role.Valid() {
		return "", Token{}, fmt.Errorf("api: unknown role %q", role)
	}
	if limit := defaultMaxDays * 24 * time.Hour; ttl <= 0 || ttl > limit {
		ttl = limit
	}
	var raw [secretBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Token{}, err
	}
	secret = secretPrefix + hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return "", Token{}, err
	}
	now := s.now().UTC()
	tok = Token{ID: hex.EncodeToString(raw[:4]), Name: name, Hash: hashSecret(secret), Role: role, Instances: instances, Delegate: delegate, Created: now, Expires: now.Add(ttl)}
	s.tokens = append(s.tokens, tok)
	return secret, tok, s.saveLocked()
}

// List returns every token (hashes included; they are not secrets).
func (s *TokenStore) List() ([]Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	return append([]Token(nil), s.tokens...), nil
}

// Revoke deletes a token by id.
func (s *TokenStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	for i, t := range s.tokens {
		if t.ID == id {
			s.tokens = append(s.tokens[:i:i], s.tokens[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("api: no token with id %q", id)
}

// Lookup authenticates a bearer secret.
func (s *TokenStore) Lookup(secret string) (Principal, bool) {
	if secret == "" {
		return Principal{}, false
	}
	h := hashSecret(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadLocked() != nil {
		return Principal{}, false
	}
	var found *Token
	for i := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(s.tokens[i].Hash), []byte(h)) == 1 {
			found = &s.tokens[i]
		}
	}
	if found == nil || !s.now().Before(found.Expires) {
		return Principal{}, false
	}
	return Principal{Token: *found}, true
}

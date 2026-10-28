// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"slices"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// PermUsersManage lets someone list people and change their grants (admin on every instance only).
const PermUsersManage = "users.manage"

// installationWide reports whether a permission is about the whole installation rather than one instance.
// It needs the admin role on every instance ("*"), however many instances there are.
func installationWide(perm string) bool {
	switch perm {
	case PermAuditView, PermBanGlobal, PermBanRevokeAny, PermUsersManage:
		return true
	}
	return false
}

// User is the person a web session belongs to.
type User struct {
	SteamID string
	Name    string
	Grants  []moderation.Grant
}

// Principal is an authenticated caller: a token, and for a web token with a session the person it acts for.
type Principal struct {
	Token Token
	User  *User
}

// tokenAllows reports whether the token's instance list lets it touch an instance; "*" (every instance) needs
// a token without a list.
func (p Principal) tokenAllows(instance string) bool {
	if len(p.Token.Instances) == 0 {
		return true
	}
	return instance != "*" && slices.Contains(p.Token.Instances, instance)
}

func minRole(a, b Role) Role {
	if a.Rank() <= b.Rank() {
		return a
	}
	return b
}

// grantOn is the best role a person's grants give on an instance: the larger of the grant on it and the grant
// on "*". For instance "*" it is the grant on "*" alone.
func (u *User) grantOn(instance string) Role {
	var best Role
	for _, g := range u.Grants {
		r := Role(g.Role)
		if !r.Valid() || (g.Instance != instance && g.Instance != "*") {
			continue
		}
		if instance == "*" && g.Instance != "*" {
			continue
		}
		if r.Rank() > best.Rank() {
			best = r
		}
	}
	return best
}

// RoleOn is the effective role on an instance, the zero Role for none. For a token alone it is the token's
// role where the token reaches. For a person it is the lower of their grant and the token's role, and
// nothing where the token does not reach. RoleOn("*") is the role on every instance, including those added later.
func (p Principal) RoleOn(instance string) Role {
	if !p.tokenAllows(instance) {
		return ""
	}
	if p.User == nil {
		return p.Token.Role
	}
	g := p.User.grantOn(instance)
	if g == "" {
		return ""
	}
	return minRole(g, p.Token.Role)
}

// Allowed reports whether the principal has any role on an instance.
func (p Principal) Allowed(instance string) bool { return p.RoleOn(instance) != "" }

// Can reports whether the principal holds a permission on an instance.
func (p Principal) Can(instance, perm string) bool { return p.RoleOn(instance).Can(perm) }

// CanAll reports whether the principal holds a permission on every instance: what an installation-wide
// permission needs.
func (p Principal) CanAll(perm string) bool { return p.RoleOn("*").Can(perm) }

// CanAny reports whether the principal holds a permission on at least one instance.
func (p Principal) CanAny(perm string) bool {
	if p.User == nil {
		return p.Token.Role.Can(perm)
	}
	for _, g := range p.User.Grants {
		if g.Instance == "*" {
			if p.RoleOn("*").Can(perm) || p.anyNamed(perm) {
				return true
			}
			continue
		}
		if p.Can(g.Instance, perm) {
			return true
		}
	}
	return false
}

// anyNamed is true when a named instance of the token's list gives the permission through the "*" grant.
func (p Principal) anyNamed(perm string) bool {
	for _, i := range p.Token.Instances {
		if p.Can(i, perm) {
			return true
		}
	}
	return false
}

// Instances are the instances of all that the principal has a role on, in the order of all.
func (p Principal) Instances(all []string) []string {
	out := []string{}
	for _, i := range all {
		if p.Allowed(i) {
			out = append(out, i)
		}
	}
	return out
}

// TopRole is the highest effective role anywhere, for the single "role" of GET /api/v1.
func (p Principal) TopRole() Role {
	if p.User == nil {
		return p.Token.Role
	}
	var top Role
	where := slices.Clone(p.Token.Instances) // a "*" grant reaches these through a limited token
	for _, g := range p.User.Grants {
		where = append(where, g.Instance)
	}
	for _, i := range where {
		if r := p.RoleOn(i); r.Rank() > top.Rank() {
			top = r
		}
	}
	return top
}

// Scope says where a permission holds, for queries that filter by instance: all is true when it holds on
// every instance (no filter needed), otherwise only lists the instances it holds on.
func (p Principal) Scope(perm string) (all bool, only []string) {
	if p.CanAll(perm) {
		return true, nil
	}
	if p.User == nil {
		if !p.Token.Role.Can(perm) {
			return false, nil
		}
		return false, slices.Clone(p.Token.Instances) // not unrestricted, or CanAll would have said so
	}
	seen := map[string]bool{}
	for _, g := range p.User.Grants {
		if g.Instance == "*" || seen[g.Instance] {
			continue
		}
		seen[g.Instance] = true
		if p.Can(g.Instance, perm) {
			only = append(only, g.Instance)
		}
	}
	for _, i := range p.Token.Instances {
		if !seen[i] && p.Can(i, perm) {
			only = append(only, i)
		}
	}
	slices.Sort(only)
	return false, only
}

// Key identifies the principal for rate limits: the token, and the person behind it.
func (p Principal) Key() string {
	if p.User == nil {
		return p.Token.ID
	}
	return p.Token.ID + ":" + p.User.SteamID
}

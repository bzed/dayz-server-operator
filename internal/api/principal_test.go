// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

func person(grants ...moderation.Grant) *User {
	return &User{SteamID: "76561198000000001", Name: "Ann", Grants: grants}
}

func g(instance string, role Role) moderation.Grant {
	return moderation.Grant{Instance: instance, Role: string(role)}
}

func TestRoleOn(t *testing.T) {
	for name, c := range map[string]struct {
		token     Token
		user      *User
		instance  string
		want      Role
		wantStar  Role // on every instance
		wantAllow bool
	}{
		"plain token, all instances": {Token{Role: RoleOperator}, nil, "alpha", RoleOperator, RoleOperator, true},
		"plain token, listed":        {Token{Role: RoleOperator, Instances: []string{"alpha"}}, nil, "alpha", RoleOperator, "", true},
		"plain token, not listed":    {Token{Role: RoleOperator, Instances: []string{"alpha"}}, nil, "beta", "", "", false},
		"plain detector":             {Token{Role: RoleDetector}, nil, "alpha", RoleDetector, RoleDetector, true},

		"viewer on star, operator on A":  {Token{Role: RoleAdmin}, person(g("*", RoleViewer), g("alpha", RoleOperator)), "alpha", RoleOperator, RoleViewer, true},
		"viewer on star, other instance": {Token{Role: RoleAdmin}, person(g("*", RoleViewer), g("alpha", RoleOperator)), "beta", RoleViewer, RoleViewer, true},
		"star beats a lower named grant": {Token{Role: RoleAdmin}, person(g("*", RoleOperator), g("alpha", RoleViewer)), "alpha", RoleOperator, RoleOperator, true},
		"no grant":                       {Token{Role: RoleAdmin}, person(g("alpha", RoleViewer)), "beta", "", "", false},
		"no grants at all":               {Token{Role: RoleAdmin}, person(), "alpha", "", "", false},
		"admin grant, operator token":    {Token{Role: RoleOperator}, person(g("*", RoleAdmin)), "alpha", RoleOperator, RoleOperator, true},
		"viewer grant, admin token":      {Token{Role: RoleAdmin}, person(g("*", RoleViewer)), "alpha", RoleViewer, RoleViewer, true},
		"token limited to A, star grant": {Token{Role: RoleAdmin, Instances: []string{"alpha"}}, person(g("*", RoleAdmin)), "beta", "", "", false},
		"token limited to A, on A":       {Token{Role: RoleAdmin, Instances: []string{"alpha"}}, person(g("*", RoleAdmin)), "alpha", RoleAdmin, "", true},
		"token limited to A, grant on B": {Token{Role: RoleAdmin, Instances: []string{"alpha"}}, person(g("beta", RoleAdmin)), "beta", "", "", false},
		"grant for an unknown role":      {Token{Role: RoleAdmin}, person(moderation.Grant{Instance: "alpha", Role: "root"}), "alpha", "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			p := Principal{Token: c.token, User: c.user}
			assert.Equal(t, c.want, p.RoleOn(c.instance))
			assert.Equal(t, c.wantStar, p.RoleOn("*"), "on every instance")
			assert.Equal(t, c.wantAllow, p.Allowed(c.instance))
		})
	}
}

func TestCan(t *testing.T) {
	p := Principal{Token: Token{Role: RoleAdmin}, User: person(g("alpha", RoleOperator), g("beta", RoleViewer))}
	assert.True(t, p.Can("alpha", PermKill))
	assert.False(t, p.Can("beta", PermKill))
	assert.True(t, p.Can("beta", PermView))
	assert.False(t, p.Can("gamma", PermView))

	assert.True(t, p.CanAny(PermKill), "operator on A")
	assert.False(t, p.CanAny(PermAuditView), "nobody is admin")
	assert.False(t, p.CanAll(PermKill), "no star grant")
	assert.False(t, p.CanAll(PermBanGlobal))
}

func TestInstallationPermsNeedAdminOnStar(t *testing.T) {
	for _, perm := range []string{PermAuditView, PermBanGlobal, PermBanRevokeAny, PermUsersManage} {
		assert.True(t, installationWide(perm), perm)
		admin := Principal{Token: Token{Role: RoleAdmin}, User: person(g("*", RoleAdmin))}
		assert.True(t, admin.CanAll(perm), perm)
		assert.True(t, admin.CanAny(perm), perm)
		onA := Principal{Token: Token{Role: RoleAdmin}, User: person(g("alpha", RoleAdmin))}
		assert.False(t, onA.CanAll(perm), "admin on A only: %s", perm)
		op := Principal{Token: Token{Role: RoleAdmin}, User: person(g("*", RoleOperator))}
		assert.False(t, op.CanAll(perm), "operator on star: %s", perm)
		capped := Principal{Token: Token{Role: RoleOperator}, User: person(g("*", RoleAdmin))}
		assert.False(t, capped.CanAll(perm), "an operator token caps the admin grant: %s", perm)
		limited := Principal{Token: Token{Role: RoleAdmin, Instances: []string{"alpha"}}, User: person(g("*", RoleAdmin))}
		assert.False(t, limited.CanAll(perm), "a token limited to some instances is not installation-wide: %s", perm)
	}
	assert.False(t, installationWide(PermKick))
	assert.False(t, installationWide(PermBanIP))
	assert.True(t, RoleAdmin.Can(PermUsersManage))
	assert.False(t, RoleOperator.Can(PermUsersManage))

	plain := Principal{Token: Token{Role: RoleAdmin}}
	assert.True(t, plain.CanAll(PermAuditView), "a plain admin token is unchanged")
	limitedPlain := Principal{Token: Token{Role: RoleAdmin, Instances: []string{"alpha"}}}
	assert.False(t, limitedPlain.CanAll(PermBanGlobal))
	assert.True(t, limitedPlain.CanAny(PermBanGlobal))
}

func TestInstancesAndKey(t *testing.T) {
	all := []string{"alpha", "beta", "gamma"}
	p := Principal{Token: Token{ID: "ab12", Role: RoleAdmin}, User: person(g("gamma", RoleViewer), g("alpha", RoleViewer), g("gone", RoleAdmin))}
	assert.Equal(t, []string{"alpha", "gamma"}, p.Instances(all), "in the order given, only those with a role")
	assert.Equal(t, "ab12:76561198000000001", p.Key())
	assert.Equal(t, "ab12", Principal{Token: Token{ID: "ab12"}}.Key())
	star := Principal{Token: Token{Role: RoleViewer}, User: person(g("*", RoleViewer))}
	assert.Equal(t, all, star.Instances(all))
	lim := Principal{Token: Token{Role: RoleViewer, Instances: []string{"beta"}}}
	assert.Equal(t, []string{"beta"}, lim.Instances(all))
}

func TestTopRole(t *testing.T) {
	assert.Equal(t, RoleOperator, Principal{Token: Token{Role: RoleOperator}}.TopRole())
	assert.Equal(t, RoleModerator, Principal{Token: Token{Role: RoleAdmin}, User: person(g("a", RoleViewer), g("b", RoleModerator))}.TopRole())
	assert.Equal(t, Role(""), Principal{Token: Token{Role: RoleAdmin}, User: person()}.TopRole())
}

func TestScope(t *testing.T) {
	all, only := Principal{Token: Token{Role: RoleModerator}}.Scope(PermPlayerHistory)
	assert.True(t, all)
	assert.Empty(t, only)
	all, only = Principal{Token: Token{Role: RoleModerator, Instances: []string{"alpha"}}}.Scope(PermPlayerHistory)
	assert.False(t, all)
	assert.Equal(t, []string{"alpha"}, only)
	_, only = Principal{Token: Token{Role: RoleViewer, Instances: []string{"alpha"}}}.Scope(PermPlayerHistory)
	assert.Empty(t, only, "the role lacks the permission")
	u := Principal{Token: Token{Role: RoleAdmin}, User: person(g("beta", RoleModerator), g("alpha", RoleViewer), g("gamma", RoleOperator))}
	all, only = u.Scope(PermPlayerHistory)
	assert.False(t, all)
	assert.Equal(t, []string{"beta", "gamma"}, only)
	all, _ = Principal{Token: Token{Role: RoleAdmin}, User: person(g("*", RoleModerator))}.Scope(PermPlayerHistory)
	assert.True(t, all)
}

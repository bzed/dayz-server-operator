// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/serve"
)

const (
	userSteamA = "76561198000000001"
	userSteamB = "76561198000000002"
)

// userEnv is a config under t.TempDir() and a way to look into its database.
type userEnv struct {
	t    *testing.T
	cfg  string
	conf *config.Config
}

func newUserEnv(t *testing.T) *userEnv {
	t.Helper()
	cfg, _ := adminConfig(t)
	c, err := config.Load(cfg)
	require.NoError(t, err)
	t.Setenv("USER", "bernd")
	return &userEnv{t: t, cfg: cfg, conf: c}
}

func (e *userEnv) run(args ...string) (string, error) {
	e.t.Helper()
	return runCmd(e.t, append(append([]string{"user"}, args...), "--config", e.cfg)...)
}

func (e *userEnv) store() *moderation.Store {
	e.t.Helper()
	st, err := moderation.Open(e.t.Context(), serve.BanDB(e.conf))
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = st.Close() })
	return st
}

func (e *userEnv) person(id string) moderation.Person {
	e.t.Helper()
	p, err := e.store().Person(e.t.Context(), id)
	require.NoError(e.t, err)
	return p
}

func TestUserGrantCreatesPerson(t *testing.T) {
	e := newUserEnv(t)
	out, err := e.run("grant", userSteamA, "--role", "operator", "--instance", "alpha", "--name", "Ann")
	require.NoError(t, err, out)
	assert.Contains(t, out, userSteamA)
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "operator")
	p := e.person(userSteamA)
	assert.Equal(t, "Ann", p.Name)
	assert.Equal(t, []moderation.Grant{{Instance: "alpha", Role: "operator"}}, p.Grants)

	// grant again: adds another instance, replaces the role on a listed one, keeps the rest
	out, err = e.run("grant", userSteamA, "--role", "viewer", "--instance", "alpha", "--instance", "beta")
	require.NoError(t, err, out)
	p = e.person(userSteamA)
	assert.Equal(t, "Ann", p.Name, "the name stays when --name is not given")
	assert.Equal(t, []moderation.Grant{{Instance: "alpha", Role: "viewer"}, {Instance: "beta", Role: "viewer"}}, p.Grants)
}

func TestUserGrantStarDefault(t *testing.T) {
	e := newUserEnv(t)
	out, err := e.run("grant", userSteamA, "--role", "admin")
	require.NoError(t, err, out)
	assert.Contains(t, out, "*", "every instance")
	assert.Equal(t, []moderation.Grant{{Instance: "*", Role: "admin"}}, e.person(userSteamA).Grants)
}

func TestUserGrantRefusals(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA)
	require.Error(t, err, "--role is required")
	_, err = e.run("grant", userSteamA, "--role", "root")
	require.ErrorContains(t, err, "role")
	_, err = e.run("grant", userSteamA, "--role", "detector")
	require.Error(t, err, "a role no person has")
	_, err = e.run("grant", userSteamA, "--role", "admin", "--instance", "a b")
	require.Error(t, err)
}

func TestUserBadSteamID(t *testing.T) {
	e := newUserEnv(t)
	for _, id := range []string{"123", "7656119800000000x", "66561198000000001", ""} {
		_, err := e.run("grant", id, "--role", "admin")
		require.ErrorContains(t, err, "Steam ID", id)
		_, err = e.run("revoke", id)
		require.Error(t, err, id)
		_, err = e.run("disable", id)
		require.Error(t, err, id)
	}
}

func TestUserRevokeOneInstance(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "moderator", "--instance", "alpha", "--instance", "beta")
	require.NoError(t, err)
	out, err := e.run("revoke", userSteamA, "--instance", "alpha")
	require.NoError(t, err, out)
	assert.Equal(t, []moderation.Grant{{Instance: "beta", Role: "moderator"}}, e.person(userSteamA).Grants)
	_, err = e.run("revoke", userSteamA, "--instance", "nowhere")
	require.ErrorContains(t, err, "no grant", "revoking something not granted says so")

	out, err = e.run("revoke", userSteamA)
	require.NoError(t, err, out)
	assert.Empty(t, e.person(userSteamA).Grants, "no flag revokes everything")
	_, err = e.run("revoke", userSteamB)
	require.Error(t, err, "an unknown person")
}

func TestUserRevokeEndsSessions(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "admin")
	require.NoError(t, err)
	st := e.store()
	require.NoError(t, st.CreateSession(t.Context(), "h", userSteamA, "", time.Now()))
	_, err = e.run("revoke", userSteamA)
	require.NoError(t, err)
	_, err = st.Session(t.Context(), "h", time.Now(), time.Hour, time.Hour)
	require.ErrorIs(t, err, moderation.ErrNotFound)
}

func TestUserDisableEnableDelete(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "admin", "--instance", "alpha")
	require.NoError(t, err)
	out, err := e.run("disable", userSteamA)
	require.NoError(t, err, out)
	p := e.person(userSteamA)
	assert.True(t, p.Disabled)
	assert.Len(t, p.Grants, 1, "the grants stay")
	out, err = e.run("enable", userSteamA)
	require.NoError(t, err, out)
	assert.False(t, e.person(userSteamA).Disabled)
	out, err = e.run("delete", userSteamA)
	require.NoError(t, err, out)
	_, err = e.store().Person(t.Context(), userSteamA)
	require.ErrorIs(t, err, moderation.ErrNotFound)
	for _, cmd := range []string{"disable", "enable", "delete"} {
		_, err = e.run(cmd, userSteamA)
		require.Error(t, err, cmd+" of an unknown person")
	}
}

func TestUserResetByUsername(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "admin")
	require.NoError(t, err)
	st := e.store()
	require.NoError(t, st.SetPassword(t.Context(), userSteamA, "ann", "hash"))
	require.NoError(t, st.SetTOTP(t.Context(), userSteamA, []byte("s")))
	require.NoError(t, st.CreateSession(t.Context(), "h", userSteamA, "", time.Now()))
	out, err := e.run("reset", "ANN")
	require.NoError(t, err, out)
	c, err := st.Credentials(t.Context(), userSteamA)
	require.NoError(t, err)
	assert.Equal(t, moderation.Credentials{}, c)
	_, err = st.PersonByUsername(t.Context(), "ann")
	require.ErrorIs(t, err, moderation.ErrNotFound)
	assert.Len(t, e.person(userSteamA).Grants, 1, "person and grants stay")

	require.NoError(t, st.SetPassword(t.Context(), userSteamA, "ann", "hash"))
	out, err = e.run("reset", userSteamA)
	require.NoError(t, err, out)
	_, err = st.PersonByUsername(t.Context(), "ann")
	require.ErrorIs(t, err, moderation.ErrNotFound, "by Steam ID too")
	_, err = e.run("reset", "nobody")
	require.Error(t, err)
}

func TestUserListFormat(t *testing.T) {
	e := newUserEnv(t)
	out, err := e.run("list")
	require.NoError(t, err)
	assert.Contains(t, out, "no people", "an empty list says so")
	_, err = e.run("grant", userSteamA, "--role", "admin", "--name", "Ann")
	require.NoError(t, err)
	_, err = e.run("grant", userSteamB, "--role", "viewer", "--instance", "alpha", "--instance", "beta")
	require.NoError(t, err)
	_, err = e.run("disable", userSteamB)
	require.NoError(t, err)
	require.NoError(t, e.store().SetPassword(t.Context(), userSteamA, "ann", "h"))
	out, err = e.run("list")
	require.NoError(t, err)
	assert.Regexp(t, userSteamA+`\s+Ann\s+ann\s+\*=admin`, out)
	assert.Regexp(t, userSteamB+`.*alpha=viewer,beta=viewer.*disabled`, out)
	assert.NotContains(t, out, "hash")
}

func TestUserChangesAreAudited(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "admin")
	require.NoError(t, err)
	_, err = e.run("reset", userSteamA)
	require.NoError(t, err)
	entries, err := admin.NewAudit(serve.AuditLog(e.conf)).Tail("", 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "users.reset", entries[0].Action)
	assert.Equal(t, "users.grant", entries[1].Action)
	for _, en := range entries {
		assert.Equal(t, "cli", en.Source)
		assert.Equal(t, "cli:bernd", en.Actor)
		assert.Equal(t, userSteamA, en.Target)
		assert.True(t, en.OK)
	}
	assert.Contains(t, entries[1].Detail, "*=admin")
}

func (e *userEnv) runWithStdin(stdin string, args ...string) (string, error) {
	e.t.Helper()
	return runCmdWithStdin(e.t, stdin, append(append([]string{"user"}, args...), "--config", e.cfg)...)
}

func TestUserPasswordSetsALogin(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "admin", "--name", "Ann")
	require.NoError(t, err)
	out, err := e.runWithStdin("correct horse battery\n", "password", userSteamA, "--username", "Ann.Admin")
	require.NoError(t, err, out)
	assert.Contains(t, out, "ann.admin")
	assert.NotContains(t, out, "correct horse battery", "the password is never printed")
	st := e.store()
	p, err := st.PersonByUsername(t.Context(), "ANN.admin")
	require.NoError(t, err)
	assert.Equal(t, userSteamA, p.SteamID)
	c, err := st.Credentials(t.Context(), userSteamA)
	require.NoError(t, err)
	assert.True(t, auth.CheckPassword(c.PasswordHash, "correct horse battery"))

	// by username, keeping it; a new password replaces the old one
	out, err = e.runWithStdin("another horse battery\n", "password", "ann.admin")
	require.NoError(t, err, out)
	c, _ = st.Credentials(t.Context(), userSteamA)
	assert.True(t, auth.CheckPassword(c.PasswordHash, "another horse battery"))
	assert.False(t, auth.CheckPassword(c.PasswordHash, "correct horse battery"))
}

func TestUserPasswordRefusals(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.runWithStdin("correct horse battery\n", "password", userSteamA, "--username", "ann")
	require.ErrorContains(t, err, "grant", "the person needs a role first")
	_, err = e.run("grant", userSteamA, "--role", "admin")
	require.NoError(t, err)
	_, err = e.runWithStdin("correct horse battery\n", "password", userSteamA)
	require.ErrorContains(t, err, "--username", "a first password needs a username")
	for _, bad := range []string{"ab", "has space", "a/b", "ünï"} {
		_, err = e.runWithStdin("correct horse battery\n", "password", userSteamA, "--username", bad)
		require.Error(t, err, bad)
	}
	_, err = e.runWithStdin("short\n", "password", userSteamA, "--username", "ann")
	require.ErrorContains(t, err, "12 to 256")
	_, err = e.runWithStdin("\n", "password", userSteamA, "--username", "ann")
	require.Error(t, err, "an empty password")
	c, _ := e.store().Credentials(t.Context(), userSteamA)
	assert.Empty(t, c.PasswordHash, "nothing was stored")

	_, err = e.run("grant", userSteamB, "--role", "viewer")
	require.NoError(t, err)
	_, err = e.runWithStdin("correct horse battery\n", "password", userSteamB, "--username", "ann")
	require.NoError(t, err)
	_, err = e.runWithStdin("correct horse battery\n", "password", userSteamA, "--username", "ANN")
	require.ErrorContains(t, err, "taken")
	_, err = e.runWithStdin("correct horse battery\n", "password", "nobody")
	require.Error(t, err)
}

func TestUserPasswordIsAudited(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "admin")
	require.NoError(t, err)
	_, err = e.runWithStdin("correct horse battery\n", "password", userSteamA, "--username", "ann")
	require.NoError(t, err)
	entries, err := admin.NewAudit(serve.AuditLog(e.conf)).Tail("", 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "users.password", entries[0].Action)
	assert.Equal(t, "cli:bernd", entries[0].Actor)
	assert.NotContains(t, entries[0].Detail, "correct")
}

func TestUserPasswordEndsSessions(t *testing.T) {
	e := newUserEnv(t)
	_, err := e.run("grant", userSteamA, "--role", "admin", "--name", "Ann")
	require.NoError(t, err)
	st := e.store()
	require.NoError(t, st.CreateSession(t.Context(), "hash", userSteamA, "", time.Now()))
	_, err = e.runWithStdin("correct horse battery\n", "password", userSteamA, "--username", "ann")
	require.NoError(t, err)
	_, err = st.Session(t.Context(), "hash", time.Now(), time.Hour, time.Hour)
	assert.ErrorIs(t, err, moderation.ErrNotFound, "whoever held a session is out")
}

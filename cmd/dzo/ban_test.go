// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type cliMod struct{ st *moderation.Store }

func (m cliMod) Store() *moderation.Store { return m.st }
func (m cliMod) Kick(_ context.Context, instance, steamID, reason string) error {
	if steamID == "76561198000000099" {
		return moderation.ErrNotOnline
	}
	return nil
}
func (m cliMod) Changed(context.Context, moderation.Ban, *moderation.Ban) map[string]string {
	return map[string]string{"alpha": "ok"}
}

func TestBanCommands(t *testing.T) {
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(t.Context(), filepath.Join(dir, "dzo.sqlite"))
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	srv.Moderation = cliMod{st}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	secret, _, _ := srv.Tokens.Create("cli", api.RoleAdmin, nil, true, time.Hour)
	tokenFile := filepath.Join(dir, "token")
	writeFile(t, tokenFile, secret+"\n")
	common := []string{"--api", ts.URL, "--token-file", tokenFile}
	run := func(args ...string) string {
		t.Helper()
		out, err := runCmd(t, append(args, common...)...)
		require.NoError(t, err, "%v: %s", args, out)
		return out
	}
	out := run("ban", "add", "--steam", "76561198000000001", "--reason", "aimbot", "--duration", "7d", "--instance", "alpha")
	require.Contains(t, out, "ban #1", "add")
	require.Contains(t, out, "alpha: ok", "add")
	out = run("ban", "list", "--state", "active")
	require.Contains(t, out, "aimbot", "list")
	require.Contains(t, out, "76561198000000001", "list")
	run("ban", "edit", "1", "--reason", "aimbot, confirmed", "--add-ip", "192.0.2.7")
	out = run("ban", "show", "1")
	require.Contains(t, out, "aimbot, confirmed", "show")
	require.Contains(t, out, "192.0.2.7", "show")
	require.Contains(t, out, "edit", "show")
	run("evidence", "add", "--steam", "76561198000000001", "--instance", "alpha", "--log", "speed 900 km/h")
	require.Contains(t, run("evidence", "list", "--steam", "76561198000000001"), "speed 900 km/h", "evidence list")
	run("ban", "revoke", "1", "--reason", "appeal")
	require.Contains(t, run("ban", "list", "--state", "revoked"), "revoked", "revoked list")
	out, err = runCmd(t, append([]string{"ban", "add", "--steam", "76561198000000001", "--reason", "x"}, common...)...)
	require.Error(t, err, "add without --duration succeeded: %s", out)
}

func TestHiddenAddressesAreNamedNotShown(t *testing.T) {
	b := moderation.Ban{Targets: []moderation.Target{{Kind: moderation.TargetSteam, Value: "76561198000000001"}, {Kind: moderation.TargetIP, Ref: "t3"}, {Kind: moderation.TargetIP, Value: "192.0.2.7"}}}
	require.Equal(t, "76561198000000001,ip (hidden),192.0.2.7", targetList(b))
}

func TestPlayerKickCommand(t *testing.T) {
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(t.Context(), filepath.Join(dir, "dzo.sqlite"))
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	srv.Moderation = cliMod{st}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	secret, _, _ := srv.Tokens.Create("cli", api.RoleModerator, nil, true, time.Hour)
	tokenFile := filepath.Join(dir, "token")
	writeFile(t, tokenFile, secret+"\n")
	common := []string{"--api", ts.URL, "--token-file", tokenFile}
	out, err := runCmd(t, append([]string{"player", "kick", "alpha", "76561198000000001", "AFK", "too", "long"}, common...)...)
	require.NoError(t, err, "kick: %s", out)
	_, err = runCmd(t, append([]string{"player", "kick", "alpha", "76561198000000099", "AFK"}, common...)...)
	require.ErrorContains(t, err, "not online", "a player who is not online")
	_, err = runCmd(t, append([]string{"player", "kill", "alpha", "76561198000000001"}, common...)...)
	require.ErrorContains(t, err, "--yes", "kill without --yes")
}

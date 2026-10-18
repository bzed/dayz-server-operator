// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type cliMod struct{ st *moderation.Store }

func (m cliMod) Store() *moderation.Store { return m.st }
func (m cliMod) Changed(context.Context, moderation.Ban, *moderation.Ban) map[string]string {
	return map[string]string{"alpha": "ok"}
}

func TestBanCommands(t *testing.T) {
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(context.Background(), filepath.Join(dir, "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
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
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	out := run("ban", "add", "--steam", "76561198000000001", "--reason", "aimbot", "--duration", "7d", "--instance", "alpha")
	if !strings.Contains(out, "ban #1") || !strings.Contains(out, "alpha: ok") {
		t.Fatalf("add: %s", out)
	}
	if out := run("ban", "list", "--state", "active"); !strings.Contains(out, "aimbot") || !strings.Contains(out, "76561198000000001") {
		t.Fatalf("list: %s", out)
	}
	run("ban", "edit", "1", "--reason", "aimbot, confirmed", "--add-ip", "192.0.2.7")
	out = run("ban", "show", "1")
	if !strings.Contains(out, "aimbot, confirmed") || !strings.Contains(out, "192.0.2.7") || !strings.Contains(out, "edit") {
		t.Fatalf("show: %s", out)
	}
	run("evidence", "add", "--steam", "76561198000000001", "--instance", "alpha", "--log", "speed 900 km/h")
	if out := run("evidence", "list", "--steam", "76561198000000001"); !strings.Contains(out, "speed 900 km/h") {
		t.Fatalf("evidence list: %s", out)
	}
	run("ban", "revoke", "1", "--reason", "appeal")
	if out := run("ban", "list", "--state", "revoked"); !strings.Contains(out, "revoked") {
		t.Fatalf("revoked list: %s", out)
	}
	if out, err := runCmd(t, append([]string{"ban", "add", "--steam", "76561198000000001", "--reason", "x"}, common...)...); err == nil {
		t.Fatalf("add without --duration succeeded: %s", out)
	}
}

func TestHiddenAddressesAreNamedNotShown(t *testing.T) {
	b := moderation.Ban{Targets: []moderation.Target{{Kind: moderation.TargetSteam, Value: "76561198000000001"}, {Kind: moderation.TargetIP, Ref: "t3"}, {Kind: moderation.TargetIP, Value: "192.0.2.7"}}}
	if got := targetList(b); got != "76561198000000001,ip (hidden),192.0.2.7" {
		t.Fatalf("targetList = %q", got)
	}
}

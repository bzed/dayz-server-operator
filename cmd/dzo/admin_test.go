// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/serve"
	"github.com/bzed/dayz-server-operator/internal/servermods"
	"github.com/bzed/dayz-server-operator/internal/site"
)

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// runUntil runs a long-lived command until its stderr shows `ready`, then
// cancels it and returns what it printed.
func runUntil(t testing.TB, ready string, args ...string) string {
	t.Helper()
	root := newRootCmd()
	var out syncBuf
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	var early error
	exited := false
	require.Eventually(t, func() bool {
		if strings.Contains(out.String(), ready) {
			return true
		}
		select {
		case early = <-done:
			exited = true
			return true
		default:
			return false
		}
	}, 10*time.Second, 10*time.Millisecond, "timeout waiting for %q:\n%s", ready, out.String())
	require.False(t, exited, "exited early: %v\n%s", early, out.String())
	cancel()
	require.NoError(t, <-done, "command failed:\n%s", out.String())
	return out.String()
}

func adminConfig(t testing.TB) (cfgPath, dir string) {
	t.Helper()
	dir = t.TempDir()
	cfgPath = filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, "paths:\n  data: "+dir+"\nserve:\n  listen: 127.0.0.1:0\n  mod_listen: 127.0.0.1:0\nweb:\n  listen: 127.0.0.1:0\n")
	return cfgPath, dir
}

func TestTokenCommands(t *testing.T) {
	cfg, dir := adminConfig(t)
	out, err := runCmd(t, "token", "create", "bot", "--role", "operator", "--instance", "alpha", "--config", cfg)
	require.NoError(t, err, "create: %s", out)
	require.Contains(t, out, "dzo_", "create")
	out, err = runCmd(t, "token", "list", "--config", cfg)
	require.NoError(t, err, "list: %s", out)
	require.Contains(t, out, "bot")
	require.Contains(t, out, "[alpha]")
	toks, _ := api.NewTokenStore(filepath.Join(dir, "secrets", "api-tokens.json")).List()
	require.Len(t, toks, 1)
	_, err = runCmd(t, "token", "revoke", toks[0].ID, "--config", cfg)
	require.NoError(t, err)
	_, err = runCmd(t, "token", "revoke", "nope", "--config", cfg)
	require.Error(t, err, "revoking an unknown id must fail")
	_, err = runCmd(t, "token", "create", "bad", "--role", "root", "--config", cfg)
	require.Error(t, err, "unknown role")
	out, _ = runCmd(t, "token", "list", "--config", cfg)
	require.NotContains(t, out, "bot", "revoked token still listed")
}

func TestPlayerAndVehicleCommands(t *testing.T) {
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hub := hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	secret, _, _ := srv.Tokens.Create("cli", api.RoleOperator, nil, true, time.Hour)
	tokenFile := filepath.Join(dir, "token")
	writeFile(t, tokenFile, secret+"\n")
	hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, Players: []admin.Player{{SteamID: "7656", Name: "alice"}}, Vehicles: []admin.Vehicle{{ID: "v1", Type: "Truck"}}})

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	defer func() {
		cancel()
		<-stopped
	}()
	go func() {
		defer close(stopped)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for ctx.Err() == nil {
			var res []admin.Result
			for _, c := range hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
				res = append(res, admin.Result{ID: c.ID, OK: c.SteamID != "refused", Message: "refused"})
			}
			if len(res) > 0 {
				hub.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			select {
			case <-ctx.Done():
			case <-tick.C:
			}
		}
	}()

	common := []string{"--api", ts.URL, "--token-file", tokenFile}
	run := func(args ...string) (string, error) { return runCmd(t, append(args, common...)...) }
	out, err := run("player", "list", "alpha")
	require.NoError(t, err, "player list: %s", out)
	require.Contains(t, out, "alice", "player list")
	out, err = run("vehicle", "list", "alpha")
	require.NoError(t, err, "vehicle list: %s", out)
	require.Contains(t, out, "Truck", "vehicle list")
	for _, args := range [][]string{
		{"player", "msg", "alpha", "all", "hello", "world"},
		{"player", "msg", "alpha", "7656", "hi", "--style", "important"},
		{"player", "tp", "alpha", "7656", "--x", "10", "--z", "20"},
		{"player", "tp", "alpha", "7656", "--to", "7657"},
		{"player", "heal", "alpha", "7656"},
		{"player", "fixgear", "alpha", "7656"},
		{"player", "kill", "alpha", "7656", "--yes"},
		{"player", "give", "alpha", "7656", "AKM", "--qty", "1", "--target", "hands"},
		{"vehicle", "repair", "alpha", "v1", "--scope", "wheels"},
		{"vehicle", "flip", "alpha", "v1"},
		{"vehicle", "move", "alpha", "v1", "--x", "100", "--z", "200"},
		{"flag", "raise", "alpha", "f1"},
		{"vehicle", "delete", "alpha", "v1", "--force"},
	} {
		out, err := run(args...)
		require.NoError(t, err, "%v: %s", args, out)
		require.Contains(t, out, "ok", "%v", args)
	}
	_, err = run("player", "msg", "alpha", "refused", "x")
	require.ErrorContains(t, err, "refused", "a refused action must be an error")
	_, err = run("player", "give", "alpha", "7656", "bad class")
	require.Error(t, err, "validation error must surface")
	_, err = run("player", "list", "nope")
	require.Error(t, err, "unknown instance")
	_, err = run("vehicle", "list", "nope")
	require.Error(t, err, "unknown instance")
	// Queued (wait=0 is an API feature; printResult handles Pending).
	root := &cobra.Command{}
	var pending syncBuf
	root.SetOut(&pending)
	require.NoError(t, printResult(root, api.ActionResult{ID: "abc", Pending: true}), "pending: %s", pending.String())
	require.Contains(t, pending.String(), "queued abc", "pending")
	// No token, no config.
	t.Setenv("DZO_TOKEN", "")
	_, err = runCmd(t, "player", "list", "alpha", "--api", ts.URL)
	require.ErrorContains(t, err, "no API token", "missing token")
	_, err = runCmd(t, "player", "list", "alpha", "--api", ts.URL, "--token-file", filepath.Join(dir, "nope"))
	require.Error(t, err, "unreadable token file")
	// The API URL and token default to config.yaml and $DZO_TOKEN.
	cfgPath, _ := adminConfig(t)
	t.Setenv("DZO_TOKEN", secret)
	_, err = runCmd(t, "player", "list", "alpha", "--config", cfgPath)
	require.Error(t, err, "the default API address has no server")
	_, err = runCmd(t, "player", "list", "alpha", "--config", filepath.Join(dir, "missing.yaml"))
	require.Error(t, err, "missing config")
	_ = http.StatusOK
}

func TestRenderWritesAdminConfig(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	data := filepath.Dir(cfg)
	inst := filepath.Join(data, "site", "instances", "x", "instance.yaml")
	b, _ := os.ReadFile(inst)
	writeFile(t, inst, string(b)+"mods:\n  - {local: dzo-admin, server: true}\nadmin: {deny_classes: [\"Land_*\"]}\n")
	require.NoError(t, os.MkdirAll(filepath.Join(data, "site", "localmods", "dzo-admin", "addons"), 0o750))
	out, err := runCmd(t, "instance", "render", "x", "--config", cfg, "--dry-run")
	require.NoError(t, err, "dry-run: %s", out)
	require.NotContains(t, out, "dzo-admin config", "dry-run must not write the mod config")
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "render: %s", out)
	require.Contains(t, out, "dzo-admin config written", "render")
	conf, err := os.ReadFile(filepath.Join(root, "profiles", "dzo", "admin", "config.json"))
	require.NoError(t, err, "config.json")
	require.Contains(t, string(conf), "Land_*", "config.json")
	require.Contains(t, string(conf), "http://127.0.0.1:2400/", "config.json")
	_, err = os.Stat(filepath.Join(data, "secrets", "admin", "x.token"))
	require.NoError(t, err)
}

func TestServeCommand(t *testing.T) {
	cfg, _ := adminConfig(t)
	writeFile(t, filepath.Join(filepath.Dir(cfg), "site", "instances", "a", "instance.yaml"), `name: a
product: dayz-stable
map: m
mission_source: {git: https://example.invalid/m.git, ref: main, path: x}
ports: {game: 2302}
network: host
`)
	out := runUntil(t, "mod endpoint on", "serve", "--config", cfg)
	require.Contains(t, out, "api on 127.0.0.1:")
	_, err := runCmd(t, "serve", "--config", filepath.Join(t.TempDir(), "nope.yaml"))
	require.Error(t, err, "missing config")
}

func TestWebCommand(t *testing.T) {
	cfg, dir := adminConfig(t)
	_, err := runCmd(t, "web", "--config", cfg)
	require.ErrorContains(t, err, "dzo token create", "no token file")
	writeFile(t, filepath.Join(dir, "secrets", "web.token"), "dzo_x\n")
	out := runUntil(t, "web interface on", "web", "--config", cfg)
	require.Contains(t, out, "1 backend(s)")
	// The API of another host.
	writeFile(t, filepath.Join(dir, "a.token"), "ta")
	writeFile(t, cfg, "paths:\n  data: "+dir+"\nweb:\n  listen: 127.0.0.1:0\n  api: {url: \"http://127.0.0.1:1\", token_file: "+filepath.Join(dir, "a.token")+"}\n")
	out = runUntil(t, "web interface on", "web", "--config", cfg)
	require.Contains(t, out, "1 backend(s)")
	// The removed keys are refused with a pointer to the migration notes.
	writeFile(t, cfg, "paths:\n  data: "+dir+"\nweb:\n  backends: []\n  user_header: X-User\n")
	_, err = runCmd(t, "web", "--config", cfg)
	require.ErrorContains(t, err, "migration")
	_, err = runCmd(t, "web", "--config", filepath.Join(dir, "nope.yaml"))
	require.Error(t, err, "missing config")
	// A listen address that cannot be bound fails cleanly.
	writeFile(t, cfg, "paths:\n  data: "+dir+"\nweb:\n  listen: 127.0.0.1:99999\n")
	_, err = runCmd(t, "web", "--config", cfg)
	require.Error(t, err, "bad listen")
}

func TestServermodsBuildCommand(t *testing.T) {
	src := filepath.Join(t.TempDir(), "servermods")
	writeFile(t, filepath.Join(src, "demo", "src", "Demo", "$PBOPREFIX$"), "Demo")
	writeFile(t, filepath.Join(src, "demo", "src", "Demo", "config.cpp"), "class CfgPatches {};\n")
	out := filepath.Join(t.TempDir(), "out")
	_, err := runCmd(t, "servermods", "build", "--src", src, "--out", out)
	require.ErrorContains(t, err, "commit", "a servermod that is not a git checkout has no commit for compat.yaml")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"}} {
		b, err := exec.Command("git", append([]string{"-C", filepath.Join(src, "demo")}, args...)...).CombinedOutput() //nolint:gosec // test
		require.NoError(t, err, "git %v: %s", args, b)
	}
	got, err := runCmd(t, "servermods", "build", "--src", src, "--out", out, "--server-build", "24570360")
	require.NoError(t, err, "build: %s", got)
	require.Contains(t, got, "demo.pbo", "build")
	require.Contains(t, got, "prefix Demo", "build")
	c := readFile(t, filepath.Join(out, "compat.yaml"))
	assert.Contains(t, c, "server_build: \"24570360\"", "compat.yaml")
	assert.Contains(t, c, "addons/demo.pbo", "compat.yaml")
	_, err = os.Stat(filepath.Join(out, "demo", "addons", "demo.pbo"))
	require.NoError(t, err)
	_, err = runCmd(t, "servermods", "build", "--src", filepath.Join(t.TempDir(), "none"))
	require.Error(t, err, "no servermods must fail")
}

func TestWarnStaleAdminMod(t *testing.T) {
	dir := t.TempDir()
	packaged := filepath.Join(dir, "site", "localmods", "dzo-admin")
	writeFile(t, filepath.Join(packaged, "addons", "dzoadmin.pbo"), "new build")
	sum := sha256.Sum256([]byte("new build"))
	writeFile(t, filepath.Join(dir, "site", "localmods", servermods.CompatFile), "servermods:\n  dzo-admin:\n    commit: x\n    pbos:\n      addons/dzoadmin.pbo: "+hex.EncodeToString(sum[:])+"\n")
	tree := &site.Tree{Dir: filepath.Join(dir, "site")}
	installed := filepath.Join(dir, "cache", "local", "dzo-admin", "0123abcd")
	inst := &resolve.Instance{Name: "x", Mods: []resolve.Mod{{Local: "dzo-admin", Server: true, Dir: installed}}}

	var out strings.Builder
	writeFile(t, filepath.Join(installed, "addons", "dzoadmin.pbo"), "old build")
	warnStaleAdminMod(&out, tree, inst)
	require.Contains(t, out.String(), "not the build shipped", "old build")
	require.Contains(t, out.String(), "dzo mod update x", "old build")
	out.Reset()
	writeFile(t, filepath.Join(installed, "addons", "dzoadmin.pbo"), "new build")
	warnStaleAdminMod(&out, tree, inst)
	require.Zero(t, out.Len(), "current build: %q", out.String())
	// Not installed yet: nothing to compare.
	inst.Mods[0].Dir = ""
	warnStaleAdminMod(&out, tree, inst)
	require.Zero(t, out.Len(), "not installed: %q", out.String())
}

func TestCheckModsUsable(t *testing.T) {
	dir := t.TempDir()
	mod := func(name string, files map[string]string) string {
		d := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(d, 0o750))
		for f, c := range files {
			writeFile(t, filepath.Join(d, f), c)
		}
		return d
	}
	check := func(name, d string) error {
		return checkModsUsable(&resolve.Instance{Mods: []resolve.Mod{{Name: "@" + name, Dir: d}, {Name: "@notinstalled"}}})
	}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{"ok", map[string]string{"meta.cpp": "x", "addons/a.pbo": "pbo"}, ""},
		{"ebo", map[string]string{"meta.cpp": "x", "addons/a.EBO": "ebo"}, ""},
		{"metaonly", map[string]string{"meta.cpp": "x"}, ""},
		{"nothing", map[string]string{}, "directory is empty"},
		{"stray", map[string]string{"readme.txt": "x"}, "neither addons/ nor meta.cpp"},
		{"emptyaddons", map[string]string{"meta.cpp": "x", "addons/.keep": "x"}, "no .pbo or .ebo"},
		{"zeropbo", map[string]string{"meta.cpp": "x", "addons/a.pbo": ""}, "addons/a.pbo is empty"},
	} {
		err := check(tc.name, mod(tc.name, tc.files))
		if tc.wantErr == "" {
			require.NoError(t, err, tc.name)
		} else {
			require.ErrorContains(t, err, tc.wantErr, tc.name)
			require.ErrorContains(t, err, "@"+tc.name, tc.name)
		}
	}
	if os.Geteuid() == 0 {
		return // root can read everything
	}
	// A generation that root installed: the directory is closed. Or only a file in it.
	locked := mod("locked", map[string]string{"meta.cpp": "x", "addons/a.pbo": "pbo"})
	lockedPBO := mod("lockedpbo", map[string]string{"meta.cpp": "x", "addons/a.pbo": "pbo"})
	require.NoError(t, os.Chmod(filepath.Join(lockedPBO, "addons", "a.pbo"), 0))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })
	for name, d := range map[string]string{"locked": locked, "lockedpbo": lockedPBO} {
		err := check(name, d)
		require.ErrorContains(t, err, "@"+name, name)
		assert.ErrorContains(t, err, "chown -R dayz:dayz", name)
	}
}

func TestRenderWritesBansTxtFromTheDatabase(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	out, err := runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "render without a database: %s", out)
	require.Contains(t, out, "no ban database yet", "render without a database")
	c, err := config.Load(cfg)
	require.NoError(t, err)
	ctx := t.Context()
	st, err := moderation.Open(ctx, serve.BanDB(c))
	require.NoError(t, err)
	guid := "0123456789abcdef0123456789abcdef"
	_, err = st.CreateBan(ctx, "test", moderation.BanInput{GUIDs: []string{guid}, Reason: "internal", Message: "cheater", Scope: moderation.ScopeGlobal}, time.Now())
	require.NoError(t, err)
	require.NoError(t, st.Close())
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "render: %s", out)
	require.Contains(t, out, "bans.txt written (1 entries)", "render")
	b, err := os.ReadFile(filepath.Join(root, "profiles", "battleye", "bans.txt"))
	require.NoError(t, err, "bans.txt")
	require.Contains(t, string(b), guid+" -1 cheater", "bans.txt")
}

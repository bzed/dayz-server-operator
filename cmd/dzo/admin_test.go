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
func runUntil(t *testing.T, ready string, args ...string) string {
	t.Helper()
	root := newRootCmd()
	var out syncBuf
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(out.String(), ready) {
		select {
		case err := <-done:
			t.Fatalf("exited early: %v\n%s", err, out.String())
		case <-deadline:
			t.Fatalf("timeout waiting for %q:\n%s", ready, out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("command failed: %v\n%s", err, out.String())
	}
	return out.String()
}

func adminConfig(t *testing.T) (cfgPath, dir string) {
	t.Helper()
	dir = t.TempDir()
	cfgPath = filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, "paths:\n  data: "+dir+"\nserve:\n  listen: 127.0.0.1:0\n  mod_listen: 127.0.0.1:0\nweb:\n  listen: 127.0.0.1:0\n")
	return cfgPath, dir
}

func TestTokenCommands(t *testing.T) {
	cfg, dir := adminConfig(t)
	out, err := runCmd(t, "token", "create", "bot", "--role", "operator", "--instance", "alpha", "--config", cfg)
	if err != nil || !strings.Contains(out, "dzo_") {
		t.Fatalf("create: %v\n%s", err, out)
	}
	out, err = runCmd(t, "token", "list", "--config", cfg)
	if err != nil || !strings.Contains(out, "bot") || !strings.Contains(out, "[alpha]") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	toks, _ := api.NewTokenStore(filepath.Join(dir, "secrets", "api-tokens.json")).List()
	if len(toks) != 1 {
		t.Fatalf("tokens = %v", toks)
	}
	if _, err := runCmd(t, "token", "revoke", toks[0].ID, "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "token", "revoke", "nope", "--config", cfg); err == nil {
		t.Fatal("revoking an unknown id must fail")
	}
	if _, err := runCmd(t, "token", "create", "bad", "--role", "root", "--config", cfg); err == nil {
		t.Fatal("unknown role")
	}
	if out, _ := runCmd(t, "token", "list", "--config", cfg); strings.Contains(out, "bot") {
		t.Fatal("revoked token still listed")
	}
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			var res []admin.Result
			for _, c := range hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
				res = append(res, admin.Result{ID: c.ID, OK: c.SteamID != "refused", Message: "refused"})
			}
			if len(res) > 0 {
				hub.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			time.Sleep(time.Millisecond)
		}
	}()

	common := []string{"--api", ts.URL, "--token-file", tokenFile}
	run := func(args ...string) (string, error) { return runCmd(t, append(args, common...)...) }
	if out, err := run("player", "list", "alpha"); err != nil || !strings.Contains(out, "alice") {
		t.Fatalf("player list: %v\n%s", err, out)
	}
	if out, err := run("vehicle", "list", "alpha"); err != nil || !strings.Contains(out, "Truck") {
		t.Fatalf("vehicle list: %v\n%s", err, out)
	}
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
		{"flag", "raise", "alpha", "f1"},
		{"vehicle", "delete", "alpha", "v1", "--force"},
	} {
		if out, err := run(args...); err != nil || !strings.Contains(out, "ok") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if _, err := run("player", "msg", "alpha", "refused", "x"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused action must be an error: %v", err)
	}
	if _, err := run("player", "give", "alpha", "7656", "bad class"); err == nil {
		t.Fatal("validation error must surface")
	}
	if _, err := run("player", "list", "nope"); err == nil {
		t.Fatal("unknown instance")
	}
	if _, err := run("vehicle", "list", "nope"); err == nil {
		t.Fatal("unknown instance")
	}
	// Queued (wait=0 is an API feature; printResult handles Pending).
	root := &cobra.Command{}
	var out syncBuf
	root.SetOut(&out)
	if err := printResult(root, api.ActionResult{ID: "abc", Pending: true}); err != nil || !strings.Contains(out.String(), "queued abc") {
		t.Fatalf("pending: %v %s", err, out.String())
	}
	// No token, no config.
	t.Setenv("DZO_TOKEN", "")
	if _, err := runCmd(t, "player", "list", "alpha", "--api", ts.URL); err == nil || !strings.Contains(err.Error(), "no API token") {
		t.Fatalf("missing token: %v", err)
	}
	if _, err := runCmd(t, "player", "list", "alpha", "--api", ts.URL, "--token-file", filepath.Join(dir, "nope")); err == nil {
		t.Fatal("unreadable token file")
	}
	// The API URL and token default to config.yaml and $DZO_TOKEN.
	cfgPath, _ := adminConfig(t)
	t.Setenv("DZO_TOKEN", secret)
	if _, err := runCmd(t, "player", "list", "alpha", "--config", cfgPath); err == nil {
		t.Fatal("the default API address has no server")
	}
	if _, err := runCmd(t, "player", "list", "alpha", "--config", filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("missing config")
	}
	_ = http.StatusOK
}

func TestRenderWritesAdminConfig(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	data := filepath.Dir(cfg)
	inst := filepath.Join(data, "site", "instances", "x", "instance.yaml")
	b, _ := os.ReadFile(inst)
	writeFile(t, inst, string(b)+"mods:\n  - {local: dzo-admin, server: true}\nadmin: {deny_classes: [\"Land_*\"]}\n")
	if err := os.MkdirAll(filepath.Join(data, "site", "localmods", "dzo-admin", "addons"), 0o750); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(t, "instance", "render", "x", "--config", cfg, "--dry-run"); err != nil || strings.Contains(out, "dzo-admin config") {
		t.Fatalf("dry-run must not write the mod config: %v\n%s", err, out)
	}
	out, err := runCmd(t, "instance", "render", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "dzo-admin config written") {
		t.Fatalf("render: %v\n%s", err, out)
	}
	conf, err := os.ReadFile(filepath.Join(root, "profiles", "dzo", "admin", "config.json"))
	if err != nil || !strings.Contains(string(conf), "Land_*") || !strings.Contains(string(conf), "http://127.0.0.1:2400/") {
		t.Fatalf("config.json: %v\n%s", err, conf)
	}
	if _, err := os.Stat(filepath.Join(data, "secrets", "admin", "x.token")); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(out, "api on 127.0.0.1:") {
		t.Fatalf("output: %s", out)
	}
	if _, err := runCmd(t, "serve", "--config", filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("missing config")
	}
}

func TestWebCommand(t *testing.T) {
	cfg, dir := adminConfig(t)
	if _, err := runCmd(t, "web", "--config", cfg); err == nil || !strings.Contains(err.Error(), "dzo token create") {
		t.Fatalf("no token file: %v", err)
	}
	writeFile(t, filepath.Join(dir, "secrets", "web.token"), "dzo_x\n")
	out := runUntil(t, "web interface on", "web", "--config", cfg)
	if !strings.Contains(out, "1 backend(s)") {
		t.Fatalf("output: %s", out)
	}
	// Explicit backends, several installations.
	writeFile(t, filepath.Join(dir, "a.token"), "ta")
	writeFile(t, cfg, "paths:\n  data: "+dir+"\nweb:\n  listen: 127.0.0.1:0\n  backends:\n    - {name: one, url: http://127.0.0.1:1, token_file: "+filepath.Join(dir, "a.token")+"}\n    - {name: two, url: http://127.0.0.1:2, token_file: "+filepath.Join(dir, "a.token")+"}\n")
	if out := runUntil(t, "web interface on", "web", "--config", cfg); !strings.Contains(out, "2 backend(s)") {
		t.Fatalf("output: %s", out)
	}
	if _, err := runCmd(t, "web", "--config", filepath.Join(dir, "nope.yaml")); err == nil {
		t.Fatal("missing config")
	}
	// A listen address that cannot be bound fails cleanly.
	writeFile(t, cfg, "paths:\n  data: "+dir+"\nweb:\n  listen: 127.0.0.1:99999\n")
	if _, err := runCmd(t, "web", "--config", cfg); err == nil {
		t.Fatal("bad listen")
	}
}

func TestServermodsBuildCommand(t *testing.T) {
	src := filepath.Join(t.TempDir(), "servermods")
	writeFile(t, filepath.Join(src, "demo", "src", "Demo", "$PBOPREFIX$"), "Demo")
	writeFile(t, filepath.Join(src, "demo", "src", "Demo", "config.cpp"), "class CfgPatches {};\n")
	out := filepath.Join(t.TempDir(), "out")
	if _, err := runCmd(t, "servermods", "build", "--src", src, "--out", out); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("a servermod that is not a git checkout has no commit for compat.yaml: %v", err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"}} {
		if b, err := exec.Command("git", append([]string{"-C", filepath.Join(src, "demo")}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // test
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	got, err := runCmd(t, "servermods", "build", "--src", src, "--out", out, "--server-build", "24570360")
	if err != nil || !strings.Contains(got, "demo.pbo") || !strings.Contains(got, "prefix Demo") {
		t.Fatalf("build: %v\n%s", err, got)
	}
	if c := readFile(t, filepath.Join(out, "compat.yaml")); !strings.Contains(c, "server_build: \"24570360\"") || !strings.Contains(c, "addons/demo.pbo") {
		t.Errorf("compat.yaml = %s", c)
	}
	if _, err := os.Stat(filepath.Join(out, "demo", "addons", "demo.pbo")); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "servermods", "build", "--src", filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("no servermods must fail")
	}
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
	if !strings.Contains(out.String(), "not the build shipped") || !strings.Contains(out.String(), "dzo mod update x") {
		t.Fatalf("old build: %q", out.String())
	}
	out.Reset()
	writeFile(t, filepath.Join(installed, "addons", "dzoadmin.pbo"), "new build")
	warnStaleAdminMod(&out, tree, inst)
	if out.Len() != 0 {
		t.Fatalf("current build: %q", out.String())
	}
	// Not installed yet: nothing to compare.
	inst.Mods[0].Dir = ""
	warnStaleAdminMod(&out, tree, inst)
	if out.Len() != 0 {
		t.Fatalf("not installed: %q", out.String())
	}
}

func TestCheckModsUsable(t *testing.T) {
	dir := t.TempDir()
	mod := func(name string, files map[string]string) string {
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
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
		if err := check(tc.name, mod(tc.name, tc.files)); tc.wantErr == "" && err != nil ||
			tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "@"+tc.name)) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
		}
	}
	if os.Geteuid() == 0 {
		return // root can read everything
	}
	// A generation that root installed: the directory is closed. Or only a file in it.
	locked := mod("locked", map[string]string{"meta.cpp": "x", "addons/a.pbo": "pbo"})
	lockedPBO := mod("lockedpbo", map[string]string{"meta.cpp": "x", "addons/a.pbo": "pbo"})
	if err := os.Chmod(filepath.Join(lockedPBO, "addons", "a.pbo"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })
	for name, d := range map[string]string{"locked": locked, "lockedpbo": lockedPBO} {
		if err := check(name, d); err == nil || !strings.Contains(err.Error(), "@"+name) || !strings.Contains(err.Error(), "chown -R dayz:dayz") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRenderWritesBansTxtFromTheDatabase(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	out, err := runCmd(t, "instance", "render", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "no ban database yet") {
		t.Fatalf("render without a database: %v\n%s", err, out)
	}
	c, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := moderation.Open(ctx, serve.BanDB(c))
	if err != nil {
		t.Fatal(err)
	}
	guid := "0123456789abcdef0123456789abcdef"
	if _, err := st.CreateBan(ctx, "test", moderation.BanInput{GUIDs: []string{guid}, Reason: "internal", Message: "cheater", Scope: moderation.ScopeGlobal}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	if err != nil || !strings.Contains(out, "bans.txt written (1 entries)") {
		t.Fatalf("render: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(root, "profiles", "battleye", "bans.txt"))
	if err != nil || !strings.Contains(string(b), guid+" -1 cheater") {
		t.Fatalf("bans.txt: %v\n%s", err, b)
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

const instanceYAML = `name: %s
product: dayz-stable
map: dayzOffline.chernarusplus
mission_source: {git: https://example.invalid/m.git, ref: main, path: mpmissions/x}
ports: {game: %d, rcon: %d, query: %d}
network: %s
mods:
  - {local: dzo-admin, server: true}
admin: {deny_classes: ["Land_*"], vehicles_s: 30}
admin_map:
  watch:
    - {layer: fire, classes: [FireplaceBase], icon: fire}
`

func fixture(t *testing.T) (*config.Config, *site.Tree) {
	t.Helper()
	data := t.TempDir()
	siteDir := filepath.Join(data, "site")
	for i, n := range []struct{ name, network string }{{"alpha", "host"}, {"beta", "publish"}} {
		dir := filepath.Join(siteDir, "instances", n.name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		y := fmt.Sprintf(instanceYAML, n.name, 2302+10*i, 2303+10*i, 27016+10*i, n.network)
		if err := os.WriteFile(filepath.Join(dir, "instance.yaml"), []byte(y), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(siteDir, "localmods", "dzo-admin", "addons"), 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Paths = config.Paths{Data: data, Instances: data + "/instances", Snapshots: data + "/snapshots", Cache: data + "/cache", Secrets: data + "/secrets", DB: data + "/db", Site: siteDir}
	cfg.Serve.Listen, cfg.Serve.ModListen = "127.0.0.1:0", "127.0.0.1:0"
	tree, err := site.LoadTree(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, tree
}

func TestModConfigAndEndpoint(t *testing.T) {
	cfg, tree := fixture(t)
	insts, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		t.Fatal(err)
	}
	if !insts[0].AdminEnabled() {
		t.Fatal("dzo-admin in mods must enable the integration")
	}
	cfg.Serve.ModListen = "127.0.0.1:2400"
	alpha, _ := ModEndpoint(cfg, insts[0])
	beta, _ := ModEndpoint(cfg, insts[1])
	if alpha != "http://127.0.0.1:2400/" || beta != "http://host.containers.internal:2400/" {
		t.Fatalf("endpoints %q %q", alpha, beta)
	}
	mc, err := ModConfig(cfg, insts[0])
	if err != nil || !mc.AllowSpawn || mc.VehiclesS != 30 || mc.DenyClasses[0] != "Land_*" || len(mc.Watch) != len(admin.VanillaEventWatch())+1 || mc.Watch[len(mc.Watch)-1].Layer != "fire" {
		t.Fatalf("mod config %+v %v", mc, err)
	}
	// The events of the Central Economy can be switched off for an instance.
	off := false
	insts[0].AdminMap.VanillaEvents = &off
	if mc, err = ModConfig(cfg, insts[0]); err != nil || len(mc.Watch) != 1 {
		t.Fatalf("without the vanilla events: %+v %v", mc.Watch, err)
	}
	insts[0].AdminMap.VanillaEvents = nil
	if err := WriteModConfig(cfg, insts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(TokenDir(cfg), "alpha.token")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(insts[0].Paths.Profiles, "dzo", "admin", "config.json")); err != nil {
		t.Fatal(err)
	}
	cfg.Serve.ModListen = "nonsense"
	if _, err := ModEndpoint(cfg, insts[0]); err == nil {
		t.Fatal("bad mod_listen must fail")
	}
	if err := WriteModConfig(cfg, insts[0]); err == nil {
		t.Fatal("bad mod_listen must fail the config write")
	}
	insts[0].Admin.Disabled = true
	if insts[0].AdminEnabled() {
		t.Fatal("admin.disabled")
	}
}

func TestRunEndToEnd(t *testing.T) {
	cfg, tree := fixture(t)
	insts, _ := resolve.ResolveAll(cfg, tree)
	if err := WriteModConfig(cfg, insts[0]); err != nil {
		t.Fatal(err)
	}
	secret, _, err := api.NewTokenStore(APITokens(cfg)).Create("op", api.RoleOperator, nil, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenB, _ := os.ReadFile(filepath.Join(TokenDir(cfg), "alpha.token"))

	reload := make(chan struct{})
	ready := make(chan [2]string, 1)
	loads := 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{
			Version: "test", Reload: reload,
			Load: func() (*site.Tree, error) {
				loads++
				if loads == 3 {
					return nil, os.ErrInvalid
				}
				return tree, nil
			},
			Ready: func(a, m string) { ready <- [2]string{a, m} },
			Logf:  t.Logf,
		})
	}()
	addrs := <-ready

	// The mod syncs against the mod listener with the rendered token.
	body := `{"token":"` + strings.TrimSpace(string(tokenB)) + `","protocol":1,"hello":true,"players":[{"steam_id":"7656"}]}`
	resp, err := http.Post("http://"+addrs[1]+"/mod/v1/sync", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("mod sync: %v %v", resp, err)
	}
	_ = resp.Body.Close()

	// And an operator sees it through the API.
	c := apiclient.New("http://"+addrs[0], secret)
	players, err := c.Players(context.Background(), "alpha")
	if err != nil || len(players) != 1 {
		t.Fatalf("players = %v %v", players, err)
	}

	reload <- struct{}{} // ok
	reload <- struct{}{} // load fails: keeps running
	reload <- struct{}{}
	if _, err := c.Players(context.Background(), "alpha"); err != nil {
		t.Fatalf("after reloads: %v", err)
	}
	if got, _ := c.Players(context.Background(), "alpha"); len(got) != 1 {
		t.Fatal("a reload must keep the world state")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = admin.ProtocolVersion
}

func TestRunErrors(t *testing.T) {
	cfg, tree := fixture(t)
	load := func() (*site.Tree, error) { return tree, nil }
	if err := Run(context.Background(), cfg, Options{Load: func() (*site.Tree, error) { return nil, os.ErrInvalid }}); err == nil {
		t.Fatal("load error must fail Run")
	}
	cfg.Serve.Listen = "bad"
	if err := Run(context.Background(), cfg, Options{Load: load}); err == nil {
		t.Fatal("bad api listen")
	}
	cfg.Serve.Listen, cfg.Serve.ModListen = "127.0.0.1:0", "bad"
	if err := Run(context.Background(), cfg, Options{Load: load}); err == nil {
		t.Fatal("bad mod listen")
	}
}

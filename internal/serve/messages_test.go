// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

func TestInstanceMessagesWelcomeFirst(t *testing.T) {
	custom, off := "Hi #name", ""
	got := instanceMessages(&resolve.Instance{Welcome: &custom, Messages: []site.Message{{Text: "rules", Repeat: 5}}})
	if len(got) != 2 || got[0].Text != "Hi #name" || !got[0].OnConnect || got[1].Text != "rules" {
		t.Errorf("messages = %+v", got)
	}
	if got := instanceMessages(&resolve.Instance{}); len(got) != 1 || got[0].Text != site.DefaultWelcome {
		t.Errorf("default welcome: %+v", got)
	}
	if got := instanceMessages(&resolve.Instance{Welcome: &off}); len(got) != 0 {
		t.Errorf("no welcome and no messages sends nothing: %+v", got)
	}
}

func TestStartMessengersStartsAndStops(t *testing.T) {
	cfg, tree := fixture(t)
	reg := admin.NewRegistry(TokenDir(cfg))
	if err := AddHubs(reg, cfg, tree); err != nil {
		t.Fatal(err)
	}
	ms, err := startMessengers(context.Background(), cfg, tree, reg, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	ms.stop() // returns: the messengers end with their context
}

func TestStartMessengersWithoutModUsesRCon(t *testing.T) {
	cfg, _ := fixture(t)
	for _, n := range []string{"alpha", "beta"} {
		p := filepath.Join(cfg.Paths.Site, "instances", n, "instance.yaml")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), "  - {local: dzo-admin, server: true}\n", "", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := site.LoadTree(cfg.Paths.Site)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := startMessengers(context.Background(), cfg, tree, admin.NewRegistry(TokenDir(cfg)), func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	ms.stop()
	if _, err := os.Stat(filepath.Join(cfg.Paths.Secrets, "rcon", "alpha")); err != nil {
		t.Errorf("the RCon backend needs the instance's password: %v", err)
	}
}

func TestServerNameIsTheHostnameOfServerDZCfg(t *testing.T) {
	site := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		dir := filepath.Join(site, "instances", name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, "serverDZ.cfg"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("a", "// a comment\nhostname = \"My DayZ Server\";  // shown in the browser\nmaxPlayers = 60;\n")
	write("b", "maxPlayers = 60;\n")
	write("c", "hostname = \"\";\n")
	write("d", "this is not a config {{{")
	write("e", "")
	for name, want := range map[string]string{
		"a": "My DayZ Server", // the hostname
		"b": "b",              // none set: the instance name
		"c": "c",              // empty
		"d": "d",              // does not parse
		"e": "e",              // no file
	} {
		if got := serverName(site, name); got != want {
			t.Errorf("serverName(%s) = %q, want %q", name, got, want)
		}
	}
}

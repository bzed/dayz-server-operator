// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstanceMessagesWelcomeFirst(t *testing.T) {
	custom, off := "Hi #name", ""
	got := instanceMessages(&resolve.Instance{Welcome: &custom, Messages: []site.Message{{Text: "rules", Repeat: 5}}})
	require.Len(t, got, 2)
	assert.Equal(t, "Hi #name", got[0].Text)
	assert.True(t, got[0].OnConnect)
	assert.Equal(t, "rules", got[1].Text)
	def := instanceMessages(&resolve.Instance{})
	require.Len(t, def, 1, "default welcome")
	assert.Equal(t, site.DefaultWelcome, def[0].Text)
	assert.Empty(t, instanceMessages(&resolve.Instance{Welcome: &off}), "no welcome and no messages sends nothing")
}

func TestStartMessengersStartsAndStops(t *testing.T) {
	cfg, tree := fixture(t)
	reg := admin.NewRegistry(TokenDir(cfg))
	require.NoError(t, AddHubs(reg, cfg, tree))
	ms, err := startMessengers(t.Context(), cfg, tree, reg, func(string, ...any) {})
	require.NoError(t, err)
	ms.stop() // returns: the messengers end with their context
}

func TestStartMessengersWithoutModUsesRCon(t *testing.T) {
	cfg, _ := fixture(t)
	for _, n := range []string{"alpha", "beta"} {
		p := filepath.Join(cfg.Paths.Site, "instances", n, "instance.yaml")
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, []byte(strings.Replace(string(b), "  - {local: dzo-admin, server: true}\n", "", 1)), 0o600))
	}
	tree, err := site.LoadTree(cfg.Paths.Site)
	require.NoError(t, err)
	ms, err := startMessengers(t.Context(), cfg, tree, admin.NewRegistry(TokenDir(cfg)), func(string, ...any) {})
	require.NoError(t, err)
	ms.stop()
	_, err = os.Stat(filepath.Join(cfg.Paths.Secrets, "rcon", "alpha"))
	assert.NoError(t, err, "the RCon backend needs the instance's password")
}

func TestServerNameIsTheHostnameOfServerDZCfg(t *testing.T) {
	site := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		dir := filepath.Join(site, "instances", name)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		if body != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "serverDZ.cfg"), []byte(body), 0o600))
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
		assert.Equal(t, want, serverName(site, name), "serverName(%s)", name)
	}
}

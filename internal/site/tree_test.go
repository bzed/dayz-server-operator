// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/yaml.v3"
)

func write(t testing.TB, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	err := os.MkdirAll(filepath.Dir(p), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(p, []byte(content), 0o600)
	require.NoError(t, err)
}

const goodInstance = `name: a
product: dayz-stable
map: m
mission_source: {preset: vanilla}
ports: {game: 2302}
network: host
mods: [{local: tools, server: true}]
`

func TestLoadTree(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "site.yaml", "image: img\nlocal_mods:\n  tools: {path: /srv/tools}\n")
	write(t, dir, "integrations/maps/vanilla.yaml", "git: g\nref: r\npath: p\n")
	write(t, dir, "instances/a/instance.yaml", goodInstance)
	tree, err := LoadTree(dir)
	require.NoError(t, err, "LoadTree")
	require.Equal(t, "img", tree.Site.Image)
	require.Equal(t, "r", tree.Maps["vanilla"].Ref)
	assert.Len(t, tree.InstanceNames(), 1)
	_, err = tree.Instance("a")
	require.NoError(t, err)
	_, err = tree.Instance("zz")
	require.Error(t, err)
}

func TestLoadTreeEmptyAndMissingSiteYAML(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadTree(dir)
	require.NoError(t, err, "no site.yaml")
	write(t, dir, "site.yaml", "")
	_, err = LoadTree(dir)
	require.NoError(t, err, "empty site.yaml")
}

func TestLoadTreeErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"unknown key", map[string]string{"site.yaml": "imagee: x\n"}, "imagee"},
		{"bad local mod", map[string]string{"site.yaml": "local_mods: {x: {url: u}}\n"}, "sha256 is required"},
		{"local mod both", map[string]string{"site.yaml": "local_mods: {x: {url: u, sha256: s, path: /p}}\n"}, "exactly one"},
		{"defaults message never sent", map[string]string{"site.yaml": "defaults: {messages: [{text: hi}]}\n"}, "defaults.messages"},
		{"defaults welcome too long", map[string]string{"site.yaml": "defaults: {welcome: \"" + strings.Repeat("a", 161) + "\"}\n"}, "defaults.welcome"},
		{"local mod rel path", map[string]string{"site.yaml": "local_mods: {x: {path: rel}}\n"}, "absolute"},
		{"bad preset", map[string]string{"integrations/maps/v.yaml": "gitt: x\n"}, "gitt"},
		{"name mismatch", map[string]string{"instances/b/instance.yaml": goodInstance}, "does not match directory"},
		{"invalid instance", map[string]string{"instances/a/instance.yaml": "name: a\n"}, "product is required"},
		{"bad instance yaml", map[string]string{"instances/a/instance.yaml": "name: [\n"}, "parse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, c := range tt.files {
				write(t, dir, rel, c)
			}
			_, err := LoadTree(dir)
			require.Error(t, err, "LoadTree() = %v, want %q", err, tt.want)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestLoadTreeUnreadableSiteYAML(t *testing.T) {
	dir := t.TempDir()
	err := os.Mkdir(filepath.Join(dir, "site.yaml"), 0o750)
	require.NoError(t, err)
	_, err = LoadTree(dir)
	require.Error(t, err)
}

func TestAddMod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances", "a", "instance.yaml")
	write(t, dir, "instances/a/instance.yaml", "# my server\nname: a\nproduct: dayz-stable   # stable\nmap: m\nmission_source: {preset: vanilla}\nports: {game: 2302}\nnetwork: host\n")

	err := AddMod(path, ModRef{ID: 1559212036})
	require.NoError(t, err, "AddMod")
	err = AddMod(path, ModRef{Local: "tools", Server: true})
	require.NoError(t, err, "AddMod local")
	got, _ := os.ReadFile(path)
	for _, want := range []string{"# my server", "# stable", "- {id: 1559212036}", "- {local: tools, server: true}"} {
		assert.Contains(t, string(got), want)
	}
	// the result still loads
	write(t, dir, "integrations/maps/vanilla.yaml", "git: g\nref: r\npath: p\n")
	tree, err := LoadTree(dir)
	require.NoError(t, err)
	require.Len(t, tree.Instances["a"].Mods, 2)
	err = AddMod(path, ModRef{ID: 1559212036})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already lists")
}

func TestAddModErrors(t *testing.T) {
	dir := t.TempDir()
	err := AddMod(filepath.Join(dir, "missing.yaml"), ModRef{ID: 1})
	require.Error(t, err)
	err = AddMod(filepath.Join(dir, "x.yaml"), ModRef{})
	require.Error(t, err, "an invalid ref must fail")
	write(t, dir, "list.yaml", "- a\n")
	err = AddMod(filepath.Join(dir, "list.yaml"), ModRef{ID: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a YAML mapping")
	write(t, dir, "bad.yaml", "a: [\n")
	err = AddMod(filepath.Join(dir, "bad.yaml"), ModRef{ID: 1})
	require.Error(t, err, "broken YAML must fail")
}

func TestLoadTreeIntegrationsAndOverlays(t *testing.T) {
	dir := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		require.NoError(t, err)
		err = os.WriteFile(p, []byte(body), 0o600)
		require.NoError(t, err)
	}
	put("instances/a/instance.yaml", "name: a\nproduct: dayz-stable\nmap: m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 1, rcon: 2, query: 3}\nnetwork: host\n")
	put("integrations/mods/10/integration.yaml", "mod: 10\nname: Shared\naliases: [11]\nfiles:\n  types.xml: {source: local, path: t.xml}\n")
	put("instances/a/integrations/10/integration.yaml", "mod: 10\nname: Own\nfiles:\n  types.xml: {source: local, path: t.xml}\n")
	put("overlays/shared/x.json", "{}")
	put("instances/a/overlays/own/x.json", "{}")
	put("overlays/own/x.json", "{}")
	tr, err := LoadTree(dir)
	require.NoError(t, err)
	li, ok := tr.IntegrationFor("a", 10)
	require.True(t, ok, "an instance's own integration wins: %+v", li)
	require.Equal(t, "Own", li.Name)
	assert.Equal(t, filepath.Join(dir, "instances", "a", "integrations", "10"), li.Dir)
	li, ok = tr.IntegrationFor("a", 11)
	require.True(t, ok, "an alias finds the shared integration: %+v", li)
	assert.Equal(t, "Shared", li.Name)
	_, ok = tr.IntegrationFor("a", 12)
	assert.False(t, ok, "a mod without integration has none")
	d, ok := tr.OverlayDir("a", "own")
	require.True(t, ok, "an instance overlay shadows the shared one: %q", d)
	assert.Equal(t, filepath.Join(dir, "instances", "a", "overlays", "own"), d)
	d, ok = tr.OverlayDir("a", "shared")
	require.True(t, ok, "shared overlay: %q", d)
	assert.Equal(t, filepath.Join(dir, "overlays", "shared"), d)
	_, ok = tr.OverlayDir("a", "nope")
	assert.False(t, ok, "an unknown overlay is not found")

	put("integrations/mods/20/integration.yaml", "mod: 20\nname: Bad\nfiles:\n  types.xml: {source: nowhere}\n")
	_, err = LoadTree(dir)
	require.Error(t, err, "an invalid integration must fail the load")
	put("integrations/mods/20/integration.yaml", "mod: 20\nname: Typo\nfilez: {}\n")
	_, err = LoadTree(dir)
	require.Error(t, err, "an unknown key in an integration must fail the load")
}

func TestRemoveAndMoveMod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instance.yaml")
	src := "name: a  # keep this comment\nmods:\n  - {id: 1}\n  - {id: 2, server: true}\n  - {local: tools, server: true}\n"
	err := os.WriteFile(path, []byte(src), 0o600)
	require.NoError(t, err)
	order := func() string {
		t.Helper()
		b, _ := os.ReadFile(path) //nolint:gosec // test fixture
		var in struct {
			Mods []ModRef `yaml:"mods"`
		}
		err := yaml.Unmarshal(b, &in)
		require.NoError(t, err)
		var keys []string
		for _, m := range in.Mods {
			keys = append(keys, m.Key())
		}
		return strings.Join(keys, ",")
	}
	err = MoveMod(path, "tools", "1", false)
	require.NoError(t, err, "before: %v %s", err, order())
	require.Equal(t, "tools,1,2", order(), "before: %v %s", err, order())
	err = MoveMod(path, "tools", "2", true)
	require.NoError(t, err, "after: %v %s", err, order())
	require.Equal(t, "1,2,tools", order(), "after: %v %s", err, order())
	err = RemoveMod(path, "2")
	require.NoError(t, err, "remove: %v %s", err, order())
	require.Equal(t, "1,tools", order(), "remove: %v %s", err, order())
	b, _ := os.ReadFile(path)
	assert.Contains(t, string(b), "# keep this comment", "the comments of the file must survive")
	for name, err := range map[string]error{
		"unknown mod":  RemoveMod(path, "99"),
		"move unknown": MoveMod(path, "99", "1", false),
		"move to none": MoveMod(path, "1", "99", false),
		"itself":       MoveMod(path, "1", "1", false),
		"no file":      RemoveMod(filepath.Join(dir, "none.yaml"), "1"),
	} {
		require.Error(t, err, "%s must fail", name)
	}
	err = os.WriteFile(path, []byte("name: a\n"), 0o600)
	require.NoError(t, err)
	err = RemoveMod(path, "1")
	require.Error(t, err, "a file without a mods list must fail")
}

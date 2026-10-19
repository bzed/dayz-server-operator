// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultIsValid(t *testing.T) {
	require.NoError(t, Default().Validate(), "Default() should validate cleanly")
}

func TestParseAppliesDefaultsAndTemplating(t *testing.T) {
	c, err := Parse([]byte(`
paths:
  data: /srv/dayz
site:
  remote: https://example.invalid/site.git
  branch: main
`))
	require.NoError(t, err)
	assert.Equal(t, "/srv/dayz", c.Paths.Data)
	assert.Equal(t, "/srv/dayz/instances", c.Paths.Instances)
	assert.Equal(t, "/srv/dayz/snapshots", c.Paths.Snapshots)
	// Products keep the built-in defaults since the file didn't override them.
	assert.EqualValues(t, 223350, c.Products["dayz-stable"].AppID, "dayz-stable app id")
}

func TestParseOverridesProducts(t *testing.T) {
	// A products map in the file is merged into the built-in defaults
	// (D14): entries not mentioned survive, entries with the same name are
	// overridden, and new entries are added.
	c, err := Parse([]byte(`
products:
  my-fork:
    app_id: 999999
    workshop_app_id: 221100
`))
	require.NoError(t, err)
	require.Len(t, c.Products, 3, "2 defaults + my-fork")
	assert.EqualValues(t, 999999, c.Products["my-fork"].AppID, "my-fork app id")
	assert.EqualValues(t, 1042420, c.Products["dayz-experimental"].AppID, "dayz-experimental should survive from defaults")
}

func TestLoadReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("paths:\n  data: /srv/dayz\n"), 0o600), "write fixture")
	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "/srv/dayz", c.Paths.Data)
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err, "expected an error for a missing file")
}

func TestParseInvalidYAML(t *testing.T) {
	_, err := Parse([]byte("paths: [this, is, not, a, map]"))
	require.Error(t, err, "expected a parse error")
}

func TestValidateRejectsRelativePaths(t *testing.T) {
	c := Default()
	c.Paths.Data = "relative/dir"
	c.resolvePaths()
	err := c.Validate()
	require.Error(t, err, "expected a validation error for a relative path")
	assert.Contains(t, err.Error(), "paths.data")
}

func TestValidateRejectsEmptyPath(t *testing.T) {
	c := Default()
	c.Paths.Secrets = ""
	require.ErrorContains(t, c.Validate(), "paths.secrets")
}

func TestValidateRejectsBadSiteURL(t *testing.T) {
	c := Default()
	c.Site.Remote = "http://[::1"
	c.Site.Branch = "main"
	require.ErrorContains(t, c.Validate(), "site.remote")
}

func TestValidateRequiresBranchWithRemote(t *testing.T) {
	c := Default()
	c.Site.Remote = "https://example.invalid/site.git"
	require.ErrorContains(t, c.Validate(), "site.branch")
}

func TestValidateRejectsNoProducts(t *testing.T) {
	c := Default()
	c.Products = nil
	require.ErrorContains(t, c.Validate(), "products")
}

func TestValidateRejectsProductMissingAppID(t *testing.T) {
	c := Default()
	c.Products["broken"] = Product{WorkshopAppID: 221100}
	require.ErrorContains(t, c.Validate(), "products.broken.app_id")
}

func TestValidateRejectsProductMissingWorkshopAppID(t *testing.T) {
	c := Default()
	c.Products["broken"] = Product{AppID: 1}
	require.ErrorContains(t, c.Validate(), "workshop_app_id")
}

func TestValidateDiscordWebhooks(t *testing.T) {
	cases := []struct {
		name    string
		hooks   []DiscordWebhook
		wantErr string
	}{
		{
			name:    "empty name",
			hooks:   []DiscordWebhook{{URL: "https://discord.example/hook"}},
			wantErr: "empty name",
		},
		{
			name: "duplicate name",
			hooks: []DiscordWebhook{
				{Name: "ops", URL: "https://discord.example/1"},
				{Name: "ops", URL: "https://discord.example/2"},
			},
			wantErr: "duplicate name",
		},
		{
			name:    "missing url",
			hooks:   []DiscordWebhook{{Name: "ops"}},
			wantErr: "url must be set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Notify.Discord = tc.hooks
			require.ErrorContains(t, c.Validate(), tc.wantErr)
		})
	}
}

func TestValidateAcceptsGoodDiscordWebhook(t *testing.T) {
	c := Default()
	c.Notify.Discord = []DiscordWebhook{{Name: "ops", URL: "https://discord.example/hook"}}
	require.NoError(t, c.Validate())
}

func TestServeAndWebDefaults(t *testing.T) {
	c := Default()
	require.Equal(t, "127.0.0.1:8080", c.Serve.Listen)
	require.Equal(t, "127.0.0.1:2400", c.Serve.ModListen)
	require.Equal(t, "127.0.0.1:8081", c.Web.Listen)
	require.NotEmpty(t, c.Serve.Installation)
}

func TestCheckListen(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:80", "[::1]:80", "localhost:80"} {
		require.NoError(t, CheckListen(ok, false), ok)
	}
	for _, bad := range []string{"0.0.0.0:80", "example.invalid:80", ":80", "nonsense", "127.0.0.1:"} {
		require.Error(t, CheckListen(bad, false), "%s must be refused", bad)
	}
	require.NoError(t, CheckListen("0.0.0.0:80", true), "insecure override")
	assert.Error(t, CheckListen("nonsense", true), "a malformed address is refused even with the override")
}

func TestValidateListenAndBackends(t *testing.T) {
	for name, yml := range map[string]string{
		"serve.listen":     "serve: {listen: '0.0.0.0:8080'}",
		"serve.mod_listen": "serve: {mod_listen: '10.0.0.1:2400'}",
		"web.listen":       "web: {listen: ':8081'}",
		"every entry":      "web: {backends: [{name: a, url: http://x}]}",
		"duplicate name":   "web: {backends: [{name: a, url: http://x, token_file: /t}, {name: a, url: http://y, token_file: /t}]}",
	} {
		_, err := Parse([]byte(yml))
		require.ErrorContains(t, err, name)
	}
	good := "serve: {listen: '0.0.0.0:8080', allow_insecure_http: true}\nweb: {backends: [{name: a, url: http://x, token_file: /t}]}"
	_, err := Parse([]byte(good))
	require.NoError(t, err)
}

func TestExporterConfig(t *testing.T) {
	c, err := Parse([]byte("paths: {data: /srv/x}\n"))
	require.NoError(t, err)
	require.Equal(t, ":9464", c.Exporter.Listen)
	require.Empty(t, c.Exporter.Allow)
	c, err = Parse([]byte("exporter:\n  listen: 127.0.0.1:9\n  allow: [192.0.2.0/24, '2001:db8::/32']\n  tls: {cert_file: /c, key_file: /k, client_ca_file: /ca}\n  bearer_token_file: /t\n"))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9", c.Exporter.Listen)
	require.Len(t, c.Exporter.Allow, 2)
	require.Equal(t, "/ca", c.Exporter.TLS.ClientCAFile)
	require.Equal(t, "/t", c.Exporter.BearerTokenFile)
	for yaml, want := range map[string]string{
		"exporter: {allow: [nope]}":              "CIDR",
		"exporter: {tls: {cert_file: /c}}":       "go together",
		"exporter: {tls: {client_ca_file: /ca}}": "needs cert_file",
		"exporter: {allow: ['192.0.2.1']}":       "CIDR",
	} {
		_, err := Parse([]byte(yaml))
		assert.ErrorContains(t, err, want, yaml)
	}
}

// There are no experimental workshop mods: every product takes its mods from
// the stable workshop (app 221100), so the mod cache is shared.
func TestProductsShareStableWorkshop(t *testing.T) {
	c := Default()
	require.EqualValues(t, 221100, c.Products["dayz-experimental"].WorkshopAppID)
	require.EqualValues(t, 221100, c.Products["dayz-stable"].WorkshopAppID)
}

func TestBinaryDefaultAndValidation(t *testing.T) {
	assert.Equal(t, "/usr/bin/dzo", Default().Binary, "default binary")
	c, err := Parse([]byte("binary: /var/lib/dzo/bin/dzo\n"))
	require.NoError(t, err)
	require.Equal(t, "/var/lib/dzo/bin/dzo", c.Binary)
	_, err = Parse([]byte("binary: dzo\n"))
	assert.Error(t, err, "a relative binary must be rejected")
}

func TestServeBanUpkeep(t *testing.T) {
	c, err := Parse([]byte("{}\n"))
	require.NoError(t, err)
	require.Equal(t, 90*24*time.Hour, c.Serve.IPRetention(), "defaults")
	require.Equal(t, 7*24*time.Hour, c.Serve.VacuumEvery(), "defaults")
	c, err = Parse([]byte("serve: {ip_retention_days: 30, vacuum_days: 1}\n"))
	require.NoError(t, err)
	require.Equal(t, 30*24*time.Hour, c.Serve.IPRetention(), "set")
	require.Equal(t, 24*time.Hour, c.Serve.VacuumEvery(), "set")
	c, err = Parse([]byte("serve: {ip_retention_days: 0, vacuum_days: 0}\n"))
	require.NoError(t, err)
	require.Negative(t, c.Serve.IPRetention(), "0 turns the upkeep off (a negative duration)")
	require.Negative(t, c.Serve.VacuumEvery(), "0 turns the upkeep off (a negative duration)")
	for _, yml := range []string{"serve: {ip_retention_days: -1}", "serve: {vacuum_days: -5}"} {
		_, err := Parse([]byte(yml))
		assert.Error(t, err, "%s must be refused", yml)
	}
}

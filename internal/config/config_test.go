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
		require.NoError(t, CheckListen(ok, false, false), ok)
	}
	for _, bad := range []string{"0.0.0.0:80", "example.invalid:80", ":80", "nonsense", "127.0.0.1:"} {
		require.Error(t, CheckListen(bad, false, false), "%s must be refused", bad)
	}
	require.NoError(t, CheckListen("0.0.0.0:80", true, false), "insecure override")
	assert.Error(t, CheckListen("nonsense", true, false), "a malformed address is refused even with the override")
}

func TestValidateListen(t *testing.T) {
	for name, yml := range map[string]string{
		"serve.listen":     "serve: {listen: '0.0.0.0:8080'}",
		"serve.mod_listen": "serve: {mod_listen: '10.0.0.1:2400'}",
		"web.listen":       "web: {listen: ':8081'}",
	} {
		_, err := Parse([]byte(yml))
		require.ErrorContains(t, err, name)
	}
	_, err := Parse([]byte("serve: {listen: '0.0.0.0:8080', allow_insecure_http: true}"))
	require.NoError(t, err)
}

func TestCheckListenWithTLS(t *testing.T) {
	require.NoError(t, CheckListen("0.0.0.0:443", false, true), "TLS makes a public address fine")
	require.Error(t, CheckListen("0.0.0.0:443", false, false))
	require.Error(t, CheckListen("nonsense", false, true))

	_, err := Parse([]byte("serve: {listen: '0.0.0.0:8443', tls: {cert_file: /c, key_file: /k}}"))
	require.NoError(t, err, "serve.listen with serve.tls")
	_, err = Parse([]byte("web: {listen: ':443', tls: {cert_file: /c, key_file: /k}}"))
	require.NoError(t, err, "web.listen with web.tls")
	_, err = Parse([]byte("serve: {mod_listen: '10.0.0.1:2400', tls: {cert_file: /c, key_file: /k}}"))
	require.Error(t, err, "mod_listen stays plain HTTP, serve.tls does not cover it")
}

func TestLoadRefusesWebBackends(t *testing.T) {
	_, err := Parse([]byte("web: {backends: [{name: a, url: http://x, token_file: /t}]}"))
	require.ErrorContains(t, err, "web.backends is no longer supported")
	require.ErrorContains(t, err, "migration")
}

func TestLoadRefusesUserHeader(t *testing.T) {
	_, err := Parse([]byte("web: {user_header: X-Forwarded-User}"))
	require.ErrorContains(t, err, "web.user_header is no longer supported")
	require.ErrorContains(t, err, "migration")
}

func TestServeWebURLMustBeHTTPS(t *testing.T) {
	for _, ok := range []string{"https://dzo.example", "https://dzo.example:8443", "http://127.0.0.1:8081", "http://localhost:8081", "http://[::1]:8081", "https://dzo.example/"} {
		_, err := Parse([]byte("serve: {web_url: '" + ok + "'}"))
		require.NoError(t, err, ok)
	}
	for _, bad := range []string{"http://dzo.example", "dzo.example", "ftp://dzo.example", "https://dzo.example/path", "https://dzo.example/?q=1", "https://dzo.example/#f", "https://", "https://user@dzo.example"} {
		_, err := Parse([]byte("serve: {web_url: '" + bad + "'}"))
		require.ErrorContains(t, err, "serve.web_url", bad)
	}
	c, err := Parse([]byte("{}"))
	require.NoError(t, err)
	require.Empty(t, c.Serve.WebURL, "optional: logins are off without it")
}

func TestSessionDefaults(t *testing.T) {
	c, err := Parse([]byte("{}"))
	require.NoError(t, err)
	require.Equal(t, 12*time.Hour, c.Serve.SessionIdle())
	require.Equal(t, 7*24*time.Hour, c.Serve.SessionMax())
	c, err = Parse([]byte("serve: {login: {session_idle_hours: 1, session_max_days: 30}}"))
	require.NoError(t, err)
	require.Equal(t, time.Hour, c.Serve.SessionIdle())
	require.Equal(t, 30*24*time.Hour, c.Serve.SessionMax())
	for _, bad := range []string{"serve: {login: {session_idle_hours: 0}}", "serve: {login: {session_max_days: 0}}", "serve: {login: {session_idle_hours: -1}}"} {
		_, err := Parse([]byte(bad))
		require.ErrorContains(t, err, "serve.login.", bad)
	}
}

func TestTLSPairsGoTogether(t *testing.T) {
	for key, yml := range map[string]string{
		"serve.tls": "serve: {tls: {cert_file: /c}}",
		"web.tls":   "web: {tls: {key_file: /k}}",
	} {
		_, err := Parse([]byte(yml))
		require.ErrorContains(t, err, key+": cert_file and key_file go together")
	}
	for key, yml := range map[string]string{
		"serve.tls.client_ca_file": "serve: {tls: {client_ca_file: /ca}}",
		"web.tls.client_ca_file":   "web: {tls: {client_ca_file: /ca}}",
	} {
		_, err := Parse([]byte(yml))
		require.ErrorContains(t, err, key+" needs cert_file and key_file")
	}
	c, err := Parse([]byte("serve: {tls: {cert_file: /c, key_file: /k, client_ca_file: /ca}}\nweb: {tls: {cert_file: /c2, key_file: /k2}}"))
	require.NoError(t, err)
	require.Equal(t, "/ca", c.Serve.TLS.ClientCAFile)
	require.Equal(t, "/c2", c.Web.TLS.CertFile)
}

func TestWebAPIRequiresURLAndToken(t *testing.T) {
	c, err := Parse([]byte("web: {api: {url: 'https://serve.example:8443', token_file: /t, ca_file: /ca}}"))
	require.NoError(t, err)
	require.Equal(t, "/ca", c.Web.API.CAFile)
	for _, bad := range []string{
		"web: {api: {url: 'https://serve.example'}}",
		"web: {api: {token_file: /t}}",
		"web: {api: {ca_file: /ca}}",
		"web: {api: {url: 'serve.example', token_file: /t}}",
		"web: {api: {url: 'ftp://serve.example', token_file: /t}}",
		"web: {api: {url: 'https://serve.example', token_file: t}}",
	} {
		_, err := Parse([]byte(bad))
		require.ErrorContains(t, err, "web.api", bad)
	}
	_, err = Parse([]byte("web: {api: {url: 'http://serve.example', token_file: /t}}"))
	require.ErrorContains(t, err, "web.api.url", "plain HTTP to another host is refused")
	_, err = Parse([]byte("web: {api: {url: 'http://127.0.0.1:8080', token_file: /t}}"))
	require.NoError(t, err, "plain HTTP to loopback")
}

func TestTrustedProxiesParse(t *testing.T) {
	c, err := Parse([]byte("web: {trusted_proxies: [10.0.0.1, '192.0.2.0/24', '2001:db8::/32', '::1']}"))
	require.NoError(t, err)
	require.Len(t, c.Web.TrustedProxies, 4)
	_, err = Parse([]byte("web: {trusted_proxies: [10.0.0.1, 'nonsense']}"))
	require.ErrorContains(t, err, `"nonsense"`)
	require.ErrorContains(t, err, "web.trusted_proxies")
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

func TestSessionRetentionDefaultAndZero(t *testing.T) {
	c, err := Parse([]byte("{}\n"))
	require.NoError(t, err)
	require.Equal(t, 365*24*time.Hour, c.Serve.SessionRetention(), "default")
	c, err = Parse([]byte("serve: {session_retention_days: 30}\n"))
	require.NoError(t, err)
	require.Equal(t, 30*24*time.Hour, c.Serve.SessionRetention())
	c, err = Parse([]byte("serve: {session_retention_days: 0}\n"))
	require.NoError(t, err)
	require.Negative(t, c.Serve.SessionRetention(), "0 keeps the sessions for ever (a negative duration)")
	_, err = Parse([]byte("serve: {session_retention_days: -1}\n"))
	require.Error(t, err)
}

func TestSteamProfileLookupDefaultsOn(t *testing.T) {
	c, err := Parse([]byte("{}\n"))
	require.NoError(t, err)
	require.True(t, c.Steam.Lookup(), "on by default")
	require.Empty(t, c.Steam.WebAPIKeyFile, "no key by default")
	c, err = Parse([]byte("steam: {profile_lookup: false, web_api_key_file: /var/lib/dzo/secrets/steam-web-api.key}\n"))
	require.NoError(t, err)
	require.False(t, c.Steam.Lookup())
	require.Equal(t, "/var/lib/dzo/secrets/steam-web-api.key", c.Steam.WebAPIKeyFile)
	_, err = Parse([]byte("steam: {web_api_key_file: relative/key}\n"))
	require.Error(t, err, "a relative key path is refused")
}

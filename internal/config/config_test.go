// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() should validate cleanly, got: %v", err)
	}
}

func TestParseAppliesDefaultsAndTemplating(t *testing.T) {
	c, err := Parse([]byte(`
paths:
  data: /srv/dayz
site:
  remote: https://example.invalid/site.git
  branch: main
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Paths.Data != "/srv/dayz" {
		t.Errorf("Paths.Data = %q, want /srv/dayz", c.Paths.Data)
	}
	if c.Paths.Instances != "/srv/dayz/instances" {
		t.Errorf("Paths.Instances = %q, want /srv/dayz/instances", c.Paths.Instances)
	}
	if c.Paths.Snapshots != "/srv/dayz/snapshots" {
		t.Errorf("Paths.Snapshots = %q, want /srv/dayz/snapshots", c.Paths.Snapshots)
	}
	// Products keep the built-in defaults since the file didn't override them.
	if got := c.Products["dayz-stable"].AppID; got != 223350 {
		t.Errorf("dayz-stable app id = %d, want 223350", got)
	}
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
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Products) != 3 {
		t.Fatalf("len(Products) = %d, want 3 (2 defaults + my-fork)", len(c.Products))
	}
	if c.Products["my-fork"].AppID != 999999 {
		t.Errorf("my-fork app id = %d, want 999999", c.Products["my-fork"].AppID)
	}
	if c.Products["dayz-experimental"].AppID != 1042420 {
		t.Errorf("dayz-experimental should survive from defaults, got %+v", c.Products["dayz-experimental"])
	}
}

func TestLoadReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("paths:\n  data: /srv/dayz\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Paths.Data != "/srv/dayz" {
		t.Errorf("Paths.Data = %q, want /srv/dayz", c.Paths.Data)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestParseInvalidYAML(t *testing.T) {
	_, err := Parse([]byte("paths: [this, is, not, a, map]"))
	if err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestValidateRejectsRelativePaths(t *testing.T) {
	c := Default()
	c.Paths.Data = "relative/dir"
	c.resolvePaths()
	err := c.Validate()
	if err == nil {
		t.Fatal("expected a validation error for a relative path")
	}
	if !strings.Contains(err.Error(), "paths.data") {
		t.Errorf("error should mention paths.data, got: %v", err)
	}
}

func TestValidateRejectsEmptyPath(t *testing.T) {
	c := Default()
	c.Paths.Secrets = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "paths.secrets") {
		t.Fatalf("expected an error mentioning paths.secrets, got: %v", err)
	}
}

func TestValidateRejectsBadSiteURL(t *testing.T) {
	c := Default()
	c.Site.Remote = "http://[::1"
	c.Site.Branch = "main"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "site.remote") {
		t.Fatalf("expected an error mentioning site.remote, got: %v", err)
	}
}

func TestValidateRequiresBranchWithRemote(t *testing.T) {
	c := Default()
	c.Site.Remote = "https://example.invalid/site.git"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "site.branch") {
		t.Fatalf("expected an error mentioning site.branch, got: %v", err)
	}
}

func TestValidateRejectsNoProducts(t *testing.T) {
	c := Default()
	c.Products = nil
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "products") {
		t.Fatalf("expected an error mentioning products, got: %v", err)
	}
}

func TestValidateRejectsProductMissingAppID(t *testing.T) {
	c := Default()
	c.Products["broken"] = Product{WorkshopAppID: 221100}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "products.broken.app_id") {
		t.Fatalf("expected an error mentioning products.broken.app_id, got: %v", err)
	}
}

func TestValidateRejectsProductMissingWorkshopAppID(t *testing.T) {
	c := Default()
	c.Products["broken"] = Product{AppID: 1}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "workshop_app_id") {
		t.Fatalf("expected an error mentioning workshop_app_id, got: %v", err)
	}
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
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected an error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateAcceptsGoodDiscordWebhook(t *testing.T) {
	c := Default()
	c.Notify.Discord = []DiscordWebhook{{Name: "ops", URL: "https://discord.example/hook"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestServeAndWebDefaults(t *testing.T) {
	c := Default()
	if c.Serve.Listen != "127.0.0.1:8080" || c.Serve.ModListen != "127.0.0.1:2400" || c.Web.Listen != "127.0.0.1:8081" || c.Serve.Installation == "" {
		t.Fatalf("defaults = %+v %+v", c.Serve, c.Web)
	}
}

func TestCheckListen(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:80", "[::1]:80", "localhost:80"} {
		if err := CheckListen(ok, false); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:80", "example.invalid:80", ":80", "nonsense", "127.0.0.1:"} {
		if err := CheckListen(bad, false); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	if err := CheckListen("0.0.0.0:80", true); err != nil {
		t.Errorf("insecure override: %v", err)
	}
	if err := CheckListen("nonsense", true); err == nil {
		t.Error("a malformed address is refused even with the override")
	}
}

func TestValidateListenAndBackends(t *testing.T) {
	for name, yml := range map[string]string{
		"serve.listen":     "serve: {listen: '0.0.0.0:8080'}",
		"serve.mod_listen": "serve: {mod_listen: '10.0.0.1:2400'}",
		"web.listen":       "web: {listen: ':8081'}",
		"every entry":      "web: {backends: [{name: a, url: http://x}]}",
		"duplicate name":   "web: {backends: [{name: a, url: http://x, token_file: /t}, {name: a, url: http://y, token_file: /t}]}",
	} {
		if _, err := Parse([]byte(yml)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	good := "serve: {listen: '0.0.0.0:8080', allow_insecure_http: true}\nweb: {backends: [{name: a, url: http://x, token_file: /t}]}"
	if _, err := Parse([]byte(good)); err != nil {
		t.Fatal(err)
	}
}

func TestExporterConfig(t *testing.T) {
	c, err := Parse([]byte("paths: {data: /srv/x}\n"))
	if err != nil || c.Exporter.Listen != ":9464" || len(c.Exporter.Allow) != 0 {
		t.Fatalf("defaults: %+v %v", c.Exporter, err)
	}
	c, err = Parse([]byte("exporter:\n  listen: 127.0.0.1:9\n  allow: [192.0.2.0/24, '2001:db8::/32']\n  tls: {cert_file: /c, key_file: /k, client_ca_file: /ca}\n  bearer_token_file: /t\n"))
	if err != nil || c.Exporter.Listen != "127.0.0.1:9" || len(c.Exporter.Allow) != 2 || c.Exporter.TLS.ClientCAFile != "/ca" || c.Exporter.BearerTokenFile != "/t" {
		t.Fatalf("parsed: %+v %v", c.Exporter, err)
	}
	for yaml, want := range map[string]string{
		"exporter: {allow: [nope]}":              "CIDR",
		"exporter: {tls: {cert_file: /c}}":       "go together",
		"exporter: {tls: {client_ca_file: /ca}}": "needs cert_file",
		"exporter: {allow: ['192.0.2.1']}":       "CIDR",
	} {
		if _, err := Parse([]byte(yaml)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error with %q, got %v", yaml, want, err)
		}
	}
}

// There are no experimental workshop mods: every product takes its mods from
// the stable workshop (app 221100), so the mod cache is shared.
func TestProductsShareStableWorkshop(t *testing.T) {
	c := Default()
	if c.Products["dayz-experimental"].WorkshopAppID != 221100 || c.Products["dayz-stable"].WorkshopAppID != 221100 {
		t.Fatalf("workshop app ids: %+v", c.Products)
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// siteConfig writes a config.yaml whose site checkout is the synthetic site
// repo used by internal/resolve's golden tests.
func siteConfig(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	site, err := filepath.Abs("../../internal/resolve/testdata/site")
	require.NoError(t, err)
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("paths:\n  data: "+dir+"\n  site: "+site+"\n"), 0o600))
	return path
}

func TestInstanceShow(t *testing.T) {
	out, err := runCmd(t, "instance", "show", "alpha", "--config", siteConfig(t))
	require.NoError(t, err, "instance show: %s", out)
	for _, want := range []string{"name: alpha", "app_id: 223350", "missing:", "mod 1559212036: not downloaded"} {
		assert.Contains(t, out, want, "output missing %q", want)
	}
}

func TestInstanceShowQuadletNeedsInstall(t *testing.T) {
	_, err := runCmd(t, "instance", "show", "alpha", "--quadlet", "--config", siteConfig(t))
	require.Error(t, err, "expected an error for --quadlet on an instance that is not installed")
}

func TestInstanceShowQuadlet(t *testing.T) {
	cfg := siteConfig(t)
	// "install" everything alpha needs
	data := filepath.Dir(cfg)
	for _, p := range []string{"products/dayz-stable", "workshop/221100/1559212036", "workshop/221100/1828439124"} {
		root := filepath.Join(data, "cache", p)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "1"), 0o750))
		require.NoError(t, os.Symlink("1", filepath.Join(root, "current")))
	}
	out, err := runCmd(t, "instance", "show", "alpha", "--quadlet", "--config", cfg)
	require.NoError(t, err, "instance show --quadlet: %s", out)
	assert.Contains(t, out, "ContainerName=dzo-alpha")
}

func TestInstanceShowUnknown(t *testing.T) {
	_, err := runCmd(t, "instance", "show", "nope", "--config", siteConfig(t))
	require.Error(t, err, "expected an error for an unknown instance")
}

func TestConfigValidateResolvesSite(t *testing.T) {
	out, err := runCmd(t, "config", "validate", "--config", siteConfig(t))
	require.NoError(t, err, "config validate: %s", out)
	assert.Contains(t, out, "2 instances resolve")
	assert.Contains(t, out, "note: alpha:")
}

func TestConfigValidateReportsBrokenSite(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "site", "instances", "x")
	require.NoError(t, os.MkdirAll(bad, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(bad, "instance.yaml"), []byte("name: x\nbogus: 1\n"), 0o600))
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("paths:\n  data: "+dir+"\n"), 0o600))
	_, err := runCmd(t, "config", "validate", "--config", cfg)
	require.Error(t, err, "expected an error for an unknown key in instance.yaml")
}

func TestConfigValidateUnresolvableSite(t *testing.T) {
	dir := t.TempDir()
	inst := filepath.Join(dir, "site", "instances", "x")
	require.NoError(t, os.MkdirAll(inst, 0o750))
	y := "name: x\nproduct: ghost\nmap: m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 1}\nnetwork: host\n"
	require.NoError(t, os.WriteFile(filepath.Join(inst, "instance.yaml"), []byte(y), 0o600))
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("paths:\n  data: "+dir+"\n"), 0o600))
	_, err := runCmd(t, "config", "validate", "--config", cfg)
	require.ErrorContains(t, err, "unknown product")
}

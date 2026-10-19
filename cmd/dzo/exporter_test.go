// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func exporterSite(t testing.TB, extra string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	writeFile(t, cfg, "paths:\n  data: "+dir+"\nexporter:\n  listen: 127.0.0.1:0\n"+extra)
	writeFile(t, filepath.Join(dir, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
`)
	return cfg
}

func TestExporterServesMetricsAndStatus(t *testing.T) {
	cfg := exporterSite(t, "")
	root := newRootCmd()
	var out syncBuf
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"exporter", "--config", cfg})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	var addr string
	startRE := regexp.MustCompile(`exporter on (\S+)`)
	require.Eventually(t, func() bool {
		m := startRE.FindStringSubmatch(out.String())
		if m == nil {
			return false
		}
		addr = m[1]
		return true
	}, 10*time.Second, 20*time.Millisecond, "the exporter did not start:\n%s", out.String())
	get := func(path string) (int, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+path, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := get("/metrics")
	assert.Equal(t, http.StatusOK, code, "metrics: %s", body)
	assert.Contains(t, body, `dzo_instance_up{instance="x"} 0`, "metrics")
	assert.Contains(t, body, "dzo_build_info", "metrics")
	code, body = get("/status/x")
	assert.Equal(t, http.StatusOK, code, "status: %s", body)
	assert.Contains(t, body, `"name": "x"`, "status")
	code, _ = get("/status/nope")
	assert.Equal(t, http.StatusNotFound, code, "unknown instance")
	cancel()
	assert.NoError(t, <-done, "the exporter must stop cleanly")
}

func TestExporterNeedsItsTokenAndRefusesBadConfig(t *testing.T) {
	cfg := exporterSite(t, "  bearer_token_file: /nonexistent\n")
	_, err := runCmd(t, "exporter", "--config", cfg)
	assert.ErrorContains(t, err, "bearer_token_file", "a missing token file")
	bad := filepath.Join(t.TempDir(), "c.yaml")
	writeFile(t, bad, "exporter:\n  allow: [nonsense]\n")
	_, err = runCmd(t, "exporter", "--config", bad)
	assert.ErrorContains(t, err, "CIDR", "a bad allow-list is a config error")
	writeFile(t, bad, "exporter:\n  tls: {cert_file: /a}\n")
	_, err = runCmd(t, "exporter", "--config", bad)
	assert.ErrorContains(t, err, "go together", "a certificate without a key")
}

func TestStatusCommand(t *testing.T) {
	cfg := exporterSite(t, "")
	out, err := runCmd(t, "status", "--config", cfg)
	require.NoError(t, err, "status: %s", out)
	require.True(t, strings.HasPrefix(out, "x: "), "status: %s", out)
	out, err = runCmd(t, "status", "x", "--json", "--config", cfg)
	require.NoError(t, err, "status x --json: %s", out)
	var i struct{ Name, Product string }
	require.NoError(t, json.Unmarshal([]byte(out), &i), "status x --json: %s", out)
	require.Equal(t, "x", i.Name)
	require.Equal(t, "dayz-stable", i.Product)
	dir := filepath.Join(t.TempDir(), "status")
	_, err = runCmd(t, "status", "--write", dir, "--config", cfg)
	require.NoError(t, err)
	for _, f := range []string{"x.json", "global.json"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if assert.NoError(t, err, f) {
			assert.True(t, json.Valid(b), "%s is not valid JSON", f)
		}
	}
	_, err = runCmd(t, "status", "nope", "--config", cfg)
	assert.Error(t, err, "an unknown instance")
	out, err = runCmd(t, "status", "--json", "--config", cfg)
	assert.NoError(t, err, "status --json: %s", out)
	assert.Contains(t, out, `"global"`, "status --json")
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/site"
)

const testKey = "0123456789ABCDEF0123456789ABCDEF"

func TestNewLookupsWithoutAKey(t *testing.T) {
	cfg, _ := fixture(t)
	ref, err := newLookups(cfg, "test", nil, t.Logf)
	require.NoError(t, err)
	assert.True(t, ref.SteamOn, "profile_lookup defaults to on")
	assert.False(t, ref.SteamKey)
	assert.NotNil(t, ref.BE, "BattlEye's master is always asked")
}

func TestNewLookupsWithAKey(t *testing.T) {
	cfg, _ := fixture(t)
	path := filepath.Join(t.TempDir(), "steam.key")
	require.NoError(t, os.WriteFile(path, []byte(testKey+"\n"), 0o600))
	cfg.Steam.WebAPIKeyFile = path
	ref, err := newLookups(cfg, "test", nil, t.Logf)
	require.NoError(t, err)
	assert.True(t, ref.SteamKey)
}

func TestNewLookupsRefusesABadKeyFile(t *testing.T) {
	cfg, _ := fixture(t)
	path := filepath.Join(t.TempDir(), "steam.key")
	require.NoError(t, os.WriteFile(path, []byte(testKey), 0o644)) //nolint:gosec // the test needs a readable file
	cfg.Steam.WebAPIKeyFile = path
	_, err := newLookups(cfg, "test", nil, t.Logf)
	require.Error(t, err, "a world-readable key file stops dzo serve")
	assert.Contains(t, err.Error(), path)
	assert.NotContains(t, err.Error(), testKey)
	cfg.Steam.WebAPIKeyFile = filepath.Join(t.TempDir(), "missing")
	_, err = newLookups(cfg, "test", nil, t.Logf)
	require.Error(t, err)
}

func TestNewLookupsWithSteamOff(t *testing.T) {
	cfg, _ := fixture(t)
	off := false
	cfg.Steam.ProfileLookup = &off
	cfg.Steam.WebAPIKeyFile = "/nonexistent/key" // not read when Steam is off
	ref, err := newLookups(cfg, "test", nil, t.Logf)
	require.NoError(t, err)
	assert.False(t, ref.SteamOn)
}

func TestRunWithABadSteamKeyFileFails(t *testing.T) {
	cfg, tree := fixture(t)
	cfg.Steam.WebAPIKeyFile = filepath.Join(t.TempDir(), "missing.key")
	err := Run(t.Context(), cfg, Options{Version: "test", Lookups: true, Load: func() (*site.Tree, error) { return tree, nil }, Logf: t.Logf})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "steam.web_api_key_file")
}

func TestRunStartsAndStopsTheLookups(t *testing.T) {
	cfg, tree := fixture(t)
	ready := make(chan [2]string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{Version: "test", Lookups: true, Load: func() (*site.Tree, error) { return tree, nil },
			Ready: func(a, m string) { ready <- [2]string{a, m} }, Logf: t.Logf})
	}()
	addrs := <-ready
	secret, _, err := api.NewTokenStore(APITokens(cfg)).Create("mod", api.RoleModerator, nil, false, false, time.Hour)
	require.NoError(t, err)
	// the route is wired: an unknown player is 404, not 503 ("lookups are not available")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addrs[0]+"/api/v1/players/76561198000000001/lookup", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	cancel()
	require.NoError(t, <-done)
}

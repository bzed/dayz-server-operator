// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/config"
)

// fakeAPI answers the status of "alpha" and every message with ok, recording the messages.
func fakeAPI(t testing.TB, status admin.Status, messageCode int) (*apiclient.Client, *[]api.MessageBody) {
	t.Helper()
	var got []api.MessageBody
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/instances/alpha":
			_ = json.NewEncoder(w).Encode(status)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/instances/alpha/message":
			b, _ := io.ReadAll(r.Body)
			var m api.MessageBody
			_ = json.Unmarshal(b, &m)
			got = append(got, m)
			w.WriteHeader(messageCode)
			_, _ = w.Write([]byte(map[bool]string{true: `{"ok":true}`, false: `{"ok":false,"message":"refused"}`}[messageCode == 200]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return apiclient.New(ts.URL, "dzo_x"), &got
}

func connected(features ...string) admin.Status {
	return admin.Status{Instance: "alpha", Connected: true, Hello: &admin.Hello{Features: features}}
}

func TestAPINotifierMapsWays(t *testing.T) {
	c, got := fakeAPI(t, connected("expansion"), 200)
	n := apiNotifier(c, "alpha")
	ctx := t.Context()
	for _, call := range []struct {
		way, typ string
	}{{"chat", ""}, {"notification", ""}, {"expansion", "market"}} {
		require.NoError(t, n(ctx, call.way, call.typ, "Restart in 5 minutes", 10), call.way)
	}
	want := []api.MessageBody{
		{Text: "Restart in 5 minutes", Style: "important", Seconds: 10},
		{Text: "Restart in 5 minutes", Style: "notification", Seconds: 10},
		{Text: "Restart in 5 minutes", Style: "expansion", NotifyType: "market", Seconds: 10},
	}
	assert.Equal(t, want, *got, "bodies")
}

func TestAPINotifierSendsExpansionAsNotificationWithoutTheFeature(t *testing.T) {
	c, got := fakeAPI(t, connected(), 200)
	require.NoError(t, apiNotifier(c, "alpha")(t.Context(), "expansion", "baguette", "x", 10))
	require.Len(t, *got, 1)
	assert.Equal(t, "notification", (*got)[0].Style)
	assert.Empty(t, (*got)[0].NotifyType)
}

func TestAPINotifierFailsWhenTheModIsNotConnected(t *testing.T) {
	c, got := fakeAPI(t, admin.Status{Instance: "alpha"}, 200)
	err := apiNotifier(c, "alpha")(t.Context(), "notification", "", "x", 10)
	require.ErrorContains(t, err, "not connected")
	assert.Empty(t, *got, "messages")
}

func TestAPINotifierReportsARefusal(t *testing.T) {
	c, _ := fakeAPI(t, connected(), http.StatusUnprocessableEntity)
	err := apiNotifier(c, "alpha")(t.Context(), "notification", "", "x", 10)
	assert.Error(t, err, "a refused message must be an error, so the countdown falls back to RCon")
}

func TestAnnounceClientNeedsAToken(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Secrets = dir
	cfg.Serve.Listen = "127.0.0.1:1"
	t.Setenv("DZO_TOKEN", "")
	_, err := announceClient(cfg)
	require.ErrorContains(t, err, "no API token")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "announce.token"), []byte("dzo_file\n"), 0o600))
	c, err := announceClient(cfg)
	require.NoError(t, err, "file token")
	require.Equal(t, "dzo_file", c.Token, "file token")
	require.Equal(t, "http://127.0.0.1:1", c.Base, "file token")
	t.Setenv("DZO_TOKEN", "dzo_env")
	c, err = announceClient(cfg)
	require.NoError(t, err, "env token")
	require.Equal(t, "dzo_env", c.Token, "env token")
}

func TestLazyNotifierWithoutATokenFailsSoTheCountdownFallsBack(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Secrets = t.TempDir()
	cfg.Serve.Listen = "127.0.0.1:1"
	t.Setenv("DZO_TOKEN", "")
	n := lazyNotifier(cfg, "alpha")
	for range 2 {
		require.ErrorContains(t, n(t.Context(), "notification", "", "x", 10), "no API token")
	}
}

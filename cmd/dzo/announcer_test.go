// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/config"
)

// fakeAPI answers the status of "alpha" and every message with ok, recording the messages.
func fakeAPI(t *testing.T, status admin.Status, messageCode int) (*apiclient.Client, *[]api.MessageBody) {
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
	ctx := context.Background()
	for _, call := range []struct {
		way, typ string
	}{{"chat", ""}, {"notification", ""}, {"expansion", "market"}} {
		if err := n(ctx, call.way, call.typ, "Restart in 5 minutes", 10); err != nil {
			t.Fatalf("%s: %v", call.way, err)
		}
	}
	want := []api.MessageBody{
		{Text: "Restart in 5 minutes", Style: "important", Seconds: 10},
		{Text: "Restart in 5 minutes", Style: "notification", Seconds: 10},
		{Text: "Restart in 5 minutes", Style: "expansion", NotifyType: "market", Seconds: 10},
	}
	if len(*got) != 3 || (*got)[0] != want[0] || (*got)[1] != want[1] || (*got)[2] != want[2] {
		t.Errorf("bodies = %+v, want %+v", *got, want)
	}
}

func TestAPINotifierSendsExpansionAsNotificationWithoutTheFeature(t *testing.T) {
	c, got := fakeAPI(t, connected(), 200)
	if err := apiNotifier(c, "alpha")(context.Background(), "expansion", "baguette", "x", 10); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || (*got)[0].Style != "notification" || (*got)[0].NotifyType != "" {
		t.Errorf("bodies = %+v", *got)
	}
}

func TestAPINotifierFailsWhenTheModIsNotConnected(t *testing.T) {
	c, got := fakeAPI(t, admin.Status{Instance: "alpha"}, 200)
	err := apiNotifier(c, "alpha")(context.Background(), "notification", "", "x", 10)
	if err == nil || !strings.Contains(err.Error(), "not connected") || len(*got) != 0 {
		t.Errorf("err = %v, messages %d", err, len(*got))
	}
}

func TestAPINotifierReportsARefusal(t *testing.T) {
	c, _ := fakeAPI(t, connected(), http.StatusUnprocessableEntity)
	if err := apiNotifier(c, "alpha")(context.Background(), "notification", "", "x", 10); err == nil {
		t.Error("a refused message must be an error, so the countdown falls back to RCon")
	}
}

func TestAnnounceClientNeedsAToken(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Secrets = dir
	cfg.Serve.Listen = "127.0.0.1:1"
	t.Setenv("DZO_TOKEN", "")
	if _, err := announceClient(cfg); err == nil || !strings.Contains(err.Error(), "no API token") {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "announce.token"), []byte("dzo_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := announceClient(cfg)
	if err != nil || c.Token != "dzo_file" || c.Base != "http://127.0.0.1:1" {
		t.Fatalf("file token: %+v, %v", c, err)
	}
	t.Setenv("DZO_TOKEN", "dzo_env")
	if c, err = announceClient(cfg); err != nil || c.Token != "dzo_env" {
		t.Fatalf("env token: %+v, %v", c, err)
	}
}

func TestLazyNotifierWithoutATokenFailsSoTheCountdownFallsBack(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Secrets = t.TempDir()
	cfg.Serve.Listen = "127.0.0.1:1"
	t.Setenv("DZO_TOKEN", "")
	n := lazyNotifier(cfg, "alpha")
	for range 2 {
		if err := n(context.Background(), "notification", "", "x", 10); err == nil || !strings.Contains(err.Error(), "no API token") {
			t.Fatalf("err = %v", err)
		}
	}
}

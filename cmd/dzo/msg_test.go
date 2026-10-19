// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/api"
)

// msgAPI answers every message route with ok and records the bodies it got.
func msgAPI(t *testing.T) (args []string, bodies *[]api.MessageBody) {
	t.Helper()
	var got []api.MessageBody
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m api.MessageBody
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("body %q: %v", b, err)
		}
		got = append(got, m)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(ts.Close)
	tokenFile := filepath.Join(t.TempDir(), "token")
	writeFile(t, tokenFile, "dzo_x\n")
	return []string{"--api", ts.URL, "--token-file", tokenFile}, &got
}

func TestMsgExpansionSendsTypeTitleSeconds(t *testing.T) {
	common, got := msgAPI(t)
	out, err := runCmd(t, append([]string{"player", "msg", "alpha", "all", "Restart", "soon", "--style", "expansion", "--type", "baguette", "--title", "Heads up", "--seconds", "5"}, common...)...)
	if err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("msg: %v\n%s", err, out)
	}
	want := api.MessageBody{Text: "Restart soon", Style: "expansion", NotifyType: "baguette", Title: "Heads up", Seconds: 5}
	if len(*got) != 1 || (*got)[0] != want {
		t.Errorf("body = %+v, want %+v", *got, want)
	}
}

func TestMsgTypeWithoutExpansionStyleIsAnError(t *testing.T) {
	common, got := msgAPI(t)
	_, err := runCmd(t, append([]string{"player", "msg", "alpha", "all", "x", "--type", "toast"}, common...)...)
	if err == nil || !strings.Contains(err.Error(), "--style expansion") || len(*got) != 0 {
		t.Errorf("err = %v, requests %d", err, len(*got))
	}
}

func TestMsgUnknownTypeListsTheTypes(t *testing.T) {
	common, got := msgAPI(t)
	_, err := runCmd(t, append([]string{"player", "msg", "alpha", "all", "x", "--style", "expansion", "--type", "popup"}, common...)...)
	if err == nil || !strings.Contains(err.Error(), "baguette") || len(*got) != 0 {
		t.Errorf("err = %v, requests %d", err, len(*got))
	}
}

func TestMsgRConStyleToEveryone(t *testing.T) {
	common, got := msgAPI(t)
	if _, err := runCmd(t, append([]string{"player", "msg", "alpha", "all", "hi", "--style", "rcon"}, common...)...); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || (*got)[0].Style != "rcon" || (*got)[0].Text != "hi" {
		t.Errorf("bodies = %+v", *got)
	}
}

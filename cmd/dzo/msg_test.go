// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/api"
)

// msgAPI answers every message route with ok and records the bodies it got.
func msgAPI(t testing.TB) (args []string, bodies *[]api.MessageBody) {
	t.Helper()
	var got []api.MessageBody
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m api.MessageBody
		assert.NoError(t, json.Unmarshal(b, &m), "body %q", b)
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
	require.NoError(t, err, "msg: %s", out)
	require.Contains(t, out, "ok", "msg")
	want := api.MessageBody{Text: "Restart soon", Style: "expansion", NotifyType: "baguette", Title: "Heads up", Seconds: 5}
	assert.Equal(t, []api.MessageBody{want}, *got, "body")
}

func TestMsgTypeWithoutExpansionStyleIsAnError(t *testing.T) {
	common, got := msgAPI(t)
	_, err := runCmd(t, append([]string{"player", "msg", "alpha", "all", "x", "--type", "toast"}, common...)...)
	require.ErrorContains(t, err, "--style expansion")
	assert.Empty(t, *got, "requests")
}

func TestMsgUnknownTypeListsTheTypes(t *testing.T) {
	common, got := msgAPI(t)
	_, err := runCmd(t, append([]string{"player", "msg", "alpha", "all", "x", "--style", "expansion", "--type", "popup"}, common...)...)
	require.ErrorContains(t, err, "baguette")
	assert.Empty(t, *got, "requests")
}

func TestMsgRConStyleToEveryone(t *testing.T) {
	common, got := msgAPI(t)
	_, err := runCmd(t, append([]string{"player", "msg", "alpha", "all", "hi", "--style", "rcon"}, common...)...)
	require.NoError(t, err)
	require.Len(t, *got, 1)
	assert.Equal(t, "rcon", (*got)[0].Style)
	assert.Equal(t, "hi", (*got)[0].Text)
}

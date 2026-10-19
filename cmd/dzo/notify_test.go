// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyTestSendsMessage(t *testing.T) {
	var gotContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Content string }
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotContent = payload.Content
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	out, err := runCmd(t, "notify", "test", "--url", server.URL, "--message", "hello dzo")
	require.NoError(t, err, "notify test")
	assert.Contains(t, out, "sent")
	assert.Equal(t, "hello dzo", gotContent, "posted content")
}

func TestNotifyTestRequiresURL(t *testing.T) {
	_, err := runCmd(t, "notify", "test")
	require.Error(t, err, "expected an error for missing --url")
}

func TestNotifyTestHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	_, err := runCmd(t, "notify", "test", "--url", server.URL)
	require.Error(t, err, "expected an error for a non-2xx webhook response")
}

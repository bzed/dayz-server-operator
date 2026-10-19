// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package monitor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerMetrics(t *testing.T) {
	h := Handler(SourceFunc(func() (Snapshot, error) { return sampleSnapshot(), nil }))
	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Get(server.URL + "/metrics")
	require.NoError(t, err, "GET /metrics")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "text/plain")
}

func TestHandlerStatusGlobal(t *testing.T) {
	h := Handler(SourceFunc(func() (Snapshot, error) { return sampleSnapshot(), nil }))
	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Get(server.URL + "/status")
	require.NoError(t, err, "GET /status")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "application/json")
}

func TestHandlerStatusInstance(t *testing.T) {
	h := Handler(SourceFunc(func() (Snapshot, error) { return sampleSnapshot(), nil }))
	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Get(server.URL + "/status/deerisle")
	require.NoError(t, err, "GET /status/deerisle")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandlerStatusInstanceNotFound(t *testing.T) {
	h := Handler(SourceFunc(func() (Snapshot, error) { return sampleSnapshot(), nil }))
	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Get(server.URL + "/status/nonexistent")
	require.NoError(t, err, "GET")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandlerStatusEmptyName(t *testing.T) {
	h := Handler(SourceFunc(func() (Snapshot, error) { return sampleSnapshot(), nil }))
	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Get(server.URL + "/status/")
	require.NoError(t, err, "GET")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandlerSourceError(t *testing.T) {
	h := Handler(SourceFunc(func() (Snapshot, error) { return Snapshot{}, errors.New("boom") }))
	server := httptest.NewServer(h)
	defer server.Close()

	for _, path := range []string{"/metrics", "/status", "/status/x"} {
		resp, err := http.Get(server.URL + path)
		require.NoError(t, err, "GET %s", path)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	}
}

func TestSnapshotFind(t *testing.T) {
	snap := sampleSnapshot()
	_, ok := snap.Find("deerisle")
	assert.True(t, ok)
	_, ok = snap.Find("nope")
	assert.False(t, ok)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/monitor"
)

func TestCheckRemoteInstanceOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(monitor.InstanceStatus{
			Name: "deerisle", Up: true, Health: monitor.HealthHealthy, LastRenderSuccess: true,
		})
	}))
	defer server.Close()

	out, err := runCmd(t, "check", "remote", "--url", server.URL, "--instance", "deerisle")
	require.NoError(t, err, "check remote")
	assert.True(t, strings.HasPrefix(out, "OK:"), "output = %q", out)
}

func TestCheckRemoteInstanceCritical(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(monitor.InstanceStatus{Name: "deerisle", Up: false})
	}))
	defer server.Close()

	out, err := runCmd(t, "check", "remote", "--url", server.URL, "--instance", "deerisle")
	require.Error(t, err, "expected a non-nil error for a CRITICAL check result")
	var exitErr *checkExitErr
	require.ErrorAs(t, err, &exitErr, "want a *checkExitErr with code 2")
	require.Equal(t, 2, exitErr.code)
	assert.True(t, strings.HasPrefix(out, "CRITICAL:"), "output = %q", out)
}

func TestCheckRemoteGlobalOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(monitor.Snapshot{Global: monitor.GlobalStatus{SteamSessionValid: true}})
	}))
	defer server.Close()

	out, err := runCmd(t, "check", "remote", "--url", server.URL)
	require.NoError(t, err, "check remote")
	assert.True(t, strings.HasPrefix(out, "OK:"), "output = %q", out)
}

func TestCheckRemoteUnknownOnFetchFailure(t *testing.T) {
	conn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	addr := conn.Addr().String()
	_ = conn.Close()

	out, err := runCmd(t, "check", "remote", "--url", "http://"+addr, "--timeout", "300ms")
	require.Error(t, err)
	var exitErr *checkExitErr
	require.ErrorAs(t, err, &exitErr, "want a *checkExitErr with code 3 (UNKNOWN)")
	require.Equal(t, 3, exitErr.code)
	assert.True(t, strings.HasPrefix(out, "UNKNOWN:"), "output = %q", out)
}

func TestCheckRemoteRequiresURL(t *testing.T) {
	_, err := runCmd(t, "check", "remote")
	require.Error(t, err, "expected an error for missing --url")
}

func TestCheckA2SCmd(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	addr := conn.LocalAddr().String()
	_ = conn.Close() // closed on purpose: nothing answers, expect CRITICAL

	out, err := runCmd(t, "check", "a2s", addr, "--timeout", "300ms")
	require.Error(t, err, "expected an error for an unreachable query port")
	assert.True(t, strings.HasPrefix(out, "CRITICAL:"), "output = %q", out)
}

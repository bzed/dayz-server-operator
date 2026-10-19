// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package monitor

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateInstanceDown(t *testing.T) {
	r := EvaluateInstance(InstanceStatus{Name: "x", Up: false})
	assert.Equal(t, StatusCritical, r.Status)
}

func TestEvaluateInstanceUnhealthy(t *testing.T) {
	r := EvaluateInstance(InstanceStatus{Name: "x", Up: true, Health: HealthUnhealthy})
	assert.Equal(t, StatusCritical, r.Status)
}

func TestEvaluateInstanceStarting(t *testing.T) {
	r := EvaluateInstance(InstanceStatus{Name: "x", Up: true, Health: HealthStarting})
	assert.Equal(t, StatusWarning, r.Status)
}

func TestEvaluateInstanceRenderFailed(t *testing.T) {
	r := EvaluateInstance(InstanceStatus{Name: "x", Up: true, Health: HealthHealthy, LastRenderSuccess: false})
	assert.Equal(t, StatusWarning, r.Status)
}

func TestEvaluateInstanceOK(t *testing.T) {
	r := EvaluateInstance(InstanceStatus{Name: "x", Up: true, Health: HealthHealthy, LastRenderSuccess: true, Players: 3, MaxPlayers: 60})
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Output(), "3/60")
}

func TestEvaluateGlobalSteamAuthRequired(t *testing.T) {
	r := EvaluateGlobal(GlobalStatus{SteamAuthRequired: true})
	assert.Equal(t, StatusWarning, r.Status)
}

func TestEvaluateGlobalSteamSessionInvalid(t *testing.T) {
	r := EvaluateGlobal(GlobalStatus{SteamSessionValid: false})
	assert.Equal(t, StatusWarning, r.Status)
}

func TestEvaluateGlobalOK(t *testing.T) {
	r := EvaluateGlobal(GlobalStatus{SteamSessionValid: true})
	assert.Equal(t, StatusOK, r.Status)
}

func TestCheckResultOutputFormat(t *testing.T) {
	r := CheckResult{Status: StatusOK, Message: "all good", Perf: []PerfDatum{{Label: "players", Value: 5}}}
	out := r.Output()
	assert.True(t, strings.HasPrefix(out, "OK: all good | "), "Output() = %q", out)
	assert.Contains(t, out, "'players'=5")
}

func TestCheckResultOutputNoPerf(t *testing.T) {
	r := CheckResult{Status: StatusCritical, Message: "down"}
	assert.Equal(t, "CRITICAL: down", r.Output())
}

func TestPerfDatumWithThresholds(t *testing.T) {
	warn, crit := 10.0, 20.0
	p := PerfDatum{Label: "x", Value: 5, Warn: &warn, Crit: &crit}
	assert.Equal(t, "'x'=5;10;20", p.String())
}

func TestStatusExitCodes(t *testing.T) {
	cases := map[Status]int{StatusOK: 0, StatusWarning: 1, StatusCritical: 2, StatusUnknown: 3}
	for s, want := range cases {
		got := s.ExitCode()
		assert.Equal(t, want, got)
	}
}

func TestCheckRemoteInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(InstanceStatus{Name: "deerisle", Up: true, Health: HealthHealthy, LastRenderSuccess: true})
	}))
	defer server.Close()

	result, err := CheckRemoteInstance(t.Context(), server.URL, time.Second)
	require.NoError(t, err, "CheckRemoteInstance")
	assert.Equal(t, StatusOK, result.Status)
}

func TestCheckRemoteGlobal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Snapshot{Global: GlobalStatus{SteamSessionValid: true}})
	}))
	defer server.Close()

	result, err := CheckRemoteGlobal(t.Context(), server.URL, time.Second)
	require.NoError(t, err, "CheckRemoteGlobal")
	assert.Equal(t, StatusOK, result.Status)
}

func TestCheckRemoteHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := CheckRemoteInstance(t.Context(), server.URL, time.Second)
	require.Error(t, err, "expected an error for a non-200 response")
}

func TestCheckRemoteInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	_, err := CheckRemoteInstance(t.Context(), server.URL, time.Second)
	require.Error(t, err, "expected an error for invalid JSON")
}

func TestCheckRemoteUnreachable(t *testing.T) {
	conn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	addr := conn.Addr().String()
	_ = conn.Close()

	_, err = CheckRemoteInstance(t.Context(), "http://"+addr, 500*time.Millisecond)
	require.Error(t, err, "expected an error for an unreachable server")
}

func TestCheckA2SSuccess(t *testing.T) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	defer func() { _ = udpConn.Close() }()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, remote, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_ = n
			body := append([]byte{17}, make([]byte, 14)...)
			resp := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'I'}, body...)
			if _, err := udpConn.WriteToUDP(resp, remote); err != nil {
				return
			}
		}
	}()

	result := CheckA2S(udpConn.LocalAddr().String(), 2*time.Second)
	assert.Equal(t, StatusOK, result.Status)
}

func TestCheckA2SFailure(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	addr := conn.LocalAddr().String()
	_ = conn.Close()

	result := CheckA2S(addr, 300*time.Millisecond)
	assert.Equal(t, StatusCritical, result.Status)
}

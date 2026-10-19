// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package monitor

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleSnapshot() Snapshot {
	rtt := 0.012
	return Snapshot{
		Global: GlobalStatus{
			Version:             "1.2.3",
			ProductBuilds:       map[string]string{"dayz-stable": "1234567"},
			UpdatesPending:      2,
			UpdateLastCheckUnix: 1700000000,
			SteamSessionValid:   true,
			JobsFailedTotal:     map[string]int{"update": 1},
			CacheBytes:          123456,
			DiskFreeBytes:       789000,
		},
		Instances: []InstanceStatus{
			{
				Name: "deerisle", Up: true, Health: HealthHealthy,
				Product: "dayz-stable", Build: "1234567", Map: "empty.deerisle",
				Players: 5, MaxPlayers: 60, UptimeSeconds: 3600,
				RestartsTotal:       map[string]int{"scheduled": 3, "crash": 1},
				A2SRTTSeconds:       &rtt,
				LastRenderSuccess:   true,
				LastRenderTimestamp: 1700000100,
				MissionDriftFiles:   0,
				ModsPendingUpdate:   1,
			},
		},
	}
}

func TestWriteMetricsContainsExpectedSeries(t *testing.T) {
	var buf bytes.Buffer
	err := WriteMetrics(&buf, sampleSnapshot())
	require.NoError(t, err, "WriteMetrics")
	out := buf.String()

	for _, want := range []string{
		`dzo_build_info{version="1.2.3"} 1`,
		`dzo_product_build_info{buildid="1234567",product="dayz-stable"} 1`,
		"dzo_updates_pending 2",
		"dzo_steam_session_valid 1",
		`dzo_jobs_failed_total{type="update"} 1`,
		`dzo_instance_up{instance="deerisle"} 1`,
		`dzo_instance_health{instance="deerisle",state="healthy"} 1`,
		`dzo_instance_health{instance="deerisle",state="starting"} 0`,
		`dzo_instance_info{build="1234567",instance="deerisle",map="empty.deerisle",product="dayz-stable"} 1`,
		`dzo_instance_players{instance="deerisle"} 5`,
		`dzo_instance_restarts_total{instance="deerisle",reason="crash"} 1`,
		`dzo_instance_restarts_total{instance="deerisle",reason="scheduled"} 3`,
		`dzo_instance_a2s_rtt_seconds{instance="deerisle"} 0.012`,
		"# HELP dzo_instance_up",
		"# TYPE dzo_instance_up gauge",
		"# TYPE dzo_jobs_failed_total counter",
	} {
		assert.Contains(t, out, want)
	}
}

func TestWriteMetricsDeterministic(t *testing.T) {
	snap := sampleSnapshot()
	var b1, b2 bytes.Buffer
	err := WriteMetrics(&b1, snap)
	require.NoError(t, err, "WriteMetrics")
	err = WriteMetrics(&b2, snap)
	require.NoError(t, err, "WriteMetrics")
	require.Equal(t, b2.String(), b1.String())
}

func TestWriteMetricsEmptySnapshot(t *testing.T) {
	var buf bytes.Buffer
	err := WriteMetrics(&buf, Snapshot{})
	require.NoError(t, err, "WriteMetrics")
	assert.Contains(t, buf.String(), "dzo_build_info")
}

func TestEscapeLabelValue(t *testing.T) {
	cases := map[string]string{
		`plain`:      `plain`,
		`back\slash`: `back\\slash`,
		"new\nline":  `new\nline`,
	}
	for in, want := range cases {
		got := escapeLabelValue(in)
		assert.Equal(t, want, got)
	}
}

func TestFormatLabelsEmpty(t *testing.T) {
	got := formatLabels(nil)
	assert.Empty(t, got)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package quadlet

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderContainerRestartLimit(t *testing.T) {
	spec := ContainerSpec{
		Name: "dzo-deerisle", Image: "localhost/dzo-runtime:latest", Network: NetworkHost,
		RestartLimitBurst: 5, RestartLimitInterval: 30 * time.Minute,
	}
	out, err := RenderContainer(spec)
	require.NoError(t, err)
	assert.Contains(t, out, "StartLimitBurst=5")
	assert.Contains(t, out, "StartLimitIntervalSec=30m")
}

func TestRenderContainerOmitsRestartLimitWhenZero(t *testing.T) {
	spec := ContainerSpec{Name: "dzo-deerisle", Image: "localhost/dzo-runtime:latest", Network: NetworkHost}
	out, err := RenderContainer(spec)
	require.NoError(t, err)
	assert.NotContains(t, out, "StartLimit", "should omit StartLimit* when unset")
}

func TestRenderContainerHostNetwork(t *testing.T) {
	spec := ContainerSpec{
		Name:        "dzo-deerisle",
		Description: "DayZ instance deerisle",
		Image:       "localhost/dzo-runtime:latest",
		Network:     NetworkHost,
		Volumes: []Volume{
			{Source: "/var/lib/dzo/instances/deerisle/mpmissions", Destination: "/dayz/mpmissions"},
			{Source: "/var/lib/dzo/cache/products/dayz-stable/current", Destination: "/dayz", Overlay: true},
		},
		Environment: map[string]string{"B": "2", "A": "1"},
		Health: Health{
			Cmd:             "/usr/local/bin/dzo health startup",
			Interval:        60 * time.Second,
			StartupCmd:      "/usr/local/bin/dzo health startup",
			StartupInterval: 30 * time.Second,
			StartupRetries:  90,
			Retries:         5,
			OnFailure:       "kill",
		},
		Memory:      "24G",
		StopTimeout: 30 * time.Second,
	}

	out, err := RenderContainer(spec)
	require.NoError(t, err)

	for _, want := range []string{
		"[Unit]",
		"Description=DayZ instance deerisle",
		"[Container]",
		"Image=localhost/dzo-runtime:latest",
		"ContainerName=dzo-deerisle",
		"Network=host",
		"Volume=/var/lib/dzo/instances/deerisle/mpmissions:/dayz/mpmissions\n",
		"Volume=/var/lib/dzo/cache/products/dayz-stable/current:/dayz:O\n",
		"Environment=A=1\n",
		"Environment=B=2\n",
		"HealthCmd=/usr/local/bin/dzo health startup",
		"HealthInterval=1m",
		"HealthStartupCmd=/usr/local/bin/dzo health startup",
		"HealthStartupInterval=30s",
		"HealthStartupRetries=90",
		"HealthStartupSuccess=1",
		"HealthRetries=5",
		"HealthOnFailure=kill",
		"PodmanArgs=--memory=24G",
		"StopTimeout=30",
		"[Service]",
		"Restart=always",
		"[Install]",
		"WantedBy=default.target",
	} {
		assert.Contains(t, out, want)
	}

	// Environment keys must be sorted (determinism, NFR-02).
	aIdx := strings.Index(out, "Environment=A=1")
	bIdx := strings.Index(out, "Environment=B=2")
	require.NotEqual(t, -1, aIdx)
	require.NotEqual(t, -1, bIdx)
	assert.Less(t, aIdx, bIdx, "Environment lines not in sorted order")
}

func TestRenderContainerPublishNetwork(t *testing.T) {
	spec := ContainerSpec{
		Name:    "dzo-hashima",
		Image:   "localhost/dzo-runtime:latest",
		Network: NetworkPublish,
		Ports: []Port{
			{HostPort: 3302, ContainerPort: 2302, Protocol: "udp"},
			{HostPort: 3303, ContainerPort: 2303},
		},
	}
	out, err := RenderContainer(spec)
	require.NoError(t, err)
	assert.Contains(t, out, "PublishPort=3302:2302/udp\n", "udp port mapping")
	assert.Contains(t, out, "PublishPort=3303:2303/tcp\n", "default-tcp port mapping")
	assert.NotContains(t, out, "Network=host", "publish network should not set Network=host")
}

func TestRenderContainerMinimalOmitsEmptySections(t *testing.T) {
	spec := ContainerSpec{
		Name:    "dzo-minimal",
		Image:   "img",
		Network: NetworkHost,
	}
	out, err := RenderContainer(spec)
	require.NoError(t, err)
	assert.NotContains(t, out, "HealthCmd", "no health config was set")
	assert.NotContains(t, out, "Description=", "empty description section should be omitted")
}

func TestRenderContainerDeterministic(t *testing.T) {
	spec := ContainerSpec{
		Name:        "dzo-x",
		Image:       "img",
		Network:     NetworkHost,
		Environment: map[string]string{"Z": "1", "Y": "2", "X": "3"},
	}
	out1, err := RenderContainer(spec)
	require.NoError(t, err)
	out2, err := RenderContainer(spec)
	require.NoError(t, err)
	require.Equal(t, out1, out2, "RenderContainer is not deterministic")
}

func TestValidateRejectsMissingFields(t *testing.T) {
	cases := []ContainerSpec{
		{Image: "img", Network: NetworkHost},        // missing Name
		{Name: "n", Network: NetworkHost},           // missing Image
		{Name: "n", Image: "img", Network: "bogus"}, // bad Network
		{Name: "n", Image: "img", Network: NetworkHost, Volumes: []Volume{{Source: ""}}},
	}
	for i, c := range cases {
		assert.Error(t, c.Validate(), "case %d: expected a validation error for %+v", i, c)
	}
}

func TestRenderContainerValidatesFirst(t *testing.T) {
	_, err := RenderContainer(ContainerSpec{})
	require.Error(t, err, "expected an error for an empty spec")
}

func TestFormatDurationUnits(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s",
		2 * time.Minute:  "2m",
		1 * time.Hour:    "1h",
		45 * time.Minute: "45m",
	}
	for d, want := range cases {
		assert.Equal(t, want, formatDuration(d), "formatDuration(%v)", d)
	}
}

func TestRenderContainerExecServiceAndNotify(t *testing.T) {
	spec := ContainerSpec{
		Name:            "dzo-x",
		Image:           "img",
		Network:         NetworkHost,
		WorkingDir:      "/dayz",
		Exec:            []string{"./DayZServer", "-mod=@1;@2", `-name=a "b" 100%`, "-p=$X"},
		NotifyHealthy:   true,
		PreStart:        []string{"/usr/bin/dzo instance render x"},
		RestartSec:      15 * time.Second,
		TimeoutStartSec: time.Hour,
	}
	out, err := RenderContainer(spec)
	require.NoError(t, err)
	for _, want := range []string{
		"WorkingDir=/dayz\n",
		`Exec=./DayZServer "-mod=@1;@2" "-name=a \"b\" 100%%" -p=$$X` + "\n",
		"Notify=healthy\n",
		"ExecStartPre=/usr/bin/dzo instance render x\n",
		"RestartSec=15s\n",
		"TimeoutStartSec=1h\n",
	} {
		assert.Contains(t, out, want)
	}
	assert.Equal(t, 1, strings.Count(out, "Exec="), "want exactly one Exec= line")
}

func TestRenderContainerPostStop(t *testing.T) {
	spec := ContainerSpec{Name: "dzo-x", Image: "localhost/dzo-runtime:latest", Network: NetworkHost}
	spec.PostStop = []string{"/usr/bin/dzo instance hook x post_stop"}
	out, err := RenderContainer(spec)
	require.NoError(t, err)
	assert.Contains(t, out, "ExecStopPost=-/usr/bin/dzo instance hook x post_stop\n", "a post_stop hook must not fail the stop")
}

func TestRenderContainerPublishHostAddress(t *testing.T) {
	out, err := RenderContainer(ContainerSpec{
		Name: "dzo-x", Image: "localhost/dzo-runtime:latest", Network: NetworkPublish,
		Ports: []Port{
			{HostIP: "127.0.0.1", HostPort: 3303, ContainerPort: 3303, Protocol: "udp"},
			{HostIP: "::1", HostPort: 3304, ContainerPort: 3304, Protocol: "udp"},
		},
	})
	require.NoError(t, err)
	for _, want := range []string{"PublishPort=127.0.0.1:3303:3303/udp\n", "PublishPort=[::1]:3304:3304/udp\n"} {
		assert.Contains(t, out, want)
	}
}

func TestRenderContainerCoreDumps(t *testing.T) {
	spec := ContainerSpec{Name: "dzo-deerisle", Image: "localhost/dzo-runtime:latest", Network: NetworkHost}
	out, err := RenderContainer(spec)
	require.NoError(t, err)
	assert.Contains(t, out, "Ulimit=core=0\n", "a crashing server must not dump core by default")
	spec.CoreDumps = true
	out, err = RenderContainer(spec)
	require.NoError(t, err)
	assert.NotContains(t, out, "Ulimit=", "CoreDumps must leave the core limit alone")
}

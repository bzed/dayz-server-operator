// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package quadlet

import (
	"strings"
	"testing"
	"time"
)

func TestRenderContainerRestartLimit(t *testing.T) {
	spec := ContainerSpec{
		Name: "dzo-deerisle", Image: "localhost/dzo-runtime:latest", Network: NetworkHost,
		RestartLimitBurst: 5, RestartLimitInterval: 30 * time.Minute,
	}
	out, err := RenderContainer(spec)
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}
	if !strings.Contains(out, "StartLimitBurst=5") {
		t.Errorf("output missing StartLimitBurst: %s", out)
	}
	if !strings.Contains(out, "StartLimitIntervalSec=30m") {
		t.Errorf("output missing StartLimitIntervalSec: %s", out)
	}
}

func TestRenderContainerOmitsRestartLimitWhenZero(t *testing.T) {
	spec := ContainerSpec{Name: "dzo-deerisle", Image: "localhost/dzo-runtime:latest", Network: NetworkHost}
	out, err := RenderContainer(spec)
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}
	if strings.Contains(out, "StartLimit") {
		t.Errorf("output should omit StartLimit* when unset: %s", out)
	}
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
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}

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
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}

	// Environment keys must be sorted (determinism, NFR-02).
	aIdx := strings.Index(out, "Environment=A=1")
	bIdx := strings.Index(out, "Environment=B=2")
	if aIdx == -1 || bIdx == -1 || aIdx > bIdx {
		t.Errorf("Environment lines not in sorted order:\n%s", out)
	}
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
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}
	if !strings.Contains(out, "PublishPort=3302:2302/udp\n") {
		t.Errorf("missing udp port mapping:\n%s", out)
	}
	if !strings.Contains(out, "PublishPort=3303:2303/tcp\n") {
		t.Errorf("missing default-tcp port mapping:\n%s", out)
	}
	if strings.Contains(out, "Network=host") {
		t.Errorf("publish network should not set Network=host:\n%s", out)
	}
}

func TestRenderContainerMinimalOmitsEmptySections(t *testing.T) {
	spec := ContainerSpec{
		Name:    "dzo-minimal",
		Image:   "img",
		Network: NetworkHost,
	}
	out, err := RenderContainer(spec)
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}
	if strings.Contains(out, "HealthCmd") {
		t.Errorf("no health config was set, should not appear:\n%s", out)
	}
	if strings.Contains(out, "Description=") {
		t.Errorf("empty description section should be omitted:\n%s", out)
	}
}

func TestRenderContainerDeterministic(t *testing.T) {
	spec := ContainerSpec{
		Name:        "dzo-x",
		Image:       "img",
		Network:     NetworkHost,
		Environment: map[string]string{"Z": "1", "Y": "2", "X": "3"},
	}
	out1, err := RenderContainer(spec)
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}
	out2, err := RenderContainer(spec)
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}
	if out1 != out2 {
		t.Fatalf("RenderContainer is not deterministic:\n%s\nvs\n%s", out1, out2)
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	cases := []ContainerSpec{
		{Image: "img", Network: NetworkHost},        // missing Name
		{Name: "n", Network: NetworkHost},           // missing Image
		{Name: "n", Image: "img", Network: "bogus"}, // bad Network
		{Name: "n", Image: "img", Network: NetworkHost, Volumes: []Volume{{Source: ""}}},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: expected a validation error for %+v", i, c)
		}
	}
}

func TestRenderContainerValidatesFirst(t *testing.T) {
	if _, err := RenderContainer(ContainerSpec{}); err == nil {
		t.Fatal("expected an error for an empty spec")
	}
}

func TestFormatDurationUnits(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s",
		2 * time.Minute:  "2m",
		1 * time.Hour:    "1h",
		45 * time.Minute: "45m",
	}
	for d, want := range cases {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
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
	if err != nil {
		t.Fatalf("RenderContainer: %v", err)
	}
	for _, want := range []string{
		"WorkingDir=/dayz\n",
		`Exec=./DayZServer "-mod=@1;@2" "-name=a \"b\" 100%%" -p=$$X` + "\n",
		"Notify=healthy\n",
		"ExecStartPre=/usr/bin/dzo instance render x\n",
		"RestartSec=15s\n",
		"TimeoutStartSec=1h\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "Exec="); n != 1 {
		t.Errorf("want exactly one Exec= line, got %d", n)
	}
}

func TestRenderContainerPostStop(t *testing.T) {
	spec := ContainerSpec{Name: "dzo-x", Image: "localhost/dzo-runtime:latest", Network: NetworkHost}
	spec.PostStop = []string{"/usr/bin/dzo instance hook x post_stop"}
	out, err := RenderContainer(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ExecStopPost=-/usr/bin/dzo instance hook x post_stop\n") {
		t.Errorf("a post_stop hook must not fail the stop:\n%s", out)
	}
}

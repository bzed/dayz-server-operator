// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package quadlet renders podman quadlet unit files (§C4/§C5): dzo
// generates these itself rather than relying on `podman generate systemd`
// (deprecated, A8#12) or the `podman quadlet` management CLI (newer than
// the podman 5.4.2 baseline, §C0). Only the keys the plan lists as
// verified on 5.4 are used.
package quadlet

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Network selects how a container's ports reach the host (D6).
type Network string

const (
	NetworkHost    Network = "host"
	NetworkPublish Network = "publish"
)

// Port is one published port mapping, used only when Network is "publish".
type Port struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp" or "udp"; empty defaults to tcp
}

// Volume is one bind mount or named volume, "host:container[:ro]".
type Volume struct {
	Source      string
	Destination string
	ReadOnly    bool
	// Overlay mounts the source as a podman overlay (":O"): the container can
	// write, the source is never touched. Use it where ":ro" must not be used.
	Overlay bool
}

// Health describes the container's startup/liveness probe (FR-18, D18).
type Health struct {
	Cmd       string
	Interval  time.Duration
	Retries   int
	OnFailure string // e.g. "kill"; empty omits HealthOnFailure

	// StartupCmd is the probe run until the server first answers (§C5's
	// HealthStartup* keys); the unit is only "healthy" after it succeeds.
	StartupCmd      string
	StartupInterval time.Duration
	StartupRetries  int
}

// ContainerSpec is dzo's input to render one instance's .container quadlet.
// It intentionally does not depend on the site repo's instance.yaml schema
// (§C3, a later phase): callers translate their own config into this
// smaller, stable shape.
type ContainerSpec struct {
	Name        string // quadlet unit name, e.g. "dzo-deerisle"
	Description string
	Image       string

	Network Network
	Ports   []Port // only used when Network == NetworkPublish

	Volumes     []Volume
	Environment map[string]string
	Exec        []string // command line appended after the image (one Exec= line, quoted)
	WorkingDir  string   // omitted when empty

	// NotifyHealthy makes the unit "active" only once the health check
	// passes (Notify=healthy).
	NotifyHealthy bool
	// PreStart commands become [Service] ExecStartPre= lines (the in-place
	// mission render, §C5).
	PreStart        []string
	RestartSec      time.Duration //nolint:staticcheck // the systemd directive name; 0 omits
	TimeoutStartSec time.Duration //nolint:staticcheck // the systemd directive name; 0 omits

	Health Health

	Memory string // e.g. "24G"; empty omits Memory
	CPUs   string // e.g. "4"; empty omits CPUs

	StopTimeout time.Duration // 0 omits StopTimeout

	// RestartLimitBurst/RestartLimitInterval configure systemd's own
	// start-rate limiting (StartLimitBurst=/StartLimitIntervalSec=, in the
	// [Unit] section) - one of F3's three restart-storm-protection layers
	// (§C5): a crash loop or repeated health-check kill lands the unit in
	// "failed" once this limit is hit, rather than flapping forever. Zero
	// values omit both keys, leaving systemd's own defaults in effect.
	RestartLimitBurst    int
	RestartLimitInterval time.Duration
}

// Validate checks the invariants RenderContainer relies on.
func (s ContainerSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("quadlet: Name is required")
	}
	if s.Image == "" {
		return fmt.Errorf("quadlet: Image is required")
	}
	if s.Network != NetworkHost && s.Network != NetworkPublish {
		return fmt.Errorf("quadlet: Network must be %q or %q, got %q", NetworkHost, NetworkPublish, s.Network)
	}
	for _, v := range s.Volumes {
		if v.Source == "" || v.Destination == "" {
			return fmt.Errorf("quadlet: volume with an empty source or destination: %+v", v)
		}
	}
	return nil
}

// RenderContainer renders a systemd quadlet .container unit for spec.
// Output is deterministic (map iteration is sorted) so repeated renders of
// the same spec produce byte-identical files.
func RenderContainer(spec ContainerSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}

	var b strings.Builder
	writeSection(&b, "Unit", func(kv *kvWriter) {
		if spec.Description != "" {
			kv.set("Description", spec.Description)
		}
		if spec.RestartLimitBurst > 0 {
			kv.set("StartLimitBurst", strconv.Itoa(spec.RestartLimitBurst))
		}
		if spec.RestartLimitInterval > 0 {
			kv.set("StartLimitIntervalSec", formatDuration(spec.RestartLimitInterval))
		}
	})

	writeSection(&b, "Container", func(kv *kvWriter) {
		kv.set("Image", spec.Image)
		kv.set("ContainerName", spec.Name)

		switch spec.Network {
		case NetworkHost:
			kv.set("Network", "host")
		case NetworkPublish:
			for _, p := range spec.Ports {
				proto := p.Protocol
				if proto == "" {
					proto = "tcp"
				}
				kv.set("PublishPort", fmt.Sprintf("%d:%d/%s", p.HostPort, p.ContainerPort, proto))
			}
		}

		volumes := append([]Volume(nil), spec.Volumes...)
		for _, v := range volumes {
			val := v.Source + ":" + v.Destination
			switch {
			case v.Overlay:
				val += ":O"
			case v.ReadOnly:
				val += ":ro"
			}
			kv.set("Volume", val)
		}

		envKeys := make([]string, 0, len(spec.Environment))
		for k := range spec.Environment {
			envKeys = append(envKeys, k)
		}
		sort.Strings(envKeys)
		for _, k := range envKeys {
			kv.set("Environment", k+"="+spec.Environment[k])
		}

		if spec.WorkingDir != "" {
			kv.set("WorkingDir", spec.WorkingDir)
		}
		if len(spec.Exec) > 0 {
			// Quadlet takes the last Exec= line only, so the command line is one line.
			words := make([]string, len(spec.Exec))
			for i, a := range spec.Exec {
				words[i] = quoteWord(a)
			}
			kv.set("Exec", strings.Join(words, " "))
		}

		if spec.Health.Cmd != "" {
			kv.set("HealthCmd", spec.Health.Cmd)
			if spec.Health.Interval > 0 {
				kv.set("HealthInterval", formatDuration(spec.Health.Interval))
			}
			if spec.Health.Retries > 0 {
				kv.set("HealthRetries", strconv.Itoa(spec.Health.Retries))
			}
			if spec.Health.OnFailure != "" {
				kv.set("HealthOnFailure", spec.Health.OnFailure)
			}
		}
		if spec.Health.StartupCmd != "" {
			kv.set("HealthStartupCmd", spec.Health.StartupCmd)
			if spec.Health.StartupInterval > 0 {
				kv.set("HealthStartupInterval", formatDuration(spec.Health.StartupInterval))
			}
			if spec.Health.StartupRetries > 0 {
				kv.set("HealthStartupRetries", strconv.Itoa(spec.Health.StartupRetries))
			}
			kv.set("HealthStartupSuccess", "1")
		}
		if spec.NotifyHealthy {
			kv.set("Notify", "healthy")
		}

		if spec.Memory != "" {
			kv.set("PodmanArgs", "--memory="+spec.Memory)
		}
		if spec.CPUs != "" {
			kv.set("PodmanArgs", "--cpus="+spec.CPUs)
		}
		if spec.StopTimeout > 0 {
			// StopTimeout= takes whole seconds (podman --stop-timeout); there is no ContainerStopTimeout.
			kv.set("StopTimeout", strconv.FormatInt(int64((spec.StopTimeout+time.Second-1)/time.Second), 10))
		}
	})

	writeSection(&b, "Service", func(kv *kvWriter) {
		for _, c := range spec.PreStart {
			kv.set("ExecStartPre", c)
		}
		kv.set("Restart", "always")
		if spec.RestartSec > 0 {
			kv.set("RestartSec", formatDuration(spec.RestartSec))
		}
		if spec.TimeoutStartSec > 0 {
			kv.set("TimeoutStartSec", formatDuration(spec.TimeoutStartSec))
		}
	})

	writeSection(&b, "Install", func(kv *kvWriter) {
		kv.set("WantedBy", "default.target")
	})

	return b.String(), nil
}

// formatDuration renders d the way systemd time spans expect (e.g. "45m",
// "60s"), always as a single unit for the values dzo uses (seconds up to a
// few hours).
func formatDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int64(d/time.Second))
}

// quoteWord quotes one Exec= word for systemd's command-line syntax when it
// holds whitespace, quotes, backslashes or ';' (as in -mod=@a;@b), and
// escapes the '%' and '$' systemd would otherwise expand.
func quoteWord(w string) string {
	w = strings.NewReplacer("%", "%%", "$", "$$").Replace(w)
	if w == "" || strings.ContainsAny(w, " \t\n\"'\\;") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(w) + `"`
	}
	return w
}

type kvWriter struct {
	b *strings.Builder
}

func (w *kvWriter) set(key, value string) {
	fmt.Fprintf(w.b, "%s=%s\n", key, value)
}

func writeSection(b *strings.Builder, name string, fill func(kv *kvWriter)) {
	var section strings.Builder
	fill(&kvWriter{b: &section})
	if section.Len() == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "[%s]\n", name)
	b.WriteString(section.String())
}

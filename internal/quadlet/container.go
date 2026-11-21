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
	"net"
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
	HostIP        string // the host address to publish on; empty means every address
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
	PreStart []string
	// PostStop commands become [Service] ExecStopPost= lines, run after the container is gone
	// and before quadlet's own cleanup; a failing command does not fail the stop ("-" prefix).
	PostStop []string
	// ExecStop commands become [Service] ExecStop= lines. They run before the
	// ExecStop quadlet itself adds (podman rm -f), so they can shut the server
	// down gracefully first.
	ExecStop []string
	// TimeoutStopSec is the whole stop budget of the unit (0 omits it).
	TimeoutStopSec  time.Duration //nolint:staticcheck // the systemd directive name
	RestartSec      time.Duration //nolint:staticcheck // the systemd directive name; 0 omits
	TimeoutStartSec time.Duration //nolint:staticcheck // the systemd directive name; 0 omits

	Health Health

	Memory string // e.g. "24G"; empty omits Memory
	CPUs   string // e.g. "4"; empty omits CPUs
	// CoreDumps lets a crashing server write a core file. Off by default: a core of a
	// multi-GB server stalls the disk, and with it every other server on the host.
	CoreDumps bool

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
	writeUnit(&b, spec)

	writeSection(&b, "Container", func(kv *kvWriter) {
		kv.set("Image", spec.Image)
		kv.set("ContainerName", spec.Name)

		switch spec.Network {
		case NetworkHost:
			kv.set("Network", "host")
		case NetworkPublish:
			for _, p := range spec.Ports {
				kv.set("PublishPort", portValue(p))
			}
		}

		for _, v := range spec.Volumes {
			kv.set("Volume", volumeValue(v))
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

		if !spec.CoreDumps {
			kv.set("Ulimit", "core=0")
		}
		if spec.Memory != "" {
			kv.set("PodmanArgs", "--memory="+spec.Memory)
		}
		if spec.CPUs != "" {
			kv.set("PodmanArgs", "--cpus="+spec.CPUs)
		}
		if spec.StopTimeout > 0 {
			// StopTimeout= takes whole seconds (podman --stop-timeout); there is no ContainerStopTimeout.
			kv.set("StopTimeout", stopTimeoutSeconds(spec.StopTimeout))
		}
	})

	writeSection(&b, "Service", func(kv *kvWriter) {
		writeService(spec, kv)
	})

	writeSection(&b, "Install", func(kv *kvWriter) {
		kv.set("WantedBy", "default.target")
	})

	return b.String(), nil
}

// writeUnit writes the [Unit] section every instance unit has: the
// description and systemd's own start-rate limit (F3's crash-loop guard).
func writeUnit(b *strings.Builder, spec ContainerSpec) {
	writeSection(b, "Unit", func(kv *kvWriter) {
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
}

// writeService writes the [Service] lines the quadlet and the supervised unit
// share: dzo's pre-start and post-stop commands, the restart policy and the
// stop budget. Quadlet adds the container's ExecStart= and stop itself;
// RenderSupervised adds Type=notify, ExecStart= (the supervisor) and
// KillMode=. Sharing the writer keeps the life cycle of both modes the same.
func writeService(spec ContainerSpec, kv *kvWriter) {
	for _, c := range spec.PreStart {
		kv.set("ExecStartPre", c)
	}
	kv.set("Restart", "always")
	// DayZServer exits with 255 on the SIGTERM of a requested stop (journal of a real
	// stop: "status=255/EXCEPTION"); without this the unit ends up "failed" after
	// every clean stop.
	kv.set("SuccessExitStatus", "255")
	if spec.RestartSec > 0 {
		kv.set("RestartSec", formatDuration(spec.RestartSec))
	}
	for _, c := range spec.PostStop {
		kv.set("ExecStopPost", "-"+c)
	}
	for _, c := range spec.ExecStop {
		kv.set("ExecStop", c)
	}
	if spec.TimeoutStopSec > 0 {
		kv.set("TimeoutStopSec", formatDuration(spec.TimeoutStopSec))
	} else if spec.StopTimeout > 0 {
		// systemd's default (90s) is shorter than the grace podman gives the server: without
		// this, systemd kills conmon first and leaves the container behind ("Exited (-1)").
		kv.set("TimeoutStopSec", formatDuration(spec.StopTimeout+30*time.Second))
	}
	if spec.TimeoutStartSec > 0 {
		kv.set("TimeoutStartSec", formatDuration(spec.TimeoutStartSec))
	}
}

// portValue renders one published port the way both quadlet's PublishPort=
// and podman's -p take it: "[host:]hostPort:containerPort/proto".
func portValue(p Port) string {
	proto := p.Protocol
	if proto == "" {
		proto = "tcp"
	}
	host := ""
	if p.HostIP != "" {
		host = net.JoinHostPort(p.HostIP, "") // "127.0.0.1:" or "[::1]:"
	}
	return fmt.Sprintf("%s%d:%d/%s", host, p.HostPort, p.ContainerPort, proto)
}

// volumeValue renders one mount the way both quadlet's Volume= and podman's
// -v take it: "source:destination[:O|:ro]".
func volumeValue(v Volume) string {
	val := v.Source + ":" + v.Destination
	switch {
	case v.Overlay:
		val += ":O"
	case v.ReadOnly:
		val += ":ro"
	}
	return val
}

// stopTimeoutSeconds renders d as the whole seconds podman's --stop-timeout
// and quadlet's StopTimeout= take.
func stopTimeoutSeconds(d time.Duration) string {
	return strconv.FormatInt(int64((d+time.Second-1)/time.Second), 10)
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

// quoteWord quotes one word for systemd's command-line syntax, which is what
// both a quadlet's Exec= and a supervised unit's ExecStart= are: it escapes the
// '%' and '$' systemd expands as specifiers and variables (also inside quotes),
// and double-quotes the word when it holds whitespace, quotes, backslashes or
// ';' (as in -mod=@a;@b, or a ';' that is a word of its own and would
// otherwise separate two commands). Control characters are written as C
// escapes inside the quotes, so no word can end the directive line.
func quoteWord(w string) string {
	w = strings.NewReplacer("%", "%%", "$", "$$").Replace(w)
	if !needsQuoting(w) {
		return w
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(w); i++ {
		switch c := w[i]; {
		case c == '\\' || c == '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// needsQuoting reports whether systemd's parser needs quoteWord's double
// quotes around w.
func needsQuoting(w string) bool {
	if w == "" {
		return true
	}
	return strings.ContainsAny(w, " \t\n\r\"'\\;") ||
		strings.ContainsFunc(w, func(r rune) bool { return r < 0x20 || r == 0x7f })
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

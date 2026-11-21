// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package quadlet

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RunArgs returns the arguments that run spec's container: the words after
// `podman`, starting with `run --name … --replace --rm`. In podman.mode
// remote the supervisor (`dzo instance supervise`) runs them instead of
// leaving the unit to quadlet, so every key RenderContainer emits for the
// container is translated to the equivalent podman flag here. RenderSupervised
// writes the words into the unit's ExecStart, so that the unit itself holds
// the container it was rendered for.
// TestRunArgsMatchesQuadlet keeps the two in step: a new quadlet key fails it
// until its mapping is decided.
//
// The run arguments never detach (-d): the supervisor stays in the foreground
// and owns the container's life cycle, and never ask podman to notify systemd
// (--sdnotify): the supervised unit is Type=notify and the supervisor reports
// readiness itself once the startup probe passes.
func RunArgs(spec ContainerSpec) ([]string, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	args := []string{"run", "--name", spec.Name, "--replace", "--rm"}

	switch spec.Network {
	case NetworkHost:
		args = append(args, "--network", "host")
	case NetworkPublish:
		for _, p := range spec.Ports {
			args = append(args, "-p", portValue(p))
		}
	}

	for _, v := range spec.Volumes {
		args = append(args, "-v", volumeValue(v))
	}

	envKeys := make([]string, 0, len(spec.Environment))
	for k := range spec.Environment {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+spec.Environment[k])
	}

	if spec.WorkingDir != "" {
		args = append(args, "--workdir", spec.WorkingDir)
	}

	if spec.Health.Cmd != "" {
		args = append(args, "--health-cmd", spec.Health.Cmd)
		if spec.Health.Interval > 0 {
			args = append(args, "--health-interval", formatDuration(spec.Health.Interval))
		}
		if spec.Health.Retries > 0 {
			args = append(args, "--health-retries", strconv.Itoa(spec.Health.Retries))
		}
		if spec.Health.OnFailure != "" {
			args = append(args, "--health-on-failure", spec.Health.OnFailure)
		}
	}
	if spec.Health.StartupCmd != "" {
		args = append(args, "--health-startup-cmd", spec.Health.StartupCmd)
		if spec.Health.StartupInterval > 0 {
			args = append(args, "--health-startup-interval", formatDuration(spec.Health.StartupInterval))
		}
		if spec.Health.StartupRetries > 0 {
			args = append(args, "--health-startup-retries", strconv.Itoa(spec.Health.StartupRetries))
		}
		args = append(args, "--health-startup-success", "1")
	}

	if !spec.CoreDumps {
		args = append(args, "--ulimit", "core=0")
	}
	// Memory= and CPUs= are PodmanArgs= in the quadlet; keep podman's words.
	if spec.Memory != "" {
		args = append(args, "--memory="+spec.Memory)
	}
	if spec.CPUs != "" {
		args = append(args, "--cpus="+spec.CPUs)
	}
	if spec.StopTimeout > 0 {
		args = append(args, "--stop-timeout", stopTimeoutSeconds(spec.StopTimeout))
	}

	args = append(args, spec.Image)
	return append(args, spec.Exec...), nil
}

// RenderSupervised renders the plain systemd user unit of an instance when
// podman runs remotely (podman.mode: remote): systemd starts the supervisor,
// which runs the container through podman's socket. [Unit], [Install] and
// the [Service] lines dzo controls are the quadlet's, so a mode switch changes
// as little of the life cycle as possible.
//
// The unit carries the podman run arguments itself, after a `--`: the supervisor
// runs exactly the container the unit was rendered for, also when systemd
// restarts a crashed server. That is what holding a unit means in local mode
// too - the quadlet's paths are the generations - and it keeps a pending update
// pending through a restart instead of applying it silently. The unit is
// Type=notify: the supervisor reports readiness once the container's startup
// probe succeeds, which is what quadlet's Notify=healthy does for a quadlet.
func RenderSupervised(spec ContainerSpec, execStart string) (string, error) {
	args, err := RunArgs(spec) // RunArgs validates the spec
	if err != nil {
		return "", err
	}
	if execStart == "" {
		return "", fmt.Errorf("quadlet: execStart is required")
	}
	words := make([]string, len(args))
	for i, a := range args {
		words[i] = quoteWord(a)
	}
	line := execStart + " -- " + strings.Join(words, " ")

	var b strings.Builder
	writeUnit(&b, spec)

	writeSection(&b, "Service", func(kv *kvWriter) {
		kv.set("Type", "notify")
		kv.set("NotifyAccess", "main")
		kv.set("ExecStart", line)
		// The stop signal reaches the supervisor only; it hands it to podman,
		// which gives the server the whole stop grace before SIGKILL.
		kv.set("KillMode", "mixed")
		writeService(spec, kv)
	})

	writeSection(&b, "Install", func(kv *kvWriter) {
		kv.set("WantedBy", "default.target")
	})

	return b.String(), nil
}

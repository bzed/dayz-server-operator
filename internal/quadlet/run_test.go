// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package quadlet_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// resolvedSpecs builds the container specs behind the resolve goldens
// (internal/resolve/testdata/*.golden) the same way resolve's own tests do, so
// that the parity test covers what dzo really renders.
func resolvedSpecs(t *testing.T) map[string]quadlet.ContainerSpec {
	t.Helper()
	data := t.TempDir()
	cfg := config.Default()
	cfg.Paths = config.Paths{
		Data: data, Instances: data + "/instances", Snapshots: data + "/snapshots",
		Cache: data + "/cache", Secrets: data + "/secrets", DB: data + "/db", Site: "../resolve/testdata/site",
	}
	for _, p := range [][2]string{
		{"products/dayz-stable", "100"},
		{"products/dayz-experimental", "200"},
		{"workshop/221100/1559212036", "111"},
		{"workshop/221100/1828439124", "222"},
		{"local/dzo-admin", "abcd"},
	} {
		gen := filepath.Join(cfg.Paths.Cache, p[0], p[1])
		require.NoError(t, os.MkdirAll(gen, 0o750))
		require.NoError(t, os.Symlink(p[1], filepath.Join(cfg.Paths.Cache, p[0], "current")))
	}
	tree, err := site.LoadTree("../resolve/testdata/site")
	require.NoError(t, err)
	all, err := resolve.ResolveAll(cfg, tree)
	require.NoError(t, err)
	specs := map[string]quadlet.ContainerSpec{}
	for _, inst := range all {
		require.Empty(t, inst.Missing, "%s: unexpected Missing", inst.Name)
		specs[inst.Name] = inst.Quadlet
	}
	require.Contains(t, specs, "alpha")
	require.Contains(t, specs, "beta")
	return specs
}

// kv is one key=value line of a unit section.
type kv struct {
	key, value string
}

// section returns the key=value lines of one [name] section of a unit.
func section(t *testing.T, unit, name string) []kv {
	t.Helper()
	var out []kv
	in := false
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = line == "["+name+"]"
			continue
		}
		if !in || line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		require.True(t, ok, "not a key=value line in [%s]: %q", name, line)
		out = append(out, kv{key: key, value: value})
	}
	require.NotEmpty(t, out, "no [%s] section in:\n%s", name, unit)
	return out
}

// values returns every value of the repeated argument flag, in order.
func values(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

// flagValue returns the value of a single-valued argument flag, accepting
// both `--flag value` and `--flag=value`.
func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, a := range args {
		if a == flag {
			require.Less(t, i+1, len(args), "%s needs a value", flag)
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	require.Failf(t, "flag missing", "%s not in %v", flag, args)
	return ""
}

// unquoteExec parses one quadlet Exec= value the way systemd splits it: words
// separated by whitespace, double quotes grouping, backslash escaping inside
// quotes, and the %% / $$ escapes quoteWord adds.
func unquoteExec(s string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord, inQuote := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\\':
			i++
			if i == len(s) {
				return nil, fmt.Errorf("trailing backslash in %q", s)
			}
			word.WriteByte(s[i])
		case c == '"':
			inQuote = !inQuote
			inWord = true
		case !inQuote && (c == ' ' || c == '\t'):
			if inWord {
				words = append(words, unescapeWord(word.String()))
				word.Reset()
				inWord = false
			}
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quote in %q", s)
	}
	if inWord {
		words = append(words, unescapeWord(word.String()))
	}
	return words, nil
}

func unescapeWord(w string) string {
	return strings.NewReplacer("%%", "%", "$$", "$").Replace(w)
}

func TestRunArgsGolden(t *testing.T) {
	spec := quadlet.ContainerSpec{
		Name:        "dzo-everything",
		Description: "every key set",
		Image:       "localhost/dzo-runtime:test",
		Network:     quadlet.NetworkPublish,
		Ports: []quadlet.Port{
			{HostIP: "127.0.0.1", HostPort: 2403, ContainerPort: 2403, Protocol: "udp"},
			{HostPort: 2402, ContainerPort: 2402},
		},
		Volumes: []quadlet.Volume{
			{Source: "/a", Destination: "/b", Overlay: true},
			{Source: "/c", Destination: "/d", ReadOnly: true},
			{Source: "/e", Destination: "/f"},
		},
		Environment:     map[string]string{"B": "2", "A": "1"},
		Exec:            []string{"./DayZServer", "-mod=@1;@2", "-name=a b"},
		WorkingDir:      "/dayz",
		NotifyHealthy:   true,
		PreStart:        []string{"/usr/bin/dzo instance render everything"},
		PostStop:        []string{"/usr/bin/dzo logs crash-summary everything"},
		ExecStop:        []string{"/usr/bin/dzo instance shutdown everything --timeout 30s"},
		TimeoutStopSec:  3 * time.Minute,
		RestartSec:      15 * time.Second,
		TimeoutStartSec: 45 * time.Minute,
		Health: quadlet.Health{
			Cmd:             "/usr/local/bin/dzo health live",
			Interval:        time.Minute,
			Retries:         5,
			OnFailure:       "kill",
			StartupCmd:      "/usr/local/bin/dzo health startup",
			StartupInterval: 30 * time.Second,
			StartupRetries:  60,
		},
		Memory:               "24G",
		CPUs:                 "4",
		StopTimeout:          120 * time.Second,
		RestartLimitBurst:    5,
		RestartLimitInterval: 30 * time.Minute,
	}
	want := []string{
		"run",
		"--name", "dzo-everything",
		"--replace",
		"--rm",
		"-p", "127.0.0.1:2403:2403/udp",
		"-p", "2402:2402/tcp",
		"-v", "/a:/b:O",
		"-v", "/c:/d:ro",
		"-v", "/e:/f",
		"-e", "A=1",
		"-e", "B=2",
		"--workdir", "/dayz",
		"--health-cmd", "/usr/local/bin/dzo health live",
		"--health-interval", "1m",
		"--health-retries", "5",
		"--health-on-failure", "kill",
		"--health-startup-cmd", "/usr/local/bin/dzo health startup",
		"--health-startup-interval", "30s",
		"--health-startup-retries", "60",
		"--health-startup-success", "1",
		"--ulimit", "core=0",
		"--memory=24G",
		"--cpus=4",
		"--stop-timeout", "120",
		"localhost/dzo-runtime:test",
		"./DayZServer",
		"-mod=@1;@2",
		"-name=a b",
	}
	got, err := quadlet.RunArgs(spec)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	for _, a := range got {
		assert.NotContains(t, []string{"--cgroups", "--sdnotify", "-d"}, a, "run arguments must not carry %q", a)
	}
}

func TestRunArgsHostNetworkAndCoreDumps(t *testing.T) {
	spec := quadlet.ContainerSpec{
		Name: "dzo-x", Image: "img", Network: quadlet.NetworkHost,
		Ports:     []quadlet.Port{{HostPort: 1, ContainerPort: 2}},
		CoreDumps: true,
	}
	args, err := quadlet.RunArgs(spec)
	require.NoError(t, err)
	assert.Equal(t, []string{"--network", "host"}, args[5:7], "host networking, no published ports")
	assert.NotContains(t, args, "--ulimit", "CoreDumps must leave the core limit alone")
	assert.NotContains(t, args, "-p")
}

func TestRunArgsValidatesFirst(t *testing.T) {
	_, err := quadlet.RunArgs(quadlet.ContainerSpec{})
	require.Error(t, err)
}

func TestRunArgsMatchesQuadlet(t *testing.T) {
	for name, spec := range resolvedSpecs(t) {
		t.Run(name, func(t *testing.T) {
			unit, err := quadlet.RenderContainer(spec)
			require.NoError(t, err)
			args, err := quadlet.RunArgs(spec)
			require.NoError(t, err)

			var wantVolumes, wantPorts, wantEnv, wantNetworks []string
			var execWords []string
			image := ""
			for _, line := range section(t, unit, "Container") {
				switch line.key {
				case "Image":
					image = line.value
				case "ContainerName":
					assert.Equal(t, line.value, flagValue(t, args, "--name"))
				case "Network":
					wantNetworks = append(wantNetworks, line.value)
				case "PublishPort":
					wantPorts = append(wantPorts, line.value)
				case "Volume":
					wantVolumes = append(wantVolumes, line.value)
				case "Environment":
					wantEnv = append(wantEnv, line.value)
				case "WorkingDir":
					assert.Equal(t, line.value, flagValue(t, args, "--workdir"))
				case "Exec":
					execWords, err = unquoteExec(line.value)
					require.NoError(t, err)
					assert.Equal(t, spec.Exec, execWords, "Exec= words must be the spec's command line")
					assert.NotContains(t, strings.Join(args, " "), "%%", "RunArgs must not keep systemd's %% escape")
				case "HealthCmd":
					assert.Equal(t, line.value, flagValue(t, args, "--health-cmd"))
				case "HealthInterval":
					assert.Equal(t, line.value, flagValue(t, args, "--health-interval"))
				case "HealthRetries":
					assert.Equal(t, line.value, flagValue(t, args, "--health-retries"))
				case "HealthOnFailure":
					assert.Equal(t, line.value, flagValue(t, args, "--health-on-failure"))
				case "HealthStartupCmd":
					assert.Equal(t, line.value, flagValue(t, args, "--health-startup-cmd"))
				case "HealthStartupInterval":
					assert.Equal(t, line.value, flagValue(t, args, "--health-startup-interval"))
				case "HealthStartupRetries":
					assert.Equal(t, line.value, flagValue(t, args, "--health-startup-retries"))
				case "HealthStartupSuccess":
					assert.Equal(t, line.value, flagValue(t, args, "--health-startup-success"))
				case "Ulimit":
					assert.Equal(t, line.value, flagValue(t, args, "--ulimit"))
				case "PodmanArgs":
					for _, w := range strings.Fields(line.value) {
						assert.Contains(t, args, w, "PodmanArgs value must reach podman verbatim")
					}
				case "StopTimeout":
					assert.Equal(t, line.value, flagValue(t, args, "--stop-timeout"))
				case "Notify":
					// Quadlet's Notify=healthy makes the quadlet's generated
					// unit notify systemd once the health check passes. A
					// supervised unit is Type=notify itself and the supervisor
					// reports readiness, so podman must not also send READY=1:
					// no --sdnotify here.
					assert.Equal(t, "healthy", line.value)
					assert.NotContains(t, args, "--sdnotify")
				default:
					// A new quadlet key must be mapped (or explicitly ignored)
					// in RunArgs and this test.
					require.Failf(t, "unhandled quadlet key", "quadlet key %q of [Container] is not handled: decide its podman flag in RunArgs and its check here", line.key)
				}
			}
			assert.Equal(t, wantVolumes, values(args, "-v"), "-v values must match Volume= in order")
			assert.Equal(t, wantPorts, values(args, "-p"), "-p values must match PublishPort= in order")
			assert.Equal(t, wantEnv, values(args, "-e"), "-e values must match Environment= in order")
			assert.Equal(t, wantNetworks, values(args, "--network"), "--network must match Network=")
			require.NotEmpty(t, image)
			assert.Equal(t, append([]string{image}, execWords...), args[len(args)-len(execWords)-1:], "the image and Exec= close the command")
		})
	}
}

func TestRenderSupervisedService(t *testing.T) {
	spec := resolvedSpecs(t)["alpha"]
	container, err := quadlet.RenderContainer(spec)
	require.NoError(t, err)
	supervised, err := quadlet.RenderSupervised(spec, "/usr/bin/dzo instance supervise alpha")
	require.NoError(t, err)

	got := map[string]bool{}
	for _, line := range section(t, supervised, "Service") {
		got[line.key+"="+line.value] = true
	}
	for _, line := range section(t, container, "Service") {
		assert.True(t, got[line.key+"="+line.value], "[Service] line %s=%s must carry over", line.key, line.value)
	}
	for _, want := range []string{
		"Type=notify",
		"NotifyAccess=main",
		"ExecStart=/usr/bin/dzo instance supervise alpha",
		"KillMode=mixed",
	} {
		assert.True(t, got[want], "want [Service] line %s", want)
	}
	assert.Equal(t, section(t, container, "Unit"), section(t, supervised, "Unit"), "[Unit] must be the quadlet's")
	assert.Equal(t, section(t, container, "Install"), section(t, supervised, "Install"), "[Install] must be the quadlet's")
	assert.NotContains(t, supervised, "[Container]", "a supervised unit has no quadlet section")
}

func TestRenderSupervisedRequiresExecStart(t *testing.T) {
	_, err := quadlet.RenderSupervised(quadlet.ContainerSpec{Name: "n", Image: "img", Network: quadlet.NetworkHost}, "")
	require.Error(t, err)
}

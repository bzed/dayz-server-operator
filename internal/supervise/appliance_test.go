// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build appliance

package supervise_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/supervise"
)

// podmanOutput runs one podman command against the socket the test uses.
func podmanOutput(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("podman", args...).CombinedOutput() //nolint:gosec // the test's own arguments
	require.NoError(t, err, "podman %s: %s", strings.Join(args, " "), out)
	return string(out)
}

// TestSuperviseRealPodman replays the spike against a real rootless podman
// socket: start a container through the supervisor, wait for READY=1, stop it
// the way systemd does, and check that --rm leaves nothing behind.
//
// It skips unless DZO_APPLIANCE_PODMAN=1 (make test-appliance). The container
// name carries a random suffix and only that container is removed, so the test
// can run next to a real installation on the same host.
func TestSuperviseRealPodman(t *testing.T) {
	if os.Getenv("DZO_APPLIANCE_PODMAN") != "1" {
		t.Skip("set DZO_APPLIANCE_PODMAN=1 to run the supervisor against a real podman socket")
	}
	if os.Getenv("CONTAINER_HOST") == "" {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		require.NotEmpty(t, runtimeDir, "CONTAINER_HOST is unset and XDG_RUNTIME_DIR is empty")
		t.Setenv("CONTAINER_HOST", "unix://"+filepath.Join(runtimeDir, "podman", "podman.sock"))
	}
	name := fmt.Sprintf("dzo-supervise-test-%d", time.Now().UnixNano())
	t0 := time.Now()

	// Cleanups run last-in, first-out: the supervisor is stopped and waited
	// for before the container is removed.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Minute)
		defer cancel()
		_ = exec.CommandContext(ctx, "podman", "rm", "-f", name).Run() //nolint:errcheck // best effort, only our own container
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	notified := make(chan string, 8)
	done := make(chan runResult, 1)
	finished := make(chan struct{})
	go func() {
		code, err := supervise.Run(ctx, supervise.Options{
			Name: name,
			RunArgs: []string{
				"run", "--name", name, "--replace", "--rm",
				"--health-cmd", "true", "--health-interval", "1s", "--stop-timeout", "10",
				"docker.io/library/debian:trixie-slim", "sleep", "infinity",
			},
			StopTimeout: 10 * time.Second,
			Poll:        250 * time.Millisecond,
			Notify:      func(state string) error { notified <- state; return nil },
			Stdout:      os.Stdout,
			Stderr:      os.Stderr,
		})
		done <- runResult{code: code, err: err}
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(time.Minute):
		}
	})

	var state string
	require.Eventually(t, func() bool {
		select {
		case state = <-notified:
			return true
		default:
			return false
		}
	}, time.Minute, 50*time.Millisecond, "the supervisor never reported readiness")
	t.Logf("ready after %s", time.Since(t0))
	assert.Equal(t, "READY=1", state)
	assert.Equal(t, "true", strings.TrimSpace(podmanOutput(t, "inspect", "-f", "{{.State.Running}}", name)))

	cancel()
	r := waitRun(t, done)
	require.NoError(t, r.err)
	t.Logf("dzo instance supervise exited with %d after %s", r.code, time.Since(t0))

	require.Eventually(t, func() bool {
		return exec.Command("podman", "container", "exists", name).Run() != nil
	}, time.Minute, 100*time.Millisecond, "the --rm container survived the supervisor")
	t.Logf("container gone after %s", time.Since(t0))
}

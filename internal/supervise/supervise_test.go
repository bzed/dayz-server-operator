// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package supervise_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/supervise"
)

// fakePodmanScript stands in for the podman client, in the style of
// writeFakeSystemctl in internal/instance: it logs its arguments to
// calls.log and answers from files the test controls. A run/attach child
// blocks on a FIFO until the test releases it, so the test decides when it
// exits and with which code. The health inspect answers the next line of a
// plan the test writes, so a poll sequence is deterministic; "missing" is a
// container that does not exist.
const fakePodmanScript = `#!/bin/sh
state="{{state}}"
printf '%s\n' "$*" >> "$state/calls.log"
case "$1" in
version)
	if [ -e "$state/version-fails" ]; then
		echo "Error: cannot connect to Podman. Please verify your connection to the remote host." >&2
		exit 125
	fi
	echo 5.8.6
	;;
inspect)
	case "$3" in
	*Health*)
		n=$(cat "$state/health-count" 2>/dev/null || echo 0)
		n=$((n + 1))
		echo "$n" > "$state/health-count"
		plan=$(sed -n "${n}p" "$state/health-plan")
		if [ -z "$plan" ]; then
			plan=$(tail -n 1 "$state/health-plan")
		fi
		if [ "$plan" = "missing" ]; then
			echo "Error: no such object: \"$4\"" >&2
			exit 125
		fi
		echo "$plan"
		;;
	*Running*)
		if [ -e "$state/running-fails" ]; then
			cat "$state/running-fails" >&2
			exit 125
		fi
		cat "$state/running"
		;;
	*ExitCode*)
		if [ -e "$state/exitcode-inspect" ]; then
			cat "$state/exitcode-inspect"
		else
			echo "Error: no such object: \"$4\"" >&2
			exit 125
		fi
		;;
	*)
		echo "fake podman: unexpected inspect format $3" >&2
		exit 1
		;;
	esac
	;;
stop)
	if [ -e "$state/stop-fails" ]; then
		echo "Error: cannot stop the container" >&2
		exit 125
	fi
	echo false > "$state/running"
	;;
run|attach)
	IFS= read -r line < "$state/child.fifo"
	if [ -e "$state/signal" ]; then
		kill -TERM $$
	fi
	exit "$(cat "$state/exitcode")"
	;;
*)
	echo "fake podman: unexpected command $1" >&2
	exit 1
	;;
esac
`

// fakePodman is the script plus its state directory.
type fakePodman struct {
	path  string
	state string
}

func newFakePodman(t *testing.T) *fakePodman {
	t.Helper()
	state := t.TempDir()
	f := &fakePodman{path: filepath.Join(state, "podman"), state: state}
	require.NoError(t, syscall.Mkfifo(filepath.Join(state, "child.fifo"), 0o600),
		"create the fifo the run/attach children block on")
	script := strings.ReplaceAll(fakePodmanScript, "{{state}}", state)
	require.NoError(t, os.WriteFile(f.path, []byte(script), 0o755), "write the fake podman")
	f.setRunning(t, false)
	f.setHealth(t, "starting")
	f.setExitCode(t, 0)
	return f
}

func (f *fakePodman) file(name string) string { return filepath.Join(f.state, name) }

func (f *fakePodman) setRunning(t *testing.T, running bool) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("running"), []byte(strconv.FormatBool(running)+"\n"), 0o600))
}

// setHealth sets the health statuses the inspect returns, one per poll; the
// last one repeats. The literal "missing" answers with a failed inspect, as
// for a container that does not exist yet.
func (f *fakePodman) setHealth(t *testing.T, statuses ...string) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("health-plan"), []byte(strings.Join(statuses, "\n")+"\n"), 0o600))
}

// setExitCode is the code the next released run/attach child exits with.
func (f *fakePodman) setExitCode(t *testing.T, code int) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("exitcode"), []byte(strconv.Itoa(code)+"\n"), 0o600))
}

// setInspectExitCode makes `podman inspect -f {{.State.ExitCode}}` answer
// code; without it the inspect fails as it does for a --rm container that is
// already gone.
func (f *fakePodman) setInspectExitCode(t *testing.T, code int) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("exitcode-inspect"), []byte(strconv.Itoa(code)+"\n"), 0o600))
}

// setRunningFails makes the running inspect fail with msg, as it does when
// the socket broke or the client cannot reach podman.
func (f *fakePodman) setRunningFails(t *testing.T, msg string) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("running-fails"), []byte(msg+"\n"), 0o600))
}

func (f *fakePodman) setStopFails(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("stop-fails"), nil, 0o600))
}

func (f *fakePodman) setSignaled(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("signal"), nil, 0o600))
}

func (f *fakePodman) setVersionFails(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.file("version-fails"), nil, 0o600))
}

func (f *fakePodman) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.file("calls.log"))
	if err != nil && !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	return splitCalls(string(data))
}

func splitCalls(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (f *fakePodman) countCalls(prefix string) int {
	data, _ := os.ReadFile(f.file("calls.log")) //nolint:errcheck // a missing log means no calls
	n := 0
	for _, call := range splitCalls(string(data)) {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

// waitCall waits until the fake logged a call starting with prefix.
func (f *fakePodman) waitCall(t *testing.T, prefix string) {
	t.Helper()
	f.waitCalls(t, prefix, 1)
}

// waitCalls waits until the fake logged n calls starting with prefix.
func (f *fakePodman) waitCalls(t *testing.T, prefix string, n int) {
	t.Helper()
	require.Eventually(t, func() bool { return f.countCalls(prefix) >= n }, 5*time.Second, time.Millisecond,
		"want %d podman call(s) %q in %v", n, prefix, f.calls(t))
}

// releaseChild lets the blocked run/attach child exit with code. It waits for
// a child to read the fifo, so a test can never block forever on a child that
// is not there.
func (f *fakePodman) releaseChild(t *testing.T, code int) {
	t.Helper()
	f.setExitCode(t, code)
	fifo := f.file("child.fifo")
	var writer *os.File
	require.Eventually(t, func() bool {
		var err error
		writer, err = os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		return err == nil
	}, 5*time.Second, time.Millisecond, "no run/attach child waits on %s", fifo)
	_, err := writer.WriteString("exit\n")
	require.NoError(t, err, "release the child")
	require.NoError(t, writer.Close())
}

// fakeClock is the supervisor's time source in the tests: its timers fire
// only when the test fires them, so a test paces the health poll and the
// re-attach without sleeping.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1) // buffered: a late firing must never block
	c.waiters = append(c.waiters, ch)
	return ch
}

// fire releases every pending timer, and none when the supervisor waits for
// nothing (the health turned healthy, the container ended).
func (c *fakeClock) fire() {
	c.mu.Lock()
	waiters := c.waiters
	c.waiters = nil
	now := c.now
	c.mu.Unlock()
	for _, ch := range waiters {
		ch <- now
	}
}

// until fires the supervisor's timers until cond holds. An iteration without a
// pending timer is not an error: the supervisor may be inside a podman call,
// and the next iteration catches the timer it registers.
func (c *fakeClock) until(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		if cond() {
			return true
		}
		c.fire()
		return cond()
	}, 10*time.Second, time.Millisecond, "the supervisor did not reach the expected state")
}

// notifier records the states the supervisor reports to systemd.
type notifier struct {
	mu     sync.Mutex
	states []string
}

func (n *notifier) notify(state string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.states = append(n.states, state)
	return nil
}

func (n *notifier) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.states...)
}

func (n *notifier) ready() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.states) > 0
}

type runResult struct {
	code int
	err  error
}

func startRun(t *testing.T, ctx context.Context, o supervise.Options) <-chan runResult {
	t.Helper()
	done := make(chan runResult, 1)
	go func() {
		code, err := supervise.Run(ctx, o)
		done <- runResult{code: code, err: err}
	}()
	return done
}

func waitRun(t *testing.T, done <-chan runResult) runResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(30 * time.Second):
		require.Fail(t, "the supervisor did not return")
		return runResult{}
	}
}

func (f *fakePodman) options(args []string, clock *fakeClock, n *notifier) supervise.Options {
	return supervise.Options{
		Podman:      f.path,
		Name:        "dzo-a",
		RunArgs:     args,
		StopTimeout: time.Minute,
		Poll:        2 * time.Second,
		Notify:      n.notify,
		Clock:       clock.Now,
		After:       clock.After,
	}
}

const runArgs = "run --name dzo-a --replace --rm img sleep 1"

func TestSuperviseStartsAndNotifiesWhenHealthy(t *testing.T) {
	f := newFakePodman(t)
	f.setHealth(t, "missing", "missing", "starting", "starting", "healthy") // the container does not exist for the first polls
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, runArgs)
	clock.until(t, n.ready)
	assert.Equal(t, []string{"READY=1"}, n.all(), "exactly one READY=1")
	assert.Equal(t, 1, f.countCalls("run "), "the container is started once")

	cancel()
	f.waitCall(t, "stop -t 60 dzo-a")
	f.releaseChild(t, 0)
	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 0, r.code, "the child's exit code")
}

func TestSuperviseAttachesToRunning(t *testing.T) {
	f := newFakePodman(t)
	f.setRunning(t, true)
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	o := f.options(strings.Fields(runArgs), clock, n)
	o.StopTimeout = 0 // the default: podman's 10 s
	done := startRun(t, ctx, o)

	f.waitCall(t, "attach --no-stdin dzo-a")
	assert.Equal(t, 0, f.countCalls("run "), "an already running container is not started a second time")

	cancel()
	f.waitCall(t, "stop -t 10 dzo-a")
	f.releaseChild(t, 0)
	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 0, r.code)
}

func TestSuperviseExitedContainerIsReplaced(t *testing.T) {
	f := newFakePodman(t) // Running=false: a stopped leftover
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, runArgs)
	assert.Equal(t, 0, f.countCalls("attach "), "a stopped container is replaced, not attached to")

	cancel()
	f.waitCall(t, "stop -t 60 dzo-a")
	f.releaseChild(t, 0)
	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 0, r.code)
}

func TestSuperviseStopsOnCancel(t *testing.T) {
	f := newFakePodman(t)
	f.setRunning(t, true)
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	o := f.options(strings.Fields(runArgs), clock, n)
	o.StopTimeout = 90 * time.Second
	done := startRun(t, ctx, o)

	f.waitCall(t, "attach --no-stdin dzo-a")
	cancel()
	f.waitCall(t, "stop -t 90 dzo-a")
	f.releaseChild(t, 7)

	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 7, r.code, "the child's exit code")
}

func TestSuperviseExitBeforeHealthy(t *testing.T) {
	f := newFakePodman(t)
	f.setHealth(t, "starting")
	clock := newFakeClock()
	n := &notifier{}
	done := startRun(t, t.Context(), f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, runArgs)
	f.releaseChild(t, 3)

	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 3, r.code)
	assert.Empty(t, n.all(), "no READY=1 for a container that exited")
}

func TestSuperviseReattachOnBrokenConnection(t *testing.T) {
	f := newFakePodman(t)
	f.setRunning(t, true)
	f.setHealth(t, "healthy")
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, "attach --no-stdin dzo-a")
	f.releaseChild(t, 7) // the client broke, the container keeps running

	clock.until(t, func() bool { return f.countCalls("attach --no-stdin dzo-a") >= 2 })
	f.setRunning(t, false) // the server stopped in the meantime
	f.releaseChild(t, 9)

	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 9, r.code, "the last child's exit code")
	assert.Equal(t, 2, f.countCalls("attach --no-stdin dzo-a"), "one attach per broken connection")
	assert.Equal(t, 0, f.countCalls("run "), "the running container is never replaced")
}

func TestSupervisePrefersContainerExitCode(t *testing.T) {
	// A broken attach hides the container's real exit code: the child's own
	// code is the client's, not the server's. When the container can still be
	// inspected (not --rm, or not removed yet), its State.ExitCode wins.
	f := newFakePodman(t)
	f.setRunning(t, true)
	f.setHealth(t, "healthy")
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, "attach --no-stdin dzo-a")
	f.releaseChild(t, 7)
	clock.until(t, func() bool { return f.countCalls("attach --no-stdin dzo-a") >= 2 })
	f.setInspectExitCode(t, 42)
	f.setRunning(t, false)
	f.releaseChild(t, 9)

	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 42, r.code, "the container's exit code wins over the client's")
}

func TestSuperviseSocketDown(t *testing.T) {
	f := newFakePodman(t)
	f.setVersionFails(t)
	clock := newFakeClock()
	n := &notifier{}
	done := startRun(t, t.Context(), f.options(strings.Fields(runArgs), clock, n))

	r := waitRun(t, done)
	require.Error(t, r.err)
	assert.Contains(t, r.err.Error(), "podman socket unreachable")
	assert.Equal(t, 1, r.code)
	assert.Equal(t, 0, f.countCalls("inspect "), "nothing is inspected when the socket does not answer")
}

func TestSuperviseMissingContainerRuns(t *testing.T) {
	// The inspect of a container that does not exist yet fails with "no such
	// object": that is not a socket failure, the container simply has to start.
	f := newFakePodman(t)
	f.setRunningFails(t, `Error: no such object: "dzo-a"`)
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, runArgs)
	f.releaseChild(t, 0)
	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 0, r.code)
}

func TestSuperviseInspectFails(t *testing.T) {
	// A socket that does not answer the first inspect fails the unit; it must
	// not fall through to `podman run --replace` and kill a server it cannot see.
	f := newFakePodman(t)
	f.setRunningFails(t, "Error: unable to connect to Podman socket")
	clock := newFakeClock()
	n := &notifier{}
	done := startRun(t, t.Context(), f.options(strings.Fields(runArgs), clock, n))

	r := waitRun(t, done)
	require.Error(t, r.err)
	assert.Contains(t, r.err.Error(), "inspecting container dzo-a")
	assert.Equal(t, 0, f.countCalls("run "), "no container is started on an unknown state")
}

func TestSuperviseInspectFailsAfterExit(t *testing.T) {
	// The client exited but the container's state cannot be read afterwards:
	// the child's code is all the supervisor has, and it says so in the journal.
	f := newFakePodman(t)
	f.setRunning(t, true)
	f.setHealth(t, "healthy")
	var stderr bytes.Buffer
	clock := newFakeClock()
	n := &notifier{}
	o := f.options(strings.Fields(runArgs), clock, n)
	o.Stderr = &stderr
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, o)

	f.waitCall(t, "attach --no-stdin dzo-a")
	f.setRunningFails(t, "Error: unable to connect to Podman socket")
	f.releaseChild(t, 7)

	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 7, r.code, "the child's exit code")
	assert.Contains(t, stderr.String(), "the container's state is unknown after podman attach exited")
}

func TestSuperviseSignaledClient(t *testing.T) {
	// A client killed by a signal is reported as 128+signal, the way a shell
	// reports it, so the unit's status keeps its usual meaning.
	f := newFakePodman(t)
	f.setSignaled(t)
	clock := newFakeClock()
	n := &notifier{}
	done := startRun(t, t.Context(), f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, runArgs)
	f.releaseChild(t, 0)
	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 143, r.code, "SIGTERM is 128+15")
}

func TestSuperviseCancelWhileReattaching(t *testing.T) {
	f := newFakePodman(t)
	f.setRunning(t, true)
	f.setHealth(t, "healthy")
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, "attach --no-stdin dzo-a")
	f.releaseChild(t, 7) // the client broke; the supervisor waits for its next attach
	cancel()

	f.waitCall(t, "stop -t 60 dzo-a")
	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 7, r.code, "the last child's exit code")
	assert.Equal(t, 1, f.countCalls("attach --no-stdin dzo-a"), "no attach is started after the cancel")
}

func TestSuperviseGivesUpAfterTenBrokenConnections(t *testing.T) {
	f := newFakePodman(t)
	f.setRunning(t, true)
	f.setHealth(t, "healthy")
	clock := newFakeClock()
	n := &notifier{}
	done := startRun(t, t.Context(), f.options(strings.Fields(runArgs), clock, n))

	for i := 1; i <= 11; i++ {
		clock.until(t, func() bool { return f.countCalls("attach --no-stdin dzo-a") >= i })
		f.releaseChild(t, 7)
	}
	r := waitRun(t, done)
	require.Error(t, r.err)
	assert.Contains(t, r.err.Error(), "broke 10 times in a row")
}

func TestSuperviseStopFailureIsNotFatal(t *testing.T) {
	// A stop that cannot reach the host must not hide the server's exit code:
	// systemd kills the cgroup afterwards anyway.
	f := newFakePodman(t)
	f.setRunning(t, true)
	f.setHealth(t, "starting")
	f.setStopFails(t)
	clock := newFakeClock()
	n := &notifier{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, f.options(strings.Fields(runArgs), clock, n))

	f.waitCall(t, "attach --no-stdin dzo-a")
	cancel()
	f.waitCall(t, "stop -t 60 dzo-a")
	f.releaseChild(t, 5)

	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 5, r.code)
}

func TestSuperviseDefaults(t *testing.T) {
	// The zero Options carry the production defaults: podman from PATH is
	// overridden here, the rest (io.Discard, time.Now, time.After, SdNotify)
	// must work on its own.
	t.Setenv("NOTIFY_SOCKET", "")
	f := newFakePodman(t)
	f.setHealth(t, "healthy")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startRun(t, ctx, supervise.Options{
		Podman:  f.path,
		Name:    "dzo-a",
		RunArgs: strings.Fields(runArgs),
	})

	f.waitCall(t, runArgs)
	f.releaseChild(t, 0)
	r := waitRun(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, 0, r.code)
}

func TestParseUnitArgs(t *testing.T) {
	words := []string{"run", "--name", "dzo-a", "--replace", "--rm", "--stop-timeout", "90", "img", "sleep", "1"}
	o, err := supervise.ParseUnitArgs("a", words)
	require.NoError(t, err)
	assert.Equal(t, words, o.RunArgs, "the words after -- are used verbatim")
	assert.Equal(t, "dzo-a", o.Name)
	assert.Equal(t, 90*time.Second, o.StopTimeout)

	o, err = supervise.ParseUnitArgs("a", []string{"run", "--name=dzo-a", "img"})
	require.NoError(t, err)
	assert.Equal(t, "dzo-a", o.Name)
	assert.Equal(t, 10*time.Second, o.StopTimeout, "the podman default when --stop-timeout is absent")
}

func TestParseUnitArgsFirstOccurrenceWins(t *testing.T) {
	// Only the words before the image are podman's; the game server's own
	// arguments follow it. A server argument spelled --name must not rename the
	// container the unit starts, and a later --stop-timeout must not shorten
	// the grace the unit gives the server.
	words := []string{
		"run", "--name", "dzo-a", "--replace", "--rm", "--stop-timeout", "90",
		"img", "./server", "--name", "other", "--stop-timeout", "1",
	}
	o, err := supervise.ParseUnitArgs("a", words)
	require.NoError(t, err)
	assert.Equal(t, "dzo-a", o.Name, "the first --name is the container's")
	assert.Equal(t, 90*time.Second, o.StopTimeout, "the first --stop-timeout is the unit's")
}

func TestParseUnitArgsErrors(t *testing.T) {
	for _, c := range []struct {
		name     string
		instance string
		words    []string
		want     string
	}{
		{"no arguments", "a", nil, "no podman arguments"},
		{"no name", "a", []string{"run", "img"}, "no --name"},
		{"dangling name", "a", []string{"run", "--name"}, "no value"},
		{"mismatched name", "a", []string{"run", "--name", "dzo-b", "img"}, `"dzo-b"`},
		{"mismatched name with =", "a", []string{"run", "--name=dzo-b", "img"}, `"dzo-b"`},
		{"stop timeout is not a number", "a", []string{"run", "--name", "dzo-a", "--stop-timeout", "soon", "img"}, "not a number of seconds"},
		{"negative stop timeout", "a", []string{"run", "--name", "dzo-a", "--stop-timeout", "-1", "img"}, "not a number of seconds"},
		{"dangling stop timeout", "a", []string{"run", "--name", "dzo-a", "--stop-timeout"}, "no value"},
		{"invalid instance name", "../a", []string{"run", "--name", "dzo-a", "img"}, "invalid instance name"},
		{"dotdot", "..", []string{"run", "--name", "dzo-..", "img"}, "invalid instance name"},
		{"empty", "", []string{"run", "--name", "dzo-", "img"}, "invalid instance name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := supervise.ParseUnitArgs(c.instance, c.words)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestSdNotify(t *testing.T) {
	t.Run("filesystem socket", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "notify.sock")
		conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		t.Setenv("NOTIFY_SOCKET", path)

		require.NoError(t, supervise.SdNotify("READY=1"), "SdNotify")
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		buf := make([]byte, 64)
		n, _, err := conn.ReadFromUnix(buf)
		require.NoError(t, err, "read the notification")
		assert.Equal(t, "READY=1", string(buf[:n]))
	})

	t.Run("abstract socket", func(t *testing.T) {
		name := "@dzo-notify-" + filepath.Base(t.TempDir())
		conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		t.Setenv("NOTIFY_SOCKET", name)

		require.NoError(t, supervise.SdNotify("STOPPING=1"), "SdNotify")
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		buf := make([]byte, 64)
		n, _, err := conn.ReadFromUnix(buf)
		require.NoError(t, err, "read the notification")
		assert.Equal(t, "STOPPING=1", string(buf[:n]))
	})

	t.Run("unset", func(t *testing.T) {
		t.Setenv("NOTIFY_SOCKET", "")
		assert.NoError(t, supervise.SdNotify("READY=1"), "no socket is no error")
	})

	t.Run("unreachable", func(t *testing.T) {
		t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
		require.Error(t, supervise.SdNotify("READY=1"))
	})
}

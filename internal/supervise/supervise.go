// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package supervise runs an instance's game container through podman. It is
// the ExecStart of a supervised instance unit (podman.mode: remote): the unit
// starts the supervisor, the supervisor starts or re-attaches to the
// container, reports the container's readiness to systemd and stops the
// container when systemd stops the unit.
//
// The unit carries the podman run arguments after a `--` (quadlet.RunArgs),
// and the supervisor uses them verbatim. It never resolves the instance or
// renders the arguments again: a unit that is held because an update is
// pending keeps running the generations it was rendered for, also when
// systemd restarts it after a crash.
package supervise

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// defaultPoll is how often the supervisor asks for the container's health
	// and, after the podman connection broke, how often it attaches again.
	defaultPoll = 2 * time.Second
	// defaultStopTimeout is podman's own default: the grace the server gets on
	// a stop, when the unit does not carry a --stop-timeout.
	defaultStopTimeout = 10 * time.Second
	// versionTimeout bounds the startup probe: a socket that does not answer
	// fails the unit instead of hanging until systemd's start timeout.
	versionTimeout = 10 * time.Second
	// stopGrace is the time beyond the server's stop grace that the stop
	// command gets. It is the same 30 s RenderSupervised adds on top of
	// StopTimeout for the unit's TimeoutStopSec.
	stopGrace = 30 * time.Second
	// childGrace is how long the podman client gets to exit after the
	// container was stopped, before the supervisor kills it.
	childGrace = 10 * time.Second
	// maxReattach is how often the supervisor attaches again in a row, without
	// the container having ended, before it gives up and fails the unit. A
	// restart limit would otherwise count the broken socket against the unit.
	maxReattach = 10
)

// Options configures one supervised container. The zero values of the
// optional fields are the production defaults; tests inject Podman, Clock and
// After.
type Options struct {
	// Podman is the podman client binary; empty means "podman" from PATH.
	Podman string
	// Name is the container name, "dzo-"+instance (quadlet.ContainerSpec.Name).
	Name string
	// RunArgs are the words after `podman` that run the container, starting
	// with "run", exactly as quadlet.RunArgs rendered them into the unit.
	// Run uses them verbatim; they also carry the --name and --stop-timeout
	// the supervisor reads (see ParseUnitArgs).
	RunArgs []string
	// StopTimeout is the grace the server gets on a stop. Empty means podman's
	// default, 10 s. It becomes `podman stop -t <seconds>` and, with stopGrace,
	// the budget the stop command runs with.
	StopTimeout time.Duration
	// Poll paces the health poll and the re-attach after a broken connection;
	// zero means 2 s.
	Poll time.Duration
	// Notify reports a state to systemd. Nil means SdNotify, the real one.
	Notify func(state string) error
	// Stdout and Stderr receive the container's output, which is the unit's
	// journal. Nil discards it.
	Stdout, Stderr io.Writer
	// Clock and After are the supervisor's time source. Nil means time.Now and
	// time.After; tests inject them to pace the health poll and the re-attach
	// without sleeping.
	Clock func() time.Time
	After func(time.Duration) <-chan time.Time
}

// supervisor is Run's state for one process.
type supervisor struct {
	Options
	lastStart time.Time // when the current podman run/attach client started
	attached  bool      // the current client is `podman attach`, not `podman run`
}

// Run supervises o's container until it ends, the unit is stopped, or the
// podman connection broke too often. It returns the exit code the unit should
// end with; err is set for the supervisor's own failures (the socket does not
// answer, podman cannot be started, the connection broke maxReattach times).
func Run(ctx context.Context, o Options) (int, error) {
	s := &supervisor{Options: o}
	s.setDefaults()
	if s.Name == "" {
		return 1, errors.New("supervise: the container name is required")
	}
	if len(s.RunArgs) == 0 {
		return 1, errors.New("supervise: the podman run arguments are required")
	}
	if err := s.checkPodman(ctx); err != nil {
		return 1, err
	}

	// Readiness is reported by one goroutine; Run waits for it before it
	// returns, so no goroutine outlives the unit.
	healthCtx, stopHealth := context.WithCancel(ctx)
	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		s.pollHealth(healthCtx)
	}()
	defer func() {
		stopHealth()
		<-healthDone
	}()

	for reattached := 0; ; {
		if ctx.Err() != nil {
			return 0, nil // stopped before anything ran
		}
		running, err := s.inspectRunning(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return 0, nil
			}
			return 1, fmt.Errorf("supervise: inspecting container %s: %w", s.Name, err)
		}
		code, stopped, err := s.runChild(ctx, running)
		if err != nil {
			return code, err
		}
		if stopped {
			return code, nil
		}
		if ctx.Err() != nil {
			// The child ended on its own while the unit was already going
			// down: the container still gets its stop.
			s.stopWithBudget(ctx)
			return code, nil
		}

		running, err = s.inspectRunning(ctx)
		if err != nil || !running {
			if err != nil {
				_, _ = fmt.Fprintf(s.Stderr, "dzo supervise %s: the container's state is unknown after podman %s exited: %v\n",
					s.Name, clientVerb(s.attached), err)
			}
			if s.attached {
				// The client was an attach: its exit code is the client's, the
				// container's real code is in its state. With --rm the container
				// may be gone already; then the client's code is all we have.
				if real, ok := s.containerExitCode(ctx); ok {
					code = real
				}
			}
			return code, nil
		}

		// The container outlived its client: the connection broke, the server
		// did not end. Attaching again keeps the unit active instead of
		// counting a broken socket as a crash against the start limit.
		//
		// A spike on a remote podman showed that a restart of the host's
		// podman.service does not even get here: the client stays, receives no
		// further output (no EOF, no error) and later exits with the
		// container's code. This path is for a real break (the socket dies or
		// the client is killed).
		reattached++
		if reattached > maxReattach {
			return code, fmt.Errorf("supervise: container %s still runs, but the podman connection broke %d times in a row", s.Name, maxReattach)
		}
		_, _ = fmt.Fprintf(s.Stderr, "dzo supervise %s: the podman connection broke while the container runs, attaching again (%d/%d)\n",
			s.Name, reattached, maxReattach)
		// At most one attach per Poll: a connection that breaks immediately
		// must not spin.
		if wait := s.Poll - s.Clock().Sub(s.lastStart); wait > 0 {
			select {
			case <-ctx.Done():
				s.stopWithBudget(ctx)
				return code, nil
			case <-s.After(wait):
			}
		}
	}
}

// setDefaults fills the zero values of Run's options.
func (s *supervisor) setDefaults() {
	if s.Podman == "" {
		s.Podman = "podman"
	}
	if s.StopTimeout <= 0 {
		s.StopTimeout = defaultStopTimeout
	}
	if s.Poll <= 0 {
		s.Poll = defaultPoll
	}
	if s.Notify == nil {
		s.Notify = SdNotify
	}
	if s.Stdout == nil {
		s.Stdout = io.Discard
	}
	if s.Stderr == nil {
		s.Stderr = io.Discard
	}
	if s.Clock == nil {
		s.Clock = time.Now
	}
	if s.After == nil {
		s.After = time.After
	}
}

// checkPodman asks the socket for the server version before anything runs: a
// socket that does not answer must fail the unit fast, with a message in the
// journal that names the cause, not hang until the start timeout.
func (s *supervisor) checkPodman(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := s.podman(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("supervise: podman socket unreachable: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return errors.New("supervise: podman socket unreachable: podman version printed no server version")
	}
	return nil
}

// pollHealth waits for the container's health check to report healthy and
// reports READY=1 once. The first check is immediate, then one per Poll. A
// container that does not exist yet, and a health check that is still starting,
// are polled again: a supervised unit must not fail just because its container
// is still booting. The poll keeps going across a re-attach - the container may
// only become healthy while the supervisor is attaching again - and Run stops
// it when it returns.
func (s *supervisor) pollHealth(ctx context.Context) {
	for {
		out, err := s.podman(ctx, "inspect", "-f", "{{.State.Health.Status}}", s.Name)
		if err == nil && strings.TrimSpace(out) == "healthy" {
			if err := s.Notify("READY=1"); err != nil {
				_, _ = fmt.Fprintf(s.Stderr, "dzo supervise %s: reporting readiness to systemd: %v\n", s.Name, err)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.After(s.Poll):
		}
	}
}

// runChild starts the podman client (attach when the container already runs,
// run otherwise) and waits for it. When ctx is cancelled, the container is
// stopped first and the client is waited for; stopped reports that the unit is
// going down, so Run does not treat the client's end as a broken connection.
func (s *supervisor) runChild(ctx context.Context, attach bool) (code int, stopped bool, err error) {
	args := s.RunArgs
	if attach {
		args = []string{"attach", "--no-stdin", s.Name}
	}
	cmd := exec.Command(s.Podman, args...) //nolint:gosec // the unit's own generated arguments, not external input
	cmd.Stdout, cmd.Stderr = s.Stdout, s.Stderr
	s.lastStart = s.Clock()
	s.attached = attach
	if err := cmd.Start(); err != nil {
		return 1, false, fmt.Errorf("supervise: starting podman %s: %w", strings.Join(args, " "), err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		code, err := exitCode(err)
		return code, false, err
	case <-ctx.Done():
		s.stopWithBudget(ctx)
		select {
		case err := <-done:
			code, err := exitCode(err)
			return code, true, err
		case <-s.After(childGrace):
			// podman stop has returned but the client does not: never leave a
			// process or a goroutine behind. The container was stopped above;
			// the orphaned client is only a view of it.
			_ = cmd.Process.Kill()
			<-done
			return 1, true, fmt.Errorf("supervise: podman %s did not exit after the container was stopped", strings.Join(args, " "))
		}
	}
}

// stopWithBudget stops the container even when ctx is already cancelled: the
// unit's SIGTERM must reach the host. The budget is the one the unit allows
// for the whole stop (podman's grace plus the 30 s RenderSupervised adds to
// TimeoutStopSec).
func (s *supervisor) stopWithBudget(ctx context.Context) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.StopTimeout+stopGrace)
	defer cancel()
	s.stop(stopCtx)
}

// stop asks podman to stop the container. A failure is reported but does not
// fail the unit: systemd's own kill of the cgroup follows, and a stop that
// cannot reach the host has no way to be retried usefully.
func (s *supervisor) stop(ctx context.Context) {
	secs := int64((s.StopTimeout + time.Second - 1) / time.Second) // whole seconds, like quadlet's StopTimeout
	out, err := s.podman(ctx, "stop", "-t", strconv.FormatInt(secs, 10), s.Name)
	if err != nil && !noSuchObject(out) {
		_, _ = fmt.Fprintf(s.Stderr, "dzo supervise %s: stopping the container: %v\n", s.Name, err)
	}
}

// inspectRunning reports whether the container runs. A container that does not
// exist (it was never started, or --rm removed it) is not an error: it simply
// does not run.
func (s *supervisor) inspectRunning(ctx context.Context) (bool, error) {
	out, err := s.podman(ctx, "inspect", "-f", "{{.State.Running}}", s.Name)
	if err != nil {
		if noSuchObject(out) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// containerExitCode reads the exit code of a container that is still in
// podman's state (not removed by --rm). ok is false when it cannot be read.
func (s *supervisor) containerExitCode(ctx context.Context) (code int, ok bool) {
	out, err := s.podman(ctx, "inspect", "-f", "{{.State.ExitCode}}", s.Name)
	if err != nil {
		return 0, false
	}
	code, err = strconv.Atoi(strings.TrimSpace(out))
	if err != nil || code < 0 {
		return 0, false
	}
	return code, true
}

// podman runs one podman client command and returns its combined output. The
// error carries the output, so a failed inspect in the journal says why.
func (s *supervisor) podman(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, s.Podman, args...) //nolint:gosec // the unit's own generated arguments, not external input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("podman %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// noSuchObject reports whether podman's output says the container does not
// exist (podman 5: `Error: no such object: "dzo-a"`).
func noSuchObject(out string) bool {
	out = strings.ToLower(out)
	return strings.Contains(out, "no such object") || strings.Contains(out, "no such container")
}

// exitCode turns the result of cmd.Wait into the exit code the unit ends with.
// A client killed by a signal is reported the way a shell reports it (128 +
// signal), so the journal and SuccessExitStatus keep their usual meaning.
func exitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 1, err
	}
	if code := exit.ExitCode(); code >= 0 {
		return code, nil
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	return 1, nil
}

// clientVerb names the podman command the current client runs, for messages.
func clientVerb(attached bool) string {
	if attached {
		return "attach"
	}
	return "run"
}

// ParseUnitArgs turns the words after `dzo instance supervise <name> --` into
// Options: the podman run arguments verbatim, the container name from their
// --name, and the stop timeout from their --stop-timeout (podman's default,
// 10 s, when it is absent).
//
// It checks that the arguments belong to this instance: --name must be
// "dzo-"+name. A unit that carries another instance's arguments is a bug in
// the unit, not a container to start.
func ParseUnitArgs(name string, words []string) (Options, error) {
	if !validInstanceName(name) {
		return Options{}, fmt.Errorf("invalid instance name %q", name)
	}
	if len(words) == 0 {
		return Options{}, errors.New("supervise: no podman arguments after --")
	}
	o := Options{RunArgs: words, StopTimeout: defaultStopTimeout}
	named := false
	for i := 0; i < len(words); i++ {
		switch w := words[i]; {
		case w == "--name" || strings.HasPrefix(w, "--name="):
			value, next, err := flagValue("--name", words, i)
			if err != nil {
				return Options{}, err
			}
			i = next
			o.Name = value
			named = true
		case w == "--stop-timeout" || strings.HasPrefix(w, "--stop-timeout="):
			value, next, err := flagValue("--stop-timeout", words, i)
			if err != nil {
				return Options{}, err
			}
			i = next
			secs, err := strconv.Atoi(value)
			if err != nil || secs < 0 {
				return Options{}, fmt.Errorf("supervise: --stop-timeout %q is not a number of seconds", value)
			}
			o.StopTimeout = time.Duration(secs) * time.Second
		}
	}
	if !named {
		return Options{}, errors.New("supervise: the podman run arguments have no --name")
	}
	if want := "dzo-" + name; o.Name != want {
		return Options{}, fmt.Errorf("supervise: the podman run arguments name the container %q, but the unit is for %q", o.Name, want)
	}
	return o, nil
}

// flagValue reads the value of flag at words[i], as `--flag value` or
// `--flag=value`, and returns the index of the last word it consumed.
func flagValue(flag string, words []string, i int) (value string, next int, err error) {
	if after, ok := strings.CutPrefix(words[i], flag+"="); ok {
		return after, i, nil
	}
	if i+1 >= len(words) {
		return "", i, fmt.Errorf("supervise: %s has no value", flag)
	}
	return words[i+1], i + 1, nil
}

// validInstanceName is the check of `dzo instance remove`: the name is a
// single path element, so nothing built from it can leave the instance
// directories.
func validInstanceName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

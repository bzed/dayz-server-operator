// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"fmt"
	"time"
)

// ShutdownOptions configures Shutdown.
type ShutdownOptions struct {
	Timeout time.Duration                           // how long to wait for the process to exit; default 30s
	Poll    time.Duration                           // how often to look; default 1s
	Running func(ctx context.Context) (bool, error) // reports whether the server process is still there
	Sleep   func(time.Duration)                     // for tests
	Log     func(format string, args ...any)        // progress, may be nil
}

// Shutdown asks the server to shut down (RCon #shutdown) and waits until its process is gone. It
// returns whether the process exited in time. It never kills anything: when it returns false
// the caller (the unit's own stop) ends the process. A server that cannot be asked is not an
// error either, the caller falls back the same way.
func Shutdown(ctx context.Context, c Commander, o ShutdownOptions) (bool, error) {
	logf := func(format string, a ...any) {
		if o.Log != nil {
			o.Log(format, a...)
		}
	}
	if o.Running == nil {
		return false, fmt.Errorf("instance: Shutdown needs Running")
	}
	if up, err := o.Running(ctx); err == nil && !up {
		return true, nil
	}
	if _, err := c.Command(ctx, "#shutdown"); err != nil {
		logf("RCon #shutdown failed (%v), leaving it to the hard stop", err)
		return false, nil
	}
	timeout, poll := o.Timeout, o.Poll
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if poll <= 0 {
		poll = time.Second
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	for waited := time.Duration(0); waited < timeout; waited += poll {
		sleep(poll)
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if up, err := o.Running(ctx); err == nil && !up {
			logf("server exited after %s", waited+poll)
			return true, nil
		}
	}
	logf("server still running after %s, leaving it to the hard stop", timeout)
	return false, nil
}

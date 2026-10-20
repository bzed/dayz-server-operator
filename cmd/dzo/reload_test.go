// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReloadOnSIGHUPRelaysTheSignalAndStops(t *testing.T) {
	reload, stop := reloadOnSIGHUP(t.Context())
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))
	select {
	case <-reload:
	case <-time.After(10 * time.Second):
		require.Fail(t, "SIGHUP did not reach the reload channel")
	}
	stop() // waits for the relay goroutine; a second call is harmless
	stop()
}

func TestRelayHUPDropsASignalWhileOneIsPending(t *testing.T) {
	hup := make(chan os.Signal, 2)
	reload := make(chan struct{}, 1)
	hup <- syscall.SIGHUP
	hup <- syscall.SIGHUP
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { relayHUP(ctx, hup, reload); close(done) }()
	require.Eventually(t, func() bool { return len(hup) == 0 }, 10*time.Second, 5*time.Millisecond)
	cancel()
	<-done
	require.Len(t, reload, 1, "one pending reload, the second signal was dropped")
}

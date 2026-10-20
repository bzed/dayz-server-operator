// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// reloadOnSIGHUP returns a channel that receives a value for every SIGHUP (one is kept pending, more are
// dropped) and a func that stops the relay and waits for its goroutine. The relay also ends with ctx.
func reloadOnSIGHUP(ctx context.Context) (<-chan struct{}, func()) {
	reload := make(chan struct{}, 1)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	rctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		relayHUP(rctx, hup, reload)
	}()
	return reload, func() {
		signal.Stop(hup)
		cancel()
		wg.Wait()
	}
}

// relayHUP turns every signal on hup into a value on reload, unless one is already pending, until ctx ends.
func relayHUP(ctx context.Context, hup <-chan os.Signal, reload chan<- struct{}) {
	for {
		select {
		case <-hup:
			select {
			case reload <- struct{}{}:
			default:
			}
		case <-ctx.Done():
			return
		}
	}
}

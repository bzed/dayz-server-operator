// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package exporter

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/bzed/dayz-server-operator/internal/mission"
)

// A FIFO planted by the game container at a managed mission file must not stall the refresh
// (the drift read used to block on it while it held the collector lock).
func TestFIFOInLiveMissionDoesNotBlockRefresh(t *testing.T) {
	f := newFixture(t)
	pristine := filepath.Join(f.data, "instances", "x", "servermpmissions", "empty.m")
	write(t, filepath.Join(pristine, "init.c"), "init")
	live := filepath.Join(f.data, "instances", "x", "mpmissions", "empty.m")
	manifest := filepath.Join(f.data, "instances", "x", "runtime", "mission-manifest.json")
	in := mission.RenderInput{PristineDir: pristine, LiveDir: live, ManifestPath: manifest, FileHistoryDir: filepath.Join(f.data, "instances", "x", "filehistory")}
	_, _, err := mission.Render(in)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(live, "init.c")))
	require.NoError(t, unix.Mkfifo(filepath.Join(live, "init.c"), 0o600))

	done := make(chan struct{})
	go func() {
		f.c.Refresh(t.Context())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "Refresh blocks on a FIFO in the live mission")
	}
	assert.Equal(t, 1, f.inst().MissionDriftFiles, "the replaced file counts as drift")
}

// While one goroutine computes the slow data, others get the cached value and never wait for it
// or start a second computation.
func TestSlowDataIsSingleFlight(t *testing.T) {
	f := newFixture(t)
	tree, err := f.c.Load()
	require.NoError(t, err)
	f.c.mu.Lock()
	f.c.slowBusy = true
	f.c.mu.Unlock()
	done := make(chan slowData, 1)
	go func() { done <- f.c.slowData(tree) }()
	select {
	case d := <-done:
		assert.NotNil(t, d.drift)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "slowData waits for a computation that is running")
	}
	f.c.mu.Lock()
	assert.True(t, f.c.slowBusy, "the flag belongs to the running computation")
	f.c.mu.Unlock()
}

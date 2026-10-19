// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCmd struct {
	got []string
	err error
}

func (f *fakeCmd) Command(_ context.Context, c string) (string, error) {
	f.got = append(f.got, c)
	return "", f.err
}

func TestShutdownWaitsForExit(t *testing.T) {
	c := &fakeCmd{}
	polls := 0
	ok, err := Shutdown(t.Context(), c, ShutdownOptions{
		Timeout: 10 * time.Second, Poll: time.Second, Sleep: func(time.Duration) {},
		Running: func(context.Context) (bool, error) { polls++; return polls < 4, nil },
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, c.got, 1)
	assert.Equal(t, "#shutdown", c.got[0])
}

func TestShutdownTimeoutLeavesTheKillToTheCaller(t *testing.T) {
	ok, err := Shutdown(t.Context(), &fakeCmd{}, ShutdownOptions{
		Timeout: 3 * time.Second, Poll: time.Second, Sleep: func(time.Duration) {},
		Running: func(context.Context) (bool, error) { return true, nil },
	})
	require.NoError(t, err)
	require.False(t, ok)
}

func TestShutdownRConFailureIsNoError(t *testing.T) {
	ok, err := Shutdown(t.Context(), &fakeCmd{err: errors.New("refused")}, ShutdownOptions{
		Running: func(context.Context) (bool, error) { return true, nil },
	})
	require.NoError(t, err)
	require.False(t, ok)
}

func TestShutdownNotRunningDoesNothing(t *testing.T) {
	c := &fakeCmd{}
	ok, err := Shutdown(t.Context(), c, ShutdownOptions{
		Running: func(context.Context) (bool, error) { return false, nil },
	})
	require.NoError(t, err, "ok=%v err=%v commands=%v", ok, err, c.got)
	require.True(t, ok, "ok=%v err=%v commands=%v", ok, err, c.got)
	require.Empty(t, c.got, "ok=%v err=%v commands=%v", ok, err, c.got)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"errors"
	"testing"
	"time"
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
	ok, err := Shutdown(context.Background(), c, ShutdownOptions{
		Timeout: 10 * time.Second, Poll: time.Second, Sleep: func(time.Duration) {},
		Running: func(context.Context) (bool, error) { polls++; return polls < 4, nil },
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want the server to be reported gone", ok, err)
	}
	if len(c.got) != 1 || c.got[0] != "#shutdown" {
		t.Errorf("commands = %v", c.got)
	}
}

func TestShutdownTimeoutLeavesTheKillToTheCaller(t *testing.T) {
	ok, err := Shutdown(context.Background(), &fakeCmd{}, ShutdownOptions{
		Timeout: 3 * time.Second, Poll: time.Second, Sleep: func(time.Duration) {},
		Running: func(context.Context) (bool, error) { return true, nil },
	})
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want false and no error", ok, err)
	}
}

func TestShutdownRConFailureIsNoError(t *testing.T) {
	ok, err := Shutdown(context.Background(), &fakeCmd{err: errors.New("refused")}, ShutdownOptions{
		Running: func(context.Context) (bool, error) { return true, nil },
	})
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestShutdownNotRunningDoesNothing(t *testing.T) {
	c := &fakeCmd{}
	ok, err := Shutdown(context.Background(), c, ShutdownOptions{
		Running: func(context.Context) (bool, error) { return false, nil },
	})
	if err != nil || !ok || len(c.got) != 0 {
		t.Fatalf("ok=%v err=%v commands=%v", ok, err, c.got)
	}
}

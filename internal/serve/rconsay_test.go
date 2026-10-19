// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCommander struct {
	sent   []string
	closed bool
}

func (f *fakeCommander) Command(_ context.Context, c string) (string, error) {
	f.sent = append(f.sent, c)
	return "", nil
}
func (f *fakeCommander) Close() error { f.closed = true; return nil }

func TestRConSayBroadcastsWithSayMinusOne(t *testing.T) {
	f := &fakeCommander{}
	say := newRConSay(func(context.Context, string) (instance.Commander, error) { return f, nil })
	require.NoError(t, say(t.Context(), "alpha", "", "hi all"))
	assert.Equal(t, []string{"say -1 hi all"}, f.sent)
	assert.True(t, f.closed)
}

func TestRConSayToOnePlayerIsRefused(t *testing.T) {
	f := &fakeCommander{}
	say := newRConSay(func(context.Context, string) (instance.Commander, error) { return f, nil })
	err := say(t.Context(), "alpha", "7656", "hi")
	require.ErrorContains(t, err, "everyone only")
	assert.Empty(t, f.sent)
}

func TestRConSayDialErrorIsReturned(t *testing.T) {
	say := newRConSay(func(context.Context, string) (instance.Commander, error) { return nil, errors.New("no password") })
	require.ErrorContains(t, say(t.Context(), "ghost", "", "hi"), "no password")
}

func TestRConSayRefusesControlCharacters(t *testing.T) {
	f := &fakeCommander{}
	say := newRConSay(func(context.Context, string) (instance.Commander, error) { return f, nil })
	require.Error(t, say(t.Context(), "alpha", "", "hi\n#kick -1"))
	assert.Empty(t, f.sent)
}

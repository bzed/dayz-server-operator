// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/instance"
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
	if err := say(context.Background(), "alpha", "", "hi all"); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0] != "say -1 hi all" || !f.closed {
		t.Errorf("sent %v, closed %v", f.sent, f.closed)
	}
}

func TestRConSayToOnePlayerIsRefused(t *testing.T) {
	f := &fakeCommander{}
	say := newRConSay(func(context.Context, string) (instance.Commander, error) { return f, nil })
	err := say(context.Background(), "alpha", "7656", "hi")
	if err == nil || !strings.Contains(err.Error(), "everyone only") || len(f.sent) != 0 {
		t.Errorf("err = %v, sent %v", err, f.sent)
	}
}

func TestRConSayDialErrorIsReturned(t *testing.T) {
	say := newRConSay(func(context.Context, string) (instance.Commander, error) { return nil, errors.New("no password") })
	if err := say(context.Background(), "ghost", "", "hi"); err == nil || !strings.Contains(err.Error(), "no password") {
		t.Errorf("err = %v", err)
	}
}

func TestRConSayRefusesControlCharacters(t *testing.T) {
	f := &fakeCommander{}
	say := newRConSay(func(context.Context, string) (instance.Commander, error) { return f, nil })
	if err := say(context.Background(), "alpha", "", "hi\n#kick -1"); err == nil || len(f.sent) != 0 {
		t.Errorf("err = %v, sent %v", err, f.sent)
	}
}

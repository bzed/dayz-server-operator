// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// newRConSay returns the API's Say hook: text to every player with RCon's "say -1", over a
// connection made for the call. BattlEye addresses players by slot and the API has no map from
// Steam ids to slots, so a message to one player is refused.
func newRConSay(dial func(ctx context.Context, name string) (instance.Commander, error)) func(ctx context.Context, name, steamID, text string) error {
	return func(ctx context.Context, name, steamID, text string) error {
		if steamID != "" {
			return errors.New("rcon says to everyone only")
		}
		if strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return errors.New("the message has control characters")
		}
		c, err := dial(ctx, name)
		if err != nil {
			return err
		}
		if cl, ok := c.(io.Closer); ok {
			defer func() { _ = cl.Close() }()
		}
		return instance.Announce(ctx, c, text)
	}
}

// rconDialer connects to the RCon of an instance by name, like the restart does.
func rconDialer(cfg *config.Config, tree *site.Tree) func(ctx context.Context, name string) (instance.Commander, error) {
	return func(ctx context.Context, name string) (instance.Commander, error) {
		inst, err := resolve.Resolve(cfg, tree, name)
		if err != nil {
			return nil, err
		}
		pw, err := runfiles.RConPassword(cfg.Paths.Secrets, name)
		if err != nil {
			return nil, err
		}
		s := &battleye.Session{Addr: inst.RConAddr(), Password: pw, Options: []battleye.Option{battleye.WithLoginTimeout(5 * time.Second)}}
		first, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if err := s.WaitConnected(first); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("RCon is not reachable: %w", err)
		}
		return s, nil
	}
}

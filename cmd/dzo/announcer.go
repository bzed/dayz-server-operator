// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/config"
)

// announceClient is the API client the restart countdown sends its notifications with: dzo serve
// on this host, with the token of $DZO_TOKEN, or else of ${paths.secrets}/announce.token (made by
// `dzo instance apply` with the announcer role).
func announceClient(cfg *config.Config) (*apiclient.Client, error) {
	token := strings.TrimSpace(os.Getenv("DZO_TOKEN"))
	if token == "" {
		b, err := os.ReadFile(filepath.Join(cfg.Paths.Secrets, "announce.token"))
		if err == nil {
			token = strings.TrimSpace(string(b))
		}
	}
	if token == "" {
		return nil, errors.New("no API token: set DZO_TOKEN or put one in announce.token in the secrets directory")
	}
	return apiclient.New("http://"+cfg.Serve.Listen, token).WithActor(os.Getenv("USER"), "restart"), nil
}

// lazyNotifier makes the client at the first announcement, so a restart without a token only
// logs that and carries on over RCon.
func lazyNotifier(cfg *config.Config, name string) func(ctx context.Context, way, typ, text string, seconds int) error {
	var once sync.Once
	var notify func(ctx context.Context, way, typ, text string, seconds int) error
	return func(ctx context.Context, way, typ, text string, seconds int) error {
		once.Do(func() {
			c, err := announceClient(cfg)
			if err != nil {
				notify = func(context.Context, string, string, string, int) error { return err }
				return
			}
			notify = apiNotifier(c, name)
		})
		return notify(ctx, way, typ, text, seconds)
	}
}

// apiNotifier sends a countdown announcement through dzo-admin by the API. Expansion goes out as a
// plain notification when the mod does not report it. An error (the mod is not connected, the API
// is down, the message is refused) makes the countdown say the text over RCon instead.
func apiNotifier(c *apiclient.Client, name string) func(ctx context.Context, way, typ, text string, seconds int) error {
	return func(ctx context.Context, way, typ, text string, seconds int) error {
		st, err := c.Status(ctx, name)
		if err != nil {
			return err
		}
		if !st.Connected {
			return errors.New("dzo-admin is not connected")
		}
		m := api.MessageBody{Text: text, Seconds: seconds}
		switch way {
		case "chat":
			m.Style = "important"
		case "notification":
			m.Style = "notification"
		case "expansion":
			m.Style, m.NotifyType = "notification", ""
			if st.Hello.Has("expansion") {
				m.Style, m.NotifyType = "expansion", typ
			}
		default:
			return fmt.Errorf("no notification way %q", way)
		}
		r, err := c.Message(ctx, name, "", m)
		if err != nil {
			return err
		}
		if !r.OK {
			return fmt.Errorf("the game server refused: %s", r.Message)
		}
		return nil
	}
}

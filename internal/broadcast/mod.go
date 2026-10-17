// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package broadcast

import (
	"context"
	"fmt"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

// Hub is the part of admin.Hub the mod backend uses.
type Hub interface {
	Status() admin.Status
	Players() []admin.Player
	Submit(ctx context.Context, cmd admin.Command) (admin.Result, error)
}

// Styles maps a message colour to the mod's message style.
var Styles = map[string]string{"": "chat", "blue": "chat", "red": "important", "green": "friendly", "yellow": "action"}

// Mod sends the messages through the dzo-admin mod, which knows who is in the game: a player has
// joined when the mod lists the player, and the message goes out in the colour asked for.
type Mod struct {
	Hub Hub
	// Poll is how often the player list is looked at (a second by default).
	Poll time.Duration
}

// Joined reports a player the first time the mod lists them. The players there when the mod
// connects are not greeted: that is every player after a restart of dzo.
func (m *Mod) Joined(ctx context.Context) <-chan Player {
	out := make(chan Player)
	poll := m.Poll
	if poll <= 0 {
		poll = time.Second
	}
	go func() {
		seen := map[string]bool{}
		primed := false
		t := time.NewTicker(poll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if !m.Hub.Status().Connected {
				primed, seen = false, map[string]bool{}
				continue
			}
			now := map[string]bool{}
			var fresh []Player
			for _, p := range m.Hub.Players() {
				now[p.SteamID] = true
				if !seen[p.SteamID] {
					fresh = append(fresh, Player{Key: p.SteamID, Name: p.Name})
				}
			}
			seen = now
			if !primed {
				primed = true
				continue
			}
			for _, p := range fresh {
				select {
				case out <- p:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// Say sends text to the player, or to everyone, in the style of the colour.
func (m *Mod) Say(ctx context.Context, p *Player, text, color string) error {
	style, ok := Styles[color]
	if !ok {
		return fmt.Errorf("broadcast: unknown colour %q", color)
	}
	c := admin.Command{Kind: admin.KindMessage, Text: text, Style: style}
	if p != nil {
		c.SteamID = p.Key
	}
	res, err := m.Hub.Submit(ctx, c)
	if err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("broadcast: %s", res.Message)
	}
	return nil
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package broadcast sends the server messages of an instance (the welcome and the messages: of
// instance.yaml) to the players while dzo serve runs. DayZ's own messages.xml is not used: it
// sends nothing on a 1.30 server. The messages go out through the dzo-admin mod when it is
// connected (in the colour the message asks for, to a player once the player is really in the
// game), or else over RCon ("say").
package broadcast

import (
	"context"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/site"
)

// Player is a player of the instance. Key is what the Backend addresses the player by: the Steam
// id for the mod, the BattlEye slot for RCon.
type Player struct {
	Key  string
	Name string
}

// Backend is the way the messages reach the players.
type Backend interface {
	// Joined reports the players who have just joined and are ready for a message.
	Joined(ctx context.Context) <-chan Player
	// Say sends text to p, or to everyone when p is nil. color is one of site.MessageColors, or
	// empty for the default; a Backend that cannot colour a message ignores it.
	Say(ctx context.Context, p *Player, text, color string) error
}

// Messenger runs the messages of one instance.
type Messenger struct {
	Backend  Backend
	Messages []site.Message
	// Unit is what delay and repeat count in (a minute by default); tests make it short.
	Unit time.Duration
	// Name (the server's hostname) and Port replace #name and #port in the texts.
	Name string
	Port int
	// Log reports a message that could not be sent.
	Log func(format string, a ...any)
}

func (m *Messenger) unit() time.Duration {
	if m.Unit > 0 {
		return m.Unit
	}
	return time.Minute
}

func (m *Messenger) logf(format string, a ...any) {
	if m.Log != nil {
		m.Log(format, a...)
	}
}

func (m *Messenger) text(msg site.Message) string {
	return site.ExpandMessage(msg.Text, m.Name, m.Port)
}

func (m *Messenger) say(ctx context.Context, p *Player, msg site.Message) {
	if err := m.Backend.Say(ctx, p, m.text(msg), msg.Color); err != nil && ctx.Err() == nil {
		who := "everyone"
		if p != nil {
			who = p.Name
		}
		m.logf("message to %s not sent: %v", who, err)
	}
}

// Run sends the messages until ctx ends: a message with on_connect to every player who joins (delay
// after the join), a message with repeat to everyone every repeat.
func (m *Messenger) Run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, msg := range m.Messages {
		if msg.Repeat <= 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(time.Duration(msg.Repeat) * m.unit())
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					m.say(ctx, nil, msg)
				}
			}
		}()
	}
	joined := m.Backend.Joined(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-joined:
			if !ok {
				<-ctx.Done()
				return
			}
			for _, msg := range m.Messages {
				if !msg.OnConnect {
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					if d := time.Duration(msg.Delay) * m.unit(); d > 0 {
						select {
						case <-ctx.Done():
							return
						case <-time.After(d):
						}
					}
					m.say(ctx, &p, msg)
				}()
			}
		}
	}
}

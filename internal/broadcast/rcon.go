// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package broadcast

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/battleye"
)

// DefaultGrace is how long RCon waits after BattlEye reports a connect before it counts the player
// as joined: the character is still loading then, and a message sent at once is not seen.
const DefaultGrace = 45 * time.Second

// Session is the part of battleye.Session the RCon backend uses.
type Session interface {
	Events() <-chan battleye.Event
	Command(ctx context.Context, command string) (string, error)
}

// RCon sends the messages as BattlEye "say": plain text, without colour. A player counts as joined
// Grace after the connect.
type RCon struct {
	Session Session
	// Grace is DefaultGrace when zero.
	Grace time.Duration

	mu      sync.Mutex
	present map[int]string // BE slot -> name, from the connect to the disconnect
}

func (r *RCon) grace() time.Duration {
	if r.Grace > 0 {
		return r.Grace
	}
	return DefaultGrace
}

func (r *RCon) here(id int, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.present[id] == name && name != ""
}

// Joined follows the connects and disconnects of the session.
func (r *RCon) Joined(ctx context.Context) <-chan Player {
	out := make(chan Player)
	r.mu.Lock()
	r.present = map[int]string{}
	r.mu.Unlock()
	go func() {
		var wg sync.WaitGroup
		defer wg.Wait()
		events := r.Session.Events()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				switch ev.Kind {
				case battleye.EventConnect:
					r.mu.Lock()
					r.present[ev.PlayerID] = ev.PlayerName
					r.mu.Unlock()
					wg.Add(1)
					go func() {
						defer wg.Done()
						select {
						case <-ctx.Done():
							return
						case <-time.After(r.grace()):
						}
						if !r.here(ev.PlayerID, ev.PlayerName) {
							return
						}
						select {
						case out <- Player{Key: strconv.Itoa(ev.PlayerID), Name: ev.PlayerName}:
						case <-ctx.Done():
						}
					}()
				case battleye.EventDisconnect:
					r.mu.Lock()
					delete(r.present, ev.PlayerID)
					r.mu.Unlock()
				default:
				}
			}
		}
	}()
	return out
}

// Say sends text to the player's slot, or to everyone.
func (r *RCon) Say(ctx context.Context, p *Player, text, _ string) error {
	if strings.ContainsAny(text, "\r\n") {
		return errors.New("broadcast: a message must not contain a newline")
	}
	to := "-1"
	if p != nil {
		id, err := strconv.Atoi(p.Key)
		if err != nil || !r.here(id, p.Name) {
			return fmt.Errorf("broadcast: %s is not connected", p.Name)
		}
		to = p.Key
	}
	_, err := r.Session.Command(ctx, "say "+to+" "+text)
	return err
}

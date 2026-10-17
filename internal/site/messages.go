// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxMessageLen is the longest message text DayZ accepts.
const MaxMessageLen = 160

// Message is one of the server messages dzo sends while it runs (internal/broadcast): once to a
// player OnConnect (Delay minutes after the player is in the game) and/or to everyone every Repeat
// minutes. The text may use #name (the instance name) and #port (the game port). Color is one of
// MessageColors, blue when empty. Countdowns and shutdowns are not offered: dzo's own restart
// countdown (restarts.announce) does that over RCon.
type Message struct {
	Text      string `yaml:"text"`
	OnConnect bool   `yaml:"on_connect,omitempty"`
	Delay     int    `yaml:"delay,omitempty"`
	Repeat    int    `yaml:"repeat,omitempty"`
	Color     string `yaml:"color,omitempty"`
}

// Validate checks one message.
func (m Message) Validate() error {
	text := strings.TrimSpace(m.Text)
	switch {
	case text == "":
		return fmt.Errorf("text is required")
	case utf8.RuneCountInString(m.Text) > MaxMessageLen:
		return fmt.Errorf("text is longer than %d characters", MaxMessageLen)
	case strings.ContainsAny(m.Text, "\r\n"):
		return fmt.Errorf("text must not contain a newline")
	case strings.Contains(m.Text, "#tmin"):
		return fmt.Errorf("#tmin only works in a countdown, which dzo does not use (restarts are announced by restarts.announce)")
	case m.Color != "" && !slices.Contains(MessageColors, m.Color):
		return fmt.Errorf("color %q is not one of %s", m.Color, strings.Join(MessageColors, ", "))
	case m.Delay < 0 || m.Repeat < 0:
		return fmt.Errorf("delay and repeat must not be negative")
	case !m.OnConnect && m.Repeat == 0:
		return fmt.Errorf("set on_connect: true or repeat (minutes), otherwise the message is never sent")
	case !m.OnConnect && m.Delay != 0:
		return fmt.Errorf("delay only applies with on_connect: true")
	}
	return nil
}

// DefaultWelcome is the welcome message of an instance that sets no welcome.
const DefaultWelcome = "Welcome to #name!"

// WelcomeMessage is the message sent to a player who joins, from the welcome setting: nil is
// DefaultWelcome, an empty string means no welcome, any other text replaces the default. DayZ
// is sent as it is (#name and #port are expanded when it goes out).
func WelcomeMessage(welcome *string) (Message, bool) {
	text := DefaultWelcome
	if welcome != nil {
		text = *welcome
	}
	if strings.TrimSpace(text) == "" {
		return Message{}, false
	}
	return Message{Text: text, OnConnect: true}, true
}

// ValidateWelcome checks the welcome setting as it will be sent.
func ValidateWelcome(welcome *string) error {
	m, ok := WelcomeMessage(welcome)
	if !ok {
		return nil
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("welcome: %w", err)
	}
	return nil
}

// ValidateMessages checks every message; errors name the index.
func ValidateMessages(ms []Message) error {
	for i, m := range ms {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("messages[%d]: %w", i, err)
		}
	}
	return nil
}

// MessageColors are the colours a message can ask for: blue is the chat's own, the others are
// the game's red, green and yellow chat lines. Only the dzo-admin mod can colour a message.
var MessageColors = []string{"blue", "red", "green", "yellow"}

// ExpandMessage replaces #name and #port in a message text, as DayZ does in its own messages.
func ExpandMessage(text, name string, port int) string {
	return strings.NewReplacer("#name", name, "#port", strconv.Itoa(port)).Replace(text)
}

// EmptyMessagesXML is the mission's db/messages.xml: no messages. dzo sends the messages itself
// (internal/broadcast), and an empty file also keeps the example messages (a shutdown countdown)
// of the pristine mission from running.
const EmptyMessagesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<messages>
</messages>
`

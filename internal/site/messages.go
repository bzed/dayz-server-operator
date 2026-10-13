// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxMessageLen is the longest message text DayZ accepts.
const MaxMessageLen = 160

// Message is one entry of the server messages (mission db/messages.xml): sent once to a player
// OnConnect (Delay minutes after the connect) and/or to everyone every Repeat minutes. The text may
// use #name (server name) and #port. Countdowns and shutdowns are not offered: dzo's own restart
// countdown (restarts.announce) does that over RCon.
type Message struct {
	Text      string `yaml:"text"`
	OnConnect bool   `yaml:"on_connect,omitempty"`
	Delay     int    `yaml:"delay,omitempty"`
	Repeat    int    `yaml:"repeat,omitempty"`
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
// expands #name to the server name itself.
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

// RenderMessagesXML writes the mission's db/messages.xml for the messages. No messages give an
// empty <messages>, which switches the server messages off. deadline and shutdown are always 0.
func RenderMessagesXML(ms []Message) ([]byte, error) {
	if err := ValidateMessages(ms); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n<messages>\n")
	for _, m := range ms {
		on := 0
		if m.OnConnect {
			on = 1
		}
		fmt.Fprintf(&b, "\t<message>\n\t\t<delay>%d</delay>\n\t\t<repeat>%d</repeat>\n\t\t<deadline>0</deadline>\n\t\t<onConnect>%d</onConnect>\n\t\t<shutdown>0</shutdown>\n\t\t<text>", m.Delay, m.Repeat, on)
		if err := xml.EscapeText(&b, []byte(strings.TrimSpace(m.Text))); err != nil {
			return nil, err
		}
		b.WriteString("</text>\n\t</message>\n")
	}
	b.WriteString("</messages>\n")
	return b.Bytes(), nil
}

// CheckMessagesXML reports an error when a messages.xml cannot be used with dzo: it does not parse,
// or a message has a countdown (deadline > 0), a shutdown, or #tmin. Comments are ignored.
func CheckMessagesXML(data []byte) error {
	var doc struct {
		XMLName  xml.Name `xml:"messages"`
		Messages []struct {
			Deadline string `xml:"deadline"`
			Shutdown string `xml:"shutdown"`
			Text     string `xml:"text"`
		} `xml:"message"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("messages.xml: %w", err)
	}
	for i, m := range doc.Messages {
		if d := strings.TrimSpace(m.Deadline); d != "" && d != "0" {
			return fmt.Errorf("messages.xml: message %d has a deadline (countdown): dzo restarts and counts down itself, see restarts.announce", i+1)
		}
		if s := strings.TrimSpace(m.Shutdown); s != "" && s != "0" {
			return fmt.Errorf("messages.xml: message %d has shutdown set: dzo restarts and stops the server itself", i+1)
		}
		if strings.Contains(m.Text, "#tmin") {
			return fmt.Errorf("messages.xml: message %d uses #tmin, which only works in a countdown", i+1)
		}
	}
	return nil
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMessages(t *testing.T) {
	long := strings.Repeat("ä", MaxMessageLen+1)
	ok := strings.Repeat("ä", MaxMessageLen) // 160 runes, 320 bytes: must pass
	cases := []struct {
		name string
		m    Message
		want string // substring of the error, "" for valid
	}{
		{"welcome", Message{Text: "Welcome to #name", OnConnect: true}, ""},
		{"welcome delayed", Message{Text: "Rules: example.org/rules", OnConnect: true, Delay: 2}, ""},
		{"repeat", Message{Text: "Homepage: example.org (port #port)", Repeat: 30}, ""},
		{"both flags", Message{Text: "hi", OnConnect: true, Repeat: 15}, ""},
		{"160 runes", Message{Text: ok, Repeat: 5}, ""},
		{"never sent", Message{Text: "hi"}, "on_connect"},
		{"empty text", Message{Text: "  ", Repeat: 5}, "text"},
		{"too long", Message{Text: long, Repeat: 5}, "160"},
		{"newline", Message{Text: "a\nb", Repeat: 5}, "newline"},
		{"tmin", Message{Text: "restart in #tmin", Repeat: 5}, "#tmin"},
		{"negative repeat", Message{Text: "hi", Repeat: -1}, "negative"},
		{"negative delay", Message{Text: "hi", OnConnect: true, Delay: -1}, "negative"},
		{"delay without on_connect", Message{Text: "hi", Repeat: 5, Delay: 1}, "delay"},
	}
	for _, c := range cases {
		err := ValidateMessages([]Message{c.m})
		if c.want == "" {
			require.NoError(t, err, c.name)
		} else {
			require.ErrorContains(t, err, c.want, c.name)
		}
	}
	err := ValidateMessages([]Message{{Text: "ok", Repeat: 1}, {Text: "bad"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "messages[1]")
}

func TestMessageColor(t *testing.T) {
	for _, c := range append([]string{""}, MessageColors...) {
		err := (Message{Text: "x", Repeat: 1, Color: c}).Validate()
		require.NoError(t, err, "colour %q", c)
	}
	err := (Message{Text: "x", Repeat: 1, Color: "pink"}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pink")
}

func TestExpandMessage(t *testing.T) {
	got := ExpandMessage("Welcome to #name on #port, #name!", "dz1", 2302)
	assert.Equal(t, "Welcome to dz1 on 2302, dz1!", got)
}

func TestEmptyMessagesXMLHasNoMessages(t *testing.T) {
	require.NotContains(t, EmptyMessagesXML, "<message>")
	assert.Contains(t, EmptyMessagesXML, "<messages>")
}

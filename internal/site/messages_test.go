// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"strings"
	"testing"
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
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
	if err := ValidateMessages([]Message{{Text: "ok", Repeat: 1}, {Text: "bad"}}); err == nil || !strings.Contains(err.Error(), "messages[1]") {
		t.Errorf("the error names the index: %v", err)
	}
}

func TestMessageColor(t *testing.T) {
	for _, c := range append([]string{""}, MessageColors...) {
		if err := (Message{Text: "x", Repeat: 1, Color: c}).Validate(); err != nil {
			t.Errorf("colour %q: %v", c, err)
		}
	}
	if err := (Message{Text: "x", Repeat: 1, Color: "pink"}).Validate(); err == nil || !strings.Contains(err.Error(), "pink") {
		t.Errorf("an unknown colour is refused: %v", err)
	}
}

func TestExpandMessage(t *testing.T) {
	if got := ExpandMessage("Welcome to #name on #port, #name!", "dz1", 2302); got != "Welcome to dz1 on 2302, dz1!" {
		t.Errorf("got %q", got)
	}
}

func TestEmptyMessagesXMLHasNoMessages(t *testing.T) {
	if strings.Contains(EmptyMessagesXML, "<message>") || !strings.Contains(EmptyMessagesXML, "<messages>") {
		t.Errorf("EmptyMessagesXML = %s", EmptyMessagesXML)
	}
}

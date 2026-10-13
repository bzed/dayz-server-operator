// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestRenderMessagesXML(t *testing.T) {
	out, err := RenderMessagesXML([]Message{
		{Text: "Welcome to #name! Rules: example.org/r?a=1&b=<2>", OnConnect: true, Delay: 1},
		{Text: "Homepage: example.org", Repeat: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`,
		"<onConnect>1</onConnect>", "<delay>1</delay>", "<repeat>30</repeat>",
		"<deadline>0</deadline>", "<shutdown>0</shutdown>",
		"a=1&amp;b=&lt;2&gt;",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if err := CheckMessagesXML(out); err != nil {
		t.Errorf("our own output must pass the check: %v", err)
	}
}

func TestRenderMessagesXMLEmptyDisables(t *testing.T) {
	out, err := RenderMessagesXML(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "<message>") || !strings.Contains(string(out), "<messages>") {
		t.Errorf("empty list = empty <messages>: %s", out)
	}
	if err := CheckMessagesXML(out); err != nil {
		t.Error(err)
	}
}

func TestCheckMessagesXML(t *testing.T) {
	msg := func(inner string) []byte {
		return []byte("<messages><message>" + inner + "<text>x</text></message></messages>")
	}
	bad := map[string][]byte{
		"deadline":   msg("<deadline>600</deadline>"),
		"shutdown":   msg("<shutdown>1</shutdown>"),
		"#tmin":      []byte("<messages><message><repeat>5</repeat><text>in #tmin</text></message></messages>"),
		"not xml":    []byte("<messages><message>"),
		"wrong root": []byte("<other/>"),
	}
	for name, data := range bad {
		if err := CheckMessagesXML(data); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	good := [][]byte{
		msg("<repeat>5</repeat><deadline>0</deadline><shutdown>0</shutdown>"),
		[]byte("<messages/>"),
		[]byte("<messages><!-- <deadline>600</deadline> --></messages>"), // vanilla ships commented examples
	}
	for i, data := range good {
		if err := CheckMessagesXML(data); err != nil {
			t.Errorf("good[%d]: %v", i, err)
		}
	}
}

func str(s string) *string { return &s }

func TestWelcomeMessage(t *testing.T) {
	m, ok := WelcomeMessage(nil)
	if !ok || m.Text != "Welcome to #name!" || !m.OnConnect || m.Delay != 0 || m.Repeat != 0 {
		t.Errorf("default = %+v %v", m, ok)
	}
	m, _ = WelcomeMessage(str("Hi on #name. Rules: example.org/r?a=1&b=2"))
	if m.Text != "Hi on #name. Rules: example.org/r?a=1&b=2" {
		t.Errorf("override = %q", m.Text)
	}
	out, err := RenderMessagesXML([]Message{m})
	if err != nil || !strings.Contains(string(out), "a=1&amp;b=2") || CheckMessagesXML(out) != nil {
		t.Errorf("escaping: %v\n%s", err, out)
	}
	if _, ok := WelcomeMessage(str("")); ok {
		t.Error(`welcome: "" means no welcome`)
	}
	if _, ok := WelcomeMessage(str("  ")); ok {
		t.Error("blank means no welcome")
	}
}

func TestValidateWelcome(t *testing.T) {
	if err := ValidateWelcome(nil); err != nil {
		t.Error(err)
	}
	if err := ValidateWelcome(str("")); err != nil {
		t.Error(err)
	}
	if err := ValidateWelcome(str("Hello #name")); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{strings.Repeat("a", MaxMessageLen+1), "in #tmin", "a\nb"} {
		if err := ValidateWelcome(str(bad)); err == nil || !strings.Contains(err.Error(), "welcome") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestInstanceMessagesYAMLAndValidate(t *testing.T) {
	var inst Instance
	src := `
name: a
welcome: "Hello from #name"
messages:
  - {text: "Welcome to #name", on_connect: true, delay: 1}
  - {text: "Homepage: example.org", repeat: 30}
`
	if err := yaml.Unmarshal([]byte(src), &inst); err != nil {
		t.Fatal(err)
	}
	if inst.Welcome == nil || *inst.Welcome != "Hello from #name" || len(inst.Messages) != 2 || !inst.Messages[0].OnConnect || inst.Messages[1].Repeat != 30 {
		t.Fatalf("parsed = %+v", inst.Messages)
	}
	v := validInstance()
	v.Messages = inst.Messages
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	v.Messages = append(v.Messages, Message{Text: "never"})
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "messages[2]") {
		t.Fatalf("Instance.Validate must validate messages: %v", err)
	}
	v = validInstance()
	v.Welcome = str("in #tmin")
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "welcome") {
		t.Fatalf("Instance.Validate must validate welcome: %v", err)
	}
}

# Server Messages (db/messages.xml) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** dzo always manages the mission's `db/messages.xml`: it generates it from a welcome message (default "Welcome to #name!", `#name` being expanded by DayZ itself; overridable or switchable off) plus configurable messages (homepage, rules, ...), and never lets it carry a countdown or shutdown.

**Architecture:** A new `welcome:` string and `messages:` list in `instance.yaml` (both defaultable in `site.yaml`) are validated in `internal/site` and rendered to XML there. `integrate.Collect` always emits the instance's `messages.xml` contribution first: generated from the welcome message followed by the list; with the welcome switched off and no list it is an empty `<messages>`; a hand-written `instances/<name>/messages.xml` is still honoured). The existing `replaceFiles` strategy in `internal/mission` puts it at `db/messages.xml`. Every messages.xml that reaches the merge (instance file or mod integration) is checked: any `<deadline>` > 0, `<shutdown>1`, or `#tmin` is rejected, because restarts and countdowns belong to dzo's own RCon restart logic (`internal/instance/restart.go`), not to DayZ's native mechanism.

**Tech Stack:** Go, `encoding/xml`, `gopkg.in/yaml.v3`, Sphinx docs (`docs/*.rst`).

**Spec:** The request in this session plus the DayZ wiki page "Server Messages" (flags On connect / Repeat / Countdown / Shutdown; text max 160 chars; placeholders `#name`, `#port`, `#tmin`; all times in minutes). No separate spec file.

## Global Constraints

- Message text is at most 160 characters (DayZ limit).
- All times (`delay`, `repeat`) are minutes, as in DayZ.
- Placeholders allowed: `#name`, `#port`. `#tmin` is **not** allowed (needs a countdown).
- dzo never writes `<deadline>` other than `0` and never `<shutdown>` other than `0`; a messages.xml with a countdown or shutdown is a config error. Restarts and countdowns stay with `dzo restart` / `restarts.announce`.
- dzo always manages the file; the pristine mission's example messages (vanilla may ship a shutdown countdown) are never kept.
- `welcome` (a `*string`): unset = default `Welcome to #name!`; a text = override; `""` = no welcome. `#name` is expanded by DayZ (verified on the current configs), so dzo does not read serverDZ.cfg. The welcome is sent on connect, without delay.
- With `welcome: ""`, no `messages:` and no `messages.xml` file, the output is an empty `<messages>`: that is how messages are disabled.
- Match the surrounding code: SPDX header on new files, `//nolint:gosec` comments only where the neighbours have them, errors prefixed like `messages[0]: ...`.
- Commits end with the trailer lines the harness gives for this session.

## Review Focus

- A message with neither `on_connect` nor `repeat` would never be sent: reject it with a clear error.
- `delay` without `on_connect`, negative numbers, empty/whitespace text, text over 160 characters (count runes, not bytes; "ä" is one character), newline in text: reject.
- Text with `&`, `<`, `"` (e.g. a URL with `?a=1&b=2`) must be XML-escaped in the output and still parse.
- A hand-written `instances/<name>/messages.xml` containing `<shutdown>1</shutdown>`, `<deadline>600</deadline>` or `#tmin` must fail the render before the live mission is touched; the same file together with `messages:` in instance.yaml is ambiguous: error.
- A mod integration that ships `messages.xml` with a countdown is rejected too; one without is still allowed to replace ours (existing "later wins" and conflict note).
- Instance without any messages config: staging gets the default welcome, not the pristine file; `welcome: ""` gives an empty `<messages>`.
- A welcome override with `&`, `<` or quotes (a URL with `?a=1&b=2`) must be escaped and still parse.
- A hand-written `messages.xml` file is used as is: no default welcome is added to it, and `welcome:` set together with the file is not an error (the file wins; the docs say so).
- `site.yaml defaults.messages` is used when the instance sets none; an instance's own list replaces it as a whole (same rule as every other defaults field).

---

### Task 1: Message type, validation, XML rendering and checking

**Files:**
- Create: `internal/site/messages.go`
- Create: `internal/site/messages_test.go`
- Modify: `internal/site/schema.go` (`Instance` struct near line 461, `Instance.Validate` near line 500)
- Modify: `internal/site/tree.go` (`Defaults` struct, line ~21)

**Interfaces:**
- Produces (used by Task 2):
  - `type Message struct { Text string; OnConnect bool; Delay int; Repeat int }` (yaml: `text`, `on_connect`, `delay`, `repeat`)
  - `const MaxMessageLen = 160`
  - `func ValidateMessages(ms []Message) error`
  - `func RenderMessagesXML(ms []Message) ([]byte, error)` — empty/nil input gives a valid empty `<messages>` document
  - `func CheckMessagesXML(data []byte) error` — nil if the document parses and has no countdown/shutdown/`#tmin`
  - `const DefaultWelcome = "Welcome to #name!"`
  - `func WelcomeMessage(welcome *string) (Message, bool)` — the on-connect welcome for the `welcome:` setting (nil = default, `""` = none, else the text). Returns false when there is no welcome.
  - `func ValidateWelcome(welcome *string) error`
  - `Instance.Welcome *string` (yaml `welcome,omitempty`), `Instance.Messages []Message` (yaml `messages,omitempty`), and the same two fields on `Defaults`

- [ ] **Step 1: Write the failing tests**

`internal/site/messages_test.go`:

```go
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
		"deadline":  msg("<deadline>600</deadline>"),
		"shutdown":  msg("<shutdown>1</shutdown>"),
		"#tmin":     []byte("<messages><message><repeat>5</repeat><text>in #tmin</text></message></messages>"),
		"not xml":   []byte("<messages><message>"),
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/site -run 'Messages' -v`
Expected: build FAIL (`undefined: Message`, `ValidateMessages`, ...).

- [ ] **Step 3: Implement `internal/site/messages.go`**

```go
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
```

Wire the config in:
- `internal/site/schema.go`, `Instance` struct: add `Welcome *string \`yaml:"welcome,omitempty"\`` and `Messages []Message \`yaml:"messages,omitempty"\`` after `AdminMap`.
- `Instance.Validate`: before the `if len(errs) > 0` at the end add
  ```go
  	if err := ValidateMessages(i.Messages); err != nil {
  		errs = append(errs, err.Error())
  	}
  	if err := ValidateWelcome(i.Welcome); err != nil {
  		errs = append(errs, err.Error())
  	}
  ```
- `internal/site/tree.go`, `Defaults`: add `Welcome *string` (`yaml:"welcome,omitempty"`) and `Messages []Message` (`yaml:"messages,omitempty"`) (comment: "// Welcome and Messages are the server messages of an instance that sets none.").
- If `Site` has a validate for defaults (search `func (s Site) Validate`), also call `ValidateMessages` and `ValidateWelcome` on `s.Defaults` there.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/site -v -run 'Messages' && go test ./internal/site`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/site
git commit -m "site: messages: configurable server messages, validated and rendered to messages.xml"
```

---

### Task 2: Always manage messages.xml in the render

**Files:**
- Modify: `internal/resolve/resolve.go` (`Instance` struct line ~154, defaults list line 223, constructor line ~246)
- Modify: `internal/integrate/integrate.go` (`Collect`, lines 53-60, and mod loop line ~95)
- Test: `internal/integrate/integrate_test.go`, `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: `site.Message`, `site.RenderMessagesXML`, `site.CheckMessagesXML`, `site.ValidateMessages` (Task 1).
- Also consumes: `site.WelcomeMessage(*string) (site.Message, bool)`.
- Produces: `resolve.Instance.Welcome *string` and `resolve.Instance.Messages []site.Message` (yaml `welcome,omitempty` / `messages,omitempty`); `Collect` always returns `"instance messages.xml"` as its first contribution.

- [ ] **Step 1: Write the failing tests**

Append to `internal/integrate/integrate_test.go` (uses `tree`, `put`, `names` from the file; `tree` writes `instances/x/instance.yaml`):

```go
func collectMessages(t *testing.T, dir string, msgs []site.Message) ([]byte, error) {
	t.Helper()
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m", Messages: msgs}
	cs, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()})
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 || cs[0].Name != "instance messages.xml" || len(cs[0].Files) != 1 || cs[0].Files[0].Name != "messages.xml" {
		t.Fatalf("the instance messages.xml is always the first contribution: %+v", cs)
	}
	return cs[0].Files[0].Data, nil
}

func TestCollectDefaultWelcome(t *testing.T) {
	data, err := collectMessages(t, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<text>Welcome to #name!</text>") || !strings.Contains(string(data), "<onConnect>1</onConnect>") {
		t.Errorf("default welcome: %s", data)
	}
}

func TestCollectWelcomeOverrideAndOff(t *testing.T) {
	tr := tree(t, t.TempDir())
	collect := func(inst *resolve.Instance) string {
		cs, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return string(cs[0].Files[0].Data)
	}
	custom := "Hello on #name, rules: example.org"
	got := collect(&resolve.Instance{Name: "x", Map: "empty.m", Welcome: &custom, Messages: []site.Message{{Text: "Homepage", Repeat: 30}}})
	if !strings.Contains(got, "Hello on #name, rules: example.org") || strings.Contains(got, "Welcome to #name!") {
		t.Errorf("override replaces the default: %s", got)
	}
	if strings.Index(got, "Hello on #name") > strings.Index(got, "Homepage") {
		t.Errorf("the welcome comes first: %s", got)
	}
	off := ""
	got = collect(&resolve.Instance{Name: "x", Map: "empty.m", Welcome: &off})
	if strings.Contains(got, "<message>") {
		t.Errorf(`welcome: "" and no messages disables everything: %s`, got)
	}
}

func TestCollectMessagesFromConfig(t *testing.T) {
	data, err := collectMessages(t, t.TempDir(), []site.Message{{Text: "Welcome to #name", OnConnect: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<text>Welcome to #name</text>") {
		t.Errorf("%s", data)
	}
}

func TestCollectMessagesFileIsKeptAndChecked(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "instances", "x", "messages.xml"), "<messages><message><repeat>5</repeat><text>hi</text></message></messages>")
	data, err := collectMessages(t, dir, nil)
	if err != nil || !strings.Contains(string(data), "<text>hi</text>") || strings.Contains(string(data), "Welcome") {
		t.Fatalf("hand-written file is used as is, without the default welcome: %v %s", err, data)
	}
	for name, body := range map[string]string{
		"deadline": "<messages><message><deadline>600</deadline><text>x</text></message></messages>",
		"shutdown": "<messages><message><shutdown>1</shutdown><text>x</text></message></messages>",
		"tmin":     "<messages><message><repeat>1</repeat><text>#tmin</text></message></messages>",
	} {
		d := t.TempDir()
		put(t, filepath.Join(d, "instances", "x", "messages.xml"), body)
		if _, err := collectMessages(t, d, nil); err == nil {
			t.Errorf("%s in messages.xml must fail", name)
		}
	}
}

func TestCollectMessagesFileAndConfigAreAmbiguous(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "instances", "x", "messages.xml"), "<messages/>")
	_, err := collectMessages(t, dir, []site.Message{{Text: "x", Repeat: 1}})
	if err == nil || !strings.Contains(err.Error(), "messages.xml") || !strings.Contains(err.Error(), "messages:") {
		t.Fatalf("both set must be an error naming both: %v", err)
	}
}

func TestCollectModMessagesWithCountdownRejected(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "integrations", "mods", "1", "integration.yaml"), "mod: 1\nname: One\nfiles:\n  messages.xml: {source: local, path: files/m.xml}\n")
	put(t, filepath.Join(dir, "integrations", "mods", "1", "files", "m.xml"), "<messages><message><shutdown>1</shutdown><deadline>60</deadline><text>x</text></message></messages>")
	tr := tree(t, dir)
	inst := &resolve.Instance{Name: "x", Map: "empty.m", Mods: []resolve.Mod{{ID: 1}}}
	if _, err := Collect(context.Background(), tr, inst, Options{CacheDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "mod 1") {
		t.Fatalf("a mod's countdown must fail the collect: %v", err)
	}
}
```

Add the `site` import if missing (it is imported already in this file).

Append to `internal/resolve/resolve_test.go`: read `TestDefaultsMergeAndInstanceWins` (line 120) first and copy its fixture setup. Add `TestMessagesDefaultsAndInstanceWins` that (a) puts `defaults: {messages: [{text: "Site welcome", on_connect: true}]}` in site.yaml, resolves an instance without `messages` and asserts `inst.Messages` has that one entry; (b) an instance with `messages: [{text: "Own", repeat: 10}]` resolves to exactly that one entry (replaces, does not append). Also: site `defaults: {welcome: "Site hello"}` is used by an instance without `welcome`; an instance with `welcome: ""` keeps `""` (non-nil pointer to an empty string) and an instance with its own text wins.

Add to `internal/mission/integrate_test.go`:

```go
func TestGeneratedMessagesReplacePristine(t *testing.T) {
	d := stagingTree(t)
	data, err := site.RenderMessagesXML(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Integrate(d, []Contribution{{Name: "instance messages.xml", Folder: "custom_instance", Files: []ContribFile{{Name: "messages.xml", Data: data}}}}); err != nil {
		t.Fatal(err)
	}
	if got := readStaging(t, d, "db/messages.xml"); strings.Contains(got, "<message>") || !strings.Contains(got, "<messages>") {
		t.Errorf("db/messages.xml = %s", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/integrate ./internal/resolve ./internal/mission 2>&1 | head -30`
Expected: FAIL (`inst.Messages undefined`; rest compile-fail).

- [ ] **Step 3: Implement**

`internal/resolve/resolve.go`:
- `Instance` struct: add `Welcome *string \`yaml:"welcome,omitempty"\`` and `Messages []site.Message \`yaml:"messages,omitempty"\`` after `AdminMap`.
- In the defaults loop (line 223) add `"Welcome"` and `"Messages"` to the field-name list (`fillZero` fills a nil pointer or zero slice from the defaults; an instance's own value wins whole, and a pointer to `""` is not zero, so `welcome: ""` in an instance beats a site default).
- In the `inst := &Instance{...}` literal add `Welcome: raw.Welcome, Messages: raw.Messages`.

`internal/integrate/integrate.go`, replace lines 53-60 of `Collect` with:

```go
	msgs, err := instanceMessages(instDir, inst)
	if err != nil {
		return nil, err
	}
	out = append(out, mission.Contribution{Name: "instance messages.xml", Folder: "custom_instance", Files: []mission.ContribFile{{Name: "messages.xml", Data: msgs}}})
```

(`out` is declared above; keep `var out` and `instDir` lines.) Add:

```go
// instanceMessages is the instance's db/messages.xml: its own instances/<name>/messages.xml (used
// as is), or generated from the welcome message and messages: in instance.yaml. With no welcome
// and no messages it is an empty <messages>: dzo never leaves the pristine mission's example
// messages (possibly a shutdown countdown) in place. A file with a countdown or shutdown is
// refused, restarts are dzo's.
func instanceMessages(instDir string, inst *resolve.Instance) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(instDir, "messages.xml")) //nolint:gosec // the site checkout
	switch {
	case err == nil:
		if len(inst.Messages) > 0 {
			return nil, fmt.Errorf("instance %s: both instances/%s/messages.xml and messages: in instance.yaml are set, use one", inst.Name, inst.Name)
		}
		if err := site.CheckMessagesXML(data); err != nil {
			return nil, fmt.Errorf("instance %s: %w", inst.Name, err)
		}
		return data, nil
	case os.IsNotExist(err):
		var ms []site.Message
		if w, ok := site.WelcomeMessage(inst.Welcome); ok {
			ms = append(ms, w)
		}
		return site.RenderMessagesXML(append(ms, inst.Messages...))
	default:
		return nil, err
	}
}
In the mod loop, after the `Normalize` call and before `c.Files = append(...)`:

```go
			if strings.EqualFold(k, "messages.xml") || strings.EqualFold(k, "db/messages.xml") {
				if err := site.CheckMessagesXML(data); err != nil {
					return nil, fmt.Errorf("mod %d (%s): %w", m.ID, li.Name, err)
				}
			}
```

Check that the existing `err` variable shadowing compiles (`msgs, err :=` is the first `err` in the function; later `:=` with new names are fine, `data, err :=` in the loop is in an inner scope).

- [ ] **Step 4: Run to verify pass, then everything**

Run: `go test ./internal/integrate ./internal/resolve ./internal/mission ./internal/site && go test ./... 2>&1 | tail -20 && make lint`
Expected: PASS. If a `testdata/*.golden` in `internal/resolve` changes, the `omitempty` tag is wrong: `Messages` must not appear when empty. If a `cmd/dzo` test counts contributions, update it: there is now always one instance contribution.

- [ ] **Step 5: Commit**

```bash
git add internal/resolve internal/integrate internal/mission
git commit -m "integrate: always manage db/messages.xml: generate from messages:, else disable; refuse countdowns"
```

---

### Task 3: Documentation

**Files:**
- Modify: `docs/configuration.rst` (the `instance.yaml` example near line 156-175 and the key list after it)
- Modify: `docs/missions.rst` (merge section, line ~236, and the strategy table line ~154)
- Modify: `docs/operations.rst` (one sentence under the restart countdown, ~line 76)

- [ ] **Step 1: Edit `docs/configuration.rst`**

In the `instance.yaml` example, after the `restarts:` block add:

```rst
   welcome: "Welcome to #name!"   # the default; "" for no welcome message
   messages:                       # more server messages, written to db/messages.xml
     - {text: "Welcome to #name! Homepage: example.org, rules: example.org/rules", on_connect: true, delay: 1}
     - {text: "Please read the rules: example.org/rules", repeat: 45}
```

After the `updates.restart_minute` entry add:

```rst
``welcome``
   The message every player gets when joining. The default is ``Welcome to #name!``; DayZ
   replaces ``#name`` with the server name (``hostname`` in ``serverDZ.cfg``). Set your own text
   (``#name`` and ``#port`` work in it, the usual 160 characters apply), or ``""`` for none. Can be set under
   ``defaults:`` in ``site.yaml``.

``messages``
   Further messages the server sends to players on its own, from the mission's ``db/messages.xml``. A
   message has ``text`` (up to 160 characters; ``#name`` is the server name, ``#port`` its port),
   ``on_connect: true`` to send it once to a player who joins (``delay`` minutes later, default
   immediately) and/or ``repeat`` to send it to everybody every that many minutes. All times are
   minutes. Good for a welcome message, the homepage and the rules. It can be set under
   ``defaults:`` in ``site.yaml`` for all instances; an instance's own list replaces it.

   dzo always manages this file: the welcome message and ``messages:`` are written to it, and
   with ``welcome: ""`` and no ``messages:`` it is empty, which disables server messages. The
   example messages of the pristine mission are never used. Instead of ``messages:`` you
   can put your own ``instances/<name>/messages.xml`` in the site repository (not together with ``messages:``); it is used as it is, without the welcome message. Neither
   a ``deadline`` (countdown), ``shutdown`` nor ``#tmin`` is accepted, in ``messages:``, in that
   file, or in a mod integration's ``messages.xml``: restarts and stops are announced by dzo
   itself, see ``restarts.announce`` and :doc:`operations`. Changes apply at the next render, and the server reads the file at start, so
   they take effect at the next restart.
```

- [ ] **Step 2: Edit `docs/missions.rst` and `docs/operations.rst`**

`missions.rst` line ~236: replace "then the instance's ``messages.xml``" with "then the instance's ``messages.xml`` (its own file, or generated from ``messages:``; empty when it has neither, see :doc:`configuration`)". Add one bullet under "Notes on what is built": "* ``messages.xml`` is always written by dzo and never has a countdown or shutdown; a file that has one fails the render."

`operations.rst`, after the paragraph ending "…then kicks everyone.": add "The countdown is dzo's own and runs over RCon; the server's built-in countdown and shutdown of ``messages.xml`` are never used (see ``messages`` in :doc:`configuration`)."

- [ ] **Step 3: Build the docs**

Run: `make docs`
Expected: builds without warnings about the edited files.

- [ ] **Step 4: Commit**

```bash
git add docs
git commit -m "docs: server messages and the managed db/messages.xml"
```

---

## Self-Review

- **Spec coverage:** configurable messages with timers (`repeat`, `delay`, `on_connect`) → Task 1; default "Welcome to #name!" (expanded by DayZ), overridable and switchable off → `welcome:` (Tasks 1, 2); not managing means disabling → empty `<messages>` when there is no welcome, list or file; managing means no restart/countdown → `Validate`, `CheckMessagesXML`, mod check; docs → Task 3. Restart announcements are untouched.
- **Placeholders:** none; `TestMessagesDefaultsAndInstanceWins` is described, to be written by copying the neighbouring test's fixture, with exact assertions given.
- **Type consistency:** `site.Message`, `WelcomeMessage`, `ValidateWelcome`, `ValidateMessages`, `RenderMessagesXML`, `CheckMessagesXML`, `resolve.Instance.Welcome/Messages` are used identically in all tasks.
- **Behaviour change for the release notes:** existing instances get a generated `db/messages.xml` (default welcome) at the next render instead of the pristine file.

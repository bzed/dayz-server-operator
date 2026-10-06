// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// defaultTemplates are the built-in message templates, one per Kind, using
// text/template over Event. Data fields are reached as {{.Data.X}}; they
// can be overridden per deployment via TemplateSet.Override.
var defaultTemplates = map[Kind]string{
	KindModUpdateDetected:      "🔎 {{.Instance}}: {{.Data.Count}} mod update(s) detected",
	KindModUpdateDownloaded:    "⬇️ {{.Instance}}: mod update downloaded ({{.Data.Name}} {{.Data.ID}})",
	KindModUpdateApplied:       "✅ {{.Instance}}: {{.Data.Count}} mod update(s) applied, restarting",
	KindModRefreshResult:       "🔁 mod {{.Data.ID}} refresh: {{.Data.Result}}",
	KindProductUpdateAvailable: "🆕 {{.Data.Product}} build {{.Data.BuildID}} available (manual action needed)",
	KindRestartScheduled:       "🕒 {{.Instance}}: restart scheduled in {{.Data.Minutes}}m",
	KindRestartStarted:         "🔄 {{.Instance}}: restart started ({{.Data.Reason}})",
	KindRestartFinished:        "✅ {{.Instance}}: restart finished",
	KindHealthUnhealthy:        "🚨 {{.Instance}}: health check failing",
	KindHealthRecovered:        "✅ {{.Instance}}: health recovered",
	KindCrash:                  "💥 {{.Instance}}: the server exited unexpectedly ({{.Data.Result}})\n```\n{{.Data.Summary}}\n```",
	KindCrashLoop:              "🔥 {{.Instance}}: crash loop, server stopped",
	KindRenderFailed:           "❌ {{.Instance}}: render failed: {{.Data.Error}}",
	KindDriftDetected:          "⚠️ {{.Instance}}: drift on {{.Data.Path}} (backed up, overwritten)",
	KindSteamLoginRequired:     "🔑 Steam login required ({{.Data.Reason}})",
	KindJobFailed:              "❌ job {{.Data.Job}} failed: {{.Data.Error}}",
}

// coalescedTemplates render a batch of same-kind events as one message
// (e.g. "12 mods updated, 3 servers restarting") instead of N separate
// ones. Data available: .Count (int) and .Instances (comma-joined, in
// first-seen order, de-duplicated). A kind with no coalesced template
// falls back to joining each event's normal rendering with newlines.
var coalescedTemplates = map[Kind]string{
	KindModUpdateDetected:   "🔎 {{.Instances}}: {{.Count}} mod update(s) detected",
	KindModUpdateDownloaded: "⬇️ {{.Instances}}: {{.Count}} mod(s) downloaded",
	KindRestartStarted:      "🔄 {{.Count}} instance(s) restarting: {{.Instances}}",
}

// TemplateSet renders Events into Discord message text.
type TemplateSet struct {
	templates map[Kind]*template.Template
	coalesced map[Kind]*template.Template
}

// NewTemplateSet compiles the default templates.
func NewTemplateSet() (*TemplateSet, error) {
	ts := &TemplateSet{templates: map[Kind]*template.Template{}, coalesced: map[Kind]*template.Template{}}
	for kind, text := range defaultTemplates {
		t, err := template.New(string(kind)).Parse(text)
		if err != nil {
			return nil, fmt.Errorf("notify: default template %q: %w", kind, err)
		}
		ts.templates[kind] = t
	}
	for kind, text := range coalescedTemplates {
		t, err := template.New(string(kind) + ".coalesced").Parse(text)
		if err != nil {
			return nil, fmt.Errorf("notify: default coalesced template %q: %w", kind, err)
		}
		ts.coalesced[kind] = t
	}
	return ts, nil
}

// Override replaces (or adds) the single-event template text for kind.
func (ts *TemplateSet) Override(kind Kind, text string) error {
	t, err := template.New(string(kind)).Parse(text)
	if err != nil {
		return fmt.Errorf("notify: template %q: %w", kind, err)
	}
	ts.templates[kind] = t
	return nil
}

// Render renders ev's message. An event kind with no template (custom
// kinds a caller defines) falls back to a generic rendering.
func (ts *TemplateSet) Render(ev Event) (string, error) {
	t, ok := ts.templates[ev.Kind]
	if !ok {
		return fmt.Sprintf("%s: %s %v", ev.Kind, ev.Instance, ev.Data), nil
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ev); err != nil {
		return "", fmt.Errorf("notify: render %q: %w", ev.Kind, err)
	}
	return buf.String(), nil
}

// RenderCoalesced renders a batch of same-kind events (as Coalescer
// produces) as one message.
func (ts *TemplateSet) RenderCoalesced(kind Kind, events []Event) (string, error) {
	if len(events) == 1 {
		return ts.Render(events[0])
	}

	t, ok := ts.coalesced[kind]
	if !ok {
		lines := make([]string, 0, len(events))
		for _, e := range events {
			line, err := ts.Render(e)
			if err != nil {
				return "", err
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	}

	data := struct {
		Count     int
		Instances string
	}{Count: len(events), Instances: joinInstances(events)}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("notify: render coalesced %q: %w", kind, err)
	}
	return buf.String(), nil
}

func joinInstances(events []Event) string {
	seen := map[string]bool{}
	var names []string
	for _, e := range events {
		if e.Instance == "" || seen[e.Instance] {
			continue
		}
		seen[e.Instance] = true
		names = append(names, e.Instance)
	}
	return strings.Join(names, ", ")
}

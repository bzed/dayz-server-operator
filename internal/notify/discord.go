// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Webhook is one named Discord notification target (mirrors
// internal/config.DiscordWebhook, kept separate so this package has no
// dependency on the config loader).
type Webhook struct {
	Name string
	URL  string
}

// DiscordNotifier posts rendered Event messages to one or more Discord
// webhooks (§C9, Q14). Each instance uses the default webhook unless its
// site config lists other named targets; Event.Targets carries that
// selection (nil = default, [] = none).
type DiscordNotifier struct {
	Webhooks  []Webhook
	Default   string // name of the webhook used when Event.Targets is nil
	Templates *TemplateSet

	HTTPClient *http.Client
	// PostFunc, when set, replaces the actual HTTP POST (for tests). It
	// receives the target URL and the JSON body.
	PostFunc func(ctx context.Context, url string, body []byte) error
}

func (d *DiscordNotifier) httpClient() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	return http.DefaultClient
}

func (d *DiscordNotifier) byName(name string) (Webhook, bool) {
	for _, w := range d.Webhooks {
		if w.Name == name {
			return w, true
		}
	}
	return Webhook{}, false
}

// targets resolves Event.Targets against the configured webhooks.
func (d *DiscordNotifier) targets(ev Event) ([]Webhook, error) {
	if ev.Targets == nil {
		w, ok := d.byName(d.Default)
		if !ok {
			return nil, fmt.Errorf("notify: default webhook %q is not configured", d.Default)
		}
		return []Webhook{w}, nil
	}
	var out []Webhook
	for _, name := range *ev.Targets {
		w, ok := d.byName(name)
		if !ok {
			return nil, fmt.Errorf("notify: webhook %q is not configured", name)
		}
		out = append(out, w)
	}
	return out, nil
}

// Notify renders ev and posts it to every resolved target. An empty
// resolved target list (Event.Targets pointing at []) is a deliberate
// no-op, not an error.
func (d *DiscordNotifier) Notify(ctx context.Context, ev Event) error {
	targets, err := d.targets(ev)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	message, err := d.Templates.Render(ev)
	if err != nil {
		return err
	}
	return d.post(ctx, targets, message)
}

// NotifyBatch renders a coalesced message for events (all assumed to share
// a Kind) and posts it once to each resolved target of the first event
// (coalescing only ever batches events that already share the same
// target selection - see Coalescer).
func (d *DiscordNotifier) NotifyBatch(ctx context.Context, kind Kind, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	targets, err := d.targets(events[0])
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	message, err := d.Templates.RenderCoalesced(kind, events)
	if err != nil {
		return err
	}
	return d.post(ctx, targets, message)
}

func (d *DiscordNotifier) post(ctx context.Context, targets []Webhook, message string) error {
	// No message pings anyone: player names and chat can contain @everyone or <@id>.
	body, err := json.Marshal(struct {
		Content         string              `json:"content"`
		AllowedMentions map[string][]string `json:"allowed_mentions"`
	}{Content: message, AllowedMentions: map[string][]string{"parse": {}}})
	if err != nil {
		return fmt.Errorf("notify: marshal payload: %w", err)
	}

	for _, w := range targets {
		if d.PostFunc != nil {
			if err := d.PostFunc(ctx, w.URL, body); err != nil {
				return fmt.Errorf("notify: post to %s: %w", w.Name, err)
			}
			continue
		}
		if err := d.httpPost(ctx, w.URL, body); err != nil {
			return fmt.Errorf("notify: post to %s: %w", w.Name, err)
		}
	}
	return nil
}

func (d *DiscordNotifier) httpPost(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord returned status %d", resp.StatusCode)
	}
	return nil
}

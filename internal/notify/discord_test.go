// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestNotifier(t testing.TB, webhooks []Webhook, defaultName string) *DiscordNotifier {
	t.Helper()
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	return &DiscordNotifier{Webhooks: webhooks, Default: defaultName, Templates: ts}
}

func TestNotifyPostsToDefaultWebhook(t *testing.T) {
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Content string }
		err := json.NewDecoder(r.Body).Decode(&payload)
		assert.NoError(t, err, "decode body")
		mu.Lock()
		received = append(received, payload.Content)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	n := newTestNotifier(t, []Webhook{{Name: "default", URL: server.URL}}, "default")
	err := n.Notify(t.Context(), Event{Kind: KindHealthUnhealthy, Instance: "deerisle"})
	require.NoError(t, err, "Notify")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, received, 1)
	require.Contains(t, received[0], "deerisle")
}

func TestNotifyRejectsUnconfiguredDefault(t *testing.T) {
	n := newTestNotifier(t, nil, "default")
	err := n.Notify(t.Context(), Event{Kind: KindHealthUnhealthy})
	require.Error(t, err, "expected an error when the default webhook isn't configured")
}

func TestNotifyExplicitTargetsEmptyIsNoOp(t *testing.T) {
	n := newTestNotifier(t, []Webhook{{Name: "default", URL: "http://unused.invalid"}}, "default")
	empty := []string{}
	err := n.Notify(t.Context(), Event{Kind: KindHealthUnhealthy, Targets: &empty})
	require.NoError(t, err, "Notify")
}

func TestNotifyExplicitTargetsMultiple(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	n := newTestNotifier(t, []Webhook{
		{Name: "ops", URL: server.URL + "/ops"},
		{Name: "public", URL: server.URL + "/public"},
	}, "ops")
	targets := []string{"ops", "public"}
	err := n.Notify(t.Context(), Event{Kind: KindRestartFinished, Instance: "x", Targets: &targets})
	require.NoError(t, err, "Notify")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, hits["/ops"])
	assert.Equal(t, 1, hits["/public"])
}

func TestNotifyUnknownTargetName(t *testing.T) {
	n := newTestNotifier(t, []Webhook{{Name: "ops", URL: "http://unused.invalid"}}, "ops")
	targets := []string{"nonexistent"}
	err := n.Notify(t.Context(), Event{Kind: KindHealthUnhealthy, Targets: &targets})
	require.Error(t, err, "expected an error for an unknown webhook name")
}

func TestNotifyHTTPErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	n := newTestNotifier(t, []Webhook{{Name: "default", URL: server.URL}}, "default")
	err := n.Notify(t.Context(), Event{Kind: KindHealthUnhealthy})
	require.Error(t, err, "expected an error for a non-2xx response")
}

func TestNotifyUsesPostFuncSeam(t *testing.T) {
	var gotURL string
	var gotBody []byte
	n := newTestNotifier(t, []Webhook{{Name: "default", URL: "http://example.invalid/hook"}}, "default")
	n.PostFunc = func(ctx context.Context, url string, body []byte) error {
		gotURL = url
		gotBody = body
		return nil
	}
	err := n.Notify(t.Context(), Event{Kind: KindHealthUnhealthy, Instance: "a"})
	require.NoError(t, err, "Notify")
	assert.Equal(t, "http://example.invalid/hook", gotURL)
	var payload struct{ Content string }
	err = json.Unmarshal(gotBody, &payload)
	require.NoError(t, err, "unmarshal")
	assert.Contains(t, payload.Content, "a")
}

func TestNotifyPostFuncError(t *testing.T) {
	n := newTestNotifier(t, []Webhook{{Name: "default", URL: "http://example.invalid/hook"}}, "default")
	n.PostFunc = func(ctx context.Context, url string, body []byte) error {
		return context.DeadlineExceeded
	}
	err := n.Notify(t.Context(), Event{Kind: KindHealthUnhealthy})
	require.Error(t, err, "expected the PostFunc error to propagate")
}

func TestNotifyBatch(t *testing.T) {
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Content string }
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		received = append(received, payload.Content)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	n := newTestNotifier(t, []Webhook{{Name: "default", URL: server.URL}}, "default")
	events := []Event{
		{Kind: KindModUpdateDetected, Instance: "a"},
		{Kind: KindModUpdateDetected, Instance: "b"},
	}
	err := n.NotifyBatch(t.Context(), KindModUpdateDetected, events)
	require.NoError(t, err, "NotifyBatch")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, received, 1)
	require.Contains(t, received[0], "2")
}

func TestNotifyBatchEmpty(t *testing.T) {
	n := newTestNotifier(t, nil, "default")
	err := n.NotifyBatch(t.Context(), KindModUpdateDetected, nil)
	require.NoError(t, err, "NotifyBatch(nil) =")
}

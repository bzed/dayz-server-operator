// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type lookupFake struct{ err error }

func (l lookupFake) LookupNow(context.Context, string) error { return l.err }

func TestPlayersClientRoundTrip(t *testing.T) {
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(t.Context(), filepath.Join(dir, "dzo.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	srv.Moderation = okMod{st}
	look := &lookupFake{}
	srv.Lookups = look
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	secret, _, _ := srv.Tokens.Create("admin", api.RoleAdmin, nil, true, false, time.Hour)
	c := New(ts.URL, secret).WithActor("alice", "test")
	require.NoError(t, st.PlayerJoined(t.Context(), "alpha", "76561198000000001", "Alice", time.Now()))

	list, total, err := c.PlayerHistory(t.Context(), PlayerHistoryFilter{Query: "ali", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, "Alice", list[0].Name)

	d, err := c.PlayerDetail(t.Context(), "76561198000000001")
	require.NoError(t, err)
	assert.Equal(t, "https://steamcommunity.com/profiles/76561198000000001", d.ProfileURL)

	d, err = c.LookupPlayer(t.Context(), "76561198000000001")
	require.NoError(t, err)
	assert.Equal(t, "Alice", d.Name)

	look.err = moderation.ErrLookupTooSoon
	_, err = c.LookupPlayer(t.Context(), "76561198000000001")
	var ae *Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusTooManyRequests, ae.Status)

	_, err = c.PlayerDetail(t.Context(), "76561198000000099")
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusNotFound, ae.Status)
}

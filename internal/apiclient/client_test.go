// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setup(t testing.TB) (*apiclient.Client, *admin.Hub, *api.Server) {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hub := hubs.Add(admin.NewHub("alpha"))
	srv := api.New("site", "v0", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	secret, _, err := srv.Tokens.Create("admin", api.RoleAdmin, nil, true, time.Hour)
	require.NoError(t, err)
	hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, Players: []admin.Player{{SteamID: "1"}}, Vehicles: []admin.Vehicle{{ID: "v"}},
		Markers: []admin.Marker{{Layer: "l", ID: "m"}}, Layers: []admin.Layer{{Name: "l"}}, Events: []admin.Event{{ID: "e"}},
		Types: &admin.TypesChunk{Hash: "h", Total: 1, Names: []string{"AKM"}}})
	return apiclient.New(ts.URL+"/", secret).WithActor("alice", "web"), hub, srv
}

func answer(t testing.TB, hub *admin.Hub, ok bool) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			var res []admin.Result
			for _, c := range hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
				res = append(res, admin.Result{ID: c.ID, OK: admin.Flag(ok), Message: map[bool]string{true: "", false: "refused"}[ok]})
			}
			if len(res) > 0 {
				hub.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func TestReads(t *testing.T) {
	c, _, _ := setup(t)
	ctx := t.Context()
	info, err := c.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, "site", info.Installation)
	list, err := c.Instances(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	st, _ := c.Status(ctx, "alpha")
	pl, _ := c.Players(ctx, "alpha")
	ve, _ := c.Vehicles(ctx, "alpha")
	ev, _ := c.Events(ctx, "alpha")
	la, _ := c.Layers(ctx, "alpha")
	ty, _ := c.Types(ctx, "alpha", "ak", 5)
	require.True(t, st.Connected, "status %+v", st)
	require.Len(t, pl, 1)
	require.Len(t, ve, 1)
	require.Len(t, ev, 1)
	require.Len(t, la, 1)
	require.Len(t, ty, 1)
	_, err = c.Markers(ctx, "alpha", "l")
	require.NoError(t, err)
	_, err = c.Status(ctx, "nope")
	var ae *apiclient.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 404, ae.Status)
	_, err = apiclient.New("http://127.0.0.1:1", "x").Info(ctx)
	require.Error(t, err, "unreachable server must fail")
}

func TestActions(t *testing.T) {
	c, hub, srv := setup(t)
	defer answer(t, hub, true)()
	ctx := t.Context()
	for name, f := range map[string]func() (api.ActionResult, error){
		"broadcast": func() (api.ActionResult, error) { return c.Message(ctx, "alpha", "", api.MessageBody{Text: "hi"}) },
		"message": func() (api.ActionResult, error) {
			return c.Message(ctx, "alpha", "1", api.MessageBody{Text: "hi", Style: "important"})
		},
		"tp":        func() (api.ActionResult, error) { return c.Teleport(ctx, "alpha", "1", 10, 20, "") },
		"tp-player": func() (api.ActionResult, error) { return c.Teleport(ctx, "alpha", "1", 0, 0, "2") },
		"heal":      func() (api.ActionResult, error) { return c.Heal(ctx, "alpha", "1") },
		"fixgear":   func() (api.ActionResult, error) { return c.FixGear(ctx, "alpha", "1") },
		"kill":      func() (api.ActionResult, error) { return c.Kill(ctx, "alpha", "1") },
		"give":      func() (api.ActionResult, error) { return c.Give(ctx, "alpha", "1", "AKM", 1, 1, "hands", true) },
		"spawn":     func() (api.ActionResult, error) { return c.Spawn(ctx, "alpha", "AKM", 20, 1, 10, 20, false) },
		"spawn-veh": func() (api.ActionResult, error) { return c.SpawnVehicle(ctx, "alpha", "AKM", 10, 20) },
		"repair":    func() (api.ActionResult, error) { return c.Repair(ctx, "alpha", "v", "") },
		"delete":    func() (api.ActionResult, error) { return c.DeleteVehicle(ctx, "alpha", "v", true) },
	} {
		r, err := f()
		require.NoError(t, err, name)
		assert.True(t, r.OK, "%s: %+v", name, r)
	}
	entries, err := c.Audit(ctx, "alpha", 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "admin:alice", entries[0].Actor)
	require.Equal(t, "web", entries[0].Source)
	// A validation error is an Error, not a result.
	_, err = c.Give(ctx, "alpha", "1", "bad class", 1, 1, "", false)
	require.Error(t, err, "bad class must fail")
	_ = srv
}

func TestActionRefused(t *testing.T) {
	c, hub, _ := setup(t)
	defer answer(t, hub, false)()
	r, err := c.Message(t.Context(), "alpha", "1", api.MessageBody{Text: "x"})
	require.NoError(t, err)
	require.False(t, r.OK)
	require.Equal(t, "refused", r.Message)
}

func TestStream(t *testing.T) {
	c, hub, _ := setup(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	seen := map[string]bool{}
	err := c.Stream(ctx, "alpha", func(kind string, data json.RawMessage) {
		seen[kind] = true
		if kind == "events" {
			hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: "2"}}})
		}
		if kind == "players" && len(seen) > 4 && string(data) != "" && json.Valid(data) {
			var p []admin.Player
			_ = json.Unmarshal(data, &p)
			if len(p) == 1 && p[0].SteamID == "2" {
				cancel()
			}
		}
	})
	require.ErrorIs(t, err, context.Canceled, "seen %v", seen)
	bad := apiclient.New(c.Base, "wrong")
	err = bad.Stream(t.Context(), "alpha", func(string, json.RawMessage) {})
	require.Error(t, err, "bad token must fail the stream")
}

func TestTileRejectsABadPathAndRelaysTheAnswer(t *testing.T) {
	c, _, _ := setup(t)
	_, err := c.Tile(t.Context(), "../x", "metadata.json")
	require.Error(t, err, "a bad tile path must not be sent")
	resp, err := c.Tile(t.Context(), "chernarusplus", "metadata.json")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.NotEqual(t, 200, resp.StatusCode)
}

func TestErrorText(t *testing.T) {
	got := (&apiclient.Error{Status: 404, Message: "no such instance"}).Error()
	assert.Equal(t, "api: 404 no such instance", got)
}

func TestMessageSendsTheWholeBody(t *testing.T) {
	c, hub, _ := setup(t)
	hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, World: "w", Features: []string{"expansion"}})
	stop := answer(t, hub, true)
	defer stop()
	r, err := c.Message(t.Context(), "alpha", "", api.MessageBody{Text: "x", Style: "expansion", NotifyType: "market", Title: "T", Seconds: 7})
	require.NoError(t, err)
	require.True(t, r.OK, "Message = %+v", r)
	_, err = c.Message(t.Context(), "alpha", "1", api.MessageBody{Text: "x", Style: "expansion", NotifyType: "popup"})
	assert.Error(t, err, "an unknown type was accepted")
}

func TestFeedAndStreamOnly(t *testing.T) {
	c, hub, _ := setup(t)
	hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: "one"})
	hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: "two"})
	got, err := c.Feed(t.Context(), "alpha", 1, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "two", got[0].Text)
	_, err = c.Feed(t.Context(), "nope", 0, 0)
	require.Error(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// The stream has no snapshot to wait for, so add events until one arrives.
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			hub.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: "three"})
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	var kinds []string
	err = c.StreamOnly(ctx, "alpha", "feed", func(kind string, _ json.RawMessage) {
		kinds = append(kinds, kind)
		cancel()
	})
	<-done
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "feed", kinds[0])
	require.NotContains(t, kinds, "players")
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
)

func setup(t *testing.T) (*apiclient.Client, *admin.Hub, *api.Server) {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hub := hubs.Add(admin.NewHub("alpha"))
	srv := api.New("site", "v0", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	secret, _, err := srv.Tokens.Create("admin", api.RoleAdmin, nil, true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, Players: []admin.Player{{SteamID: "1"}}, Vehicles: []admin.Vehicle{{ID: "v"}},
		Markers: []admin.Marker{{Layer: "l", ID: "m"}}, Layers: []admin.Layer{{Name: "l"}}, Events: []admin.Event{{ID: "e"}},
		Types: &admin.TypesChunk{Hash: "h", Total: 1, Names: []string{"AKM"}}})
	return apiclient.New(ts.URL+"/", secret).WithActor("alice", "web"), hub, srv
}

func answer(t *testing.T, hub *admin.Hub, ok bool) func() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for ctx.Err() == nil {
			var res []admin.Result
			for _, c := range hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
				res = append(res, admin.Result{ID: c.ID, OK: admin.Flag(ok), Message: map[bool]string{true: "", false: "refused"}[ok]})
			}
			if len(res) > 0 {
				hub.Sync(admin.SyncRequest{Proto: 1, Results: res})
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return cancel
}

func TestReads(t *testing.T) {
	c, _, _ := setup(t)
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil || info.Installation != "site" {
		t.Fatalf("info %+v %v", info, err)
	}
	list, err := c.Instances(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("instances %v %v", list, err)
	}
	st, _ := c.Status(ctx, "alpha")
	pl, _ := c.Players(ctx, "alpha")
	ve, _ := c.Vehicles(ctx, "alpha")
	ev, _ := c.Events(ctx, "alpha")
	la, _ := c.Layers(ctx, "alpha")
	ty, _ := c.Types(ctx, "alpha", "ak", 5)
	if !st.Connected || len(pl) != 1 || len(ve) != 1 || len(ev) != 1 || len(la) != 1 || len(ty) != 1 {
		t.Fatalf("reads: %+v %v %v %v %v %v", st, pl, ve, ev, la, ty)
	}
	if _, err := c.Markers(ctx, "alpha", "l"); err != nil {
		t.Fatal(err)
	}
	_, err = c.Status(ctx, "nope")
	var ae *apiclient.Error
	if !errors.As(err, &ae) || ae.Status != 404 {
		t.Fatalf("err = %v", err)
	}
	if _, err := apiclient.New("http://127.0.0.1:1", "x").Info(ctx); err == nil {
		t.Fatal("unreachable server must fail")
	}
}

func TestActions(t *testing.T) {
	c, hub, srv := setup(t)
	defer answer(t, hub, true)()
	ctx := context.Background()
	for name, f := range map[string]func() (api.ActionResult, error){
		"broadcast": func() (api.ActionResult, error) { return c.Message(ctx, "alpha", "", "hi", "") },
		"message":   func() (api.ActionResult, error) { return c.Message(ctx, "alpha", "1", "hi", "important") },
		"tp":        func() (api.ActionResult, error) { return c.Teleport(ctx, "alpha", "1", 10, 20, "") },
		"tp-player": func() (api.ActionResult, error) { return c.Teleport(ctx, "alpha", "1", 0, 0, "2") },
		"give":      func() (api.ActionResult, error) { return c.Give(ctx, "alpha", "1", "AKM", 1, 1, "hands") },
		"repair":    func() (api.ActionResult, error) { return c.Repair(ctx, "alpha", "v", "") },
		"delete":    func() (api.ActionResult, error) { return c.DeleteVehicle(ctx, "alpha", "v", true) },
	} {
		if r, err := f(); err != nil || !r.OK {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
	entries, err := c.Audit(ctx, "alpha", 1)
	if err != nil || len(entries) != 1 || entries[0].Actor != "admin:alice" || entries[0].Source != "web" {
		t.Fatalf("audit = %+v %v", entries, err)
	}
	// A validation error is an Error, not a result.
	if _, err := c.Give(ctx, "alpha", "1", "bad class", 1, 1, ""); err == nil {
		t.Fatal("bad class must fail")
	}
	_ = srv
}

func TestActionRefused(t *testing.T) {
	c, hub, _ := setup(t)
	defer answer(t, hub, false)()
	r, err := c.Message(context.Background(), "alpha", "1", "x", "")
	if err != nil || r.OK || r.Message != "refused" {
		t.Fatalf("r = %+v err = %v", r, err)
	}
}

func TestStream(t *testing.T) {
	c, hub, _ := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v seen %v", err, seen)
	}
	bad := apiclient.New(c.Base, "wrong")
	if err := bad.Stream(context.Background(), "alpha", func(string, json.RawMessage) {}); err == nil {
		t.Fatal("bad token must fail the stream")
	}
}

func TestTileRejectsABadPathAndRelaysTheAnswer(t *testing.T) {
	c, _, _ := setup(t)
	if _, err := c.Tile(context.Background(), "../x", "metadata.json"); err == nil {
		t.Error("a bad tile path must not be sent")
	}
	resp, err := c.Tile(context.Background(), "chernarusplus", "metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Errorf("no tiles are built in this test, got %d", resp.StatusCode)
	}
}

func TestErrorText(t *testing.T) {
	if got := (&apiclient.Error{Status: 404, Message: "no such instance"}).Error(); got != "api: 404 no such instance" {
		t.Errorf("Error() = %q", got)
	}
}

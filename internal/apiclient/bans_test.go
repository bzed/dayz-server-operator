// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type okMod struct{ st *moderation.Store }

func (m okMod) Store() *moderation.Store { return m.st }
func (m okMod) Kick(_ context.Context, instance, steamID, reason string) error {
	if steamID == "76561198000000099" {
		return moderation.ErrNotOnline
	}
	return nil
}
func (m okMod) Changed(context.Context, moderation.Ban, *moderation.Ban) map[string]string {
	return map[string]string{"alpha": "ok"}
}

func banClient(t *testing.T) *Client {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(context.Background(), filepath.Join(dir, "dzo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv.Moderation = okMod{st}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	secret, _, _ := srv.Tokens.Create("admin", api.RoleAdmin, nil, true, time.Hour)
	return New(ts.URL, secret).WithActor("alice", "test")
}

func TestBanClientRoundTrip(t *testing.T) {
	c, ctx := banClient(t), context.Background()
	res, err := c.CreateBan(ctx, moderation.BanInput{SteamID: "76561198000000001", Reason: "aimbot", Scope: moderation.ScopeGlobal})
	if err != nil || res.Sync["alpha"] != "ok" {
		t.Fatalf("CreateBan = %+v, %v", res, err)
	}
	in := res.Ban.Input()
	in.Reason = "aimbot!"
	if r2, err := c.UpdateBan(ctx, res.Ban.ID, in); err != nil || r2.Ban.Reason != "aimbot!" {
		t.Fatalf("UpdateBan = %+v, %v", r2, err)
	}
	if bs, err := c.Bans(ctx, BanFilter{State: "active", Query: "76561198000000001"}); err != nil || len(bs) != 1 {
		t.Fatalf("Bans = %+v, %v", bs, err)
	}
	if _, err := c.RevokeBan(ctx, res.Ban.ID, "appeal"); err != nil {
		t.Fatal(err)
	}
	if h, err := c.BanHistory(ctx, res.Ban.ID); err != nil || len(h) != 3 {
		t.Fatalf("BanHistory = %+v, %v", h, err)
	}
	if _, err := c.Ban(ctx, 999); err == nil {
		t.Fatal("Ban(999) found")
	}
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	ev, err := c.AddEvidenceImage(ctx, moderation.EvidenceInput{SteamID: "76561198000000001", Instance: "alpha", Body: "shot"}, png)
	if err != nil || ev.Kind != moderation.EvidenceImage {
		t.Fatalf("AddEvidenceImage = %+v, %v", ev, err)
	}
	if _, err := c.AddEvidence(ctx, moderation.EvidenceInput{SteamID: "76561198000000001", Instance: "alpha", Kind: moderation.EvidenceLog, Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if list, err := c.Evidence(ctx, moderation.Who{SteamID: "76561198000000001"}); err != nil || len(list) != 2 {
		t.Fatalf("Evidence = %+v, %v", list, err)
	}
	resp, err := c.EvidenceFile(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Header.Get("Content-Type") != "image/png" || len(b) != len(png) {
		t.Fatalf("EvidenceFile: %v %d", resp.Header, len(b))
	}
	if _, err := c.Identities(ctx, "76561198000000001"); err != nil {
		t.Fatal(err)
	}
}

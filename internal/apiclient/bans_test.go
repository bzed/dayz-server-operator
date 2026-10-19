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
	"github.com/stretchr/testify/require"
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

func banClient(t testing.TB) *Client {
	t.Helper()
	dir := t.TempDir()
	hubs := admin.NewRegistry(dir)
	hubs.Add(admin.NewHub("alpha"))
	srv := api.New("s", "v", hubs, api.NewTokenStore(filepath.Join(dir, "t.json")), admin.NewAudit(filepath.Join(dir, "a.jsonl")))
	st, err := moderation.Open(t.Context(), filepath.Join(dir, "dzo.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	srv.Moderation = okMod{st}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	secret, _, _ := srv.Tokens.Create("admin", api.RoleAdmin, nil, true, time.Hour)
	return New(ts.URL, secret).WithActor("alice", "test")
}

func TestBanClientRoundTrip(t *testing.T) {
	c, ctx := banClient(t), t.Context()
	res, err := c.CreateBan(ctx, moderation.BanInput{SteamID: "76561198000000001", Reason: "aimbot", Scope: moderation.ScopeGlobal})
	require.NoError(t, err)
	require.Equal(t, "ok", res.Sync["alpha"])
	in := res.Ban.Input()
	in.Reason = "aimbot!"
	r2, err := c.UpdateBan(ctx, res.Ban.ID, in)
	require.NoError(t, err)
	require.Equal(t, "aimbot!", r2.Ban.Reason)
	bs, err := c.Bans(ctx, BanFilter{State: "active", Query: "76561198000000001"})
	require.NoError(t, err)
	require.Len(t, bs, 1)
	_, err = c.RevokeBan(ctx, res.Ban.ID, "appeal")
	require.NoError(t, err)
	h, err := c.BanHistory(ctx, res.Ban.ID)
	require.NoError(t, err)
	require.Len(t, h, 3)
	_, err = c.Ban(ctx, 999)
	require.Error(t, err, "Ban(999) found")
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	ev, err := c.AddEvidenceImage(ctx, moderation.EvidenceInput{SteamID: "76561198000000001", Instance: "alpha", Body: "shot"}, png)
	require.NoError(t, err)
	require.Equal(t, moderation.EvidenceImage, ev.Kind)
	_, err = c.AddEvidence(ctx, moderation.EvidenceInput{SteamID: "76561198000000001", Instance: "alpha", Kind: moderation.EvidenceLog, Body: "x"})
	require.NoError(t, err)
	list, err := c.Evidence(ctx, moderation.Who{SteamID: "76561198000000001"})
	require.NoError(t, err)
	require.Len(t, list, 2)
	resp, err := c.EvidenceFile(ctx, ev.ID)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	require.Len(t, b, len(png))
	_, err = c.Identities(ctx, "76561198000000001")
	require.NoError(t, err)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const steamB = "76561198000000002"

type fakeLookups struct {
	mu    sync.Mutex
	asked []string
	err   error
}

func (f *fakeLookups) LookupNow(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, id)
	return f.err
}

// playerEnv has alice (steamA) on alpha and bob (steamB) on beta, and a BattlEye-banned alice.
func playerEnv(t *testing.T) (*env, *fakeMod, *fakeLookups) {
	t.Helper()
	e, m := newBanEnv(t)
	ctx := t.Context()
	require.NoError(t, m.st.PlayerJoined(ctx, "alpha", steamA, "Alice", banNow.Add(-time.Hour)))
	require.NoError(t, m.st.PlayerJoined(ctx, "beta", steamB, "Bob", banNow.Add(-2*time.Hour)))
	g, _ := moderation.DeriveGUID(steamA)
	require.NoError(t, m.st.SaveBEStatus(ctx, g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "Global Ban #1", CheckedAt: banNow}))
	require.NoError(t, m.st.Seen(ctx, "alpha", g, "192.0.2.7", "Alice", banNow))
	l := &fakeLookups{}
	e.srv.Lookups = l
	return e, m, l
}

func TestPlayerRoutesNeedPlayerHistory(t *testing.T) {
	e, _, _ := playerEnv(t)
	for _, c := range []struct {
		role Role
		want int
	}{{RoleViewer, 403}, {RoleAnnouncer, 403}, {RoleDetector, 403}, {RoleModerator, 200}, {RoleOperator, 200}, {RoleAdmin, 200}} {
		tok := e.token(c.role, nil, false)
		for _, r := range [][2]string{{"GET", "/api/v1/players"}, {"GET", "/api/v1/players/" + steamA}, {"POST", "/api/v1/players/" + steamA + "/lookup"}} {
			code, b := e.do(r[0], r[1], tok, "")
			assert.Equal(t, c.want, code, "%s %s %s: %s", c.role, r[0], r[1], b)
		}
	}
	code, _ := e.do("GET", "/api/v1/players", "", "")
	assert.Equal(t, 401, code)
}

func TestPlayersList(t *testing.T) {
	e, _, _ := playerEnv(t)
	mod := e.token(RoleModerator, nil, false)
	var out struct {
		Players []moderation.PlayerSummary `json:"players"`
		Total   int                        `json:"total"`
	}
	code, b := e.do("GET", "/api/v1/players", mod, "")
	require.Equal(t, 200, code, "%s", b)
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, 2, out.Total)
	require.Len(t, out.Players, 2)
	assert.Equal(t, steamA, out.Players[0].SteamID, "most recently seen first")
	assert.True(t, out.Players[0].BEBanned)

	_, b = e.do("GET", "/api/v1/players?q=bob", mod, "")
	require.NoError(t, json.Unmarshal(b, &out))
	require.Len(t, out.Players, 1)
	assert.Equal(t, steamB, out.Players[0].SteamID)
	_, b = e.do("GET", "/api/v1/players?flagged=1", mod, "")
	require.NoError(t, json.Unmarshal(b, &out))
	require.Len(t, out.Players, 1)
	assert.Equal(t, steamA, out.Players[0].SteamID)
	_, b = e.do("GET", "/api/v1/players?instance=beta", mod, "")
	require.NoError(t, json.Unmarshal(b, &out))
	require.Len(t, out.Players, 1)
	_, b = e.do("GET", "/api/v1/players?online=1", mod, "")
	require.NoError(t, json.Unmarshal(b, &out))
	assert.LessOrEqual(t, len(out.Players), 2)
	for _, p := range out.Players {
		assert.True(t, p.Online, "online=1 lists only online players")
	}
	_, b = e.do("GET", "/api/v1/players?sort=name", mod, "")
	require.NoError(t, json.Unmarshal(b, &out))
	require.Len(t, out.Players, 2)
	first := out.Players[0].Name
	_, b = e.do("GET", "/api/v1/players?sort=name&dir=desc", mod, "")
	require.NoError(t, json.Unmarshal(b, &out))
	assert.NotEqual(t, first, out.Players[0].Name, "dir=desc reverses the order")
	code, b = e.do("GET", "/api/v1/players?sort=1%3BDROP", mod, "")
	assert.Equal(t, 400, code, "%s", b)
	_, b = e.do("GET", "/api/v1/players?limit=1&offset=1", mod, "")
	require.NoError(t, json.Unmarshal(b, &out))
	require.Len(t, out.Players, 1)
	assert.Equal(t, 2, out.Total)
}

func TestPlayersListIsCappedAt200(t *testing.T) {
	e, m, _ := playerEnv(t)
	for i := range 210 {
		require.NoError(t, m.st.PlayerJoined(t.Context(), "alpha", fmt.Sprintf("765611980000%05d", 100+i), "p", banNow.Add(-time.Duration(i)*time.Minute)))
	}
	var out struct {
		Players []moderation.PlayerSummary `json:"players"`
	}
	_, b := e.do("GET", "/api/v1/players?limit=500", e.token(RoleModerator, nil, false), "")
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Len(t, out.Players, 200)
}

func TestPlayersOfARestrictedToken(t *testing.T) {
	e, _, _ := playerEnv(t)
	tok := e.token(RoleModerator, []string{"alpha"}, false)
	var out struct {
		Players []moderation.PlayerSummary `json:"players"`
	}
	_, b := e.do("GET", "/api/v1/players", tok, "")
	require.NoError(t, json.Unmarshal(b, &out))
	require.Len(t, out.Players, 1, "only players seen on its instances")
	assert.Equal(t, steamA, out.Players[0].SteamID)
	code, _ := e.do("GET", "/api/v1/players/"+steamB, tok, "")
	assert.Equal(t, 404, code, "bob was only on beta")
	code, _ = e.do("POST", "/api/v1/players/"+steamB+"/lookup", tok, "")
	assert.Equal(t, 404, code)
	code, _ = e.do("GET", "/api/v1/players?instance=beta", tok, "")
	assert.Equal(t, 404, code, "an instance the token may not touch")
}

func TestPlayerDetail(t *testing.T) {
	e, m, _ := playerEnv(t)
	_, err := m.st.CreateBan(t.Context(), "x", moderation.BanInput{SteamID: steamA, Reason: "aimbot", Scope: moderation.ScopeGlobal}, banNow)
	require.NoError(t, err)
	_, err = m.st.CreateBan(t.Context(), "x", moderation.BanInput{SteamID: steamB, Reason: "someone else", Scope: moderation.ScopeGlobal}, banNow)
	require.NoError(t, err)
	var d PlayerDetail
	code, b := e.do("GET", "/api/v1/players/"+steamA, e.token(RoleModerator, nil, false), "")
	require.Equal(t, 200, code, "%s", b)
	require.NoError(t, json.Unmarshal(b, &d))
	assert.Equal(t, "Alice", d.Name)
	assert.True(t, d.BEBanned)
	assert.Equal(t, "https://steamcommunity.com/profiles/"+steamA, d.ProfileURL)
	require.Len(t, d.Bans, 1, "only the bans of this player")
	assert.Equal(t, "aimbot", d.Bans[0].Reason)
	require.Len(t, d.Identities, 1)
	assert.Empty(t, d.Identities[0].IP, "addresses need ip.history")
	assert.NotContains(t, string(b), "192.0.2.7")

	_, b = e.do("GET", "/api/v1/players/"+steamA, e.token(RoleAdmin, nil, false), "")
	require.NoError(t, json.Unmarshal(b, &d))
	assert.Equal(t, "192.0.2.7", d.Identities[0].IP)
	var audited bool
	entries, _ := e.srv.Audit.Tail("", 50)
	for _, a := range entries {
		audited = audited || a.Action == "ip.history"
	}
	assert.True(t, audited, "reading the address history is recorded")
}

func TestPlayerDetailErrors(t *testing.T) {
	e, _, _ := playerEnv(t)
	mod := e.token(RoleModerator, nil, false)
	code, _ := e.do("GET", "/api/v1/players/nonsense", mod, "")
	assert.Equal(t, 400, code)
	code, _ = e.do("GET", "/api/v1/players/76561198000000099", mod, "")
	assert.Equal(t, 404, code)
}

func TestPlayerLookupPost(t *testing.T) {
	e, _, l := playerEnv(t)
	mod := e.token(RoleModerator, nil, true)
	code, b := e.do("POST", "/api/v1/players/"+steamA+"/lookup", mod, "", "X-Dzo-Actor", "carol")
	require.Equal(t, 200, code, "%s", b)
	assert.Equal(t, []string{steamA}, l.asked)
	var d PlayerDetail
	require.NoError(t, json.Unmarshal(b, &d), "the answer is the fresh record")
	assert.Equal(t, steamA, d.SteamID)
	entries, _ := e.srv.Audit.Tail("", 50)
	var found bool
	for _, a := range entries {
		if a.Action == "player.lookup" && a.Target == steamA && strings.Contains(a.Actor, "carol") {
			found = true
		}
	}
	assert.True(t, found, "audited: %+v", entries)

	l.err = moderation.ErrLookupTooSoon
	code, _ = e.do("POST", "/api/v1/players/"+steamA+"/lookup", mod, "")
	assert.Equal(t, http.StatusTooManyRequests, code)
	l.err = fmt.Errorf("steam is down")
	code, _ = e.do("POST", "/api/v1/players/"+steamA+"/lookup", mod, "")
	assert.Equal(t, http.StatusBadGateway, code)

	e.srv.Lookups = nil
	code, _ = e.do("POST", "/api/v1/players/"+steamA+"/lookup", mod, "")
	assert.Equal(t, http.StatusServiceUnavailable, code)
}

func TestPlayersWithoutADatabase(t *testing.T) {
	e := newEnv(t)
	code, _ := e.do("GET", "/api/v1/players", e.token(RoleModerator, nil, false), "")
	assert.Equal(t, http.StatusServiceUnavailable, code)
}

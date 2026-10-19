// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

func TestIPPermissions(t *testing.T) {
	for role, want := range map[Role][2]bool{
		RoleDetector: {false, false}, RoleViewer: {false, false}, RoleModerator: {false, false},
		RoleOperator: {true, false}, RoleAdmin: {true, true},
	} {
		got := [2]bool{role.Can(PermPlayerIP), role.Can(PermIPHistory)}
		assert.Equal(t, want, got)
	}
}

func seenAlice(t testing.TB, m *fakeMod, ips ...string) {
	t.Helper()
	g, _ := moderation.DeriveGUID(steamA)
	for i, ip := range ips {
		err := m.st.Seen(t.Context(), "alpha", g, ip, "alice", banNow.Add(time.Duration(i)*time.Hour))
		require.NoError(t, err)
	}
}

func TestIdentitiesShowAddressesOnlyWithTheHistoryPermission(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7")
	_, b := e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleOperator, nil, false), "")
	require.NotContains(t, string(b), "192.0.2.7")
	require.Contains(t, string(b), `"ref":"s`)
	require.Contains(t, string(b), `"shared_with":0`)
	_, b = e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleAdmin, nil, false), "")
	require.Contains(t, string(b), "192.0.2.7")
}

func TestCurrentConnectionNeedsPlayerIP(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7", "198.51.100.1")
	path := "/api/v1/instances/alpha/players/" + steamA + "/connection"
	code, b := e.do("GET", path, e.token(RoleOperator, nil, false), "")
	require.Equal(t, 200, code, "operator: %d %s", code, b)
	require.Contains(t, string(b), "198.51.100.1", "operator: %d %s", code, b)
	require.NotContains(t, string(b), "192.0.2.7", "operator: %d %s", code, b)
	code, _ = e.do("GET", path, e.token(RoleModerator, nil, false), "")
	require.Equal(t, 403, code)
	code, _ = e.do("GET", "/api/v1/instances/alpha/players/123/connection", e.token(RoleOperator, nil, false), "")
	require.Equal(t, 400, code)
}

func TestBansHideAddressesFromThoseWithoutThePermission(t *testing.T) {
	e, _ := newBanEnv(t)
	op, adm := e.token(RoleOperator, nil, false), e.token(RoleAdmin, nil, false)
	code, b := e.do("POST", "/api/v1/bans", op, banBody("instances", []string{"alpha"}, 0, "192.0.2.7"))
	require.Equal(t, 201, code, "create: %d %s", code, b)
	require.NotContains(t, string(b), "192.0.2.7", "create: %d %s", code, b)
	var res BanResult
	_ = json.Unmarshal(b, &res)
	for _, path := range []string{"/api/v1/bans", fmt.Sprintf("/api/v1/bans/%d", res.Ban.ID), fmt.Sprintf("/api/v1/bans/%d/history", res.Ban.ID)} {
		_, b := e.do("GET", path, op, "")
		assert.NotContains(t, string(b), "192.0.2.7")
		_, b = e.do("GET", path, adm, "")
		assert.Contains(t, string(b), "192.0.2.7")
	}
	// Editing with what the operator saw keeps the address.
	in := res.Ban.Input()
	in.Reason = "aimbot, confirmed"
	body, _ := json.Marshal(in)
	code, b = e.do("PUT", fmt.Sprintf("/api/v1/bans/%d", res.Ban.ID), op, string(body))
	require.Equal(t, 200, code, "edit: %d %s", code, b)
	require.NotContains(t, string(b), "192.0.2.7", "edit: %d %s", code, b)
	_, b = e.do("GET", fmt.Sprintf("/api/v1/bans/%d", res.Ban.ID), adm, "")
	require.Contains(t, string(b), "192.0.2.7")
}

func TestBanByAddressReferenceNeedsBanIPAndNeverShowsTheAddress(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7")
	_, b := e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleOperator, nil, false), "")
	var ids []moderation.Sighting
	_ = json.Unmarshal(b, &ids)
	body, _ := json.Marshal(moderation.BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: moderation.ScopeInstances, Instances: []string{"alpha"}})
	code, _ := e.do("POST", "/api/v1/bans", e.token(RoleModerator, nil, false), string(body))
	require.Equal(t, 403, code)
	code, b = e.do("POST", "/api/v1/bans", e.token(RoleOperator, nil, false), string(body))
	require.Equal(t, 201, code, "operator: %d %s", code, b)
	require.NotContains(t, string(b), "192.0.2.7", "operator: %d %s", code, b)
}

func TestEvidenceHidesItsAddress(t *testing.T) {
	e, _ := newBanEnv(t)
	op := e.token(RoleOperator, nil, false)
	body := `{"ip":"192.0.2.7","instance":"alpha","kind":"note","body":"spamming"}`
	code, b := e.do("POST", "/api/v1/evidence", op, body)
	require.Equal(t, 201, code, "add: %d %s", code, b)
	require.NotContains(t, string(b), "192.0.2.7", "add: %d %s", code, b)
	_, b = e.do("GET", "/api/v1/evidence?ip=192.0.2.7", op, "")
	require.NotContains(t, string(b), `"ip"`)
	require.Contains(t, string(b), "spamming")
	_, b = e.do("GET", "/api/v1/evidence?ip=192.0.2.7", e.token(RoleAdmin, nil, false), "")
	require.Contains(t, string(b), "192.0.2.7")
}

func TestAuditTargetsNameNoAddress(t *testing.T) {
	b := moderation.Ban{Targets: []moderation.Target{{Kind: moderation.TargetSteam, Value: steamA}, {Kind: moderation.TargetIP, Value: "192.0.2.7"}}}
	got := targetsOf(b)
	require.Equal(t, steamA+",ip", got)
	require.Equal(t, "ip", ipLabel("192.0.2.7"))
	require.Empty(t, ipLabel(""))
}

func TestReadingTheAddressHistoryIsAudited(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7")
	e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleOperator, nil, false), "")
	got, _ := e.srv.Audit.Tail("", 10)
	require.Empty(t, got)
	e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleAdmin, nil, false), "")
	got, _ = e.srv.Audit.Tail("", 10)
	require.Len(t, got, 1)
	require.Equal(t, "ip.history", got[0].Action)
	require.Equal(t, steamA, got[0].Target)
	require.NotContains(t, got[0].Detail+got[0].Result, "192.0.2.7")
}

func TestKickNeedsThePermissionAndAShortReason(t *testing.T) {
	e, m := newBanEnv(t)
	path := "/api/v1/instances/alpha/players/" + steamA + "/kick"
	code, _ := e.do("POST", path, e.token(RoleViewer, nil, false), `{"reason":"afk"}`)
	require.Equal(t, 403, code)
	code, _ = e.do("POST", path, e.token(RoleModerator, nil, false), strings.Repeat("x", 0)+`{"reason":""}`)
	require.Equal(t, 400, code)
	long := `{"reason":"` + strings.Repeat("r", moderation.MaxMessage+1) + `"}`
	code, b := e.do("POST", path, e.token(RoleModerator, nil, false), long)
	require.Equal(t, 400, code, "long reason: %d %s", code, b)
	require.Contains(t, string(b), "64", "long reason: %d %s", code, b)
	code, b = e.do("POST", path, e.token(RoleModerator, nil, true), `{"reason":"afk"}`, "X-Dzo-Actor", "alice")
	require.Equal(t, 200, code, "kick: %d %s %v", code, b, m.kicks)
	require.Len(t, m.kicks, 1, "kick: %d %s %v", code, b, m.kicks)
	require.Equal(t, "alpha "+steamA+" afk", m.kicks[0], "kick: %d %s %v", code, b, m.kicks)
	got, _ := e.srv.Audit.Tail("alpha", 5)
	require.NotEmpty(t, got)
	require.Equal(t, "player.kick", got[0].Action)
	require.Equal(t, steamA, got[0].Target)
	require.Contains(t, got[0].Detail, "afk")
	m.kickErr = moderation.ErrNotOnline
	code, _ = e.do("POST", path, e.token(RoleModerator, nil, false), `{"reason":"afk"}`)
	require.Equal(t, 404, code)
}

func TestPlayerActionPermissions(t *testing.T) {
	for role, want := range map[Role][4]bool{ // kick, kill, heal, fix gear
		RoleDetector: {false, false, false, false}, RoleViewer: {false, false, false, false},
		RoleModerator: {true, false, false, false}, RoleOperator: {true, true, true, true}, RoleAdmin: {true, true, true, true},
	} {
		got := [4]bool{role.Can(PermKick), role.Can(PermKill), role.Can(PermHeal), role.Can(PermFixGear)}
		assert.Equal(t, want, got)
	}
}

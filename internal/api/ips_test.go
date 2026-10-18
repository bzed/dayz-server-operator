// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

func TestIPPermissions(t *testing.T) {
	for role, want := range map[Role][2]bool{
		RoleDetector: {false, false}, RoleViewer: {false, false}, RoleModerator: {false, false},
		RoleOperator: {true, false}, RoleAdmin: {true, true},
	} {
		if got := [2]bool{role.Can(PermPlayerIP), role.Can(PermIPHistory)}; got != want {
			t.Errorf("%s: player.ip, ip.history = %v, want %v", role, got, want)
		}
	}
}

func seenAlice(t *testing.T, m *fakeMod, ips ...string) {
	t.Helper()
	g, _ := moderation.DeriveGUID(steamA)
	for i, ip := range ips {
		if err := m.st.Seen(context.Background(), "alpha", g, ip, "alice", banNow.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIdentitiesShowAddressesOnlyWithTheHistoryPermission(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7")
	_, b := e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleOperator, nil, false), "")
	if strings.Contains(string(b), "192.0.2.7") || !strings.Contains(string(b), `"ref":"s`) || !strings.Contains(string(b), `"shared_with":0`) {
		t.Fatalf("operator: %s", b)
	}
	_, b = e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleAdmin, nil, false), "")
	if !strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("admin: %s", b)
	}
}

func TestCurrentConnectionNeedsPlayerIP(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7", "198.51.100.1")
	path := "/api/v1/instances/alpha/players/" + steamA + "/connection"
	code, b := e.do("GET", path, e.token(RoleOperator, nil, false), "")
	if code != 200 || !strings.Contains(string(b), "198.51.100.1") || strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("operator: %d %s", code, b)
	}
	if code, _ := e.do("GET", path, e.token(RoleModerator, nil, false), ""); code != 403 {
		t.Fatalf("moderator: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/instances/alpha/players/123/connection", e.token(RoleOperator, nil, false), ""); code != 400 {
		t.Fatalf("bad steam id: %d", code)
	}
}

func TestBansHideAddressesFromThoseWithoutThePermission(t *testing.T) {
	e, _ := newBanEnv(t)
	op, adm := e.token(RoleOperator, nil, false), e.token(RoleAdmin, nil, false)
	code, b := e.do("POST", "/api/v1/bans", op, banBody("instances", []string{"alpha"}, 0, "192.0.2.7"))
	if code != 201 || strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("create: %d %s", code, b)
	}
	var res BanResult
	_ = json.Unmarshal(b, &res)
	for _, path := range []string{"/api/v1/bans", fmt.Sprintf("/api/v1/bans/%d", res.Ban.ID), fmt.Sprintf("/api/v1/bans/%d/history", res.Ban.ID)} {
		if _, b := e.do("GET", path, op, ""); strings.Contains(string(b), "192.0.2.7") {
			t.Errorf("%s shows the address to an operator: %s", path, b)
		}
		if _, b := e.do("GET", path, adm, ""); !strings.Contains(string(b), "192.0.2.7") {
			t.Errorf("%s hides the address from an admin: %s", path, b)
		}
	}
	// Editing with what the operator saw keeps the address.
	in := res.Ban.Input()
	in.Reason = "aimbot, confirmed"
	body, _ := json.Marshal(in)
	if code, b := e.do("PUT", fmt.Sprintf("/api/v1/bans/%d", res.Ban.ID), op, string(body)); code != 200 || strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("edit: %d %s", code, b)
	}
	_, b = e.do("GET", fmt.Sprintf("/api/v1/bans/%d", res.Ban.ID), adm, "")
	if !strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("the edit dropped the address: %s", b)
	}
}

func TestBanByAddressReferenceNeedsBanIPAndNeverShowsTheAddress(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7")
	_, b := e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleOperator, nil, false), "")
	var ids []moderation.Sighting
	_ = json.Unmarshal(b, &ids)
	body, _ := json.Marshal(moderation.BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: moderation.ScopeInstances, Instances: []string{"alpha"}})
	if code, _ := e.do("POST", "/api/v1/bans", e.token(RoleModerator, nil, false), string(body)); code != 403 {
		t.Fatalf("a moderator banned by address: %d", code)
	}
	code, b := e.do("POST", "/api/v1/bans", e.token(RoleOperator, nil, false), string(body))
	if code != 201 || strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("operator: %d %s", code, b)
	}
}

func TestEvidenceHidesItsAddress(t *testing.T) {
	e, _ := newBanEnv(t)
	op := e.token(RoleOperator, nil, false)
	body := `{"ip":"192.0.2.7","instance":"alpha","kind":"note","body":"spamming"}`
	if code, b := e.do("POST", "/api/v1/evidence", op, body); code != 201 || strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("add: %d %s", code, b)
	}
	if _, b := e.do("GET", "/api/v1/evidence?ip=192.0.2.7", op, ""); strings.Contains(string(b), `"ip"`) || !strings.Contains(string(b), "spamming") {
		t.Fatalf("list: %s", b)
	}
	if _, b := e.do("GET", "/api/v1/evidence?ip=192.0.2.7", e.token(RoleAdmin, nil, false), ""); !strings.Contains(string(b), "192.0.2.7") {
		t.Fatalf("admin list: %s", b)
	}
}

func TestAuditTargetsNameNoAddress(t *testing.T) {
	b := moderation.Ban{Targets: []moderation.Target{{Kind: moderation.TargetSteam, Value: steamA}, {Kind: moderation.TargetIP, Value: "192.0.2.7"}}}
	if got := targetsOf(b); got != steamA+",ip" {
		t.Fatalf("targetsOf = %q", got)
	}
	if ipLabel("192.0.2.7") != "ip" || ipLabel("") != "" {
		t.Fatal("ipLabel")
	}
}

func TestReadingTheAddressHistoryIsAudited(t *testing.T) {
	e, m := newBanEnv(t)
	seenAlice(t, m, "192.0.2.7")
	e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleOperator, nil, false), "")
	if got, _ := e.srv.Audit.Tail("", 10); len(got) != 0 {
		t.Fatalf("a read without addresses was audited: %+v", got)
	}
	e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleAdmin, nil, false), "")
	got, _ := e.srv.Audit.Tail("", 10)
	if len(got) != 1 || got[0].Action != "ip.history" || got[0].Target != steamA || strings.Contains(got[0].Detail+got[0].Result, "192.0.2.7") {
		t.Fatalf("audit: %+v", got)
	}
}

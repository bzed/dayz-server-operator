// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const webSteam = "76561198000000001"

type webMod struct{ st *moderation.Store }

func (m webMod) Store() *moderation.Store { return m.st }
func (m webMod) Changed(context.Context, moderation.Ban, *moderation.Ban) map[string]string {
	return map[string]string{"alpha": "ok"}
}

func (e *env) postForm(path string, form url.Values, csrf bool) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.web.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if csrf {
		req.Header.Set("X-CSRF-Token", e.srv.csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestBanFormCreatesAndFansOut(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	if code != 200 || !strings.Contains(body, webSteam) || !strings.Contains(body, `name="also" value="other"`) {
		t.Fatalf("form: %d %s", code, body)
	}
	form := url.Values{"steam_id": {webSteam}, "reason": {"aimbot <script>"}, "message": {"bye"}, "duration": {"7d"},
		"scope": {"instances"}, "instances": {"alpha"}, "also": {"other"}}
	if code, _ := e.postForm("/b/main/bans", form, false); code != 403 {
		t.Fatalf("no CSRF token: %d", code)
	}
	code, body = e.postForm("/b/main/bans", form, true)
	if code != 200 || strings.Count(body, "ban #1") != 2 || !strings.Contains(body, "alpha: ok") {
		t.Fatalf("create: %d %s", code, body)
	}
	_, list, _ := e.get("/b/main/bans?state=active")
	if !strings.Contains(list, "aimbot &lt;script&gt;") || strings.Contains(list, "<script>") {
		t.Fatalf("list escaping: %s", list)
	}
	_, other, _ := e.get("/b/other/bans")
	if !strings.Contains(other, "aimbot") {
		t.Fatalf("the other backend has no ban: %s", other)
	}
}

func TestBanDetailEditAndRevoke(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	form := url.Values{"steam_id": {webSteam}, "reason": {"first"}, "duration": {"permanent"}, "scope": {"global"}}
	if code, body := e.postForm("/b/main/bans", form, true); code != 200 || !strings.Contains(body, "ban #1") {
		t.Fatalf("create: %d %s", code, body)
	}
	code, body, _ := e.get("/b/main/bans/1")
	if code != 200 || !strings.Contains(body, "first") || !strings.Contains(body, "create") {
		t.Fatalf("detail: %d %s", code, body)
	}
	form.Set("reason", "second")
	form.Set("duration", "keep")
	if code, body := e.postForm("/b/main/bans/1", form, true); code != 200 || !strings.Contains(body, "changed") {
		t.Fatalf("edit: %d %s", code, body)
	}
	if code, body := e.postForm("/b/main/bans/1/revoke", url.Values{"reason": {"appeal"}}, true); code != 200 || !strings.Contains(body, "revoked") {
		t.Fatalf("revoke: %d %s", code, body)
	}
	_, body, _ = e.get("/b/main/bans/1")
	if !strings.Contains(body, "second") || !strings.Contains(body, "appeal") {
		t.Fatalf("detail after: %s", body)
	}
	if code, _, _ := e.get("/b/main/bans/abc"); code != 404 {
		t.Fatalf("bad id: %d", code)
	}
}

func TestEvidenceImageProxy(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	c := e.srv.backend("main").Client
	ev, err := c.AddEvidenceImage(context.Background(), moderation.EvidenceInput{SteamID: webSteam, Instance: "alpha", Body: "shot"}, png)
	if err != nil {
		t.Fatal(err)
	}
	code, body, hdr := e.get("/b/main/evidence/" + itoa(ev.ID) + "/file")
	if code != 200 || hdr.Get("Content-Type") != "image/png" || hdr.Get("X-Content-Type-Options") != "nosniff" || len(body) != len(png) {
		t.Fatalf("proxy: %d %v", code, hdr)
	}
	_, page, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	if !strings.Contains(page, `<img src="/b/main/evidence/`+itoa(ev.ID)+`/file"`) {
		t.Fatalf("the form does not show the player's evidence: %s", page)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestBanFormRefusesAMessageBattlEyeWouldCut(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	form := url.Values{"steam_id": {webSteam}, "reason": {"a long internal reason"}, "message": {strings.Repeat("ä", 33)},
		"duration": {"7d"}, "scope": {"global"}}
	_, body := e.postForm("/b/main/bans", form, true)
	if !strings.Contains(body, "BattlEye shows at most 64") {
		t.Fatalf("no explanation for the player message: %s", body)
	}
	if _, list, _ := e.get("/b/main/bans?state=active"); strings.Contains(list, "ban #1") || strings.Contains(list, webSteam) {
		t.Fatalf("the ban was created anyway: %s", list)
	}
}

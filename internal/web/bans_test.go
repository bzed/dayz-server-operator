// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const webSteam = "76561198000000001"

type webMod struct{ st *moderation.Store }

func (m webMod) Store() *moderation.Store { return m.st }
func (m webMod) Kick(_ context.Context, instance, steamID, reason string) error {
	kicked = append(kicked, instance+" "+steamID+" "+reason)
	return nil
}

// kicked records what the web tests asked to kick (the tests of this package do not run in parallel).
var kicked []string

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
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestBanFormCreatesAndFansOut(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	code, body, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	require.Equal(t, 200, code, "form: %s", body)
	require.Contains(t, body, webSteam)
	require.Contains(t, body, `name="also" value="other"`)
	form := url.Values{"steam_id": {webSteam}, "reason": {"aimbot <script>"}, "message": {"bye"}, "duration": {"7d"},
		"scope": {"instances"}, "instances": {"alpha"}, "also": {"other"}}
	code, _ = e.postForm("/b/main/bans", form, false)
	require.Equal(t, 403, code, "no CSRF token")
	code, body = e.postForm("/b/main/bans", form, true)
	require.Equal(t, 200, code, "create: %s", body)
	require.Equal(t, 2, strings.Count(body, "ban #1"), "create: %s", body)
	require.Contains(t, body, "alpha: ok")
	_, list, _ := e.get("/b/main/bans?state=active")
	require.Contains(t, list, "aimbot &lt;script&gt;")
	require.NotContains(t, list, "<script>")
	_, other, _ := e.get("/b/other/bans")
	require.Contains(t, other, "aimbot", "the other backend has no ban")
}

func TestBanDetailEditAndRevoke(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	form := url.Values{"steam_id": {webSteam}, "reason": {"first"}, "duration": {"permanent"}, "scope": {"global"}}
	code, body := e.postForm("/b/main/bans", form, true)
	require.Equal(t, 200, code, "create: %s", body)
	require.Contains(t, body, "ban #1")
	code, body, _ = e.get("/b/main/bans/1")
	require.Equal(t, 200, code, "detail: %s", body)
	require.Contains(t, body, "first")
	require.Contains(t, body, "create")
	form.Set("reason", "second")
	form.Set("duration", "keep")
	code, body = e.postForm("/b/main/bans/1", form, true)
	require.Equal(t, 200, code, "edit: %s", body)
	require.Contains(t, body, "changed")
	code, body = e.postForm("/b/main/bans/1/revoke", url.Values{"reason": {"appeal"}}, true)
	require.Equal(t, 200, code, "revoke: %s", body)
	require.Contains(t, body, "revoked")
	_, body, _ = e.get("/b/main/bans/1")
	require.Contains(t, body, "second")
	require.Contains(t, body, "appeal")
	code, _, _ = e.get("/b/main/bans/abc")
	require.Equal(t, 404, code, "bad id")
}

func TestEvidenceImageProxy(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	c := e.srv.backend("main").Client
	ev, err := c.AddEvidenceImage(t.Context(), moderation.EvidenceInput{SteamID: webSteam, Instance: "alpha", Body: "shot"}, png)
	require.NoError(t, err)
	code, body, hdr := e.get("/b/main/evidence/" + itoa(ev.ID) + "/file")
	require.Equal(t, 200, code, "proxy: %v", hdr)
	require.Equal(t, "image/png", hdr.Get("Content-Type"))
	require.Equal(t, "nosniff", hdr.Get("X-Content-Type-Options"))
	require.Len(t, body, len(png))
	_, page, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	require.Contains(t, page, `<img src="/b/main/evidence/`+itoa(ev.ID)+`/file"`, "the form does not show the player's evidence")
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestBanFormRefusesAMessageBattlEyeWouldCut(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	form := url.Values{"steam_id": {webSteam}, "reason": {"a long internal reason"}, "message": {strings.Repeat("ä", 33)},
		"duration": {"7d"}, "scope": {"global"}}
	_, body := e.postForm("/b/main/bans", form, true)
	require.Contains(t, body, "BattlEye shows at most 64", "no explanation for the player message")
	_, list, _ := e.get("/b/main/bans?state=active")
	require.NotContains(t, list, "ban #1", "the ban was created anyway")
	require.NotContains(t, list, webSteam, "the ban was created anyway")
}

func TestBanFormOffersAddressesWithoutShowingThem(t *testing.T) {
	e := newEnv(t, api.RoleOperator)
	guid, _ := moderation.DeriveGUID(webSteam)
	st := e.api.Moderation.(webMod).st
	_ = st.Seen(t.Context(), "alpha", guid, "192.0.2.7", "alice", time.Now())
	_ = st.Seen(t.Context(), "alpha", "ea4796e1d71203969fbaad87e2195978", "192.0.2.7", "bob", time.Now())
	_, body, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	require.NotContains(t, body, "192.0.2.7")
	require.Contains(t, body, `name="ip_refs"`)
	require.Contains(t, body, "shared with 1 other player")
	_, ids, _ := e.get("/b/main/bans/new?steam_id=" + webSteam)
	ref := between(ids, `name="ip_refs" value="`, `"`)
	form := url.Values{"steam_id": {webSteam}, "reason": {"evading"}, "duration": {"7d"}, "scope": {"instances"}, "instances": {"alpha"}, "ip_refs": {ref}}
	_, out := e.postForm("/b/main/bans", form, true)
	require.Contains(t, out, "ban #1", "create")
	_, page, _ := e.get("/b/main/bans/1")
	require.NotContains(t, page, "192.0.2.7")
	require.Contains(t, page, "address hidden")
	require.Contains(t, page, `name="ip_refs"`)
	_, list, _ := e.get("/b/main/bans")
	require.NotContains(t, list, "192.0.2.7", "list")
}

func TestBanFormShowsAddressesWithTheHistoryPermission(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	guid, _ := moderation.DeriveGUID(webSteam)
	_ = e.api.Moderation.(webMod).st.Seen(t.Context(), "alpha", guid, "192.0.2.7", "alice", time.Now())
	_, body, _ := e.get("/b/main/bans/new?steam_id=" + webSteam)
	require.Contains(t, body, "192.0.2.7")
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	return s[:strings.Index(s, b)]
}

func TestBanFormDefaultsToThisInstanceOnly(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/bans/new?steam_id=" + webSteam + "&instance=alpha")
	for _, want := range []string{`name="scope_choice" value="this" checked`, `name="this_instance" value="alpha"`,
		`name="scope_choice" value="global"`, `name="scope_choice" value="chosen"`} {
		require.Contains(t, body, want, "scope choices")
	}
	form := url.Values{"steam_id": {webSteam}, "reason": {"x"}, "duration": {"7d"}, "scope_choice": {"this"}, "this_instance": {"alpha"}}
	e.postForm("/b/main/bans", form, true)
	b, err := e.srv.backend("main").Client.Ban(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, moderation.ScopeInstances, b.Scope)
	require.Equal(t, []string{"alpha"}, b.Instances)
	form.Set("scope_choice", "global")
	e.postForm("/b/main/bans", form, true)
	b, err = e.srv.backend("main").Client.Ban(t.Context(), 2)
	require.NoError(t, err)
	require.Equal(t, moderation.ScopeGlobal, b.Scope)
	// Without an instance in the link there is no "this instance" choice.
	_, body, _ = e.get("/b/main/bans/new?steam_id=" + webSteam)
	require.NotContains(t, body, `value="this"`, "form without an instance")
	require.Contains(t, body, `name="scope_choice" value="chosen" checked`, "form without an instance")
}

func TestBanPageManagesEvidence(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.postForm("/b/main/bans", url.Values{"steam_id": {webSteam}, "reason": {"x"}, "duration": {"7d"}, "scope_choice": {"this"}, "this_instance": {"alpha"}}, true)
	_, page, _ := e.get("/b/main/bans/1")
	require.Contains(t, page, `hx-post="/b/main/bans/1/evidence"`, "no evidence form")
	require.Contains(t, page, `enctype="multipart/form-data"`, "no evidence form")
	// A note.
	code, out := e.postMultipart("/b/main/bans/1/evidence", map[string]string{"kind": "note", "instance": "alpha", "body": "chat spam <b>"}, nil)
	require.Equal(t, 200, code, "note: %s", out)
	require.Contains(t, out, "added", "note")
	// An image.
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)
	code, out = e.postMultipart("/b/main/bans/1/evidence", map[string]string{"kind": "image", "instance": "alpha", "body": "the shot"}, png)
	require.Equal(t, 200, code, "image: %s", out)
	require.Contains(t, out, "added", "image")
	_, page, _ = e.get("/b/main/bans/1")
	for _, want := range []string{"chat spam &lt;b&gt;", `<img src="/b/main/evidence/2/file"`, `hx-post="/b/main/bans/1/evidence/1/unlink"`} {
		require.Contains(t, page, want, "evidence not shown")
	}
	code, out = e.postForm("/b/main/bans/1/evidence/1/unlink", url.Values{}, true)
	require.Equal(t, 200, code, "unlink: %s", out)
	require.Contains(t, out, "unlinked")
	_, page, _ = e.get("/b/main/bans/1")
	require.NotContains(t, page, `evidence/1/unlink`, "after unlink the note is offered to link again")
	require.Contains(t, page, `hx-post="/b/main/bans/1/evidence/link"`, "after unlink the note is offered to link again")
	require.Contains(t, page, `name="evidence" value="1"`, "after unlink the note is offered to link again")
	code, out = e.postForm("/b/main/bans/1/evidence/link", url.Values{"evidence": {"1"}}, true)
	require.Equal(t, 200, code, "link: %s", out)
	require.Contains(t, out, "linked")
	_, page, _ = e.get("/b/main/bans/1")
	require.Contains(t, page, `evidence/1/unlink`, "not linked again")
	code, _ = e.postForm("/b/main/bans/1/evidence/1/unlink", url.Values{}, false)
	require.Equal(t, 403, code, "unlink without a CSRF token")
}

func (e *env) postMultipart(path string, fields map[string]string, file []byte) (int, string) {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "shot.png")
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.web.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", e.srv.csrf)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

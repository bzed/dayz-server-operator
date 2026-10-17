// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

var testPNG = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)

func (e *env) upload(token string, fields map[string]string, file []byte) (int, []byte) {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "shot.png")
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/evidence", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestDetectorFilesLogEvidence(t *testing.T) {
	e, _ := newBanEnv(t)
	det := e.token(RoleDetector, []string{"alpha"}, false)
	code, b := e.do("POST", "/api/v1/evidence", det, `{"steam_id":"`+steamA+`","instance":"alpha","kind":"log","body":"teleported 3 km"}`)
	if code != 201 {
		t.Fatalf("add: %d %s", code, b)
	}
	if code, _ := e.do("POST", "/api/v1/evidence", det, `{"steam_id":"`+steamA+`","instance":"beta","kind":"log","body":"x"}`); code != 400 {
		t.Fatalf("detector filed for an instance it may not touch: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/evidence?steam_id="+steamA, det, ""); code != 403 {
		t.Fatalf("detector read evidence: %d", code)
	}
	mod := e.token(RoleModerator, nil, false)
	code, b = e.do("GET", "/api/v1/evidence?steam_id="+steamA, mod, "")
	if code != 200 || !strings.Contains(string(b), "teleported 3 km") {
		t.Fatalf("list: %d %s", code, b)
	}
	if code, _ := e.do("GET", "/api/v1/evidence", mod, ""); code != 400 {
		t.Fatalf("list without a player: %d", code)
	}
}

func TestEvidenceFileHeaders(t *testing.T) {
	e, _ := newBanEnv(t)
	mod := e.token(RoleModerator, nil, false)
	code, b := e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha", "body": "screenshot"}, testPNG)
	if code != 201 {
		t.Fatalf("upload: %d %s", code, b)
	}
	var ev moderation.Evidence
	_ = json.Unmarshal(b, &ev)
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/evidence/%d/file", e.ts.URL, ev.ID), nil)
	req.Header.Set("Authorization", "Bearer "+mod)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	h := resp.Header
	if resp.StatusCode != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || !bytes.Equal(got, testPNG) {
		t.Fatalf("file: %d %v", resp.StatusCode, h)
	}
	if code, b := e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha"}, []byte("<html><script>x</script>")); code != 400 {
		t.Fatalf("html accepted as an image: %d %s", code, b)
	}
	if code, _ := e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha"}, append(testPNG, make([]byte, moderation.MaxImage)...)); code != 413 && code != 400 {
		t.Fatalf("oversized image: %d", code)
	}
	if code, _ := e.do("GET", fmt.Sprintf("/api/v1/evidence/%d/file", ev.ID+100), mod, ""); code != 404 {
		t.Fatalf("missing file: %d", code)
	}
}

func TestIdentitiesRoute(t *testing.T) {
	e, m := newBanEnv(t)
	g, _ := moderation.DeriveGUID(steamA)
	_ = m.st.Seen(context.Background(), "alpha", g, "192.0.2.7", "alice", banNow)
	code, b := e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleModerator, nil, false), "")
	if code != 200 || !strings.Contains(string(b), `"192.0.2.7"`) || !strings.Contains(string(b), `"shared_with":0`) {
		t.Fatalf("identities: %d %s", code, b)
	}
	if code, _ := e.do("GET", "/api/v1/players/123/identities", e.token(RoleModerator, nil, false), ""); code != 400 {
		t.Fatalf("bad steam id: %d", code)
	}
}

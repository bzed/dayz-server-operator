// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.NoError(e.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestDetectorFilesLogEvidence(t *testing.T) {
	e, _ := newBanEnv(t)
	det := e.token(RoleDetector, []string{"alpha"}, false)
	code, _ := e.do("POST", "/api/v1/evidence", det, `{"steam_id":"`+steamA+`","instance":"alpha","kind":"log","body":"teleported 3 km"}`)
	require.Equal(t, 201, code)
	code, _ = e.do("POST", "/api/v1/evidence", det, `{"steam_id":"`+steamA+`","instance":"beta","kind":"log","body":"x"}`)
	require.Equal(t, 400, code)
	code, _ = e.do("GET", "/api/v1/evidence?steam_id="+steamA, det, "")
	require.Equal(t, 403, code)
	mod := e.token(RoleModerator, nil, false)
	code, b := e.do("GET", "/api/v1/evidence?steam_id="+steamA, mod, "")
	require.Equal(t, 200, code, "list: %d %s", code, b)
	require.Contains(t, string(b), "teleported 3 km", "list: %d %s", code, b)
	code, _ = e.do("GET", "/api/v1/evidence", mod, "")
	require.Equal(t, 400, code)
}

func TestEvidenceFileHeaders(t *testing.T) {
	e, _ := newBanEnv(t)
	mod := e.token(RoleModerator, nil, false)
	code, b := e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha", "body": "screenshot"}, testPNG)
	require.Equal(t, 201, code)
	var ev moderation.Evidence
	_ = json.Unmarshal(b, &ev)
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/evidence/%d/file", e.ts.URL, ev.ID), nil)
	req.Header.Set("Authorization", "Bearer "+mod)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	h := resp.Header
	require.Equal(t, 200, resp.StatusCode, "file: %d %v", resp.StatusCode, h)
	require.Equal(t, "image/png", h.Get("Content-Type"), "file: %d %v", resp.StatusCode, h)
	require.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), "file: %d %v", resp.StatusCode, h)
	require.Contains(t, h.Get("Content-Security-Policy"), "sandbox", "file: %d %v", resp.StatusCode, h)
	require.Equal(t, testPNG, got, "file: %d %v", resp.StatusCode, h)
	code, _ = e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha"}, []byte("<html><script>x</script>"))
	require.Equal(t, 400, code)
	code, _ = e.upload(mod, map[string]string{"steam_id": steamA, "instance": "alpha"}, append(testPNG, make([]byte, moderation.MaxImage)...))
	require.Contains(t, []int{400, 413}, code, "oversized image")
	code, _ = e.do("GET", fmt.Sprintf("/api/v1/evidence/%d/file", ev.ID+100), mod, "")
	require.Equal(t, 404, code)
}

func TestIdentitiesRoute(t *testing.T) {
	e, m := newBanEnv(t)
	g, _ := moderation.DeriveGUID(steamA)
	_ = m.st.Seen(t.Context(), "alpha", g, "192.0.2.7", "alice", banNow)
	code, b := e.do("GET", "/api/v1/players/"+steamA+"/identities", e.token(RoleAdmin, nil, false), "")
	require.Equal(t, 200, code, "identities: %d %s", code, b)
	require.Contains(t, string(b), `"192.0.2.7"`, "identities: %d %s", code, b)
	require.Contains(t, string(b), `"shared_with":0`, "identities: %d %s", code, b)
	code, _ = e.do("GET", "/api/v1/players/123/identities", e.token(RoleModerator, nil, false), "")
	require.Equal(t, 400, code)
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"github.com/bzed/dayz-server-operator/internal/admin"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/apiclient"
)

// fakeInstances serves /api/v1/instances with the given JSON, or 500 while fail is set, and counts the calls.
func fakeInstances(t *testing.T, body string, fail *atomic.Bool, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/instances" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestNavIsCachedForTheTTL(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	ts := fakeInstances(t, `[{"instance":"a b","connected":true,"hello":{"world":"enoch"}}]`, &fail, &calls)
	s, err := New([]*Backend{{Name: "main", Client: apiclient.New(ts.URL, "x")}}, Options{})
	require.NoError(t, err)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	got := s.nav(t.Context())
	require.Len(t, got, 1)
	assert.Equal(t, []navInstance{{Name: "a b", World: "enoch", Connected: true}}, got[0].Instances)
	s.nav(t.Context())
	assert.EqualValues(t, 1, calls.Load(), "within the TTL the list is served from the cache")

	now = now.Add(navTTL + time.Second)
	s.nav(t.Context())
	assert.EqualValues(t, 2, calls.Load())
}

func TestNavKeepsTheLastListWhenABackendFails(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	ts := fakeInstances(t, `[{"instance":"alpha"}]`, &fail, &calls)
	s, err := New([]*Backend{{Name: "main", Client: apiclient.New(ts.URL, "x")}}, Options{})
	require.NoError(t, err)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	s.nav(t.Context())
	fail.Store(true)
	now = now.Add(navTTL + time.Second)
	got := s.nav(t.Context())
	require.Len(t, got, 1)
	require.Len(t, got[0].Instances, 1)
	assert.Equal(t, "alpha", got[0].Instances[0].Name)
	assert.NotEmpty(t, got[0].Err)
}

func TestEveryPageHasTheSwitcher(t *testing.T) {
	e := newEnv(t, "admin")
	for _, path := range []string{"/", "/b/main/i/alpha", "/b/main/i/alpha/map", "/b/main/i/alpha/events", "/b/main/bans", "/b/main/bans/new", "/b/main/audit"} {
		code, body, _ := e.get(path)
		require.Equal(t, http.StatusOK, code, path)
		assert.Contains(t, body, `<details class="switch"`, path)
		assert.Contains(t, body, `href="/b/main/i/alpha"`, path)
	}
	_, body, _ := e.get("/b/down/audit")
	assert.Contains(t, body, "unreachable")
}

func TestInstanceBarMarksTheActiveTab(t *testing.T) {
	e := newEnv(t, "admin")
	for path, tab := range map[string]string{"/b/main/i/alpha": "Overview", "/b/main/i/alpha/map": "Map", "/b/main/i/alpha/events": "Events"} {
		_, body, _ := e.get(path)
		assert.Equal(t, 1, strings.Count(body, `aria-current="page"><i`), path+": exactly one tab is current")
		assert.Regexp(t, `aria-current="page"><i[^>]*></i>`+tab+`</a>`, body, path)
	}
	_, body, _ := e.get("/b/main/bans")
	assert.NotContains(t, body, `class="instbar"`, "pages outside an instance have no instance bar")
}

func TestAuditLinksTheInstance(t *testing.T) {
	e := newEnv(t, "admin")
	require.NoError(t, e.api.Audit.Log(admin.AuditEntry{Time: time.Now(), Actor: "x", Source: "web", Instance: "alpha", Action: "kick", OK: true}))
	_, body, _ := e.get("/b/main/audit")
	assert.Contains(t, body, `<td><a href="/b/main/i/alpha">alpha</a></td>`)
}

func TestMapPageHasNoSecondLinkRow(t *testing.T) {
	e := newEnv(t, "admin")
	_, body, _ := e.get("/b/main/i/alpha/map")
	assert.NotContains(t, body, `class="maptitle"`)
	assert.Contains(t, body, `id="stats"`, "the live facts stay where map.js updates them")
}

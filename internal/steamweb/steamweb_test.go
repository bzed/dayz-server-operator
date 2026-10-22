// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package steamweb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type moderationInfo = moderation.SteamInfo

const testKey = "0123456789ABCDEF0123456789ABCDEF"

const (
	idBanned, idClean, idThird = "76561198000000001", "76561198000000002", "76561198000000003"
	idPublic                   = "76561198066209976"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

// steam is a fake Steam: routes maps a URL path to the file it answers with, or to a status.
type steam struct {
	*httptest.Server
	status map[string]int
	files  map[string]string
	seen   []string
}

func newSteam(t *testing.T, files map[string]string) *steam {
	t.Helper()
	s := &steam{status: map[string]int{}, files: files}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.seen = append(s.seen, r.URL.Path+"?"+r.URL.RawQuery)
		if code, ok := s.status[r.URL.Path]; ok {
			http.Error(w, "no", code)
			return
		}
		f, ok := s.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fixture(t, f))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *steam) client(key string) *Client {
	return &Client{Key: key, CommunityBase: s.URL, APIBase: s.URL, HTTP: s.Client(), UserAgent: "dzo/test"}
}

func TestCommunityPublicProfile(t *testing.T) {
	s := newSteam(t, map[string]string{"/profiles/" + idPublic: "public.xml"})
	in, err := s.client("").Community(t.Context(), idPublic)
	require.NoError(t, err)
	assert.Equal(t, "community", in.Source)
	assert.EqualValues(t, "ok", in.State)
	assert.Equal(t, "Smith", in.Persona)
	assert.Equal(t, "public", in.Privacy)
	assert.True(t, in.VACBanned)
	assert.False(t, in.Limited)
	assert.Equal(t, "None", in.TradeBan)
	assert.Equal(t, "June 29, 2012", in.MemberSince)
	assert.Equal(t, "Ukraine", in.Location)
	require.NotNil(t, in.DayZHours)
	assert.InDelta(t, 1532.4, *in.DayZHours, 0.01, "the hours of DayZ, app 221100, with a thousands comma")
	assert.Nil(t, in.VACBans, "the community page has no counts")
	assert.Contains(t, s.seen[0], "xml=1")
}

func TestCommunityPrivateProfile(t *testing.T) {
	s := newSteam(t, map[string]string{"/profiles/" + idBanned: "private.xml"})
	in, err := s.client("").Community(t.Context(), idBanned)
	require.NoError(t, err)
	assert.Equal(t, "private", in.Privacy)
	assert.True(t, in.VACBanned, "bans are public even on a private profile")
	assert.True(t, in.Limited)
	assert.Equal(t, "Banned", in.TradeBan)
	assert.Nil(t, in.DayZHours)
}

func TestCommunityNotFoundIsUnknown(t *testing.T) {
	s := newSteam(t, map[string]string{"/profiles/" + idThird: "notfound.xml"})
	in, err := s.client("").Community(t.Context(), idThird)
	require.NoError(t, err)
	assert.EqualValues(t, "unknown", in.State)
}

func TestCommunityBodyIsLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<profile>" + strings.Repeat("x", 1<<20) + "</profile>"))
	}))
	defer srv.Close()
	c := &Client{CommunityBase: srv.URL, HTTP: srv.Client()}
	_, err := c.Community(t.Context(), idPublic)
	require.Error(t, err)
}

func TestCommunityRefusesABadID(t *testing.T) {
	s := newSteam(t, nil)
	_, err := s.client("").Community(t.Context(), "../x")
	require.Error(t, err)
	assert.Empty(t, s.seen, "nothing is requested for a bad id")
}

func TestBansMergesBothEndpoints(t *testing.T) {
	s := newSteam(t, map[string]string{"/ISteamUser/GetPlayerBans/v1/": "bans.json", "/ISteamUser/GetPlayerSummaries/v0002/": "summaries.json"})
	got, err := s.client(testKey).Bans(t.Context(), []string{idBanned, idClean, idThird})
	require.NoError(t, err)
	require.Len(t, got, 2, "an id Steam does not know is absent")
	b := got[idBanned]
	assert.Equal(t, "api", b.Source)
	assert.True(t, b.VACBanned)
	assert.True(t, b.CommunityBanned)
	require.NotNil(t, b.VACBans)
	assert.Equal(t, 2, *b.VACBans)
	assert.Equal(t, 1, *b.GameBans)
	assert.Equal(t, 214, *b.DaysSinceBan)
	assert.Equal(t, "banned", b.EconomyBan)
	assert.Equal(t, "DE", b.Country)
	require.NotNil(t, b.Created)
	assert.Equal(t, time.Date(2012, 6, 29, 0, 0, 0, 0, time.UTC), b.Created.UTC())
	assert.Equal(t, "Hidden", b.Persona)
	assert.Equal(t, "private", b.Privacy)
	c := got[idClean]
	assert.False(t, c.VACBanned)
	assert.Equal(t, "public", c.Privacy)
	assert.Equal(t, 0, *c.VACBans)
	for _, u := range s.seen {
		assert.Contains(t, u, "key="+testKey, "the key goes to Steam")
		assert.Contains(t, u, "steamids="+idBanned+"%2C"+idClean+"%2C"+idThird)
	}
}

func TestBansNeedsAKey(t *testing.T) {
	s := newSteam(t, nil)
	_, err := s.client("").Bans(t.Context(), []string{idBanned})
	require.Error(t, err)
	assert.Empty(t, s.seen)
}

func TestBansTakesAtMost100(t *testing.T) {
	s := newSteam(t, nil)
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = idBanned
	}
	_, err := s.client(testKey).Bans(t.Context(), ids)
	require.Error(t, err)
}

func TestBansErrorHidesTheKey(t *testing.T) {
	s := newSteam(t, nil)
	s.status["/ISteamUser/GetPlayerBans/v1/"] = http.StatusForbidden
	_, err := s.client(testKey).Bans(t.Context(), []string{idBanned})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), testKey)
	assert.Contains(t, err.Error(), "GetPlayerBans")
	assert.Contains(t, err.Error(), "403")

	// a transport error carries the URL in net/http's error: it must not reach ours
	srv := s.client(testKey)
	srv.APIBase = "http://127.0.0.1:1"
	_, err = srv.Bans(t.Context(), []string{idBanned})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), testKey)
	assert.NotContains(t, err.Error(), "127.0.0.1:1/ISteamUser")
}

func TestDetailsFixtures(t *testing.T) {
	s := newSteam(t, map[string]string{
		"/IPlayerService/GetOwnedGames/v1/": "owned.json", "/IPlayerService/GetSteamLevel/v1/": "level.json", "/ISteamUser/GetFriendList/v0001/": "friends.json"})
	var in moderationInfo
	require.NoError(t, s.client(testKey).Details(t.Context(), idPublic, &in))
	require.NotNil(t, in.DayZHours)
	assert.InDelta(t, 532.4, *in.DayZHours, 0.001, "31944 minutes")
	require.NotNil(t, in.Level)
	assert.Equal(t, 55, *in.Level)
	assert.Equal(t, []string{idClean, idThird}, in.FriendIDs, "a value that is no Steam ID is dropped")
	joined := strings.Join(s.seen, " ")
	assert.Contains(t, joined, "appids_filter%5B0%5D=221100")
	assert.Contains(t, joined, "include_played_free_games=1")
}

func TestDetailsPrivateGamesAndFriends(t *testing.T) {
	s := newSteam(t, map[string]string{"/IPlayerService/GetOwnedGames/v1/": "empty.json", "/IPlayerService/GetSteamLevel/v1/": "level.json"})
	s.status["/ISteamUser/GetFriendList/v0001/"] = http.StatusUnauthorized
	var in moderationInfo
	require.NoError(t, s.client(testKey).Details(t.Context(), idPublic, &in), "a private part is not an error")
	assert.Nil(t, in.DayZHours)
	assert.Empty(t, in.FriendIDs)
	require.NotNil(t, in.Level)
}

func TestDetailsServerErrorIsAnError(t *testing.T) {
	s := newSteam(t, nil)
	s.status["/IPlayerService/GetOwnedGames/v1/"] = http.StatusInternalServerError
	var in moderationInfo
	require.Error(t, s.client(testKey).Details(t.Context(), idPublic, &in))
}

func TestDetailsCapsFriends(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"friendslist":{"friends":[`)
	for i := range 2500 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"steamid":"7656119` + strings.Repeat("0", 3) + pad(i) + `"}`)
	}
	b.WriteString(`]}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "GetFriendList") {
			_, _ = w.Write([]byte(b.String()))
			return
		}
		_, _ = w.Write([]byte(`{"response":{}}`))
	}))
	defer srv.Close()
	var in moderationInfo
	c := &Client{Key: testKey, APIBase: srv.URL, HTTP: srv.Client()}
	require.NoError(t, c.Details(t.Context(), idPublic, &in))
	assert.Len(t, in.FriendIDs, MaxFriends)
}

func pad(i int) string {
	n := []byte("0000000")
	for k := 6; k >= 0 && i > 0; k-- {
		n[k] = byte('0' + i%10)
		i /= 10
	}
	return string(n)
}

func TestReadKey(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.key")
	require.NoError(t, os.WriteFile(good, []byte(" "+testKey+"\n"), 0o600))
	k, err := ReadKey(good)
	require.NoError(t, err)
	assert.Equal(t, testKey, k)

	open := filepath.Join(dir, "open.key")
	require.NoError(t, os.WriteFile(open, []byte(testKey), 0o640))
	_, err = ReadKey(open)
	require.Error(t, err, "a group-readable key file is refused")
	assert.Contains(t, err.Error(), open, "the error names the file")
	assert.NotContains(t, err.Error(), testKey)

	bad := filepath.Join(dir, "bad.key")
	require.NoError(t, os.WriteFile(bad, []byte("not a key\n"), 0o600))
	_, err = ReadKey(bad)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not a key", "the content is not echoed")

	_, err = ReadKey(filepath.Join(dir, "missing"))
	require.Error(t, err)
}

func TestProfileURL(t *testing.T) {
	assert.Equal(t, "https://steamcommunity.com/profiles/"+idPublic, ProfileURL(idPublic))
	assert.Empty(t, ProfileURL("../x"))
	assert.Empty(t, ProfileURL(""))
}

func TestWaitIsCalledBeforeEveryRequest(t *testing.T) {
	s := newSteam(t, map[string]string{"/IPlayerService/GetOwnedGames/v1/": "owned.json", "/IPlayerService/GetSteamLevel/v1/": "level.json", "/ISteamUser/GetFriendList/v0001/": "friends.json"})
	c := s.client(testKey)
	waits := 0
	c.Wait = func(context.Context) error { waits++; return nil }
	var in moderationInfo
	require.NoError(t, c.Details(t.Context(), idPublic, &in))
	assert.Equal(t, 3, waits, "GetOwnedGames, GetSteamLevel and GetFriendList each wait")
	c.Wait = func(context.Context) error { return errors.New("stop") }
	require.Error(t, c.Details(t.Context(), idPublic, &in))
	assert.Len(t, s.seen, 3, "no request after a failed wait")
}

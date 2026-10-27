// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

var vNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func ip(n int) *int         { return &n }
func fp(f float64) *float64 { return &f }

func detail(mod func(*api.PlayerDetail)) api.PlayerDetail {
	d := api.PlayerDetail{}
	d.SteamID = "76561198000000001"
	if mod != nil {
		mod(&d)
	}
	return d
}

func lines(c verdictCell) []string {
	var out []string
	for _, l := range c.Lines {
		out = append(out, l.Text)
	}
	return out
}

func TestBattlEyeCell(t *testing.T) {
	asked := vNow.Add(-48 * time.Hour)
	for name, tc := range map[string]struct {
		mod  func(*api.PlayerDetail)
		want []string
		bad  bool
		foot string
	}{
		"not asked": {nil, []string{"Not asked yet"}, false, ""},
		"dayz banned": {func(d *api.PlayerDetail) {
			d.BE = []moderation.BEStatus{{Game: "dayz", State: "ok", Banned: true, Reason: "Global Ban #760490", CheckedAt: asked}, {Game: "arma3", State: "ok", CheckedAt: asked}, {Game: "arma2oa", State: "ok", CheckedAt: asked}}
		}, []string{"DayZ: Global Ban #760490", "Arma 3: not banned", "Arma 2: not banned"}, true, "asked 2d 0h ago"},
		"arma only": {func(d *api.PlayerDetail) {
			d.BE = []moderation.BEStatus{{Game: "dayz", State: "ok", CheckedAt: asked}, {Game: "arma3", State: "ok", Banned: true, CheckedAt: asked}}
		}, []string{"DayZ: not banned", "Arma 3: banned"}, true, "asked 2d 0h ago"},
		"no answer": {func(d *api.PlayerDetail) {
			d.BE = []moderation.BEStatus{{Game: "dayz", State: "unknown", CheckedAt: vNow.Add(-5 * time.Minute)}}
		}, []string{"DayZ: no answer"}, false, "asked 5m ago"},
	} {
		t.Run(name, func(t *testing.T) {
			c := verdicts(detail(tc.mod), "main", vNow)[0]
			assert.Equal(t, "BattlEye", c.Title)
			assert.Equal(t, tc.want, lines(c))
			assert.Equal(t, tc.bad, c.Bad)
			assert.Equal(t, tc.foot, c.Foot)
		})
	}
}

func TestSteamCell(t *testing.T) {
	checked := vNow.Add(-5 * time.Hour)
	for name, tc := range map[string]struct {
		mod  func(*api.PlayerDetail)
		want []string
		bad  bool
	}{
		"not asked": {nil, []string{"Not asked yet"}, false},
		"no answer": {func(d *api.PlayerDetail) { d.Steam = moderation.SteamInfo{State: "unknown", CheckedAt: checked} }, []string{"No answer from Steam"}, false},
		"keyless vac": {func(d *api.PlayerDetail) {
			d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked, Privacy: "public", VACBanned: true}
		}, []string{"VAC banned"}, true},
		"keyless none": {func(d *api.PlayerDetail) {
			d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked, Privacy: "public"}
		}, []string{"No bans"}, false},
		"key counts": {func(d *api.PlayerDetail) {
			d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked, Source: "api", VACBanned: true, VACBans: ip(2), GameBans: ip(1), DaysSinceBan: ip(214), EconomyBan: "none"}
		}, []string{"2 VAC bans, last 214 days ago", "1 game ban"}, true},
		"key community": {func(d *api.PlayerDetail) {
			d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked, Source: "api", CommunityBanned: true, VACBans: ip(0), GameBans: ip(0), EconomyBan: "banned"}
		}, []string{"Community ban", "Economy ban: banned"}, true},
		"key clean": {func(d *api.PlayerDetail) {
			d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked, Source: "api", VACBans: ip(0), GameBans: ip(0), DaysSinceBan: ip(0), EconomyBan: "none"}
		}, []string{"No bans"}, false},
		"private": {func(d *api.PlayerDetail) {
			d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked, Privacy: "private"}
		}, []string{"Private profile, no ban data"}, false},
		"private but VAC": {func(d *api.PlayerDetail) {
			d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked, Privacy: "private", VACBanned: true}
		}, []string{"VAC banned"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			c := verdicts(detail(tc.mod), "main", vNow)[1]
			assert.Equal(t, "Steam", c.Title)
			assert.Equal(t, tc.want, lines(c))
			assert.Equal(t, tc.bad, c.Bad)
		})
	}
	c := verdicts(detail(func(d *api.PlayerDetail) { d.Steam = moderation.SteamInfo{State: "ok", CheckedAt: checked} }), "main", vNow)[1]
	assert.Equal(t, "checked 5h 0m ago", c.Foot)
}

func TestHereCell(t *testing.T) {
	soon := vNow.Add(10 * 24 * time.Hour)
	past := vNow.Add(-20 * 24 * time.Hour)
	for name, tc := range map[string]struct {
		bans []moderation.Ban
		want []string
		bad  bool
		foot string
	}{
		"none":             {nil, []string{"No bans"}, false, ""},
		"permanent":        {[]moderation.Ban{{ID: 12, Scope: moderation.ScopeGlobal}}, []string{"Banned permanently"}, true, "#12, all instances"},
		"until":            {[]moderation.Ban{{ID: 7, ExpiresAt: &soon, Scope: moderation.ScopeInstances, Instances: []string{"alpha", "beta"}}}, []string{"Banned until 2026-11-01"}, true, "#7, alpha, beta"},
		"expired":          {[]moderation.Ban{{ID: 3, ExpiresAt: &past, Scope: moderation.ScopeGlobal}}, []string{"Ban expired 2026-10-02"}, false, "#3, all instances"},
		"revoked":          {[]moderation.Ban{{ID: 4, RevokedAt: &past, Scope: moderation.ScopeGlobal}}, []string{"Ban revoked 2026-10-02"}, false, "#4, all instances"},
		"active beats old": {[]moderation.Ban{{ID: 3, ExpiresAt: &past, Scope: moderation.ScopeGlobal}, {ID: 9, Scope: moderation.ScopeGlobal}}, []string{"Banned permanently"}, true, "#9, all instances"},
	} {
		t.Run(name, func(t *testing.T) {
			c := verdicts(detail(func(d *api.PlayerDetail) { d.Bans = tc.bans }), "main", vNow)[2]
			assert.Equal(t, "Here", c.Title)
			assert.Equal(t, tc.want, lines(c))
			assert.Equal(t, tc.bad, c.Bad)
			assert.Equal(t, tc.foot, c.Foot)
			if len(tc.bans) > 0 {
				assert.Contains(t, c.FootHref, "/b/main/bans/")
			}
		})
	}
}

func TestSteamFacts(t *testing.T) {
	assert.Empty(t, steamFacts(detail(nil)))
	created := time.Date(2012, 6, 29, 0, 0, 0, 0, time.UTC)
	d := detail(func(d *api.PlayerDetail) {
		d.Steam = moderation.SteamInfo{State: "ok", Persona: "fritz", Privacy: "public", Created: &created, Country: "DE", Level: ip(55), DayZHours: fp(532.4)}
	})
	assert.Equal(t, "“fritz”, public profile, since June 2012, DE, level 55, 532 h of DayZ", steamFacts(d))
	d = detail(func(d *api.PlayerDetail) {
		d.Steam = moderation.SteamInfo{State: "ok", Persona: "x", Privacy: "private", MemberSince: "June 29, 2012", Location: "Ukraine"}
	})
	assert.Equal(t, "“x”, private profile, since June 29, 2012, Ukraine", steamFacts(d))
}

// ---- pages ----

type fakeLookup struct {
	mu    sync.Mutex
	asked []string
	err   error
	set   func(id string)
}

func (f *fakeLookup) LookupNow(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, id)
	if f.set != nil {
		f.set(id)
	}
	return f.err
}

func playerSetup(t *testing.T, role api.Role) (*env, *moderation.Store, *fakeLookup) {
	t.Helper()
	e := newEnv(t, role)
	st := e.api.Moderation.Store()
	f := &fakeLookup{}
	e.api.Lookups = f
	return e, st, f
}

func seedPlayer(t *testing.T, st *moderation.Store, steam, name string, joined time.Time) {
	t.Helper()
	require.NoError(t, st.PlayerJoined(t.Context(), "alpha", steam, name, joined))
}

func TestPlayerPageShowsTheVerdictsAndEscapes(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "<b>Fritz</b>", time.Now().Add(-time.Hour))
	seedPlayer(t, st, webSteam, "xX_Fritz_Xx", time.Now().Add(-30*time.Minute))
	g, _ := moderation.DeriveGUID(webSteam)
	require.NoError(t, st.SaveBEStatus(t.Context(), g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "<script>x</script> Global Ban #1", CheckedAt: time.Now()}))
	require.NoError(t, st.SaveSteamInfo(t.Context(), webSteam, moderation.SteamInfo{State: moderation.LookupOK, CheckedAt: time.Now(), Persona: "<i>fritz</i>", Privacy: "public", VACBanned: true}))
	_, err := st.CreateBan(t.Context(), "x", moderation.BanInput{SteamID: webSteam, Reason: "aimbot", Scope: moderation.ScopeGlobal}, time.Now())
	require.NoError(t, err)

	code, body, _ := e.get("/b/main/players/" + webSteam)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "<script>x</script>")
	assert.NotContains(t, body, "<b>Fritz</b>")
	assert.NotContains(t, body, "<i>fritz</i>")
	assert.Contains(t, body, "&lt;script&gt;x&lt;/script&gt; Global Ban #1")
	assert.Contains(t, body, "DayZ: ")
	assert.Equal(t, 3, strings.Count(body, `<section class="verdict`), "three cells")
	assert.Equal(t, 3, strings.Count(body, `verdict bad`), "BattlEye, Steam and our ban all say banned")
	assert.Contains(t, body, "VAC banned")
	assert.Contains(t, body, "Banned permanently")
	assert.Contains(t, body, `href="https://steamcommunity.com/profiles/`+webSteam+`"`)
	assert.Contains(t, body, `rel="noopener noreferrer"`)
	assert.Contains(t, body, "xX_Fritz_Xx")
	assert.Contains(t, body, "also known as")
	assert.Contains(t, body, `href="/b/main/bans/new?steam_id=`+webSteam)
	assert.Contains(t, body, `hx-post="/b/main/players/`+webSteam+`/lookup"`)
}

func TestPlayerPageListsSightings(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "Fritz", time.Now())
	g, _ := moderation.DeriveGUID(webSteam)
	require.NoError(t, st.Seen(t.Context(), "alpha", g, "203.0.113.7", "Fritz", time.Now()))
	code, body, _ := e.get("/b/main/players/" + webSteam)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Seen with")
	assert.Contains(t, body, "Others behind it")
	assert.Contains(t, body, "</html>", "the page rendered to its end")
	assert.NotContains(t, body, "template error")
}

func TestPlayerListShowsTheTimeOfDay(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	at := time.Date(2026, 10, 3, 14, 25, 0, 0, time.UTC)
	seedPlayer(t, st, webSteam, "Fritz", at)
	require.NoError(t, st.PlayerLeft(t.Context(), "alpha", webSteam, at.Add(time.Hour)))
	_, body, _ := e.get("/b/main/players")
	assert.Contains(t, body, "<td>2026-10-03 15:25</td>", "last seen")
	assert.Contains(t, body, "<td>2026-10-03 14:25</td>", "first seen")
}

func TestPlayerPageOnlyBanCellsAreBad(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "Fritz", time.Now())
	_, body, _ := e.get("/b/main/players/" + webSteam)
	assert.Zero(t, strings.Count(body, "verdict bad"), "an unasked player has no red")
	assert.Contains(t, body, "Not asked yet")
	assert.Contains(t, body, "No bans")
}

func TestPlayerPageUnknownAndBadID(t *testing.T) {
	e, _, _ := playerSetup(t, api.RoleModerator)
	code, body, _ := e.get("/b/main/players/76561198000000099")
	assert.Equal(t, http.StatusNotFound, code)
	assert.Contains(t, body, "never connected")
	code, _, _ = e.get("/b/main/players/nonsense")
	assert.Equal(t, http.StatusBadRequest, code)
	code, _, _ = e.get("/b/nope/players/" + webSteam)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestPlayerPagesNeedTheRole(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleViewer)
	seedPlayer(t, st, webSteam, "Fritz", time.Now())
	for _, p := range []string{"/b/main/players", "/b/main/players/" + webSteam} {
		code, body, _ := e.get(p)
		assert.Equal(t, http.StatusForbidden, code, p)
		assert.Contains(t, body, "may not see the player database", p)
		assert.NotContains(t, body, "Fritz")
	}
}

func TestCheckNowPostsWithCSRFAndSwapsTheVerdictRow(t *testing.T) {
	e, st, f := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "Fritz", time.Now())
	g, _ := moderation.DeriveGUID(webSteam)
	f.set = func(string) {
		_ = st.SaveBEStatus(t.Context(), g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "Global Ban #7", CheckedAt: time.Now()})
	}
	req, _ := http.NewRequest(http.MethodPost, e.web.URL+"/b/main/players/"+webSteam+"/lookup", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "no CSRF token, no lookup")
	assert.Empty(t, f.asked)

	code, body := e.post("/b/main/players/"+webSteam+"/lookup", url.Values{}, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{webSteam}, f.asked)
	assert.Contains(t, body, "Global Ban #7", "the fresh verdicts come back as the fragment")
	assert.Contains(t, body, `id="verdicts"`)
	assert.NotContains(t, body, "<html", "a fragment, not a page")

	f.err = moderation.ErrLookupTooSoon
	code, body = e.post("/b/main/players/"+webSteam+"/lookup", url.Values{}, nil)
	assert.Equal(t, http.StatusOK, code, "htmx swaps only 2xx")
	assert.Contains(t, body, "looked up a moment ago")
	assert.Contains(t, body, `id="verdicts"`, "the verdict row stays")
}

func TestPlayerListPage(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "Fritz <b>", time.Now().Add(-time.Hour))
	seedPlayer(t, st, "76561198000000002", "Bob", time.Now().Add(-2*time.Hour))
	g, _ := moderation.DeriveGUID(webSteam)
	require.NoError(t, st.SaveBEStatus(t.Context(), g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "x", CheckedAt: time.Now()}))
	code, body, _ := e.get("/b/main/players")
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "Fritz <b>")
	assert.Contains(t, body, `href="/b/main/players/`+webSteam+`"`)
	assert.Contains(t, body, "Bob")
	assert.Contains(t, body, `title="BattlEye global ban"`, "a flagged player says why")
	_, body, _ = e.get("/b/main/players?flagged=1")
	assert.Contains(t, body, "Fritz")
	assert.NotContains(t, body, ">Bob<")
	_, body, _ = e.get("/b/main/players?q=bob")
	assert.NotContains(t, body, "Fritz")
	_, body, _ = e.get("/b/main/players?instance=nowhere")
	assert.Contains(t, body, "No players")
}

func TestPlayerPanelLinksTheProfile(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleAdmin)
	seedPlayer(t, st, webSteam, "Fritz", time.Now())
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "Fritz", Alive: true}}})
	g, _ := moderation.DeriveGUID(webSteam)
	require.NoError(t, st.SaveBEStatus(t.Context(), g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "Global Ban #1", CheckedAt: time.Now()}))
	_, body, _ := e.get("/b/main/i/alpha/players/" + webSteam + "/panel")
	assert.Contains(t, body, `href="/b/main/players/`+webSteam+`"`)
	assert.NotContains(t, body, `class="profile" disabled`)
	assert.Contains(t, body, "Global Ban #1", "a moderator sees the verdicts before acting")
}

func TestPlayerPanelWithoutTheRoleHasNoVerdicts(t *testing.T) {
	e := newEnv(t, api.RoleViewer)
	_, body, _ := e.get("/b/main/i/alpha/players/7656/panel")
	assert.NotContains(t, body, "verdicts")
	assert.NotContains(t, body, `href="/b/main/players/`)
}

func TestFlaggedDotInThePlayerRow(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "Fritz", time.Now())
	e.hub.Sync(admin.SyncRequest{Proto: 1, Players: []admin.Player{{SteamID: webSteam, Name: "Fritz", Alive: true}, {SteamID: "7656", Name: "<b>alice</b>", Alive: true}}})
	g, _ := moderation.DeriveGUID(webSteam)
	require.NoError(t, st.SaveBEStatus(t.Context(), g, moderation.BEStatus{Game: moderation.BEDayZ, State: moderation.LookupOK, Banned: true, Reason: "x", CheckedAt: time.Now()}))
	_, body, _ := e.get("/b/main/i/alpha/players")
	assert.Equal(t, 1, strings.Count(body, `class="flagged"`), "only the banned player")
	assert.Contains(t, body, `title="BattlEye global ban"`)
}

func TestBansLinkSteamTargetsToThePlayer(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	_, err := st.CreateBan(t.Context(), "x", moderation.BanInput{SteamID: webSteam, Reason: "r", Scope: moderation.ScopeGlobal}, time.Now())
	require.NoError(t, err)
	_, body, _ := e.get("/b/main/bans")
	assert.Contains(t, body, `<a href="/b/main/players/`+webSteam+`"><code>`+webSteam+`</code></a>`)
}

func TestHeaderLinksPlayers(t *testing.T) {
	e := newEnv(t, api.RoleModerator)
	_, body, _ := e.get("/")
	assert.Contains(t, body, `href="/b/main/players"`)
}

func TestPlayerListFiltersOnline(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "Online Olga", time.Now().Add(-time.Hour))
	seedPlayer(t, st, "76561198000000002", "Gone Gus", time.Now().Add(-2*time.Hour))
	require.NoError(t, st.PlayerLeft(t.Context(), "alpha", "76561198000000002", time.Now().Add(-time.Hour)))

	_, body, _ := e.get("/b/main/players")
	assert.Contains(t, body, "Gone Gus")
	assert.Contains(t, body, `name="online" value="1"> Only online players`)
	_, body, _ = e.get("/b/main/players?online=1&sort=name")
	assert.Contains(t, body, "Online Olga")
	assert.NotContains(t, body, "Gone Gus")
	assert.Contains(t, body, `name="online" value="1" checked>`, "the box stays ticked")
	assert.Contains(t, body, `name="sort" value="name"`, "the sort survives a new search")
	assert.Contains(t, body, `href="/b/main/players?dir=desc&amp;online=1&amp;sort=name"`, "the headers keep the filter")
}

func TestPlayerListHeadersSort(t *testing.T) {
	e, st, _ := playerSetup(t, api.RoleModerator)
	seedPlayer(t, st, webSteam, "zed", time.Now().Add(-time.Hour))
	seedPlayer(t, st, "76561198000000002", "Anna", time.Now().Add(-30*time.Minute))
	idx := func(body, s string) int { return strings.Index(body, s) }

	_, body, _ := e.get("/b/main/players")
	assert.Contains(t, body, `aria-sort="descending"><a class="sort" href="/b/main/players?sort=last"`, "last seen newest first by default; a click reverses it")
	assert.Less(t, idx(body, ">Anna<"), idx(body, ">zed<"), "newest first")
	assert.Contains(t, body, `href="/b/main/players?sort=name"`, "the name column sorts A to Z first")
	assert.Contains(t, body, `href="/b/main/players?dir=desc&amp;sort=be"`, "a flag column sorts the flagged first")

	_, body, _ = e.get("/b/main/players?sort=name")
	assert.Less(t, idx(body, ">Anna<"), idx(body, ">zed<"), "A to Z")
	assert.Contains(t, body, `aria-sort="ascending"><a class="sort" href="/b/main/players?dir=desc&amp;sort=name"`)

	_, body, _ = e.get("/b/main/players?sort=name&dir=desc")
	assert.Less(t, idx(body, ">zed<"), idx(body, ">Anna<"), "Z to A")

	_, body, _ = e.get("/b/main/players?sort=bogus")
	assert.Contains(t, body, `aria-sort="descending"><a class="sort" href="/b/main/players?sort=last"`, "an unknown column falls back to the default")
}

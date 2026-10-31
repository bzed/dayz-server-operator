// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e && live

package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// The live tier: the browser against a real `dzo serve` and a real DayZ server, started by
// scripts/test-client.sh (make test-client). The `live` tag is an explicit opt-in like `e2e`: without
// DZO_LIVE_API the tests fail instead of skipping.
//
//	DZO_LIVE_API         base URL of the API of the running dzo serve
//	DZO_LIVE_TOKEN_FILE  a web token (dzo token create --web, role admin)
//	DZO_LIVE_DB          the database of that dzo serve, where the test logs the admin 76561198000000077 in
//	DZO_LIVE_INSTANCE    the instance, t1
//	DZO_LIVE_PLAYER      the name of the headless client's player; empty without a client

type liveEnv struct {
	web      *httptest.Server
	instance string
	player   string
}

func newLiveEnv(t *testing.T) *liveEnv {
	t.Helper()
	api := os.Getenv("DZO_LIVE_API")
	require.NotEmpty(t, api, "DZO_LIVE_API: the live tests run from scripts/test-client.sh")
	token, err := os.ReadFile(os.Getenv("DZO_LIVE_TOKEN_FILE"))
	require.NoError(t, err, "DZO_LIVE_TOKEN_FILE")
	instance := os.Getenv("DZO_LIVE_INSTANCE")
	require.NotEmpty(t, instance, "DZO_LIVE_INSTANCE")
	assets := t.TempDir()
	useRealAssets(t, assets)
	// the pages need a login: make a session for the admin the script granted, in the database dzo serve uses
	st, err := moderation.Open(t.Context(), os.Getenv("DZO_LIVE_DB"))
	require.NoError(t, err, "DZO_LIVE_DB")
	t.Cleanup(func() { _ = st.Close() })
	session, hash, err := auth.NewSecret("dzs_")
	require.NoError(t, err)
	require.NoError(t, st.CreateSession(t.Context(), hash, "76561198000000077", "", time.Now()))
	srv, err := New(&Backend{Name: "live", Client: apiclient.New(api, strings.TrimSpace(string(token)))}, Options{Assets: assets})
	require.NoError(t, err)
	ws := httptest.NewServer(withSession(srv.Handler(), session))
	t.Cleanup(ws.Close)
	return &liveEnv{web: ws, instance: instance, player: os.Getenv("DZO_LIVE_PLAYER")}
}

func (l *liveEnv) url(path string) string { return l.web.URL + "/b/live/i/" + l.instance + path }

// markFirstTestCar counts the rows of the vehicle table that are one of the four bare cars server_tests
// spawns near x 7400, and marks the Actions button of the first one for a click.
const markFirstTestCar = `(function () {
  var re = /^(Offroad_02|Hatchback_02|CivilianSedan|OffroadHatchback)\b/;
  var n = 0, first = null;
  document.querySelectorAll('table tbody tr').forEach(function (tr) {
    if (tr.cells.length < 8) { return; }
    var x = parseFloat(tr.cells[2].textContent);
    if (re.test(tr.cells[0].textContent.trim()) && x > 7390 && x < 7440) {
      n++;
      if (!first) { first = tr.querySelector('button[data-rowpanel]'); }
    }
  });
  if (first) { first.setAttribute('data-e2e-car', '1'); }
  return n;
})()`

func TestLiveVehiclesAndMap(t *testing.T) {
	l := newLiveEnv(t)
	b := newBrowserWithin(t, 3*time.Minute)
	b.run(t, chromedp.Navigate(l.url("")), chromedp.WaitReady("body"))

	// server_tests spawns four bare cars there; with a client the earlier checks of the script have used up all
	// but one of them by the time this runs.
	want := 4
	if l.player != "" {
		want = 1
	}
	var cars int
	require.Eventually(t, func() bool {
		_ = chromedp.Run(b.ctx, chromedp.Evaluate(markFirstTestCar, &cars))
		return cars >= want
	}, 25*time.Second, 500*time.Millisecond, "the bare cars near x 7400 are listed (found %d, want %d)", cars, want)

	b.clickUntil(t, `button[data-e2e-car]`, func() bool { return b.count("tr.rowpanel") == 1 })
	b.clickUntil(t, `tr.rowpanel button[hx-vals*="missing"]`, func() bool { return b.textOf("#panel-flash") != "" })
	require.Equal(t, 1, b.count("#panel-flash .flash.ok"), "the repair succeeds: %s", b.textOf("#panel-flash"))
	require.NotEmpty(t, strings.TrimSpace(b.textOf("#panel-flash")), "with the result text of the real mod")

	b.run(t, chromedp.Navigate(l.url("/map")), chromedp.WaitVisible(`.leaflet-container`))
	require.Eventually(t, func() bool {
		return strings.Contains(b.textOf(".leaflet-control-layers-overlays"), "Police cars") && b.count(".mi-wrap") >= 1
	}, 25*time.Second, 500*time.Millisecond, "the Police cars layer has markers")
}

func TestLivePlayer(t *testing.T) {
	l := newLiveEnv(t)
	if l.player == "" {
		t.Skip("DZO_LIVE_PLAYER is empty: test-client.sh ran with --no-client")
	}
	b := newBrowserWithin(t, 3*time.Minute)
	b.run(t, chromedp.Navigate(l.url("")), chromedp.WaitReady("body"))
	// the table comes in by htmx polling
	require.Eventually(t, func() bool { return strings.Contains(b.textOf("td.who"), l.player) }, 25*time.Second, 500*time.Millisecond, "the player row appears")

	b.clickUntil(t, `td.who button[data-info]`, func() bool { return b.count("td.who.open") == 1 })
	require.Eventually(t, func() bool { return b.count("td.who .pop dl") == 1 }, 10*time.Second, 100*time.Millisecond, "the info popup shows the connection data: %s", b.textOf("td.who .pop"))

	b.clickUntil(t, `button[data-rowpanel]`, func() bool { return b.count("tr.rowpanel") == 1 })
	b.run(t, chromedp.SendKeys(`.playerpanel form[hx-post$="/message"] input[name=text]`, "live test"))
	b.run(t, chromedp.Click(`.playerpanel form[hx-post$="/message"] button`))
	require.Eventually(t, func() bool { return b.textOf("#panel-flash") != "" }, 15*time.Second, 100*time.Millisecond)
	require.Equal(t, 1, b.count("#panel-flash .flash.ok"), "the message is delivered: %s", b.textOf("#panel-flash"))

	b.run(t, chromedp.Navigate(l.url("/map")), chromedp.WaitVisible(`.leaflet-container`))
	require.Eventually(t, func() bool { return strings.Contains(b.textOf(".pm-wrap .pm-l"), l.player) }, 25*time.Second, 500*time.Millisecond, "the player has a marker on the map")
}

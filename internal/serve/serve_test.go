// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/notify"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const instanceYAML = `name: %s
product: dayz-stable
map: dayzOffline.chernarusplus
mission_source: {git: https://example.invalid/m.git, ref: main, path: mpmissions/x}
ports: {game: %d, rcon: %d, query: %d}
network: %s
mods:
  - {local: dzo-admin, server: true}
admin: {deny_classes: ["Land_*"], vehicles_s: 30}
admin_map:
  watch:
    - {layer: fire, classes: [FireplaceBase], icon: fire}
`

func fixture(t testing.TB) (*config.Config, *site.Tree) {
	t.Helper()
	data := t.TempDir()
	siteDir := filepath.Join(data, "site")
	for i, n := range []struct{ name, network string }{{"alpha", "host"}, {"beta", "publish"}} {
		dir := filepath.Join(siteDir, "instances", n.name)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		y := fmt.Sprintf(instanceYAML, n.name, 2302+10*i, 2303+10*i, 27016+10*i, n.network)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "instance.yaml"), []byte(y), 0o600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(siteDir, "localmods", "dzo-admin", "addons"), 0o750))
	cfg := config.Default()
	cfg.Paths = config.Paths{Data: data, Instances: data + "/instances", Snapshots: data + "/snapshots", Cache: data + "/cache", Secrets: data + "/secrets", DB: data + "/db", Site: siteDir}
	cfg.Serve.Listen, cfg.Serve.ModListen = "127.0.0.1:0", "127.0.0.1:0"
	tree, err := site.LoadTree(siteDir)
	require.NoError(t, err)
	return cfg, tree
}

func TestModConfigAndEndpoint(t *testing.T) {
	cfg, tree := fixture(t)
	insts, err := resolve.ResolveAll(cfg, tree)
	require.NoError(t, err)
	require.True(t, insts[0].AdminEnabled(), "dzo-admin in mods must enable the integration")
	cfg.Serve.ModListen = "127.0.0.1:2400"
	alpha, _ := ModEndpoint(cfg, insts[0])
	beta, _ := ModEndpoint(cfg, insts[1])
	require.Equal(t, "http://127.0.0.1:2400/", alpha)
	require.Equal(t, "http://host.containers.internal:2400/", beta)
	mc, err := ModConfig(cfg, insts[0])
	require.NoError(t, err)
	require.True(t, mc.AllowSpawn, "mod config %+v", mc)
	require.Equal(t, 200, mc.MoveOccupiedMax, "unset = 200 m")
	zero := 0
	insts[0].Admin.MoveOccupiedMax = &zero
	mc, err = ModConfig(cfg, insts[0])
	require.NoError(t, err)
	require.Equal(t, 0, mc.MoveOccupiedMax, "0 refuses moves with players inside")
	insts[0].Admin.MoveOccupiedMax = nil
	mc, err = ModConfig(cfg, insts[0])
	require.NoError(t, err)
	require.Equal(t, 30, mc.VehiclesS)
	require.Equal(t, "Land_*", mc.DenyClasses[0])
	require.Len(t, mc.Watch, len(admin.VanillaEventWatch())+1)
	require.Equal(t, "fire", mc.Watch[len(mc.Watch)-1].Layer)
	// The events of the Central Economy can be switched off for an instance.
	off := false
	insts[0].AdminMap.VanillaEvents = &off
	mc, err = ModConfig(cfg, insts[0])
	require.NoError(t, err)
	require.Len(t, mc.Watch, 1, "without the vanilla events")
	insts[0].AdminMap.VanillaEvents = nil
	require.NoError(t, WriteModConfig(cfg, insts[0]))
	require.FileExists(t, filepath.Join(TokenDir(cfg), "alpha.token"))
	require.FileExists(t, filepath.Join(insts[0].Paths.Profiles, "dzo", "admin", "config.json"))
	cfg.Serve.ModListen = "nonsense"
	_, err = ModEndpoint(cfg, insts[0])
	require.Error(t, err, "bad mod_listen must fail")
	require.Error(t, WriteModConfig(cfg, insts[0]), "bad mod_listen must fail the config write")
	insts[0].Admin.Disabled = true
	require.False(t, insts[0].AdminEnabled(), "admin.disabled")
}

func TestRunEndToEnd(t *testing.T) {
	cfg, tree := fixture(t)
	insts, _ := resolve.ResolveAll(cfg, tree)
	require.NoError(t, WriteModConfig(cfg, insts[0]))
	secret, _, err := api.NewTokenStore(APITokens(cfg)).Create("op", api.RoleOperator, nil, false, false, time.Hour)
	require.NoError(t, err)
	tokenB, _ := os.ReadFile(filepath.Join(TokenDir(cfg), "alpha.token"))

	reload := make(chan struct{})
	ready := make(chan [2]string, 1)
	loads := 0
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{
			Version: "test", Reload: reload,
			Load: func() (*site.Tree, error) {
				loads++
				if loads == 3 {
					return nil, os.ErrInvalid
				}
				return tree, nil
			},
			Ready: func(a, m string) { ready <- [2]string{a, m} },
			Logf:  t.Logf,
		})
	}()
	addrs := <-ready

	// The mod syncs against the mod listener with the rendered token.
	body := `{"token":"` + strings.TrimSpace(string(tokenB)) + `","protocol":1,"hello":true,"players":[{"steam_id":"7656"}]}`
	resp, err := http.Post("http://"+addrs[1]+"/mod/v1/sync", "application/json", strings.NewReader(body))
	require.NoError(t, err, "mod sync")
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "mod sync")

	// And an operator sees it through the API.
	c := apiclient.New("http://"+addrs[0], secret)
	players, err := c.Players(t.Context(), "alpha")
	require.NoError(t, err)
	require.Len(t, players, 1)

	reload <- struct{}{} // ok
	reload <- struct{}{} // load fails: keeps running
	reload <- struct{}{}
	_, err = c.Players(t.Context(), "alpha")
	require.NoError(t, err, "after reloads")
	got, _ := c.Players(t.Context(), "alpha")
	require.Len(t, got, 1, "a reload must keep the world state")
	cancel()
	require.NoError(t, <-done)
	_ = admin.ProtocolVersion
}

// selfSigned writes a certificate for 127.0.0.1 and its key under dir and returns the paths.
func selfSigned(t *testing.T, dir string) (cert, key string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "dzo test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	require.NoError(t, err)
	kb, err := x509.MarshalECPrivateKey(k)
	require.NoError(t, err)
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600))
	return cert, key
}

func TestServeAPIOverTLS(t *testing.T) {
	cfg, tree := fixture(t)
	certFile, keyFile := selfSigned(t, t.TempDir())
	cfg.Serve.TLS = config.TLS{CertFile: certFile, KeyFile: keyFile}
	secret, _, err := api.NewTokenStore(APITokens(cfg)).Create("op", api.RoleOperator, nil, false, false, time.Hour)
	require.NoError(t, err)

	ready := make(chan [2]string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{Version: "test", Load: func() (*site.Tree, error) { return tree, nil },
			Ready: func(a, m string) { ready <- [2]string{a, m} }, Logf: t.Logf})
	}()
	addrs := <-ready

	pemBytes, err := os.ReadFile(certFile)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(pemBytes))
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	resp, err := hc.Get("https://" + addrs[0] + "/healthz")
	require.NoError(t, err, "the API listener speaks HTTPS")
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	c := apiclient.NewTLS("https://"+addrs[0], secret, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
	_, err = c.Instances(t.Context())
	require.NoError(t, err, "an API client with the CA")

	// the mod listener stays plain HTTP
	resp, err = http.Post("http://"+addrs[1]+"/mod/v1/sync", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	_ = resp.Body.Close()

	cancel()
	require.NoError(t, <-done)
}

func TestRunWebTokensNeedLogins(t *testing.T) {
	for _, tc := range []struct {
		webURL string
		code   int
		msg    string
	}{{"", http.StatusServiceUnavailable, "serve.web_url"}, {"https://dzo.example", http.StatusForbidden, "user session"}} {
		cfg, tree := fixture(t)
		cfg.Serve.WebURL = tc.webURL
		secret, _, err := api.NewTokenStore(APITokens(cfg)).Create("web", api.RoleAdmin, nil, false, true, time.Hour)
		require.NoError(t, err)
		ready := make(chan [2]string, 1)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, cfg, Options{Version: "test", Load: func() (*site.Tree, error) { return tree, nil },
				Ready: func(a, m string) { ready <- [2]string{a, m} }, Logf: t.Logf})
		}()
		addrs := <-ready
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addrs[0]+"/api/v1", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body := new(strings.Builder)
		_, _ = io.Copy(body, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, tc.code, resp.StatusCode, "web_url %q: %s", tc.webURL, body)
		require.Contains(t, body.String(), tc.msg)
		cancel()
		require.NoError(t, <-done)
	}
}

func TestRunErrors(t *testing.T) {
	cfg, tree := fixture(t)
	load := func() (*site.Tree, error) { return tree, nil }
	require.Error(t, Run(t.Context(), cfg, Options{Load: func() (*site.Tree, error) { return nil, os.ErrInvalid }}), "load error must fail Run")
	cfg.Serve.Listen = "bad"
	require.Error(t, Run(t.Context(), cfg, Options{Load: load}), "bad api listen")
	cfg.Serve.Listen, cfg.Serve.ModListen = "127.0.0.1:0", "bad"
	require.Error(t, Run(t.Context(), cfg, Options{Load: load}), "bad mod listen")
}

// The shipped event rules reach the hub as they reach the mod: the mod marks every watch marker
// admin-only, the rule's visibility and colour are what lets the operator see an event.
func TestAddHubsStylesTheVanillaEvents(t *testing.T) {
	cfg, tree := fixture(t)
	reg := admin.NewRegistry(TokenDir(cfg))
	require.NoError(t, AddHubs(reg, cfg, tree))
	h, ok := reg.Hub("alpha")
	require.True(t, ok, "no hub for alpha")
	h.Sync(admin.SyncRequest{Has: []string{"markers"}, Markers: []admin.Marker{
		{ID: "w1", Layer: "Police cars", Shape: "point", Icon: "car", Source: "watch", Visibility: "admin"},
		{ID: "w2", Layer: "fire", Shape: "point", Icon: "fire", Source: "watch", Visibility: "admin"},
	}})
	got := map[string]admin.Marker{}
	for _, m := range h.Markers("") {
		got[m.Layer] = m
	}
	m := got["Police cars"]
	assert.Equal(t, "viewer", m.Visibility, "event marker visibility")
	assert.NotEmpty(t, m.Color, "event marker has the colour of the rule")
	assert.Equal(t, "admin", got["fire"].Visibility, "the instance's own rule without a visibility stays admin-only")
}

// The classes of a vehicle kind are the shipped ones plus the ones the instance lists.
func TestAddHubsVehicleKinds(t *testing.T) {
	cfg, tree := fixture(t)
	reg := admin.NewRegistry(TokenDir(cfg))
	require.NoError(t, AddHubs(reg, cfg, tree))
	h, _ := reg.Hub("alpha")
	h.Sync(admin.SyncRequest{Has: []string{"vehicles"}, Vehicles: []admin.Vehicle{{ID: "1", Kind: "car", Type: "Old_Bike_Red"}, {ID: "2", Kind: "car", Type: "TandemBike"}, {ID: "3", Kind: "car", Type: "ExpansionMh6"}}})
	kinds := map[string]string{}
	for _, v := range h.Vehicles() {
		kinds[v.ID] = v.Kind
	}
	require.Equal(t, map[string]string{"1": "bicycle", "2": "car", "3": "helicopter"}, kinds, "shipped classes")
	insts, err := resolve.ResolveAll(cfg, tree)
	require.NoError(t, err)
	insts[0].Admin.VehicleKinds = map[string][]string{"bicycle": {"TandemBike"}}
	got := vehicleKinds(insts[0])
	require.Contains(t, got["bicycle"], "TandemBike")
	require.Contains(t, got["bicycle"], "Old_Bike*")
	require.Contains(t, got["helicopter"], "RFMosquito")
}

func TestBanInstancesNeedTheRConPassword(t *testing.T) {
	cfg, tree := fixture(t)
	insts, err := BanInstances(cfg, tree, t.Logf)
	require.NoError(t, err)
	require.Empty(t, insts, "without a password file")
	pwFile := filepath.Join(cfg.Paths.Secrets, "rcon", "alpha")
	require.NoError(t, os.MkdirAll(filepath.Dir(pwFile), 0o700))
	require.NoError(t, os.WriteFile(pwFile, []byte("secret\n"), 0o600))
	insts, err = BanInstances(cfg, tree, t.Logf)
	require.NoError(t, err)
	require.Len(t, insts, 1, "with a password file")
	assert.Equal(t, "alpha", insts[0].Name)
	assert.Equal(t, "secret", insts[0].RConPass)
	assert.Equal(t, filepath.Join(cfg.Paths.Instances, "alpha", "profiles", "battleye"), insts[0].BEDir)
}

func TestAddHubsFeedLog(t *testing.T) {
	cfg, tree := fixture(t)
	reg := admin.NewRegistry(TokenDir(cfg))
	require.NoError(t, AddHubs(reg, cfg, tree))
	h, _ := reg.Hub("alpha")
	h.AddFeed(admin.FeedEvent{Kind: admin.FeedChat, Text: "hi"})
	path := FeedLogPath(cfg, "alpha")
	require.Equal(t, filepath.Join(cfg.Paths.DB, "feed", "alpha.jsonl"), path)
	require.FileExists(t, path)

	// A reload keeps the hub, a fresh registry (restart) loads the log.
	require.NoError(t, AddHubs(reg, cfg, tree))
	require.Len(t, h.Feed(0, 0), 1)
	fresh := admin.NewRegistry(TokenDir(cfg))
	require.NoError(t, AddHubs(fresh, cfg, tree))
	h2, _ := fresh.Hub("alpha")
	got := h2.Feed(0, 0)
	require.Len(t, got, 1)
	require.Equal(t, "hi", got[0].Text)
}

func TestRunKillfeedPosts(t *testing.T) {
	cfg, tree := fixture(t)
	cfg.Notify.Discord = []config.DiscordWebhook{{Name: "pvp", URL: "https://example.invalid/hook"}}
	alpha := tree.Instances["alpha"]
	alpha.Killfeed = site.KillfeedConfig{Discord: []string{"pvp"}}
	tree.Instances["alpha"] = alpha
	insts, _ := resolve.ResolveAll(cfg, tree)
	require.NoError(t, WriteModConfig(cfg, insts[0]))
	tokenB, _ := os.ReadFile(filepath.Join(TokenDir(cfg), "alpha.token"))

	posted := make(chan notify.Event, 4)
	ready := make(chan [2]string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{
			Version: "test", Load: func() (*site.Tree, error) { return tree, nil },
			Ready:  func(a, m string) { ready <- [2]string{a, m} },
			Logf:   t.Logf,
			Notify: func(_ context.Context, ev notify.Event) error { posted <- ev; return nil },
		})
	}()
	addrs := <-ready
	body := `{"token":"` + strings.TrimSpace(string(tokenB)) + `","protocol":1,"hello":true,"has":["feed"],` +
		`"feed":[{"seq":1,"kind":"kill","name":"Alice","target_name":"Bob","weapon":"M4-A1","distance":123.4}]}`
	resp, err := http.Post("http://"+addrs[1]+"/mod/v1/sync", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	select {
	case ev := <-posted:
		require.Equal(t, notify.KindKill, ev.Kind)
		require.Equal(t, "alpha", ev.Instance)
		require.Equal(t, "💀 **Alice** killed **Bob** with M4-A1 (123 m)", ev.Data["Lines"])
		require.Equal(t, []string{"pvp"}, *ev.Targets)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the kill was not posted")
	}
	cancel()
	require.NoError(t, <-done)
}

func TestRunRecordsVehicles(t *testing.T) {
	cfg, tree := fixture(t)
	insts, _ := resolve.ResolveAll(cfg, tree)
	require.NoError(t, WriteModConfig(cfg, insts[0]))
	tokenB, _ := os.ReadFile(filepath.Join(TokenDir(cfg), "alpha.token"))
	ready := make(chan [2]string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{Version: "test", Load: func() (*site.Tree, error) { return tree, nil },
			Ready: func(a, m string) { ready <- [2]string{a, m} }, Logf: t.Logf})
	}()
	addrs := <-ready
	body := `{"token":"` + strings.TrimSpace(string(tokenB)) + `","protocol":1,"hello":true,"features":["driver"],"has":["vehicles"],` +
		`"vehicles":[{"id":"v1","type":"Offroad_02","driver":"76561198000000001","driver_name":"Alice","driven_ago":60}]}`
	resp, err := http.Post("http://"+addrs[1]+"/mod/v1/sync", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Eventually(t, func() bool {
		st, err := moderation.OpenReadOnly(t.Context(), BanDB(cfg))
		if err != nil {
			return false
		}
		defer func() { _ = st.Close() }()
		recs, err := st.VehicleRecords(t.Context(), "alpha")
		r, ok := recs["v1"]
		return err == nil && ok && r.Driver == "76561198000000001" && r.DriverName == "Alice" && !r.DrivenAt.IsZero()
	}, 5*time.Second, 20*time.Millisecond, "the vehicle is recorded with its last driver")
	cancel()
	require.NoError(t, <-done)
}

func TestRunRecordsPlayerHistory(t *testing.T) {
	cfg, tree := fixture(t)
	insts, _ := resolve.ResolveAll(cfg, tree)
	require.NoError(t, WriteModConfig(cfg, insts[0]))
	tokenB, _ := os.ReadFile(filepath.Join(TokenDir(cfg), "alpha.token"))
	ready := make(chan [2]string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{Version: "test", Load: func() (*site.Tree, error) { return tree, nil },
			Ready: func(a, m string) { ready <- [2]string{a, m} }, Logf: t.Logf})
	}()
	addrs := <-ready
	body := `{"token":"` + strings.TrimSpace(string(tokenB)) + `","protocol":1,"hello":true,"players":[{"steam_id":"76561198000000001","name":"Alice"}]}`
	resp, err := http.Post("http://"+addrs[1]+"/mod/v1/sync", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Eventually(t, func() bool {
		st, err := moderation.OpenReadOnly(t.Context(), BanDB(cfg))
		if err != nil {
			return false
		}
		defer func() { _ = st.Close() }()
		p, err := st.Player(t.Context(), "76561198000000001")
		return err == nil && p.Online && p.Name == "Alice" && p.Instances[0] == "alpha"
	}, 5*time.Second, 20*time.Millisecond, "the player is in the history while online")
	cancel()
	require.NoError(t, <-done)
	st, err := moderation.OpenReadOnly(t.Context(), BanDB(cfg))
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	p, err := st.Player(t.Context(), "76561198000000001")
	require.NoError(t, err)
	require.False(t, p.Online, "stopping dzo serve ends the session")
}

func TestAddHubsDropsRemovedInstances(t *testing.T) {
	cfg, tree := fixture(t)
	reg := admin.NewRegistry(TokenDir(cfg))
	require.NoError(t, AddHubs(reg, cfg, tree))
	require.Equal(t, []string{"alpha", "beta"}, reg.Names())
	delete(tree.Instances, "beta")
	require.NoError(t, AddHubs(reg, cfg, tree))
	require.Equal(t, []string{"alpha"}, reg.Names(), "a reload without beta drops its hub")
}

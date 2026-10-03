// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package exporter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/a2s"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/monitor"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/steam"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	t    *testing.T
	data string
	cfg  *config.Config
	c    *Collector

	unit, health string
	unitErr      error
	query        func() (a2s.InfoResponse, error)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, data: t.TempDir()}
	f.unit = "ActiveState=active\nSubState=running\nActiveEnterTimestampMonotonic=1000000\nNRestarts=2\n"
	f.health = "healthy"
	f.query = func() (a2s.InfoResponse, error) { return a2s.InfoResponse{Players: 3, MaxPlayers: 60}, nil }
	cfg, err := config.Parse([]byte("paths:\n  data: " + f.data + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	f.cfg = cfg
	write(t, filepath.Join(f.data, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
`)
	write(t, filepath.Join(f.data, "cache", "products", "dayz-stable", "7", "DayZServer"), "bin")
	if err := os.Symlink("7", filepath.Join(f.data, "cache", "products", "dayz-stable", "current")); err != nil {
		t.Fatal(err)
	}
	f.c = &Collector{
		Cfg: cfg, Version: "v-test", Load: func() (*site.Tree, error) { return site.LoadTree(cfg.Paths.Site) },
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "systemctl":
				return []byte(f.unit), f.unitErr
			case "podman":
				if f.health == "" {
					return nil, errors.New("no such container")
				}
				return []byte(f.health + "\n"), nil
			}
			return nil, errors.New("unexpected " + name)
		},
		Query:      func(string, time.Duration) (a2s.InfoResponse, error) { return f.query() },
		BootUptime: func() (float64, error) { return 61, nil },
		Now:        time.Now,
	}
	return f
}

func (f *fixture) inst() monitor.InstanceStatus {
	f.t.Helper()
	snap, err := f.c.Snapshot()
	if err != nil || len(snap.Instances) != 1 {
		f.t.Fatalf("snapshot: %v %+v", err, snap)
	}
	return snap.Instances[0]
}

func TestCollectsARunningInstance(t *testing.T) {
	f := newFixture(t)
	f.c.Updates = func() Updates { return Updates{PendingByInstance: map[string]int{"x": 2}, LastCheck: 1234} }
	write(t, filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json"), "{}")
	write(t, filepath.Join(f.data, "cache", "workshop", "big"), strings.Repeat("x", 5000))
	now := time.Now()
	if err := os.MkdirAll(filepath.Join(f.data, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (steam.Status{Account: "a", LastSuccessAt: &now}).Save(filepath.Join(f.data, "secrets", "steam-status.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.data, "snapshots", "x", "index.json"), `{"snapshots":[
 {"id":"a","reason":"manual","state":"complete","created":"2026-10-01T10:00:00Z"},
 {"id":"b","reason":"manual","state":"complete","created":"2026-10-02T10:00:00Z"},
 {"id":"c","reason":"update","state":"creating","created":"2026-10-03T10:00:00Z"}]}`)

	snap, _ := f.c.Snapshot()
	i := snap.Instances[0]
	if !i.Up || i.Players != 3 || i.MaxPlayers != 60 || i.Health != monitor.HealthHealthy || i.UptimeSeconds != 60 {
		t.Errorf("instance = %+v", i)
	}
	if i.RestartsTotal["all"] != 2 || i.Product != "dayz-stable" || i.Build != "7" || i.Map != "empty.m" || i.ModsPendingUpdate != 2 {
		t.Errorf("instance = %+v", i)
	}
	if i.A2SRTTSeconds == nil || !i.LastRenderSuccess || i.LastRenderTimestamp == 0 || !strings.Contains(i.Message, "3/60 players") {
		t.Errorf("instance = %+v", i)
	}
	g := snap.Global
	if g.Version != "v-test" || g.ProductBuilds["dayz-stable"] != "7" || g.UpdateLastCheckUnix != 1234 || g.UpdatesPending != 2 {
		t.Errorf("global = %+v", g)
	}
	if g.CacheBytes < 5000 || g.DiskFreeBytes <= 0 || !g.SteamSessionValid || g.SteamAuthRequired {
		t.Errorf("global = %+v", g)
	}
	if len(snap.Backups) != 1 || snap.Backups[0].Count != 2 || snap.Backups[0].LastSuccess["manual"] != 1790935200 || snap.Backups[0].LastSuccess["update"] != 0 {
		t.Errorf("backups = %+v (only complete snapshots count)", snap.Backups)
	}
}

func TestInstanceStates(t *testing.T) {
	f := newFixture(t)
	f.unit = "ActiveState=inactive\nSubState=dead\nNRestarts=0\n"
	f.health = ""
	if i := f.inst(); i.Up || i.Health != monitor.HealthUnhealthy || !strings.Contains(i.Message, "stopped (inactive)") {
		t.Errorf("a stopped instance = %+v", i)
	}

	f = newFixture(t)
	f.query = func() (a2s.InfoResponse, error) { return a2s.InfoResponse{}, errors.New("timeout") }
	if i := f.inst(); i.Up || !strings.Contains(i.Message, "does not answer the Steam query") {
		t.Errorf("an active unit that does not answer = %+v", i)
	}

	f = newFixture(t)
	f.unit = "ActiveState=activating\nSubState=start\nNRestarts=0\n"
	f.health = "starting"
	if i := f.inst(); i.Up || i.Health != monitor.HealthStarting || i.Message != "starting" {
		t.Errorf("a starting instance = %+v", i)
	}

	f = newFixture(t)
	f.health = "" // no podman health: an active unit is taken as healthy
	if i := f.inst(); !i.Up || i.Health != monitor.HealthHealthy {
		t.Errorf("no health check = %+v", i)
	}

	f = newFixture(t)
	f.unit, f.unitErr = "", errors.New("Failed to connect to bus")
	if i := f.inst(); i.Up || !strings.Contains(i.Message, "systemd:") {
		t.Errorf("no systemd = %+v", i)
	}

	f = newFixture(t)
	f.health = "unhealthy"
	if i := f.inst(); i.Health != monitor.HealthUnhealthy || !i.Up {
		t.Errorf("unhealthy = %+v", i)
	}
}

func TestFailedRenderGateAndSiteErrors(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json"), "{}")
	gate := instance.GateFile(filepath.Join(f.data, "instances", "x", "runtime"))
	if err := os.MkdirAll(filepath.Dir(gate), 0o750); err != nil {
		t.Fatal(err)
	}
	g := instance.FailureGate{}
	g.RecordFailure("h", "bad serverDZ.cfg", time.Now().Add(time.Hour))
	if err := g.State.Save(gate); err != nil {
		t.Fatal(err)
	}
	if i := f.inst(); i.LastRenderSuccess {
		t.Errorf("a recorded render failure must show: %+v", i)
	}

	f = newFixture(t)
	f.c.Load = func() (*site.Tree, error) { return nil, errors.New("no site") }
	snap, _ := f.c.Snapshot()
	if len(snap.Instances) != 0 || snap.Global.JobsFailedTotal["site_load"] != 1 {
		t.Errorf("a site that does not load is reported, not hidden: %+v", snap)
	}

	f = newFixture(t)
	write(t, filepath.Join(f.data, "site", "instances", "x", "instance.yaml"), "name: x\nproduct: nope\nmap: m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 1}\nnetwork: host\n")
	if i := f.inst(); !strings.Contains(i.Message, "cannot resolve") || i.Up {
		t.Errorf("an instance that does not resolve: %+v", i)
	}
}

func TestMissionDriftIsCounted(t *testing.T) {
	f := newFixture(t)
	pristine := filepath.Join(f.data, "instances", "x", "servermpmissions", "empty.m")
	write(t, filepath.Join(pristine, "init.c"), "init")
	write(t, filepath.Join(pristine, "db", "types.xml"), "types")
	live, manifest := filepath.Join(f.data, "instances", "x", "mpmissions", "empty.m"), filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json")
	in := mission.RenderInput{PristineDir: pristine, LiveDir: live, ManifestPath: manifest, FileHistoryDir: filepath.Join(f.data, "instances", "x", "filehistory")}
	if _, _, err := mission.Render(in); err != nil {
		t.Fatal(err)
	}
	if i := f.inst(); i.MissionDriftFiles != 0 {
		t.Errorf("a fresh render has no drift: %+v", i)
	}
	write(t, filepath.Join(live, "init.c"), "somebody edited this")
	f.c.slow.drift = nil // the drift count is slow; force it
	f.c.Refresh(context.Background())
	if i := f.inst(); i.MissionDriftFiles != 1 {
		t.Errorf("one managed file changed: %+v", i)
	}
}

func TestSnapshotIsCachedAndRefreshes(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	f.query = func() (a2s.InfoResponse, error) { return a2s.InfoResponse{Players: byte(calls.Add(1))}, nil }
	if i := f.inst(); i.Players != 1 {
		t.Fatalf("players = %d", i.Players)
	}
	if i := f.inst(); i.Players != 1 || calls.Load() != 1 {
		t.Errorf("a scrape must not query the server again: players %d, %d queries", i.Players, calls.Load())
	}
	f.c.Refresh(context.Background())
	if i := f.inst(); i.Players != 2 {
		t.Errorf("a refresh updates it: %d", i.Players)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.c.Interval = 20 * time.Millisecond
	f.c.Start(ctx)
	time.Sleep(150 * time.Millisecond)
	cancel()
	if calls.Load() < 4 {
		t.Errorf("the background loop must keep refreshing: %d queries", calls.Load())
	}
}

func TestMetricsEndToEnd(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json"), "{}")
	write(t, filepath.Join(f.data, "snapshots", "x", "index.json"), `{"snapshots":[{"id":"a","reason":"manual","state":"complete","created":"2026-10-01T10:00:00Z"}]}`)
	srv := httptest.NewServer(monitor.Handler(f.c))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, want := range []string{`dzo_instance_up{instance="x"} 1`, `dzo_instance_players{instance="x"} 3`, `dzo_build_info{version="v-test"} 1`, `dzo_backup_count{instance="x"} 1`, `dzo_backup_last_success_timestamp{instance="x",reason="manual"}`, `dzo_product_build_info{buildid="7",product="dayz-stable"} 1`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("metrics lack %s", want)
		}
	}
	// and what dzo check remote reads back
	res, err := monitor.CheckRemoteInstance(context.Background(), srv.URL+"/status/x", 2*time.Second)
	if err != nil || res.Status != monitor.StatusOK {
		t.Errorf("check remote = %+v %v", res, err)
	}
}

func TestGuardAllowListAndToken(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })
	h, err := Guard(ok, config.Exporter{Allow: []string{"192.0.2.0/24", "2001:db8::/32"}}, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	call := func(remote, auth string) int {
		r := httptest.NewRequest("GET", "/metrics", nil)
		r.RemoteAddr = remote
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, tc := range []struct {
		remote, auth string
		want         int
	}{
		{"192.0.2.7:5000", "Bearer s3cret", 200},
		{"[2001:db8::1]:5000", "Bearer s3cret", 200},
		{"192.0.2.7:5000", "", 401},
		{"192.0.2.7:5000", "Bearer wrong", 401},
		{"198.51.100.1:5000", "Bearer s3cret", 403}, // the address is checked before the token
		{"garbage", "Bearer s3cret", 403},
	} {
		if got := call(tc.remote, tc.auth); got != tc.want {
			t.Errorf("%s %q: %d, want %d", tc.remote, tc.auth, got, tc.want)
		}
	}
	open, _ := Guard(ok, config.Exporter{}, "")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.1:1"
	w := httptest.NewRecorder()
	open.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("no rules, no restrictions: %d", w.Code)
	}
	if _, err := Guard(ok, config.Exporter{Allow: []string{"nope"}}, ""); err == nil {
		t.Error("a bad CIDR must be refused")
	}
}

// ---- TLS ----

type pair struct{ certPEM, keyPEM []byte }

func newCert(t *testing.T, cn string, serial int64) pair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	return pair{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})}
}

func (p pair) write(t *testing.T, dir string) (cert, key string) {
	t.Helper()
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(cert, p.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, p.keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func serial(t *testing.T, addr string, clientCert *tls.Certificate) (int64, error) {
	t.Helper()
	cfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // a test against our own self-signed certificates
	if clientCert != nil {
		cfg.Certificates = []tls.Certificate{*clientCert}
	}
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	// with TLS 1.3 a rejected client certificate shows on the first read
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write([]byte("GET /status HTTP/1.0\r\n\r\n"))
	buf := make([]byte, 16)
	if _, err := conn.Read(buf); err != nil {
		return 0, err
	}
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64(), nil
}

func startTLS(t *testing.T, c config.Exporter) (addr string, reload chan struct{}, logs func() []string) {
	t.Helper()
	c.Listen = "127.0.0.1:0"
	reload = make(chan struct{})
	var mu sync.Mutex
	var lines []string
	ready := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = Serve(ctx, c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }), reload,
			func(a string) { ready <- a }, func(f string, a ...any) { mu.Lock(); lines = append(lines, f); mu.Unlock() })
	}()
	select {
	case addr = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("the server did not start")
	}
	return addr, reload, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), lines...) }
}

func TestTLSCertificateIsReloadedWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCert(t, "one", 1).write(t, dir)
	var c config.Exporter
	c.TLS.CertFile, c.TLS.KeyFile = cert, key
	addr, reload, logs := startTLS(t, c)

	if s, err := serial(t, addr, nil); err != nil || s != 1 {
		t.Fatalf("serial = %d %v", s, err)
	}
	newCert(t, "two", 2).write(t, dir)
	reload <- struct{}{} // like systemctl reload
	time.Sleep(100 * time.Millisecond)
	if s, err := serial(t, addr, nil); err != nil || s != 2 {
		t.Fatalf("after the reload: serial = %d %v", s, err)
	}

	// a broken replacement is rejected, the old certificate stays
	if err := os.WriteFile(cert, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	reload <- struct{}{}
	time.Sleep(100 * time.Millisecond)
	if s, err := serial(t, addr, nil); err != nil || s != 2 {
		t.Fatalf("after a broken certificate: serial = %d %v", s, err)
	}
	found := false
	for _, l := range logs() {
		found = found || strings.Contains(l, "rejected")
	}
	if !found {
		t.Errorf("the rejection must be logged: %v", logs())
	}

	// and files that are replaced are noticed by themselves
	newCert(t, "three", 3).write(t, dir)
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(cert, old, old)
	time.Sleep(certChecks + 200*time.Millisecond)
	if s, err := serial(t, addr, nil); err != nil || s != 3 {
		t.Fatalf("changed files are picked up on a new connection: serial = %d %v", s, err)
	}
}

func TestMutualTLS(t *testing.T) {
	dir := t.TempDir()
	srvCert, srvKey := newCert(t, "server", 1).write(t, dir)
	client := newCert(t, "client", 9)
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, client.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	var c config.Exporter
	c.TLS.CertFile, c.TLS.KeyFile, c.TLS.ClientCAFile = srvCert, srvKey, ca
	addr, _, _ := startTLS(t, c)
	if _, err := serial(t, addr, nil); err == nil {
		t.Error("a client without a certificate must be refused")
	}
	cc, err := tls.X509KeyPair(client.certPEM, client.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := serial(t, addr, &cc); err != nil || s != 1 {
		t.Errorf("a client with the right certificate: %d %v", s, err)
	}
}

func TestServeErrors(t *testing.T) {
	run := func(c config.Exporter) error {
		c.Listen = "127.0.0.1:0"
		return Serve(context.Background(), c, http.NotFoundHandler(), nil, nil, func(string, ...any) {})
	}
	if err := run(config.Exporter{BearerTokenFile: "/nonexistent"}); err == nil {
		t.Error("a missing token file")
	}
	empty := filepath.Join(t.TempDir(), "t")
	write(t, empty, "\n")
	if err := run(config.Exporter{BearerTokenFile: empty}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty token file: %v", err)
	}
	var c config.Exporter
	c.TLS.CertFile, c.TLS.KeyFile = "/nonexistent", "/nonexistent"
	if err := run(c); err == nil {
		t.Error("missing certificate files")
	}
	dir := t.TempDir()
	cert, key := newCert(t, "x", 1).write(t, dir)
	c.TLS.CertFile, c.TLS.KeyFile, c.TLS.ClientCAFile = cert, key, empty
	if err := run(c); err == nil || !strings.Contains(err.Error(), "holds no certificate") {
		t.Errorf("a client CA file without certificates: %v", err)
	}
	if err := run(config.Exporter{Allow: []string{"x"}}); err == nil {
		t.Error("a bad allow-list")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = l.Close() }()
	if err := Serve(context.Background(), config.Exporter{Listen: l.Addr().String()}, http.NotFoundHandler(), nil, nil, func(string, ...any) {}); err == nil {
		t.Error("an address in use")
	}
}

func TestProcUptime(t *testing.T) {
	if u, err := ProcUptime(); err != nil || u <= 0 {
		t.Errorf("uptime = %v %v", u, err)
	}
}

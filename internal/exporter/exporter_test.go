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
	"github.com/bzed/dayz-server-operator/internal/tlsfiles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t testing.TB, path, body string) {
	t.Helper()
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(path, []byte(body), 0o600)
	require.NoError(t, err)
}

type fixture struct {
	t    testing.TB
	data string
	cfg  *config.Config
	c    *Collector

	unit, health string
	unitErr      error
	query        func() (a2s.InfoResponse, error)
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	f := &fixture{t: t, data: t.TempDir()}
	f.unit = "ActiveState=active\nSubState=running\nActiveEnterTimestampMonotonic=1000000\nNRestarts=2\n"
	f.health = "healthy"
	f.query = func() (a2s.InfoResponse, error) { return a2s.InfoResponse{Players: 3, MaxPlayers: 60}, nil }
	cfg, err := config.Parse([]byte("paths:\n  data: " + f.data + "\n"))
	require.NoError(t, err)
	f.cfg = cfg
	write(t, filepath.Join(f.data, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: empty.m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
`)
	write(t, filepath.Join(f.data, "cache", "products", "dayz-stable", "7", "DayZServer"), "bin")
	err = os.Symlink("7", filepath.Join(f.data, "cache", "products", "dayz-stable", "current"))
	require.NoError(t, err)
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
	require.NoError(f.t, err, "snapshot")
	require.Len(f.t, snap.Instances, 1)
	return snap.Instances[0]
}

func TestCollectsARunningInstance(t *testing.T) {
	f := newFixture(t)
	f.c.Updates = func() Updates { return Updates{PendingByInstance: map[string]int{"x": 2}, LastCheck: 1234} }
	write(t, filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json"), "{}")
	write(t, filepath.Join(f.data, "cache", "workshop", "big"), strings.Repeat("x", 5000))
	now := time.Now()
	err := os.MkdirAll(filepath.Join(f.data, "secrets"), 0o700)
	require.NoError(t, err)
	err = (steam.Status{Account: "a", LastSuccessAt: &now}).Save(filepath.Join(f.data, "secrets", "steam-status.json"))
	require.NoError(t, err)
	write(t, filepath.Join(f.data, "snapshots", "x", "index.json"), `{"snapshots":[
 {"id":"a","reason":"manual","state":"complete","created":"2026-10-01T10:00:00Z"},
 {"id":"b","reason":"manual","state":"complete","created":"2026-10-02T10:00:00Z"},
 {"id":"c","reason":"update","state":"creating","created":"2026-10-03T10:00:00Z"}]}`)

	snap, _ := f.c.Snapshot()
	i := snap.Instances[0]
	require.True(t, i.Up)
	require.Equal(t, 3, i.Players)
	require.Equal(t, 60, i.MaxPlayers)
	require.Equal(t, monitor.HealthHealthy, i.Health)
	assert.InDelta(t, 60, i.UptimeSeconds, 1e-9)
	require.Equal(t, 2, i.RestartsTotal["all"])
	require.Equal(t, "dayz-stable", i.Product)
	require.Equal(t, "7", i.Build)
	require.Equal(t, "empty.m", i.Map)
	assert.Equal(t, 2, i.ModsPendingUpdate)
	require.NotNil(t, i.A2SRTTSeconds)
	require.True(t, i.LastRenderSuccess)
	require.NotZero(t, i.LastRenderTimestamp)
	assert.Contains(t, i.Message, "3/60 players")
	g := snap.Global
	require.Equal(t, "v-test", g.Version)
	require.Equal(t, "7", g.ProductBuilds["dayz-stable"])
	require.Equal(t, int64(1234), g.UpdateLastCheckUnix)
	assert.Equal(t, 2, g.UpdatesPending)
	assert.GreaterOrEqual(t, g.CacheBytes, int64(5000))
	assert.Positive(t, g.DiskFreeBytes)
	assert.True(t, g.SteamSessionValid)
	assert.False(t, g.SteamAuthRequired)
	require.Len(t, snap.Backups, 1)
	require.Equal(t, 2, snap.Backups[0].Count)
	require.Equal(t, int64(1790935200), snap.Backups[0].LastSuccess["manual"])
	assert.Equal(t, int64(0), snap.Backups[0].LastSuccess["update"])
}

func TestInstanceStates(t *testing.T) {
	f := newFixture(t)
	f.unit = "ActiveState=inactive\nSubState=dead\nNRestarts=0\n"
	f.health = ""
	i := f.inst()
	require.False(t, i.Up)
	require.Equal(t, monitor.HealthUnhealthy, i.Health)
	assert.Contains(t, i.Message, "stopped (inactive)")

	f = newFixture(t)
	f.query = func() (a2s.InfoResponse, error) { return a2s.InfoResponse{}, errors.New("timeout") }
	i = f.inst()
	require.False(t, i.Up)
	assert.Contains(t, i.Message, "does not answer the Steam query")

	f = newFixture(t)
	f.unit = "ActiveState=activating\nSubState=start\nNRestarts=0\n"
	f.health = "starting"
	i = f.inst()
	require.False(t, i.Up)
	require.Equal(t, monitor.HealthStarting, i.Health)
	assert.Equal(t, "starting", i.Message)

	f = newFixture(t)
	f.health = "" // no podman health: an active unit is taken as healthy
	i = f.inst()
	require.True(t, i.Up)
	assert.Equal(t, monitor.HealthHealthy, i.Health)

	f = newFixture(t)
	f.unit, f.unitErr = "", errors.New("Failed to connect to bus")
	i = f.inst()
	require.False(t, i.Up)
	assert.Contains(t, i.Message, "systemd:")

	f = newFixture(t)
	f.health = "unhealthy"
	i = f.inst()
	require.Equal(t, monitor.HealthUnhealthy, i.Health)
	assert.True(t, i.Up)
}

func TestFailedRenderGateAndSiteErrors(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json"), "{}")
	gate := instance.GateFile(filepath.Join(f.data, "instances", "x", "runtime"))
	err := os.MkdirAll(filepath.Dir(gate), 0o750)
	require.NoError(t, err)
	g := instance.FailureGate{}
	g.RecordFailure("h", "bad serverDZ.cfg", time.Now().Add(time.Hour))
	err = g.State.Save(gate)
	require.NoError(t, err)
	i := f.inst()
	assert.False(t, i.LastRenderSuccess)

	f = newFixture(t)
	f.c.Load = func() (*site.Tree, error) { return nil, errors.New("no site") }
	snap, _ := f.c.Snapshot()
	require.Empty(t, snap.Instances)
	assert.Equal(t, 1, snap.Global.JobsFailedTotal["site_load"])

	f = newFixture(t)
	write(t, filepath.Join(f.data, "site", "instances", "x", "instance.yaml"), "name: x\nproduct: nope\nmap: m\nmission_source: {git: g, ref: r, path: p}\nports: {game: 1}\nnetwork: host\n")
	i = f.inst()
	require.Contains(t, i.Message, "cannot resolve")
	assert.False(t, i.Up)
}

func TestMissionDriftIsCounted(t *testing.T) {
	f := newFixture(t)
	pristine := filepath.Join(f.data, "instances", "x", "servermpmissions", "empty.m")
	write(t, filepath.Join(pristine, "init.c"), "init")
	write(t, filepath.Join(pristine, "db", "types.xml"), "types")
	live, manifest := filepath.Join(f.data, "instances", "x", "mpmissions", "empty.m"), filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json")
	in := mission.RenderInput{PristineDir: pristine, LiveDir: live, ManifestPath: manifest, FileHistoryDir: filepath.Join(f.data, "instances", "x", "filehistory")}
	_, _, err := mission.Render(in)
	require.NoError(t, err)
	i := f.inst()
	assert.Equal(t, 0, i.MissionDriftFiles)
	write(t, filepath.Join(live, "init.c"), "somebody edited this")
	f.c.slow.drift = nil // the drift count is slow; force it
	f.c.Refresh(t.Context())
	i = f.inst()
	assert.Equal(t, 1, i.MissionDriftFiles)
}

func TestSnapshotIsCachedAndRefreshes(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	f.query = func() (a2s.InfoResponse, error) { return a2s.InfoResponse{Players: byte(calls.Add(1))}, nil }
	i := f.inst()
	require.Equal(t, 1, i.Players)
	i = f.inst()
	require.Equal(t, 1, i.Players)
	assert.Equal(t, int32(1), calls.Load())
	f.c.Refresh(t.Context())
	i = f.inst()
	assert.Equal(t, 2, i.Players)
	ctx, cancel := context.WithCancel(t.Context())
	f.c.Interval = 20 * time.Millisecond
	f.c.Start(ctx)
	defer cancel()
	require.Eventually(t, func() bool { return calls.Load() >= 4 }, 5*time.Second, 5*time.Millisecond,
		"the background loop must keep refreshing")
}

func TestMetricsEndToEnd(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.data, "instances", "x", "mpmissions", ".dzo-manifest.json"), "{}")
	write(t, filepath.Join(f.data, "logs", "x", "2026-10-01", "script_1.log.gz"), "12345")
	write(t, filepath.Join(f.data, "instances", "x", "profiles", "huge.dat"), strings.Repeat("x", 2<<20))
	write(t, filepath.Join(f.data, "snapshots", "x", "index.json"), `{"snapshots":[{"id":"a","reason":"manual","state":"complete","created":"2026-10-01T10:00:00Z"}]}`)
	srv := httptest.NewServer(monitor.Handler(f.c))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, want := range []string{`dzo_instance_up{instance="x"} 1`, `dzo_instance_players{instance="x"} 3`, `dzo_build_info{version="v-test"} 1`, `dzo_backup_count{instance="x"} 1`, `dzo_logs_archive_bytes{instance="x"} 5`, `dzo_profile_unmatched_bytes{instance="x"} 2.097152e+06`, `dzo_backup_last_success_timestamp{instance="x",reason="manual"}`, `dzo_product_build_info{buildid="7",product="dayz-stable"} 1`} {
		assert.Contains(t, string(b), want)
	}
	// and what dzo check remote reads back
	res, err := monitor.CheckRemoteInstance(t.Context(), srv.URL+"/status/x", 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, monitor.StatusOK, res.Status)
}

func TestGuardAllowListAndToken(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })
	h, err := Guard(ok, config.Exporter{Allow: []string{"192.0.2.0/24", "2001:db8::/32"}}, "s3cret")
	require.NoError(t, err)
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
		got := call(tc.remote, tc.auth)
		assert.Equal(t, tc.want, got)
	}
	open, _ := Guard(ok, config.Exporter{}, "")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.1:1"
	w := httptest.NewRecorder()
	open.ServeHTTP(w, r)
	assert.Equal(t, 200, w.Code)
	_, err = Guard(ok, config.Exporter{Allow: []string{"nope"}}, "")
	assert.Error(t, err, "a bad CIDR must be refused")
}

// ---- TLS ----

type pair struct{ certPEM, keyPEM []byte }

func newCert(t testing.TB, cn string, serial int64) pair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	kb, _ := x509.MarshalECPrivateKey(key)
	return pair{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})}
}

func (p pair) write(t *testing.T, dir string) (cert, key string) {
	t.Helper()
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	err := os.WriteFile(cert, p.certPEM, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(key, p.keyPEM, 0o600)
	require.NoError(t, err)
	return cert, key
}

func serial(t testing.TB, addr string, clientCert *tls.Certificate) (int64, error) {
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

// servedSerial is the serial number of the certificate the server presents
// now, or 0 while it cannot be read.
func servedSerial(t testing.TB, addr string) int64 {
	t.Helper()
	s, err := serial(t, addr, nil)
	if err != nil {
		return 0
	}
	return s
}

func startTLS(t testing.TB, c config.Exporter) (addr string, reload chan struct{}, logs func() []string) {
	t.Helper()
	c.Listen = "127.0.0.1:0"
	reload = make(chan struct{})
	var mu sync.Mutex
	var lines []string
	ready := make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		_ = Serve(ctx, c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }), reload,
			func(a string) { ready <- a }, func(f string, a ...any) { mu.Lock(); lines = append(lines, f); mu.Unlock() })
	}()
	select {
	case addr = <-ready:
	case <-time.After(3 * time.Second):
		require.FailNow(t, "the server did not start")
	}
	return addr, reload, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), lines...) }
}

func TestTLSCertificateIsReloadedWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCert(t, "one", 1).write(t, dir)
	var c config.Exporter
	c.TLS.CertFile, c.TLS.KeyFile = cert, key
	addr, reload, logs := startTLS(t, c)

	s, err := serial(t, addr, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), s)
	newCert(t, "two", 2).write(t, dir)
	reload <- struct{}{} // like systemctl reload
	require.Eventually(t, func() bool { return servedSerial(t, addr) == 2 }, 5*time.Second, 10*time.Millisecond,
		"after the reload the new certificate is served")

	// a broken replacement is rejected, the old certificate stays
	err = os.WriteFile(cert, []byte("not a certificate"), 0o600)
	require.NoError(t, err)
	reload <- struct{}{}
	require.Eventually(t, func() bool {
		for _, l := range logs() {
			if strings.Contains(l, "rejected") {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the rejection must be logged")
	s, err = serial(t, addr, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), s, "after a broken certificate")

	// and files that are replaced are noticed by themselves
	newCert(t, "three", 3).write(t, dir)
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(cert, old, old)
	require.Eventually(t, func() bool { return servedSerial(t, addr) == 3 }, tlsfiles.Recheck+5*time.Second, 100*time.Millisecond,
		"changed files are picked up on a new connection")
}

func TestMutualTLS(t *testing.T) {
	dir := t.TempDir()
	srvCert, srvKey := newCert(t, "server", 1).write(t, dir)
	client := newCert(t, "client", 9)
	ca := filepath.Join(dir, "ca.pem")
	err := os.WriteFile(ca, client.certPEM, 0o600)
	require.NoError(t, err)
	var c config.Exporter
	c.TLS.CertFile, c.TLS.KeyFile, c.TLS.ClientCAFile = srvCert, srvKey, ca
	addr, _, _ := startTLS(t, c)
	_, err = serial(t, addr, nil)
	require.Error(t, err, "a client without a certificate must be refused")
	cc, err := tls.X509KeyPair(client.certPEM, client.keyPEM)
	require.NoError(t, err)
	s, err := serial(t, addr, &cc)
	require.NoError(t, err)
	assert.Equal(t, int64(1), s)
}

func TestServeErrors(t *testing.T) {
	run := func(c config.Exporter) error {
		c.Listen = "127.0.0.1:0"
		return Serve(t.Context(), c, http.NotFoundHandler(), nil, nil, func(string, ...any) {})
	}
	err := run(config.Exporter{BearerTokenFile: "/nonexistent"})
	require.Error(t, err, "a missing token file")
	empty := filepath.Join(t.TempDir(), "t")
	write(t, empty, "\n")
	err = run(config.Exporter{BearerTokenFile: empty})
	require.ErrorContains(t, err, "empty")
	var c config.Exporter
	c.TLS.CertFile, c.TLS.KeyFile = "/nonexistent", "/nonexistent"
	err = run(c)
	require.Error(t, err, "missing certificate files")
	dir := t.TempDir()
	cert, key := newCert(t, "x", 1).write(t, dir)
	c.TLS.CertFile, c.TLS.KeyFile, c.TLS.ClientCAFile = cert, key, empty
	err = run(c)
	require.ErrorContains(t, err, "holds no certificate")
	err = run(config.Exporter{Allow: []string{"x"}})
	require.Error(t, err, "a bad allow-list")
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = l.Close() }()
	err = Serve(t.Context(), config.Exporter{Listen: l.Addr().String()}, http.NotFoundHandler(), nil, nil, func(string, ...any) {})
	assert.Error(t, err, "an address in use")
}

func TestProcUptime(t *testing.T) {
	u, err := ProcUptime()
	require.NoError(t, err)
	assert.Positive(t, u)
}

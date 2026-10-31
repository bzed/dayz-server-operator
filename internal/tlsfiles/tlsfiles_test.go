// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package tlsfiles_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/tlsfiles"
)

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
	kb, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pair{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})}
}

func (p pair) write(t *testing.T, dir string) (cert, key string) {
	t.Helper()
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(cert, p.certPEM, 0o600))
	require.NoError(t, os.WriteFile(key, p.keyPEM, 0o600))
	return cert, key
}

// touch gives path a modification time that differs from every earlier one.
func touch(t *testing.T, path string, n int) {
	t.Helper()
	m := time.Now().Add(time.Duration(n) * time.Minute)
	require.NoError(t, os.Chtimes(path, m, m))
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Now()} }
func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(f string, _ ...any) { l.mu.Lock(); l.lines = append(l.lines, f); l.mu.Unlock() }
func (l *logs) count() int              { l.mu.Lock(); defer l.mu.Unlock(); return len(l.lines) }

func serial(t *testing.T, p *tlsfiles.Pair) int64 {
	t.Helper()
	c, err := p.GetCertificate(nil)
	require.NoError(t, err)
	x, err := x509.ParseCertificate(c.Certificate[0])
	require.NoError(t, err)
	return x.SerialNumber.Int64()
}

func TestPairReloadsChangedFiles(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCert(t, "a", 1).write(t, dir)
	var l logs
	p, err := tlsfiles.NewPair(cert, key, l.logf)
	require.NoError(t, err)
	clk := newClock()
	p.SetClock(clk.now)
	require.Equal(t, int64(1), serial(t, p))

	newCert(t, "b", 2).write(t, dir)
	touch(t, cert, 10)
	require.Equal(t, int64(1), serial(t, p), "before the re-check interval the files are not looked at")
	clk.add(tlsfiles.Recheck + time.Second)
	require.Equal(t, int64(2), serial(t, p))
}

func TestPairKeepsOldOnBadFiles(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCert(t, "a", 1).write(t, dir)
	var l logs
	p, err := tlsfiles.NewPair(cert, key, l.logf)
	require.NoError(t, err)
	clk := newClock()
	p.SetClock(clk.now)

	require.NoError(t, os.WriteFile(key, []byte("garbage"), 0o600))
	touch(t, key, 10)
	clk.add(tlsfiles.Recheck + time.Second)
	require.Equal(t, int64(1), serial(t, p), "the old pair stays active")
	require.Equal(t, 1, l.count())
	clk.add(tlsfiles.Recheck + time.Second)
	require.Equal(t, int64(1), serial(t, p))
	require.Equal(t, 1, l.count(), "the same bad files are not tried on every check")
}

func TestReloadOnSignal(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCert(t, "a", 1).write(t, dir)
	var l logs
	p, err := tlsfiles.NewPair(cert, key, l.logf)
	require.NoError(t, err)

	// same modification time as before, so only an explicit reload notices
	fi, err := os.Stat(cert)
	require.NoError(t, err)
	newCert(t, "b", 2).write(t, dir)
	require.NoError(t, os.Chtimes(cert, fi.ModTime(), fi.ModTime()))
	p.Reload()
	require.Equal(t, int64(2), serial(t, p))
}

func TestNewPairMissingFiles(t *testing.T) {
	_, err := tlsfiles.NewPair("/nonexistent", "/nonexistent", func(string, ...any) {})
	require.Error(t, err)
}

func TestPoolReloads(t *testing.T) {
	dir := t.TempDir()
	a, b := newCert(t, "a", 1), newCert(t, "b", 2)
	ca := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(ca, a.certPEM, 0o600))
	var l logs
	pool, err := tlsfiles.NewPool(ca, l.logf)
	require.NoError(t, err)
	clk := newClock()
	pool.SetClock(clk.now)

	signedBy := func(p pair) bool {
		block, _ := pem.Decode(p.certPEM)
		x, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		_, err = x.Verify(x509.VerifyOptions{Roots: pool.Get(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
		return err == nil
	}
	require.True(t, signedBy(a))
	require.False(t, signedBy(b))

	require.NoError(t, os.WriteFile(ca, b.certPEM, 0o600))
	touch(t, ca, 10)
	clk.add(tlsfiles.Recheck + time.Second)
	require.True(t, signedBy(b), "after the re-check the new CA is trusted")
	require.False(t, signedBy(a))
}

func TestPoolRefusesEmptyFile(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("\n"), 0o600))
	_, err := tlsfiles.NewPool(ca, func(string, ...any) {})
	require.ErrorContains(t, err, "holds no certificate")
	_, err = tlsfiles.NewPool("/nonexistent", func(string, ...any) {})
	require.Error(t, err)
}

func TestServerConfig(t *testing.T) {
	dir := t.TempDir()
	cert, key := newCert(t, "a", 1).write(t, dir)
	p, err := tlsfiles.NewPair(cert, key, func(string, ...any) {})
	require.NoError(t, err)

	plain := tlsfiles.ServerConfig(p, nil)
	assert.Equal(t, uint16(tls.VersionTLS12), plain.MinVersion)
	assert.Equal(t, tls.NoClientCert, plain.ClientAuth)

	ca := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(ca, newCert(t, "c", 9).certPEM, 0o600))
	pool, err := tlsfiles.NewPool(ca, func(string, ...any) {})
	require.NoError(t, err)
	mutual := tlsfiles.ServerConfig(p, pool)
	assert.Equal(t, tls.RequireAndVerifyClientCert, mutual.ClientAuth)
	cfg, err := mutual.GetConfigForClient(nil)
	require.NoError(t, err)
	assert.Same(t, pool.Get(), cfg.ClientCAs, "each handshake sees the pool's current certificates")
}

func TestClientConfigTrustsItsRoots(t *testing.T) {
	dir := t.TempDir()
	srv := newCert(t, "srv", 1)
	cert, key := srv.write(t, dir)
	p, err := tlsfiles.NewPair(cert, key, func(string, ...any) {})
	require.NoError(t, err)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsfiles.ServerConfig(p, nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); _ = c.Close() }()
		}
	}()
	dial := func(caPEM []byte) error {
		ca := filepath.Join(dir, "ca.pem")
		require.NoError(t, os.WriteFile(ca, caPEM, 0o600))
		roots, err := tlsfiles.LoadRoots(ca)
		require.NoError(t, err)
		c, err := tls.Dial("tcp", ln.Addr().String(), tlsfiles.ClientConfig(roots))
		if err == nil {
			_ = c.Close()
		}
		return err
	}
	require.NoError(t, dial(srv.certPEM))
	require.Error(t, dial(newCert(t, "other", 5).certPEM), "a server this CA did not sign is refused")

	assert.NotNil(t, tlsfiles.ClientConfig(nil), "no CA file: the system roots")
	_, err = tlsfiles.LoadRoots(filepath.Join(dir, "missing.pem"))
	require.Error(t, err)
	empty := filepath.Join(dir, "empty.pem")
	require.NoError(t, os.WriteFile(empty, []byte("nothing"), 0o600))
	_, err = tlsfiles.LoadRoots(empty)
	require.ErrorContains(t, err, "no certificate")
}

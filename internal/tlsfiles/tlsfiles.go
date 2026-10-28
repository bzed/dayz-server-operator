// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tlsfiles serves TLS certificates and CA pools from files and picks
// up changes to those files without a restart, so a renewed certificate (by
// certbot or any other tool) takes effect on its own. A replacement that does
// not load is rejected and logged, and the old one stays active.
package tlsfiles

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Recheck bounds how often the files are looked at.
const Recheck = 5 * time.Second

// watcher re-reads a set of files when their modification times change.
type watcher struct {
	files []string
	// load reads the files and installs the result; it must not change
	// anything when it fails.
	load func() error
	logf func(string, ...any)
	what string

	mu      sync.Mutex
	now     func() time.Time
	mtime   []time.Time
	checked time.Time
}

func newWatcher(what string, files []string, load func() error, logf func(string, ...any)) (*watcher, error) {
	w := &watcher{what: what, files: files, load: load, logf: logf, now: time.Now}
	if err := w.reload(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *watcher) setClock(now func() time.Time) {
	w.mu.Lock()
	w.now, w.checked = now, now()
	w.mu.Unlock()
}

func (w *watcher) stat() ([]time.Time, error) {
	m := make([]time.Time, len(w.files))
	for i, f := range w.files {
		fi, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		m[i] = fi.ModTime()
	}
	return m, nil
}

func (w *watcher) reload() error {
	m, err := w.stat()
	if err != nil {
		return err
	}
	if err := w.load(); err != nil {
		return err
	}
	w.mu.Lock()
	w.mtime, w.checked = m, w.now()
	w.mu.Unlock()
	return nil
}

// Reload re-reads the files now (SIGHUP, systemctl reload).
func (w *watcher) Reload() {
	if err := w.reload(); err != nil {
		w.logf("tls: the new %s was rejected, the old one stays active: %v", w.what, err)
		return
	}
	w.logf("tls: %s reloaded", w.what)
}

// check reloads when the files changed since the last look, at most every
// Recheck.
func (w *watcher) check() {
	w.mu.Lock()
	now := w.now()
	if now.Sub(w.checked) <= Recheck {
		w.mu.Unlock()
		return
	}
	w.checked = now
	old := w.mtime
	w.mu.Unlock()

	m, err := w.stat()
	if err != nil || equal(m, old) {
		return
	}
	w.Reload()
	// whether it loaded or not, do not try the same files on every check
	w.mu.Lock()
	w.mtime = m
	w.mu.Unlock()
}

func equal(a, b []time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// Pair is a certificate and its key.
type Pair struct {
	w *watcher

	mu      sync.RWMutex
	current *tls.Certificate
}

// NewPair loads the pair; it fails if the files do not load.
func NewPair(certFile, keyFile string, logf func(string, ...any)) (*Pair, error) {
	p := &Pair{}
	w, err := newWatcher("certificate", []string{certFile, keyFile}, func() error {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return err
		}
		p.mu.Lock()
		p.current = &pair
		p.mu.Unlock()
		return nil
	}, logf)
	if err != nil {
		return nil, err
	}
	p.w = w
	return p, nil
}

// Reload re-reads the files now.
func (p *Pair) Reload() { p.w.Reload() }

// GetCertificate is a tls.Config.GetCertificate; it notices changed files.
func (p *Pair) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.w.check()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current, nil
}

// Pool is a set of CA certificates read from a PEM file.
type Pool struct {
	w *watcher

	mu      sync.RWMutex
	current *x509.CertPool
}

// NewPool loads the CA file; it fails if the file holds no certificate.
func NewPool(caFile string, logf func(string, ...any)) (*Pool, error) {
	p := &Pool{}
	w, err := newWatcher("CA file", []string{caFile}, func() error {
		pem, err := os.ReadFile(caFile) //nolint:gosec // the operator's own file
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("the file holds no certificate")
		}
		p.mu.Lock()
		p.current = pool
		p.mu.Unlock()
		return nil
	}, logf)
	if err != nil {
		return nil, err
	}
	p.w = w
	return p, nil
}

// Reload re-reads the file now.
func (p *Pool) Reload() { p.w.Reload() }

// Get returns the current certificates, after looking for a changed file.
func (p *Pool) Get() *x509.CertPool {
	p.w.check()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current
}

// ServerConfig is a server TLS configuration (TLS 1.2 or later) for p. With a
// client CA pool every client must present a certificate signed by it, and each
// handshake uses the pool's current certificates.
func ServerConfig(p *Pair, clientCA *Pool) *tls.Config {
	cfg := &tls.Config{GetCertificate: p.GetCertificate, MinVersion: tls.VersionTLS12}
	if clientCA != nil {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = clientCA.Get()
		cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
			c := cfg.Clone()
			c.GetConfigForClient = nil
			c.ClientCAs = clientCA.Get()
			return c, nil
		}
	}
	return cfg
}

// ClientConfig is a client TLS configuration that trusts the pool's current
// certificates on every handshake. A nil pool means the system roots.
func ClientConfig(ca *Pool) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if ca == nil {
		return cfg
	}
	// The standard verification is replaced by VerifyConnection, which does the
	// same against the pool as it is now.
	cfg.InsecureSkipVerify = true //nolint:gosec // VerifyConnection below verifies the chain and the name
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("the server sent no certificate")
		}
		inter := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			inter.AddCert(c)
		}
		_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
			Roots: ca.Get(), Intermediates: inter, DNSName: cs.ServerName,
		})
		if err != nil {
			return fmt.Errorf("server certificate: %w", err)
		}
		return nil
	}
	return cfg
}

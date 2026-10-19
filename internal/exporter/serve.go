// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package exporter

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/config"
)

// certChecks bounds how often the certificate files are looked at.
const certChecks = 5 * time.Second

// certs holds the TLS certificate and re-reads it when the files change, so a
// replaced certificate takes effect without a restart. A new certificate that
// does not load is rejected and logged, and the old one stays active.
type certs struct {
	cert, key string
	logf      func(string, ...any)

	mu      sync.Mutex
	current *tls.Certificate
	mtime   [2]time.Time
	checked time.Time
}

func newCerts(certFile, keyFile string, logf func(string, ...any)) (*certs, error) {
	c := &certs{cert: certFile, key: keyFile, logf: logf}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *certs) stat() (m [2]time.Time, err error) {
	for i, f := range []string{c.cert, c.key} {
		fi, err := os.Stat(f)
		if err != nil {
			return m, err
		}
		m[i] = fi.ModTime()
	}
	return m, nil
}

func (c *certs) load() error {
	m, err := c.stat()
	if err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(c.cert, c.key)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.current, c.mtime, c.checked = &pair, m, time.Now()
	c.mu.Unlock()
	return nil
}

// Reload re-reads the files now (SIGHUP, systemctl reload).
func (c *certs) Reload() {
	if err := c.load(); err != nil {
		c.logf("tls: the new certificate was rejected, the old one stays active: %v", err)
		return
	}
	c.logf("tls: certificate reloaded")
}

func (c *certs) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	recheck := time.Since(c.checked) > certChecks
	if recheck {
		c.checked = time.Now()
	}
	c.mu.Unlock()
	if recheck {
		if m, err := c.stat(); err == nil {
			c.mu.Lock()
			changed := m != c.mtime
			c.mu.Unlock()
			if changed {
				c.Reload()
				// whether it loaded or not, do not try the same files on every handshake
				c.mu.Lock()
				c.mtime = m
				c.mu.Unlock()
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current, nil
}

// Guard wraps h with the access rules: the IP allow-list and the bearer token.
func Guard(h http.Handler, c config.Exporter, token string) (http.Handler, error) {
	var nets []*net.IPNet
	for _, cidr := range c.Allow {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("exporter.allow: %q: %w", cidr, err)
		}
		nets = append(nets, n)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(nets) > 0 {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			ok := err == nil && ip != nil
			if ok {
				ok = false
				for _, n := range nets {
					ok = ok || n.Contains(ip)
				}
			}
			if !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		if token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		h.ServeHTTP(w, r)
	}), nil
}

// Serve serves h on c.Listen until ctx ends: plain HTTP, or TLS (with client
// certificates if client_ca_file is set). A value on reload re-reads the
// certificate files at once; otherwise they are checked on new connections.
func Serve(ctx context.Context, c config.Exporter, h http.Handler, reload <-chan struct{}, ready func(addr string), logf func(string, ...any)) error {
	token := ""
	if c.BearerTokenFile != "" {
		b, err := os.ReadFile(c.BearerTokenFile) //nolint:gosec // the operator's own file
		if err != nil {
			return fmt.Errorf("exporter.bearer_token_file: %w", err)
		}
		token = strings.TrimSpace(string(b))
		if token == "" {
			return errors.New("exporter.bearer_token_file is empty")
		}
	}
	guarded, err := Guard(h, c, token)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: guarded, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: time.Minute, IdleTimeout: 2 * time.Minute}
	if c.TLS.CertFile != "" {
		cs, err := newCerts(c.TLS.CertFile, c.TLS.KeyFile, logf)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("exporter.tls: %w", err)
		}
		srv.TLSConfig = &tls.Config{GetCertificate: cs.get, MinVersion: tls.VersionTLS12}
		if c.TLS.ClientCAFile != "" {
			pem, err := os.ReadFile(c.TLS.ClientCAFile) //nolint:gosec // the operator's own file
			if err != nil {
				_ = ln.Close()
				return fmt.Errorf("exporter.tls.client_ca_file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				_ = ln.Close()
				return errors.New("exporter.tls.client_ca_file holds no certificate")
			}
			srv.TLSConfig.ClientCAs, srv.TLSConfig.ClientAuth = pool, tls.RequireAndVerifyClientCert
		}
		go func() {
			for {
				select {
				case <-reload:
					cs.Reload()
				case <-ctx.Done():
					return
				}
			}
		}()
		ln = tls.NewListener(ln, srv.TLSConfig)
	}
	if ready != nil {
		ready(ln.Addr().String())
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		sd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sd)
		return nil
	}
}

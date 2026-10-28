// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package exporter

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/tlsfiles"
)

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
		pair, err := tlsfiles.NewPair(c.TLS.CertFile, c.TLS.KeyFile, logf)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("exporter.tls: %w", err)
		}
		var pool *tlsfiles.Pool
		if c.TLS.ClientCAFile != "" {
			pool, err = tlsfiles.NewPool(c.TLS.ClientCAFile, logf)
			if err != nil {
				_ = ln.Close()
				return fmt.Errorf("exporter.tls.client_ca_file: %w", err)
			}
		}
		srv.TLSConfig = tlsfiles.ServerConfig(pair, pool)
		go func() {
			for {
				select {
				case <-reload:
					pair.Reload()
					if pool != nil {
						pool.Reload()
					}
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

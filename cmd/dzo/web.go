// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/tlsfiles"
	"github.com/bzed/dayz-server-operator/internal/version"
	"github.com/bzed/dayz-server-operator/internal/web"
)

// webBackend builds the one client of the web interface from web.api. Without
// it, the web interface uses this host's own API (https when serve.tls is set)
// with ${paths.secrets}/web.token. The returned func re-reads the CA file.
func webBackend(cfg *config.Config, logf func(string, ...any)) (*web.Backend, func(), error) {
	scheme := "http"
	if cfg.Serve.TLS.CertFile != "" {
		scheme = "https"
	}
	url, tokenFile := scheme+"://"+cfg.Serve.Listen, filepath.Join(cfg.Paths.Secrets, "web.token")
	if cfg.Web.API.URL != "" {
		url, tokenFile = cfg.Web.API.URL, cfg.Web.API.TokenFile
	}
	tok, err := os.ReadFile(tokenFile) //nolint:gosec // operator-configured secret file
	if err != nil {
		return nil, nil, fmt.Errorf("%w (create one with `dzo token create web --role admin --web`)", err)
	}
	token := strings.TrimSpace(string(tok))
	client, reload := apiclient.New(url, token), func() {}
	if ca := cfg.Web.API.CAFile; ca != "" {
		roots, err := tlsfiles.LoadRoots(ca)
		if err != nil {
			return nil, nil, fmt.Errorf("web.api.ca_file: %w", err)
		}
		client = apiclient.NewTLS(url, token, tlsfiles.ClientConfig(roots))
	}
	return &web.Backend{Name: cfg.Serve.Installation, Client: client}, reload, nil
}

func newWebCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Run the web interface (talks to one dzo installation through its API)",
		Long: "Serves the web interface of one installation on web.listen, over HTTPS when web.tls is set. " +
			"It reaches the installation through web.api, or this host's API with the web token. " +
			"SIGHUP (systemctl reload) re-reads the certificate files.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			logf := func(f string, a ...any) { _, _ = fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...) }
			backend, reloadAPI, err := webBackend(cfg, logf)
			if err != nil {
				return err
			}
			proxies, err := auth.ParseProxies(cfg.Web.TrustedProxies)
			if err != nil {
				return fmt.Errorf("web.trusted_proxies: %w", err)
			}
			srv, err := web.New(backend, web.Options{Version: version.String(), Assets: cfg.Web.Assets, DocsDir: cfg.Web.DocsDir, FlagsDir: cfg.Web.FlagsDir, TrustedProxies: proxies})
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", cfg.Web.Listen)
			if err != nil {
				return err
			}
			hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			scheme, reloads := "http", []func(){reloadAPI}
			if t := cfg.Web.TLS; t.CertFile != "" {
				pair, err := tlsfiles.NewPair(t.CertFile, t.KeyFile, logf)
				if err != nil {
					_ = ln.Close()
					return fmt.Errorf("web.tls: %w", err)
				}
				var pool *tlsfiles.Pool
				if t.ClientCAFile != "" {
					if pool, err = tlsfiles.NewPool(t.ClientCAFile, logf); err != nil {
						_ = ln.Close()
						return fmt.Errorf("web.tls.client_ca_file: %w", err)
					}
					reloads = append(reloads, pool.Reload)
				}
				reloads = append(reloads, pair.Reload)
				hs.TLSConfig = tlsfiles.ServerConfig(pair, pool)
				ln, scheme = tls.NewListener(ln, hs.TLSConfig), "https"
			}
			reload, stopReload := reloadOnSIGHUP(ctx)
			defer stopReload()
			go func() {
				for {
					select {
					case <-reload:
						for _, r := range reloads {
							r()
						}
					case <-ctx.Done():
						return
					}
				}
			}()
			hs.BaseContext = func(net.Listener) context.Context { return ctx }
			go func() {
				<-ctx.Done()
				sd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = hs.Shutdown(sd)
			}()
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "web interface on %s://%s with %d backend(s)\n", scheme, ln.Addr(), 1)
			if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

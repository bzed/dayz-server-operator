// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
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
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/version"
	"github.com/bzed/dayz-server-operator/internal/web"
)

// webBackends turns web.backends into clients. Without any, the web
// interface uses this host's own API with ${paths.secrets}/web.token.
func webBackends(cfg *config.Config) ([]*web.Backend, error) {
	bs := cfg.Web.Backends
	if len(bs) == 0 {
		bs = []config.Backend{{Name: cfg.Serve.Installation, URL: "http://" + cfg.Serve.Listen, TokenFile: filepath.Join(cfg.Paths.Secrets, "web.token")}}
	}
	var out []*web.Backend
	for _, b := range bs {
		tok, err := os.ReadFile(b.TokenFile) //nolint:gosec // operator-configured secret file
		if err != nil {
			return nil, fmt.Errorf("backend %q: %w (create one with `dzo token create web --role operator --delegate`)", b.Name, err)
		}
		out = append(out, &web.Backend{Name: b.Name, Client: apiclient.New(b.URL, strings.TrimSpace(string(tok)))})
	}
	return out, nil
}

func newWebCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Run the web interface (talks to one or more dzo installations through their API)",
		Long: "Serves the web interface on web.listen. It has no login of its own yet: keep it on localhost " +
			"or behind an authenticating reverse proxy and set web.user_header.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			backends, err := webBackends(cfg)
			if err != nil {
				return err
			}
			srv, err := web.New(backends, web.Options{Version: version.String(), Assets: cfg.Web.Assets, DocsDir: cfg.Web.DocsDir, FlagsDir: cfg.Web.FlagsDir, UserHeader: cfg.Web.UserHeader})
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
			hs.BaseContext = func(net.Listener) context.Context { return ctx }
			go func() {
				<-ctx.Done()
				sd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = hs.Shutdown(sd)
			}()
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "web interface on http://%s with %d backend(s)\n", ln.Addr(), len(backends))
			if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

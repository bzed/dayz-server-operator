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

// webBackends turns web.api into the one client of the web interface. Without
// it, the web interface uses this host's own API with ${paths.secrets}/web.token.
func webBackends(cfg *config.Config) ([]*web.Backend, error) {
	url, tokenFile := "http://"+cfg.Serve.Listen, filepath.Join(cfg.Paths.Secrets, "web.token")
	if cfg.Web.API.URL != "" {
		url, tokenFile = cfg.Web.API.URL, cfg.Web.API.TokenFile
	}
	tok, err := os.ReadFile(tokenFile) //nolint:gosec // operator-configured secret file
	if err != nil {
		return nil, fmt.Errorf("%w (create one with `dzo token create web --role operator --delegate`)", err)
	}
	return []*web.Backend{{Name: cfg.Serve.Installation, Client: apiclient.New(url, strings.TrimSpace(string(tok)))}}, nil
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
			srv, err := web.New(backends, web.Options{Version: version.String(), Assets: cfg.Web.Assets, DocsDir: cfg.Web.DocsDir, FlagsDir: cfg.Web.FlagsDir})
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

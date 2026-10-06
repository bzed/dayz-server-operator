// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads and validates dzo's operator configuration
// (/etc/dzo/config.yaml, §C2 of the implementation plan): data paths, the
// site config repo remote, server products and default notification
// targets. It never touches per-instance configuration, which lives in the
// site repo (§C3) and is out of scope for this package.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Paths holds every on-disk location dzo manages. All fields are directories.
// Values may reference "${data}" to inherit Data's resolved value (§C2).
type Paths struct {
	Data      string `yaml:"data"`
	Instances string `yaml:"instances"`
	Snapshots string `yaml:"snapshots"`
	Logs      string `yaml:"logs"` // archive of rotated profile logs
	Cache     string `yaml:"cache"`
	Secrets   string `yaml:"secrets"`
	DB        string `yaml:"db"`
	Site      string `yaml:"site"` // checkout of the site config repo
}

// Site describes the site config repo (D16): shared mod integrations and
// per-instance configuration, checked out from a configurable git remote.
type Site struct {
	// Remote is the git URL of the site repo. Empty means "not configured
	// yet" - dzo can still run setup/diagnostic commands without it.
	Remote string `yaml:"remote"`
	Branch string `yaml:"branch"`
	// DeployKeyPath is an optional path (normally under paths.secrets) to
	// an SSH private key used to fetch/push the site repo.
	DeployKeyPath string `yaml:"deploy_key_path"`
	// AutoPush controls whether dzo-initiated commits (mod add, web edits)
	// are pushed back automatically, or only committed locally.
	AutoPush bool `yaml:"auto_push"`
}

// Product describes one server "product" dzo can install (D14): the Steam
// app id of the dedicated server and the Steam Workshop app id its mods are
// downloaded through.
type Product struct {
	AppID         uint32 `yaml:"app_id"`
	WorkshopAppID uint32 `yaml:"workshop_app_id"`
	// BetaBranch selects a non-default steamcmd beta branch (e.g. for
	// experimental builds distributed as a beta of the same app id).
	BetaBranch string `yaml:"beta_branch,omitempty"`
}

// Steam names the Steam account steamcmd logs in with for downloads (§C7);
// the session itself lives in the steamcmd home (see `dzo steam login`).
type Steam struct {
	Account string `yaml:"account"`
}

// DiscordWebhook is one named Discord notification target (§C9).
type DiscordWebhook struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Notify holds default notification targets. Per-instance config in the
// site repo can select a subset of these by name, or none.
type Notify struct {
	Discord []DiscordWebhook `yaml:"discord"`
}

// Serve configures `dzo serve`: the JSON API of this installation and the
// endpoint the dzo-admin mods call (§C12, §C16).
type Serve struct {
	// Installation names this installation in the API, so one web interface
	// can tell several apart.
	Installation string `yaml:"installation"`
	Listen       string `yaml:"listen"`     // /api/v1
	ModListen    string `yaml:"mod_listen"` // /mod/v1/sync, for the game servers
	// AllowInsecureHTTP permits plain HTTP on a non-loopback address.
	AllowInsecureHTTP bool `yaml:"allow_insecure_http"`
}

// Backend is one installation the web interface talks to.
type Backend struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	// TokenFile holds an API token of that installation (role operator,
	// created with `dzo token create --delegate`).
	TokenFile string `yaml:"token_file"`
}

// Web configures `dzo web`, the web interface. It reaches installations only
// through their API, so it can run on another host and serve several.
type Web struct {
	Listen            string `yaml:"listen"`
	AllowInsecureHTTP bool   `yaml:"allow_insecure_http"`
	// Assets is the directory with htmx and Leaflet (Debian's libjs-*).
	Assets string `yaml:"assets"`
	// DocsDir is the built documentation, served under /docs/.
	DocsDir string `yaml:"docs_dir"`
	// UserHeader names the request header an authenticating reverse proxy
	// sets to the signed-in user (e.g. X-Forwarded-User). The web interface
	// has no login of its own yet: it reports this user to the audit log, and
	// must only be reachable through such a proxy or on localhost.
	UserHeader string    `yaml:"user_header"`
	Backends   []Backend `yaml:"backends"`
}

// Exporter configures `dzo exporter`: /metrics for Prometheus and /status for
// Icinga (§C9). It listens on plain HTTP unless TLS is configured.
type Exporter struct {
	Listen string `yaml:"listen"`
	TLS    struct {
		CertFile     string `yaml:"cert_file"`
		KeyFile      string `yaml:"key_file"`
		ClientCAFile string `yaml:"client_ca_file"` // set for mutual TLS
	} `yaml:"tls"`
	// Allow lists the networks (CIDR) that may connect; empty allows all.
	Allow []string `yaml:"allow"`
	// BearerTokenFile, if set, makes every request need that token.
	BearerTokenFile string `yaml:"bearer_token_file"`
}

// MapTiles says where each map's tile source comes from. A map is named like
// the world of its mission (chernarusplus, enoch, sakhal, deerisle).
type MapTiles struct {
	Maps map[string]MapSource `yaml:"maps"`
}

// MapSource is the data PBO of a map: the DayZ client's worlds_<map>_data.pbo,
// or a modded map's data.pbo. The dedicated server's copy is not enough.
type MapSource struct {
	Source string `yaml:"source"`
}

// Config is the root of /etc/dzo/config.yaml.
type Config struct {
	// Binary is the dzo executable the units run and mount into the containers
	// (default /usr/bin/dzo). A host that deploys by hand points it at a directory
	// it can write, so a new build is moved into place while the old one runs.
	Binary   string             `yaml:"binary"`
	Paths    Paths              `yaml:"paths"`
	Site     Site               `yaml:"site"`
	Steam    Steam              `yaml:"steam"`
	Serve    Serve              `yaml:"serve"`
	Web      Web                `yaml:"web"`
	MapTiles MapTiles           `yaml:"map_tiles"`
	Exporter Exporter           `yaml:"exporter"`
	Products map[string]Product `yaml:"products"`
	Notify   Notify             `yaml:"notify"`
}

// defaultsTemplated returns the configuration documented in §C2 with its
// path fields still holding "${data}" references, before any file is read
// and before those references are resolved against Paths.Data. It is the
// unmarshal target for Parse, so a user overriding only paths.data still
// gets every other path derived from it.
func defaultsTemplated() *Config {
	return &Config{
		Binary: "/usr/bin/dzo",
		//nolint:gosec // G101: "Secrets" here is a directory path field (paths.secrets), not a credential value
		Paths: Paths{
			Data:      "/var/lib/dzo",
			Instances: "${data}/instances",
			Snapshots: "${data}/snapshots",
			Logs:      "${data}/logs",
			Cache:     "${data}/cache",
			Secrets:   "${data}/secrets",
			DB:        "${data}/db",
			Site:      "${data}/site",
		},
		Serve:    Serve{Installation: "default", Listen: "127.0.0.1:8080", ModListen: "127.0.0.1:2400"},
		Web:      Web{Listen: "127.0.0.1:8081", Assets: "/usr/share/javascript", DocsDir: "/usr/share/doc/dzo/html"},
		Exporter: Exporter{Listen: ":9464"},
		Products: map[string]Product{
			"dayz-stable": {
				AppID:         223350,
				WorkshopAppID: 221100,
			},
			"dayz-experimental": {
				AppID:         1042420,
				WorkshopAppID: 221100,
			},
		},
	}
}

// Default returns the configuration documented in §C2, before any file is
// read: paths.data = /var/lib/dzo, the rest derived from it, and the two
// DayZ products from D14. Site and Notify start empty - they have no
// meaningful default. The returned config has already resolved its paths,
// so it validates and can be used as-is.
func Default() *Config {
	c := defaultsTemplated()
	c.resolvePaths()
	return c
}

// Load reads, defaults, resolves and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is an operator-supplied config location (CLI flag/systemd unit), not attacker input
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse defaults, resolves and validates raw YAML config bytes. It is the
// core of Load, split out so tests and callers that already have the bytes
// (e.g. from a site repo checkout) don't need a real file.
func Parse(data []byte) (*Config, error) {
	c := defaultsTemplated()
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}

	c.resolvePaths()

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// resolvePaths expands "${data}" references in every Paths field against
// the (already-set) Data field. Only Data itself is treated as final; every
// other field is templated exactly once.
func (c *Config) resolvePaths() {
	const token = "${data}"
	expand := func(s string) string {
		return strings.ReplaceAll(s, token, c.Paths.Data)
	}
	c.Paths.Instances = expand(c.Paths.Instances)
	c.Paths.Snapshots = expand(c.Paths.Snapshots)
	c.Paths.Logs = expand(c.Paths.Logs)
	c.Paths.Cache = expand(c.Paths.Cache)
	c.Paths.Secrets = expand(c.Paths.Secrets)
	c.Paths.DB = expand(c.Paths.DB)
	c.Paths.Site = expand(c.Paths.Site)
}

// Validate checks the invariants the rest of dzo relies on: every path is
// absolute and set, site.remote (when set) parses as a URL, and every
// product has a non-zero app id. It does not touch the filesystem - that is
// "dzo setup"'s job (btrfs checks, ownership, free space).
func (c *Config) Validate() error {
	var errs []string
	if !filepath.IsAbs(c.Binary) {
		errs = append(errs, "binary: must be an absolute path")
	}

	pathFields := map[string]string{
		"paths.data":      c.Paths.Data,
		"paths.instances": c.Paths.Instances,
		"paths.snapshots": c.Paths.Snapshots,
		"paths.logs":      c.Paths.Logs,
		"paths.cache":     c.Paths.Cache,
		"paths.secrets":   c.Paths.Secrets,
		"paths.db":        c.Paths.DB,
		"paths.site":      c.Paths.Site,
	}
	for name, p := range pathFields {
		if p == "" {
			errs = append(errs, fmt.Sprintf("%s: must not be empty", name))
			continue
		}
		if !filepath.IsAbs(p) {
			errs = append(errs, fmt.Sprintf("%s: %q is not an absolute path", name, p))
		}
	}

	for _, cidr := range c.Exporter.Allow {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			errs = append(errs, fmt.Sprintf("exporter.allow: %q is not a CIDR network", cidr))
		}
	}
	if (c.Exporter.TLS.CertFile == "") != (c.Exporter.TLS.KeyFile == "") {
		errs = append(errs, "exporter.tls: cert_file and key_file go together")
	}
	if c.Exporter.TLS.ClientCAFile != "" && c.Exporter.TLS.CertFile == "" {
		errs = append(errs, "exporter.tls.client_ca_file needs cert_file and key_file")
	}

	if c.Site.Remote != "" {
		if _, err := url.Parse(c.Site.Remote); err != nil {
			errs = append(errs, fmt.Sprintf("site.remote: %q is not a valid URL: %v", c.Site.Remote, err))
		}
	}
	if c.Site.Remote != "" && c.Site.Branch == "" {
		errs = append(errs, "site.branch: required when site.remote is set")
	}

	if len(c.Products) == 0 {
		errs = append(errs, "products: at least one product must be configured")
	}
	for name, p := range c.Products {
		if p.AppID == 0 {
			errs = append(errs, fmt.Sprintf("products.%s.app_id: must be set", name))
		}
		if p.WorkshopAppID == 0 {
			errs = append(errs, fmt.Sprintf("products.%s.workshop_app_id: must be set", name))
		}
	}

	for _, l := range []struct {
		name, addr string
		insecure   bool
	}{
		{"serve.listen", c.Serve.Listen, c.Serve.AllowInsecureHTTP},
		{"serve.mod_listen", c.Serve.ModListen, c.Serve.AllowInsecureHTTP},
		{"web.listen", c.Web.Listen, c.Web.AllowInsecureHTTP},
	} {
		if err := CheckListen(l.addr, l.insecure); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", l.name, err))
		}
	}
	names := map[string]bool{}
	for _, b := range c.Web.Backends {
		if b.Name == "" || b.URL == "" || b.TokenFile == "" {
			errs = append(errs, "web.backends: every entry needs name, url and token_file")
		} else if names[b.Name] {
			errs = append(errs, fmt.Sprintf("web.backends: duplicate name %q", b.Name))
		}
		names[b.Name] = true
	}

	seen := map[string]bool{}
	for _, w := range c.Notify.Discord {
		if w.Name == "" {
			errs = append(errs, "notify.discord: entry with empty name")
			continue
		}
		if seen[w.Name] {
			errs = append(errs, fmt.Sprintf("notify.discord: duplicate name %q", w.Name))
		}
		seen[w.Name] = true
		if w.URL == "" {
			errs = append(errs, fmt.Sprintf("notify.discord.%s: url must be set", w.Name))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config: invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// CheckListen validates a listen address: host:port, and plain HTTP only on
// a loopback address unless insecure is set (§C12: the web interface and the
// mod endpoint sit behind a reverse proxy or on localhost).
func CheckListen(addr string, insecure bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("%q is not host:port", addr)
	}
	if insecure {
		return nil
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%q is not a loopback address; put dzo behind a reverse proxy with HTTPS or set allow_insecure_http", addr)
	}
	return nil
}

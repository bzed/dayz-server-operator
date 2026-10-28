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
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	// ProfileLookup lets dzo ask Steam about players (profile page, bans) for the player pages. Unset: on.
	ProfileLookup *bool `yaml:"profile_lookup"`
	// WebAPIKeyFile holds a Steam Web API key (mode 0600, absolute path). With it the Web API replaces the
	// community page and adds ban counts, account age, level, playtime and friends. Unset: no key.
	WebAPIKeyFile string `yaml:"web_api_key_file"`
}

// Lookup reports whether Steam is asked about players.
func (s Steam) Lookup() bool { return s.ProfileLookup == nil || *s.ProfileLookup }

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

// TLS is the certificate pair a listener serves, and optionally the CA that
// client certificates must be signed by (mutual TLS). The files are reloaded
// when they change.
type TLS struct {
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	ClientCAFile string `yaml:"client_ca_file"` // set for mutual TLS
}

// Serve configures `dzo serve`: the JSON API of this installation and the
// endpoint the dzo-admin mods call (§C12, §C16).
type Serve struct {
	// Enabled says whether `dzo units sync` runs `dzo serve` as a unit. Unset means: when an
	// instance runs the dzo-admin mod, which needs the endpoint; true and false force it.
	Enabled *bool `yaml:"enabled"`
	// Installation names this installation in the API, so one web interface
	// can tell several apart.
	Installation string `yaml:"installation"`
	Listen       string `yaml:"listen"`     // /api/v1
	ModListen    string `yaml:"mod_listen"` // /mod/v1/sync, for the game servers
	// AllowInsecureHTTP permits plain HTTP on a non-loopback address.
	AllowInsecureHTTP bool `yaml:"allow_insecure_http"`
	// IPRetentionDays is how long the address of a player sighting is kept unless a ban uses it (unset:
	// 90; 0: for ever). VacuumDays is how often the ban database file is rebuilt (unset: 7; 0: never).
	IPRetentionDays *int `yaml:"ip_retention_days"`
	VacuumDays      *int `yaml:"vacuum_days"`
	// SessionRetentionDays is how long the sessions of the player history (who was on which instance
	// when) are kept (unset: 365; 0: for ever). The players, their names and first-seen times stay.
	SessionRetentionDays *int `yaml:"session_retention_days"`
	// GeoIPDir holds the GeoLite2 databases geoipupdate keeps (GeoLite2-City, -Country and -ASN, .mmdb); with
	// them the player list shows flags and the player info the city, location and provider. GeoIPLegacyDir
	// holds GeoIP.dat and GeoIPv6.dat of Debian's geoip-database, which know the country only and are the
	// fallback.
	GeoIPDir       string `yaml:"geoip_dir"`
	GeoIPLegacyDir string `yaml:"geoip_legacy_dir"`
	// TLS switches the API listener (listen) to HTTPS; mod_listen stays plain HTTP for the game servers.
	TLS TLS `yaml:"tls"`
	// WebURL is the address people open the web interface at. User logins need it: it fixes the Steam
	// return address and the WebAuthn relying party.
	WebURL string `yaml:"web_url"`
	// Login holds the lifetimes of web sessions.
	Login struct {
		SessionIdleHours *int `yaml:"session_idle_hours"`
		SessionMaxDays   *int `yaml:"session_max_days"`
	} `yaml:"login"`
}

const day = 24 * time.Hour

// days turns a setting in days into a duration: unset is def, 0 is off (negative).
func days(v *int, def int) time.Duration {
	switch {
	case v == nil:
		return time.Duration(def) * day
	case *v == 0:
		return -1
	}
	return time.Duration(*v) * day
}

// IPRetention is how long a sighting keeps its address; negative means for ever.
func (s Serve) IPRetention() time.Duration { return days(s.IPRetentionDays, 90) }

// SessionRetention is how long the sessions of the player history are kept; negative means for ever.
func (s Serve) SessionRetention() time.Duration { return days(s.SessionRetentionDays, 365) }

// VacuumEvery is how often the ban database is vacuumed; negative means never.
func (s Serve) VacuumEvery() time.Duration { return days(s.VacuumDays, 7) }

// SessionIdle is how long a web session may sit unused (default 12 hours).
func (s Serve) SessionIdle() time.Duration {
	if s.Login.SessionIdleHours == nil {
		return 12 * time.Hour
	}
	return time.Duration(*s.Login.SessionIdleHours) * time.Hour
}

// SessionMax is how long a web session lives at most, however busy (default 7 days).
func (s Serve) SessionMax() time.Duration {
	if s.Login.SessionMaxDays == nil {
		return 7 * day
	}
	return time.Duration(*s.Login.SessionMaxDays) * day
}

// Web configures `dzo web`, the web interface of one installation. It reaches
// the installation only through its API, so it can run on another host.
type Web struct {
	// Enabled says whether `dzo units sync` runs `dzo web` as a unit. Unset means: when the web
	// interface is set up, that is when web.api is set or ${paths.secrets}/web.token exists; true
	// and false force it.
	Enabled           *bool  `yaml:"enabled"`
	Listen            string `yaml:"listen"`
	AllowInsecureHTTP bool   `yaml:"allow_insecure_http"`
	// TLS switches the web listener to HTTPS.
	TLS TLS `yaml:"tls"`
	// API says where the installation's API is when it is not this host's: its URL, the file with a
	// web token, and the CA file for a private CA.
	API struct {
		URL       string `yaml:"url"`
		TokenFile string `yaml:"token_file"`
		CAFile    string `yaml:"ca_file"`
	} `yaml:"api"`
	// TrustedProxies are the reverse proxies (addresses or CIDRs) whose X-Forwarded-For is believed.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// Assets is the directory with htmx and Leaflet (Debian's libjs-*).
	Assets string `yaml:"assets"`
	// DocsDir is the built documentation, served under /docs/.
	DocsDir string `yaml:"docs_dir"`
	// FlagsDir holds the country flags as <code>.svg in the 4:3 shape (Debian's iso-flags-svg); without
	// them the player list shows the country code.
	FlagsDir string `yaml:"flags_dir"`

	// RemovedBackends and RemovedUserHeader only exist so that a config that still sets the keys
	// web.backends and web.user_header is refused with an explanation.
	RemovedBackends   any `yaml:"backends"`
	RemovedUserHeader any `yaml:"user_header"`
}

// Exporter configures `dzo exporter`: /metrics for Prometheus and /status for
// Icinga (§C9). It listens on plain HTTP unless TLS is configured.
type Exporter struct {
	Listen string `yaml:"listen"`
	TLS    TLS    `yaml:"tls"`
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
		Serve:    Serve{Installation: "default", Listen: "127.0.0.1:8080", ModListen: "127.0.0.1:2400", GeoIPDir: "/var/lib/GeoIP", GeoIPLegacyDir: "/usr/share/GeoIP"},
		Web:      Web{Listen: "127.0.0.1:8081", Assets: "/usr/share/javascript", DocsDir: "/usr/share/doc/dzo/html", FlagsDir: "/usr/share/iso-flags-svg/country-4x3"},
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
	errs = append(errs, checkTLS("exporter.tls", c.Exporter.TLS)...)
	errs = append(errs, checkTLS("serve.tls", c.Serve.TLS)...)
	errs = append(errs, checkTLS("web.tls", c.Web.TLS)...)

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

	if k := c.Steam.WebAPIKeyFile; k != "" && !filepath.IsAbs(k) {
		errs = append(errs, "steam.web_api_key_file: must be an absolute path")
	}
	for name, v := range map[string]*int{"serve.ip_retention_days": c.Serve.IPRetentionDays, "serve.vacuum_days": c.Serve.VacuumDays, "serve.session_retention_days": c.Serve.SessionRetentionDays} {
		if v != nil && *v < 0 {
			errs = append(errs, fmt.Sprintf("%s: must be 0 (off) or a number of days", name))
		}
	}
	for _, l := range []struct {
		name, addr string
		insecure   bool
		tls        bool
	}{
		{"serve.listen", c.Serve.Listen, c.Serve.AllowInsecureHTTP, c.Serve.TLS.CertFile != ""},
		{"serve.mod_listen", c.Serve.ModListen, c.Serve.AllowInsecureHTTP, false},
		{"web.listen", c.Web.Listen, c.Web.AllowInsecureHTTP, c.Web.TLS.CertFile != ""},
	} {
		if err := CheckListen(l.addr, l.insecure, l.tls); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", l.name, err))
		}
	}
	errs = append(errs, c.checkLogins()...)

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

// checkTLS checks that a tls block is complete.
func checkTLS(name string, t TLS) []string {
	var errs []string
	if (t.CertFile == "") != (t.KeyFile == "") {
		errs = append(errs, name+": cert_file and key_file go together")
	}
	if t.ClientCAFile != "" && t.CertFile == "" {
		errs = append(errs, name+".client_ca_file needs cert_file and key_file")
	}
	return errs
}

// loopbackHost reports whether host names this machine.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkLogins checks the settings of the web interface and of user logins.
func (c *Config) checkLogins() []string {
	var errs []string
	if c.Web.RemovedBackends != nil {
		errs = append(errs, "web.backends is no longer supported: one web interface serves one installation, see the migration notes (docs/migration.rst)")
	}
	if c.Web.RemovedUserHeader != nil {
		errs = append(errs, "web.user_header is no longer supported: dzo web has its own login now, see the migration notes (docs/migration.rst)")
	}
	if v := c.Serve.WebURL; v != "" {
		u, err := url.Parse(v)
		switch {
		case err != nil || u.Host == "" || u.User != nil:
			errs = append(errs, fmt.Sprintf("serve.web_url: %q is not an address like https://dzo.example", v))
		case u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery:
			errs = append(errs, fmt.Sprintf("serve.web_url: %q must be scheme and host only, without path, query or fragment", v))
		case u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname())):
			errs = append(errs, fmt.Sprintf("serve.web_url: %q must be https (plain http only for localhost)", v))
		}
	}
	if v := c.Serve.Login.SessionIdleHours; v != nil && *v <= 0 {
		errs = append(errs, "serve.login.session_idle_hours: must be a positive number of hours")
	}
	if v := c.Serve.Login.SessionMaxDays; v != nil && *v <= 0 {
		errs = append(errs, "serve.login.session_max_days: must be a positive number of days")
	}
	api := c.Web.API
	switch {
	case (api.URL == "") != (api.TokenFile == ""):
		errs = append(errs, "web.api: url and token_file go together")
	case api.URL == "" && api.CAFile != "":
		errs = append(errs, "web.api.ca_file needs web.api.url and web.api.token_file")
	case api.URL != "":
		u, err := url.Parse(api.URL)
		switch {
		case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
			errs = append(errs, fmt.Sprintf("web.api.url: %q is not an http(s) address", api.URL))
		case u.Scheme == "http" && !loopbackHost(u.Hostname()):
			errs = append(errs, fmt.Sprintf("web.api.url: %q must be https (plain http only for localhost)", api.URL))
		}
		if !filepath.IsAbs(api.TokenFile) {
			errs = append(errs, "web.api.token_file: must be an absolute path")
		}
	}
	for _, e := range c.Web.TrustedProxies {
		if _, err := netip.ParsePrefix(e); err != nil {
			if _, err := netip.ParseAddr(e); err != nil {
				errs = append(errs, fmt.Sprintf("web.trusted_proxies: %q is not an address or a CIDR network", e))
			}
		}
	}
	return errs
}

// CheckListen validates a listen address: host:port, and plain HTTP only on
// a loopback address unless tls or insecure is set (§C12: the web interface
// and the mod endpoint sit behind a reverse proxy or on localhost).
func CheckListen(addr string, insecure, tls bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("%q is not host:port", addr)
	}
	if insecure || tls || loopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%q is not a loopback address; set tls, put dzo behind a reverse proxy with HTTPS or set allow_insecure_http", addr)
}

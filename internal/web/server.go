// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web is the dzo web interface (§C12): server-rendered pages with
// htmx and a Leaflet map. It reaches every installation only through its
// /api/v1 (internal/apiclient), never through the core packages, so one web
// interface can serve several installations and can run on another host.
//
// There is no login in this package yet: the interface acts with the token of
// each backend and must be reachable only on localhost or behind an
// authenticating reverse proxy, whose user header (Options.UserHeader) is
// passed to the API as the audit actor.
package web

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Backend is one dzo installation.
type Backend struct {
	Name   string
	Client *apiclient.Client
}

// Options configure the web interface.
type Options struct {
	Version string
	// Assets holds htmx/htmx.min.js and leaflet/leaflet.{js,css} (Debian's
	// libjs-htmx and libjs-leaflet under /usr/share/javascript).
	Assets string
	// DocsDir is the built Sphinx site, served under /docs/ when set.
	DocsDir string
	// FlagsDir holds the country flags, <code>.svg (Debian's iso-flags-svg country-4x3); empty serves none.
	FlagsDir string
	// UserHeader is the header an authenticating proxy puts the user in.
	UserHeader string
}

// Server is the web interface.
type Server struct {
	backends []*Backend
	opts     Options
	csrf     string
	tmpl     *template.Template
	navc     navCache
	now      func() time.Time // the clock, replaced by tests
}

// New returns a Server over the given backends.
func New(backends []*Backend, opts Options) (*Server, error) {
	if len(backends) == 0 {
		return nil, fmt.Errorf("web: no backends configured")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	t, err := template.New("").Funcs(template.FuncMap{"pct": pct, "deref": func(f *float64) float64 { return *f }, "join": strings.Join, "expiry": moderation.FormatExpiry, "facts": factsOf}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{backends: backends, opts: opts, csrf: hex.EncodeToString(raw[:]), tmpl: t, now: time.Now}, nil
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func (s *Server) backend(name string) *Backend {
	for _, b := range s.backends {
		if b.Name == name {
			return b
		}
	}
	return nil
}

// Handler returns the web interface's http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	if s.opts.Assets != "" {
		mux.Handle("GET /vendor/", http.StripPrefix("/vendor/", http.FileServer(http.Dir(s.opts.Assets))))
	}
	if s.opts.DocsDir != "" {
		mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.Dir(s.opts.DocsDir))))
	}
	mux.HandleFunc("GET /flags/{code}", s.flag)
	mux.HandleFunc("GET /{$}", s.dashboard)

	const inst = "/b/{backend}/i/{instance}"
	mux.HandleFunc("GET /b/{backend}/audit", s.audit)
	mux.HandleFunc("GET /b/{backend}/bans", s.bansPage)
	mux.HandleFunc("GET /b/{backend}/bans/new", s.banForm)
	mux.HandleFunc("POST /b/{backend}/bans", s.createBan)
	mux.HandleFunc("GET /b/{backend}/bans/{id}", s.banPage)
	mux.HandleFunc("POST /b/{backend}/bans/{id}", s.updateBan)
	mux.HandleFunc("POST /b/{backend}/bans/{id}/revoke", s.revokeBan)
	mux.HandleFunc("POST /b/{backend}/bans/{id}/evidence", s.addEvidence)
	mux.HandleFunc("POST /b/{backend}/bans/{id}/evidence/link", s.linkEvidence)
	mux.HandleFunc("POST /b/{backend}/bans/{id}/evidence/{eid}/unlink", s.unlinkEvidence)
	mux.HandleFunc("GET /b/{backend}/evidence/{id}/file", s.evidenceFile)
	mux.HandleFunc("GET "+inst, s.instance)
	mux.HandleFunc("GET "+inst+"/players", s.playersFragment)
	mux.HandleFunc("GET "+inst+"/vehicles", s.vehiclesFragment)
	mux.HandleFunc("GET "+inst+"/players/{steamid}/panel", s.playerPanel)
	mux.HandleFunc("GET "+inst+"/players/{steamid}/info", s.playerInfo)
	mux.HandleFunc("GET "+inst+"/vehicles/{id}/panel", s.vehiclePanel)
	mux.HandleFunc("GET "+inst+"/types", s.typesFragment)
	mux.HandleFunc("GET "+inst+"/vehicle-types", s.vehicleTypesFragment)
	mux.HandleFunc("GET /b/{backend}/tiles/{map}/{rest...}", s.tiles)
	mux.HandleFunc("GET "+inst+"/map", s.mapPage)
	mux.HandleFunc("GET "+inst+"/events", s.eventsPage)
	mux.HandleFunc("GET "+inst+"/feed", s.feedFragment)
	mux.HandleFunc("GET "+inst+"/stream", s.stream)
	mux.HandleFunc("POST "+inst+"/message", s.act(actMessage))
	mux.HandleFunc("POST "+inst+"/players/{steamid}/message", s.act(actMessage))
	mux.HandleFunc("POST "+inst+"/players/{steamid}/teleport", s.act(actTeleport))
	mux.HandleFunc("POST "+inst+"/players/{steamid}/give", s.act(actGive))
	mux.HandleFunc("POST "+inst+"/spawn", s.act(actSpawn))
	mux.HandleFunc("POST "+inst+"/spawn-vehicle", s.act(actSpawnVehicle))
	mux.HandleFunc("POST "+inst+"/players/{steamid}/kick", s.act(actKick))
	mux.HandleFunc("POST "+inst+"/players/{steamid}/heal", s.act(actHeal))
	mux.HandleFunc("POST "+inst+"/players/{steamid}/fixgear", s.act(actFixGear))
	mux.HandleFunc("POST "+inst+"/players/{steamid}/kill", s.act(actKill))
	mux.HandleFunc("POST "+inst+"/vehicles/{id}/repair", s.act(actRepair))
	mux.HandleFunc("POST "+inst+"/vehicles/{id}/flip", s.act(actFlip))
	mux.HandleFunc("POST "+inst+"/flags/{id}/raise", s.act(actRaise))
	mux.HandleFunc("POST "+inst+"/vehicles/{id}/delete", s.act(actDelete))
	return secure(mux)
}

// secure adds the browser hardening headers (§C12): a strict CSP without
// inline scripts, no framing, no sniffing.
func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

// csrfOK checks a mutating request: the per-process token that every page
// hands to htmx and the map script, plus a same-origin check.
func (s *Server) csrfOK(r *http.Request) bool {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrf)) != 1 {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

// actor is who the audit log blames: the proxy-authenticated user if one is
// configured, else just "web".
func (s *Server) actor(r *http.Request) string {
	if s.opts.UserHeader != "" {
		if u := strings.TrimSpace(r.Header.Get(s.opts.UserHeader)); u != "" {
			return u
		}
	}
	return "web"
}

func (s *Server) client(b *Backend, r *http.Request) *apiclient.Client {
	return b.Client.WithActor(s.actor(r), "web")
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// page is the data every full page shares.
type page struct {
	Title    string
	CSRF     string
	Backends []*Backend
	Backend  string
	Version  string
	Class    string // body class; "map" makes the page fill the window
	Data     any

	Nav        []navBackend // the instances of every backend, for the switcher
	Instance   string       // the instance the page is about, "" outside instance pages
	Section    string       // overview, map, events, players, bans, audit or dashboard
	Home       string       // the backend the Bans and Audit log links go to
	Now        time.Time    // when the page was made, for the facts
	Facts      *factsView   // the facts of Instance, in the instance bar
	HasPlayers bool         // the player database is served: the header links it
}

func (s *Server) page(r *http.Request, title, backend, instance, section string, data any) page {
	home := backend
	if home == "" {
		home = s.backends[0].Name
	}
	return page{Home: home, Title: title, CSRF: s.csrf, Backends: s.backends, Backend: backend, Version: s.opts.Version, Data: data,
		Now: s.now(), Nav: s.nav(r.Context()), Instance: instance, Section: section}
}

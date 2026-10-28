// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web is the dzo web interface (§C12): server-rendered pages with
// htmx and a Leaflet map. It reaches the installation only through its
// /api/v1 (internal/apiclient), never through the core packages, so it can
// run on another host.
//
// People log in here (a password or Steam, then a second factor), but dzo
// serve runs every step and holds the sessions: this package relays the steps
// with its web token, keeps the session secret in a cookie and sends it back
// with every API call. It stores nothing else.
package web

import (
	"crypto/subtle"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/auth"
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
	// TrustedProxies are the reverse proxies whose X-Forwarded-For is believed when working out the
	// browser's address for the login log and the audit log.
	TrustedProxies auth.Proxies
}

// Server is the web interface.
type Server struct {
	backends []*Backend
	opts     Options
	tmpl     *template.Template
	navc     navCache
	now      func() time.Time // the clock, replaced by tests

	infos       infoCache
	webURLCache atomic.Pointer[webURLInfo]
}

// New returns a Server over the one installation b. The routes keep the
// installation's name in their path (/b/{name}/...).
func New(b *Backend, opts Options) (*Server, error) {
	if b == nil {
		return nil, fmt.Errorf("web: no backend configured")
	}
	t, err := template.New("").Funcs(template.FuncMap{"pct": pct, "deref": func(f *float64) float64 { return *f }, "join": strings.Join, "expiry": moderation.FormatExpiry, "facts": factsOf, "day": func(t time.Time) string { return t.UTC().Format("2006-01-02") }, "stamp": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }, "ago": ago, "hours": hours}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{backends: []*Backend{b}, opts: opts, tmpl: t, now: time.Now}, nil
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
	// root holds what needs no login: the login pages and the files they use. Everything else is the
	// application in mux, behind the check of the session.
	root := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	root.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	if s.opts.Assets != "" {
		root.Handle("GET /vendor/", http.StripPrefix("/vendor/", http.FileServer(http.Dir(s.opts.Assets))))
	}
	if s.opts.DocsDir != "" {
		root.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.Dir(s.opts.DocsDir))))
	} else {
		root.Handle("GET /docs/", http.NotFoundHandler())
	}
	root.HandleFunc("GET /flags/{code}", s.flag)
	root.HandleFunc("GET /login", s.loginPage)
	root.HandleFunc("POST /login", s.loginPassword)
	root.HandleFunc("GET /login/steam", s.loginSteam)
	root.HandleFunc("GET /login/steam/return", s.loginSteamReturn)
	root.HandleFunc("GET /login/second", s.loginSecondPage)
	root.HandleFunc("POST /login/second", s.loginSecond)
	root.HandleFunc("POST /login/passkey/begin", s.passkeyBegin)
	root.HandleFunc("POST /login/passkey/finish", s.passkeyFinish)
	root.HandleFunc("GET /login/enrol", s.enrolPage)
	root.HandleFunc("POST /login/enrol", s.enrol)
	root.HandleFunc("POST /login/enrol/passkey/begin", s.enrolPasskeyBegin)
	root.HandleFunc("POST /login/enrol/passkey/finish", s.enrolPasskeyFinish)

	mux := http.NewServeMux()
	root.Handle("/", s.requireSession(mux))
	mux.HandleFunc("POST /logout", s.logout)
	s.accountRoutes(mux)
	s.usersRoutes(mux)
	mux.HandleFunc("GET /{$}", s.dashboard)

	const inst = "/b/{backend}/i/{instance}"
	mux.HandleFunc("GET /b/{backend}/audit", s.audit)
	mux.HandleFunc("GET /b/{backend}/players", s.playersPage)
	mux.HandleFunc("GET /b/{backend}/players/{steamid}", s.playerPage)
	mux.HandleFunc("POST /b/{backend}/players/{steamid}/lookup", s.lookupPlayer)
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
	mux.HandleFunc("POST "+inst+"/vehicles/{id}/move", s.act(actMoveVehicle))
	mux.HandleFunc("POST "+inst+"/flags/{id}/raise", s.act(actRaise))
	mux.HandleFunc("POST "+inst+"/vehicles/{id}/delete", s.act(actDelete))
	return secure(root)
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

// csrfOK checks a mutating request of a logged-in person: the token of their session, which every page hands to
// htmx and the map script (or a form carries), plus a same-origin check.
func (s *Server) csrfOK(r *http.Request) bool {
	d := sessionFrom(r.Context())
	if d == nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(requestCSRF(r)), []byte(csrfFor(d.secret))) == 1 && sameOrigin(r)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	s.renderCode(w, http.StatusOK, name, data)
}

// renderCode renders a template with a status code.
func (s *Server) renderCode(w http.ResponseWriter, code int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
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
	UserName   string       // the person logged in, for the header
	CanUsers   bool         // the person may manage people: the header links the users page
}

func (s *Server) page(r *http.Request, title, backend, instance, section string, data any) page {
	home := backend
	if home == "" {
		home = s.backends[0].Name
	}
	csrf := ""
	if d := sessionFrom(r.Context()); d != nil {
		csrf = csrfFor(d.secret)
	}
	pg := page{Home: home, Title: title, CSRF: csrf, Backends: s.backends, Backend: backend, Version: s.opts.Version, Data: data,
		Now: s.now(), Nav: s.navFor(r), Instance: instance, Section: section, HasPlayers: true}
	if u := infoOf(r).User; u != nil {
		pg.UserName = u.Name
		if pg.UserName == "" {
			pg.UserName = u.SteamID
		}
	}
	pg.CanUsers = holds(r, api.PermUsersManage)
	return pg
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type bansView struct {
	Backend, State, Query string
	Bans                  []moderation.Ban
	Now                   time.Time
	Err                   string
}

func (s *Server) backendOf(w http.ResponseWriter, r *http.Request) (*Backend, *apiclient.Client, bool) {
	b := s.backend(r.PathValue("backend"))
	if b == nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return b, s.client(b, r), true
}

func (s *Server) bansPage(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	v := bansView{Backend: b.Name, State: r.URL.Query().Get("state"), Query: r.URL.Query().Get("q"), Now: time.Now()}
	var err error
	if v.Bans, err = c.Bans(r.Context(), apiclient.BanFilter{State: v.State, Query: v.Query, Limit: 200}); err != nil {
		v.Err = err.Error()
	}
	s.render(w, "bans", s.page("Bans", b.Name, v))
}

type banFormView struct {
	Backend, SteamID, Instance string
	Instances                  []admin.Status
	Others                     []string
	Identities                 []moderation.Sighting
	Evidence                   []moderation.Evidence
	CanIP, CanGlobal           bool
	Err                        string
}

func (s *Server) banForm(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	v := banFormView{Backend: b.Name, SteamID: q.Get("steam_id"), Instance: q.Get("instance")}
	for _, o := range s.backends {
		if o.Name != b.Name {
			v.Others = append(v.Others, o.Name)
		}
	}
	var err error
	if v.Instances, err = c.Instances(r.Context()); err != nil {
		v.Err = err.Error()
	}
	if info, err := c.Info(r.Context()); err == nil {
		v.CanIP, v.CanGlobal = info.Role.Can(api.PermBanIP), info.Role.Can(api.PermBanGlobal)
	}
	if moderation.ValidSteamID(v.SteamID) {
		v.Identities, _ = c.Identities(r.Context(), v.SteamID) // optional extras; the form works without them
		v.Evidence, _ = c.Evidence(r.Context(), moderation.Who{SteamID: v.SteamID})
	}
	s.render(w, "banform", s.page("Ban a player", b.Name, v))
}

// formInput reads the ban fields of a form. duration "keep" leaves keep's expiry.
func formInput(r *http.Request, keep *time.Time) (moderation.BanInput, error) {
	f := r.PostForm
	in := moderation.BanInput{SteamID: strings.TrimSpace(f.Get("steam_id")), Reason: f.Get("reason"), Message: f.Get("message"),
		Scope: moderation.Scope(f.Get("scope")), IPs: f["ips"], IPRefs: f["ip_refs"]}
	if g := strings.TrimSpace(f.Get("guid")); g != "" {
		in.GUIDs = []string{g}
	}
	if in.Scope == moderation.ScopeInstances {
		in.Instances = f["instances"]
	}
	for _, e := range f["evidence"] {
		id, err := strconv.ParseInt(e, 10, 64)
		if err != nil {
			return in, fmt.Errorf("invalid evidence id %q", e)
		}
		in.Evidence = append(in.Evidence, id)
	}
	d := f.Get("duration")
	if d == "custom" {
		d = strings.TrimSpace(f.Get("duration_custom"))
	}
	if d == "keep" {
		in.ExpiresAt = keep
		return in, nil
	}
	exp, err := moderation.ExpiryFrom(d, time.Now())
	in.ExpiresAt = exp
	return in, err
}

type banOutcome struct {
	Backend string
	ID      int64
	Sync    map[string]string
	Err     string
}

func (s *Server) createBan(w http.ResponseWriter, r *http.Request) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	b, _, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	in, err := formInput(r, nil)
	if err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	targets := []*Backend{b}
	for _, name := range r.PostForm["also"] {
		if o := s.backend(name); o != nil && o != b && !slices.Contains(targets, o) {
			targets = append(targets, o)
		}
	}
	var out []banOutcome
	for i, t := range targets {
		tin := in
		if i > 0 {
			tin.Evidence = nil // evidence ids belong to the first backend's database
		}
		o := banOutcome{Backend: t.Name}
		if res, err := s.client(t, r).CreateBan(r.Context(), tin); err != nil {
			o.Err = err.Error()
		} else {
			o.ID, o.Sync = res.Ban.ID, res.Sync
		}
		out = append(out, o)
	}
	s.render(w, "banresult", out)
}

type banView struct {
	Backend  string
	Ban      moderation.Ban
	History  []moderation.HistoryEntry
	Evidence []moderation.Evidence
	Now      time.Time
	Err      string
}

func banID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Server) banPage(w http.ResponseWriter, r *http.Request) {
	b, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id, ok := banID(w, r)
	if !ok {
		return
	}
	v := banView{Backend: b.Name, Now: time.Now()}
	var err error
	if v.Ban, err = c.Ban(r.Context(), id); err != nil {
		v.Err = err.Error()
	} else {
		v.History, _ = c.BanHistory(r.Context(), id) // shown when it loads
		for _, e := range v.Ban.Evidence {
			if ev, err := c.EvidenceItem(r.Context(), e); err == nil {
				v.Evidence = append(v.Evidence, ev)
			}
		}
	}
	s.render(w, "ban", s.page(fmt.Sprintf("Ban #%d", id), b.Name, v))
}

func (s *Server) updateBan(w http.ResponseWriter, r *http.Request) {
	s.banAction(w, r, func(c *apiclient.Client, id int64) (string, error) {
		old, err := c.Ban(r.Context(), id)
		if err != nil {
			return "", err
		}
		in, err := formInput(r, old.ExpiresAt)
		if err != nil {
			return "", err
		}
		res, err := c.UpdateBan(r.Context(), id, in)
		return syncText("changed", res.Sync), err
	})
}

func (s *Server) revokeBan(w http.ResponseWriter, r *http.Request) {
	s.banAction(w, r, func(c *apiclient.Client, id int64) (string, error) {
		res, err := c.RevokeBan(r.Context(), id, r.PostForm.Get("reason"))
		return syncText("revoked", res.Sync), err
	})
}

func syncText(verb string, sync map[string]string) string {
	var parts []string
	for n, st := range sync {
		parts = append(parts, n+": "+st)
	}
	slices.Sort(parts)
	if len(parts) == 0 {
		return verb
	}
	return verb + " (" + strings.Join(parts, ", ") + ")"
}

func (s *Server) banAction(w http.ResponseWriter, r *http.Request, f func(*apiclient.Client, int64) (string, error)) {
	if !s.csrfOK(r) {
		http.Error(w, "bad CSRF token", http.StatusForbidden)
		return
	}
	_, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id, ok := banID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	text, err := f(c, id)
	if err != nil {
		s.render(w, "flash", flash{Text: err.Error()})
		return
	}
	s.render(w, "flash", flash{OK: true, Text: text})
}

// evidenceFile passes an evidence image through, with the API's type and never sniffed.
func (s *Server) evidenceFile(w http.ResponseWriter, r *http.Request) {
	_, c, ok := s.backendOf(w, r)
	if !ok {
		return
	}
	id, ok := banID(w, r)
	if !ok {
		return
	}
	resp, err := c.EvidenceFile(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	ct := resp.Header.Get("Content-Type")
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, moderation.MaxImage)) // a broken transfer only breaks this image
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
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
	CanBan                bool // may ban on at least one instance
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
	v := bansView{Backend: b.Name, State: r.URL.Query().Get("state"), Query: r.URL.Query().Get("q"), Now: time.Now(), CanBan: canAnywhere(r).BanInstance}
	var err error
	if v.Bans, err = c.Bans(r.Context(), apiclient.BanFilter{State: v.State, Query: v.Query, Limit: 200}); err != nil {
		v.Err = err.Error()
	}
	s.render(w, "bans", s.page(r, "Bans", b.Name, "", "bans", v))
}

type banFormView struct {
	Backend, SteamID, Instance string
	Instances                  []admin.Status
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
	var err error
	if v.Instances, err = c.Instances(r.Context()); err != nil {
		v.Err = err.Error()
	}
	v.CanGlobal = holds(r, api.PermBanGlobal)
	if v.Instance != "" {
		v.CanIP = roleOn(infoOf(r), v.Instance).Can(api.PermBanIP)
	} else {
		v.CanIP = infoOf(r).Role.Can(api.PermBanIP)
	}
	if moderation.ValidSteamID(v.SteamID) {
		v.Identities, _ = c.Identities(r.Context(), v.SteamID) // optional extras; the form works without them
		v.Evidence, _ = c.Evidence(r.Context(), moderation.Who{SteamID: v.SteamID})
	}
	s.render(w, "banform", s.page(r, "Ban a player", b.Name, "", "bans", v))
}

// formInput reads the ban fields of a form. duration "keep" leaves keep's expiry.
func formInput(r *http.Request, keep *time.Time) (moderation.BanInput, error) {
	f := r.PostForm
	in := moderation.BanInput{SteamID: strings.TrimSpace(f.Get("steam_id")), Reason: f.Get("reason"), Message: f.Get("message"),
		Scope: moderation.Scope(f.Get("scope")), IPs: f["ips"], IPRefs: f["ip_refs"]}
	if g := strings.TrimSpace(f.Get("guid")); g != "" {
		in.GUIDs = []string{g}
	}
	switch f.Get("scope_choice") { // the form's three choices; the edit form sends scope and instances itself
	case "this":
		in.Scope, in.Instances = moderation.ScopeInstances, []string{strings.TrimSpace(f.Get("this_instance"))}
	case "global":
		in.Scope = moderation.ScopeGlobal
	case "chosen":
		in.Scope, in.Instances = moderation.ScopeInstances, f["instances"]
	default:
		if in.Scope == moderation.ScopeInstances {
			in.Instances = f["instances"]
		}
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
	o := banOutcome{Backend: b.Name}
	if res, err := s.client(b, r).CreateBan(r.Context(), in); err != nil {
		o.Err = err.Error()
	} else {
		o.ID, o.Sync = res.Ban.ID, res.Sync
	}
	s.render(w, "banresult", []banOutcome{o})
}

type banView struct {
	Backend  string
	Ban      moderation.Ban
	History  []moderation.HistoryEntry
	Evidence []moderation.Evidence
	Unlinked []moderation.Evidence // the player's other evidence, which can be linked
	Instance []string              // where evidence can be filed
	Now      time.Time
	CanBan   bool // may change the ban: the API decides in the end
	CanFile  bool // may file evidence
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
	v := banView{Backend: b.Name, Now: time.Now(), CanBan: canAnywhere(r).BanInstance, CanFile: canAnywhere(r).EvidenceAdd}
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
		if who := evidenceWho(v.Ban.Input()); who != (moderation.Who{}) {
			all, _ := c.Evidence(r.Context(), who) // optional: the page works without the offer
			for _, ev := range all {
				if !slices.Contains(v.Ban.Evidence, ev.ID) {
					v.Unlinked = append(v.Unlinked, ev)
				}
			}
		}
		if v.Ban.Scope == moderation.ScopeInstances {
			v.Instance = v.Ban.Instances
		} else if all, err := c.Instances(r.Context()); err == nil {
			for _, i := range all {
				v.Instance = append(v.Instance, i.Instance)
			}
		}
	}
	s.render(w, "ban", s.page(r, fmt.Sprintf("Ban #%d", id), b.Name, "", "bans", v))
}

// evidenceWho names the player a ban is about, for filing and finding evidence: the Steam ID, else a GUID.
func evidenceWho(in moderation.BanInput) moderation.Who {
	switch {
	case in.SteamID != "":
		return moderation.Who{SteamID: in.SteamID}
	case len(in.GUIDs) > 0:
		return moderation.Who{GUID: in.GUIDs[0]}
	}
	return moderation.Who{}
}

// relink changes the evidence linked to a ban and keeps everything else as it is.
func relink(r *http.Request, c *apiclient.Client, id int64, change func([]int64) []int64) (string, error) {
	old, err := c.Ban(r.Context(), id)
	if err != nil {
		return "", err
	}
	in := old.Input()
	in.Evidence = change(in.Evidence)
	res, err := c.UpdateBan(r.Context(), id, in)
	return syncText("", res.Sync), err
}

func (s *Server) linkEvidence(w http.ResponseWriter, r *http.Request) {
	s.banAction(w, r, func(c *apiclient.Client, id int64) (string, error) {
		var add []int64
		for _, e := range r.PostForm["evidence"] {
			n, err := strconv.ParseInt(e, 10, 64)
			if err != nil {
				return "", fmt.Errorf("invalid evidence id %q", e)
			}
			add = append(add, n)
		}
		_, err := relink(r, c, id, func(cur []int64) []int64 {
			for _, n := range add {
				if !slices.Contains(cur, n) {
					cur = append(cur, n)
				}
			}
			return cur
		})
		if err == nil {
			w.Header().Set("HX-Refresh", "true")
		}
		return "linked", err
	})
}

func (s *Server) unlinkEvidence(w http.ResponseWriter, r *http.Request) {
	s.banAction(w, r, func(c *apiclient.Client, id int64) (string, error) {
		eid, err := strconv.ParseInt(r.PathValue("eid"), 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid evidence id %q", r.PathValue("eid"))
		}
		_, err = relink(r, c, id, func(cur []int64) []int64 {
			return slices.DeleteFunc(slices.Clone(cur), func(n int64) bool { return n == eid })
		})
		if err == nil {
			w.Header().Set("HX-Refresh", "true")
		}
		return "unlinked", err
	})
}

// addEvidence files a note, a log or a screenshot against the player of a ban and links it to the ban.
func (s *Server) addEvidence(w http.ResponseWriter, r *http.Request) {
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
	fail := func(err error) { s.render(w, "flash", flash{Text: err.Error()}) }
	r.Body = http.MaxBytesReader(w, r.Body, moderation.MaxImage+moderation.MaxEvidenceBody+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is capped by the MaxBytesReader above
		fail(fmt.Errorf("the upload is too large or broken: %v", err))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	old, err := c.Ban(r.Context(), id)
	if err != nil {
		fail(err)
		return
	}
	who := evidenceWho(old.Input())
	if who == (moderation.Who{}) {
		fail(errors.New("the ban has no player to file evidence against"))
		return
	}
	in := moderation.EvidenceInput{SteamID: who.SteamID, GUID: who.GUID, Instance: r.FormValue("instance"),
		Kind: moderation.EvidenceKind(r.FormValue("kind")), Body: r.FormValue("body")}
	var ev moderation.Evidence
	if in.Kind == moderation.EvidenceImage {
		f, _, ferr := r.FormFile("file")
		if ferr != nil {
			fail(errors.New("choose an image to upload"))
			return
		}
		defer func() { _ = f.Close() }()
		img, ferr := io.ReadAll(io.LimitReader(f, moderation.MaxImage+1))
		if ferr != nil {
			fail(ferr)
			return
		}
		ev, err = c.AddEvidenceImage(r.Context(), in, img)
	} else {
		ev, err = c.AddEvidence(r.Context(), in)
	}
	if err != nil {
		fail(err)
		return
	}
	if _, err := relink(r, c, id, func(cur []int64) []int64 { return append(cur, ev.ID) }); err != nil {
		fail(fmt.Errorf("evidence #%d was filed but could not be linked: %v", ev.ID, err))
		return
	}
	w.Header().Set("HX-Refresh", "true")
	s.render(w, "flash", flash{OK: true, Text: fmt.Sprintf("evidence #%d added", ev.ID)})
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

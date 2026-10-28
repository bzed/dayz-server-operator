// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// evidenceJSONMax bounds a JSON evidence body: the text plus room for the other fields.
const evidenceJSONMax = moderation.MaxEvidenceBody + 4<<10

func (s *Server) listEvidence(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	list, err := st.EvidenceFor(r.Context(), moderation.Who{SteamID: q.Get("steam_id"), GUID: q.Get("guid"), IP: q.Get("ip")})
	if err != nil {
		storeFail(w, err)
		return
	}
	out := []moderation.Evidence{}
	for _, e := range list {
		if canOnRecord(c, e.Instance, PermBanView) {
			out = append(out, maskEvidence(c, e))
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) addEvidence(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	var in moderation.EvidenceInput
	var image []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, moderation.MaxImage+evidenceJSONMax)
		if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is capped by the MaxBytesReader above
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				fail(w, http.StatusRequestEntityTooLarge, "the upload is larger than %d MiB", moderation.MaxImage>>20)
				return
			}
			fail(w, http.StatusBadRequest, "bad upload: %v", err)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		in = moderation.EvidenceInput{SteamID: r.FormValue("steam_id"), GUID: r.FormValue("guid"), IP: r.FormValue("ip"),
			Instance: r.FormValue("instance"), Kind: moderation.EvidenceKind(r.FormValue("kind")), Body: r.FormValue("body")}
		f, _, err := r.FormFile("file")
		if err != nil {
			fail(w, http.StatusBadRequest, "the upload has no file")
			return
		}
		defer func() { _ = f.Close() }()
		if image, err = io.ReadAll(io.LimitReader(f, moderation.MaxImage+1)); err != nil {
			fail(w, http.StatusBadRequest, "reading the upload: %v", err)
			return
		}
	} else {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, evidenceJSONMax))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			fail(w, http.StatusBadRequest, "bad request body: %v", err)
			return
		}
	}
	if _, ok := s.Hubs.Hub(in.Instance); !ok || !c.who.Allowed(in.Instance) {
		fail(w, http.StatusBadRequest, "unknown instance %q", in.Instance)
		return
	}
	if !c.who.Can(in.Instance, PermEvidenceAdd) {
		forbidden(w, c.who.RoleOn(in.Instance), PermEvidenceAdd)
		return
	}
	e, err := st.AddEvidence(r.Context(), c.actor, in, image, s.now())
	if err != nil {
		storeFail(w, err)
		return
	}
	_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Instance: e.Instance, Action: "evidence.add",
		Target: firstNonEmpty(e.SteamID, e.GUID, ipLabel(e.IP)), Detail: fmt.Sprintf("evidence #%d %s", e.ID, e.Kind), OK: true}) // stored either way
	writeJSON(w, http.StatusCreated, maskEvidence(c, e))
}

// ipLabel names an address for the audit log without writing it there.
func ipLabel(ip string) string {
	if ip == "" {
		return ""
	}
	return "ip"
}

// canOnRecord reports whether a permission holds for a record of an instance; a record without one needs it on
// any instance.
func canOnRecord(c *call, instance, perm string) bool {
	if instance == "" {
		return c.who.CanAny(perm)
	}
	return c.who.Can(instance, perm)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// loadEvidence reads the {id} item, answering 404 for one from an instance the caller may not touch.
func (s *Server) loadEvidence(w http.ResponseWriter, r *http.Request, c *call) (*moderation.Store, moderation.Evidence, bool) {
	st, ok := s.store(w)
	if !ok {
		return nil, moderation.Evidence{}, false
	}
	id, ok := pathID(w, r)
	if !ok {
		return nil, moderation.Evidence{}, false
	}
	e, err := st.GetEvidence(r.Context(), id)
	if err == nil && !canOnRecord(c, e.Instance, PermBanView) {
		err = moderation.ErrNotFound
	}
	if err != nil {
		storeFail(w, err)
		return nil, moderation.Evidence{}, false
	}
	return st, e, true
}

func (s *Server) getEvidence(w http.ResponseWriter, r *http.Request, c *call) {
	if _, e, ok := s.loadEvidence(w, r, c); ok {
		writeJSON(w, 200, maskEvidence(c, e))
	}
}

// evidenceFile serves an image with its stored type, never sniffed, in a sandbox.
func (s *Server) evidenceFile(w http.ResponseWriter, r *http.Request, c *call) {
	st, e, ok := s.loadEvidence(w, r, c)
	if !ok {
		return
	}
	path, ctype, err := st.EvidenceFile(r.Context(), e.ID)
	if err != nil {
		storeFail(w, err)
		return
	}
	f, err := os.Open(path) //nolint:gosec // a path the store built from a hash
	if err != nil {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f) // the client went away, or the disk failed mid-file: nothing left to answer
}

func (s *Server) identities(w http.ResponseWriter, r *http.Request, c *call) {
	st, ok := s.store(w)
	if !ok {
		return
	}
	list, err := st.Identities(r.Context(), r.PathValue("steamid"))
	if err != nil {
		storeFail(w, err)
		return
	}
	out := []moderation.Sighting{}
	shown := false
	for _, x := range list {
		if canOnRecord(c, x.Instance, PermBanView) {
			if seesIPsOn(c, x.Instance) {
				shown = shown || x.IP != ""
			} else {
				x.IP = ""
			}
			out = append(out, x)
		}
	}
	if shown { // an address history is personal data: who read it is recorded, without the addresses
		_ = s.Audit.Log(admin.AuditEntry{Actor: c.actor, Source: c.src, Client: c.client, Action: "ip.history", Target: r.PathValue("steamid"), OK: true}) // the read goes on if the line cannot be written
	}
	writeJSON(w, 200, out)
}

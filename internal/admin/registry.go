// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxSyncBody = 16 << 20

// Registry holds the hubs of every instance and authenticates the mods: the
// per-instance token is the only guard of the endpoint (§C13), so tokens are
// compared in constant time and failures are rate limited per address.
type Registry struct {
	// TokenDir holds <instance>.token files (0600), written by
	// WriteModConfig at render time and re-read when they change.
	TokenDir string

	mu     sync.Mutex
	hubs   map[string]*Hub
	tokens map[string]cachedToken
	fails  *Limiter
}

type cachedToken struct {
	value string
	mtime time.Time
}

// NewRegistry returns an empty registry reading tokens from tokenDir.
func NewRegistry(tokenDir string) *Registry {
	return &Registry{TokenDir: tokenDir, hubs: map[string]*Hub{}, tokens: map[string]cachedToken{}, fails: NewLimiter(10, time.Minute)}
}

// Add registers a hub. An instance that is already registered keeps its
// state and only takes the new limits, so a reload of the site config does
// not drop the live map.
func (r *Registry) Add(h *Hub) *Hub {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.hubs[h.Name]; ok {
		old.mu.Lock()
		old.Allow, old.Deny, old.MarkerDir, old.Styles, old.NextRestart = h.Allow, h.Deny, h.MarkerDir, h.Styles, h.NextRestart
		old.mu.Unlock()
		return old
	}
	r.hubs[h.Name] = h
	return h
}

// Hub returns the hub of an instance.
func (r *Registry) Hub(name string) (*Hub, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hubs[name]
	return h, ok
}

// Names lists the registered instances, sorted.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.hubs))
	for n := range r.hubs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) token(name string) string {
	path := filepath.Join(r.TokenDir, name+".token")
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.tokens[name]; ok && c.mtime.Equal(info.ModTime()) {
		return c.value
	}
	b, err := os.ReadFile(path) //nolint:gosec // inside the operator's own secrets dir
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	r.tokens[name] = cachedToken{value: v, mtime: info.ModTime()}
	return v
}

// Authenticate finds the instance a token belongs to.
func (r *Registry) Authenticate(token string) (*Hub, bool) {
	if token == "" {
		return nil, false
	}
	var found *Hub
	for _, name := range r.Names() {
		want := r.token(name)
		// Compare every candidate so the time does not tell which instance
		// (or whether any) matched.
		if want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(token)) == 1 {
			found, _ = r.Hub(name)
		}
	}
	return found, found != nil
}

// ModHandler is the endpoint the mods call: POST /mod/v1/sync.
func (r *Registry) ModHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mod/v1/sync", r.sync)
	return mux
}

func writeReply(w http.ResponseWriter, code int, rep SyncReply) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(rep)
}

func (r *Registry) sync(w http.ResponseWriter, req *http.Request) {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	var in SyncRequest
	body := http.MaxBytesReader(w, req.Body, maxSyncBody)
	if err := json.NewDecoder(body).Decode(&in); err != nil {
		writeReply(w, http.StatusBadRequest, SyncReply{Error: "bad request"})
		return
	}
	if r.fails.Blocked(host) {
		writeReply(w, http.StatusTooManyRequests, SyncReply{Error: "too many failures"})
		return
	}
	hub, ok := r.Authenticate(in.Token)
	if !ok {
		r.fails.Allow(host)
		writeReply(w, http.StatusUnauthorized, SyncReply{Error: "invalid token"})
		return
	}
	if in.Proto != ProtocolVersion {
		writeReply(w, http.StatusOK, SyncReply{Error: "protocol mismatch: dzo speaks version " + itoa(ProtocolVersion), Proto: ProtocolVersion})
		return
	}
	in.Normalise()
	writeReply(w, http.StatusOK, hub.Sync(in))
}

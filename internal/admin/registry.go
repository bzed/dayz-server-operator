// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
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

	// Logf receives the connection log: mods connecting, reconnecting, getting lost
	// and being turned away. It never sees a token. Nil logs nothing.
	Logf func(format string, args ...any)

	mu     sync.Mutex
	hubs   map[string]*Hub
	tokens map[string]cachedToken
	fails  *Limiter
	logged map[string]time.Time // last time a refusal was logged, per reason and address
	online map[string]bool      // what CheckLost last saw, per instance
}

// refusalLogEvery is how often one address gets the same refusal logged: a mod
// with a wrong token retries every few seconds.
const refusalLogEvery = time.Minute

type cachedToken struct {
	value string
	mtime time.Time
}

// NewRegistry returns an empty registry reading tokens from tokenDir.
func NewRegistry(tokenDir string) *Registry {
	return &Registry{TokenDir: tokenDir, hubs: map[string]*Hub{}, tokens: map[string]cachedToken{}, fails: NewLimiter(10, time.Minute),
		logged: map[string]time.Time{}, online: map[string]bool{}}
}

// Add registers a hub. An instance that is already registered keeps its
// state and only takes the new limits, so a reload of the site config does
// not drop the live map.
func (r *Registry) Add(h *Hub) *Hub {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.hubs[h.Name]; ok {
		old.mu.Lock()
		old.Allow, old.Deny, old.Kinds, old.MarkerDir, old.Styles, old.NextRestart = h.Allow, h.Deny, h.Kinds, h.MarkerDir, h.Styles, h.NextRestart
		old.mu.Unlock()
		return old
	}
	if h.Logf == nil {
		h.Logf = r.logf
	}
	r.hubs[h.Name] = h
	return h
}

// Retain drops the hubs of the instances that are not in names: an instance removed from the site is
// gone after a reload (its mod is turned away, its vehicle records are forgotten), the others keep their state.
func (r *Registry) Retain(names []string) {
	keep := map[string]bool{}
	for _, n := range names {
		keep[n] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for n := range r.hubs {
		if !keep[n] {
			delete(r.hubs, n)
			delete(r.tokens, n)
			delete(r.online, n)
		}
	}
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

func (r *Registry) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// refuse logs a refusal at most once per refusalLogEvery for the same reason and address.
func (r *Registry) refuse(reason, host, format string, args ...any) {
	key := reason + "|" + host
	r.mu.Lock()
	last, seen := r.logged[key]
	if seen && time.Since(last) < refusalLogEvery {
		r.mu.Unlock()
		return
	}
	r.logged[key] = time.Now()
	r.mu.Unlock()
	r.logf("mod endpoint: refused %s: "+format, append([]any{host}, args...)...)
}

// missingTokens lists the instances without a readable token file: no mod can
// authenticate for them.
func (r *Registry) missingTokens() []string {
	var out []string
	for _, n := range r.Names() {
		if r.token(n) == "" {
			out = append(out, n)
		}
	}
	return out
}

// LogTokens logs which instances have a token file, so an instance whose mod
// can never connect shows at start (and after a reload).
func (r *Registry) LogTokens() {
	for _, n := range r.Names() {
		if r.token(n) == "" {
			r.logf("instance %s: no token file %s, its dzo-admin mod cannot connect (render the instance)", n, filepath.Join(r.TokenDir, n+".token"))
		} else {
			r.logf("instance %s: waiting for its dzo-admin mod", n)
		}
	}
}

// CheckLost logs every instance that was connected on the last call and is not now.
func (r *Registry) CheckLost() {
	for _, n := range r.Names() {
		h, _ := r.Hub(n)
		st := h.Status()
		r.mu.Lock()
		was := r.online[n]
		r.online[n] = st.Connected
		r.mu.Unlock()
		if was && !st.Connected {
			r.logf("instance %s: mod lost, no sync since %s (server stopped, restarting or cannot reach dzo)", n, st.LastSeen.Format(time.RFC3339))
		}
	}
}

// Watch runs CheckLost until ctx is done.
func (r *Registry) Watch(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.CheckLost()
		}
	}
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
		r.refuse("bad", host, "unreadable sync body: %v", err)
		writeReply(w, http.StatusBadRequest, SyncReply{Error: "bad request"})
		return
	}
	if r.fails.Blocked(host) {
		r.refuse("blocked", host, "too many failed tokens, blocked for a minute")
		writeReply(w, http.StatusTooManyRequests, SyncReply{Error: "too many failures"})
		return
	}
	hub, ok := r.Authenticate(in.Token)
	if !ok {
		r.fails.Allow(host)
		if missing := r.missingTokens(); len(missing) > 0 {
			r.refuse("token", host, "token matches no instance (instances without a token file: %s)", strings.Join(missing, ", "))
		} else {
			r.refuse("token", host, "token matches no instance (stale mod config.json after a re-render?)")
		}
		writeReply(w, http.StatusUnauthorized, SyncReply{Error: "invalid token"})
		return
	}
	if in.Proto != ProtocolVersion {
		r.refuse("proto", host, "instance %s speaks protocol %d, dzo %d (update the mod or dzo)", hub.Name, in.Proto, ProtocolVersion)
		writeReply(w, http.StatusOK, SyncReply{Error: "protocol mismatch: dzo speaks version " + itoa(ProtocolVersion), Proto: ProtocolVersion})
		return
	}
	in.Normalise()
	before := hub.Status()
	rep := hub.Sync(in)
	after := hub.Status()
	switch {
	case before.Hello == nil:
		r.logf("instance %s: mod connected from %s (mod %s, protocol %d, world %s)", hub.Name, host, in.ModVersion, in.Proto, in.World)
	case !before.Connected:
		r.logf("instance %s: mod reconnected from %s after %s (mod %s, world %s)", hub.Name, host, after.LastSeen.Sub(before.LastSeen).Round(time.Second), in.ModVersion, in.World)
	case after.Hello != before.Hello:
		r.logf("instance %s: mod restarted (hello from %s, mod %s, world %s)", hub.Name, host, in.ModVersion, in.World)
	}
	r.mu.Lock()
	r.online[hub.Name] = true
	r.mu.Unlock()
	writeReply(w, http.StatusOK, rep)
}

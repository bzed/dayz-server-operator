// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/auth"
)

// navTTL is how long the instance lists of the backends are reused, so a page render does not add one API round
// trip per backend.
const navTTL = 15 * time.Second

type navInstance struct {
	Name, World string
	Connected   bool
}

type navBackend struct {
	Name      string
	Instances []navInstance
	Err       string
}

// navCache keeps the lists per session: each person sees only the instances they have a role on.
type navCache struct {
	mu sync.Mutex
	m  map[string]navEntry
}

type navEntry struct {
	at   time.Time
	list []navBackend
}

// nav returns the instance lists of the backends for a session, at most navTTL old. A backend that fails keeps
// its previous list and carries the error. It holds names, connection state and the world only. c is the API
// client of the session, which decides which instances are listed.
func (s *Server) nav(ctx context.Context, session string, c *apiclient.Client) []navBackend {
	s.navc.mu.Lock()
	defer s.navc.mu.Unlock()
	now := s.now()
	e := s.navc.m[session]
	if e.list != nil && now.Sub(e.at) < navTTL {
		return e.list
	}
	out := make([]navBackend, len(s.backends))
	var wg sync.WaitGroup
	for i, b := range s.backends {
		out[i] = navBackend{Name: b.Name}
		if i < len(e.list) {
			out[i].Instances = e.list[i].Instances
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			st, err := c.Instances(cctx)
			if err != nil {
				out[i].Err = err.Error()
				return
			}
			out[i].Instances = nil
			for _, x := range st {
				ni := navInstance{Name: x.Instance, Connected: x.Connected}
				if x.Hello != nil {
					ni.World = x.Hello.World
				}
				out[i].Instances = append(out[i].Instances, ni)
			}
		}()
	}
	wg.Wait()
	if s.navc.m == nil {
		s.navc.m = map[string]navEntry{}
	}
	for k, old := range s.navc.m { // forget the sessions that have not been seen for a while
		if now.Sub(old.at) > 10*navTTL {
			delete(s.navc.m, k)
		}
	}
	s.navc.m[session] = navEntry{at: now, list: out}
	return out
}

// navFor is the instance list for the person of a request.
func (s *Server) navFor(r *http.Request) []navBackend {
	session := ""
	if d := sessionFrom(r.Context()); d != nil {
		session = d.secret
	}
	return s.nav(r.Context(), auth.HashSecret(session), s.userClient(s.backends[0], r, session))
}

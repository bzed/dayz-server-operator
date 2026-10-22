// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"sync"
	"time"
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

type navCache struct {
	mu   sync.Mutex
	at   time.Time
	list []navBackend
}

// nav returns the instance lists of all backends, at most navTTL old. A backend that fails keeps its previous
// list and carries the error. It holds names, connection state and the world only, the same for every viewer.
func (s *Server) nav(ctx context.Context) []navBackend {
	s.navc.mu.Lock()
	defer s.navc.mu.Unlock()
	now := s.now()
	if s.navc.list != nil && now.Sub(s.navc.at) < navTTL {
		return s.navc.list
	}
	out := make([]navBackend, len(s.backends))
	var wg sync.WaitGroup
	for i, b := range s.backends {
		out[i] = navBackend{Name: b.Name}
		if i < len(s.navc.list) {
			out[i].Instances = s.navc.list[i].Instances
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			st, err := b.Client.Instances(cctx)
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
	s.navc.list, s.navc.at = out, now
	return out
}

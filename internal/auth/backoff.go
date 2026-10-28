// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"sync"
	"time"
)

const (
	backoffFree    = 5
	backoffFirst   = time.Second
	backoffMax     = 15 * time.Minute
	backoffForget  = time.Hour
	backoffMaxKeys = 100000
)

type failures struct {
	n     int
	last  time.Time
	until time.Time
}

// Backoff slows down guessing: per key (an account, a client address) the
// first five failures are free, then the wait doubles from one second to
// fifteen minutes. A key is forgotten after an hour without failures. It lives
// in memory only; it is a speed bump, not a decision.
type Backoff struct {
	now func() time.Time

	mu        sync.Mutex
	keys      map[string]*failures
	lastSweep time.Time
}

// NewBackoff returns an empty Backoff using now as its clock.
func NewBackoff(now func() time.Time) *Backoff {
	return &Backoff{now: now, keys: map[string]*failures{}}
}

// sweep drops the forgotten keys; the caller holds the lock.
func (b *Backoff) sweep(now time.Time) {
	if now.Sub(b.lastSweep) < time.Minute && len(b.keys) < backoffMaxKeys {
		return
	}
	b.lastSweep = now
	for k, f := range b.keys {
		if now.Sub(f.last) > backoffForget {
			delete(b.keys, k)
		}
	}
}

// Wait is how long the caller must still wait: the largest wait of the keys.
func (b *Backoff) Wait(keys ...string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweep(now)
	var wait time.Duration
	for _, k := range keys {
		f, ok := b.keys[k]
		if !ok || now.Sub(f.last) > backoffForget {
			continue
		}
		wait = max(wait, f.until.Sub(now))
	}
	return wait
}

// Fail counts a failure on each key.
func (b *Backoff) Fail(keys ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweep(now)
	for _, k := range keys {
		f, ok := b.keys[k]
		if !ok {
			if len(b.keys) >= backoffMaxKeys {
				continue // under a flood of new keys, keep what is tracked
			}
			f = &failures{}
			b.keys[k] = f
		}
		if now.Sub(f.last) > backoffForget {
			*f = failures{}
		}
		f.n++
		f.last = now
		if f.n > backoffFree {
			d := backoffMax
			if shift := f.n - backoffFree - 1; shift < 20 {
				d = min(backoffFirst<<shift, backoffMax)
			}
			f.until = now.Add(d)
		}
	}
}

// Success forgets the failures of key.
func (b *Backoff) Success(key string) {
	b.mu.Lock()
	delete(b.keys, key)
	b.mu.Unlock()
}

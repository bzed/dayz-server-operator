// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package lookups keeps the cached answers of BattlEye's master server and of Steam about the players in the
// player history fresh, gently: a few requests a minute, one packet a second at most, and a long pause when a
// source stops answering. The answers are information for the player pages; dzo enforces nothing with them.
package lookups

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// The cache lifetimes and limits (see the player database plan). BattlEye is asked rarely and slowly: the
// master is not ours and documents no limit.
const (
	beTTL        = 7 * 24 * time.Hour
	steamTTL     = 24 * time.Hour
	seenWithin   = 30 * 24 * time.Hour
	beGap        = time.Second
	steamKeyGap  = time.Second
	steamPageGap = 2 * time.Second

	defaultLookupTimeout = 30 * time.Second
	beRound              = 10 // GUIDs per round: 30 packets, 30 s
	steamRound           = 20
	beSilentMax          = 3 // rounds in a row without any answer before BattlEye is left alone
	steamFailMax         = 5
	pause                = time.Hour
	lookupGap            = 10 * time.Minute
	maxNudged            = 1000
)

// Store is the part of moderation.Store the refresher uses.
type Store interface {
	DueLookups(ctx context.Context, kind string, checkedBefore, seenAfter time.Time, limit int) ([]string, error)
	SaveBEStatus(ctx context.Context, guid string, st moderation.BEStatus) error
	SaveSteamInfo(ctx context.Context, steamID string, in moderation.SteamInfo) error
}

// BEChecker asks BattlEye's master (*battleye.Master).
type BEChecker interface {
	Check(ctx context.Context, g battleye.Game, guid string) (battleye.GlobalBan, error)
}

// SteamAPI is what the refresher needs of Steam (*steamweb.Client).
type SteamAPI interface {
	Community(ctx context.Context, steamID string) (moderation.SteamInfo, error)
	Bans(ctx context.Context, ids []string) (map[string]moderation.SteamInfo, error)
	Details(ctx context.Context, steamID string, in *moderation.SteamInfo) error
}

// Refresher looks players up. A nil BE or a false SteamOn turns that source off.
type Refresher struct {
	Store Store
	BE    BEChecker
	Steam SteamAPI
	// SteamOn: ask Steam at all. SteamKey: the client has a Web API key (Bans and Details instead of the
	// community page).
	SteamOn, SteamKey bool
	// Now is the clock and Sleep the wait (defaults time.Now and a context-aware sleep); Tick paces the
	// rounds (default: every minute). Tests replace them.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	Tick  <-chan time.Time
	Logf  func(string, ...any)
	// LookupTimeout bounds LookupNow (default 30 s: with a key it is five paced Steam requests and the BattlEye query).
	LookupTimeout time.Duration

	mu         sync.Mutex
	nudged     []string
	lastNow    map[string]time.Time
	busy       map[string]bool
	beNext     time.Time
	steamNext  time.Time
	beSilent   int
	bePause    time.Time
	steamFails int
	steamPause time.Time
	logged     map[string]time.Time
	kick       chan struct{}
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Refresher) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("lookups: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

// logOnce logs a message for a source at most once an hour.
func (r *Refresher) logOnce(source, format string, args ...any) {
	if r.Logf == nil {
		return
	}
	r.mu.Lock()
	if r.logged == nil {
		r.logged = map[string]time.Time{}
	}
	now := r.now()
	if at, ok := r.logged[source]; ok && now.Sub(at) < time.Hour {
		r.mu.Unlock()
		return
	}
	r.logged[source] = now
	r.mu.Unlock()
	r.Logf(format, args...)
}

// pace waits until the next request of a source may go out and books the one after it gap later.
func (r *Refresher) pace(ctx context.Context, next *time.Time, gap time.Duration) error {
	r.mu.Lock()
	now := r.now()
	at := *next
	if at.Before(now) {
		at = now
	}
	*next = at.Add(gap)
	r.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		return r.sleep(ctx, d)
	}
	return nil
}

// SteamWait paces the requests to Steam; the steamweb client calls it before each one (Client.Wait).
func (r *Refresher) SteamWait(ctx context.Context) error {
	gap := steamPageGap
	if r.SteamKey {
		gap = steamKeyGap
	}
	return r.pace(ctx, &r.steamNext, gap)
}

// Nudge asks for a player to be looked up in the next round, ahead of the others, and wakes the loop.
func (r *Refresher) Nudge(steamID string) {
	if !moderation.ValidSteamID(steamID) {
		return
	}
	r.mu.Lock()
	if !slices.Contains(r.nudged, steamID) && len(r.nudged) < maxNudged {
		r.nudged = append(r.nudged, steamID)
	}
	if r.kick == nil {
		r.kick = make(chan struct{}, 1)
	}
	kick := r.kick
	r.mu.Unlock()
	select {
	case kick <- struct{}{}:
	default:
	}
}

// Run does a round every minute (and when nudged) until ctx is done.
func (r *Refresher) Run(ctx context.Context) {
	r.mu.Lock()
	if r.kick == nil {
		r.kick = make(chan struct{}, 1)
	}
	kick := r.kick
	r.mu.Unlock()
	tick := r.Tick
	if tick == nil {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		case <-kick:
		}
		r.round(ctx)
	}
}

// takeNudged returns and clears the nudged Steam IDs.
func (r *Refresher) takeNudged() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.nudged
	r.nudged = nil
	return n
}

func (r *Refresher) round(ctx context.Context) {
	nudged := r.takeNudged()
	if r.BE != nil {
		r.beRound(ctx, nudged)
	}
	if r.SteamOn && r.Steam != nil {
		r.steamRound(ctx, nudged)
	}
}

// front moves the entries of first that are in list to its front.
func front(list, first []string) []string {
	var out []string
	for _, f := range first {
		if slices.Contains(list, f) {
			out = append(out, f)
		}
	}
	for _, l := range list {
		if !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

func (r *Refresher) beRound(ctx context.Context, nudged []string) {
	r.mu.Lock()
	paused := r.now().Before(r.bePause)
	r.mu.Unlock()
	if paused {
		return
	}
	now := r.now()
	due, err := r.Store.DueLookups(ctx, "be", now.Add(-beTTL), now.Add(-seenWithin), beRound)
	if err != nil {
		r.logOnce("be-db", "lookups: %v", err)
		return
	}
	var guids []string
	for _, id := range nudged {
		if g, err := moderation.DeriveGUID(id); err == nil {
			guids = append(guids, g)
		}
	}
	for _, g := range front(due, guids) {
		if ctx.Err() != nil {
			return
		}
		if !slices.Contains(due, g) {
			continue
		}
		answered, err := r.checkBE(ctx, g)
		if err != nil {
			return
		}
		r.mu.Lock()
		if answered {
			r.beSilent = 0
		} else if r.beSilent++; r.beSilent >= beSilentMax {
			r.bePause, r.beSilent = r.now().Add(pause), 0
			r.mu.Unlock()
			r.logOnce("be", "lookups: BattlEye's master did not answer %d times in a row; pausing for %s", beSilentMax, pause)
			return
		}
		r.mu.Unlock()
	}
}

// checkBE asks every game about a GUID and stores the answers. answered is false when no game answered; err
// is set only when the context ended.
func (r *Refresher) checkBE(ctx context.Context, guid string) (answered bool, err error) {
	for _, g := range battleye.Games {
		if err := r.pace(ctx, &r.beNext, beGap); err != nil {
			return answered, err
		}
		res, cerr := r.BE.Check(ctx, g, guid)
		st := moderation.BEStatus{Game: moderation.BEGame(g.ID), State: moderation.LookupOK, Banned: res.Banned, Reason: res.Reason, CheckedAt: r.now()}
		switch {
		case ctx.Err() != nil:
			return answered, fmt.Errorf("lookups: %w", ctx.Err())
		case errors.Is(cerr, battleye.ErrNoAnswer):
			st.State = moderation.LookupUnknown
		case cerr != nil:
			r.logOnce("be-err", "lookups: BattlEye: %v", cerr)
			return answered, fmt.Errorf("lookups: battleye: %w", cerr)
		default:
			answered = true
		}
		if err := r.Store.SaveBEStatus(ctx, guid, st); err != nil {
			r.logOnce("be-db", "lookups: %v", err)
		}
	}
	return answered, nil
}

func (r *Refresher) steamRound(ctx context.Context, nudged []string) {
	r.mu.Lock()
	paused := r.now().Before(r.steamPause)
	r.mu.Unlock()
	if paused {
		return
	}
	now := r.now()
	due, err := r.Store.DueLookups(ctx, "steam", now.Add(-steamTTL), now.Add(-seenWithin), steamRound)
	if err != nil {
		r.logOnce("steam-db", "lookups: %v", err)
		return
	}
	due = front(due, nudged)
	if len(due) == 0 {
		return
	}
	if r.SteamKey {
		r.steamWithKey(ctx, due)
		return
	}
	for _, id := range due {
		if ctx.Err() != nil || !r.steamOK() {
			return
		}
		if err := r.steamCommunity(ctx, id); err != nil {
			r.steamFailed(err)
		}
	}
}

// steamOK reports whether Steam is not paused.
func (r *Refresher) steamOK() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.now().Before(r.steamPause)
}

func (r *Refresher) steamFailed(err error) {
	r.mu.Lock()
	r.steamFails++
	pausing := r.steamFails >= steamFailMax
	if pausing {
		r.steamPause, r.steamFails = r.now().Add(pause), 0
	}
	r.mu.Unlock()
	r.logOnce("steam", "lookups: Steam: %v", err)
	if pausing {
		r.logOnce("steam-pause", "lookups: Steam failed %d times in a row; pausing for %s", steamFailMax, pause)
	}
}

func (r *Refresher) steamWorked() {
	r.mu.Lock()
	r.steamFails = 0
	r.mu.Unlock()
}

func (r *Refresher) steamCommunity(ctx context.Context, id string) error {
	in, err := r.Steam.Community(ctx, id)
	if err != nil {
		return err
	}
	in.CheckedAt = r.now()
	if err := r.Store.SaveSteamInfo(ctx, id, in); err != nil {
		return err
	}
	r.steamWorked()
	return nil
}

func (r *Refresher) steamWithKey(ctx context.Context, ids []string) {
	got, err := r.Steam.Bans(ctx, ids)
	if err != nil {
		r.steamFailed(err)
		return
	}
	r.steamWorked()
	for _, id := range ids {
		if ctx.Err() != nil || !r.steamOK() {
			return
		}
		in, ok := got[id]
		if !ok {
			in = moderation.SteamInfo{State: moderation.LookupUnknown}
		} else if err := r.Steam.Details(ctx, id, &in); err != nil {
			r.steamFailed(err)
		}
		in.CheckedAt = r.now()
		if err := r.Store.SaveSteamInfo(ctx, id, in); err != nil {
			r.logOnce("steam-db", "lookups: %v", err)
		}
	}
}

// LookupNow asks both sources about one player and stores the answers, for the "Check now" button. It
// refuses a player looked up less than ten minutes ago with moderation.ErrLookupTooSoon, and gives up after
// LookupTimeout.
func (r *Refresher) LookupNow(ctx context.Context, steamID string) error {
	guid, err := moderation.DeriveGUID(steamID)
	if err != nil {
		return fmt.Errorf("lookups: %w", err)
	}
	r.mu.Lock()
	if at, ok := r.lastNow[steamID]; (ok && r.now().Sub(at) < lookupGap) || r.busy[steamID] {
		r.mu.Unlock()
		return moderation.ErrLookupTooSoon
	}
	if r.busy == nil {
		r.busy, r.lastNow = map[string]bool{}, map[string]time.Time{}
	}
	r.busy[steamID] = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.busy, steamID); r.mu.Unlock() }()

	timeout := r.LookupTimeout
	if timeout <= 0 {
		timeout = defaultLookupTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if r.BE != nil {
		if _, err := r.checkBE(ctx, guid); err != nil {
			return err
		}
	}
	if r.SteamOn && r.Steam != nil {
		if r.SteamKey {
			got, err := r.Steam.Bans(ctx, []string{steamID})
			if err != nil {
				return fmt.Errorf("lookups: %w", err)
			}
			in, ok := got[steamID]
			if !ok {
				in = moderation.SteamInfo{State: moderation.LookupUnknown}
			} else if err := r.Steam.Details(ctx, steamID, &in); err != nil {
				return fmt.Errorf("lookups: %w", err)
			}
			in.CheckedAt = r.now()
			if err := r.Store.SaveSteamInfo(ctx, steamID, in); err != nil {
				return fmt.Errorf("lookups: %w", err)
			}
		} else if err := r.steamCommunity(ctx, steamID); err != nil {
			return fmt.Errorf("lookups: %w", err)
		}
	}
	r.mu.Lock()
	r.lastNow[steamID] = r.now()
	r.mu.Unlock()
	return nil
}

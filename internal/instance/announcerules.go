// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// AnnounceRule is one rule of announce.ways (see site.AnnounceWay): the way the countdown reaches
// the players while at most Within is left until the kick (0 = the whole countdown), and when.
type AnnounceRule struct {
	Within time.Duration
	Way    string // rcon, chat, notification or expansion
	Type   string // the Expansion notification type of way expansion
	Every  time.Duration
	At     []time.Duration
	// Now adds one "<text> NOW" announcement this long before the kick.
	Now time.Duration
}

// Seconds a countdown notification stays on screen; the NOW notice goes a little sooner so that it
// is gone when the players are kicked.
const (
	noticeSeconds    = 10
	noticeNowSeconds = 8
)

// defaultRules is the countdown without announce.ways: the old marks over RCon while more than ten
// minutes are left, then once a minute on screen and a NOW notice 10 seconds before the kick.
func defaultRules() []AnnounceRule {
	return []AnnounceRule{
		{Way: "rcon", At: []time.Duration{30 * time.Minute, 20 * time.Minute, 15 * time.Minute}},
		{Within: 10 * time.Minute, Way: "notification", Every: time.Minute, Now: 10 * time.Second},
	}
}

func (a Announcement) rules() []AnnounceRule {
	if len(a.Rules) == 0 {
		return defaultRules()
	}
	return a.Rules
}

// ruleSpan is a rule with the span of time left (until the kick) it covers: (lo, hi].
type ruleSpan struct {
	AnnounceRule
	lo, hi time.Duration
}

const forever = time.Duration(1<<62 - 1)

// spans orders the rules by Within, the one without a Within last, and cuts the time line into the
// spans the rules cover: a rule covers what lies above the next inner rule's Within.
func spans(rules []AnnounceRule) []ruleSpan {
	sorted := slices.Clone(rules)
	sort.SliceStable(sorted, func(i, j int) bool {
		wi, wj := sorted[i].Within, sorted[j].Within
		if wi == 0 {
			wi = forever
		}
		if wj == 0 {
			wj = forever
		}
		return wi < wj
	})
	out := make([]ruleSpan, len(sorted))
	var lo time.Duration
	for i, r := range sorted {
		hi := r.Within
		if hi == 0 {
			hi = forever
		}
		out[i] = ruleSpan{AnnounceRule: r, lo: lo, hi: hi}
		lo = hi
	}
	return out
}

// covering is the rule that applies at left (until the kick): the one with the smallest Within that
// is not below it. The last rule applies to everything above it.
func covering(sp []ruleSpan, left time.Duration) ruleSpan {
	for _, s := range sp {
		if left <= s.hi {
			return s
		}
	}
	return sp[len(sp)-1]
}

// marks are the times left (until the kick) at which the rules announce, the start of the
// countdown (first) included. A rule's marks lie inside its span and never above the start.
func marks(sp []ruleSpan, first time.Duration) []time.Duration {
	set := map[time.Duration]bool{first: true}
	for _, s := range sp {
		for _, m := range s.At {
			if m > s.lo && m <= s.hi && m <= first {
				set[m] = true
			}
		}
		if s.Every > 0 {
			base := s.hi
			if base == forever {
				base = first
			}
			for l := base; l > s.lo; l -= s.Every {
				if l <= first {
					set[l] = true
				}
			}
		}
	}
	out := make([]time.Duration, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

// onScreen reports whether a way puts messages on the player's screen, where they must not pile up.
func onScreen(way string) bool { return way == "notification" || way == "expansion" }

// timeline lists the steps of a restart in order. The announcements count down to the kick.
func (a Announcement) timeline() []step {
	kick := time.Duration(a.Kick) * time.Minute
	lock := time.Duration(a.Lock) * time.Minute
	first := time.Duration(a.Minutes)*time.Minute - kick
	text := strings.TrimSpace(a.Text)
	if text == "" {
		text = "Server " + a.event()
	}
	if lock < kick {
		lock = kick // the server is locked at the latest when the players are kicked
	}
	sp := spans(a.rules())
	var steps []step
	at := map[time.Duration]int{} // time left until the kick -> index in steps
	add := func(left time.Duration, s step) {
		r := covering(sp, left)
		s.left, s.way, s.typ = left+kick, r.Way, r.Type
		at[left] = len(steps)
		steps = append(steps, s)
	}
	if first > 0 {
		for _, m := range marks(sp, first) {
			add(m, step{say: fmt.Sprintf("%s in %s", text, durationText(m)), seconds: noticeSeconds})
		}
		for _, s := range sp {
			if s.Now > 0 && s.Now <= first {
				if i, ok := at[s.Now]; ok { // NOW replaces a mark at the same time
					steps = slices.Delete(steps, i, i+1)
					for k, v := range at {
						if v > i {
							at[k] = v - 1
						}
					}
				}
				r := s
				steps = append(steps, step{left: s.Now + kick, way: r.Way, typ: r.Type, say: text + " NOW", seconds: noticeNowSeconds, now: true})
				at[s.Now] = len(steps) - 1
			}
		}
	}
	if lock > kick {
		msg := "The server is locked for the " + a.event() + ": no new logins"
		left := lock - kick
		if i, ok := at[left]; ok && onScreen(steps[i].way) && !steps[i].now {
			// one notice on the screen instead of two at once
			steps[i].say += ". " + msg
			steps[i].lock = true
		} else {
			r := covering(sp, left)
			steps = append(steps, step{left: lock, way: r.Way, typ: r.Type, lock: true, say: msg, seconds: noticeSeconds})
		}
	}
	steps = append(steps, step{left: kick, kick: true})
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].left > steps[j].left })
	return steps
}

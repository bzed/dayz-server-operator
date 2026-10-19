// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// AnnounceWays are the ways a countdown message can reach the players.
var AnnounceWays = []string{"rcon", "chat", "notification", "expansion"}

// ExpansionTypes are the Expansion notification types of the expansion way.
var ExpansionTypes = []string{"toast", "baguette", "activity", "killfeed", "market", "garage"}

// AnnounceWay is one rule of announce.ways: how the countdown reaches the players while at most
// Within is left until the kick, and when. Within 0 covers the whole countdown. The rule with the
// smallest Within that is not below the time left is the one that applies.
type AnnounceWay struct {
	Within Duration `yaml:"within,omitempty"`
	// Way is rcon (BattlEye say), chat (coloured, through dzo-admin), notification (on screen,
	// through dzo-admin) or expansion (an Expansion notification of Type).
	Way  string `yaml:"way"`
	Type string `yaml:"type,omitempty"`
	// Every is the interval of the announcements; At lists the exact times left instead.
	Every Duration   `yaml:"every,omitempty"`
	At    []Duration `yaml:"at,omitempty"`
	// Now, when set, adds one "<text> NOW" announcement this long before the kick.
	Now Duration `yaml:"now,omitempty"`
}

// EffectiveWays is Ways, or when none are set the default: the old marks over RCon while more than
// ten minutes are left, then once a minute on screen and a NOW notice 10 seconds before the kick.
func (a Announce) EffectiveWays() []AnnounceWay {
	if len(a.Ways) > 0 {
		return a.Ways
	}
	return []AnnounceWay{
		{Way: "rcon", At: []Duration{Duration(30 * time.Minute), Duration(20 * time.Minute), Duration(15 * time.Minute)}},
		{Within: Duration(10 * time.Minute), Way: "notification", Every: Duration(time.Minute), Now: Duration(10 * time.Second)},
	}
}

// ValidateWays checks announce.ways.
func (a Announce) ValidateWays() error { return a.validateWays("announce.ways") }

func (a Announce) validateWays(prefix string) error {
	seenWithin := map[Duration]bool{}
	nowSet := false
	for i, w := range a.Ways {
		fail := func(format string, args ...any) error {
			return fmt.Errorf("%s[%d]: %s", prefix, i, fmt.Sprintf(format, args...))
		}
		if !slices.Contains(AnnounceWays, w.Way) {
			return fail("way %q: want %s", w.Way, strings.Join(AnnounceWays, ", "))
		}
		if w.Type != "" && w.Way != "expansion" {
			return fail("type only goes with way expansion")
		}
		if w.Type != "" && !slices.Contains(ExpansionTypes, w.Type) {
			return fail("unknown type %q (want %s)", w.Type, strings.Join(ExpansionTypes, ", "))
		}
		if w.Within < 0 {
			return fail("within must not be negative")
		}
		if seenWithin[w.Within] {
			return fail("within %s is used by an earlier rule", time.Duration(w.Within))
		}
		seenWithin[w.Within] = true
		if (w.Every > 0) == (len(w.At) > 0) {
			return fail("needs every or at, not both")
		}
		gap := time.Second
		if w.Way == "notification" || w.Way == "expansion" {
			gap = time.Minute // a notification a second would clutter the screen
		}
		if w.Every != 0 && w.Every.Std() < gap {
			return fail("every must be at least %s for way %s (a notification at most once a minute)", gap, w.Way)
		}
		marks := slices.Clone(w.At)
		slices.SortFunc(marks, func(x, y Duration) int { return int(y - x) })
		for j, m := range marks {
			if m <= 0 {
				return fail("at times must be positive")
			}
			if j > 0 && (marks[j-1]-m).Std() < gap {
				return fail("at times must be at least %s apart for way %s (a notification at most once a minute)", gap, w.Way)
			}
		}
		if w.Now != 0 {
			if w.Now < 0 || (w.Within > 0 && w.Now >= w.Within) {
				return fail("now must be positive and below within")
			}
			if nowSet {
				return fail("now is set by an earlier rule, only one rule may send the NOW notice")
			}
			nowSet = true
		}
		if w.Every < 0 {
			return fail("every must not be negative")
		}
	}
	return nil
}

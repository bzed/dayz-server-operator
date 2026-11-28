// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"regexp"
	"sort"
	"strings"
)

var classRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)

func validClass(s string) bool { return classRe.MatchString(s) }

// MatchGlobs reports whether name equals one of the patterns, where one "*"
// stands for any text: "Old_Bike*", "*_DE", "StaticObj_Wreck_*_DE" (the same
// rule the mod uses).
func MatchGlobs(patterns []string, name string) bool {
	for _, p := range patterns {
		if before, after, ok := strings.Cut(p, "*"); ok {
			if len(name) >= len(before)+len(after) && strings.HasPrefix(name, before) && strings.HasSuffix(name, after) {
				return true
			}
		} else if p != "" && p == name {
			return true
		}
	}
	return false
}

const (
	// maxTypes is the most class names dzo keeps in one list. The vanilla game has about 10000 spawnable
	// classes, a heavily modded server a few times that.
	maxTypes     = 100000
	maxTypesHash = 128
)

// typeList assembles the spawnable class list the mod sends in chunks.
type typeList struct {
	hash  string
	total int
	names []string
	set   map[string]bool
}

// add takes a chunk of the list and reports whether it fits; a chunk that does not (the first
// ones were lost, for example when dzo restarted) means the mod has to start the transfer again.
//
// A chunk that cannot belong to a real list (a Total of 0 or above maxTypes, a hash that is too long, a
// name that is not a class name, more names than Total) empties the list and is refused, so the mod
// starts again and a token holder cannot make dzo keep an unbounded list.
func (t *typeList) add(c TypesChunk) bool {
	if c.Total <= 0 || c.Total > maxTypes || len(c.Hash) > maxTypesHash {
		*t = typeList{}
		return false
	}
	if c.Hash != t.hash || c.Offset == 0 {
		*t = typeList{hash: c.Hash, total: c.Total}
	}
	if c.Offset != len(t.names) {
		return false
	}
	if len(c.Names) > t.total-len(t.names) {
		*t = typeList{}
		return false
	}
	for _, n := range c.Names {
		if !validClass(n) {
			*t = typeList{}
			return false
		}
	}
	t.names = append(t.names, c.Names...)
	if t.complete() {
		sort.Strings(t.names)
		t.set = make(map[string]bool, len(t.names))
		for _, n := range t.names {
			t.set[n] = true
		}
	}
	return true
}

func (t *typeList) complete() bool { return t.total > 0 && len(t.names) == t.total }

func (t *typeList) has(name string) bool { return t.set[name] }

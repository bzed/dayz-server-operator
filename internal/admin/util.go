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

// MatchGlobs reports whether name equals one of the patterns, where a
// trailing "*" makes a pattern a prefix match (the same rule the mod uses).
func MatchGlobs(patterns []string, name string) bool {
	for _, p := range patterns {
		if strings.HasSuffix(p, "*") {
			if strings.HasPrefix(name, strings.TrimSuffix(p, "*")) {
				return true
			}
		} else if p != "" && p == name {
			return true
		}
	}
	return false
}

// typeList assembles the spawnable class list the mod sends in chunks.
type typeList struct {
	hash  string
	total int
	names []string
	set   map[string]bool
}

// add takes a chunk of the list and reports whether it fits; a chunk that does not (the first
// ones were lost, for example when dzo restarted) means the mod has to start the transfer again.
func (t *typeList) add(c TypesChunk) bool {
	if c.Hash != t.hash || c.Offset == 0 {
		*t = typeList{hash: c.Hash, total: c.Total}
	}
	if c.Offset != len(t.names) {
		return false
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

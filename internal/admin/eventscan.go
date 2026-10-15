// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Most Central Economy events put plain engine objects into the world (police car wrecks, supply
// crates, convoy wrecks, ...): they have no script class, so the mod cannot hook their creation.
// dzo reads the events of the live mission instead and hands the mod where each event can spawn
// and which classes it spawns; the mod looks for those objects there (the "event_scan" part of
// config.json). Events whose objects have a script class that is hooked anyway (effect areas,
// vehicles), and the ones without a fixed place (infected, animals, ...), are left out.

// ScanSpawn is one place an event can spawn at. Group names a cfgeventgroups group whose objects are
// looked for instead of the rule's own classes.
type ScanSpawn struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Group string  `json:"g,omitempty"`
}

// ScanGroup is the set of objects a group event places around its position.
type ScanGroup struct {
	Name    string   `json:"name"`
	Classes []string `json:"classes"`
	Radius  float64  `json:"radius"`
}

// ScanRule is one event to look for.
type ScanRule struct {
	Event   string      `json:"name"`
	Classes []string    `json:"classes,omitempty"`
	Radius  float64     `json:"radius"`
	Groups  []ScanGroup `json:"groups,omitempty"`
	Spawns  []ScanSpawn `json:"spawns"`
}

// defaultScanRadius is how far from the spawn position an event's own object is looked for.
const defaultScanRadius = 8

var (
	// Events that are not looked for: they have no fixed place, are other layers already (vehicles,
	// effect areas) or are items the loot economy spawns everywhere.
	skipEventRe = regexp.MustCompile(`^(Infected|Animal|Ambient|Trajectory|Vehicle|Loot$|StaticContaminatedArea$|StaticFridgeTest$)`)
	// Group members that are scenery around the event (decals, craters, stones, ...): they only
	// count when a group has nothing else.
	sceneryRe = regexp.MustCompile(`(?i)decal|crater|stone|bucket|stump|pallet|camonet|barbed|roadblock|bags|tarp`)
)

type eventsXML struct {
	Events []struct {
		Name     string `xml:"name,attr"`
		Active   string `xml:"active"`
		Children []struct {
			Type string `xml:"type,attr"`
		} `xml:"children>child"`
	} `xml:"event"`
}

type eventSpawnsXML struct {
	Events []struct {
		Name string `xml:"name,attr"`
		Pos  []struct {
			X     float64  `xml:"x,attr"`
			Y     *float64 `xml:"y,attr"`
			Z     float64  `xml:"z,attr"`
			Group string   `xml:"group,attr"`
		} `xml:"pos"`
	} `xml:"event"`
}

type eventGroupsXML struct {
	Groups []struct {
		Name     string `xml:"name,attr"`
		Children []struct {
			Type string  `xml:"type,attr"`
			X    float64 `xml:"x,attr"`
			Z    float64 `xml:"z,attr"`
		} `xml:"child"`
	} `xml:"group"`
}

func readXML(path string, v any) (bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the live mission
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := xml.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return true, nil
}

// BuildEventScan reads db/events.xml, cfgeventspawns.xml and cfgeventgroups.xml of a mission and
// returns the rules for the events that are active and have fixed spawn positions. A mission
// without these files gives no rules.
func BuildEventScan(missionDir string) ([]ScanRule, error) {
	var events eventsXML
	var spawns eventSpawnsXML
	var groups eventGroupsXML
	for _, f := range []struct {
		file string
		v    any
	}{{"db/events.xml", &events}, {"cfgeventspawns.xml", &spawns}, {"cfgeventgroups.xml", &groups}} {
		if _, err := readXML(filepath.Join(missionDir, f.file), f.v); err != nil {
			return nil, err
		}
	}

	active := map[string][]string{}
	for _, e := range events.Events {
		if strings.TrimSpace(e.Active) == "0" || skipEventRe.MatchString(e.Name) {
			continue
		}
		var kids []string
		for _, c := range e.Children {
			kids = append(kids, c.Type)
		}
		active[e.Name] = kids
	}
	groupDefs := map[string]ScanGroup{}
	for _, g := range groups.Groups {
		var all, main []string
		radius := 0.0
		for _, c := range g.Children {
			all = append(all, c.Type)
			if !sceneryRe.MatchString(c.Type) {
				main = append(main, c.Type)
			}
			radius = math.Max(radius, math.Hypot(c.X, c.Z))
		}
		if len(main) == 0 {
			main = all
		}
		groupDefs[g.Name] = ScanGroup{Name: g.Name, Classes: uniq(main), Radius: math.Ceil(radius) + 5}
	}

	var rules []ScanRule
	for _, e := range spawns.Events {
		kids, ok := active[e.Name]
		if !ok || len(e.Pos) == 0 {
			continue
		}
		r := ScanRule{Event: e.Name, Classes: uniq(kids), Radius: defaultScanRadius}
		used := map[string]bool{}
		for _, p := range e.Pos {
			s := ScanSpawn{X: p.X, Z: p.Z}
			if p.Y != nil {
				s.Y = *p.Y
			}
			if g, ok := groupDefs[p.Group]; ok && p.Group != "" && len(r.Classes) == 0 {
				if !used[p.Group] {
					used[p.Group] = true
					r.Groups = append(r.Groups, g)
				}
				s.Group = p.Group
			}
			r.Spawns = append(r.Spawns, s)
		}
		// A rule needs something to look for: its own classes or a group on every spawn.
		if len(r.Classes) == 0 && len(r.Groups) == 0 {
			continue
		}
		sort.Slice(r.Groups, func(i, j int) bool { return r.Groups[i].Name < r.Groups[j].Name })
		rules = append(rules, r)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Event < rules[j].Event })
	return rules, nil
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

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
	// The main object of a group is the distinct one: a wreck first, then another vehicle or big
	// object; decals, craters, stones and the like only when nothing else is there.
	mainRe    = regexp.MustCompile(`(?i)wreck|crash`)
	vehicleRe = regexp.MustCompile(`(?i)train|wagon|locomotive|boat|ship|container|truck|tank|bmp|btr|uaz|bus`)
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

// economyCoreXML is cfgeconomycore.xml: besides the mission's own files, every <ce folder> lists more
// Central Economy files (those of mods and overlays).
type economyCoreXML struct {
	CE []struct {
		Folder string `xml:"folder,attr"`
		Files  []struct {
			Name string `xml:"name,attr"`
			Type string `xml:"type,attr"`
		} `xml:"file"`
	} `xml:"ce"`
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

// ceFiles lists the files of one kind: the mission's own and those the <ce> folders of
// cfgeconomycore.xml register. kind is "events", "eventspawns" or "eventgroups"; a file counts by its
// registered type or, for the two position files, by its usual name (they are merged into the
// mission's own file by dzo, but a mission can register them too).
func ceFiles(missionDir, own, kind string, core economyCoreXML) []string {
	files := []string{filepath.Join(missionDir, own)}
	for _, ce := range core.CE {
		for _, f := range ce.Files {
			if strings.EqualFold(f.Type, kind) || (kind != "events" && strings.EqualFold(f.Name, "cfg"+kind+".xml")) {
				files = append(files, filepath.Join(missionDir, ce.Folder, f.Name))
			}
		}
	}
	return files
}

// mainObject picks the distinct object of a group and how far from the group's position it is.
func mainObject(children []struct {
	Type string  `xml:"type,attr"`
	X    float64 `xml:"x,attr"`
	Z    float64 `xml:"z,attr"`
}) (string, float64) {
	for _, re := range []*regexp.Regexp{mainRe, vehicleRe} {
		for _, c := range children {
			if re.MatchString(c.Type) && !sceneryRe.MatchString(c.Type) {
				return c.Type, math.Hypot(c.X, c.Z)
			}
		}
	}
	for _, c := range children {
		if !sceneryRe.MatchString(c.Type) {
			return c.Type, math.Hypot(c.X, c.Z)
		}
	}
	if len(children) > 0 {
		return children[0].Type, math.Hypot(children[0].X, children[0].Z)
	}
	return "", 0
}

// BuildEventScan reads the Central Economy event files of a mission (db/events.xml,
// cfgeventspawns.xml, cfgeventgroups.xml and the same kinds of files that cfgeconomycore.xml
// registers) and returns the rules for the events that are active and have fixed spawn positions.
// Files that several sources define are merged: events and positions add up, a group defined twice
// is the later one. A mission without these files gives no rules.
func BuildEventScan(missionDir string) ([]ScanRule, error) {
	var core economyCoreXML
	if _, err := readXML(filepath.Join(missionDir, "cfgeconomycore.xml"), &core); err != nil {
		return nil, err
	}
	// kids and active by event name; an event that any file marks inactive stays out only when none
	// activates it.
	active := map[string]bool{}
	kids := map[string][]string{}
	for _, f := range ceFiles(missionDir, "db/events.xml", "events", core) {
		var x eventsXML
		if _, err := readXML(f, &x); err != nil {
			return nil, err
		}
		for _, e := range x.Events {
			if skipEventRe.MatchString(e.Name) {
				continue
			}
			if strings.TrimSpace(e.Active) != "0" {
				active[e.Name] = true
			}
			for _, c := range e.Children {
				kids[e.Name] = append(kids[e.Name], c.Type)
			}
		}
	}
	groupDefs := map[string]ScanGroup{}
	for _, f := range ceFiles(missionDir, "cfgeventgroups.xml", "eventgroups", core) {
		var x eventGroupsXML
		if _, err := readXML(f, &x); err != nil {
			return nil, err
		}
		for _, g := range x.Groups {
			main, dist := mainObject(g.Children)
			if main != "" {
				groupDefs[g.Name] = ScanGroup{Name: g.Name, Classes: []string{main}, Radius: math.Ceil(dist) + 5}
			}
		}
	}
	byEvent := map[string]*ScanRule{}
	var order []string
	for _, f := range ceFiles(missionDir, "cfgeventspawns.xml", "eventspawns", core) {
		var x eventSpawnsXML
		if _, err := readXML(f, &x); err != nil {
			return nil, err
		}
		for _, e := range x.Events {
			if !active[e.Name] || len(e.Pos) == 0 {
				continue
			}
			r := byEvent[e.Name]
			if r == nil {
				r = &ScanRule{Event: e.Name, Classes: uniq(kids[e.Name]), Radius: defaultScanRadius}
				byEvent[e.Name] = r
				order = append(order, e.Name)
			}
			for _, p := range e.Pos {
				sp := ScanSpawn{X: p.X, Z: p.Z}
				if p.Y != nil {
					sp.Y = *p.Y
				}
				if g, ok := groupDefs[p.Group]; ok && p.Group != "" && len(r.Classes) == 0 {
					have := false
					for _, x := range r.Groups {
						have = have || x.Name == g.Name
					}
					if !have {
						r.Groups = append(r.Groups, g)
					}
					sp.Group = p.Group
				}
				r.Spawns = append(r.Spawns, sp)
			}
		}
	}
	var rules []ScanRule
	for _, name := range order {
		r := byEvent[name]
		// A rule needs something to look for: its own classes or a group on a spawn.
		if len(r.Classes) == 0 && len(r.Groups) == 0 {
			continue
		}
		sort.Slice(r.Groups, func(i, j int) bool { return r.Groups[i].Name < r.Groups[j].Name })
		rules = append(rules, *r)
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

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import "testing"

func TestVanillaEventWatch(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range VanillaEventWatch() {
		if err := r.Validate(); err != nil {
			t.Errorf("%v", err)
		}
		if r.Icon == "" || r.Color == "" || r.Label == "" || seen[r.Layer] {
			t.Errorf("layer %q: icon, colour and label are needed, and a layer once", r.Layer)
		}
		if r.Visibility != "viewer" {
			t.Errorf("layer %q: events are visible to every role", r.Layer)
		}
		seen[r.Layer] = true
	}
}

func TestEventStyles(t *testing.T) {
	if s := StyleOfEvent("effect_area"); s.Icon != "exclamation-triangle" {
		t.Errorf("effect area icon = %q", s.Icon)
	}
	if s := StyleOfEvent("SomeModEvent"); s.Icon != "flag" || s.Label != "SomeModEvent" {
		t.Errorf("unknown = %+v", s)
	}
	for name, s := range eventStyles {
		if !IconNameOK(s.Icon) || !ColorOK(s.Color) {
			t.Errorf("%s: bad style %+v", name, s)
		}
	}
}

// The classes the Deer Isle 6.1 mission (db/events.xml, cfgeventgroups.xml) places for its events: the
// first rule that matches a class names its layer.
func TestVanillaEventWatchDeerIsle(t *testing.T) {
	for class, layer := range map[string]string{
		"Land_jmc_ce_oven":              "Survivor camps",
		"Land_STAG_Submarine_Dark":      "Submarines",
		"Land_jmc_van_pinkies":          "Pinkies van",
		"Land_jmc_van_ambulance":        "Ambulances",
		"Land_jmc_skeleton_gnawed_4":    "Skeletons",
		"Land_Wreck_Uaz":                "Military wrecks",
		"StaticObj_Misc_SupplyBox2_DE":  "Airplane crates", // the airdrop crates, also in the convoy groups
		"Wreck_Mi8_Crashed":             "Helicopter crashes",
		"Land_Wreck_sed01_aban1_police": "Police cars",
	} {
		got := ""
		for _, r := range VanillaEventWatch() {
			if MatchGlobs(r.Classes, class) {
				got = r.Layer
				break
			}
		}
		if got != layer {
			t.Errorf("%s: layer %q, want %q", class, got, layer)
		}
	}
}

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVanillaEventWatch(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range VanillaEventWatch() {
		assert.NoError(t, r.Validate(), "layer %q", r.Layer)
		assert.NotEmpty(t, r.Icon, "layer %q: icon, colour and label are needed", r.Layer)
		assert.NotEmpty(t, r.Color, "layer %q: icon, colour and label are needed", r.Layer)
		assert.NotEmpty(t, r.Label, "layer %q: icon, colour and label are needed", r.Layer)
		assert.False(t, seen[r.Layer], "layer %q: a layer once", r.Layer)
		assert.Equal(t, "viewer", r.Visibility)
		seen[r.Layer] = true
	}
}

func TestEventStyles(t *testing.T) {
	s := StyleOfEvent("effect_area")
	assert.Equal(t, "exclamation-triangle", s.Icon)
	s = StyleOfEvent("SomeModEvent")
	require.Equal(t, "flag", s.Icon)
	assert.Equal(t, "SomeModEvent", s.Label)
	for name, s := range eventStyles {
		assert.True(t, IconNameOK(s.Icon), "%s: bad style %+v", name, s)
		assert.True(t, ColorOK(s.Color), "%s: bad style %+v", name, s)
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
		assert.Equal(t, layer, got)
	}
}

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

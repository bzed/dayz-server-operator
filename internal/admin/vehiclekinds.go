// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import "slices"

// The game has a car for everything with an engine that a mod adds: the DayZBicycle bicycles, the
// helicopters of DayZ Expansion and the Mosquito of Deer Isle all derive from CarScript, so the mod
// reports them as kind "car". The hub gives a car the kind of the first list that has its class, for
// the icon on the map and in the vehicle list. The kinds are the ones the web interface has an icon for.

// DefaultVehicleKinds are the classes dzo knows, by kind: the DayZBicycle mod (Steam Workshop 2971190303),
// as its Central Economy event lists them, and the helicopters of DayZ Expansion and of Deer Isle. A
// trailing * makes a class a prefix. admin.vehicle_kinds adds the classes of other mods.
func DefaultVehicleKinds() map[string][]string {
	return map[string][]string{
		"bicycle":    {"Old_Bike*", "Lady_Bicycle*", "MMTArma"},
		"boat":       {},
		"helicopter": {"ExpansionMh6*", "ExpansionMerlin*", "ExpansionUh1h*", "ExpansionGyrocopter*", "RFMosquito"},
	}
}

// VehicleKindOK reports whether kind is one the map has an icon for (the car, motorbike and the kinds
// of DefaultVehicleKinds; only the latter can be set by class). The boats of the game and of DayZ Expansion
// are reported as "boat" by the mod itself; the kind lists only the boats of other mods.
func VehicleKindOK(kind string) bool {
	_, ok := DefaultVehicleKinds()[kind]
	return ok
}

// kindOf is the kind of the vehicle class t among kinds, or "" when no list has it. The kinds are tried
// in alphabetical order, so a class in two lists always gets the same one.
func kindOf(kinds map[string][]string, t string) string {
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		if MatchGlobs(kinds[k], t) {
			return k
		}
	}
	return ""
}

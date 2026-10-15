// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

// EventStyle is how the map shows an event: a Fork Awesome icon, a colour and a label.
type EventStyle struct {
	Icon  string
	Color string
	Label string
}

// eventStyles covers the events of the vanilla missions (chernarusplus, enoch, sakhal, nasdara). The
// key is the event name of db/events.xml, or the category of an event the mod reports itself.
var eventStyles = map[string]EventStyle{
	"effect_area":            {"exclamation-triangle", "#7fa800", "Contaminated area"},
	"StaticContaminatedArea": {"exclamation-triangle", "#7fa800", "Contaminated area"},
	"StaticHeliCrash":        {"plane", "#d9822b", "Helicopter crash"},
	"StaticHeliCrashBig":     {"plane", "#c4511f", "Large helicopter crash"},
	"StaticSantaCrash":       {"gift", "#c0392b", "Santa's sleigh"},
	"StaticMilitaryConvoy":   {"truck", "#5b7a3a", "Military convoy"},
	"StaticPoliceCar":        {"car", "#2f6fb5", "Police car"},
	"StaticPoliceSituation":  {"bullhorn", "#2f6fb5", "Police situation"},
	"StaticScientist":        {"flask", "#6a1b9a", "Scientist camp"},
	"StaticTrain":            {"train", "#6b6b6b", "Train"},
	"StaticAirplaneCrate":    {"archive", "#8a5a2b", "Airplane supply crate"},
	"StaticAmbulance":        {"ambulance", "#c0392b", "Ambulance"},
	"StaticBoatFishing":      {"ship", "#2a8aa8", "Fishing boat"},
	"StaticBoatMilitary":     {"anchor", "#5b7a3a", "Military boat"},
	"StaticBonfire":          {"fire", "#e67e22", "Bonfire"},
	"StaticChristmasTree":    {"tree", "#2e7d32", "Christmas tree"},
	"StaticContainerLocked":  {"lock", "#7a3db8", "Locked container"},
	"StaticWaterTruck":       {"tint", "#2a8aa8", "Water truck"},
	"ItemBricks":             {"cubes", "#a0522d", "Bricks"},
	"ItemPlanks":             {"cubes", "#8b6b3d", "Planks"},
}

// StyleOfEvent returns the style of an event or category; an unknown one gets a flag.
func StyleOfEvent(name string) EventStyle {
	if s, ok := eventStyles[name]; ok {
		return s
	}
	return EventStyle{"flag", "#7a3db8", name}
}

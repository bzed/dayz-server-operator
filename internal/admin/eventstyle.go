// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

// EventStyle is how the map shows an event the mod reports itself (effect areas): a Fork Awesome icon,
// a colour and a label. The other events of the Central Economy are marker layers, see VanillaEventWatch.
type EventStyle struct {
	Icon  string
	Color string
	Label string
}

var eventStyles = map[string]EventStyle{
	"effect_area": {"exclamation-triangle", "#7fa800", "Contaminated area"},
}

// StyleOfEvent returns the style of an event category; an unknown one gets a flag.
func StyleOfEvent(name string) EventStyle {
	if s, ok := eventStyles[name]; ok {
		return s
	}
	return EventStyle{"flag", "#7a3db8", name}
}

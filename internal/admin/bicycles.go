// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

// DefaultBicycleClasses are the classes of the DayZBicycle mod (Steam Workshop 2971190303), as its
// Central Economy event lists them: the Old_Bike colours and the Old_BikeArma variant, the Lady_Bicycle
// variants and MMTArma. The mod derives them from CarScript, so the game reports them as cars; dzo
// calls a vehicle of one of these classes a bicycle. Other bicycle mods are added with
// admin.bicycle_classes in the instance.
func DefaultBicycleClasses() []string {
	return []string{"Old_Bike*", "Lady_Bicycle*", "MMTArma"}
}

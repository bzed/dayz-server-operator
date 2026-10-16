// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

// Central Economy events put an object into the world that is distinct for the event: the wreck of
// the helicopter, the police car, the tank. The events are therefore shown the way mods show theirs,
// with class watch rules: whatever object of one of these classes exists is a marker on the layer, with
// the icon of the event. Subclasses match through the config inheritance (the dynamic event variants
// "_DE" of a wreck are subclasses), and `*` stands for any text in a class name. Nothing about where an
// event can spawn, or which files configure it, is needed: a mission or mod with other events adds a
// watch rule for the classes of its event, and a rule here is replaced by one with the same layer name.
//
// The first rule that matches a class wins, so the specific rules come before the catch-alls.
func VanillaEventWatch() []WatchRule {
	rules := []WatchRule{
		{Layer: "Helicopter crashes", Icon: "helicopter", Color: "#d9822b", Label: "Helicopter crash",
			Classes: []string{"Wreck_UH1Y", "Wreck_Mi8_Crashed", "StaticObj_Wreck_Mi24_TK*", "StaticObj_Wreck_Chinook*"}},
		{Layer: "Santa's sleigh", Icon: "gift", Color: "#c0392b", Label: "Santa's sleigh", Classes: []string{"Wreck_SantasSleigh"}},
		{Layer: "Airplane crates", Icon: "archive", Color: "#8a5a2b", Label: "Airplane supply crate", Classes: []string{"StaticObj_Misc_SupplyBox*_DE"}},
		{Layer: "Trains", Icon: "train", Color: "#6b6b6b", Label: "Train", Classes: []string{"StaticObj_Wreck_Train_*_DE"}},
		{Layer: "Police cars", Icon: "car", Color: "#2f6fb5", Label: "Police car", Classes: []string{
			"Land_Wreck_sed01_aban1_police*", "Land_Wreck_sed01_aban2_police*", "Land_Wreck_hb01_aban1_police*", "Land_Wreck_hb01_aban2_police*",
			"Land_Wreck_sed02_aban1_Police*", "Land_Wreck_sed02_aban2_Police*", "Land_Wreck_UAZ_Aban1_Police*", "Land_Wreck_UAZ_Aban2_Police*"}},
		{Layer: "Ambulances", Icon: "ambulance", Color: "#c0392b", Label: "Ambulance", Classes: []string{"Land_Wreck_S1023_Medic_Beige_DE"}},
		{Layer: "Water trucks", Icon: "tint", Color: "#2a8aa8", Label: "Water truck", Classes: []string{"Land_Wreck_Truck01_Aban1_Cistern*", "Land_Wreck_Truck01_Aban2_Cistern*"}},
		{Layer: "Military wrecks", Icon: "truck", Color: "#5b7a3a", Label: "Military convoy", Classes: []string{"StaticObj_Wreck_*_DE", "Land_Wreck_*_TK_DE"}},
		{Layer: "Roadblock wrecks", Icon: "bullhorn", Color: "#2f6fb5", Label: "Roadblock / abandoned vehicles", Classes: []string{"Land_Wreck_*_DE", "Land_wreck_*_DE"}},
		{Layer: "Locked containers", Icon: "lock", Color: "#7a3db8", Label: "Locked container", Classes: []string{"ContainerLockedBase", "Land_Mil_Container*_DE"}},
		{Layer: "Fishing boats", Icon: "ship", Color: "#2a8aa8", Label: "Fishing boat", Classes: []string{"Land_Boat_Small9_DE"}},
		{Layer: "Military boats", Icon: "anchor", Color: "#5b7a3a", Label: "Military boat", Classes: []string{"StaticObj_PatrolBoat_Military_DE"}},
		{Layer: "Scientists", Icon: "flask", Color: "#6a1b9a", Label: "Scientist camp", Classes: []string{"StaticObj_FrozenScientist_DE"}},
		{Layer: "Bonfires", Icon: "fire", Color: "#e67e22", Label: "Bonfire", Classes: []string{"Bonfire"}},
		{Layer: "Christmas trees", Icon: "tree", Color: "#2e7d32", Label: "Christmas tree", Classes: []string{"ChristmasTree"}},
		{Layer: "Brick piles", Icon: "cubes", Color: "#a0522d", Label: "Bricks", Classes: []string{"PileOfBricks"}},
		{Layer: "Plank piles", Icon: "cubes", Color: "#8b6b3d", Label: "Planks", Classes: []string{"PileOfWoodenPlanks"}},
	}
	// Events are not secret: everyone who sees the map sees them.
	for i := range rules {
		rules[i].Visibility = "viewer"
	}
	return rules
}

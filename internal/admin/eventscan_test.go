// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMission(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBuildEventScan(t *testing.T) {
	dir := writeMission(t, map[string]string{
		"db/events.xml": `<events>
 <event name="StaticHeliCrash"><active>1</active><children><child type="Wreck_UH1Y"/><child type="Wreck_Mi8_Crashed"/></children></event>
 <event name="StaticMilitaryConvoy"><active>1</active><children/></event>
 <event name="StaticPoliceCar"><active>0</active><children><child type="Land_Wreck_UAZ_Aban1_Police"/></children></event>
 <event name="InfectedCity"><active>1</active><children><child type="ZmbM_x"/></children></event>
 <event name="VehicleMotorbike"><active>1</active><children><child type="Motorbike_01_Blue"/></children></event>
 <event name="StaticContaminatedArea"><active>1</active><children><child type="ContaminatedArea_Dynamic"/></children></event>
</events>`,
		"cfgeventspawns.xml": `<eventposdef>
 <event name="StaticHeliCrash"><pos x="100" z="200" a="0" y="5"/><pos x="300" z="400" a="0"/></event>
 <event name="StaticMilitaryConvoy"><pos x="10" z="20" a="0" y="1" group="Abad"/><pos x="30" z="40" a="0" group="Nope"/></event>
 <event name="StaticPoliceCar"><pos x="1" z="2" a="0"/></event>
 <event name="InfectedCity"><pos x="1" z="2" a="0"/></event>
 <event name="VehicleMotorbike"><pos x="1" z="2" a="0"/></event>
 <event name="StaticContaminatedArea"><pos x="1" z="2" a="0"/></event>
</eventposdef>`,
		"cfgeventgroups.xml": `<eventgroupdef>
 <group name="Abad"><child type="StaticObj_Wreck_M1A2_DE" x="0" z="0" a="0"/><child type="StaticObj_decal_scorchmark_01_large_f_DE" x="3" z="4" a="0"/></group>
</eventgroupdef>`,
	})
	rules, err := BuildEventScan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %+v", rules)
	}
	heli, convoy := rules[0], rules[1]
	if heli.Event != "StaticHeliCrash" || len(heli.Spawns) != 2 || heli.Spawns[0].Y != 5 || len(heli.Classes) != 2 || heli.Radius != defaultScanRadius {
		t.Errorf("heli = %+v", heli)
	}
	if len(convoy.Groups) != 1 {
		t.Fatalf("convoy groups = %+v", convoy.Groups)
	}
	g := convoy.Groups[0]
	if convoy.Event != "StaticMilitaryConvoy" || g.Name != "Abad" || len(g.Classes) != 1 || g.Classes[0] != "StaticObj_Wreck_M1A2_DE" || g.Radius != 10 {
		t.Errorf("convoy = %+v", convoy)
	}
	// The spawn whose group is not defined stays in the rule but looks for nothing.
	if len(convoy.Spawns) != 2 || convoy.Spawns[0].Group != "Abad" || convoy.Spawns[1].Group != "" {
		t.Errorf("convoy spawns = %+v", convoy.Spawns)
	}
}

func TestBuildEventScanNoFiles(t *testing.T) {
	rules, err := BuildEventScan(t.TempDir())
	if err != nil || len(rules) != 0 {
		t.Errorf("got %v, %v", rules, err)
	}
}

func TestEventStyles(t *testing.T) {
	if s := StyleOfEvent("StaticHeliCrash"); s.Icon != "plane" {
		t.Errorf("heli icon = %q", s.Icon)
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

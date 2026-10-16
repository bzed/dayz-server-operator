// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid for scripts/test-spawnable.sh, never for production. Creates every public item class once
// (the candidates for dzo-admin's spawn list), lets it fill itself the way the debug spawn does
// (OnDebugSpawn, which dzo's "with attachments" uses), and deletes it again. Before each class it
// appends the name to $profile:dzo_probe/current (and closes the file), so after a crash the last
// line names the class that did it. Classes in $profile:dzo_probe/skip are not tried. Ends with a
// line "DZO-SPAWNPROBE done <tried>".
modded class MissionServer
{
	override void OnInit()
	{
		super.OnInit();
		MakeDirectory("$profile:dzo_probe");
		if (FileExist("$profile:dzo_probe/run"))
			GetGame().GetCallQueue(CALL_CATEGORY_SYSTEM).CallLater(DZOSpawnProbeRun, 20000, false);
	}

	void DZOSpawnProbeRun()
	{
		TStringArray skip = new TStringArray;
		FileHandle sf = OpenFile("$profile:dzo_probe/skip", FileMode.READ);
		if (sf != 0)
		{
			string line;
			while (FGets(sf, line) >= 0)
			{
				line.Trim();
				if (line != "")
					skip.Insert(line);
			}
			CloseFile(sf);
		}
		vector pos = Vector(7500, 0, 7500);
		pos[1] = GetGame().SurfaceY(pos[0], pos[2]);
		TStringArray roots = {"CfgVehicles", "CfgWeapons", "CfgMagazines"};
		int tried = 0;
		foreach (string root : roots)
		{
			int n = GetGame().ConfigGetChildrenCount(root);
			for (int i = 0; i < n; i++)
			{
				string name;
				GetGame().ConfigGetChildName(root, i, name);
				if (GetGame().ConfigGetInt(root + " " + name + " scope") != 2)
					continue;
				if (root == "CfgVehicles" && !GetGame().IsKindOf(name, "Inventory_Base") && !GetGame().IsKindOf(name, "Clothing_Base"))
					continue;
				if (skip.Find(name) > -1)
					continue;
				FileHandle cur = OpenFile("$profile:dzo_probe/current", FileMode.APPEND);
				FPrintln(cur, name);
				CloseFile(cur);
				Print("DZO-SPAWNPROBE try " + name);
				EntityAI e = EntityAI.Cast(GetGame().CreateObjectEx(name, pos, ECE_PLACE_ON_SURFACE));
				if (e)
				{
					e.OnDebugSpawn();
					GetGame().ObjectDelete(e);
				}
				tried++;
			}
		}
		Print("DZO-SPAWNPROBE done " + tried);
	}
}

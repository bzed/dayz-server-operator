// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid for the end-to-end run of the stored-vehicles feature, never for production. Expansion's
// virtual garage keeps a stored vehicle as an entity-storage .bin plus a garage JSON that references
// it by global ID, and dzo-admin reads and acts on such a pair. A real garage entry is made by a
// player who stores a vehicle, so this probe makes the pairs without one: when the file
// $profile:dzo_garage_probe/make exists at mission start, it goes through every garage the module
// loaded and creates the entity-storage file of every stored vehicle that has none - it spawns the
// vehicle at its stored position, complete the way the game's debug spawn makes a car, saves it with
// the same call and to the same file Expansion's own garage store uses (ExpansionEntityStorageModule
// SaveToFile, ExpansionGarageVehicleData.GetEntityStorageFileName) and deletes it again. One line per
// vehicle goes to $profile:dzo_garage_probe/done, then "DZO-GARAGEPROBE done <n>". The garage JSONs
// themselves are the test's to write into the mission storage. Without the Expansion garage the
// probe is inert.
#ifdef EXPANSIONMODGARAGE
modded class MissionServer
{
	override void OnInit()
	{
		super.OnInit();
		MakeDirectory("$profile:dzo_garage_probe");
		if (FileExist("$profile:dzo_garage_probe/make"))
			GetGame().GetCallQueue(CALL_CATEGORY_SYSTEM).CallLater(DZOGarageProbeMake, 20000, false);
	}

	void DZOGarageProbeMake()
	{
		DeleteFile("$profile:dzo_garage_probe/make");
		if (!ExpansionGarageModule.s_Instance || !ExpansionGarageModule.s_Instance.GetGarageData())
		{
			Print("DZO-GARAGEPROBE no garage data");
			DZOGarageProbeLog("FAILED no garage data");
			return;
		}
		int made = 0;
		foreach (ExpansionGarageData garage : ExpansionGarageModule.s_Instance.GetGarageData())
		{
			if (!garage)
				continue;
			foreach (ExpansionGarageVehicleData vd : garage.m_Vehicles)
			{
				if (!vd || !vd.IsGlobalIDValid())
					continue;
				string hex = ExpansionStatic.IntToHex(vd.m_GlobalID);
				string file = vd.GetEntityStorageFileName();
				if (FileExist(file))
				{
					Print("DZO-GARAGEPROBE kept " + vd.m_ClassName + " (" + hex + ")");
					DZOGarageProbeLog(vd.m_ClassName + " " + hex + " kept " + file);
					continue;
				}
				if (DZOGarageProbeStore(vd, file))
					made++;
			}
		}
		Print("DZO-GARAGEPROBE done " + made);
	}

	// Spawns the vehicle complete at its stored position, puts one item into its cargo, saves it to
	// the entity-storage file the garage keeps for the global ID and deletes it again. False when
	// the vehicle could not be made or saved.
	bool DZOGarageProbeStore(ExpansionGarageVehicleData vd, string file)
	{
		string hex = ExpansionStatic.IntToHex(vd.m_GlobalID);
		ExpansionStatic.MakeDirectoryRecursive(ExpansionEntityStorageModule.GetStorageDirectory());
		EntityAI vehicle = EntityAI.Cast(GetGame().CreateObjectEx(vd.m_ClassName, vd.m_Position, ECE_PLACE_ON_SURFACE | ECE_CREATEPHYSICS | ECE_UPDATEPATHGRAPH));
		if (!vehicle)
		{
			Print("DZO-GARAGEPROBE could not create " + vd.m_ClassName + " (" + hex + ")");
			DZOGarageProbeLog(vd.m_ClassName + " " + hex + " FAILED create");
			return false;
		}
		vehicle.OnDebugSpawn(); // wheels, battery, fluids: a car as a player drives it, not a bare one
		if (!vehicle.GetInventory() || !vehicle.GetInventory().CreateInInventory("SodaCan_Cola"))
			Print("DZO-GARAGEPROBE could not put the cargo into " + vd.m_ClassName + " (" + hex + ")");
		// The height of the ground at the stored position (the surface, water included) and the
		// distance of the model's zero point from it (what turns the origin's y into the standing
		// height), both for the run's checks: a moved spawn point must keep the height of the
		// ground, not a metre above it.
		float mzp = 0;
		ExpansionVehicle ev;
		if (ExpansionVehicle.Get(ev, vehicle))
			mzp = ev.GetModelZeroPointDistanceFromGround();
		vector surface = ExpansionStatic.GetSurfaceWaterPosition(vd.m_Position);
		if (!ExpansionEntityStorageModule.SaveToFile(vehicle, file))
		{
			Print("DZO-GARAGEPROBE could not save " + vd.m_ClassName + " (" + hex + ") to " + file);
			DZOGarageProbeLog(vd.m_ClassName + " " + hex + " FAILED save " + file);
			GetGame().ObjectDelete(vehicle);
			return false;
		}
		Print("DZO-GARAGEPROBE saved " + vd.m_ClassName + " (" + hex + ") to " + file);
		DZOGarageProbeLog(vd.m_ClassName + " " + hex + " saved " + file + " surface=" + surface[1] + " mzp=" + mzp);
		GetGame().ObjectDelete(vehicle);
		return true;
	}

	// Appends one line to $profile:dzo_garage_probe/done, the file the run waits for.
	void DZOGarageProbeLog(string line)
	{
		FileHandle f = OpenFile("$profile:dzo_garage_probe/done", FileMode.APPEND);
		if (f == 0)
		{
			Print("DZO-GARAGEPROBE could not open done");
			return;
		}
		FPrintln(f, "DZO-GARAGEPROBE " + line);
		CloseFile(f);
	}
}
#endif

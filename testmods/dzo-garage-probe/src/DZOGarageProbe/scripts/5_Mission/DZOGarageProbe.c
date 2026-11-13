// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid for the end-to-end run of the stored-vehicles feature, never for production. Expansion's
// virtual garage keeps a stored vehicle as an entity-storage .bin plus a garage JSON that references
// it by global ID, and dzo-admin reads and acts on such a pair. A real garage entry is made by a
// player who stores a vehicle, so this probe makes the pair without one: when the file
// $profile:dzo_garage_probe/make exists at mission start, it spawns the vehicle of a real garage
// entry at its stored position, complete the way the game's debug spawn makes a car, puts one item
// into its cargo, saves it with the same call and to the same file Expansion's own garage store uses
// (ExpansionEntityStorageModule.SaveToFile, ExpansionGarageVehicleData.GetEntityStorageFileName) and
// deletes it again. One line per vehicle goes to $profile:dzo_garage_probe/done, then
// "DZO-GARAGEPROBE done <n>". The global IDs and positions are the ones of the garage JSONs in the
// mission storage this run tests with. Without the Expansion garage the probe is inert.
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
		// The global IDs and stored positions of two vehicles of the real garage data this run tests
		// with (OffroadHatchback_Blue and CivilianSedan). Expansion's global ID counter starts at
		// int.MIN, so the real IDs read -2147483648.-2147483648.-2147483648.<n>.
		int minInt = int.MIN;
		TIntArray id1 = {minInt, minInt, minInt, minInt};
		id1[3] = minInt + 42;   // -2147483606
		TIntArray id2 = {minInt, minInt, minInt, minInt};
		id2[3] = minInt + 1554; // -2147482094
		DZOGarageProbeStore("OffroadHatchback_Blue", "3469.12841796875 214.21888732910156 7374.64208984375", id1);
		DZOGarageProbeStore("CivilianSedan", "3118.31982421875 262.57537841796875 12011.01171875", id2);
		Print("DZO-GARAGEPROBE done 2");
	}

	// Spawns the vehicle complete at its stored position, puts one item into its cargo, saves it to
	// the entity-storage file the garage keeps for the global ID and deletes it again.
	void DZOGarageProbeStore(string className, string storedPos, TIntArray id)
	{
		string hex = ExpansionStatic.IntToHex(id);
		ExpansionStatic.MakeDirectoryRecursive(ExpansionEntityStorageModule.GetStorageDirectory());
		vector pos = storedPos.ToVector();
		EntityAI vehicle = EntityAI.Cast(GetGame().CreateObjectEx(className, pos, ECE_PLACE_ON_SURFACE | ECE_CREATEPHYSICS | ECE_UPDATEPATHGRAPH));
		if (!vehicle)
		{
			Print("DZO-GARAGEPROBE could not create " + className + " (" + hex + ")");
			DZOGarageProbeLog(className + " " + hex + " FAILED create");
			return;
		}
		vehicle.OnDebugSpawn(); // wheels, battery, fluids: a car as a player drives it, not a bare one
		if (!vehicle.GetInventory() || !vehicle.GetInventory().CreateInInventory("SodaCan_Cola"))
			Print("DZO-GARAGEPROBE could not put the cargo into " + className + " (" + hex + ")");
		string file = ExpansionEntityStorageModule.GetFileName(hex); // what ExpansionGarageVehicleData.GetEntityStorageFileName() resolves to
		if (!ExpansionEntityStorageModule.SaveToFile(vehicle, file))
		{
			Print("DZO-GARAGEPROBE could not save " + className + " (" + hex + ") to " + file);
			DZOGarageProbeLog(className + " " + hex + " FAILED save " + file);
			GetGame().ObjectDelete(vehicle);
			return;
		}
		Print("DZO-GARAGEPROBE saved " + className + " (" + hex + ") to " + file);
		DZOGarageProbeLog(className + " " + hex + " saved " + file);
		GetGame().ObjectDelete(vehicle);
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

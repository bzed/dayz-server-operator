// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

class CfgPatches
{
	class DZOSpawnProbe
	{
		units[] = {};
		weapons[] = {};
		requiredVersion = 0.1;
		requiredAddons[] = {"DZ_Data", "DZ_Scripts"};
	};
};

class CfgMods
{
	class DZOSpawnProbe
	{
		dir = "DZOSpawnProbe";
		name = "DZOSpawnProbe";
		author = "Bernd Zeimetz";
		type = "servermod";
		dependencies[] = {"Mission"};

		class defs
		{
			class missionScriptModule
			{
				value = "";
				files[] = {"DZOSpawnProbe/scripts/5_Mission"};
			};
		};
	};
};

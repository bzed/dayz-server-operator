// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

class CfgPatches
{
	class DZOGarageProbe
	{
		units[] = {};
		weapons[] = {};
		requiredVersion = 0.1;
		requiredAddons[] = {"DZ_Data", "DZ_Scripts"};
	};
};

class CfgMods
{
	class DZOGarageProbe
	{
		dir = "DZOGarageProbe";
		name = "DZOGarageProbe";
		author = "Bernd Zeimetz";
		type = "servermod";
		dependencies[] = {"Mission"};

		class defs
		{
			class missionScriptModule
			{
				value = "";
				files[] = {"DZOGarageProbe/scripts/5_Mission"};
			};
		};
	};
};

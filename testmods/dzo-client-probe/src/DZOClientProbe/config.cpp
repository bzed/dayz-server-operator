// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

class CfgPatches
{
	class DZOClientProbe
	{
		units[] = {};
		weapons[] = {};
		requiredVersion = 0.1;
		requiredAddons[] = {"DZ_Data", "DZ_Scripts"};
	};
};

class CfgMods
{
	class DZOClientProbe
	{
		dir = "DZOClientProbe";
		name = "DZOClientProbe";
		author = "Bernd Zeimetz";
		type = "servermod";
		dependencies[] = {"Mission"};

		class defs
		{
			class missionScriptModule
			{
				value = "";
				files[] = {"DZOClientProbe/scripts/5_Mission"};
			};
		};
	};
};

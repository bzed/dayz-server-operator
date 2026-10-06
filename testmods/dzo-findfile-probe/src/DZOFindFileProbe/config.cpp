// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

class CfgPatches
{
	class DZOFindFileProbe
	{
		units[] = {};
		weapons[] = {};
		requiredVersion = 0.1;
		requiredAddons[] = {"DZ_Data", "DZ_Scripts", "JM_CF_Scripts"};
	};
};

class CfgMods
{
	class DZOFindFileProbe
	{
		dir = "DZOFindFileProbe";
		name = "DZOFindFileProbe";
		author = "Bernd Zeimetz";
		type = "servermod";
		dependencies[] = {"Mission"};

		class defs
		{
			class missionScriptModule
			{
				value = "";
				files[] = {"DZOFindFileProbe/scripts/5_Mission"};
			};
		};
	};
};

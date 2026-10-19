// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid, never for production: makes an Expansion territory without a player (see scripts/test-client.sh).
#ifdef EXPANSIONMODBASEBUILDING
modded class ExpansionTerritoryModule
{
	// Exec_CreateTerritory without the sender: the owner is "probe".
	void DZOProbeCreate(string territoryName, TerritoryFlag flag)
	{
		string texturePath = flag.GetFlagTexturePath();
		ExpansionTerritory territory = new ExpansionTerritory(m_NextTerritoryID, territoryName, 1, "probe", flag.GetPosition(), texturePath);
		flag.SetIsExpansionTerritoryFlag(true);
		flag.SetTerritoryID(m_NextTerritoryID);
		flag.SetOwnerID("probe");
		territory.AddMember("probe", "probe", true);
		flag.SetTerritory(territory);
		m_TerritoryFlags.Insert(m_NextTerritoryID, flag);
		UpdateClient(m_NextTerritoryID);
		m_NextTerritoryID++;
	}
}
#endif

// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid for dzo's BattlEye event patterns, never for production. A player who has this mod sends
// two chat lines a minute after the mission started, so a BattlEye RCon client sees chat events
// without anyone typing. MissionGameplay exists on the client only: the server loads the mod
// (it has to hold the key) but never runs this.
modded class MissionGameplay
{
	override void OnInit()
	{
		super.OnInit();
		GetGame().GetCallQueue(CALL_CATEGORY_GUI).CallLater(DZOBETestChat, 60000, false, "dzo-be-test first line");
		GetGame().GetCallQueue(CALL_CATEGORY_GUI).CallLater(DZOBETestChat, 75000, false, "dzo-be-test second: with a colon");
	}

	void DZOBETestChat(string text)
	{
		Print("DZO-BE-TEST sending chat: " + text);
		GetGame().ChatPlayer(text);
	}
}

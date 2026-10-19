// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid, never for production: shows what a server-only mod can put on a player's screen. A file
// $profile:dzo_notify/<style> makes the probe send one notification to every connected player:
//   vanilla                                                       NotificationSystem.SendNotificationToPlayerIdentityExtended
//   toast baguette activity killfeed market garage                ExpansionNotification(...).Create(), one per ExpansionNotificationType
// The Expansion styles exist only when Expansion is loaded (EXPANSIONMODCORE, checked in the script log).
modded class MissionServer
{
	override void OnInit()
	{
		super.OnInit();
		MakeDirectory("$profile:dzo_notify");
		GetGame().GetCallQueue(CALL_CATEGORY_SYSTEM).CallLater(DZONotifyTick, 1000, true);
	}

	void DZONotifyTick()
	{
		TStringArray styles = {"vanilla", "toast", "baguette", "activity", "killfeed", "market", "garage"};
		foreach (string style : styles)
		{
			string path = "$profile:dzo_notify/" + style;
			if (!FileExist(path))
				continue;
			DeleteFile(path);
			array<Man> men = new array<Man>;
			GetGame().GetPlayers(men);
			foreach (Man m : men)
			{
				if (m.GetIdentity())
					DZONotifySend(m.GetIdentity(), style);
			}
			Print("DZO-NOTIFY sent " + style + " to " + men.Count() + " player(s)");
		}
	}

	void DZONotifySend(PlayerIdentity id, string style)
	{
		string title = "dzo " + style;
		string text = "Server restart in 10 minutes";
		if (style == "vanilla")
		{
			NotificationSystem.SendNotificationToPlayerIdentityExtended(id, 6, title, text, "");
			return;
		}
#ifdef EXPANSIONMODCORE
		ExpansionNotificationType type = ExpansionNotificationType.TOAST;
		switch (style)
		{
		case "baguette": type = ExpansionNotificationType.BAGUETTE; break;
		case "activity": type = ExpansionNotificationType.ACTIVITY; break;
		case "killfeed": type = ExpansionNotificationType.KILLFEED; break;
		case "market": type = ExpansionNotificationType.MARKET; break;
		case "garage": type = ExpansionNotificationType.GARAGE; break;
		}
		ExpansionNotification(title, text, "", ARGB(255, 52, 152, 219), 6, type).Create(id);
#else
		Print("DZO-NOTIFY no Expansion in this build, cannot send " + style);
#endif
	}
}

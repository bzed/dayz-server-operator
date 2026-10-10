// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid for scripts/test-client.sh, never for production. Every 3 s it logs what each connected
// player has, as one line
//   DZO-PROBE player <name> items=<count> chemlight=<count> hands=<type> transport=<0|1> crew=<vehicle>/<seat0>
// and a file $profile:dzo_probe/board puts the first player into the driver seat of a new Offroad_02,
// so dzo-admin's vehicle crew can be checked. The player is the only thing the probe touches.
modded class MissionServer
{
	override void OnInit()
	{
		super.OnInit();
		MakeDirectory("$profile:dzo_probe");
		GetGame().GetCallQueue(CALL_CATEGORY_SYSTEM).CallLater(DZOProbeTick, 3000, true);
	}

	void DZOProbeTick()
	{
		array<Man> men = new array<Man>;
		GetGame().GetPlayers(men);
		foreach (Man m : men)
		{
			PlayerBase p = PlayerBase.Cast(m);
			if (!p)
				continue;
			if (FileExist("$profile:dzo_probe/board"))
			{
				DeleteFile("$profile:dzo_probe/board");
				DZOProbeBoard(p);
			}
			array<EntityAI> items = new array<EntityAI>;
			p.GetInventory().EnumerateInventory(InventoryTraversalType.PREORDER, items);
			int light = 0;
			foreach (EntityAI e : items)
			{
				if (e.GetType() == "Chemlight_Red")
					light++;
			}
			string hands = "none";
			EntityAI inHands = p.GetHumanInventory().GetEntityInHands();
			if (inHands)
				hands = inHands.GetType();
			string crew = "";
			Transport t = Transport.Cast(p.GetParent());
			if (t)
			{
				string driver = "none";
				Human h = t.CrewMember(0);
				if (h)
					driver = h.GetIdentity().GetName();
				crew = t.GetType() + "/" + driver;
			}
			Print("DZO-PROBE player " + p.GetIdentity().GetName() + " items=" + items.Count() + " chemlight=" + light + " hands=" + hands + " transport=" + p.IsInTransport() + " crew=" + crew);
		}
	}

	void DZOProbeBoard(PlayerBase p)
	{
		Transport car = Transport.Cast(GetGame().CreateObjectEx("Offroad_02", p.GetPosition() + "4 0 0", ECE_PLACE_ON_SURFACE | ECE_CREATEPHYSICS));
		if (!car)
		{
			Print("DZO-PROBE board FAIL: could not create the car");
			return;
		}
		p.StartCommand_Vehicle(car, 0, 0);
		Print("DZO-PROBE board: " + p.GetIdentity().GetName() + " into " + car.GetType());
	}
}

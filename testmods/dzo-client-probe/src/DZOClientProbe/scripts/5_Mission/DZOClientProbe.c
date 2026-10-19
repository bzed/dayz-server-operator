// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid for scripts/test-client.sh, never for production. Every 3 s it logs what each connected
// player has, as one line
//   DZO-PROBE player <name> items=<count> chemlight=<count> hands=<type> kit=<Type:Part+Part; of the items that carry attachments> transport=<0|1> crew=<vehicle>/<seat0>
// followed by hp=<health> blood=<blood> gear=<health of the worst item in percent> alive=<0|1>, a file
// $profile:dzo_probe/hurt injures the first player (health, blood, every item they carry), and a file $profile:dzo_probe/board puts the first player into the driver seat of a new Offroad_02,
// so dzo-admin's vehicle crew can be checked. The player is the only thing the probe touches.
modded class MissionServer
{
	protected ref array<Object> m_BareCars;

	override void OnInit()
	{
		super.OnInit();
		MakeDirectory("$profile:dzo_probe");
		GetGame().GetCallQueue(CALL_CATEGORY_SYSTEM).CallLater(DZOProbeTick, 3000, true);
	}

	void DZOProbeTick()
	{
		// These two need no player: the server-only run of test-client.sh (--no-client) uses them.
		if (FileExist("$profile:dzo_probe/event"))
		{
			DeleteFile("$profile:dzo_probe/event");
			DZOProbeEvent();
		}
		if (FileExist("$profile:dzo_probe/gas"))
		{
			DeleteFile("$profile:dzo_probe/gas");
			// What a thrown M18 smoke grenade ends up with: the dynamic area.
			Object gas = GetGame().CreateObjectEx("ContaminatedArea_Dynamic", Vector(7300, GetGame().SurfaceY(7300, 7300), 7300), ECE_PLACE_ON_SURFACE);
			Print("DZO-PROBE gas: " + gas);
		}
		if (FileExist("$profile:dzo_probe/flag"))
		{
			DeleteFile("$profile:dzo_probe/flag");
			// A territory flag with half of its refresher time left (20 days).
			TerritoryFlag flag = TerritoryFlag.Cast(GetGame().CreateObjectEx("TerritoryFlag", Vector(7600, GetGame().SurfaceY(7600, 7600), 7600), ECE_PLACE_ON_SURFACE));
			if (flag)
				flag.SetRefreshTimer01(0.5);
			Print("DZO-PROBE flag: " + flag);
		}
#ifdef EXPANSIONMODBASEBUILDING
		if (FileExist("$profile:dzo_probe/territory"))
		{
			DeleteFile("$profile:dzo_probe/territory");
			// An Expansion territory without a player: what the territory module does on a client's request.
			TerritoryFlag tflag = TerritoryFlag.Cast(GetGame().CreateObjectEx("TerritoryFlag", Vector(7700, GetGame().SurfaceY(7700, 7700), 7700), ECE_PLACE_ON_SURFACE));
			ExpansionTerritoryModule tmod;
			CF_Modules<ExpansionTerritoryModule>.Get(tmod);
			if (tflag && tmod)
				tmod.DZOProbeCreate("Probe Camp", tflag);
			Print("DZO-PROBE territory: " + tflag + " " + tmod);
		}
#endif
		if (FileExist("$profile:dzo_probe/bike"))
		{
			DeleteFile("$profile:dzo_probe/bike");
			DZOProbeBike();
		}
		if (FileExist("$profile:dzo_probe/bare"))
		{
			DeleteFile("$profile:dzo_probe/bare");
			DZOProbeBareCars();
		}
		if (FileExist("$profile:dzo_probe/parts"))
		{
			DeleteFile("$profile:dzo_probe/parts");
			DZOProbeParts(Vector(7400, 0, 7400), "bare");
			DZOProbeParts(Vector(7600, 0, 7600), "spawned");
		}
		if (FileExist("$profile:dzo_probe/ground"))
		{
			DeleteFile("$profile:dzo_probe/ground");
			DZOProbeGround();
		}
		if (FileExist("$profile:dzo_probe/items"))
		{
			DeleteFile("$profile:dzo_probe/items");
			DZOProbeItems();
		}
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
			if (FileExist("$profile:dzo_probe/hurt"))
			{
				DeleteFile("$profile:dzo_probe/hurt");
				p.SetHealth("", "Health", 40);
				p.SetHealth("", "Blood", 3000);
				foreach (EntityAI hurt : items)
				{
					if (hurt.GetMaxHealth("", "") > 0)
						hurt.SetHealth("", "", hurt.GetMaxHealth("", "") * 0.1);
				}
			}
			int worst = 100;
			foreach (EntityAI gear : items)
			{
				if (gear.GetMaxHealth("", "") > 0 && !gear.IsInherited(Man))
					worst = Math.Min(worst, Math.Round(gear.GetHealth01("", "") * 100));
			}
			int alive = 0;
			if (p.IsAlive())
				alive = 1;
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
			// "Type:Part+Part;" for every item that carries attachments (the player's clothes are skipped by the grep).
			string kit = "";
			foreach (EntityAI k : items)
			{
				if (k.GetInventory().AttachmentCount() == 0 || k.IsClothing())
					continue;
				kit = kit + k.GetType() + ":";
				for (int a = 0; a < k.GetInventory().AttachmentCount(); a++)
					kit = kit + k.GetInventory().GetAttachmentFromIndex(a).GetType() + "+";
				kit = kit + ";";
			}
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
			Print("DZO-PROBE player " + p.GetIdentity().GetName() + " items=" + items.Count() + " chemlight=" + light + " hands=" + hands + " kit=" + kit + " transport=" + p.IsInTransport() + " crew=" + crew + " hp=" + Math.Round(p.GetHealth("", "Health")) + " blood=" + Math.Round(p.GetHealth("", "Blood")) + " gear=" + worst + " alive=" + alive);
		}
	}

	// A Central Economy event object as the CE places it: a police car wreck, a plain engine object that has
	// no script class. The test's watch rule selects its class.
	void DZOProbeEvent()
	{
		Object o = GetGame().CreateObjectEx("Land_Wreck_sed01_aban1_police", Vector(7500, GetGame().SurfaceY(7500, 7500), 7500), ECE_PLACE_ON_SURFACE);
		Print("DZO-PROBE event: " + o + " at " + o.GetPosition());
	}

	// 1.30: a parked motorbike, for dzo-admin's vehicle list.
	void DZOProbeBike()
	{
		Object bike = GetGame().CreateObjectEx("Motorbike_01_Blue", "7520 0 7500", ECE_PLACE_ON_SURFACE | ECE_CREATEPHYSICS);
		Print("DZO-PROBE bike: " + bike);
	}

	// Cars without parts, as the economy leaves them, for dzo-admin's "add missing parts".
	void DZOProbeBareCars()
	{
		TStringArray types = {"Offroad_02", "Hatchback_02", "CivilianSedan", "OffroadHatchback"};
		for (int i = 0; i < types.Count(); i++)
		{
			vector pos = Vector(7400 + i * 8, 0, 7400);
			pos[1] = GetGame().SurfaceY(pos[0], pos[2]);
			Object car = GetGame().CreateObjectEx(types.Get(i), pos, ECE_PLACE_ON_SURFACE | ECE_CREATEPHYSICS);
			Print("DZO-PROBE bare: " + types.Get(i) + " " + car);
			// the position query below does not always find every one of them
			if (!m_BareCars)
				m_BareCars = new array<Object>;
			m_BareCars.Insert(car);
		}
	}

	// DZO-PROBE parts <tag> <type> <Part+Part+...>: the attachments of every car around pos.
	void DZOProbeParts(vector pos, string tag)
	{
		array<Object> objs = new array<Object>;
		array<CargoBase> proxies = new array<CargoBase>;
		pos[1] = GetGame().SurfaceY(pos[0], pos[2]);
		GetGame().GetObjectsAtPosition(pos, 40, objs, proxies);
		if (tag == "bare" && m_BareCars)
		{
			foreach (Object b : m_BareCars)
			{
				if (b && objs.Find(b) < 0)
					objs.Insert(b);
			}
		}
		foreach (Object o : objs)
		{
			CarScript car = CarScript.Cast(o);
			if (!car)
				continue;
			string parts = "";
			for (int a = 0; a < car.GetInventory().AttachmentCount(); a++)
				parts = parts + car.GetInventory().GetAttachmentFromIndex(a).GetType() + "+";
			Print("DZO-PROBE parts " + tag + " " + car.GetType() + " " + parts);
		}
	}

	// DZO-PROBE ground <type> stacks=<n> quantity=<sum>: Nail items lying around 7450/7450.
	void DZOProbeGround()
	{
		array<Object> objs = new array<Object>;
		array<CargoBase> proxies = new array<CargoBase>;
		GetGame().GetObjectsAtPosition(Vector(7450, GetGame().SurfaceY(7450, 7450), 7450), 20, objs, proxies);
		int stacks = 0;
		float qty = 0;
		foreach (Object o : objs)
		{
			ItemBase ib = ItemBase.Cast(o);
			if (!ib || ib.GetType() != "Nail")
				continue;
			stacks++;
			qty = qty + ib.GetQuantity();
		}
		Print("DZO-PROBE ground Nail stacks=" + stacks + " quantity=" + qty);
	}

	// DZO-PROBE items <type>=<quantity>/<max> ...: the items lying within 12 m of 7480/7480 and of 7460/7460.
	void DZOProbeItems()
	{
		array<vector> spots = {Vector(7480, 0, 7480), Vector(7460, 0, 7460)};
		foreach (vector spot : spots)
		{
			array<Object> objs = new array<Object>;
			array<CargoBase> proxies = new array<CargoBase>;
			GetGame().GetObjectsAtPosition(Vector(spot[0], GetGame().SurfaceY(spot[0], spot[2]), spot[2]), 12, objs, proxies);
			string list = "";
			foreach (Object o : objs)
			{
				ItemBase ib = ItemBase.Cast(o);
				if (ib)
					list = list + ib.GetType() + "=" + ib.GetQuantity() + "/" + ib.GetQuantityMax() + " ";
			}
			Print("DZO-PROBE items " + spot[0] + " " + list);
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

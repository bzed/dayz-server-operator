// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid, never for production: makes the game events dzo-admin's event feed reports happen without a
// player at the keyboard (see scripts/test-client.sh). The players are characters without an identity, so
// the events are told apart by their positions: the kill is at 7100 7000, the base at 7000 7200.
class DZOProbeFeed
{
	protected static PlayerBase Spawn(string type, vector at)
	{
		return PlayerBase.Cast(GetGame().CreatePlayer(null, type, at, 0, "NONE"));
	}

	static void Run()
	{
		vector killerAt = Vector(7000, GetGame().SurfaceY(7000, 7000), 7000);
		vector victimAt = Vector(7100, GetGame().SurfaceY(7100, 7000), 7000);
		PlayerBase killer = Spawn("SurvivorM_Mirek", killerAt);
		PlayerBase victim = Spawn("SurvivorF_Eva", victimAt);
		PlayerBase loner = Spawn("SurvivorM_Boris", Vector(7050, GetGame().SurfaceY(7050, 7050), 7050));
		if (!killer || !victim || !loner)
		{
			Print("DZO-PROBE feed: no characters");
			return;
		}
		// A rifle shot 100 m away.
		EntityAI rifle = killer.GetHumanInventory().CreateInHands("M4A1");
		victim.SetHealth("GlobalHealth", "Health", 1);
		victim.ProcessDirectDamage(DamageType.FIRE_ARM, rifle, "Torso", "Bullet_556x45", "0 0 0", 1);
		// Death without a killer.
		loner.SetHealth("", "", 0);
#ifdef EXPANSIONMODAI
		// The same shot at an Expansion AI, 150 m away: the victim is named, its faction is not a raw key.
		eAIBase bot = eAIBase.Cast(GetGame().CreateObject(eAISurvivor.GetQuasiRandom(), Vector(7150, GetGame().SurfaceY(7150, 7000), 7000)));
		if (bot)
		{
			Print("DZO-PROBE feed: ai '" + bot.GetCachedName() + "'");
			bot.SetHealth("GlobalHealth", "Health", 1);
			bot.ProcessDirectDamage(DamageType.FIRE_ARM, rifle, "Torso", "Bullet_556x45", "0 0 0", 1);
		}
#endif
		Print("DZO-PROBE feed: shot " + victim.IsAlive() + " alone " + loner.IsAlive());

		// A fence: its base, a wall on it, then a raid on the base takes the wall with it.
		vector baseAt = Vector(7000, GetGame().SurfaceY(7000, 7200), 7200);
		Fence fence = Fence.Cast(GetGame().CreateObjectEx("Fence", baseAt, ECE_PLACE_ON_SURFACE));
		if (fence && killer)
		{
			ConstructionBase cons = fence.GetConstruction();
			cons.BuildPartServerEx(killer, "wall_base_down", AT_BUILD_PART);
			cons.BuildPartServerEx(killer, "wall_wood_down", AT_BUILD_PART);
			cons.DismantlePartServerEx(killer, "wall_wood_down", AT_DISMANTLE_PART);
			cons.BuildPartServerEx(killer, "wall_wood_down", AT_BUILD_PART);
			cons.DestroyPartServer(killer, "wall_base_down", AT_DESTROY_PART);
		}
		Print("DZO-PROBE feed: fence " + fence);

		// A flag raised and lowered by a player.
		vector flagAt = Vector(7200, GetGame().SurfaceY(7200, 7200), 7200);
		TerritoryFlag flag = TerritoryFlag.Cast(GetGame().CreateObjectEx("TerritoryFlag", flagAt, ECE_PLACE_ON_SURFACE));
		if (flag && killer)
		{
			flag.AnimateFlagEx(0, killer);
			flag.AnimateFlagEx(1, killer);
		}
		Print("DZO-PROBE feed: flag " + flag);

		Print("DZO-PROBE feed: done");
	}
}

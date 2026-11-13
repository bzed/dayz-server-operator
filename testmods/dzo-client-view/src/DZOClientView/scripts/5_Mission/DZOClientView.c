// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Test aid for scripts/test-client.sh --bike-view, never for production. Loaded on the client, it logs every 2 s
// what this client knows about the vehicles around the player, one line each:
//   DZO-VIEW <type> <network id> x=<x> y=<y> z=<z> ground=<terrain height there>
// The server's vehicle list says where the server has a vehicle; only this says where a client sees it.
modded class MissionGameplay
{
	protected float m_DZOViewWait;

	override void OnUpdate(float timeslice)
	{
		super.OnUpdate(timeslice);
		m_DZOViewWait -= timeslice;
		if (m_DZOViewWait > 0)
			return;
		m_DZOViewWait = 2;
		Man player = GetGame().GetPlayer();
		if (!player)
			return;
		array<Object> objects = new array<Object>;
		vector me = player.GetPosition();
		vector dir = player.GetDirection();
		Print("DZO-SELF x=" + me[0] + " z=" + me[2] + " dx=" + dir[0] + " dz=" + dir[2]);
		GetGame().GetObjectsAtPosition(player.GetPosition(), 800, objects, null);
		foreach (Object o : objects)
		{
			if (!Transport.Cast(o))
				continue;
			vector p = o.GetPosition();
			string wheels = "";
			Motorbike bike = Motorbike.Cast(o);
			if (bike)
			{
				wheels = " surface=" + GetGame().SurfaceRoadY3D(p[0], p[1] + 2, p[2], RoadSurfaceDetection.UNDER) + " ypr=" + o.GetOrientation() + " kick=" + bike.GetKickstand() + " fallen=" + bike.IsFallen();
				for (int i = 0; i < bike.WheelCount(); i++)
				{
					vector hub = bike.ModelToWorld(bike.WheelGetPositionLS(i));
					wheels += " w" + i + "=" + bike.WheelHasContact(i) + "/hub " + hub[1] + "/contact " + bike.WheelGetContactPosition(i)[1];
				}
			}
			Print("DZO-VIEW " + o.GetType() + " " + o.GetNetworkIDString() + " x=" + p[0] + " y=" + p[1] + " z=" + p[2] + " ground=" + GetGame().SurfaceY(p[0], p[2]) + wheels);
		}
	}
}

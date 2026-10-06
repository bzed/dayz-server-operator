// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Checks that file lookups with $profile: and $mission: work on this server, through CF.FindFileEx
// (CF-Test on 1.30: vanilla 1.30 FindFile ignores the placeholders). Every check logs one line
//   DZO-PROBE <name> OK|FAIL ...
// and the end logs DZO-PROBE RESULT OK|FAIL. "control" lines show what the engine's own FindFile
// does and never fail the result.
class DZOFindFileProbe
{
	static const string TAG = "DZO-PROBE";

	static ref array<string> Find(string pattern, bool viaCF)
	{
		ref array<string> names = new array<string>;
		string fileName;
		FileAttr attr;
		FindFileHandle h;
		if (viaCF)
			h = CF.FindFileEx(pattern, fileName, attr, FindFileFlags.ALL);
		else
			h = FindFile(pattern, fileName, attr, FindFileFlags.ALL);
		bool found = fileName != "";
		while (found)
		{
			names.Insert(fileName);
			found = FindNextFile(h, fileName, attr);
		}
		if (h)
			CloseFindFile(h);
		return names;
	}

	static void Write(string path)
	{
		FileHandle fh = OpenFile(path, FileMode.WRITE);
		if (fh != 0)
		{
			FPrintln(fh, "dzo findfile probe");
			CloseFile(fh);
		}
	}

	// exact > 0: that many files must match; 0: at least one. want: a file name that must be among them.
	static bool Check(string name, string pattern, string want, int exact)
	{
		array<string> found = Find(pattern, true);
		bool ok = found.Count() > 0;
		if (exact > 0)
			ok = found.Count() == exact;
		if (want != "" && found.Find(want) == -1)
			ok = false;
		string status = "FAIL";
		if (ok)
			status = "OK";
		Print(TAG + " " + name + " " + status + " pattern=" + pattern + " found=" + found.Count() + " want=" + want);
		return ok;
	}

	static void Control(string name, string pattern)
	{
		array<string> found = Find(pattern, false);
		Print(TAG + " control " + name + " pattern=" + pattern + " found=" + found.Count());
	}

	static void Run()
	{
		MakeDirectory("$profile:dzo_probe");
		Write("$profile:dzo_probe/probe-a.txt");
		Write("$profile:dzo_probe/probe-b.txt");

		bool ok = true;
		// the server's own log is in the profiles folder, the probe files were just written there
		ok = Check("profile-rpt", "$profile:*.RPT", "", 0) && ok;
		ok = Check("profile-files", "$profile:dzo_probe/probe-*.txt", "probe-b.txt", 2) && ok;
		// the mission folder: the Central Economy files and init.c
		ok = Check("mission-xml", "$mission:*.xml", "cfgeconomycore.xml", 0) && ok;
		ok = Check("mission-db", "$mission:db/*.xml", "types.xml", 0) && ok;
		ok = Check("mission-init", "$mission:*.c", "init.c", 0) && ok;

		Control("profile-files", "$profile:dzo_probe/probe-*.txt");
		Control("mission-xml", "$mission:*.xml");

		string result = "FAIL";
		if (ok)
			result = "OK";
		Print(TAG + " RESULT " + result);
	}
}

modded class MissionServer
{
	override void OnInit()
	{
		super.OnInit();
		g_Game.GetCallQueue(CALL_CATEGORY_SYSTEM).CallLater(ProbeFindFile, 5000, false);
	}

	void ProbeFindFile()
	{
		DZOFindFileProbe.Run();
	}
}

.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

FAQ: things we found out the hard way
=====================================

These answers come from running dzo on a development host (Debian 13, podman 5.4.2, btrfs, DayZ
Server 1.29 and 1.30 experimental). They explain why dzo does some things the way it does.

Containers and servers
----------------------

Why does dzo mount the server build as an overlay and not read-only?
   With a ``:ro`` mount ``DayZServer`` silently skips every addon directory except ``addons/`` and
   ``dta/``: the Sakhal DLC and every ``-mod`` and ``-servermod`` are not loaded, without an error. A
   podman overlay (``:O``) is writable for the server and leaves the shared build untouched, so dzo
   uses it for the build and for every mod. Do not add ``:ro`` to mounts that hold mods or the build.

Where does the server write its data?
   Only below ``/dayz``: with ``-storage=/dayz/storage`` and ``-profiles=/dayz/profiles`` nothing is
   written into the build. The live mission (``instances/<name>/mpmissions``) is a writable mount;
   in our runs, with dzo-admin, it held only files dzo manages. Mods such as Expansion may write
   other files there, which dzo reports as foreign files.

How long does a server take to start?
   About 30 seconds from start to healthy on stable and 45 seconds on experimental, on a small
   test host without many mods. A startup timeout of a few minutes is generous, but a server with
   many mods will need more.

Can I use the network mode ``publish``?
   Not for RCon yet: RCon is not published, so dzo cannot reach it from the host. Use ``host``.

Can I run several instances on one host?
   Yes. With host networking each instance gets its own game, query and RCon ports from one range
   and they start and stop independently.

Why is ``dzo`` required at ``/usr/bin/dzo``?
   The generated units mount that binary into the container (the health check is ``dzo health``).

Why did a unit fail with an unknown ``ContainerStopTimeout``?
   There is no such quadlet key. The key is ``StopTimeout=`` (seconds), and dzo sets systemd's
   ``TimeoutStopSec`` to the stop timeouts plus 30 seconds so that systemd does not kill podman
   before its grace period is over. Current dzo versions generate the right keys.

Snapshots
---------

Do snapshots need root?
   No. As the service user, creating subvolumes, read-only snapshots, writable restores and deleting
   snapshots work on Linux 7.2 and on Debian 13's 6.12.

Why is the snapshot before an update skipped?
   Because the instance directory was created before it was a subvolume, so it is a plain directory.
   dzo warns and skips the snapshot; ``dzo setup`` does not convert it. Create the instance again as
   a subvolume, or move the data into a new subvolume yourself.

BattlEye and RCon
-----------------

RCon was reachable from the whole network. Is that fixed?
   Yes. A BattlEye server listens on every interface by default, so anyone who knew the password
   could control the server. With host networking dzo writes ``RConIP 127.0.0.1`` into the BattlEye
   config.

Why do RCon commands time out for about 40 seconds after a restart?
   After a restart of the game server the old connection reports "connection refused" within a
   second. The new one can log in about 20 seconds after the restart began, while the mission is
   still loading, and commands are answered about 20 seconds later, when the mission has loaded. dzo
   reconnects with backoff, and commands in that window time out; that is expected.

Why does dzo send every command up to three times?
   The answer travels over UDP and may never come. A command waits at most 15 seconds per try and is
   tried three times. A real server also ignores some logins when connections follow each other
   closely (13 % with no pause, 10 % after 0.3 s, 3 % after 1 s, none after 2 s). dzo keeps one
   connection and dials again with backoff, so you do not notice.

How do I kick a player?
   BattlEye's own ``kick <id> <reason>``, which is what the graceful restart sends. ``#kick 0`` kicks
   nobody and ``#kick -1`` kicks everybody, so be careful with it.

How does the server log player events?
   From a real capture (a BattlEye client, 1.29):

   .. code-block:: text

      Player #0 dzotester (10.125.0.3:41939) connected
      Player #0 dzotester - BE GUID: 907725ff524e1026154b5ac3cccb0258
      Connected to BE Master
      Ban check timed out, no response from BE Master
      (Global) dzotester: dzo-be-test second: with a colon
      Player #0 dzotester (907725ff524e1026154b5ac3cccb0258) has been kicked by BattlEye: Admin Kick (dzo test kick)
      Player #0 dzotester disconnected

   Note ``BE GUID:`` in the GUID line, and the kick line with the GUID in parentheses and the reason
   as ``Admin Kick (<reason>)``.

Why is a player's GUID shown as unverified?
   The BattlEye master never answered in our tests (``Ban check timed out``), so the GUID stays
   unverified (``(?)`` in ``players``). The pattern for the later ``Verified GUID`` line is still a
   guess, because we never saw one. Not verified either: a ``players`` answer with 60 players, and the
   native DayZ ``ban.txt`` (see moderation).

What does BattlEye's bans.txt look like?
   Captured from a real server (``internal/moderation/testdata/v1``): one ban per line,
   ``<GUID or IP> <expiry> <reason>``, where the expiry is ``-1`` for a permanent ban and an absolute
   Unix time otherwise. The reason is cut to 64 characters, and an IPv6 address is silently not
   banned. A ``//`` comment line is ignored when loaded and not kept by ``writeBans``, which also
   drops expired bans. ``addBan`` writes the file by itself. dzo writes this file itself; see
   moderation.

Missions and mod files
----------------------

How is dzo's merge different from the legacy ``xmlmerge``?
   ``xmlmerge`` does no merging: its output is the children of the mod file followed by the children
   of the base file, so nothing is replaced and duplicates stay. dzo replaces an element of the same
   ``name`` and appends the rest. ``mapgrouppos.xml`` is the exception: it holds many ``<group>``
   elements of one name, so groups are matched by name and position. The result has the same
   elements, in a different order (mod elements last), and duplicates inside one mod file collapse
   into one. Whether DayZ takes the first or the last of two elements of one name is not known; dzo's
   way avoids the question.

What did the legacy start get wrong that dzo does not?
   * ``cfgeventspawns.xml`` could come out empty: ``xmlmerge`` failed on a mod file, ``xmlstarlet``
     wrote nothing, and the empty file replaced the mission's.
   * It kept duplicate elements (for example ``HypeTrain_Myshkino`` twice).
   * ``INIT=local`` asks for an ``init.c`` that does not exist, and under ``set -e`` that stopped
     the legacy start before ``types.xml`` for one mod.
   * It registered an overlay's ``globals.xml`` as a Central Economy file. dzo merges its variables
     into ``db/globals.xml``.

   See :doc:`migration` for what the converter does about them. The output is not byte-equal to the
   legacy one by design; compare it as data.

Why was a mod's XML file rejected or not merged?
   A mod file without a root element (a bare list of ``<event>``) cannot be merged. dzo does not
   normalise such files yet: ``normalize:`` is ignored with a warning, so fix or wrap the file.

Why does the order of two mods replacing one file matter?
   Where two mods replace the same file (``cfgweather.xml``), dzo goes by the order of the mod
   list. The legacy start used the order ``find`` listed the mod links in, so the winner can differ.

Map data
--------

Can dzo download the map data (``worlds_*_data.pbo``) for the map tiles by itself?
   No. The file is in depot 221101 (25.6 GB) of app 221100, and ``steamcmd`` cannot download a single
   file of a depot: it does not know ``sDepotDownloadFileFilter``, and the following
   ``download_depot`` would fetch the whole depot. Give ``dzo map tiles build`` the path to the PBO
   from a client install. A tool that can filter a depot (DepotDownloader has ``-filelist``) would
   do it, but is not a dependency. Whether the service account owns DayZ and may download the depot
   was not tested.

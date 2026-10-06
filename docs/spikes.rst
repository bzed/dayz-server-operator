.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Phase 0 spikes: what was found
==============================

The plan lists ten spikes (S0 to S10) that were to run before the real code. Most of them were
settled by building the code and running it on a development host: Debian 13, kernel 7.2.8, podman
5.4.2, two vCPUs, 16 GB of RAM, btrfs, DayZ Server 1.29.163709 (build 24570360) and 1.30 experimental
1.30.164014 (build 25319221). This page says, per spike, what is known, how, and what is still open.
"Run" means measured on that host; "not run" means nobody has done it.

.. list-table::
   :header-rows: 1
   :widths: 8 30 14 48

   * - Spike
     - Question
     - Status
     - In one line
   * - S0
     - Golden master of the legacy renderer
     - partly run
     - three branches compared as data; found and fixed a real merge bug
   * - S1
     - Read-only server root, experimental
     - run
     - a read-only mount breaks mod loading; an overlay works
   * - S2
     - Live mission as a nested writable mount
     - run
     - works; the vanilla server and dzo-admin write nothing into it
   * - S3
     - ``xmlmerge`` semantics
     - run
     - the legacy tool only concatenates; ours replaces by name
   * - S4
     - BattlEye RCon against a real server
     - partly run
     - protocol, keep-alive and restarts verified; no players, so no events
   * - S5
     - Host networking, several instances
     - run
     - works; RCon was open to the network and is bound to localhost now
   * - S6
     - Quadlet features on podman 5.4.2
     - run
     - works, with two wrong keys fixed
   * - S7
     - Unprivileged btrfs on the target kernel
     - run
     - snapshots work as the service user on kernel 7.2 and 6.12
   * - S8
     - ``dzo-admin`` feasibility
     - run
     - see the admin map page
   * - S9
     - Boot test groundwork
     - run
     - ``dzo test boot`` and its fixtures
   * - S10
     - Map data from the client depot
     - run, negative
     - steamcmd cannot download one file of a depot

S0: golden master
-----------------

The legacy ``mergexml`` function (and ``xml.sh``, unchanged) was run in a Debian container
(``scripts/s0/legacy-render.sh``: xmlstarlet, ``xmlmerge``, jq, patch) on the files of a legacy branch, with
Bohemia's Central Economy repository as the pristine mission and every mod of the branch active. The result
was compared with what dzo builds for the converted branch (``TestGoldenAgainstLegacyRender`` in
``internal/legacy``, run with ``DZO_S0_LEGACY``, ``DZO_S0_SITE``, ``DZO_S0_INSTANCE`` and ``DZO_S0_CE``). The two
cannot be byte-equal by design (the legacy tool concatenates, dzo replaces by name, the formatting differs), so
the files are compared as data: the elements below the root as a multiset, JSON as a tree.

Run for ``chernarus``, ``livonia`` and ``onlyup-prisonbreak``, the branches whose maps are Bohemia's. Not run: ``deerisle``
and ``hashima`` (their mission repositories are not the Central Economy one), the files that live inside a
workshop mod (``source: mod``: not downloaded here, so left out on both sides), and the ``start.sh`` hooks (they need
xmlstarlet and were not run on the dzo side). The legacy merge order is the order ``find`` lists the mod
links in, dzo's is the mod list; where two mods replace one file (``cfgweather.xml``), the winners differ.

What it found:

* **A bug in dzo, fixed.** ``mapgrouppos.xml`` has many ``<group>`` elements of one name, one per position, and dzo
  replaced the *first* group of a name when a mod or overlay brought one of the same name: hundreds of placements
  were lost (673 on Chernarus). Groups are now matched by name and position (``ce.MergeXMLChildren`` takes a list of
  attributes). Every other file keeps matching by name.
* **The legacy start breaks files.** ``cfgeventspawns.xml`` (and on Livonia ``cfgrandompresets.xml``) came out of the legacy
  merge as an empty file: ``xmlmerge`` failed on a mod file, ``xmlstarlet fo`` wrote nothing, the lint failed inside a
  subshell that cannot stop the script, and the empty file replaced the mission's. dzo merges the normalised
  files: 118 events.
* **Duplicates collapse.** The legacy merge keeps both copies of an element: ``HypeTrain_Myshkino`` and
  ``HypeTrain_Petrovka`` twice in ``cfgeventgroups.xml``, and on Livonia 194 groups of ``mapgrouppos.xml`` that two overlays
  both bring (the same object twice at the same position). dzo keeps one.
* **``init.c`` made the legacy start skip a file.** ``xml.sh`` runs under ``set -e`` and ``INIT=local`` asks for an ``init.c`` that does not exist,
  so for mod 2981609048 it stopped before ``types.xml``: dzo registers ``mod_2981609048`` (and the converter keeps the
  patch but does not activate it, see :doc:`migration`).
* **Registration of ``globals.xml``.** The legacy start registered the ``globals.xml`` of an overlay as a Central Economy file; dzo copies it
  and does not.
* **Extra files.** The legacy start copied the merged files into ``custom_<name>/`` again and the other files only when the overlay had
  a Central Economy file; dzo copies the files it did not merge, always (so the JSON files that ``cfggameplay.json`` points at
  are there).
* **Same data, other bytes** for ``cfgenvironment.xml``, ``mapgroupproto.xml``, ``cfgundergroundtriggers.json`` and several
  ``types.xml``: the formatting.
* **The ``Land_Train_*`` groups** of Chernarus are still in dzo's ``mapgrouppos.xml`` here only because the HypeTrain ``start.sh`` that removes them was not run.

The converter learned two things from this: a ``github.com/…/blob/…`` URL in an ``xml.env`` (the page, not the file) is turned into the raw URL,
and an ``init.c`` patch is no longer activated, because the legacy start never applied it and the one that was tried does not apply
to the current Central Economy ``init.c``. The "same bytes as the legacy render" of the plan is therefore replaced by "the same data,
except for the listed fixes".

S1: a read-only server root
---------------------------

The server directory of a build is an immutable generation, so the container should not be able to
change it. Findings, on stable and experimental:

* Mounting the build (or a mod) with ``:ro`` makes ``DayZServer`` silently skip every addon
  directory except ``addons/`` and ``dta/``: the Sakhal DLC and every ``-servermod``/``-mod`` are not
  loaded, with no error and no script from them. 416 Game-module files instead of 422 with dzo-admin.
  Giving the mod a writable mount fixes it, so the cause is the read-only flag.
* A podman overlay (``:O``) is writable for the server and leaves the generation untouched. dzo uses it
  for the build and for every mod.
* Persistence is not written below ``/dayz``: with ``-storage=/dayz/storage`` and ``-profiles=/dayz/profiles`` the
  server writes only there.
* Time to ready on the host: about 30 seconds from start to "healthy" on stable, 45 seconds on
  experimental (``dzo instance restart`` returns after about 45 seconds). Startup timeouts of a few
  minutes are generous. Not measured: the five production servers with their mods.
* Experimental consuming workshop content through app 221100 is still unverified: no workshop mod was
  installed on the experimental instance.

S2: the live mission as a nested writable mount
-----------------------------------------------

``instances/<name>/mpmissions`` is mounted writable at ``/dayz/mpmissions`` below the build overlay and
works. After hours of running both servers with dzo-admin, every file in the live mission was a file dzo
manages (43 of 43): nothing else is written there. The inventory of what other mods write (Expansion,
Editor exports) is open; the classification of foreign files exists for them.

S3: ``xmlmerge``
----------------

The legacy start merges mod files with ``xmlmerge`` (gwenhywfar). Measured with real mod files from the
legacy repository and Bohemia's Central Economy files (``TestAgainstXmlmergeOracle`` in
``internal/mission``, which runs when ``DZO_S3_MODS`` and ``DZO_S3_CE`` are set):

* ``xmlmerge`` does no merging: its output is the children of the mod file followed by the children of
  the base file. It never replaces anything and keeps duplicates (an element of the same name twice).
  ``<territories>`` of ``cfgenvironment.xml`` ends up twice, not merged.
* dzo replaces an element of the same ``name`` (and appends the rest). On the real files the set of
  elements is the same as with ``xmlmerge``; the order differs (mod elements come last), and duplicates
  inside one mod file collapse into one (``HypeTrain_Myshkino`` and ``HypeTrain_Petrovka`` are in the
  mod's ``cfgeventgroups.xml`` twice, so 93 elements against 95). Whether DayZ takes the first or the last of two
  elements of one name is not known; dzo's way avoids the question.
* Two real findings: a mod file without a root element (a bare list of ``<event>``) is useless to
  ``xmlmerge`` (it takes the first element for the root) and needs a normalisation, which dzo does not
  have yet (``normalize:`` is ignored with a warning); and ``xmlmerge`` writes broken XML for one
  combination (``cfgrandompresets.xml`` of a mod into the Chernarus file: ``chance="" "0.03"``), where the
  legacy start stops. dzo merges that pair without error.

S4: BattlEye RCon
-----------------

Run against a real server (``TestLiveServer`` and ``TestLiveSessionRestart`` in ``internal/battleye``,
which run when ``DZO_LIVE_BE_ADDR`` and ``DZO_LIVE_BE_PASSWORD`` are set):

* Login, ``players`` (empty list: ``Players on server:`` header, a ruler, ``(0 players in total)``), an
  unknown command (``Unknown command``), ``#lock``, ``#unlock``, ``#kick -1`` and ``#shutdown`` (empty
  answers, ``Result: OK`` in the server log) work.
* The server answers every keep-alive (an empty command), so silence means it is gone.
* On a restart of the game server the old connection reports "connection refused" within a second; the
  new one logs in as soon as the process listens (about 20 seconds after the restart began, while the
  mission was still loading), and commands are answered about 20 seconds later, when the mission has
  loaded. ``Session`` redials with backoff and the commands in that window time out; they are not
  errors of the session.
* A real server ignores some logins when connections follow each other: with a new connection per command,
  13 % of the logins got no answer with no pause between them, 10 % after 0.3 seconds, 3 % after 1 second and
  none after 2 seconds (30 each). Sending the login again within the same five seconds did not help. A
  session that keeps one connection is not affected, and ``Session`` dials again with backoff.
* A command whose answer never comes would hang its caller, because the answer travels over UDP:
  ``Session.Command`` waits at most 15 seconds per try and tries three times. (A ``dzo restart --now`` of the
  experimental instance once showed no sign of life for four minutes, with the RCon connection up; it was
  not reproduced in the runs that followed, and the cause is not known.)
* RCon listened on every interface: the server was controllable from the whole network with the
  password. dzo now writes ``RConIP 127.0.0.1`` into the BattlEye config with host networking.

Not verified because no player was connected: the event messages (connect, GUID, chat, kick) and their
acknowledgement timing, a multi-packet ``players`` answer with 60 players, and the format of the native
``ban.txt``.

S5: host networking and several instances
-----------------------------------------

Two instances (stable and experimental) run side by side with host networking, each with its own game,
query and RCon ports from one range, started and stopped independently. The health probe inside the
container reaches the query port (``dzo health``). With network mode ``publish`` RCon is not published,
so dzo cannot reach it from the host: that mode is not usable for RCon yet.

S6: quadlet features on podman 5.4.2
------------------------------------

``HealthStartup*``, ``HealthOnFailure=kill``, ``Notify=healthy`` (the unit is active only when the
server answers), user units for the service user and ``Volume=…:O`` work. Two keys in dzo's units were
wrong and are fixed: there is no ``ContainerStopTimeout`` (podman refused the whole unit; it is
``StopTimeout=`` in seconds), and systemd's default ``TimeoutStopSec`` is shorter than podman's grace
(now the stop timeouts plus 30 seconds). ``ExecStop=`` lines of the unit run before quadlet's own.
``dzo`` itself must exist at ``/usr/bin/dzo``, because the units mount it into the container.

S7: unprivileged btrfs
----------------------

As the service user, without root: creating subvolumes, read-only snapshots, writable restores and
deleting snapshots work on kernel 7.2.8 on the host and on Debian 13's kernel 6.12 in a VM (see the README).
An instance directory that was created before it was a subvolume (the first one of the development host)
is not one, and then a snapshot before an update is skipped with a warning; ``dzo setup`` does not
convert it.

S8 and S9
---------

``dzo-admin`` (state push, spawn, vehicle repair, markers) is documented in :doc:`admin-map`.
``dzo test boot`` and the log fixtures of broken inputs are in :doc:`development`.

S10: map data from the client depot
-----------------------------------

The plan wanted ``worlds_*_data.pbo`` downloaded as a single file with ``sDepotDownloadFileFilter``.
Findings:

* App 221100's content is depot 221101 (25.6 GB, 21 GB to download) on the public branch, with the
  executables in 221102 and 221103. The data PBOs are inside 221101.
* ``steamcmd`` does not know ``sDepotDownloadFileFilter``: ``Command not found:
  @sDepotDownloadFileFilter``, and the following ``download_depot`` would have fetched the whole depot (the
  test was stopped before any data arrived). There is no per-file download in steamcmd.
* Whether the service account owns DayZ and may download depot 221101 at all was not tested.

So the PBO stays a configured path or a command line argument (``dzo map tiles build``), as it is, and
the automatic download of the plan is dropped unless a tool that can filter a depot (DepotDownloader has
``-filelist``) is accepted as a dependency.

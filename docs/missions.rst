.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Missions, integrations and overlays
===================================

The mission folder holds the Central Economy files and ``init.c``. dzo treats it
with care: **dzo never wipes or recreates the live mission**.

The game world (the persistence, ``storage_1``) is not in the mission folder.
dzo starts the server with ``-storage=/dayz/storage``, and ``/dayz/storage`` is
``<instances>/<name>/storage/<map>`` on the host. The server then writes
``storage_<instanceId>`` there and nothing into the mission, so the mission folder
only holds files dzo and the mods manage. dzo keeps one world per map. Changing
the map starts a fresh world instead of loading the old map's persistence; the
old one stays in its directory. Mods that use the ``$storage:``
placeholder get the same directory.

Pristine and live
-----------------

Each instance has two copies:

``servermpmissions/`` — the pristine mission
   A checkout of the instance's mission repository at the configured ``ref``.
   ``dzo mission update <name>`` fetches a new version. Nothing else changes it.

``mpmissions/<map>/`` — the live mission
   What the server runs. dzo creates it once from the pristine mission
   (``dzo instance create`` or ``dzo mission init``) and then only updates it in
   place.

Which files dzo manages
-----------------------

dzo keeps a manifest of the files it owns in the live mission. The manifest is
``instances/<name>/runtime/mission-manifest.json``. It is not in ``mpmissions/``,
because the game container can write there and dzo must not trust what it finds
there. dzo never reads the old ``mpmissions/.dzo-manifest.json`` of earlier
versions. The first render after the upgrade deletes it. That render starts with
an empty manifest. It treats every pristine file as new, backs up the live copy
of each one in ``filehistory/`` and writes the pristine content. The
``EditorFiles/`` folder stays in the live mission. That render copies every
file in ``instances/<name>/EditorFiles/`` again and records it in the new
manifest, so later deletions work as before. One case needs manual work. If
you delete a file from ``instances/<name>/EditorFiles/`` after the last render
of the old version and before the first render of the new version, its live
copy stays in ``mpmissions/<map>/EditorFiles/``. Remove that copy by hand.

dzo reads and writes the live mission only below ``mpmissions/<map>``. It does
not follow a link that leaves this folder. It stops with an error if
``mpmissions/<map>`` itself is a link, or if a link in a path leaves the folder.
If a managed file is a link, a FIFO or another file that is not a regular file,
dzo does not read it. It counts the file as drift and replaces it with a regular
file. dzo also rejects a link or a special file in the mission repository
(``mission_source``) and in the fallback mission: ``dzo mission update`` and
``dzo instance render`` stop with an error.

Managed files
   Every file that comes from the pristine mission. dzo updates them on every
   render.

Generated files
   Folders dzo creates itself: ``mod_<id>/`` for mod CE data, ``custom_<name>/``
   for overlays, and ``EditorFiles/``.

Foreign files
   Everything else: ``storage_*``, files mods write at runtime, files you
   placed by hand. **dzo never changes or deletes them.** dzo treats paths
   listed in ``mission.unmanaged`` as foreign even if the mission repository
   ships them.

If a managed file changed in the live mission (by a mod or by hand), dzo warns.
It copies the changed file to ``filehistory/`` and overwrites it
(``drift: warn-backup-overwrite``). Recurring drift on the same file usually
means it belongs in ``mission.unmanaged``.

The render
----------

Before every start dzo builds the mission in a staging area:

#. start from the pristine mission;
#. add each mod's integration files, in the order of the ``mods`` list;
#. add overlays (shared ones first, then the instance's own);
#. validate everything: XML and JSON must parse, every referenced file and
   every CE folder must exist;
#. only if everything is valid: take a snapshot if one is due, write the changes
   into the live mission atomically, update the manifest.

If validation fails, dzo aborts the start and the live mission stays untouched.

Preview a render at any time:

.. code-block:: sh

   dzo instance render deerisle --dry-run

``dzo instance render <name>`` (without ``--dry-run``) is the same render, applied.
It is what the unit runs before every start. The first render into an empty
``mpmissions/<map>`` is the one-time initialisation from the pristine mission.
Every later render only touches files dzo manages (in the manifest, or new in
the pristine mission). dzo never writes ``storage_*``, and it never writes
anything matching ``mission.unmanaged``.

The render also prepares what the server needs besides the mission:

- the signature keys in ``runtime/keys``: the server build's and those of every
  client mod. dzo copies them because the container cannot follow links into the
  cache;
- your own signature keys: ``*.bikey`` files in ``keys/`` of the site
  repository, in ``instances/<name>/keys/`` and at the top of the instance's
  overlays;
- ``runtime/serverDZ.cfg`` from the site repository's
  ``instances/<name>/serverDZ.cfg``;
- the BattlEye config with the RCon port and a password that dzo generates once
  and keeps below ``paths.secrets``.

In ``serverDZ.cfg`` dzo sets ``Missions/DayZ/template`` to the
instance's ``map`` and ``steamQueryPort`` to ``ports.query``; everything else stays
as you wrote it. A missing or unparsable ``serverDZ.cfg`` fails the render before
dzo touches the live mission, also with ``--dry-run``.

dzo fetches the pristine mission from ``mission_source`` the first time it is
missing, and never again on its own. A start does not need the network. ``--update-pristine`` fetches it again; the live
mission follows on that same render. dzo takes a base file that the map lacks
(for example ``cfgweather.xml``) from ``fallback_mission`` in the installed
server build, when a build is installed.

**The default source** is Bohemia Interactive's `Central Economy repository
<https://github.com/BohemiaInteractive/DayZ-Central-Economy>`_. An instance without
``mission_source`` uses the folder named like its ``map`` (``dayzOffline.chernarusplus``,
``dayzOffline.enoch``, ``dayzOffline.sakhal``) at ``master``, not the files that ship
inside the server build. ``ref`` and ``path`` alone change that default, for example
``mission_source: {ref: DZ_1.29}`` pins a tag. A map the repository does not have (a
modded map) needs a ``mission_source`` of its own (``git``/``ref``/``path``, or a
``preset``). ``dzo instance render <name> --update-pristine`` fetches the repository
again; the live mission follows, with the usual drift handling.

How dzo merges mod files
------------------------

``integrations/mods/<modid>/integration.yaml`` says which files a mod
contributes and where they come from:

.. code-block:: yaml

   mod: 2291785308                  # required: the workshop id
   name: DayZExpansionCore          # required: a label for reports
   aliases: [2291785437]            # default: none; further workshop ids that use this integration
   normalize: [xml-decl]            # default: none; eventposdef-root | wrap-root | xml-decl
   files:                           # default: none; the key is the file name
     types.xml:             {source: local, path: files/types.xml}
     cfgspawnabletypes.xml: {source: url, url: https://…, sha256: …}
     cfgeventspawns.xml:    {source: local, path: files/cfgeventspawns.xml,
                             maps: [dayzOffline.chernarusplus]}
     events.xml:            {source: mod, path: ./info/events.xml}
   hooks:
     post_merge: [hooks/remove-static-trains.sh]   # default: none

``mod`` and ``name`` are required; ``mod`` is the workshop id the integration is for, and
``aliases`` lists further workshop ids that use the same integration.

Each entry of ``files`` has a ``source``:

- ``local``: in the site repository, needs ``path``, relative to the
  integration's folder;
- ``url``: downloaded, cached, needs ``url``; ``sha256`` pins it by hash and dzo
  verifies it;
- ``mod``: a file shipped inside the mod, needs ``path``.

``maps`` restricts an entry to the listed maps (the ``map`` of the instance);
the same file name can then appear for several maps. ``normalize`` is explained
at the end of :ref:`the merge <merge-details>`. ``hooks.post_merge`` is a list
of scripts, see Hooks below. dzo never changes the downloaded mod itself.

An instance can replace an integration for itself with ``instances/<name>/integrations/<modid>/``
(same file, same keys). It takes the place of the shared one as a whole, key by
key, not merged.

A map preset, ``integrations/maps/<name>.yaml``, is a mission source an instance refers to
with ``mission_source: {preset: <name>}``. All three keys are required; ``ref`` and ``path``
of the instance's ``mission_source`` replace them:

.. code-block:: yaml

   git: https://<git host>/<owner>/DeerIsle-mission.git   # the repository
   ref: main                                              # a branch, tag or commit
   path: empty.deerisle                                   # the mission folder in the repository

Each file type has a fixed merge strategy:

.. list-table::
   :header-rows: 1

   * - Files
     - Strategy
   * - ``types``, ``spawnabletypes``, ``events``
     - copied to ``mod_<id>/`` and registered as a ``<ce folder>`` in
       ``cfgeconomycore.xml``
   * - ``mapgrouppos``, ``mapgroupproto``, ``cfgeventgroups``,
       ``cfgeventspawns``, ``cfgenvironment``, ``cfgrandompresets``,
       ``zombie_territories``, ``globals`` (into ``db/globals.xml``)
     - XML elements appended to the mission file
   * - ``cfggameplay.json``
     - deep merge; known lists (object spawners, spawn gear presets,
       restricted areas) are appended without duplicates
   * - ``cfgundergroundtriggers.json``, ``cfgeffectarea.json``
     - arrays concatenated
   * - ``cfgweather.xml``, ``messages.xml``
     - replaced
   * - ``init.c``
     - patched
   * - anything else
     - copied to ``custom_<name>/``

When two mods define the same thing, the one later in the ``mods`` list wins and
the render report lists the conflict.

Overlays
--------

An overlay is a folder of mission files: your own tweaks, loadouts, object
spawners. Shared overlays live in ``site/overlays/``, instance overlays in
``instances/<name>/overlays/``. They use the same merge strategies as mods.

EditorFiles
-----------

dzo copies the files in ``instances/<name>/EditorFiles/`` (``*.dze`` for the
`DayZ Editor Loader
<https://steamcommunity.com/sharedfiles/filedetails/?id=2276010135>`_
servermod) to ``EditorFiles/`` in the live mission on every render.
They are generated files: dzo owns them. If you remove a file from the site,
dzo removes it from the mission at the next render. A copy goes to the file
history first, and dzo removes a folder that ends up empty. dzo removes only
files it wrote; a file you put there yourself stays.

MapStreamDZE
------------

dzo mirrors the files in ``instances/<name>/MapStreamDZE/`` (``*.dze``,
subfolders allowed) to ``profiles/dzo/mapstream/DZE/`` of the instance, where
the `dzo_mapstream
<https://steamcommunity.com/sharedfiles/filedetails/?id=3812840035>`_ servermod reads them. It is not part of the mission: the sync
runs on every render (so at every start), after dzo applies the mission. dzo
owns that target folder: it writes files when they differ, and it removes files
and folders that are not in the site any more. The render prints the
number of files written and removed when it changed anything.

dzo does not touch an instance without a ``MapStreamDZE`` folder, so files the
mod wrote itself stay. Git does not keep an empty folder: to remove the last
file, delete it and keep a placeholder such as ``.gitkeep`` in the folder. dzo
copies the placeholder too, which is harmless. Symlinks and other non-regular files in the
folder fail the render. There is no ``config.yaml``, ``site.yaml`` or
``instance.yaml`` key for it.

An ``overlay.yaml`` can declare files that ``cfggameplay.json`` must reference.
dzo then adds the references with the right path:

.. code-block:: yaml

   object_spawners: ["*.json"]            # default: none; file name patterns, in the overlay
   spawn_gear_presets: ["loadout-*.json"] # default: none
   restricted_areas: ["areas-*.json"]     # default: none

Each key lists file name patterns of the overlay. dzo puts them into the list of
that name in ``cfggameplay.json``: ``object_spawners`` (``objectSpawnersArr``),
``spawn_gear_presets`` (``spawnGearPresetFiles``) and ``restricted_areas``
(``playerRestrictedAreaFiles``).

Hooks
-----

For anything the strategies cannot express, a hook runs a script. A hook is an
executable that gets a JSON context on stdin and these variables:
``DZO_INSTANCE``, ``DZO_HOOK_POINT``, ``DZO_MAP``,
``DZO_LIVE_MISSION`` and, for ``post_merge``, ``DZO_STAGING``. The path of the
executable is relative to ``instances/<name>/`` in the site repository, or to
the integration's folder for an integration's ``post_merge``. One script runs
for at most 10 minutes.

.. list-table::
   :header-rows: 1
   :widths: 18 50 32

   * - Hook
     - When
     - A failing script
   * - ``post_merge``
     - in the staging area: an integration's own ``hooks.post_merge`` right after its files
       were merged, the instance's ``hooks.post_merge`` after everything
     - fails the render, the live mission stays as it was
   * - ``post_render``
     - after the live mission was updated, at the end of ``dzo instance render``
     - fails the render (and so the start)
   * - ``pre_start``
     - before the server starts (``ExecStartPre``, after the render), against the live
       mission, for example to update trader stock
     - fails the start
   * - ``post_stop``
     - after the container is gone (``ExecStopPost``)
     - is printed, nothing else
   * - ``pre_update``
     - in a restart that deploys pending mod or build generations, before the snapshot
     - aborts the restart, the server starts again as it was
   * - ``post_update``
     - after those generations were deployed
     - is printed
   * - ``post_download``
     - after a new generation of a mod was installed, for every instance that uses it
     - is printed
   * - ``post_backup``
     - after a snapshot, see :doc:`backups`
     - is printed

``dzo instance hook <name> <point>`` runs the scripts of one point by hand.

.. _merge-details:

The merge itself
~~~~~~~~~~~~~~~~

The staging area starts as the pristine mission. The instance's ``messages.xml``
comes next; it is always empty (``dzo serve`` sends the messages, see
:doc:`configuration`). Then the mods come in list order, then the overlays in
list order. After the merge every file it wrote must parse, and every
``<ce folder>`` must exist with the files it names. Otherwise the render stops
before dzo touches the live mission. Notes on what is built:

* ``init.c`` of an integration is a unified diff; dzo applies it with ``patch``
  to the mission's ``init.c``. A diff that does not apply fails the render.
* dzo always writes ``messages.xml`` empty. It accepts the ``messages.xml`` of a
  mod integration or an overlay but replaces it with the empty one. ``dzo serve``
  sends the server messages itself.
* ``globals.xml`` is not a Central Economy file type, so dzo does not register
  it. dzo merges its ``<var>`` elements by ``name`` into ``db/globals.xml`` (the
  later contributor wins a variable).
* dzo reports two contributors that replace the same file (``cfgweather.xml``,
  ``messages.xml``) as a conflict; the later one wins. dzo silently replaces the
  elements of XML files merged by ``name``. In ``mapgrouppos.xml`` it identifies
  a group by ``name`` and ``pos``, because the file has many groups of one name
  (one per position).
* ``normalize:`` in an integration repairs the quirks of mod files before dzo
  merges them: ``eventposdef-root`` renames the root ``<events>`` of
  ``cfgeventspawns.xml`` to ``<eventposdef>``. ``wrap-root`` wraps a file that is
  a bare list of elements into the root element of its kind (the name of the
  file says which). ``xml-decl`` adds the XML declaration. A JSON file is left
  alone.
* dzo downloads a ``url`` source once and keeps it in ``paths.cache``, keyed by
  its ``sha256`` when it has one, else by its URL. It verifies the download when
  a ``sha256`` is present; otherwise it never fetches the file again.

Mission history and reset
-------------------------

* ``dzo mission rollback <name> [<time>]`` restores managed files from
  ``filehistory/``.
* ``dzo mission reinit <name>`` recreates the live mission from pristine. This is
  the only destructive mission command: it asks for confirmation and always
  takes a snapshot first.

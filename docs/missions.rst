.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Missions, integrations and overlays
===================================

The mission folder holds the Central Economy files and ``init.c``. dzo treats it
with care: **the live mission is never wiped or recreated**.

The game world (the persistence, ``storage_1``) is not in the mission folder.
dzo starts the server with ``-storage=/storage``, and ``/storage`` is
``<instances>/<name>/storage/<map>`` on the host. The server then writes
``storage_<instanceId>`` there and nothing into the mission, so the mission folder
only holds files dzo and the mods manage. Keeping the world per map means that
changing the map starts a fresh world instead of loading the old map's
persistence; the old one stays in its directory. Mods that use the ``$storage:``
placeholder get the same directory.

Pristine and live
-----------------

Each instance has two copies:

``servermpmissions/`` — the pristine mission
   A checkout of the instance's mission repository at the configured ``ref``.
   ``dzo mission update <name>`` fetches a new version. Nothing else changes it.

``mpmissions/<map>/`` — the live mission
   What the server runs. It is created once from the pristine mission
   (``dzo instance create`` or ``dzo mission init``) and then only updated in
   place.

Which files dzo manages
-----------------------

dzo keeps a manifest of the files it owns in the live mission:

Managed files
   Every file that comes from the pristine mission. dzo updates them on every
   render.

Generated files
   Folders dzo creates itself: ``mod_<id>/`` for mod CE data, ``custom_<name>/``
   for overlays, and EditorFiles.

Foreign files
   Everything else: ``storage_*``, files mods write at runtime, files you
   placed by hand. **dzo never changes or deletes them.** Paths listed in
   ``mission.unmanaged`` are treated as foreign even if the mission repository
   ships them.

If a managed file was changed in the live mission (by a mod or by hand), dzo
warns, copies the changed file to ``filehistory/`` and overwrites it
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

If validation fails, the start is aborted and the live mission is untouched.

Preview a render at any time:

.. code-block:: sh

   dzo instance render deerisle --dry-run

``dzo instance render <name>`` (without ``--dry-run``) is the same render, applied.
It is what the unit runs before every start. The first render into an empty
``mpmissions/<map>`` is the one-time initialisation from the pristine mission;
every later render only touches files dzo manages (in the manifest, or new in
the pristine mission). ``storage_*`` and everything matching
``mission.unmanaged`` is never written.

The render also prepares what the server needs besides the mission: the signature
keys (``runtime/keys``: the server build's and those of every client mod, copied
because the container cannot follow links into the cache), ``runtime/serverDZ.cfg``
from the site repository's ``instances/<name>/serverDZ.cfg``, and the BattlEye
config with the RCon port and a password that dzo generates once and keeps below
``paths.secrets``. In ``serverDZ.cfg`` dzo sets ``Missions/DayZ/template`` to the
instance's ``map`` and ``steamQueryPort`` to ``ports.query``; everything else stays
as you wrote it. A missing or unparsable ``serverDZ.cfg`` fails the render before
the live mission is touched, also with ``--dry-run``.

The pristine mission is fetched from ``mission_source`` the first time it is missing, and never again on its own, so a start
does not need the network. ``--update-pristine`` fetches it again; the live
mission follows on that same render. A base file the map lacks (for example
``cfgweather.xml``) is taken from ``fallback_mission`` in the installed server
build, when a build is installed.

**The default source** is Bohemia Interactive's `Central Economy repository
<https://github.com/BohemiaInteractive/DayZ-Central-Economy>`_: an instance without
``mission_source`` uses the folder named like its ``map`` (``dayzOffline.chernarusplus``,
``dayzOffline.enoch``, ``dayzOffline.sakhal``) at ``master``, not the files that ship
inside the server build. ``ref`` and ``path`` alone change that default, for example
``mission_source: {ref: DZ_1.29}`` pins a tag. A map the repository does not have (a
modded map) needs a ``mission_source`` of its own (``git``/``ref``/``path``, or a
``preset``). ``dzo instance render <name> --update-pristine`` fetches the repository
again; the live mission follows, with the usual drift handling.

How mod files are merged
------------------------

``integrations/mods/<modid>/integration.yaml`` says which files a mod
contributes and where they come from:

.. code-block:: yaml

   mod: 2291785308
   name: DayZExpansionCore
   files:
     types.xml:             {source: local, path: files/types.xml}
     cfgspawnabletypes.xml: {source: url, url: https://…, sha256: …}
     cfgeventspawns.xml:    {source: local, path: files/cfgeventspawns.xml,
                             maps: [dayzOffline.chernarusplus]}
     events.xml:            {source: mod, path: ./info/events.xml}
   hooks:
     post_merge: [hooks/remove-static-trains.sh]

``source`` is ``local`` (in the site repository), ``url`` (downloaded, cached,
optionally pinned by hash) or ``mod`` (a file shipped inside the mod). dzo never
changes the downloaded mod itself.

Each file type has a fixed merge strategy:

.. list-table::
   :header-rows: 1

   * - Files
     - Strategy
   * - ``types``, ``spawnabletypes``, ``events``, ``globals``
     - copied to ``mod_<id>/`` and registered as a ``<ce folder>`` in
       ``cfgeconomycore.xml``
   * - ``mapgrouppos``, ``mapgroupproto``, ``cfgeventgroups``,
       ``cfgeventspawns``, ``cfgenvironment``, ``cfgrandompresets``,
       ``zombie_territories``
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

An ``overlay.yaml`` can declare files that ``cfggameplay.json`` must reference.
dzo then adds the references with the right path:

.. code-block:: yaml

   object_spawners: ["*.json"]
   spawn_gear_presets: ["loadout-*.json"]

Hooks
-----

For anything the strategies cannot express, a hook runs a script. A hook is an
executable (a path relative to ``instances/<name>/`` in the site repository, or to the
integration's folder for an integration's ``post_merge``) that gets a JSON context on stdin
and these variables: ``DZO_INSTANCE``, ``DZO_HOOK_POINT``, ``DZO_MAP``,
``DZO_LIVE_MISSION`` and, for ``post_merge``, ``DZO_STAGING``. One script runs for at most
10 minutes.

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

The merge itself
~~~~~~~~~~~~~~~~

The staging area is the pristine mission, then the instance's ``messages.xml``, then the
mods in list order, then the overlays in list order. After the merge every file it wrote must
parse, and every ``<ce folder>`` must exist with the files it names, or the render stops before
the live mission is touched. Notes on what is built:

* ``init.c`` of an integration is a unified diff, applied with ``patch`` to the mission's
  ``init.c``; a diff that does not apply fails the render.
* ``globals.xml`` is not a Central Economy file type: it is copied into the mod's folder and not
  registered.
* Two contributors that replace the same file (``cfgweather.xml``, ``messages.xml``) are reported
  as a conflict; the later one wins. Elements of the XML files that are merged by ``name`` are
  replaced silently; in ``mapgrouppos.xml`` a group is identified by ``name`` and ``pos``, because the file
  has many groups of one name (one per position).
* ``normalize:`` in an integration repairs the quirks of mod files before they are merged:
  ``eventposdef-root`` renames the root ``<events>`` of ``cfgeventspawns.xml`` to ``<eventposdef>``,
  ``wrap-root`` wraps a file that is a bare list of elements into the root element of its kind (the
  name of the file says which), and ``xml-decl`` adds the XML declaration. A JSON file is left alone.
* A ``url`` source is downloaded once and kept in ``paths.cache``: by its ``sha256`` when it has
  one (and verified), else by its URL (and never fetched again).

Mission history and reset
-------------------------

* ``dzo mission rollback <name> [<time>]`` restores managed files from
  ``filehistory/``.
* ``dzo mission reinit <name>`` recreates the live mission from pristine. This is
  the only destructive mission command: it asks for confirmation and always
  takes a snapshot first.

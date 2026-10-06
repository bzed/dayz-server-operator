.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Coming from dayzdockerserver
============================

dzo replaces ``dayzdockerserver``. There is **no data migration**: servers
moving to dzo start fresh, with a new world, empty profiles and no player or ban
history. The old servers are only used as a source of configuration.

Convert the configuration
-------------------------

``dzo legacy convert-config`` reads the ``dayzdockerserver`` git branches (never
podman volumes or a running host) and writes an example site repository for you
to review:

.. code-block:: sh

   dzo legacy convert-config \
       --repo ~/src/dayzdockerserver \
       --ref chernarus --ref livonia --ref deerisle \
       --out ~/src/dayz-site \
       [--port-offset 100] [--image <runtime image>] [--dry-run]

What it converts:

.. list-table::
   :header-rows: 1

   * - Old
     - New
   * - ``files/serverDZ.cfg``
     - ``instances/<name>/serverDZ.cfg``; the join and admin passwords are replaced by
       ``CHANGE-ME`` (the old files kept them in git), and dzo sets ports, ``template`` and
       ``instanceId`` itself
   * - ``config/containers/server.json``
     - ``ports`` (kept, ``--port-offset`` is added), ``container.mounts``, ``env`` and
       ``logz_dir``; the network is ``host``
   * - ``parameters=`` of ``server/bin/dz``
     - ``params`` (``cpuCount``, ``limitFPS``, the other flags as ``extra``)
   * - ``template=`` and ``map.env``
     - ``map``; ``mission_source`` and ``fallback_mission`` for a map that is not one of the
       ``dayzOffline.<map>`` folders of Bohemia's repository
   * - ``files/mods/@<Name>`` links
     - a **candidate** ``mods`` list, every entry marked ``# review: active?``
   * - ``files/servermods``
     - ``server: true`` on the matching mods
   * - ``files/mods/<id>/`` (``xml.env``, ``map.env``, XML/JSON files,
       ``init.c`` patches, ``start.sh``)
     - ``integrations/mods/<id>/`` (identical files across branches become
       shared integrations, differing ones per-instance overrides)
   * - ``files/custom/<dir>``
     - overlays (shared if identical across branches)
   * - ``files/messages.xml``
     - ``instances/<name>/messages.xml``
   * - ``files/bin/pre_start.sh`` (trader stock, weather)
     - a ``pre_start`` hook, if it does anything (a script that is commented out is dropped)

Not converted: which mods were actually active, restart times and update checks
(they lived in the host's crontab), RCon passwords, Steam credentials, and any
game data. Instances get the site defaults with ``# review`` markers instead.

Known problems of the old scripts are fixed or reported: an ``INIT=local`` that asked for a file
that does not exist (only ``init.c.<map>`` patches did), ``xml.env`` entries without a file, files no
``xml.env`` line used, and ``start.sh`` files that ran inside the directory of the merged files
(they get ``cd "$DZO_STAGING"``). The integrations get ``normalize: [eventposdef-root, wrap-root,
xml-decl]``, which does what the old ``installxml`` did. ``CONVERSION_REPORT.md`` lists per instance what was converted, what
needs a decision, what was dropped and why, and where branches disagree.

The converter is idempotent: run it again and only changed files are rewritten; nothing is
deleted. ``--dry-run`` lists what would change. After writing it loads the site, so a result that
does not validate is an error.

Review, then roll out
---------------------

#. Go through ``CONVERSION_REPORT.md`` and all ``# review`` markers. Settle the
   mod list and the mod order (the order decides which mod wins when two mods
   change the same mission file).
#. ``dzo site validate``.
#. For each server: announce the wipe → ``dzo instance create`` → install the
   build and mods → test in parallel on other ports (``--port-offset``) →
   switch to the real ports and stop the old server → remove the old setup
   once the new one is stable.

The old volumes are not touched and not read.

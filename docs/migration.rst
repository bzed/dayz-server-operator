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
     - ``map``; ``mission_source`` for a map that is not one of the
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
     - not copied; set ``welcome:`` and ``messages:`` in ``instance.yaml`` (dzo sends them itself)
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

The web interface: logins and one installation
----------------------------------------------

This is for installations of dzo that already run ``dzo web``. Before, everybody who reached the web interface
was the same person, acting with one delegate token; ``web.user_header`` only told the audit log what a proxy
had written. Now people log in (:ref:`web-login`) and have a role per instance, and one web interface serves
one installation. After the upgrade:

1. **Remove the old keys.** ``dzo web`` and ``dzo serve`` refuse a ``config.yaml`` that still sets
   ``web.backends`` or ``web.user_header``. Several backends: run one web interface per installation, on the
   host of each installation or elsewhere with ``web.api`` (``url``, ``token_file``, ``ca_file`` for a private
   CA). The backend picker and sending a ban to several installations at once are gone.
2. **Set ``serve.web_url``** to the address people open the web interface at (``https``; plain ``http`` only
   for localhost). Without it logins are off and the web interface says so. Restart ``dzo serve``.
3. **Make a web token.** The old delegate token (``--delegate``) no longer gives the web interface anything.
   Create the new one on the installation: ``dzo token create web --role admin --web >
   <paths.secrets>/web.token`` (its role and instances cap what any person can do through it). Delegate tokens
   keep working for scripts and tools.
4. **Make the first admin.** ``dzo user grant <Steam64 ID> --role admin`` on the host of ``dzo serve`` (and
   more people, per instance, with ``--instance``). They open the web interface, sign in through Steam and set
   up a second factor. Everything else is done on the people page.
5. **Run ``dzo units sync``** (``dzo-web.service`` gained ``ExecReload``) and, for HTTPS, set ``serve.tls`` and
   ``web.tls`` or put a proxy in front (:ref:`web-https`; behind a proxy set ``web.trusted_proxies``).
6. Audit entries of web actions now name ``<token>:<Steam ID>`` and carry the browser's address for
   ``serve.audit_ip_retention_days`` (default 90).

Tokens of bots and scripts are unchanged, except that an admin token limited to some instances no longer reads
the audit log or revokes other people's bans: those permissions are installation-wide.

.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Configuration
=============

dzo has two kinds of configuration:

* the **operator configuration** in ``/etc/dzo/config.yaml``: paths, the site
  repository, products, notification targets, web and exporter settings;
* the **site repository**: everything about your servers, in git.

Secrets (Steam session, RCon passwords, webhook URLs, keys) are never in git.
They live in ``paths.secrets`` with mode 0600.

Operator configuration
----------------------

.. code-block:: yaml

   binary: /usr/bin/dzo       # what the units run and the containers mount

   paths:
     data: /var/lib/dzo
     instances: ${data}/instances
     snapshots: ${data}/snapshots
     logs: ${data}/logs         # archive of rotated profile logs
     cache: ${data}/cache
     secrets: ${data}/secrets
     db: ${data}/db
     site: ${data}/site

   site:
     url: git@<git host>:<you>/dayz-site.git
     branch: main
     commit: true
     push: false

   steam:
     account: <steam account name>  # used by steamcmd for downloads

   products:                       # built-in defaults, override only if needed
     dayz-stable:       {server_appid: 223350,  branch: public, workshop_appid: 221100}
     dayz-experimental: {server_appid: 1042420, branch: public, workshop_appid: 221100}

   notify:
     discord:
       default: {}                 # the webhook URL is kept in the secrets directory
       admins: {events: [drift, render_failed, job_failed, steam_auth]}

   serve:                          # `dzo serve`, see the API page
     installation: default         # name of this installation in the API
     listen: 127.0.0.1:8080        # /api/v1
     mod_listen: 127.0.0.1:2400    # where the dzo-admin mods connect
     allow_insecure_http: false    # plain HTTP on a non-local address

   web:                            # `dzo web`, see the web interface page
     listen: 127.0.0.1:8081
     user_header: X-Forwarded-User # set by your authenticating reverse proxy
     assets: /usr/share/javascript # htmx and Leaflet (Debian packages)
     docs_dir: /usr/share/doc/dzo/html
     backends:                     # default: this host's own API
       - {name: main, url: "http://127.0.0.1:8080", token_file: /var/lib/dzo/secrets/web.token}

   map_tiles:                      # `dzo map tiles build`, see the admin map page
     maps:
       enoch: {source: /srv/dayz/mapsources/worlds_enoch_data.pbo}

   exporter:
     listen: ":9464"
     tls: {cert_file: null, key_file: null}

   database:
     driver: sqlite                # or: postgres
     dsn: null

Site repository layout
----------------------

.. code-block:: text

   site/
     site.yaml                      defaults for all instances
     integrations/
       mods/<modid>/integration.yaml   how a mod's files go into the mission
       mods/<modid>/files/…            local integration files
       mods/<modid>/hooks/…            scripts for this mod
       maps/<name>.yaml                mission source presets
     overlays/<name>/…              reusable mission overlays
     instances/<name>/
       instance.yaml
       serverDZ.cfg
       messages.xml
       overlays/<name>/…            overlays for this instance only
       integrations/<modid>/…       per-instance override of an integration
       hooks/…

``dzo site pull`` updates the checkout. Uncommitted local edits block the pull
and are reported instead of being overwritten. ``dzo site validate`` checks all
files against their schemas.

instance.yaml
-------------

.. code-block:: yaml

   name: deerisle
   product: dayz-stable            # or dayz-experimental
   map: empty.deerisle             # mission folder; must match serverDZ.cfg template
   mission_source:
     git: https://<git host>/<owner>/DeerIsle-mission.git
     ref: main                     # updated only with: dzo mission update
     path: "empty.deerisle"
   fallback_mission: dayzOffline.chernarusplus
   mission:
     unmanaged: ["expansion/**"]   # never managed, even if the mission repo ships it
     drift: warn-backup-overwrite
   ports: {game: 8302, rcon: 8303, query: 8716}
   network: host                   # or: publish
   params: {cpuCount: auto, extra: ["-netlog", "-adminlog"]}
   mods:                           # order = merge precedence for mission files (later wins)
     - {id: 1559212036}                    # Community Framework
     - {id: 1828439124, server: true}      # loaded with -servermod
   overlays: [login-times, stamina]
   updates:
     policy: auto                  # auto | notify | manual (mods only)
     check_interval: 1h
     restart_announce: {minutes: 12, lock: 5, delay: 2, text: "MOD UPDATE!"}
   restarts:
     schedule: ["*-*-* 00/4:00"]   # systemd OnCalendar expressions
     announce: {minutes: 30, lock: 3, delay: 3}
   health: {startup_timeout: 45m, interval: 60s, retries: 5}
   restart_limit: {burst: 5, interval: 30min}
   backup:
     keep: 20
   notify: {discord: [default, admins]}
   container:
     env: {}
     mounts: ["/var/lib/GeoIP:/var/lib/GeoIP:ro"]
     memory: null                  # e.g. 24G
   hooks:
     pre_start: ["hooks/traderstocks.sh"]
   admin:                          # dzo-admin mod, see the admin map page
     deny_classes: ["Land_*"]      # items admins may not spawn (trailing * = prefix)
   admin_map:
     watch:
       - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: ufo}

Main keys:

``product``
   Which server build family the instance runs. The exact build is chosen with
   ``dzo instance upgrade`` (see :doc:`mods-and-updates`).

``map`` and ``mission_source``
   The mission folder name and where its pristine copy comes from. Without a
   ``mission_source``, a ``dayzOffline.<map>`` folder is taken from Bohemia's Central
   Economy repository at ``master``. See :doc:`missions`.

``ports``
   Game, RCon and Steam query port. dzo refuses overlapping ports between
   instances.

``network``
   ``host`` (default) uses the host's network directly. ``publish`` gives the
   container its own network namespace and publishes only the game ports.

``mods``
   Workshop ids. ``server: true`` loads the mod with ``-servermod``. The list
   order is also the order of the ``-mod=`` and ``-servermod=`` arguments, and
   decides which mod wins when two mods change the same mission file. dzo does
   not sort mods or check their dependencies: list a mod after the mods it needs.

``admin`` and ``admin_map``
   Settings of the :doc:`dzo-admin mod <admin-map>`, which an instance runs when
   ``{local: dzo-admin, server: true}`` is in ``mods``. ``admin`` has
   ``disabled``, the update intervals ``sync_ms`` (default 1000),
   ``players_s`` (5), ``vehicles_s`` (60), ``markers_s`` (10) and ``events_s``
   (30), ``allow_spawn`` (default true), and ``allow_classes`` /
   ``deny_classes`` for item spawns. ``admin_map.watch`` lists class watch rules.

``updates.policy``
   What happens when a mod update is found: ``auto`` restarts the server
   gracefully within the update windows, ``notify`` only reports, ``manual``
   only records it.

``stop``
   How the server is brought down, for ``dzo instance stop``, every restart (maintenance,
   updates, ``dzo restart``) and a reboot of the host. ``method: rcon`` (the default)
   sends ``#shutdown`` over RCon, waits up to ``timeout`` (default ``30s``) for the
   process to exit, and kills it when it does not; the restart that follows is
   immediate, players are not announced to (use ``dzo restart`` for a countdown).
   ``method: kill`` skips the request and kills the server at once. ``ignore_asserts``
   (default ``true``) starts the server with a stdin that answers "Ignore" to the
   ``(A)bort (R)etry (I)gnore`` prompt of an assertion: the experimental builds raise one
   at every shutdown ("Script is leaking!") and, with a stdin at end-of-file (any
   container), spin forever waiting for the answer. Set it to ``false`` to start the
   server unchanged. See :doc:`operations`.

``restarts.schedule``
   Maintenance restarts, as systemd calendar expressions.

``health``
   ``startup_timeout`` is how long a start may take (large mod lists load
   slowly). ``interval`` × ``retries`` is how long a hung server may stay hung
   before it is killed.

``restart_limit``
   Crash-loop brake: after ``burst`` starts within ``interval`` the server stays
   stopped and you are alerted.

serverDZ.cfg
------------

Keep your normal ``serverDZ.cfg`` in the instance directory. dzo enforces the
keys it owns (ports, ``template``, ``instanceId``, ``steamQueryPort``) and shows
a diff when your file and the enforced values differ (``dzo config diff``).

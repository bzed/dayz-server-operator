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
     enabled: null                 # unit: null = when an instance runs dzo-admin, true or false to force
     installation: default         # name of this installation in the API
     listen: 127.0.0.1:8080        # /api/v1
     mod_listen: 127.0.0.1:2400    # where the dzo-admin mods connect
     allow_insecure_http: false    # plain HTTP on a non-local address

   web:                            # `dzo web`, see the web interface page
     enabled: null                 # unit: null = once backends or web.token exist, true or false to force
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

   database:                       # planned: not read yet, the player database is not built
     driver: sqlite                # or: postgres
     dsn: null

Defaults for all instances (the ``defaults:`` block) are not set here: they are
in ``site.yaml`` in the site repository, see :ref:`site-yaml`.

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

.. _site-yaml:

site.yaml
---------

``site.yaml`` in the root of the site repository is optional. Its ``defaults:``
block pre-fills settings for every instance: any setting an instance leaves
unset in its ``instance.yaml`` is taken from here, and a setting the instance
does set replaces it (a nested block is filled key by key). Keys set in neither
place get dzo's built-in values.

.. code-block:: yaml

   image: localhost/dzo-runtime:latest   # the runtime container image
   defaults:
     restarts:
       schedule: ["04:00", "16:00"]
     updates:
       policy: auto
     health: {startup_timeout: 45m}
     backup:
       keep: 20
       max_age: 30d
     notify: {discord: [default]}
   local_mods:                           # where servermods that are not in the site repo come from
     mymod: {url: https://example.org/mymod-1.0.zip, sha256: <sha256>}

The settings that can be defaulted are ``rcon_bind``, ``admin_map.watch`` (the watch rules are merged
with the instance's by layer name, the instance wins, see :doc:`admin-map`) and the blocks ``params``, ``updates``, ``restarts``,
``health``, ``restart_limit``, ``stop``, ``notify``, ``container``, ``backup``
and ``logs``, with the same keys as in ``instance.yaml`` below. Use
``dzo instance show <name>`` to see the result for an instance (see
:doc:`resolved-instance`). Commit the change and run ``dzo site pull`` on the host.

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
   rcon_bind: 127.0.0.1            # default; 0.0.0.0 opens RCon to the network, see below
   network: host                   # or: publish
   params: {cpuCount: auto, extra: ["-netlog", "-adminlog"]}
   mods:                           # order = merge precedence for mission files (later wins)
     - {id: 1559212036}                    # Community Framework
     - {id: 1828439124, server: true}      # loaded with -servermod
   overlays: [login-times, stamina]
   updates:
     policy: auto                  # auto | notify | manual (mods only)
     check_interval: 1h
     restart_announce: {minutes: 12, lock: 5, kick: 2, text: "MOD UPDATE! Restart"}
   restarts:
     schedule: ["04:00", "Sat 16:00"]   # restart times; Mon,Thu 04:00 for some weekdays
     announce: {minutes: 30, lock: 7, kick: 3}
   welcome: "Welcome to #name!"    # the default; "" for no welcome message
   messages:                       # more server messages, written to db/messages.xml
     - {text: "Homepage: example.org, rules: example.org/rules", on_connect: true, delay: 1}
     - {text: "Please read the rules: example.org/rules", repeat: 45}
   priority:                       # Steam64 IDs that skip the login queue (priority.txt)
     - "76561198108512982"
   health: {startup_timeout: 45m, interval: 60s, retries: 5}
   restart_limit: {burst: 5, interval: 30min}
   backup:
     keep: 20
     schedule: ["00:00", "12:00"]  # periodic snapshots, same format as restarts.schedule
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
       - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: rocket, color: "#8e44ad"}

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

``rcon_bind``
   The address BattlEye's RCon (UDP, the ``ports.rcon`` port) is reachable on. The default is
   ``127.0.0.1``: only the host itself, which is all dzo needs (its own RCon client, the health
   checks and ``dzo rcon`` connect there), so the password never has to protect a port on the
   internet. Many hosts want external RCon tools (BERCon, web panels) to connect. Then set an
   address of the host to bind to it only, or ``0.0.0.0`` (``::`` for IPv6) for every interface;
   it can be set once under ``defaults:`` in ``site.yaml`` for all instances. It applies with
   both ``network`` modes (with ``publish`` it is the host address the RCon port is published on),
   and takes effect at the next start. dzo connects to the address you bound, or to ``127.0.0.1``
   for ``0.0.0.0``. The RCon password is generated per instance and protects the port, but
   BattlEye RCon has no encryption and no rate limit beyond its own: allow the port only from the
   addresses that need it in your firewall.

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

``welcome``
   The message every player gets when joining. The default is ``Welcome to #name!``; DayZ
   replaces ``#name`` with the server name (``hostname`` in ``serverDZ.cfg``). Set your own text
   (``#name`` and ``#port`` work in it, the usual 160 characters apply), or ``""`` for none. Can
   be set under ``defaults:`` in ``site.yaml``.

``messages``
   Further messages the server sends on its own, from the mission's ``db/messages.xml``. A
   message has ``text`` (up to 160 characters), ``on_connect: true`` to send it once to a player
   who joins (``delay`` minutes later, default immediately) and/or ``repeat`` to send it to
   everybody every that many minutes. All times are minutes. Good for the homepage and the rules.
   It can be set under ``defaults:`` in ``site.yaml``; an instance's own list replaces it.

   dzo always manages this file: the welcome message and ``messages:`` are written to it, and
   with ``welcome: ""`` and no ``messages:`` it is empty, which disables server messages. The
   example messages of the pristine mission are never used. Instead of ``messages:`` you can put
   your own ``instances/<name>/messages.xml`` in the site repository (not together with
   ``messages:``); it is used as it is, without the welcome message. Neither a ``deadline``
   (countdown), ``shutdown`` nor ``#tmin`` is accepted, in ``messages:``, in that file, or in a mod
   integration's or an overlay's ``messages.xml``: restarts and stops are announced by dzo itself, see
   ``restarts.announce`` and :doc:`operations`. The server reads the file at start, so changes
   take effect at the next restart.

``priority``
   Steam64 IDs (17 digits) of players who skip the login queue when the server is full. dzo writes
   them to ``priority.txt`` in the server root (``/dayz`` in the container) as ``id1;id2;``, and
   the server reads it at start. Per instance, in ``instance.yaml``.

``updates.restart_minute``
   With ``policy: auto``, the minute of the hour at which update restarts happen: ``0`` is the
   full hour, ``30`` is XX:30. The update check then starts the announcement
   ``restart_announce.minutes`` earlier (with 30 minutes, ``0`` is started at XX:30 and ``30`` at
   XX:00), so the server restarts exactly at the slot; ``dzo instance apply`` adds those minutes to
   ``dzo-update-check.timer``. A check that runs more than 10 minutes after the start of the slot
   waits for the next hour. Without it an update restart starts at the first check that finds the
   update. Together with ``min_restart_interval: 1h`` a server is restarted for updates at most once
   an hour.

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
   Maintenance restarts: the times of day as ``HH:MM`` or ``Mon,Thu HH:MM``, see :doc:`operations`.

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

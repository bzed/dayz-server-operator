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

``/etc/dzo/config.yaml`` is optional: every key has a built-in default, and a file
only has to contain what you change. The example lists **every** key with its
built-in value (``# default`` marks a value that is the default, other values are
examples). Keys that are not set in the file keep the default; a key that is set
replaces it. For the maps (``products``) the entries of the file are added to the
built-in ones, and an entry of the same name replaces the built-in entry as a whole.

.. code-block:: yaml

   binary: /usr/bin/dzo              # default

   paths:
     data: /var/lib/dzo              # default
     instances: ${data}/instances    # default
     snapshots: ${data}/snapshots    # default
     logs: ${data}/logs              # default
     cache: ${data}/cache            # default
     secrets: ${data}/secrets        # default
     db: ${data}/db                  # default
     site: ${data}/site              # default

   site:
     remote: ssh://git@<git host>/<you>/dayz-site.git   # default: empty (not configured)
     branch: main                                 # default: empty, required with remote
     deploy_key_path: /var/lib/dzo/secrets/site_ed25519   # default: empty (ssh's own keys)
     auto_push: false                             # default

   steam:
     account: <steam account name>   # default: empty
     profile_lookup: true            # default
     web_api_key_file: /var/lib/dzo/secrets/steam-web-api.key   # default: none (no Web API key)

   products:                         # the two entries below are built in
     dayz-stable:
       app_id: 223350
       workshop_app_id: 221100
       beta_branch: ""               # default: none (the public branch)
     dayz-experimental:
       app_id: 1042420
       workshop_app_id: 221100
       beta_branch: ""

   notify:
     discord:                        # default: none
       - {name: default, url: "https://discord.com/api/webhooks/<id>/<token>"}
       - {name: admins,  url: "https://discord.com/api/webhooks/<id>/<token>"}

   serve:                            # `dzo serve`, see the API page
     enabled: null                   # default: unset (see below)
     installation: default           # default
     listen: 127.0.0.1:8080          # default
     mod_listen: 127.0.0.1:2400      # default
     allow_insecure_http: false      # default
     ip_retention_days: 90           # default (unset = 90)
     vacuum_days: 7                  # default (unset = 7)
     session_retention_days: 365     # default (unset = 365)
     geoip_dir: /var/lib/GeoIP       # default
     geoip_legacy_dir: /usr/share/GeoIP   # default
     tls:                            # default: none (plain HTTP on listen)
       cert_file: /etc/dzo/tls/serve-cert.pem   # default: empty
       key_file: /etc/dzo/tls/serve-key.pem     # default: empty
       client_ca_file: /etc/dzo/tls/ca.pem      # default: empty (no client certificates)
     web_url: https://dzo.example.invalid       # default: none (user logins are off)
     login:
       session_idle_hours: 12        # default (unset = 12)
       session_max_days: 7           # default (unset = 7)

   web:                              # `dzo web`, see the web interface page
     enabled: null                   # default: unset (see below)
     listen: 127.0.0.1:8081          # default
     allow_insecure_http: false      # default
     assets: /usr/share/javascript   # default
     docs_dir: /usr/share/doc/dzo/html   # default
     flags_dir: /usr/share/iso-flags-svg/country-4x3   # default
     tls:                            # default: none (plain HTTP on listen)
       cert_file: /etc/dzo/tls/web-cert.pem     # default: empty
       key_file: /etc/dzo/tls/web-key.pem       # default: empty
       client_ca_file: /etc/dzo/tls/ca.pem      # default: empty (no client certificates)
     api:                            # default: none (this host's API, see below)
       url: https://dzo.example.invalid:8080    # default: none
       token_file: /var/lib/dzo/secrets/web.token   # default: none
       ca_file: /etc/dzo/tls/serve-ca.pem       # default: none (the system's CAs)
     trusted_proxies: ["192.0.2.10", "198.51.100.0/24"]   # default: none

   map_tiles:                        # `dzo map tiles build`, see the admin map page
     maps:                           # default: none
       enoch: {source: /srv/dayz/mapsources/worlds_enoch_data.pbo}

   exporter:                         # `dzo exporter`, see the monitoring page
     listen: ":9464"                 # default
     tls:
       cert_file: /etc/dzo/tls/cert.pem    # default: empty (plain HTTP)
       key_file: /etc/dzo/tls/key.pem      # default: empty
       client_ca_file: /etc/dzo/tls/ca.pem # default: empty (no client certificates)
     allow: ["192.0.2.0/24"]         # default: none (every address may connect)
     bearer_token_file: /var/lib/dzo/secrets/exporter.token   # default: empty (no token)

The settings of ``serve``, ``web`` and ``exporter`` are read when the service starts;
the others when a command runs. ``dzo setup`` and ``dzo site validate`` check the file
and print what is wrong. A key dzo does not know is ignored.

``binary``
   The dzo executable the units run and mount into the containers. Must be an absolute path.
   A host that deploys by hand points it at a directory it can write, so a new build is moved
   into place while the old one runs.

``paths``
   Every directory dzo manages; all must be absolute. ``data`` is the root. The other paths
   may contain ``${data}``, which is replaced by the value of ``paths.data``, so setting only
   ``data`` moves everything. ``instances`` and ``snapshots`` must be on the same btrfs
   filesystem (see :doc:`installation`). ``logs`` is the archive of rotated profile logs,
   ``cache`` holds downloaded server builds and mods, ``secrets`` holds the Steam session,
   RCon passwords and tokens (mode 0600), ``db`` the databases (bans, player sightings),
   ``site`` the checkout of the site repository.

``site``
   The site repository, see below.

   ``remote``
      The git URL (``ssh://git@host/path.git``, ``https://...``) or a local path of the site repository. The scp-like form ``git@host:path`` is refused by the URL check; write it as ``ssh://git@host/path``. Empty means "not configured yet":
      ``dzo setup`` and the diagnostic commands still run without it.
   ``branch``
      The branch to check out. Required when ``remote`` is set.
   ``deploy_key_path``
      An SSH private key (normally below ``paths.secrets``) used to fetch and push the
      site repository. Empty uses ssh's own keys and agent.
   ``auto_push``
      ``true`` pushes the commits dzo makes itself (``dzo mod add``, edits in the web
      interface) to ``remote`` at once; ``false`` only commits them in the checkout.

``steam.account``
   The Steam account steamcmd logs in with for downloads. The session itself is created with
   ``dzo steam login`` and kept in the steamcmd home; the password is never in this file.

``steam.profile_lookup``
   Whether ``dzo serve`` asks Steam about the players in the player history (:ref:`player-history`): their Steam
   name, whether the profile is private, VAC and community bans and account facts, shown on the player page. ``true``
   (the default) or ``false``. Without a key (see below) dzo reads the public community profile page
   (``https://steamcommunity.com/profiles/<id>?xml=1``), at most one request every 2 seconds and about one per
   player per day, for players seen in the last 30 days. That page is not an official API and may change; when it
   cannot be read the player page shows the profile link only. ``false`` sends nothing to Steam; the profile
   link is still shown, it is only a link. Takes effect when ``dzo serve`` starts.

``steam.web_api_key_file``
   The path of a file with a Steam Web API key (32 hex digits; get one at https://steamcommunity.com/dev/apikey),
   absolute, readable by ``dzo serve`` only (mode 0600; a group- or world-readable file stops ``dzo serve`` with an
   error that names the file). Unset: no key. With a key the Web API replaces the community page and adds the number
   of VAC and game bans, the days since the last ban, community and economy bans (also for private profiles), the
   account's age and country, the Steam level, the DayZ playtime and the player's public friends who also played on
   your servers. The key is sent to ``api.steampowered.com`` only, never written to a log or shown in an error, and
   the file must stay out of version control (the repository's ``.gitignore`` lists ``*.key`` and ``secrets/``).
   Needs ``profile_lookup: true``. Takes effect when ``dzo serve`` starts.
   Required for every download (server builds and mods); empty makes those commands fail.

``products``
   The server builds an instance can run, by name (the ``product:`` of ``instance.yaml``).
   ``dayz-stable`` and ``dayz-experimental`` are built in; add an entry for another
   product, or give one of the two an entry of the same name to replace it. At least one
   product is needed.

   ``app_id``
      The Steam app id of the dedicated server. Required.
   ``workshop_app_id``
      The Steam app id the workshop mods are downloaded through (221100, the game).
      Required.
   ``beta_branch``
      A steamcmd beta branch for builds that are distributed as a beta of the same app
      id. Empty uses the default branch.

``notify.discord``
   The Discord webhooks, each with a ``name`` (unique) and the ``url`` of the webhook.
   The URL is a secret: restrict the permissions of ``config.yaml``. An instance selects
   webhooks by name with its ``notify.discord``; without a selection it uses the webhook
   named ``default``. See :doc:`monitoring`.

``serve``
   ``dzo serve``: the JSON API (:doc:`api`) and the endpoint the dzo-admin mods call.

   ``enabled``
      Whether ``dzo units sync`` runs ``dzo serve`` as a unit. Unset: when an instance runs the
      dzo-admin mod, which needs the endpoint. ``true`` and ``false`` force it.
   ``installation``
      The name of this installation in the API and in the web interface.
   ``listen``
      The address of ``/api/v1``. Plain HTTP is only accepted on a loopback address unless
      ``allow_insecure_http`` is set; put a reverse proxy with HTTPS in front otherwise.
   ``mod_listen``
      The address of ``/mod/v1/sync``, where the game servers' dzo-admin mods connect. The same
      loopback rule applies.
   ``allow_insecure_http``
      Allows plain HTTP on ``listen`` and ``mod_listen`` on a non-loopback address. It is not needed
      for ``listen`` when ``tls`` is set.
   ``tls``
      ``cert_file`` and ``key_file`` go together and switch the API listener (``listen``) to HTTPS,
      with TLS 1.2 or later. ``client_ca_file`` (needs the other two) makes client certificates
      mandatory (mutual TLS), for example so that only the web host can reach the API. ``mod_listen``
      stays plain HTTP: the game servers on this host call it. A non-loopback ``listen`` is accepted
      without ``allow_insecure_http`` when ``tls`` is set. The files are checked on new connections
      (every few seconds at most) and on ``systemctl reload``: a renewed certificate or CA takes
      effect without a restart and without dropping connections, and a replacement that does not load
      is logged and the old one stays active. Everything else in this block is read at start. Giving
      only one of the two files, or ``client_ca_file`` alone, is refused when the configuration loads.
   ``web_url``
      The address people open the web interface at, as scheme and host (and port) only, for example
      ``https://dzo.example.invalid``. User logins (:doc:`web`) need it: it fixes the return address
      of the Steam sign-in and the relying party of passkeys. Unset: logins are off and the web
      interface says so. It must be ``https``; plain ``http`` is only accepted for ``localhost``,
      ``127.0.0.1`` and ``[::1]``. A path, query, fragment or user name is refused. ``dzo web`` learns
      the address from the API, so it is set once, on the host of ``dzo serve``. Changing it takes
      effect when ``dzo serve`` starts, and passkeys registered under the old name stop working.
   ``login``
      ``session_idle_hours`` (unset: 12) is how long a web session may sit unused before it ends,
      ``session_max_days`` (unset: 7) how long it lives at most however busy the person is. Both are
      whole numbers and must be at least 1; ``0`` and negative values are refused when the
      configuration loads. A change affects the sessions that exist as soon as ``dzo serve``
      is restarted.
   ``ip_retention_days``
      How many days the address of a player sighting is kept, unless a ban uses it. Unset: 90.
      ``0``: for ever. Negative values are refused.
   ``vacuum_days``
      How often, in days, the ban database file is rebuilt. Unset: 7. ``0``: never.
   ``session_retention_days``
      How many days the sessions of the player history (who was on which instance, from when to when) are
      kept. Unset: 365. ``0``: for ever. Negative values are refused. Only the sessions go: the players, the
      names they used and the times they were first and last seen stay (:ref:`player-history`). ``dzo serve``
      applies it every minute with the other upkeep; a change takes effect when ``dzo serve`` starts.
   ``geoip_dir``, ``geoip_legacy_dir``
      Where the GeoLite2 databases (``GeoLite2-City``, ``-Country``, ``-ASN``, ``.mmdb``) and
      the legacy ``GeoIP.dat`` / ``GeoIPv6.dat`` (country only, the fallback) are looked up,
      see :doc:`geoip`.

``web``
   ``dzo web``, the web interface (:doc:`web`). One web interface serves one installation and reaches
   it only through its API.

   ``enabled``
      Whether ``dzo units sync`` runs ``dzo web`` as a unit. Unset: when the web interface is set
      up, that is when ``api`` is set or ``${paths.secrets}/web.token`` exists.
   ``listen``, ``allow_insecure_http``
      As for ``serve``.
   ``tls``
      As ``serve.tls``, for the web listener: ``cert_file`` and ``key_file`` switch it to HTTPS and
      allow a non-loopback ``listen`` without ``allow_insecure_http``; ``client_ca_file`` requires
      client certificates. The files reload as for ``serve.tls``.
   ``api``
      Where the installation's API is, when it is not this host's. ``url`` (``https``, or ``http`` for
      a loopback address) and ``token_file`` (an absolute path; a ``web`` token created on that
      installation) go together: giving one without the other is refused, and so is ``ca_file``
      without them. ``ca_file`` is a PEM file with the CA that signed the API's certificate, for a
      private CA; unset, the system's CAs are used. It reloads when it changes, like the other TLS
      files. Without ``api`` the web interface uses this host's API (``serve.listen``) with
      ``${paths.secrets}/web.token``. Read at start.
   ``trusted_proxies``
      Reverse proxies, as addresses or CIDR networks, whose ``X-Forwarded-For`` header is believed
      when working out the browser's address for the login log and the audit log. Default: none, then
      the address of the connection is used and forwarding headers are ignored. A value that is
      neither an address nor a CIDR network is refused, naming it. Read at start.
   ``assets``
      The directory with htmx and Leaflet (Debian's ``libjs-*`` packages).
   ``docs_dir``
      The built documentation, served under ``/docs/``.
   ``flags_dir``
      The country flags as ``<code>.svg`` in the 4:3 shape (Debian's ``iso-flags-svg``); without
      them the player list shows the country code.
   ``backends``, ``user_header``
      Removed. One web interface serves one installation (use ``api``), and ``dzo web`` has its own
      login. A configuration that still sets either key is refused with a pointer to the migration
      notes (:doc:`migration`).

``map_tiles.maps``
   The data PBO each map's tiles are built from, by the name of the mission's world
   (``chernarusplus``, ``enoch``, ``sakhal``, ``deerisle``): ``source`` is the DayZ client's
   ``worlds_<map>_data.pbo``, or a modded map's ``data.pbo``. The dedicated server's copy is not
   enough. See :doc:`admin-map`.

``exporter``
   ``dzo exporter``: ``/metrics`` for Prometheus and ``/status`` for Icinga (:doc:`monitoring`).

   ``listen``
      The address to listen on. Plain HTTP unless ``tls`` is set.
   ``tls``
      ``cert_file`` and ``key_file`` go together and switch on HTTPS; they are reloaded when
      they change. ``client_ca_file`` (needs the other two) makes client certificates
      mandatory (mutual TLS); it is reloaded the same way, so a replaced CA file takes effect
      without a restart. A replacement that does not load is logged and the old one stays active.
   ``allow``
      The networks (CIDR) that may connect. Empty allows every address.
   ``bearer_token_file``
      If set, every request needs this token as ``Authorization: Bearer``.

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
       EditorFiles/…                .dze files for the mission's EditorFiles/ (see missions)
       MapStreamDZE/…               .dze files mirrored to profiles/dzo/mapstream/DZE (dzo_mapstream)
       hooks/…

``dzo site pull`` updates the checkout. Uncommitted local edits block the pull
and are reported instead of being overwritten. ``dzo site validate`` checks all
files against their schemas.

.. _site-yaml:

site.yaml
---------

``site.yaml`` in the root of the site repository is optional. Its ``defaults:``
block pre-fills settings for every instance. How it combines with an instance:

* a setting the instance leaves unset is taken from ``defaults:``;
* a setting the instance does set replaces the default. A nested block is filled
  key by key, a list or a map is replaced as a whole (``[]`` clears it);
* exceptions: ``logs.rotate`` is added to the instance's rules, and
  ``admin_map.watch`` rules are merged by layer name (the instance's rule replaces the
  default rule of the same layer as a whole, see :doc:`admin-map`);
* keys set in neither place get dzo's built-in values.

A plain ``false``, ``0`` or empty string counts as "unset" for a setting that is not a
pointer: an instance cannot switch a default ``true`` off by writing ``false`` (this
affects ``container.core_dumps`` and ``updates.apply_with_scheduled_restart``). The
settings that take an explicit value for that are ``stop.ignore_asserts``, ``welcome``
(``""``), ``updates.restart_minute`` and ``admin_map.vanilla_events``.

The example lists **every** key. ``# default`` marks the built-in value, other values
are examples; the keys under ``defaults:`` are those of ``instance.yaml`` and are
explained there, see :ref:`instance-yaml`.

.. code-block:: yaml

   image: localhost/dzo-runtime:latest   # default: the runtime container image

   defaults:                             # everything below is optional
     rcon_bind: 127.0.0.1                # default
     params: {cpuCount: auto, limitFPS: 60, extra: ["-netlog"]}
     updates:
       policy: auto                      # default
       check_interval: 1h                # default
       window: ["06:00-10:00"]
       quiet_hours: ["18:00-24:00"]
       min_restart_interval: 4h
       batch_delay: 20m
       apply_with_scheduled_restart: true
       max_delay: 12h
       restart_minute: 0
       restart_announce: {minutes: 30, lock: 7, kick: 3, text: "MOD UPDATE! Restart", ways: []}
     restarts:
       schedule: ["04:00", "16:00"]
       announce: {minutes: 30, lock: 7, kick: 3, text: "Restart", ways: []}
     health: {startup_timeout: 45m, interval: 60s, retries: 5}   # default
     restart_limit: {burst: 5, interval: 30m, cooldown: 1h}      # burst and interval: default
     stop: {method: rcon, timeout: 30s, ignore_asserts: true}    # default
     notify: {discord: [default]}
     container:
       env: {TZ: Europe/Berlin}
       mounts: ["/var/lib/GeoIP:/var/lib/GeoIP:ro"]
       logz_dir: /srv/logz
       cpus: "4"
       memory: 24G
       core_dumps: false                 # default
     backup:
       before: [update, mod_update, mission_update, config_change]   # default
       schedule: ["00:00", "12:00"]
       keep: 20                          # default
       keep_by_reason: {mod_update: 8}
       max_age: 30d
       min_keep: 3                       # default
       min_free_bytes: 50GiB
     logs:
       rotate: ['^RFMosquito_.*\.log$', {match: '\.mdmp$', max_age: 14d, keep_newest: 0}]
       keep_newest: 1
       min_age: 10m                      # default
       archive: {compress: gzip, max_age: 90d, max_size: 50GiB}
     admin_map:
       vanilla_events: true              # default
       watch:
         - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: rocket, color: "#8e44ad",
            visibility: moderator, label: "UFO", max: 50, cluster: 25}
     welcome: "Welcome to #name!"        # default
     messages:
       - {text: "Rules: example.org/rules", on_connect: true, delay: 1, repeat: 45, color: green}

   local_mods:                           # where servermods that are not in the site repo come from
     mymod: {url: https://example.org/mymod-1.0.zip, sha256: <sha256>}
     other: {path: /srv/build/@Other}

``image``
   The container image the instances run (built by ``dzo setup``). Default
   ``localhost/dzo-runtime:latest``.

``defaults``
   The settings that can be defaulted are ``rcon_bind``, ``params``, ``updates``,
   ``restarts``, ``health``, ``restart_limit``, ``stop``, ``notify``, ``container``,
   ``backup``, ``logs``, ``admin_map``, ``welcome`` and ``messages``, with the same
   keys as in ``instance.yaml`` below. The identity of an instance (``name``, ``product``,
   ``map``, ``mission_source``, ``fallback_mission``, ``mission``, ``ports``, ``network``,
   ``mods``, ``overlays``, ``hooks``, ``admin``, ``priority``) cannot be defaulted.
   Use ``dzo instance show <name>`` to see the result for an instance (see
   :doc:`resolved-instance`).

``local_mods``
   Where a local servermod (``{local: <name>, server: true}`` in ``mods``) is imported from.
   Each entry has exactly one of ``url`` (a release archive, which needs ``sha256``) or
   ``path`` (an absolute directory on the host). A local mod without an entry is read from
   ``localmods/<name>/`` in the site repository. See :doc:`mods-and-updates`.

Commit the change and run ``dzo site pull`` on the host.

.. _instance-yaml:

instance.yaml
-------------

One ``instances/<name>/instance.yaml`` per server. Unknown keys are errors. The example
lists **every** key; ``# default`` marks the built-in value, other values are examples.
Where a key can also be set under ``defaults:`` in ``site.yaml``, the instance's value
replaces the site's (see :ref:`site-yaml` for the rules), which is said in the notes
below as "site default".

.. code-block:: yaml

   name: deerisle                  # required; must be the name of the directory
   product: dayz-stable            # required; a product of config.yaml: dayz-stable, dayz-experimental, ...
   map: empty.deerisle             # required; mission folder; must match serverDZ.cfg template
   mission_source:                 # default: the Central Economy repository, see below
     git: https://<git host>/<owner>/DeerIsle-mission.git   # or: preset: <name>, not both
     preset: null
     ref: main                     # updated only with: dzo mission update
     path: "empty.deerisle"
   fallback_mission: dayzOffline.chernarusplus   # default: none
   mission:
     unmanaged: ["expansion/**"]   # default: none; never managed, even if the mission repo ships it
     drift: warn-backup-overwrite  # default
   ports: {game: 8302, rcon: 8303, query: 8716}   # game is required
   rcon_bind: 127.0.0.1            # default; 0.0.0.0 opens RCon to the network, see below
   network: host                   # required: host or publish
   params:
     cpuCount: auto                # default: unset (no -cpuCount)
     limitFPS: 60                  # default: unset (no -limitFPS)
     extra: ["-netlog", "-adminlog"]   # default: none
   mods:                           # default: none; order = merge precedence for mission files (later wins)
     - {id: 1559212036}                    # Community Framework
     - {id: 1828439124, server: true}      # loaded with -servermod
     - {local: dzo-admin, server: true}    # a local servermod
     - {local: my-client-mod, debug_client: true}   # debugging only
   overlays: [login-times, stamina]   # default: none
   updates:
     policy: auto                  # default; auto | notify | manual (mods only)
     check_interval: 1h            # default
     window: ["06:00-10:00", "14:00-16:00"]   # default: none (any time)
     quiet_hours: ["18:00-24:00"]  # default: none
     min_restart_interval: 4h      # default: none
     batch_delay: 20m              # default: none
     apply_with_scheduled_restart: true   # default: false
     max_delay: 12h                # default: none
     restart_minute: 0             # default: unset
     restart_announce:             # default: none (no countdown fields set)
       minutes: 12
       lock: 5
       kick: 2
       text: "MOD UPDATE! Restart" # default: none
       ways:                       # default: see the operations page
         - {within: 10m, way: notification, every: 1m, now: 10s}
   restarts:
     schedule: ["04:00", "Sat 16:00"]   # default: none (no maintenance restart)
     announce:                     # how it reaches the players, see operations
       minutes: 30
       lock: 7
       kick: 3
       text: "Restart"
       ways:                       # default: see the operations page
         - {way: rcon, at: [30m, 20m, 15m]}
         - {within: 10m, way: expansion, type: baguette, every: 1m, now: 10s}
   welcome: "Welcome to #name!"    # default; "" for no welcome message
   messages:                       # default: none; more server messages, sent by dzo serve
     - {text: "Homepage: example.org, rules: example.org/rules", on_connect: true, delay: 1}
     - {text: "Please read the rules: example.org/rules", repeat: 45, color: green}
   priority:                       # default: none; Steam64 IDs that skip the login queue (priority.txt)
     - "76561198108512982"
   health:
     startup_timeout: 45m          # default
     interval: 60s                 # default
     retries: 5                    # default
   restart_limit:
     burst: 5                      # default
     interval: 30m                 # default
     cooldown: 1h                  # default: none
   stop:
     method: rcon                  # default; rcon | kill
     timeout: 30s                  # default
     ignore_asserts: true          # default
   notify:
     discord: [default, admins]    # default: [default]; [] sends nothing
   killfeed:                       # Discord killfeed of the game's kills, see below; needs dzo-admin 0.5.0
     discord: [pvp]                # default: none (the killfeed is off); webhooks of config.yaml notify.discord
     delay: 10m                    # default: 0 (at once); at most 24h
     ai_killers: true              # default; kills by Expansion AI
     ai_victims: false             # default; players killing AI
     deaths: false                 # default; also deaths without a killer (infected, animals, falls)
   container:
     env: {}                       # default: none
     mounts: ["/var/lib/GeoIP:/var/lib/GeoIP:ro"]   # default: none
     logz_dir: /srv/logz           # default: none
     cpus: "4"                     # default: no limit
     memory: 24G                   # default: no limit
     core_dumps: false             # default; true lets a crashing server write a core file
   hooks:                          # default: none; paths relative to instances/<name>/
     pre_start: ["hooks/traderstocks.sh"]
     post_stop: []
     pre_update: []
     post_update: []
     post_download: []
     post_merge: []
     post_render: []
     post_backup: []
   backup:
     before: [update, mod_update, mission_update, config_change]   # default
     schedule: ["00:00", "12:00"]  # default: none; periodic snapshots, same format as restarts.schedule
     keep: 20                      # default
     keep_by_reason: {mod_update: 8}   # default: none
     max_age: 30d                  # default: none
     min_keep: 3                   # default
     min_free_bytes: 50GiB         # default: none
   logs:                           # profile log rotation: added to the server's own files, see below
     rotate:                       # default: the server's own files
       - '^RFMosquito_.*\.log$'                    # a mod's log files (regular expression)
       - {match: '\.mdmp$', keep_newest: 0, max_age: 14d}   # with options
     keep_newest: 1                # default: 1
     min_age: 10m                  # default
     archive:
       compress: gzip              # default; gzip | none
       max_age: 90d                # default
       max_size: 50GiB             # default: none
   admin:                          # dzo-admin mod, see the admin map page
     disabled: false               # default
     sync_ms: 1000                 # default
     players_s: 5                  # default
     vehicles_s: 60                # default
     markers_s: 10                 # default
     events_s: 30                  # default
     allow_spawn: true             # default
     move_occupied_max: 200        # default; metres, 0 = no moves of vehicles with players inside
     allow_classes: []             # default: none
     deny_classes: ["Land_*"]      # default: none; items admins may not spawn (trailing * = prefix)
     vehicle_kinds: {bicycle: ["Bike_*"], helicopter: ["MyHeli_*"]}   # default: none
   admin_map:
     vanilla_events: true          # default
     watch:                        # default: none
       - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: rocket, color: "#8e44ad",
          visibility: moderator, label: "UFO", max: 50, cluster: 25}

Keys:

``name``
   The name of the instance; required, and it must be the name of the directory
   ``instances/<name>/``. It is the name in every command, the unit names
   (``dzo-<name>.service``) and the directory below ``paths.instances``.

``product``
   Which server build family the instance runs. The exact build is chosen with
   ``dzo instance upgrade`` (see :doc:`mods-and-updates`).

``map``, ``mission_source``, ``fallback_mission`` and ``mission``
   The mission folder name and where its pristine copy comes from. Without a
   ``mission_source``, a ``dayzOffline.<map>`` folder is taken from Bohemia's Central
   Economy repository at ``master``. See :doc:`missions`. ``mission_source`` has either
   ``git`` (a repository URL, which needs ``ref`` and ``path``) or ``preset`` (the name of an
   ``integrations/maps/<name>.yaml`` of the site repository), never both; ``ref`` and ``path``
   override those of a preset and of the default. ``fallback_mission`` names the
   ``dayzOffline.<map>`` folder of the installed server build that a base file the map lacks
   is taken from. ``mission.unmanaged`` lists glob patterns of files dzo never manages, even
   when the mission repository ships them; ``mission.drift`` says what happens to a managed
   file changed on disk since dzo wrote it, and has one value, ``warn-backup-overwrite`` (the
   default): warn, keep a copy, overwrite.

``ports``
   ``game`` (required), ``rcon`` and ``query``: the game, BattlEye RCon and Steam query
   port. dzo refuses a port that two instances use.

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
   Required. ``host`` uses the host's network directly. ``publish`` gives the
   container its own network namespace and publishes only the game ports.

``params``
   Launch parameters of ``DayZServer`` that dzo does not derive from other settings.
   ``cpuCount`` is a number (``-cpuCount=``) or ``auto``, which passes nothing and leaves the
   choice to the server; ``limitFPS`` is ``-limitFPS=``; ``extra`` is a list of further
   command-line arguments, appended as they are. Site default.

``container``
   Escape hatches for the container of the server; site default.

   ``env``
      Extra environment variables of the container, a map of name to value.
   ``mounts``
      Extra bind mounts, each ``source:destination`` or ``source:destination:ro``.
   ``logz_dir``
      A host directory mounted at the same path inside the container (for a mod that writes
      its logs there).
   ``cpus``
      A CPU limit for the container (``--cpus``), for example ``"4"``. Default: none.
   ``memory``
      A memory limit (``--memory``), for example ``24G``. Default: none.
   ``core_dumps``
      See below.

``container.core_dumps``
   ``false`` (default) sets ``Ulimit=core=0`` on the container. A DayZ server that
   crashes (or dies on shutdown) would otherwise write a core file of its whole memory,
   several GB, and that disk load stalls every other server on the host for many seconds.
   Set ``true`` only to debug a crash, and mind the disk space. The unit is rewritten at
   the next render; a running server keeps its limit until it is restarted.

``mods``
   Workshop mods (``id``) and local servermods (``local``, a name, see ``local_mods`` in
   :ref:`site-yaml`); exactly one of the two per entry. ``server: true`` loads the mod
   with ``-servermod``; a local mod must set it. ``debug_client: true`` (local mods only,
   instead of ``server``) loads a local mod as a client mod for debugging: players cannot
   download it and must install the same signed build by hand. The list
   order is also the order of the ``-mod=`` and ``-servermod=`` arguments, and
   decides which mod wins when two mods change the same mission file. dzo does
   not sort mods or check their dependencies: list a mod after the mods it needs.

``overlays``
   The names of the overlays (``overlays/<name>/`` of the site repository, or
   ``instances/<name>/overlays/<name>/``) merged into the mission, in list order, after the
   mods. See :doc:`missions`.

``logs``
   Which log files in ``profiles/`` are moved to the log archive (``paths.logs``), and how long the
   archive is kept; site default. Details in :ref:`profile-log-rotation`.

   ``rotate``
      A list of regular expressions matched against the path below ``profiles/``; a rule is a
      plain string, or ``{match: ..., keep_newest: N, max_age: ...}`` for options (``keep_newest``
      overrides the setting below for this rule, ``max_age`` how long its archived files are
      kept instead of ``archive.max_age``). **The default is the server's own files**:
      ``^DayZServer(_x64)?_.*\.RPT$``, ``^script_.*\.log$``, ``^crash_.*\.log$``,
      ``^error_.*\.log$``, ``^warning_.*\.log$``, ``^DayZServer(_x64)?_.*\.ADM$`` and
      ``\.mdmp$`` (``max_age: 14d``). The rules of an instance are added to those of ``defaults:``
      in ``site.yaml``; as soon as one of them exists the default list is **not** used any
      more, so repeat the server's own rules you still want. A mod's logs, which land anywhere in
      ``profiles/``, need a rule. ``dzo logs rotate <name> --dry-run`` lists the large files no
      rule matches.
   ``keep_newest``
      How many of the newest files of a rule stay in place. Default 1; 0 archives all.
   ``min_age``
      A file changed more recently is never touched. Default ``10m``.
   ``archive``
      ``compress`` is ``gzip`` (default) or ``none``; ``max_age`` how long archived files are kept
      (default ``90d``); ``max_size`` the size of the archive above which the oldest files are
      deleted (``50GiB``, ``500MiB``, ... default: no limit).

``admin`` and ``admin_map``
   Settings of the :doc:`dzo-admin mod <admin-map>`, which an instance runs when
   ``{local: dzo-admin, server: true}`` is in ``mods``.

   ``admin.disabled``
      ``true`` switches the integration off although the mod is listed.
   ``admin.sync_ms``, ``players_s``, ``vehicles_s``, ``markers_s``, ``events_s``
      How often the mod reports to ``dzo serve``: the sync in milliseconds (default 1000), players
      (5 s), vehicles (60 s), map markers (10 s) and events (30 s). 0 is the default;
      negative values are refused.
   ``admin.allow_spawn``
      Whether admins may spawn items (default true).
   ``admin.move_occupied_max``
      The farthest, in metres (ground distance), an operator may move a vehicle that has players inside, on the map
      or with ``dzo vehicle move``. Default 200; 0 refuses every move of an occupied vehicle; a negative value is an
      error. Empty vehicles can be moved anywhere in the world. A short move keeps the terrain the players' game
      already has loaded; a long one can leave them looking at no world while it streams in. Needs dzo-admin 0.7.0
      (an older mod cannot move vehicles at all). Written to the mod's ``config.json`` by ``dzo instance render``, read when
      the server starts.
   ``admin.allow_classes``, ``admin.deny_classes``
      Class names admins may or may not spawn; a trailing ``*`` is a prefix.
   ``admin.vehicle_kinds``
      Class name patterns by kind, ``bicycle``, ``boat`` or ``helicopter``, of other mods, on top of the
      classes dzo knows: the map and the vehicle list show them with the icon of the kind.
   ``admin_map.vanilla_events``
      Whether the map shows the events of the Central Economy as marker layers (default true). A
      rule with the same ``layer`` replaces one. Site default.
   ``admin_map.watch``
      Class watch rules, merged with those of the site by ``layer``, see :doc:`admin-map`. A rule has
      ``layer`` and ``classes`` (required), and ``icon`` (a Fork Awesome name), ``color`` (``#rgb``,
      ``#rrggbb`` or a colour name), ``visibility`` (the lowest role that sees the markers:
      ``viewer``, ``moderator``, ``operator`` or ``admin``, default admin), ``label``, ``max`` (the
      most markers), ``cluster`` (a hint for the web map). ``only_if`` is refused: it is not
      supported yet.

``updates``
   The mod update pipeline of the instance (:doc:`mods-and-updates`); site default.

   ``policy``
      What happens when a mod update is found: ``auto`` (default) restarts the server
      gracefully within the update windows, ``notify`` only reports, ``manual`` only
      records it.
   ``check_interval``
      How often the update check runs. Default ``1h``.
   ``window``
      Time ranges (``HH:MM-HH:MM``, a range may span midnight) in which an automatic update
      restart may happen. Default: any time.
   ``quiet_hours``
      Time ranges in which it never happens.
   ``min_restart_interval``
      No automatic update restart less than this after the last restart.
   ``batch_delay``
      Wait this long after the first pending update for more, so they go in one restart.
   ``apply_with_scheduled_restart``
      ``true`` uses the next maintenance restart (``restarts.schedule``) for the update instead of
      a restart of its own.
   ``max_delay``
      An update pending this long is applied whatever ``window`` and ``quiet_hours`` say.
   ``restart_minute``
      See below.
   ``restart_announce``
      The countdown of an update restart, the keys of ``restarts.announce``.

``welcome``
   The message every player gets when joining. The default is ``Welcome to #name!``. Set your own
   text (``#name``, the ``hostname`` of the instance's ``serverDZ.cfg`` (the instance name when it sets none), and ``#port``, the game port, work in it; up to 160
   characters), or ``""`` for none. Can be set under ``defaults:`` in ``site.yaml``.

``messages``
   Further messages the server sends on its own. A message has ``text`` (up to 160 characters),
   ``on_connect: true`` to send it once to a player who joins (``delay`` minutes after the player
   is in the game, default at once) and/or ``repeat`` to send it to everybody every that many
   minutes, and optionally ``color``: ``red`` (the default), ``green``, ``yellow`` or ``blue`` (the chat's own colour, hard to read).
   All times are minutes. Good for the homepage and the rules. It can be set under ``defaults:`` in
   ``site.yaml``; an instance's own list replaces it.

   The messages are sent by ``dzo serve`` while the server runs, not by DayZ: the server's own
   ``messages.xml`` sends nothing on a 1.30 server, so dzo always writes it empty (which also
   keeps the example messages of the pristine mission, with their shutdown countdown, from
   running), and an ``instances/<name>/messages.xml`` is refused. With the dzo-admin mod connected
   (the default, see :doc:`admin-map`) a message goes out as a coloured chat line, to a joining
   player as soon as the player is in the game. Without the mod it is sent over RCon (``say``):
   without colour, and to a joining player 45 seconds after the connect, because the character is
   still loading before. The messages are read when ``dzo serve`` starts and when the site is
   reloaded. Restarts and stops are announced by dzo itself, see ``restarts.announce`` and
   :doc:`operations`; there is no countdown in ``messages:``.

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

``restarts``
   Maintenance restarts; site default.

   ``schedule``
      The times of day as ``HH:MM`` or ``Mon,Thu HH:MM``, see :doc:`operations`. Default: none.
   ``announce``
      The countdown before the restart: ``minutes``, ``lock`` and ``kick`` (minutes before the
      restart, ``minutes`` >= ``lock`` >= ``kick``), ``text`` (the message) and ``ways`` (how and
      when it reaches the players, with ``within``, ``way``, ``type``, ``every``, ``at`` and ``now``).
      All of it is explained in :doc:`operations`.

``health``
   ``startup_timeout`` is how long a start may take (large mod lists load
   slowly; default ``45m``). ``interval`` (default ``60s``) × ``retries`` (default 5) is how
   long a hung server may stay hung before it is killed. Site default.

``restart_limit``
   Crash-loop brake: after ``burst`` (default 5) starts within ``interval`` (default ``30m``)
   the server stays stopped and you are alerted. ``cooldown`` is accepted for a retry after
   that time but is not acted on yet. Site default.

``notify``
   ``discord`` lists the names of the webhooks of ``notify.discord`` in ``config.yaml``
   this instance reports to. Unset: the webhook named ``default``. ``[]``: none. Site default.

``killfeed``
   Posts the kills of the instance to Discord, so a community can follow its server. Off unless
   ``discord`` names at least one webhook. It needs the :doc:`dzo-admin mod <admin-map>` (0.5.0 or
   newer) on the instance, because the mod reports the kills (see :ref:`event-feed`); without the
   mod nothing is posted. Not a site default: set it per instance. The ``notify`` key is a different
   thing: it routes the operator's notifications (updates, restarts, crashes), the killfeed has its own
   targets, so a public channel can get the kills and a private one the operator messages.

   ``discord``
      The names of webhooks of ``notify.discord`` in ``config.yaml`` that get the kills; any number.
      A name that is not there is an error when the instance is resolved (``dzo instance render``,
      ``dzo serve``), not a silent no-op. Unset or ``[]``: the killfeed is off.
   ``delay``
      Holds every kill back for this long before it is posted (``10m``, ``1h``; default ``0``, at once;
      at most ``24h``). It is meant for PvP servers: a kill posted at once tells everybody where a fight
      is going on. The delay counts from the moment ``dzo serve`` receives the kill. A changed ``delay``
      applies to the kills that come after the change; the ones already waiting keep their time.
   ``ai_killers``
      Include kills by DayZ Expansion AI (default ``true``). The killer is shown as ``AI "Name" (faction)``,
      the AI's own name and its faction, each only when it has one (dzo-admin 0.6.1; older mods send only the faction).
   ``ai_victims``
      Include players killing AI (default ``false``).
   ``deaths``
      Also post deaths without a killer: infected, animals, explosions, vehicles without a driver,
      falls, starvation and bleeding (default ``false``).

   .. code-block:: yaml

      killfeed:
        discord: [pvp]        # posts to the webhook named pvp
        delay: 10m            # a PvP server: kills appear ten minutes later
        deaths: true          # and what the infected did

   A kill is one line, for example ``💀 **Alice** killed **Bob** with M4-A1 (123 m)``, ``🤖 **AI "Frida" (East)**
   killed **Bob** with AKM (54 m)`` (melee has no distance) or, with ``deaths``, ``☠️ **Bob** was killed by
   infected``. Positions are never posted. Names and weapons come from players and mods, so Discord markup
   and mentions in them are escaped: they show as text and ping nobody. Kills that are due together (a
   grenade, a raid) are posted in as few messages as fit under Discord's limit of 2000 characters, in
   order.

   The killfeed is best effort and never slows the game: a post that fails, is refused with Discord's rate
   limit (429) or takes longer than 5 seconds is dropped and logged, not retried, and the later kills go
   out on time. At most 500 kills wait per instance; beyond that the oldest are dropped. Kills that wait
   for their delay survive a reload of the site (``SIGHUP``) but not a restart of ``dzo serve``. Kills
   that happened before ``dzo serve`` started are not posted. The same events, with chat and base
   building, are on the events page (:doc:`web`) and in ``dzo events`` (:doc:`cli`).

``hooks``
   Scripts for the hook points ``pre_start``, ``post_stop``, ``pre_update``, ``post_update``,
   ``post_download``, ``post_merge``, ``post_render`` and ``post_backup``: each a list of paths
   relative to ``instances/<name>/``. When they run, and what a failing script does, is in
   :doc:`missions`. Not a site default.

``backup``
   The snapshot policy, see :doc:`backups`; site default. ``before`` lists the changes that
   take a snapshot first (``update``, ``mod_update``, ``mission_update``, ``config_change``,
   ``render``; the first four are the default, ``[]`` none). ``schedule`` lists the times of the
   periodic snapshots, like ``restarts.schedule``. After a snapshot the oldest are pruned:
   ``keep`` is the number kept (default 20), ``keep_by_reason`` a limit per reason
   (``scheduled``, ``update``, ...), ``max_age`` how old a snapshot may get (``30d``, ``12h``),
   ``min_keep`` the number never to go below (default 3), ``min_free_bytes`` the free space to
   keep by pruning the oldest first (``50GiB``).

serverDZ.cfg
------------

Keep your normal ``serverDZ.cfg`` in the instance directory. dzo enforces the
keys it owns (ports, ``template``, ``instanceId``, ``steamQueryPort``) and shows
a diff when your file and the enforced values differ (``dzo config diff``).

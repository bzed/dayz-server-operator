.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Command reference
=================

All commands run as the service user (``sudo -iu dayz``). ``dzo <command>
--help`` shows every option, and the man pages have the full reference. The most used
commands are on the :download:`cheat sheet <cheatsheet/dzo-cheatsheet.pdf>` (two A4 pages).

Setup and Steam
---------------

.. list-table::
   :widths: 45 55

   * - ``dzo setup [--dry-run]``
     - first-time setup as the service user: directories, container images, site
       repo, weekly image refresh timer (see :doc:`installation`)
   * - ``dzo setup --images-only``
     - rebuild the container images and prune the old ones
   * - ``dzo steam login [--user <name>] [--passthrough]``
     - interactive Steam login (password and Steam Guard)
   * - ``dzo steam status``
     - account, last login, session state
   * - ``dzo steam reset-cache``
     - wipe steamcmd's working directory

Site repository
---------------

.. list-table::
   :widths: 45 55

   * - ``dzo site pull | validate``
     - update the site checkout (clone it the first time), load it and resolve every
       instance
   * - ``dzo site status | commit -m <msg> [--push]``
     - uncommitted changes in the checkout (``dzo mod add`` edits it); commit and push them

Products and updates
--------------------

.. list-table::
   :widths: 45 55

   * - ``dzo product install <product>``
     - first download of a server build
   * - ``dzo product update <product> [--force]``
     - download a new server build (manual only)
   * - ``dzo update check [--apply]``
     - check mods (and apply per policy); report new server builds

Instances
---------

.. list-table::
   :widths: 45 55

   * - ``dzo instance show <name> [--quadlet]``
     - print the resolved configuration (see :doc:`resolved-instance`)
   * - ``dzo instance create <name>``
     - first render: subvolume, pristine mission, first live mission (fails if the instance exists)
   * - ``dzo instance apply <name> [--dry-run]``
     - (re)generate the units and timers
   * - ``dzo instance clone <old> <new> [--force]``
     - copy an instance's data into a new instance of the site repository
   * - ``dzo instance remove <name> [--keep-snapshots] [--delete-logs] [--yes]``
     - stop, remove units and data
   * - ``dzo instance upgrade <name> --build <id> [--wipe] [--dry-run]``
     - switch to another installed server build (see :doc:`mods-and-updates`)
   * - ``dzo instance mods <name> list | move <id> --before | --after <other>``
     - the mod list and its order (add and remove: ``dzo mod add | remove``)
   * - ``dzo instance hook <name> <point>``
     - run the hook scripts of one hook point (the units use it, see :doc:`missions`)
   * - ``dzo instance shutdown <name>``
     - ask the server to shut down over RCon and wait (the unit's ``ExecStop``)
   * - ``dzo instance ack-failure <name>``
     - allow starts again after a failed render or crash loop
   * - ``dzo start <name>``
     - start
   * - ``dzo stop <name> [--graceful | --minutes N --lock N --kick N --text T] [--cancel]``
     - stop and stay stopped; with a countdown option, announce, lock and kick the players first (:ref:`graceful-stop`)
   * - ``dzo restart <name> [--minutes N --lock N --kick N --text T] [--now] [--cancel]``
     - graceful restart
   * - ``dzo status [<name>] [--json] [--write <dir>]``
     - state, players, health, restarts, last render (what the exporter reports)
   * - ``dzo exporter``
     - serve ``/metrics`` and ``/status`` (the exporter unit runs this)
   * - ``dzo logs <name> [-f] [-n N]``
     - server console (the unit's journal)
   * - ``dzo logs rotate <name> | --all [--dry-run]``
     - move old profile logs into the archive, apply its retention (see :doc:`operations`)
   * - ``dzo logs archive <name> [--since 7d] [--match <re>]``
     - list the archived profile logs
   * - ``dzo logs crash-summary <name>``
     - after an unclean exit: tails of the newest logs to the journal and Discord (the unit runs it)
   * - ``dzo shell <name> [command…]``
     - a debug container with the instance's mounts and no network (``--network host`` to reach its ports)
   * - ``dzo exec <name> <command…>``
     - a command in the running container
   * - ``dzo rcon exec --instance <name> <command>``
     - one RCon command with the instance's port and password
   * - ``dzo rcon console <name>``
     - interactive console with the server's messages
   * - ``dzo rcon rotate <name> [--restart]``
     - a new RCon password (read at the next start)
   * - ``dzo wipe <name> [--restart] [--yes]``
     - wipe the world (confirmation, destructive snapshot first)

Mods
----

.. list-table::
   :widths: 45 55

   * - ``dzo mod add <id | local name> --instance <name> [--server]``
     - install a mod and add it to an instance (a local mod is always a servermod)
   * - ``dzo mod add <local name> --instance <name> --client --force``
     - debugging only: a signed local mod that clients load too (:ref:`debug-client-mods`)
   * - ``dzo mod list [<instance>]``
     - the mods of an instance and their installed generation
   * - ``dzo mod remove <id | local name> --instance <name>``
     - remove a mod from an instance's list
   * - ``dzo mod update [<instance>]`` (also ``dzo mod download``)
     - download the mods listed in ``instance.yaml`` that are missing, and new versions of the installed ones (to add a new mod to the list use ``dzo mod add``)
   * - ``dzo mod refresh <id>… | --all [--instance <name>] --force``
     - force a fresh download
   * - ``dzo mod deps <pbo | config.bin | config.cpp>``
     - show a mod's declared dependencies (diagnostics, does not affect the load order)
   * - ``dzo integration check <modid>``
     - fetch, normalise and validate a mod's integration files

Missions and config
-------------------

.. list-table::
   :widths: 45 55

   * - ``dzo instance render <name> [--dry-run] [--update-pristine]``
     - build the mission and apply it in place, or show what would change
   * - ``dzo mission init | update <name>``
     - first live mission; fetch a new pristine version and show the plan
   * - ``dzo mission status <name>``
     - source, age of the pristine copy, what the next render would change
   * - ``dzo mission rollback <name> [<time>] [--list]``
     - restore managed files from file history
   * - ``dzo mission reinit <name> [--yes]``
     - recreate the live mission (confirmation and snapshot)
   * - ``dzo config diff | apply <name>``
     - serverDZ.cfg changes

Backups
-------

.. list-table::
   :widths: 45 55

   * - ``dzo backup create | list | diff | prune <name>``
     - snapshots
   * - ``dzo backup pin | unpin <name> <id>``
     - protect a snapshot from pruning
   * - ``dzo restore <name> <id> [--path <path>]``
     - full or partial restore

Monitoring and notifications
----------------------------

.. list-table::
   :widths: 45 55

   * - ``dzo check remote --url <status url> [--instance <name>] [--updates] [--steam] [--jobs] [--disk]``
     - Icinga plugin
   * - ``dzo check a2s <host>:<query port>``
     - Icinga plugin, game server only
   * - ``dzo notify test [--target <name>]``
     - send a test notification

Web interface and API
---------------------

.. list-table::
   :widths: 45 55

   * - ``dzo serve``
     - run the JSON API and the endpoint the dzo-admin mods call (see :doc:`api`)
   * - ``dzo web``
     - run the web interface (see :doc:`web`)
   * - ``dzo token create <name> --role <role> [--instance <name>…] [--delegate | --web] [--ttl <d>]``
     - create an API token; the secret is printed once. ``--delegate`` may name the acting user (scripts and tools), ``--web`` is the token of ``dzo web``, which acts for logged-in people (see :doc:`api`)
   * - ``dzo token list`` / ``dzo token revoke <id>``
     - list or delete tokens
   * - ``dzo user list``
     - the people who may use the web interface, with their grants (Steam ID, username, name, ``instance=role``, state)
   * - ``dzo user grant <steamid> --role <role> --username <name> [--instance <name>…] [--name <text>]``
     - give a role (``viewer``, ``moderator``, ``operator``, ``admin``) on the listed instances, or on ``*`` (every instance, also later ones) without ``--instance``. Creates the person and replaces the role on instances they already have. ``--username`` (3 to 32 of ``a-z 0-9 . _ -``, unique, not case-sensitive) is the login name and what people see; a new person needs it, for an existing one it is kept unless given, and only an administrator changes it (here or on the people page). ``--name`` is an optional display name. This is how the first admin is made
   * - ``dzo user revoke <steamid> [--instance <name>…]``
     - take the roles on the listed instances away, or every grant without ``--instance``. A person left without a grant is logged out
   * - ``dzo user password <steamid|username>``
     - set the password of a person who has a role and a username, reading the password from the terminal (twice, no echo) or one line from a pipe. For when Steam sign-in does not work: the first login with it sets up a second factor. All sessions of the person end
   * - ``dzo user disable <steamid>`` / ``enable`` / ``delete``
     - block a person from logging in (their sessions end, grants stay) / allow it again / delete the person with grants, credentials and sessions
   * - ``dzo user reset <steamid|username>``
     - remove password, TOTP, passkeys, recovery codes and sessions (the username stays); the person signs in through Steam and sets up a second factor again

``dzo user`` runs on the host of ``dzo serve`` and writes its database (``<paths.db>/dzo.sqlite``) directly,
which is safe while ``dzo serve`` runs, like ``dzo token`` with the token file. So it works when nobody can
log in. Every change is written to the audit log with the source ``cli`` and the actor ``cli:$USER``.

Map tiles
---------

.. list-table::
   :widths: 45 55

   * - ``dzo map tiles build <map> [data.pbo] [--force]``
     - build the satellite tiles of a map from its data PBO (see :doc:`admin-map`)
   * - ``dzo map tiles status``
     - maps with tiles, and whether the source file changed since

Players and vehicles
--------------------

These talk to a running ``dzo serve`` through the API. Give them a token with
``--token-file <file>`` or ``$DZO_TOKEN``; ``--api <url>`` picks the
installation (default: the local ``serve.listen``). They need the
:doc:`dzo-admin mod <admin-map>` on the instance.

.. list-table::
   :widths: 45 55

   * - ``dzo player list <instance>``
     - online players
   * - ``dzo events <instance> [-f] [-n <lines>] [--kind <kind,...>] [--json] [--no-color]``
     - what happens in the game, journalctl style: kills, deaths, joins, building and raids, chat, admin actions.
       Prints the newest ``-n`` events (default 20; the feed is kept over restarts of ``dzo serve``) and exits;
       ``-f`` follows until Ctrl-C and reconnects by itself, marking events that were lost with ``-- events missed --``.
       ``--kind`` takes kinds (``kill``, ``chat``, ...) or groups (``kills``, ``chat``, ``players``, ``building``,
       ``raids``, ``flags``, ``stashes``, ``admin``). ``--json`` prints one JSON object per event. Colours only on a
       terminal. Names and chat are printed with control characters replaced by ``?``. Needs the ``view`` role.
   * - ``dzo player msg <instance> <steamid|all> <text> [--style chat|important|friendly|blue|notification|expansion|rcon] [--type toast|baguette|activity|killfeed|market|garage] [--title <text>] [--seconds <n>]``
     - message one player or everyone. ``chat``, ``important``, ``friendly`` and ``blue`` are coloured chat lines; ``notification`` is
       the on-screen notification of the game (``--title``, default "Server", and ``--seconds``, default 10, at most 60);
       ``expansion`` is an Expansion notification of ``--type`` (default ``toast``) and needs DayZ Expansion on the server;
       ``rcon`` is BattlEye's ``say``, to everyone only
   * - ``dzo player kick <instance> <steamid> <reason...>``
     - kick a player; the reason (at most 64 bytes) is shown to them
   * - ``dzo player heal <instance> <steamid>``, ``dzo player fixgear <instance> <steamid>``
     - heal a player; repair everything they carry and wear
   * - ``dzo player kill <instance> <steamid> --yes``
     - kill a player (needs ``--yes``: it cannot be undone)
   * - ``dzo player tp <instance> <steamid> (--x <x> --z <z> | --to <steamid>)``
     - teleport a player
   * - ``dzo player give <instance> <steamid> <class> [--qty <n>|default|max] [--health <0..1>] [--target inventory|hands|ground] [--attachments]``
     - spawn an item for a player
   * - ``dzo player spawn <instance> <class> --x <x> --z <z> [--qty <n>|default|max] [--health <0..1>] [--attachments]``
     - spawn an item on the ground at a position; a quantity above the stack size makes more stacks
   * - ``dzo vehicle list <instance>``
     - persistent vehicles
   * - ``dzo vehicle spawn <instance> <class> --x <x> --z <z>``
     - spawn a vehicle with its parts and fuel on the ground at a position
   * - ``dzo vehicle repair <instance> <id> [--scope all|engine|parts|wheels|fluids|missing]``
     - repair a vehicle; ``all`` also refuels it and fits missing parts
   * - ``dzo vehicle flip <instance> <id>``
     - stand a vehicle upright on its wheels (not a boat: ``boats cannot be flipped``)
   * - ``dzo vehicle move <instance> <id> --x <x> --z <z>``
     - put a vehicle on the ground at a position, upright and at rest; players inside move with it, at most
       ``admin.move_occupied_max`` metres (:doc:`configuration`)
   * - ``dzo vehicle delete <instance> <id> [--force]``
     - delete a vehicle (``--force``: even with players inside)
   * - ``dzo flag raise <instance> <id>``
     - raise a territory flag and fill its timer; the id is the id of its marker on the ``Flags`` layer (``/map/markers``)

Bans and evidence
-----------------

These talk to a running ``dzo serve`` through the API, like the player commands (``--token-file`` or
``$DZO_TOKEN``, ``--api``). See :doc:`moderation`.

.. list-table::
   :widths: 45 55

   * - ``dzo ban add (--steam <id> | --guid <guid> | --ip <addr>) --duration <30m|12h|7d|2w|permanent> (--instance <name> | --global) [--reason <text>] [--message <text>] [--evidence <id>]``
     - ban a player; the answer says per instance whether the ban is in force or pending
   * - ``dzo ban list [--state active|expired|revoked] [--instance <name>] [--query <text>] [--limit <n>]``
     - bans, newest first
   * - ``dzo ban show <id>``
     - a ban with its targets, evidence and history
   * - ``dzo ban edit <id> [--duration ...] [--reason ...] [--message ...] [--add-ip <addr>] [--remove-ip <addr>] ...``
     - change only the given fields
   * - ``dzo ban revoke <id> --reason <text>``
     - unban: end a ban now (it stays in the history)
   * - ``dzo evidence add (--steam <id> | --guid <guid> | --ip <addr>) --instance <name> (--log <text> | --file <path|-> | --note <text> | --image <file> [--caption <text>])``
     - file evidence against a player
   * - ``dzo evidence list (--steam <id> | --guid <guid> | --ip <addr>)``
     - the evidence filed against a player

Migration and development
-------------------------

.. list-table::
   :widths: 45 55

   * - ``dzo legacy convert-config --repo <dir> --ref <branch>… --out <dir>``
     - example site config from dayzdockerserver branches (see :doc:`migration`)
   * - ``dzo test boot <name> [--server steam|<dir>] [--mods-from …] [--container] [--expect <re>] [--out <dir>]``
     - boot a real server on a render result (development machines only)
   * - ``dzo test boot --vanilla <name>``
     - boot the unmodded server and record the baseline of this server build
   * - ``dzo gen-man <dir>``
     - write the man pages of every command (the Debian build runs it)

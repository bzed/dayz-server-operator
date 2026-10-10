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
   * - ``dzo start | stop <name>``
     - start, stop (stays stopped)
   * - ``dzo restart <name> [--minutes N --lock N --delay N --text T] [--now] [--cancel]``
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
   * - ``dzo mod update [<instance>]``
     - check and download updates now
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
   * - ``dzo loki-config``
     - print a log agent configuration snippet

Web interface and API
---------------------

.. list-table::
   :widths: 45 55

   * - ``dzo serve``
     - run the JSON API and the endpoint the dzo-admin mods call (see :doc:`api`)
   * - ``dzo web``
     - run the web interface (see :doc:`web`)
   * - ``dzo token create <name> --role <role> [--instance <name>…] [--delegate] [--ttl <d>]``
     - create an API token; the secret is printed once
   * - ``dzo token list`` / ``dzo token revoke <id>``
     - list or delete tokens

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
   * - ``dzo player msg <instance> <steamid|all> <text> [--style chat|important|notification]``
     - message one player or everyone
   * - ``dzo player tp <instance> <steamid> (--x <x> --z <z> | --to <steamid>)``
     - teleport a player
   * - ``dzo player give <instance> <steamid> <class> [--qty <n>] [--health <0..1>] [--target inventory|hands|ground]``
     - spawn an item for a player
   * - ``dzo vehicle list <instance>``
     - persistent vehicles
   * - ``dzo vehicle repair <instance> <id> [--scope all|engine|parts|wheels|fluids]``
     - repair a vehicle
   * - ``dzo vehicle delete <instance> <id> [--force]``
     - delete a vehicle (``--force``: even with players inside)

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

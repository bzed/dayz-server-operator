.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Development
===========

Principles
----------

dzo is written the lazy way: lazy meaning efficient, not careless.

* **Build what is needed now.** No features, options or abstractions "for
  later". An interface with one implementation, a factory for one product or a
  setting for a value that never changes is not added.
* **Reuse before writing.** In this order: code that already exists in dzo, the
  Go standard library, a feature of the platform (systemd, podman, btrfs, the
  database), an existing well-maintained library, and only then new code.
* **Boring over clever.** Short, obvious code that someone can debug at 3 am.
* **Never lazy about safety.** Input validation, error handling that prevents
  data loss, security measures and tests are not simplified away.
* A deliberate shortcut with a known limit gets a ``ponytail:`` comment that
  names the limit and the upgrade path.

Building
--------

dzo is a single static Go binary built with the current upstream Go toolchain.
Modules are vendored, so builds work offline.

.. code-block:: sh

   make build      # static binary
   make servermods # pack the dzo-admin mod into dist/servermods/ (needs the submodules; see below)
   make test       # tests with race detector and coverage gate
   make lint       # includes reuse lint (licence headers)
   make licenses   # dependency licences must be AGPL-compatible
   make docs       # this documentation (needs python3-sphinx)
   make deb        # Debian package

Building and testing the servermod
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

``make servermods`` packs ``servermods/*/src/*`` with dzo's own Go packer (no
DayZ Tools, no armake2) into ``dist/servermods/<mod>/addons/*.pbo`` and writes
``compat.yaml``. The output is reproducible, and servermods are never sent to
clients, so nothing is signed and no key is needed. The scripts go in as plain
text and ``config.cpp`` is not rapified; the server reads both.

While 1.30 is experimental, a servermod has to load on **both** 1.29 (stable,
Steam app 223350) and 1.30 (experimental, app 1042420, "DayZ Server Exp"). One
PBO serves both; version-specific code belongs behind ``#ifdef DAYZ_1_29`` with
the new code in ``#else``. Boot it on each, one server tree per version, never
two servers on one tree and never default ports. ``dzo test boot --server
<dir>`` takes the experimental install directory; ``--server steam`` is the
stable one. Each server build needs its own vanilla baseline
(``dzo test boot --vanilla``).

A quick check without a configured instance: symlink ``dist/servermods/dzo-admin``
into a tree of each server as ``@dzo-admin``, start it with
``-servermod=@dzo-admin`` (a relative path), and compare the script module file
counts with an unmodded boot of the same version. Both of these must hold:

* no ``SCRIPT    (E)`` line that the vanilla boot does not also log (the
  experimental server logs ``Leaked 'BunkerBroadcastManager'`` without any mod);
* the counts rise by the mod's files: ``DZO_ADMIN`` appears in the ``defines``
  of the ``Module:`` lines, and the Game, World and Mission counts are higher
  than vanilla.

Measured with dzo-admin at ``a0e644b``:

.. list-table::
   :header-rows: 1

   * - Server
     - Game
     - World
     - Mission
   * - 1.29.163709 (build 24570360), vanilla / with mod
     - 416 / 422
     - 2123 / 2125
     - 209 / 210
   * - 1.30.164014 (build 25319221), vanilla / with mod
     - 440 / 446
     - 2321 / 2323
     - 219 / 220

Tests
-----

The test suite needs no Steam account and no DayZ server: steamcmd, systemd,
podman, RCon and the Steam API are replaced by fakes and recorded fixtures.
Coverage must stay at 85 % or more; CI fails below that.

Boot test with a real server
----------------------------

Unit tests cannot prove that DayZ accepts what dzo renders. ``dzo test boot``
starts a real ``DayZServer`` headless on a render result and checks the logs.

.. warning::

   Only on a development machine or a dedicated test machine. ``dzo test boot``
   refuses to run on a host where dzo manages instances (instance directories
   below ``paths.instances``, or dzo quadlets for the user), and there is no
   option to override that.

.. code-block:: sh

   dzo test boot --vanilla deerisle   # once per server build: record the baseline
   dzo test boot deerisle             # boot the instance
   dzo test boot deerisle --expect 'MyMod: loaded' --out ./logs --keep-tree

``--server steam`` (the default) uses the "DayZ Server" tool installed by your
Steam client (Library, Tools). dzo finds it through Steam's own manifests, and
neither installs it nor writes into Steam's directories; ``--server <dir>``
uses another installation. Mods come from your client's workshop folder
(``--mods-from steam-client``, the default with ``--server steam``), from dzo's
cache (``--mods-from cache``), or from a directory of ``@Mod`` folders. dzo-admin
and other local servermods always come from dzo's cache. With ``--container`` the
server runs in the instance's runtime image instead (podman): the tree, the server install and the mods
are mounted at their own paths, the install and the mods as overlays, so the links of the tree resolve and
nothing is written into them. ``--port-from`` and ``--port-to`` choose the range of the test ports on a host that reserves
one for the game.

The test builds a throwaway tree (symlinks to the server install, a copy of the
mission as a render would apply it, your ``serverDZ.cfg`` with test ports, a
random password and a hostname that starts with ``[dzo boot test]``), starts the
server on free ports and stops it afterwards. The Steam query answers a couple
of seconds after the start, long before the mission has loaded, so dzo waits for
the query, then for the mission's scripts, then for ``--settle`` seconds (20 by
default), and with dzo-admin for the mod's first contact. ``--timeout`` limits
the whole wait. The exit code is 0 if everything passed, 1 if a check failed and
2 if the test itself could not run.

.. list-table::
   :header-rows: 1
   :widths: 18 82

   * - Check
     - What fails it
   * - ``ready``
     - the server exited or did not answer the Steam query in time
   * - ``mission``
     - the query answers but the mission's scripts never load
   * - ``crash``
     - a crash dump was written
   * - ``scripts``
     - a script compile error (``SCRIPT (E)``, the tag is space-padded), or no
       script log at all
   * - ``modules``
     - a mod ships scripts for a module but its file count did not rise over the
       vanilla baseline: the scripts were not loaded (an absolute mod path or a
       PBO without a prefix loads nothing and prints no error)
   * - ``ce``
     - an XML file that does not parse, a ``<ce folder>`` or a file that does not
       exist, a types file that cannot be read
   * - ``expect``
     - an ``--expect`` regular expression that is not in the script log or the RPT
   * - ``dzo-admin``
     - the mod never made contact at the test endpoint

Everything else that is new against the baseline is shown as a warning, verbatim,
for example a class name in ``types.xml`` that does not exist
(``Type 'X' will be ignored``), because Bohemia's own missions have a few of
those too.

The vanilla baseline holds, per server build, the number of script files each
module loads and the error lines an unmodded server logs. A server installed
through Steam logs several hundred of them, because its map data is stripped.
Without a baseline the counts and error lines are only shown. The baseline is
cached below ``paths.cache`` and keyed by the server binary, so a game update
needs a new one.

What a real 1.29 server logs for broken input
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

These were captured with a real server (build 24570360) and are the fixtures
the checks are tested against (``internal/boottest/testdata``).

.. list-table::
   :header-rows: 1
   :widths: 40 60

   * - Input
     - Log
   * - ``types.xml`` that is not well-formed
     - ``!!! [ERROR][XML] :: load [...types.xml] failed``, ``Error reading end
       tag. line N``, ``!!! [CE][offlineDB] :: Failed to read types file``
   * - ``<ce folder>`` that does not exist, or a file that does not
     - ``!!! [ERROR][XML] :: load [...] failed``, ``Failed to open file``,
       ``!!! [CE][offlineDB] :: Failed to read types file``
   * - class that does not exist in ``types.xml``
     - ``!!! [CE][offlineDB] :: Type 'X' will be ignored. (Type does not exist.
       (Typo?))``
   * - script error in the mission's ``init.c``
     - ``SCRIPT    (E): @"...init.c,99": Can't find variable 'x'`` and
       ``Can't compile mission init script``; the mission scripts never load
   * - broken ``cfggameplay.json``
     - nothing: the file is read only if ``serverDZ.cfg`` has
       ``enableCfgGameplayFile = 1``, and not checked by this test

The central economy marks its errors with ``!!!``. ``error.log`` also holds
plain information lines (``ENTITY : Load entity type ...``) that differ from run
to run; only its ``(E)`` and ``(W)`` lines count.

Logs are kept in ``--out`` (default ``./boottest-<instance>-<time>/``), and the
whole tree with ``--keep-tree``.

Booting by hand
~~~~~~~~~~~~~~~

Without dzo, the same procedure: build a tree of symlinks to the Steam server
install (never write into it), give the server free ports for the game, the
Steam query (``steamQueryPort`` in ``serverDZ.cfg``) and RCon (``RConPort`` in
``profiles/battleye/beserver_x64.cfg``), pass mods as paths relative to the tree
(absolute ones are silently ignored), run it from the tree with ``ulimit -c 0``
and stop it with ``timeout -k 15``. Then read ``script_*.log``, the ``.RPT`` and
``error.log`` in ``profiles/``.

Contributing
------------

* Code and documentation change together: a change in behaviour updates the
  matching page here.
* The documentation is plain reStructuredText in ``docs/``, built with Sphinx.
  ``make docs`` must build without warnings.
* dzo is licensed under the AGPL-3.0-or-later. The repository follows the
  `REUSE <https://reuse.software/>`_ specification: every new file starts with
  SPDX headers in the file's comment syntax, for example in Go:

  .. code-block:: go

     // SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
     // SPDX-License-Identifier: AGPL-3.0-or-later

  Files that cannot carry comments (images, test fixtures) get a
  ``<file>.license`` file next to them or an entry in ``REUSE.toml``.
  ``make lint`` runs ``reuse lint`` and fails on files without licence
  information.

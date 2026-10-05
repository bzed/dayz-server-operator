.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Quick start: the first server
=============================

This walk-through creates one vanilla Chernarus server. All commands run as the
service user (``sudo -iu dayz``). It follows what has been run end to end on a
development host; :doc:`cli` lists every command, including some that are
described but not built yet.

.. note::

   ``dzo instance create``, ``dzo instance apply``, ``dzo instance upgrade``,
   ``dzo instance mods``, ``dzo mod remove`` and the ``dzo mission``
   subcommands other than ``diff`` and ``apply`` are design, not code yet. What
   they would do is done by the steps below: ``dzo instance render`` creates
   the instance on its first run, ``dzo units sync`` writes the units, and the
   mod list is a plain edit of ``instance.yaml``.

1. Point dzo at your site repository
------------------------------------

Create an empty git repository for your configuration and add it to
``/etc/dzo/config.yaml``:

.. code-block:: yaml

   site:
     remote: git@<git host>:<you>/dayz-site.git   # or a local path
     branch: main

.. code-block:: sh

   dzo setup            # directories, container images, the site checkout
   dzo site validate    # loads the site and resolves every instance

``dzo site pull`` fetches changes you pushed to the site repository later.

2. Log in to Steam and install the server build
-----------------------------------------------

.. code-block:: sh

   dzo steam login --user <steam account>     # password and Steam Guard
   dzo product install dayz-stable            # about 3 GB

3. Describe the instance
------------------------

Create ``instances/chernarus/instance.yaml`` in the site repository:

.. code-block:: yaml

   name: chernarus
   product: dayz-stable
   map: dayzOffline.chernarusplus   # mission source: Bohemia's Central Economy repository
   ports: {game: 2302, rcon: 2303, query: 27016}
   network: host
   mods: []
   restarts:
     schedule: ["*-*-* 00/4:00"]    # every 4 hours

Without ``mission_source`` the mission comes from the ``dayzOffline.chernarusplus``
folder of the Central Economy repository (see :doc:`missions`). Add a
``serverDZ.cfg`` next to it (your usual server settings, with a ``password``;
dzo sets the mission ``template`` and the Steam query port itself), commit,
push, then:

.. code-block:: sh

   dzo site pull
   dzo site validate

4. Render and start it
----------------------

.. code-block:: sh

   dzo instance render chernarus --dry-run   # what would be written
   dzo instance render chernarus             # creates the instance on the first run
   dzo units sync                            # writes the container units and timers
   dzo start chernarus
   dzo status chernarus

``dzo start`` returns once the container is healthy, that is when the server
answers Steam queries. Follow the console with ``dzo logs chernarus -f``.

5. Add a mod
------------

Add it to the instance in the site repository (``{id: 1559212036}`` is
Community Framework, ``{id: 1828439124, server: true}`` loads a mod with
``-servermod``; a local servermod such as dzo-admin is ``{local: dzo-admin,
server: true}``), commit, push and:

.. code-block:: sh

   dzo site pull
   dzo mod update chernarus        # downloads what is new
   dzo restart chernarus --minutes 5

The restart announces itself in game, locks the server, kicks the remaining
players and restarts with the new mod generations. See :doc:`operations`.

What next
---------

* Mod integrations and mission overlays: :doc:`missions`
* How the server is stopped and restarted: :ref:`stopping-the-server`
* Automatic mod updates and server upgrades: :doc:`mods-and-updates`
* Snapshots and restores: :doc:`backups`
* The web interface and the live map: :doc:`web`
* Prometheus, Icinga and Discord: :doc:`monitoring`

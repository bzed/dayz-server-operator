.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

dzo — DayZ server operator
==========================

dzo runs and maintains DayZ dedicated servers on a Debian host. It installs the
server and workshop mods, prepares each server's mission, runs every server as a
rootless podman container managed by systemd, restarts servers gracefully, keeps
mods up to date, takes btrfs snapshots before changes, and exports monitoring
data. An optional web interface adds player management, moderation and a live
admin map.

.. note::

   dzo is under active development. These pages describe how dzo works and is
   meant to be used. Commands and options may still change before the first
   release.

Where to start
--------------

* New to dzo: read :doc:`overview`, then follow :doc:`installation` and
  :doc:`quickstart`.
* In a hurry: the :download:`two-page cheat sheet (PDF) <cheatsheet/dzo-cheatsheet.pdf>` has
  the setup steps and the everyday commands.
* Coming from ``dayzdockerserver``: see :doc:`migration`.
* Something is broken: see :doc:`troubleshooting`.

.. toctree::
   :maxdepth: 2
   :caption: Getting started

   overview
   installation
   dev-host
   quickstart
   migration

.. toctree::
   :maxdepth: 2
   :caption: Administration

   configuration
   resolved-instance
   missions
   mods-and-updates
   operations
   backups
   monitoring
   troubleshooting

.. toctree::
   :maxdepth: 2
   :caption: Web interface

   web
   api
   admin-map

.. toctree::
   :maxdepth: 2
   :caption: Reference

   cli
   development
   spikes

.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Installation
============

Requirements
------------

* Debian 13 (trixie), x86_64, with podman 5.4 and systemd 257 from Debian.
* Linux kernel 6.12 or newer (the trixie kernel, or a trixie-backports kernel).
* A **btrfs** filesystem for the instance data and the snapshots. Both must be on
  the same btrfs filesystem. The download cache can live anywhere, but on btrfs
  copies are nearly free.
* A Steam account that owns DayZ. The DayZ server cannot be downloaded
  anonymously.
* Enough disk space: about 3 GB per server build, plus mods, plus snapshots.

Recommended, not required:

* Mount the btrfs filesystem with ``user_subvol_rm_allowed``. dzo can then delete
  old snapshots instantly. Without it, deletion still works but takes longer.

Install the package
-------------------

dzo is shipped as a Debian package, ``dzo``. It pulls in ``podman``, ``passt``,
``git``, ``uidmap`` and the web libraries from Debian. steamcmd is not installed
on the host. ``dzo setup`` builds a steamcmd container image, but the commands
that download servers and mods still run the ``steamcmd`` they find in the
``PATH`` (``--steamcmd`` picks another program, for example a wrapper that runs
the image).

.. code-block:: sh

   apt install ./dzo_<version>_amd64.deb

The package

* creates the system user ``dayz`` with the home directory ``/var/lib/dzo``,
* assigns subordinate uid/gid ranges to it (needed for rootless podman),
* enables lingering, so the user's services run without a login,
* installs the Containerfiles of the two images in ``/usr/share/dzo/images/``,
  the dzo-admin servermod with its ``compat.yaml`` in
  ``/usr/share/dzo/servermods/``, and example map presets in
  ``/usr/share/dzo/presets/maps/`` (copy the ones you want to
  ``integrations/maps/`` in your site repository).

It does **not** start any game server.

On a development or test host without the package, see :doc:`dev-host` for the
user, the btrfs mount and the other steps by hand.

Choose the data locations
-------------------------

All paths are configured in ``/etc/dzo/config.yaml``. The defaults put
everything under ``/var/lib/dzo``. To use another location, for example
``/srv/dayz``:

.. code-block:: yaml

   paths:
     data: /srv/dayz
     instances: ${data}/instances    # must be btrfs
     snapshots: ${data}/snapshots    # same btrfs filesystem as instances
     cache: ${data}/cache
     secrets: ${data}/secrets
     db: ${data}/db

Run the setup
-------------

Run the setup as the service user (it refuses to run as root). It

* creates the directories under ``paths`` (the secrets directory is private) and
  checks that they are writable, that ``instances`` and ``snapshots`` are on the
  same filesystem, and warns if ``instances`` is not on btrfs or the free space
  is low,
* builds the two container images, ``localhost/dzo-runtime`` for the game
  servers and ``localhost/dzo-steamcmd``, unless they exist already,
* tells you if the Steam login is still to be done,
* clones the site repository from ``site.remote``, and
* enables a weekly timer, ``dzo-image-refresh.timer``, that rebuilds the images
  to pick up security updates and removes the old ones.

.. code-block:: sh

   sudo -iu dayz dzo setup --dry-run   # show what would be done
   sudo -iu dayz dzo setup

Every step reports ``ok``, ``todo`` (what a dry run would do), ``warn`` or
``error``. Running it again changes nothing that is already in place, so run it
after an upgrade too. ``dzo setup --images-only`` rebuilds the images right now
(that is what the timer runs).

Upgrading
---------

``apt install ./dzo_<version>.deb`` (or ``apt upgrade``) replaces ``/usr/bin/dzo`` by renaming, so
running processes keep the old build until they restart. The package then runs ``dzo units sync`` as
``dayz`` (it rewrites the generated units for the new version) and restarts the services that run from
the binary: the exporter, ``dzo serve`` and ``dzo web``, when they are active. **A game server is
never restarted by an upgrade.** It keeps running on the old build (the binary is mounted into its
container) and uses the new units and the new binary at its next restart: the next scheduled restart,
or ``dzo restart <name>``. Settings that did not exist before get their defaults, so no config edit
is needed. Check ``dzo version`` and ``dzo status`` afterwards.

Log in to Steam
---------------

Downloads need a Steam login. It is interactive: you type the password and
confirm Steam Guard (a code by e-mail or from the mobile app, or a confirmation
in the app).

.. code-block:: sh

   sudo -iu dayz dzo steam login --user <steam account>
   sudo -iu dayz dzo steam status

dzo keeps the Steam session, not the password. When Steam ends the session
(password change, new device check, long inactivity), downloads pause and dzo
notifies you. Log in again with the same command. See :doc:`mods-and-updates`.

Next steps
----------

Continue with :doc:`quickstart`.

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

* The ``contrib`` and ``non-free`` components of the Debian archive and the
  ``i386`` architecture: the ``steamcmd`` package is non-free and exists for
  i386 only, and ``dzo`` depends on it (see `Prepare apt`_).

Recommended, not required:

* Mount the btrfs filesystem with ``user_subvol_rm_allowed``. dzo can then delete
  old snapshots instantly. Without it, deletion still works but takes longer.

Prepare apt
-----------

``dzo`` depends on Debian's ``steamcmd:i386``. That package is non-free and only
built for i386, so apt must know both before it can install ``dzo``. As root:

.. code-block:: sh

   dpkg --add-architecture i386
   sed -i /etc/apt/sources.list.d/debian.sources \
       -e 's/^Components: main$/Components: main contrib non-free/'   # or edit your sources by hand
   apt update

The steamcmd package asks you to accept Valve's license when it is installed.
For an unattended install, answer it beforehand:

.. code-block:: sh

   echo steamcmd steam/question select "I AGREE" | debconf-set-selections
   echo steamcmd steam/license note '' | debconf-set-selections

Add the apt repository
----------------------

The package is published in a signed apt repository on GitHub Pages. It is
built from ``main`` for Debian 13 (trixie), amd64. The repository is signed with
a key of its own (``dzo apt repository <bernd@bzed.de>``, fingerprint
``184C DDC4 96A9 C739 D10F  67B0 FE4E E034 4431 4050``); apt only trusts that
key for this one source.

.. code-block:: sh

   install -d -m 0755 /etc/apt/keyrings
   curl -fsSLo /etc/apt/keyrings/dzo-archive-keyring.asc \
       https://bzed.github.io/dayz-server-operator/apt/dzo-archive-keyring.asc
   gpg --show-keys --with-fingerprint /etc/apt/keyrings/dzo-archive-keyring.asc  # compare the fingerprint
   cat >/etc/apt/sources.list.d/dzo.sources <<'EOF'
   Types: deb
   URIs: https://bzed.github.io/dayz-server-operator/apt
   Suites: trixie
   Components: main
   Architectures: amd64
   Signed-By: /etc/apt/keyrings/dzo-archive-keyring.asc
   EOF
   apt update
   apt install dzo

``apt upgrade`` then keeps dzo current. The repository holds the latest build of
``main`` only; its versions are snapshots of the form
``0.1.0-1+git5.abc1234`` (see :ref:`apt-repository`).

Install the package
-------------------

dzo is shipped as a Debian package, ``dzo``, from the repository above or as a
``.deb`` file from the CI artifacts. It pulls in ``podman``, ``passt``,
``git``, ``uidmap``, ``steamcmd:i386`` and the web libraries from Debian (prepare
apt as described above first). The commands that download servers and mods run the
``steamcmd`` they find in the ``PATH`` (``--steamcmd`` picks another program, for
example a wrapper that runs the ``localhost/dzo-steamcmd`` image that ``dzo setup``
builds).

.. code-block:: sh

   apt install ./dzo_<version>_amd64.deb

The package

* creates the user ``dayz`` with the home directory ``/var/lib/dzo``. It gets a free
  UID of 1000 or higher on purpose: journald keeps a journal of its own only for
  regular users, and ``dzo logs`` reads that one. (A system user, UID below 1000,
  may not read the system journal, so ``dzo logs`` would show nothing. An existing
  ``dayz`` user is left as it is; see below.)
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
     logs: ${data}/logs              # archive of rotated profile logs
     cache: ${data}/cache
     secrets: ${data}/secrets
     db: ${data}/db
     site: ${data}/site              # checkout of the site repository

Run the setup
-------------

Run the setup as the service user (it refuses to run as root). Every dzo command does: except the
container's health probes, ``version``, ``help`` and the package's build steps, dzo exits when run as
root. What root creates is owned by root, and the game servers run as the service user and silently
skip a mod directory they cannot read. ``dzo instance render`` fails, so the server does not start, when an
installed mod or one of its files cannot be read by the service user, or when the mod holds nothing to load
(an empty folder from Steam, or an empty or missing ``.pbo``). The setup

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

Journal access of an existing ``dayz`` user
-------------------------------------------

If ``dayz`` already exists as a system user (UID below 1000), ``dzo logs`` and
``journalctl --user`` show nothing, and ``dzo setup`` warns about it. There are two ways out:

* add the user to the ``systemd-journal`` group, as root:
  ``usermod -aG systemd-journal dayz``, and start a new session (``loginctl terminate-user dayz``
  and log in again). This is the small change, but ``dayz`` can then read the whole system journal;
* or recreate the account with a UID of 1000 or higher. Stop the instances first, give
  ``dayz`` the new UID (``usermod -u <new uid> dayz``), and change the owner of ``/var/lib/dzo``
  to it (``chown -R``). On a new host, the package already creates it that way.

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

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
   Signed-By: /etc/apt/keyrings/dzo-archive-keyring.asc
   EOF
   apt update
   apt install dzo

``apt upgrade`` then keeps dzo current. The repository holds the latest build of
``main`` only. Its version is a snapshot on top of the latest release:
``scripts/debian-snapshot.sh`` adds a changelog entry
``<version at the top of debian/changelog>+git<commits since the last tag>.<sha>``
before the build. The top entry of ``debian/changelog`` is always the latest
release; the upcoming one is added when it is cut. ``+`` sorts after that release
and before the next one (``0.1.0-1+git5.abc`` is older than ``0.1.0-2`` and
``0.2.0-1``), and the commit count only grows, so snapshots stay in order. The
commit of a release itself is built as it is.

Maintaining the repository
~~~~~~~~~~~~~~~~~~~~~~~~~~

The ``Docs`` workflow (``.github/workflows/docs.yml``) builds the package in a
trixie container, runs ``scripts/build-apt-repo.sh`` (reprepro, configured in
``apt/conf/distributions``) and publishes the result under ``/apt/`` next to the
documentation. The signing key is the repository secret ``APT_GPG_PRIVATE_KEY``
(an ASCII-armored secret key without a passphrase). The public half is
``apt/dzo-archive-keyring.asc``; the workflow fails if the two do not match.

To replace the key, create one in a throwaway GnuPG home, never in your own
keyring:

.. code-block:: sh

   export GNUPGHOME=$(mktemp -d)
   gpg --batch --passphrase '' --quick-gen-key "dzo apt repository <bernd@bzed.de>" ed25519 sign never
   gpg --armor --export > apt/dzo-archive-keyring.asc
   gpg --armor --export-secret-keys | gh secret set APT_GPG_PRIVATE_KEY
   rm -rf "$GNUPGHOME"

Then commit the new public key and update the fingerprint here and in the
README. Users have to fetch the new key.

Install the package
-------------------

dzo is shipped as a Debian package, ``dzo``, from the repository above or as a
``.deb`` file from the CI artifacts. It pulls in ``podman``, ``passt``,
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

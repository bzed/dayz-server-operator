.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Installation
============

Requirements
------------

* Debian 13 (trixie) or Ubuntu 26.04 LTS or later, or Fedora 43 or later, or
  the RHEL 10 family (RHEL 10, CentOS Stream 10, AlmaLinux 10, Rocky Linux 10),
  x86_64, with podman 5.4 and systemd 257 or newer from the distribution.
  Ubuntu 26.04 LTS is the minimum; 24.04 LTS ships podman 4.9 and systemd 255.
* Linux kernel 6.12 or newer (the trixie kernel, or a trixie-backports kernel).
* A **btrfs** filesystem for the instance data and the snapshots, for the
  snapshot backups. Both must be on the same btrfs filesystem. On the RHEL 10
  family there is no btrfs; see `Fedora 43+ and the RHEL 10 family`_. The
  download cache can live anywhere, but on btrfs copies are nearly free.
* A Steam account that owns DayZ. The DayZ server cannot be downloaded
  anonymously.
* Enough disk space: about 3 GB per server build, plus mods, plus snapshots.

* On Debian and Ubuntu, the ``contrib`` and ``non-free`` components of the
  Debian archive (``multiverse`` on Ubuntu) and the ``i386`` architecture: the
  ``steamcmd`` package is non-free and exists for i386 only, and ``dzo``
  depends on it (see `Prepare apt`_).

Recommended, not required:

* Mount the btrfs filesystem with ``user_subvol_rm_allowed``. dzo can then delete
  old snapshots instantly. Without it, deletion still works but takes longer.

Prepare apt
-----------

``dzo`` depends on ``steamcmd:i386``. That package is non-free and only
built for i386, so apt must know both before it can install ``dzo``. As root, on
Debian:

.. code-block:: sh

   dpkg --add-architecture i386
   sed -i /etc/apt/sources.list.d/debian.sources \
       -e 's/^Components: main$/Components: main contrib non-free/'   # or edit your sources by hand
   apt update

On Ubuntu ``steamcmd`` is in ``multiverse`` instead of Debian's
``contrib``/``non-free``; see `Ubuntu 26.04 LTS and later`_ for the commands.

The steamcmd package asks you to accept Valve's license when it is installed.
For an unattended install, answer it beforehand:

.. code-block:: sh

   echo steamcmd steam/question select "I AGREE" | debconf-set-selections
   echo steamcmd steam/license note '' | debconf-set-selections

Add the apt repository
----------------------

The package is published in a signed apt repository on GitHub Pages. It is
built from ``main`` on Debian 13 (trixie), for Debian 13 and for Ubuntu 26.04 LTS
and later, amd64. The repository is signed with a key of its own (``dzo apt
repository <bernd@bzed.de>``, fingerprint
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
   Suites: stable
   Components: main
   Architectures: amd64
   Signed-By: /etc/apt/keyrings/dzo-archive-keyring.asc
   EOF
   apt update
   apt install dzo

``stable`` is the suite to use: today it names the same package as ``trixie``,
and it keeps its name if the repository ever moves to another codename.
``Suites: trixie`` still works and serves exactly the same package, so an
existing installation needs no change.

``apt upgrade`` then keeps dzo current. The repository holds the latest build of
``main`` only; its versions are snapshots of the form
``0.1.0-1+git5.abc1234`` (see :ref:`apt-repository`).

Ubuntu 26.04 LTS and later
--------------------------

The same package and this repository work on Ubuntu. **26.04 LTS** is the
minimum: earlier releases ship podman and systemd versions older than the 5.4
and 257 that ``dzo`` requires (24.04 LTS: podman 4.9, systemd 255), so apt
refuses the package there. Later Ubuntu releases, interim ones included, work;
everything the web interface needs is compiled into ``dzo``.

Only the preparation differs: ``steamcmd`` is in Ubuntu's ``multiverse``
component. The official sources enable it already (``Components: main
restricted universe multiverse``); the ``sed`` below adds it where they do not.
As root:

.. code-block:: sh

   dpkg --add-architecture i386
   sed -i /etc/apt/sources.list.d/ubuntu.sources \
       -e '/^Components:.*multiverse/! s/^Components: \(.*\)$/Components: \1 multiverse/'  # usually a no-op
   apt update
   echo steamcmd steam/question select "I AGREE" | debconf-set-selections
   echo steamcmd steam/license note '' | debconf-set-selections

   install -d -m 0755 /etc/apt/keyrings
   curl -fsSLo /etc/apt/keyrings/dzo-archive-keyring.asc \
       https://bzed.github.io/dayz-server-operator/apt/dzo-archive-keyring.asc
   gpg --show-keys --with-fingerprint /etc/apt/keyrings/dzo-archive-keyring.asc  # compare the fingerprint
   cat >/etc/apt/sources.list.d/dzo.sources <<'EOF'
   Types: deb
   URIs: https://bzed.github.io/dayz-server-operator/apt
   Suites: stable
   Components: main
   Architectures: amd64
   Signed-By: /etc/apt/keyrings/dzo-archive-keyring.asc
   EOF
   apt update
   apt install dzo

Everything else on this page applies unchanged, including the btrfs filesystem
and ``dzo setup``.

Install the package
-------------------

dzo is shipped as a Debian package, ``dzo``, from the repository above or as a
``.deb`` file from the CI artifacts, and as an rpm for Fedora and the RHEL 10
family (see below). It pulls in ``podman``, ``passt``,
``git``, ``uidmap``, ``geoip-database`` and ``steamcmd:i386`` (prepare apt as described
above first); the web interface's htmx, Leaflet, Fork Awesome and country flags are
compiled in, so it needs no Debian package for them. The commands that download
servers and mods run the ``steamcmd`` they find in the ``PATH``, and
``/usr/libexec/dzo/steamcmd-container`` (steamcmd from the ``localhost/dzo-steamcmd``
image that ``dzo setup`` builds) when there is none; ``--steamcmd`` picks another
program (see below).

.. code-block:: sh

   apt install ./dzo_<version>_amd64.deb

The package

* creates the user ``dayz`` with the home directory ``/var/lib/dzo``. It gets a free
  UID of 1000 or higher on purpose: journald keeps a journal of its own only for
  regular users, and ``dzo logs`` reads that one. (A system user, UID below 1000,
  may not read the system journal, so ``dzo logs`` would show nothing. An existing
  ``dayz`` user is left as it is; see below.)
* assigns subordinate uid/gid ranges to it (needed for rootless podman): the lowest
  free block of 65536 ids at or above 100000 that overlaps no range in
  ``/etc/subuid`` resp. ``/etc/subgid``, so the package installs on a host where
  another rootless podman user already holds 100000-165535. The two files are
  chosen independently, and a range that ``dayz`` already has is kept as it is.
* enables lingering, so the user's services run without a login,
* installs the Containerfiles of the two images in ``/usr/share/dzo/images/``,
  the dzo-admin servermod with its ``compat.yaml`` in
  ``/usr/share/dzo/servermods/``, and example map presets in
  ``/usr/share/dzo/presets/maps/`` (copy the ones you want to
  ``integrations/maps/`` in your site repository).

It does **not** start any game server.

On a development or test host without the package, see :doc:`dev-host` for the
user, the btrfs mount and the other steps by hand.

Fedora 43+ and the RHEL 10 family
---------------------------------

The same package is built as an rpm for Fedora 43 and later and for the RHEL 10
family (RHEL 10, CentOS Stream 10, AlmaLinux 10, Rocky Linux 10), x86_64. CI
installs it on Fedora 43, Fedora 44 and CentOS Stream 10. It is published in a
signed dnf repository next to the apt one; the packages and the repository
metadata are signed with the same key as the apt repository (fingerprint
``184C DDC4 96A9 C739 D10F  67B0 FE4E E034 4431 4050``):

.. code-block:: sh

   curl -fsSLo /etc/yum.repos.d/dzo.repo https://bzed.github.io/dayz-server-operator/rpm/dzo.repo
   dnf install dzo

On Fedora, dnf5's ``dnf config-manager addrepo --from-repofile=<url>`` writes the
same file; the RHEL 10 family ships dnf4, where the ``curl`` line is the way.

dnf asks once to confirm the key's fingerprint (or import it with
``rpm --import https://bzed.github.io/dayz-server-operator/rpm/dzo-repo-keyring.asc``)
and then verifies both the package signatures (``gpgcheck``) and the repository
metadata (``repo_gpgcheck``). The repository holds the latest build of ``main``
only, with the same snapshot versions as the Debian package: the rpm release is
the Debian revision, so ``0.8.2-1`` is older than ``0.8.2-1+git43.abc1234`` and
that is older than ``0.8.2-2`` (see :ref:`apt-repository`).

The rpm carries the same files as the ``.deb`` and creates the same ``dayz``
account: a regular user with a UID of 1000 or higher (rpm's own sysusers.d
handling would allocate a system UID below 1000, so the package creates the
account itself), its subordinate uid/gid range and lingering. As on Debian it
pulls in ``podman``, ``systemd``, ``git``, ``patch``, ``shadow-utils`` and
``passt``; ``btrfs-progs`` and ``geoipupdate`` are recommended
(``geoipupdate`` is in EPEL).

There is no ``steamcmd`` package on Fedora or the RHEL family; dzo runs steamcmd
from the ``localhost/dzo-steamcmd`` image. See `steamcmd on Fedora, RHEL and
Arch`_ below.

SELinux: not verified yet
~~~~~~~~~~~~~~~~~~~~~~~~~

Fedora and the RHEL family enforce SELinux by default, and dzo has **not** been
verified on an enforcing host yet. The game server containers bind-mount the
build, the mods, the profiles and the dzo binary, and the steamcmd wrapper
mounts the download cache and its Steam home without a ``:z`` relabelling, so a
container or the wrapper can hit an AVC denial. If something fails with a
permission error that root does not have, check the audit log:

.. code-block:: sh

   ausearch -m avc -ts recent
   journalctl -t setroubleshoot

Setting SELinux to permissive (``setenforce 0``) only confirms that SELinux is
the cause; it is not a fix. Please report the denials together with the output
of ``dzo status`` and of ``systemctl --user cat <instance>`` (as ``dayz``).

No btrfs on the RHEL 10 family
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

The RHEL 10 kernel has no btrfs, so ``paths.instances`` and ``paths.snapshots``
cannot be on one there. dzo runs, but the btrfs snapshot backups are not
available and ``dzo setup`` warns about it; use the distribution's backup for
the data directory, or back up the instance and site directories another way.
Fedora's default filesystem is btrfs, so the backups work there.

steamcmd on Fedora, RHEL and Arch
---------------------------------

These distributions package no ``steamcmd`` (Debian's non-free ``steamcmd:i386`` is
the only non-free package dzo needs). dzo runs steamcmd from the
``localhost/dzo-steamcmd`` image instead, which ``dzo setup`` builds from the packaged
Containerfile: when ``PATH`` holds no ``steamcmd``, ``--steamcmd`` (``dzo mod add |
update | refresh`` and ``dzo product install | update``) and ``dzo steam login
--command`` default to ``/usr/libexec/dzo/steamcmd-container``. It runs

.. code-block:: sh

   podman run --rm -i [-t] --network host --userns keep-id \
       -v <paths.secrets>/steamcmd-home:/home/steam \
       -v <paths.cache>:<paths.cache> \
       -e HOME=/home/steam localhost/dzo-steamcmd:latest steamcmd <arguments>

with ``-t`` only when its stdin is a terminal, which it is for ``dzo steam login``
(that command attaches a pty for the password and Steam Guard prompts) and is not for
a download job. The container's home is ``<paths.secrets>/steamcmd-home``, created
with mode 0700: steamcmd updates itself there and the Steam session lives there, so
both persist between runs. ``paths.cache`` is mounted at its own path because dzo
hands steamcmd its working directories below it (``+force_install_dir``, product
updates and workshop items). Without the image the wrapper exits with
``dzo-steamcmd image missing: run dzo setup``.

The wrapper asks dzo for both paths with ``dzo config get paths.secrets`` and ``dzo
config get paths.cache`` (see :doc:`cli`), so it follows a ``paths`` configuration
that is not the default. ``DZO`` and ``DZO_CONFIG`` override the dzo binary and the
config file it reads; they are meant for tests.

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

* checks that ``podman`` is version 5.4 or newer (a newer major version, podman
  6 included, is fine) and reports ``ok podman <version>``,
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

``apt install ./dzo_<version>.deb`` (or ``apt upgrade``; ``dnf upgrade dzo`` on Fedora and the RHEL
family) replaces ``/usr/bin/dzo`` by renaming, so
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

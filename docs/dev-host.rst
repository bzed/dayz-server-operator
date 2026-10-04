.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Setting up a host without the package
=====================================

The Debian package does a few things that you have to do yourself on a
development or test host where the package is not installed: the user, the
subordinate uid/gid ranges, lingering, the data directories, the Containerfiles.
This page lists all of them, on a fresh Debian 13 (trixie) host. Everything
here is run as root unless it says otherwise.

Packages
--------

.. code-block:: sh

   apt install podman passt uidmap git btrfs-progs systemd-container \
       build-essential

``btrfs-progs`` is not used by dzo (it talks to the kernel itself) but you need
``mkfs.btrfs`` and ``btrfs subvolume list`` to create and inspect the
filesystem. For building dzo itself see :doc:`development`; the Go toolchain is
the current upstream one, not Debian's.

Backports kernel
----------------

Snapshots and subvolume handling need Linux 6.12 or newer. trixie ships 6.12,
which is enough; use the backports kernel if you want a newer btrfs.

.. code-block:: sh

   cat > /etc/apt/sources.list.d/trixie-backports.sources <<'EOT'
   Types: deb
   URIs: http://deb.debian.org/debian
   Suites: trixie-backports
   Components: main
   Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp
   EOT
   apt update
   apt install -t trixie-backports linux-image-amd64
   reboot
   uname -r        # the new kernel

On a cloud image use ``linux-image-cloud-amd64`` instead. Packages from backports
are only installed when you ask for them with ``-t trixie-backports``, so the
rest of the system stays on trixie.

The service user and group
--------------------------

.. code-block:: sh

   groupadd --system dayz
   useradd --system --gid dayz --home-dir /var/lib/dzo --create-home \
       --shell /bin/bash --comment "DayZ server operator" dayz
   chmod 0750 /var/lib/dzo

(The package uses systemd-sysusers with ``u dayz - "DayZ server operator"
/var/lib/dzo -``; the shell is ``nologin`` there. On a development host a real
shell makes ``sudo -iu dayz`` and ``machinectl shell`` easier.)

Rootless podman needs a range of subordinate ids, and the user's services must
keep running without a login:

.. code-block:: sh

   usermod --add-subuids 100000-165535 --add-subgids 100000-165535 dayz
   grep '^dayz:' /etc/subuid /etc/subgid
   loginctl enable-linger dayz

Check that the ranges do not overlap with another user's in ``/etc/subuid``. To
get a shell with a working user systemd session (``systemctl --user``), use
``machinectl shell dayz@``, or ``sudo -iu dayz`` after enabling lingering and
setting ``XDG_RUNTIME_DIR=/run/user/$(id -u dayz)``.

To work on the host as your own user as well, add yourself to the group
(``usermod -aG dayz <you>``) and make what you need group-readable; do not run
dzo as root, ``dzo setup`` refuses it.

The btrfs filesystem
--------------------

Instance data and snapshots must be on **one** btrfs filesystem. Pick one of
these.

A whole disk (here a second disk ``/dev/vdb``; check with ``lsblk`` first, the
next command destroys what is on it):

.. code-block:: sh

   mkfs.btrfs -L dzo /dev/vdb

A logical volume, if you use LVM:

.. code-block:: sh

   lvcreate --name dzo --size 100G vg0
   mkfs.btrfs -L dzo /dev/vg0/dzo

A file on an existing filesystem is enough for a throw-away test:

.. code-block:: sh

   truncate -s 40G /var/lib/dzo-btrfs.img
   mkfs.btrfs -L dzo /var/lib/dzo-btrfs.img
   # mount with -o loop,... instead of the fstab line below

Mount it on the data directory (the default is ``/var/lib/dzo``; the user's home
is on it, which is fine) with the options dzo wants:

.. code-block:: sh

   echo 'LABEL=dzo /var/lib/dzo btrfs noatime,compress=zstd:1,user_subvol_rm_allowed 0 0' >> /etc/fstab
   systemctl daemon-reload
   mount /var/lib/dzo
   chown dayz:dayz /var/lib/dzo
   chmod 0750 /var/lib/dzo
   findmnt -no FSTYPE,OPTIONS /var/lib/dzo

If ``/var/lib/dzo`` already contains files (the home directory with a profile),
mount on a temporary directory, copy them over with ``cp -a`` and mount again.

What the users need for snapshots
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

dzo creates, snapshots and deletes subvolumes as the unprivileged ``dayz`` user
with the btrfs ioctls, so no sudo rule or helper is needed. What it relies on:

* The user owns ``paths.instances`` and ``paths.snapshots`` (the ``chown``
  above, or the directories ``dzo setup`` creates below an owned data
  directory). Creating a subvolume or snapshot only needs write access to the
  directory it goes into.
* ``user_subvol_rm_allowed`` in the mount options. Without it only the owner's
  ``rmdir`` of an empty subvolume works, so dzo deletes a snapshot file by file,
  which is slow for a big instance. Enable it without a reboot with
  ``mount -o remount,user_subvol_rm_allowed /var/lib/dzo``.
* Both directories on the same filesystem, because a snapshot cannot cross
  filesystems. If you moved ``paths.snapshots`` elsewhere, ``dzo backup`` says
  so.

Do not use ``noexec`` or ``nosuid`` options that stop the game server binary from
running if you keep server builds on this filesystem. Quotas are not needed;
leave them off, they slow snapshot deletion.

Data directories and configuration
----------------------------------

.. code-block:: sh

   install -d -o dayz -g dayz -m 0750 /run/dzo /run/dzo/status
   install -d /etc/dzo

``/run`` is a tmpfs, so these two directories vanish at reboot. The package
recreates them with a tmpfiles.d snippet; do the same. ``/var/lib/dzo`` is the
user's home and persistent, so it is not part of the snippet (the ``useradd
--create-home`` above created it):

.. code-block:: sh

   cat > /etc/tmpfiles.d/dzo.conf <<'EOT'
   d /run/dzo 0750 dayz dayz -
   d /run/dzo/status 0750 dayz dayz -
   EOT

Write ``/etc/dzo/config.yaml`` as described in :doc:`installation` and
:doc:`configuration` (``site.remote`` points at your site repository; use a
placeholder-free path of your own, or a local git directory for tests).

The dzo binary and its files
----------------------------

From a checkout of this repository, as your own user:

.. code-block:: sh

   make build
   make servermods          # needs the submodules

and as root:

.. code-block:: sh

   install -m 0755 bin/dzo /usr/local/bin/dzo
   install -d /usr/share/dzo
   cp -a images /usr/share/dzo/images
   cp -a dist/servermods /usr/share/dzo/servermods

``dzo setup --images-dir <dir>`` takes another directory for the Containerfiles,
so you can leave them in the checkout. The ``dzo-*`` timers and quadlets that
dzo writes call the binary by the path it is run from; a ``go run`` or test
binary in a build cache is replaced by ``/usr/bin/dzo``, so on a test host keep
the binary in ``/usr/bin`` or ``/usr/local/bin`` and link it:
``ln -s /usr/local/bin/dzo /usr/bin/dzo``.

Check and continue
------------------

.. code-block:: sh

   sudo -iu dayz podman info --format '{{.Host.Security.Rootless}}'   # true
   sudo -iu dayz dzo setup --dry-run --images-dir /usr/share/dzo/images
   sudo -iu dayz dzo setup --images-dir /usr/share/dzo/images

Then log in to Steam and continue as in :doc:`installation` and
:doc:`quickstart`.

Testing in a virtual machine
----------------------------

A Debian 13 cloud image under libvirt is a convenient test host. Create it with
``virt-install --osinfo debian13 --boot uefi`` (the image does not boot with
BIOS), attach a second disk for the btrfs filesystem and bridge it to your
network, then follow this page. A game server only accepts BattlEye connections
from real players if the VM is reachable from the internet; local tests (boot
test, A2S query, RCon) work from the host network alone.

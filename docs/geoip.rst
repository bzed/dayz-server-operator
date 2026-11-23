.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

GeoIP: flags and player locations
=================================

``dzo serve`` looks up the address a player connects from in a GeoIP database. The :doc:`web interface <web>`
then shows

* a **country flag** before each name in the player list, for everyone who can see the list, and
* in the player info (hover over the name, or tap its ``i`` button) the **country**, and with the ``player.ip``
  permission also the **city**, the approximate **location** and the **provider** (autonomous system).

The location is where the database puts the address, usually the middle of a city or the seat of the provider.
It is not where the player lives, and VPNs and mobile networks make it wrong. Private and local addresses are
never looked up. The address itself stays behind the ``player.ip`` permission as before (:doc:`moderation`).

Without a database nothing breaks: the list shows no flags and the info no location.

dzo looks for two kinds of database, which are different file formats. Install one of them; what a plain
install of dzo gets you depends on the package family.

GeoLite2 with geoipupdate (recommended for more detail)
--------------------------------------------------------

MaxMind's GeoLite2 City, Country and ASN databases (``*.mmdb``) give the country, city, location and provider.
``geoipupdate`` keeps them current. Every dzo package recommends it - the ``.deb`` and the rpm as a
``Recommends``, the Arch package as an optional dependency (``optdepends``). No package pulls in the
databases themselves: without ``geoipupdate`` and its account there are no GeoLite2 files.

#. Create a **free MaxMind account** at https://www.maxmind.com/en/geolite2/signup and accept the GeoLite2 licence.
   Create a licence key in the account portal and note the account ID.
#. Install the updater and configure it. ``/etc/GeoIP.conf`` needs your account ID, the licence key and the
   editions dzo reads:

   .. code-block:: console

      # apt install geoipupdate    # Debian (contrib) and Ubuntu (multiverse)
      # dnf install geoipupdate    # Fedora; on the RHEL 10 family check whether your repositories have it
      # pacman -S geoipupdate      # Arch (extra)

   .. code-block:: text

      AccountID 123456
      LicenseKey xxxxxxxxxxxxxxxx
      EditionIDs GeoLite2-City GeoLite2-Country GeoLite2-ASN

#. Run the update once by hand and check that it works:

   .. code-block:: console

      # systemctl start geoipupdate.service
      # journalctl -u geoipupdate.service
      # ls -l /var/lib/GeoIP

   The databases appear in ``/var/lib/GeoIP``, the default of ``serve.geoip_dir``. ``geoipupdate.timer`` then
   keeps them current.

dzo reopens the files when they change (it checks once a minute), so neither ``dzo serve`` nor the game servers
need a restart after an update.

The legacy database: geoip-database
------------------------------------

The old format is ``/usr/share/GeoIP/GeoIP.dat`` (IPv4) and ``GeoIPv6.dat`` (IPv6), the default of
``serve.geoip_legacy_dir``. dzo uses these files whenever the GeoLite2 databases are missing or have no answer.
**They hold the country only**: you get the flags and the country in the info, but no city, location or
provider.

Which package brings them differs:

* The ``.deb`` **depends on** Debian's ``geoip-database``, so on Debian and Ubuntu country flags work after a
  plain install with nothing to configure. It is also the choice for installations that cannot or do not want
  to use a MaxMind account.
* The rpm and the Arch package ship no legacy database and do not depend on one. Fedora and the RHEL 10 family
  package no ``geoip-database``; there ``geoipupdate`` is what the rpm recommends. Arch does package one,
  ``geoip-database`` in ``extra`` with the same two files, but the dzo package does not pull it in:
  ``pacman -S geoip-database`` installs it.

The data is only as new as the package, and no job refreshes it between package updates. Debian 13's
``geoip-database`` was reverted to data from 2019 (the version is ``20250401+really20191224``); Ubuntu 26.04
ships a current data set (``20260113``). For current results on Debian 13 **apt needs
configuration**: take ``geoip-database`` from **trixie-backports**. Enable backports and pin the package so
that apt always takes it from there and upgrades it with the system:

.. code-block:: console

   # cat >/etc/apt/sources.list.d/backports.sources <<'EOF'
   Types: deb
   URIs: http://deb.debian.org/debian
   Suites: trixie-backports
   Components: main
   Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp
   EOF
   # cat >/etc/apt/preferences.d/geoip-database <<'EOF'
   Package: geoip-database
   Pin: release a=trixie-backports
   Pin-Priority: 900
   EOF
   # apt update
   # apt install geoip-database
   # apt-cache policy geoip-database     # the candidate must come from trixie-backports

Backports are not installed by default (apt gives them priority 100). The pin raises it for this one package, so
``apt upgrade`` picks up every new data release. On Ubuntu, ``geoipupdate`` is the way to data newer than the
release's package.

Settings
--------

You can change the directories in the :doc:`configuration <configuration>`:

.. code-block:: yaml

   serve:                                # excerpt of config.yaml, all keys: see Configuration
     geoip_dir: /var/lib/GeoIP           # GeoLite2-City, -Country and -ASN (.mmdb)
     geoip_legacy_dir: /usr/share/GeoIP  # GeoIP.dat and GeoIPv6.dat
   web:
     flags_dir: ""   # default: none (the built-in flags)

Which database answers
----------------------

For each address dzo asks, in this order:

#. ``GeoLite2-City.mmdb``: country, city and location; else ``GeoLite2-Country.mmdb``: country.
#. ``GeoIP.dat`` or ``GeoIPv6.dat``: country, when the GeoLite2 databases did not give one.
#. ``GeoLite2-ASN.mmdb``: the provider, independent of the above.

Flags
-----

The flags are built into dzo, from the ``iso-country-flags-svg-collection`` in the 4:3 shape, and the
web interface serves them under ``/flags/``. A directory in ``web.flags_dir`` overrides them file by
file. A flag it lacks comes from the built-in set, and a missing directory only logs a warning at the
start of ``dzo web``. Without a flag for a country, for example for a code outside ISO 3166-1, the list
shows the country code where the flag would be.

GeoLite2 is a product of MaxMind. Use of the databases is bound by MaxMind's GeoLite2 end-user licence.

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

dzo looks for two kinds of database, which are different file formats. Install one of them.

GeoLite2 with geoipupdate (recommended for more detail)
--------------------------------------------------------

MaxMind's GeoLite2 City, Country and ASN databases (``*.mmdb``) give the country, city, location and provider.
They are kept up to date by ``geoipupdate``, which the dzo package recommends (it is in Debian's ``contrib``
component).

#. Create a **free MaxMind account** at https://www.maxmind.com/en/geolite2/signup and accept the GeoLite2 licence.
   Create a licence key in the account portal and note the account ID.
#. Install the updater and configure it. ``/etc/GeoIP.conf`` needs your account ID, the licence key and the
   editions dzo reads:

   .. code-block:: console

      # apt install geoipupdate

   .. code-block:: text

      AccountID 123456
      LicenseKey xxxxxxxxxxxxxxxx
      EditionIDs GeoLite2-City GeoLite2-Country GeoLite2-ASN

#. Run the update once by hand and check that it works:

   .. code-block:: console

      # systemctl start geoipupdate.service
      # journalctl -u geoipupdate.service
      # ls -l /var/lib/GeoIP

   The databases appear in ``/var/lib/GeoIP``. ``geoipupdate.timer`` then keeps them current.

dzo reopens the files when they change (it checks once a minute), so neither ``dzo serve`` nor the game servers
need a restart after an update.

The default: geoip-database
---------------------------

The dzo package **depends on** Debian's ``geoip-database``, so country flags work after a plain install with
nothing to configure. It is also the choice for installations that cannot or do not want to use a MaxMind
account. It ships the old-format ``/usr/share/GeoIP/GeoIP.dat`` (IPv4) and ``GeoIPv6.dat`` (IPv6). dzo uses them whenever the GeoLite2
databases are missing or have no answer. **These files hold the country only**: you get the flags and the country in the
info, but no city, location or provider.

The package that comes with trixie itself is years behind (its data is from 2019), and unlike ``geoipupdate``
there is no job that refreshes the data: the data is only as new as the package. For current results
**some apt configuration is needed**: take ``geoip-database`` from **trixie-backports**. Enable backports and
pin the package so that it is always taken from there and upgraded with the system:

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
``apt upgrade`` picks up every new data release.

Settings
--------

The directories can be changed in the :doc:`configuration <configuration>`:

.. code-block:: yaml

   serve:
     geoip_dir: /var/lib/GeoIP           # GeoLite2-City, -Country and -ASN (.mmdb)
     geoip_legacy_dir: /usr/share/GeoIP  # GeoIP.dat and GeoIPv6.dat
   web:
     flags_dir: /usr/share/iso-flags-svg/country-4x3

Which database answers
----------------------

For each address dzo asks, in this order:

#. ``GeoLite2-City.mmdb``: country, city and location; else ``GeoLite2-Country.mmdb``: country.
#. ``GeoIP.dat`` or ``GeoIPv6.dat``: country, when the GeoLite2 databases did not give one.
#. ``GeoLite2-ASN.mmdb``: the provider, independent of the above.

Flags
-----

The flags are the SVG files of Debian's ``iso-flags-svg`` package (a dependency of dzo, in the 4:3 shape),
which the web interface serves under ``/flags/``. If the directory (``web.flags_dir``) is missing, for example on
a host that runs ``dzo web`` from a build without the package, the list shows the country code where the flag
would be.

GeoLite2 is a product of MaxMind. Use of the databases is bound by MaxMind's GeoLite2 end-user licence.

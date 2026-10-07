#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Build the signed apt repository (reprepro) from the .deb files in a directory.
#
#   scripts/build-apt-repo.sh <deb-dir> <out-dir>
#
# The repository is rebuilt from scratch: <out-dir> is replaced. It is signed
# with the only secret key in GNUPGHOME (CI imports it from the APT_GPG_PRIVATE_KEY
# secret into a temporary home; locally point GNUPGHOME at a throwaway directory,
# never at your own keyring). Needs reprepro and gpg.
set -eu

[ $# -eq 2 ] || { echo "usage: $0 <deb-dir> <out-dir>" >&2; exit 2; }
debs=$1
out=$2
here=$(cd "$(dirname "$0")/.." && pwd)

[ -n "${GNUPGHOME:-}" ] || { echo "GNUPGHOME must point at a keyring with the signing key" >&2; exit 2; }
[ "$(gpg --list-secret-keys --with-colons | grep -c '^sec')" -eq 1 ] ||
	{ echo "$GNUPGHOME must hold exactly one secret key" >&2; exit 2; }
ls "$debs"/*.deb >/dev/null

rm -rf "$out"
mkdir -p "$out"
base=$(mktemp -d)
trap 'rm -rf "$base"' EXIT
cp -r "$here/apt/conf" "$base/conf"

codename=$(awk '/^Codename:/{print $2}' "$base/conf/distributions")
for deb in "$debs"/*.deb; do
	reprepro --silent --basedir "$base" --outdir "$out" includedeb "$codename" "$deb"
done
# only the published tree: no database, no config
rm -rf "$out/db" "$out/conf"

# the public key, armored (for humans) and binary (for signed-by=)
gpg --armor --export >"$out/dzo-archive-keyring.asc"
gpg --export >"$out/dzo-archive-keyring.gpg"

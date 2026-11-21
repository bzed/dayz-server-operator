#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Build the signed dnf repository from the .rpm files in a directory.
#
#   scripts/build-rpm-repo.sh <rpm-dir> <out-dir>
#
# The repository is rebuilt from scratch: <out-dir> is replaced. Every package is
# signed with the only secret key in GNUPGHOME (CI imports it from the
# APT_GPG_PRIVATE_KEY secret into a temporary home; locally point GNUPGHOME at a
# throwaway directory, never at your own keyring). The repository metadata is
# signed too, for "repo_gpgcheck=1". Needs rpm-sign, createrepo_c and gpg.
#
# The key is the same one that signs the apt repository: rpm --import accepts
# its EdDSA key on Fedora 43/44 and CentOS Stream 10 (verified in CI's rpm-install
# and locally), so users trust one dzo key for both repositories.
set -eu

[ $# -eq 2 ] || { echo "usage: $0 <rpm-dir> <out-dir>" >&2; exit 2; }
rpms=$1
out=$2
here=$(cd "$(dirname "$0")/.." && pwd)

[ -n "${GNUPGHOME:-}" ] || { echo "GNUPGHOME must point at a keyring with the signing key" >&2; exit 2; }
[ "$(gpg --list-secret-keys --with-colons | grep -c '^sec')" -eq 1 ] ||
	{ echo "$GNUPGHOME must hold exactly one secret key" >&2; exit 2; }
ls "$rpms"/*.rpm >/dev/null

rm -rf "$out"
mkdir -p "$out"
cp "$rpms"/*.rpm "$out"/

# rpmsign signs with %_gpg_name; take it from the only key in GNUPGHOME.
keyid=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/{print $10; exit}')
for rpm in "$out"/*.rpm; do
	rpmsign --addsign --define "_gpg_name $keyid" "$rpm"
done

createrepo_c --quiet "$out"
# dnf's repo_gpgcheck reads repodata/repomd.xml.asc; sign after createrepo_c,
# which writes repomd.xml (and would overwrite the signature if run again).
gpg --batch --yes --detach-sign --armor --output "$out/repodata/repomd.xml.asc" "$out/repodata/repomd.xml"

# the public key (armored, for rpm --import and gpgkey=) and the repository file
gpg --armor --export >"$out/dzo-repo-keyring.asc"
cp "$here/rpm/dzo.repo" "$out/dzo.repo"

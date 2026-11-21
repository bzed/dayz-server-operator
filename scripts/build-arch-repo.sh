#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Build the signed pacman repository from the .pkg.tar.zst files in a directory.
#
#   scripts/build-arch-repo.sh <pkg-dir> <out-dir>
#
# The repository is rebuilt from scratch: <out-dir> is replaced. Every package
# gets a detached signature and the database is signed with "repo-add --sign",
# both with the only secret key in GNUPGHOME (CI imports it from the
# APT_GPG_PRIVATE_KEY secret into a temporary home; locally point GNUPGHOME at a
# throwaway directory, never at your own keyring). Needs repo-add (the pacman
# package) and gpg.
#
# The key is the same one that signs the apt and dnf repositories: pacman
# imports the published keyring with "pacman-key --add" and trusts it with
# "pacman-key --lsign-key", so a user trusts one dzo key for all three.
set -eu

[ $# -eq 2 ] || { echo "usage: $0 <pkg-dir> <out-dir>" >&2; exit 2; }
pkgs=$1
out=$2
here=$(cd "$(dirname "$0")/.." && pwd)

[ -n "${GNUPGHOME:-}" ] || { echo "GNUPGHOME must point at a keyring with the signing key" >&2; exit 2; }
[ "$(gpg --list-secret-keys --with-colons | grep -c '^sec')" -eq 1 ] ||
	{ echo "$GNUPGHOME must hold exactly one secret key" >&2; exit 2; }
ls "$pkgs"/*.pkg.tar.zst >/dev/null

rm -rf "$out"
mkdir -p "$out"
cp "$pkgs"/*.pkg.tar.zst "$out"/

# One detached signature per package; pacman asks for <package>.sig next to the
# package file and verifies it against the local keyring.
keyid=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/{print $10; exit}')
for pkg in "$out"/*.pkg.tar.zst; do
	gpg --batch --yes --local-user "$keyid" --detach-sign --output "$pkg.sig" "$pkg"
done

# repo-add signs the database (dzo.db.tar.gz.sig) and creates dzo.db and
# dzo.db.sig as links to it. GitHub Pages serves the site from a static tree and
# a symlink or a cross-file hardlink does not survive that reliably, so the
# database and its signature are published as regular files under the names
# pacman asks for. libalpm detects the compression of the database itself, so the
# .tar.gz bytes under the dzo.db name are what it expects. The .tar.gz files are
# left out: nothing fetches them.
repo-add --sign --quiet "$out/dzo.db.tar.gz" "$out"/*.pkg.tar.zst
rm -f "$out/dzo.db" "$out/dzo.db.sig"
cp "$out/dzo.db.tar.gz" "$out/dzo.db"
cp "$out/dzo.db.tar.gz.sig" "$out/dzo.db.sig"
# repo-add writes a files database too (for pacman -F). Nothing fetches it, and
# its own symlinks and signatures are more of the same, so it is left out.
rm -f "$out/dzo.files" "$out/dzo.files.sig" "$out/dzo.files.tar.gz" "$out/dzo.files.tar.gz.sig"
rm -f "$out/dzo.db.tar.gz" "$out/dzo.db.tar.gz.sig"

# the public key (for pacman-key --add) and the repository configuration users
# include from /etc/pacman.conf
gpg --armor --export >"$out/dzo-repo-keyring.asc"
cp "$here/arch/dzo.conf" "$out/dzo.conf"

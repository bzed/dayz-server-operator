#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Download the pinned web files the web interface serves (htmx, Leaflet and
# Fork Awesome) into internal/web/vendor and verify them against the
# SHA256SUMS committed next to them. Nothing is downloaded during a build:
# this script only changes the tree, and a changed file shows up as a git
# diff.
#
#   scripts/vendor-web.sh            download and check against SHA256SUMS
#   scripts/vendor-web.sh --update   download and rewrite SHA256SUMS
#
# The versions are Debian trixie's (htmx 2.0.4, Leaflet 1.7.1, Fork Awesome
# 1.2.0); a later patch release of the same major version needs a new pin
# here and a run with --update.
#
# The country flags are not vendored. Debian's iso-flags-svg (built from
# https://github.com/linuxmint/iso-country-flags-svg-collection) is
# public domain under the Creative Commons Public Domain Dedication, which
# the `licenses` allow-list in the Makefile does not cover, so web.flags_dir
# keeps serving them from the installed iso-flags-svg.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
vendor="$root/internal/web/vendor"

usage() {
	echo "usage: $0 [--update]" >&2
	exit 2
}

update=0
case "${1:-}" in
"") ;;
--update) update=1 ;;
*) usage ;;
esac
[ $# -le 1 ] || usage

htmx_version=2.0.4
leaflet_version=1.7.1
fork_awesome_version=1.2.0

# The sha256 of the release archives themselves, so that --update cannot
# record a tampered or replaced download either.
htmx_sha256=a34988d1b9f005a458d593aa8d7486537cb878b8ac3b82703db1268983123ecc
leaflet_sha256=43aca726165904ff73a34571c263b561cec94e6b9992a4414a600f3e984d1b03
fork_awesome_sha256=90d571a643f0c80172a80468cf56555811b210a90130ceb3abbe51756deb2d65

work=$(mktemp -d "${TMPDIR:-/var/tmp}/dzo-vendor-XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

fetch() { # fetch <url> <sha256> <file>
	if ! curl -fsSL -o "$3" "$1"; then
		echo "vendor-web: cannot download $1" >&2
		exit 1
	fi
	if ! printf '%s  %s\n' "$2" "$3" | sha256sum -c --status -; then
		echo "vendor-web: $1 is not the pinned release, its sha256 differs" >&2
		exit 1
	fi
}

fetch "https://registry.npmjs.org/htmx.org/-/htmx.org-$htmx_version.tgz" "$htmx_sha256" "$work/htmx.tgz"
fetch "https://registry.npmjs.org/leaflet/-/leaflet-$leaflet_version.tgz" "$leaflet_sha256" "$work/leaflet.tgz"
fetch "https://registry.npmjs.org/fork-awesome/-/fork-awesome-$fork_awesome_version.tgz" "$fork_awesome_sha256" "$work/fork-awesome.tgz"

# Only the files the web interface references: the browser builds, the
# Leaflet marker images and the Fork Awesome CSS and web fonts. The sources,
# the minified variants and the licence files of the tarballs stay out; their
# licences live under LICENSES/ instead.
mkdir -p "$work/htmx" "$work/leaflet/images" "$work/fork-awesome/css" "$work/fork-awesome/fonts"
tar -xzf "$work/htmx.tgz" -C "$work/htmx" --strip-components=2 package/dist/htmx.min.js
tar -xzf "$work/leaflet.tgz" -C "$work/leaflet" --strip-components=2 package/dist/leaflet.js package/dist/leaflet.css
tar -xzf "$work/leaflet.tgz" -C "$work/leaflet/images" --strip-components=3 --wildcards 'package/dist/images/*'
tar -xzf "$work/fork-awesome.tgz" -C "$work/fork-awesome/css" --strip-components=2 package/css/fork-awesome.css
tar -xzf "$work/fork-awesome.tgz" -C "$work/fork-awesome/fonts" --strip-components=2 --wildcards 'package/fonts/*'

# Swap the trees in only once the downloads are known to be good. flags/ is
# not touched: this script does not vendor it (see above).
rm -rf "$vendor/htmx" "$vendor/leaflet" "$vendor/fork-awesome"
mkdir -p "$vendor"
cp -a "$work/htmx" "$work/leaflet" "$work/fork-awesome" "$vendor/"

sums="$vendor/SHA256SUMS"
if [ "$update" -eq 1 ]; then
	{
		echo "# Checksums of the vendored web files, written by scripts/vendor-web.sh; sha256sum -c"
		echo "# ignores the # lines. htmx $htmx_version, Leaflet $leaflet_version and Fork Awesome $fork_awesome_version."
		echo '# Rewrite after a version bump with: scripts/vendor-web.sh --update'
	} >"$sums"
	(
		cd "$vendor"
		find . -type f ! -name SHA256SUMS -print | sed 's|^\./||' | LC_ALL=C sort |
			while IFS= read -r file; do sha256sum "$file"; done
	) >>"$sums"
	echo "vendor-web: wrote $sums"
else
	(cd "$vendor" && sha256sum -c SHA256SUMS)
fi

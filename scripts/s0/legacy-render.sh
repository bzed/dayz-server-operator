#!/bin/bash
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Spike S0: render a mission with the legacy dayzdockerserver code.
#
#   legacy-render.sh <legacy repo> <branch> <CE repo> <out dir>
#
# Runs the legacy xml.sh and the mergexml function of server/bin/dz unchanged, in a Debian
# container with the tools they need (xmlstarlet, xmlmerge, jq, patch), on the files of one
# branch, with every mod of the branch "active" and the Central Economy repository as the
# pristine mission. <out dir>/mission is the merged mission, <out dir>/log what the scripts said.
# Needs podman and network access (xml.env entries with a URL are downloaded).
set -euo pipefail
repo=$(realpath "$1"); branch=$2; ce=$(realpath "$3"); out=$(realpath -m "$4")
rm -rf "$out"; mkdir -p "$out/legacy"
git -C "$repo" archive "$branch" files server config | tar -x -C "$out/legacy"

cat > "$out/run.sh" <<'INNER'
#!/bin/bash
set -uo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get -qq update >/dev/null && apt-get -qq install -y xmlstarlet gwenhywfar-tools jq patch libxml2-utils curl >/dev/null
ln -sfn /work/legacy/files /files
ln -sfn /ce /mpmissions
mkdir -p /serverfiles/keys /serverfiles/mpmissions /profiles/battleye /mods/221100 /tmp
touch /serverfiles/keys/dayz.bikey
ln -sfn /files/custom /profiles/custom
export FILES=/files SERVER_FILES=/serverfiles SERVER_PROFILE=/profiles WORKSHOP_DIR=/mods/221100
green= default= yellow= red=
MAP=$(grep -E "template=" /files/serverDZ.cfg | grep -vE "^//" | cut -d= -f2 | cut -d\; -f1 | tr -d '" ')
MPMISSIONS=/serverfiles/mpmissions
echo "map: $MAP"
get_mod_name() { echo "$1"; }
# every mod of the branch is active: files/mods/@Name -> <id>
for l in /files/mods/@*; do
  [ -L "$l" ] || continue
  id=$(basename "$(readlink "$l")")
  mkdir -p "/mods/221100/$id"
  ln -sfn "/mods/221100/$id" "/profiles/$(basename "$l")"
done
for d in /files/mods/[0-9]*; do
  [ -d "$d" ] || continue
  id=$(basename "$d")
  mkdir -p "/mods/221100/$id"
  bash /files/bin/xml.sh "$id" 2>&1 | sed "s/^/xml.sh $id: /"
done
cp -a "/mpmissions/$MAP" "$MPMISSIONS/"
eval "$(sed -n '/^mergexml(){/,/^}/p' /work/legacy/server/bin/dz)"
set +e
mergexml
mkdir -p /work/mission && cp -a "$MPMISSIONS/$MAP/." /work/mission/
echo "$MAP" > /work/map
INNER
chmod +x "$out/run.sh"
podman run --rm -v "$out:/work:Z" -v "$ce:/ce:ro,Z" docker.io/library/debian:trixie /work/run.sh > "$out/log" 2>&1 || { tail -30 "$out/log"; exit 1; }
echo "legacy mission: $out/mission ($(cat "$out/map")), log: $out/log"

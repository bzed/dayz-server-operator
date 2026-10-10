#!/bin/bash
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests the dzo-admin actions that need a connected player, with a real DayZ client: a local server
# with dzo-admin (and the probe servermod testmods/dzo-client-probe), a real `dzo serve`, and the
# DayZ client started headless (the dayz-dev skill's scripts). Checks, through `dzo player|vehicle`:
#   players state, message (three styles), teleport, refusal while in a vehicle, spawn_item into the
#   inventory, the hands and the ground (with a class watch rule on the item), an unknown class, and
#   the crew of a vehicle.
# Needs, on a machine that does not run dzo instances: the Steam DayZ Server tool (stable 223350, or
# --exp 1042420), the DayZ client of the same build, a running logged-in Steam, sway, and the dayz-dev
# skill's scripts (DAYZ_DEV_SCRIPTS, default ~/.claude/skills/dayz-dev/scripts). One game at a time.
# Usage: scripts/test-client.sh [--exp] [--keep]
set -euo pipefail
cd "$(dirname "$0")/.."
dev=${DAYZ_DEV_SCRIPTS:-$HOME/.claude/skills/dayz-dev/scripts}
exp=() keep=0 product=stable
for a in "$@"; do
	case $a in
	--exp) exp=(-e) product=experimental ;;
	--keep) keep=1 ;;
	*) echo "usage: $0 [--exp] [--keep]" >&2; exit 2 ;;
	esac
done
[ -x "$dev/dayz-client-headless.sh" ] || { echo "dayz-dev scripts not found in $dev (DAYZ_DEV_SCRIPTS)" >&2; exit 2; }
"$dev/dayz-client-headless.sh" status >&2 || { echo "a DayZ client is running; one game at a time" >&2; exit 2; }

work=$(mktemp -d /tmp/dzcl.XXXXXX)
tree=$work/tree
pids=()
cleanup() {
	"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true
	for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
	sleep 5
	for p in "${pids[@]}"; do kill -9 "$p" 2>/dev/null || true; done
	if [ "$keep" = 1 ]; then echo "kept $work"; else rm -rf "$work"; fi
}
trap cleanup EXIT INT TERM

fail=0 pass=0
ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1"; fail=$((fail + 1)); }
# verdict <name> <status>: PASS when the status is 0.
verdict() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }
waitfor() { timeout "$1" bash -c "until $2; do sleep 3; done"; }

go build -o "$work/dzo" ./cmd/dzo
"$work/dzo" servermods build --src servermods --out "$work/built" >/dev/null
"$work/dzo" servermods build --src testmods --out "$work/tbuilt" >/dev/null

srv=$("$dev/find-dayzserver.sh" "${exp[@]}")
"$dev/make-server-tree.sh" "$tree" "$srv" >/dev/null
set -- $("$dev/free-ports.sh" 4)
game=$1 query=$2 api=$3 mod=$4
cfg=$tree/serverDZ.cfg
grep -q '^steamQueryPort' "$cfg" || sed -i '1i steamQueryPort = 0;' "$cfg"
sed -i "s/^steamQueryPort = .*/steamQueryPort = $query;/" "$cfg"
grep -q '^BattlEye' "$cfg" || sed -i '1i BattlEye = 0;' "$cfg" # the headless client has no BattlEye
sed -i 's/^BattlEye = .*/BattlEye = 0;/; s/^password = .*/password = "";/' "$cfg"

# The operator side: a config, a one-instance site, the instance's mod token and an API token.
mkdir -p "$work/site/instances/t1" "$work/secrets/admin" "$tree/profiles/dzo-admin"
cp -r scripts/findfile-site/integrations "$work/site/"
echo 'local_mods: {}' >"$work/site/site.yaml"
cat >"$work/site/instances/t1/instance.yaml" <<Y
name: t1
product: dayz-$product
map: dayzOffline.chernarusplus
mission_source: {preset: vanilla-chernarusplus}
ports: {game: $game, rcon: $((query + 1)), query: $query}
network: host
Y
cat >"$work/config.yaml" <<Y
paths: {data: $work/data, cache: $work/cache, site: $work/site, secrets: $work/secrets}
serve: {installation: t, listen: "127.0.0.1:$api", mod_listen: "127.0.0.1:$mod"}
Y
tok=$(head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n')
echo "$tok" >"$work/secrets/admin/t1.token"
echo "{\"version\":1,\"endpoint\":\"http://127.0.0.1:$mod/\",\"token\":\"$tok\",\"sync_ms\":500,\"players_s\":2,\"vehicles_s\":5,\"markers_s\":10,\"events_s\":5,\"allow_spawn\":true,\"watch\":[{\"layer\":\"lights\",\"classes\":[\"Chemlight_Red\"],\"icon\":\"flare\",\"label\":\"watched chemlight\",\"max\":20}]}" >"$tree/profiles/dzo-admin/config.json"
chmod 600 "$work/secrets/admin/t1.token" "$tree/profiles/dzo-admin/config.json"
"$work/dzo" token create tester --role admin --config "$work/config.yaml" >"$work/api.token"
chmod 600 "$work/api.token"
dzo() { "$work/dzo" "$@" --config "$work/config.yaml" --token-file "$work/api.token"; }

"$work/dzo" serve --config "$work/config.yaml" >"$work/serve.log" 2>&1 &
pids+=($!)
ln -sfn "$work/built/dzo-admin" "$tree/@dzo-admin"
ln -sfn "$work/tbuilt/dzo-client-probe" "$tree/@dzo-client-probe"
(cd "$tree" && ulimit -c 0 && exec ./DayZServer -config=serverDZ.cfg -profiles=profiles \
	"-servermod=@dzo-admin;@dzo-client-probe" -port="$game" -nosplash -nopause -dologs -adminlog) >"$work/server.log" 2>&1 &
server=$!
pids+=("$server")
echo "== $product server $srv, game port $game, work dir $work"
until grep -q 'CREATED -> CONNECTED' "$work/server.log" 2>/dev/null; do
	sleep 2
	kill -0 "$server" 2>/dev/null || { echo "server died" >&2; tail -20 "$work/server.log" >&2; exit 2; }
done
sleep 8
script=$(ls "$tree"/profiles/script_*.log | tail -1)
adm=$(ls "$tree"/profiles/*.ADM | tail -1)
# The experimental server logs 'Leaked BunkerBroadcastManager' without any mod.
s=0
grep -E 'SCRIPT +\(E\)' "$script" | grep -qv 'BunkerBroadcast' && s=1
verdict "scripts compile: no SCRIPT (E) beyond vanilla noise" $s

"$dev/dayz-client-headless.sh" "${exp[@]}" start "$work/client" -name=dzotester -connect=127.0.0.1 -port="$game" >/dev/null 2>&1
waitfor 240 "grep -q 'dzotester.*is connected' '$adm'" || { bad "client joined"; exit 1; }
ok "client joined"
waitfor 90 "'$work/dzo' player list t1 --config '$work/config.yaml' --token-file '$work/api.token' | grep -q dzotester" || { bad "players: state lists the player"; exit 1; }
ok "players: state lists the player"
id=$(dzo player list t1 | awk '/dzotester/{print $1}')
probe() { grep -h 'DZO-PROBE player' "$script" | tail -1; }
sleep 12 # the character is still loading for a while after the join

for style in important chat notification; do
	s=0
	dzo player msg t1 "$id" "test $style" --style "$style" | grep -q ok || s=1
	verdict "message ($style)" $s
done
s=0
dzo player msg t1 all "to everyone" | grep -q ok || s=1
verdict "message to all" $s
sleep 2
"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$work/messages.png" >/dev/null 2>&1 || true

s=1
if dzo player tp t1 "$id" --x 7500 --z 7500 | grep -q ok; then
	sleep 6
	dzo player list t1 | grep -Eq 'dzotester +750[0-9] +7500' && s=0
fi
verdict "teleport" $s

chemlights() { probe | sed -n 's/.*chemlight=\([0-9]*\).*/\1/p'; }
before=$(chemlights)
dzo player give t1 "$id" Chemlight_Red | grep -q ok || true
sleep 5
after=$(chemlights)
s=1
[ "$after" = "$((before + 1))" ] && s=0
verdict "spawn_item into the inventory ($before -> $after)" $s
dzo player give t1 "$id" Chemlight_Red --target hands | grep -q ok || true
sleep 5
s=1
probe | grep -q 'hands=Chemlight_Red' && s=0
verdict "spawn_item into the hands" $s
s=1
out=$(dzo player give t1 "$id" NoSuchThing 2>&1 || true)
case $out in *'unknown class'*) s=0 ;; esac
verdict "spawn_item refuses an unknown class" $s

# A class watch rule hooks ItemBase: an item on the ground becomes a marker.
dzo player give t1 "$id" Chemlight_Red --target ground | grep -q ok || true
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q 'watched chemlight'" || s=1
verdict "class watch rule: a chemlight on the ground becomes a marker" $s

touch "$tree/profiles/dzo_probe/board"
s=0
waitfor 30 "grep -h 'DZO-PROBE player' '$script' | tail -1 | grep -q 'transport=true'" || s=1
verdict "probe: the player sits in a car" $s
s=1
out=$(dzo player tp t1 "$id" --x 100 --z 100 2>&1 || true)
case $out in *'in a vehicle'*) s=0 ;; esac
verdict "teleport is refused in a vehicle" $s
s=0
waitfor 30 "'$work/dzo' vehicle list t1 --config '$work/config.yaml' --token-file '$work/api.token' | grep -q 'Offroad_02.*crew 1'" || s=1
verdict "vehicle state: the car has crew 1" $s

echo "== $pass passed, $fail failed"
[ "$fail" = 0 ]

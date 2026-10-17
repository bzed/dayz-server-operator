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
# --no-client runs only the checks that need no player (motorbike, event scan) and starts no game, so it
# can run while a DayZ client is open.
# Usage: scripts/test-client.sh [--exp] [--keep] [--no-client]
set -euo pipefail
cd "$(dirname "$0")/.."
dev=${DAYZ_DEV_SCRIPTS:-$HOME/.claude/skills/dayz-dev/scripts}
exp=() keep=0 noclient=0 product=stable
for a in "$@"; do
	case $a in
	--exp) exp=(-e) product=experimental ;;
	--keep) keep=1 ;;
	--no-client) noclient=1 ;;
	*) echo "usage: $0 [--exp] [--keep] [--no-client]" >&2; exit 2 ;;
	esac
done
[ -x "$dev/dayz-client-headless.sh" ] || { echo "dayz-dev scripts not found in $dev (DAYZ_DEV_SCRIPTS)" >&2; exit 2; }
[ "$noclient" = 1 ] || "$dev/dayz-client-headless.sh" status >&2 || { echo "a DayZ client is running; one game at a time" >&2; exit 2; }

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

# server_tests: needs the server, dzo serve and the probe, no client.
server_tests() {
# 1.30 only: Motorbike is not a Car, dzo-admin keeps a registry for it. 1.29 has no motorbikes.
# The world already has motorbikes of the economy: the probe's is the id that is new.
vl() { "$work/dzo" vehicle list t1 --config "$work/config.yaml" --token-file "$work/api.token" "$@"; }
vl | awk '$2 == "motorbike" {print $1}' | sort >"$work/bikes.before"
touch "$tree/profiles/dzo_probe/bike"
s=0
bike=
for _ in $(seq 20); do
	bike=$(vl | awk '$2 == "motorbike" && /Motorbike_01_Blue/ {print $1}' | sort | comm -13 "$work/bikes.before" - | head -1)
	[ -n "$bike" ] && break
	sleep 3
done
[ -n "$bike" ] || s=1
verdict "motorbike: listed with kind motorbike" $s
s=0
"$work/dzo" vehicle repair t1 "$bike" --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "motorbike: repair" $s
s=0
"$work/dzo" vehicle repair t1 "$bike" --scope missing --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "motorbike: add missing parts" $s
s=0
"$work/dzo" vehicle flip t1 "$bike" --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "motorbike: flip" $s
s=0
"$work/dzo" vehicle delete t1 "$bike" --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
waitfor 30 "! '$work/dzo' vehicle list t1 --config '$work/config.yaml' --token-file '$work/api.token' | grep -q '^$bike '" || s=1
verdict "motorbike: delete" $s

# Missing parts: bare cars (as the economy leaves them) get their parts, the headlights in both slots and the battery.
touch "$tree/profiles/dzo_probe/bare"
s=0
waitfor 60 "[ \$('$work/dzo' vehicle list t1 --config '$work/config.yaml' --token-file '$work/api.token' | awk '\$3 ~ /^(Offroad_02|Hatchback_02|CivilianSedan|OffroadHatchback)\$/ && \$4 > 7390 && \$4 < 7440' | wc -l) = 4 ]" || s=1
verdict "repair: the bare cars are listed" $s
for id in $(vl | awk '$3 ~ /^(Offroad_02|Hatchback_02|CivilianSedan|OffroadHatchback)$/ && $4 > 7390 && $4 < 7440 {print $1}'); do
	"$work/dzo" vehicle repair t1 "$id" --scope missing --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
done
verdict "repair: add missing parts to the bare cars" $s
sleep 2
touch "$tree/profiles/dzo_probe/parts"
waitfor 20 "grep -q 'DZO-PROBE parts bare' '$script'" || true
sleep 4
for car in Offroad_02 Hatchback_02 CivilianSedan OffroadHatchback; do
	s=0
	line=$(grep "DZO-PROBE parts bare $car " "$script" | tail -1 || true)
	[ "$(grep -o 'HeadlightH7' <<<"$line" | wc -l || true)" = 2 ] || s=1
	grep -Eq 'CarBattery|TruckBattery' <<<"$line" || s=1
	[ "$s" = 0 ] || echo "$line" >&2
	verdict "repair: $car has both headlights and the battery" $s
done

# A vehicle spawned on the map comes with its parts.
s=0
"$work/dzo" vehicle spawn t1 Offroad_02 --x 7600 --z 7600 --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "spawn vehicle: at a position" $s
touch "$tree/profiles/dzo_probe/parts"
sleep 5
s=0
line=$(grep "DZO-PROBE parts spawned Offroad_02 " "$script" | tail -1 || true)
[ "$(grep -o 'HeadlightH7' <<<"$line" | wc -l)" = 2 ] && grep -q 'CarBattery' <<<"$line" || s=1
[ "$s" = 0 ] || echo "$line" >&2
verdict "spawn vehicle: with both headlights and the battery" $s

# More than a stack holds is more stacks, on the ground at a position.
s=0
"$work/dzo" player spawn t1 Nail --qty 500 --x 7450 --z 7450 --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "spawn item: at a position" $s
touch "$tree/profiles/dzo_probe/ground"
sleep 5
s=0
line=$(grep 'DZO-PROBE ground Nail' "$script" | tail -1 || true)
case $line in *'quantity=500'*) ;; *) s=1 ;; esac
case $line in *'stacks=1 '*) s=1 ;; esac
[ "$s" = 0 ] || echo "$line" >&2
verdict "spawn item: 500 nails are several stacks that add up" $s

# Central Economy events are found by the class of their object: a plain building object (no script class of its
# own, like the wrecks the events place) is a marker when a watch rule selects its class, wildcards included.
touch "$tree/profiles/dzo_probe/event"
ev() { curl -s -H "Authorization: Bearer $(cat "$work/api.token")" "http://127.0.0.1:$api/api/v1/instances/t1/events"; }
mk() { curl -s -H "Authorization: Bearer $(cat "$work/api.token")" "http://127.0.0.1:$api/api/v1/instances/t1/map/markers"; }
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q 'Police cars'" || s=1
verdict "event object: the police car wreck is a marker of its layer" $s
s=0
mk | grep -o '{[^{}]*"layer":"Police cars"[^{}]*}' | grep -q '"icon":"car"' || s=1
verdict "event object: with the icon of the layer" $s
s=0
waitfor 40 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -o '{[^{}]*\"layer\":\"Police cars\"[^{}]*}' | grep -Eq '\"x\":7500[,.}]'" || s=1 # the server's own police events are markers too
verdict "event object: at the place of the wreck" $s

# A dynamic gas zone (as a thrown M18 smoke grenade makes it) is an event with the size of the zone.
touch "$tree/profiles/dzo_probe/gas"
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/events | grep -q '\"x\":73[0-9][0-9],[^}]*\"effect_area\"'" || s=1
verdict "gas zone: reported as an event at the place it was thrown" $s
s=0
ev | grep -o '"x":73[0-9][0-9][^}]*' | grep -q '"radius":120' || s=1
[ "$s" = 0 ] || ev >&2
verdict "gas zone: with the radius of the zone (120 m)" $s
}


go build -o "$work/dzo" ./cmd/dzo
"$work/dzo" servermods build --src servermods --out "$work/built" >/dev/null
mkdir "$work/tsrc" && cp -r testmods/dzo-client-probe "$work/tsrc/" && git -C "$work/tsrc/dzo-client-probe" init -q && git -C "$work/tsrc/dzo-client-probe" add -A && git -C "$work/tsrc/dzo-client-probe" -c user.name=t -c user.email=t@t commit -qm probe # dzo-be-test is built with armake2, not by dzo
"$work/dzo" servermods build --src "$work/tsrc" --out "$work/tbuilt" >/dev/null

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
mkdir -p "$work/site/instances/t1" "$work/secrets/admin" "$tree/profiles/dzo/admin"
cp -r scripts/findfile-site/integrations "$work/site/"
echo 'local_mods: {}' >"$work/site/site.yaml"
cat >"$work/site/instances/t1/instance.yaml" <<Y
name: t1
product: dayz-$product
map: dayzOffline.chernarusplus
mission_source: {preset: vanilla-chernarusplus}
ports: {game: $game, rcon: $((query + 1)), query: $query}
network: host
mods:
  - {local: dzo-admin, server: true}
welcome: "Welcome to #name, port #port (default colour)"
messages:
  - {text: "Rules: be nice (yellow)", on_connect: true, color: yellow}
  - {text: "Homepage: example.org (green)", on_connect: true, color: green, delay: 0}
Y
mkdir -p "$work/site/localmods/dzo-admin/addons"
cat >"$work/config.yaml" <<Y
paths: {data: $work/data, cache: $work/cache, site: $work/site, secrets: $work/secrets}
serve: {installation: t, listen: "127.0.0.1:$api", mod_listen: "127.0.0.1:$mod"}
Y
tok=$(head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n')
echo "$tok" >"$work/secrets/admin/t1.token"
echo "{\"version\":1,\"endpoint\":\"http://127.0.0.1:$mod/\",\"token\":\"$tok\",\"sync_ms\":500,\"players_s\":2,\"vehicles_s\":5,\"markers_s\":10,\"events_s\":5,\"allow_spawn\":true,\"watch\":[{\"layer\":\"Police cars\",\"classes\":[\"Land_Wreck_sed01_aban1_police*\"],\"icon\":\"car\",\"label\":\"Police car\"},{\"layer\":\"lights\",\"classes\":[\"Chemlight_Red\"],\"icon\":\"flare\",\"label\":\"watched chemlight\",\"max\":20}]}" >"$tree/profiles/dzo/admin/config.json"
chmod 600 "$work/secrets/admin/t1.token" "$tree/profiles/dzo/admin/config.json"
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

if [ "$noclient" = 1 ]; then
	server_tests
	echo "== $pass passed, $fail failed"
	[ "$fail" = 0 ]
	exit
fi

"$dev/dayz-client-headless.sh" "${exp[@]}" start "$work/client" -name=dzotester -connect=127.0.0.1 -port="$game" >/dev/null 2>&1
waitfor 240 "grep -q 'dzotester.*is connected' '$adm'" || { bad "client joined"; exit 1; }
ok "client joined"
waitfor 90 "'$work/dzo' player list t1 --config '$work/config.yaml' --token-file '$work/api.token' | grep -q dzotester" || { bad "players: state lists the player"; exit 1; }
ok "players: state lists the player"
# The server messages of the instance (welcome and messages:) are sent by dzo serve as soon as the
# player is in the game. Nothing in the client reports a chat line, so the screenshot is the evidence.
sleep 4
"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$work/welcome.png" >/dev/null 2>&1 || true
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
# With attachments: the item is given the kit it needs (the game's debug spawn, else the kit found in the configs).
for t in "Flashlight --attachments" "PersonalRadio" "Rangefinder --attachments"; do
	dzo player give t1 "$id" $t | grep -q ok || true
done
sleep 6
s=1
probe | grep -q 'Flashlight:[^;]*Battery' && s=0
verdict "spawn_item with attachments: a flashlight gets its battery" $s
s=1
probe | grep -q 'Rangefinder:[^;]*Battery' && s=0
verdict "spawn_item with attachments: a rangefinder gets its battery" $s
s=0
probe | grep -q 'PersonalRadio:' && s=1
verdict "spawn_item without attachments: a bare radio" $s
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

server_tests
echo "== $pass passed, $fail failed"
[ "$fail" = 0 ]

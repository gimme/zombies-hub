#!/bin/bash
# docker/t6-player.sh - run a Black Ops II player, for the game tests of Black Ops II (e2e/game/bo2): the player's page
# and their game at once, as one PC has them.
#
#   t6-player.sh play <out> <server> <name>   the container's command
#   t6-player.sh say <action> [json]          by docker exec: a call of the player's to the lobby, as their page makes
#                                             it, e.g. say ready '{"ready":true}'; prints the lobby's answer
#   t6-player.sh view                         by docker exec: the last view that the lobby sent the player (JSON)
#
# play says hello to the lobby of the server at <server> (its address on Docker's network) as <name>, keeps the lobby's
# events coming, as the player's page does, and says that the player joins Black Ops II games by hand, as a player
# without the launcher does. Each time the player's room has its game up, play starts the player's game: Plutonium's
# T6 Zombies, in LAN mode, which joins the room's game with its password under the name that the room gives the
# player, as the room's line for the game's console does (web/src/windows.ts: t6Console) and as the launcher does
# (launcher/launch.go). Once the room's game is no longer up, play closes the player's game, as the player would. The
# lobby's calls and the game's packets come from the container's one address, where the server expects the player's
# game (server/rooms.go: player.addr).
#
# The game runs under Wine on a virtual display (Xvfb at $DISPLAY, which Mesa draws on the CPU), from copies that it
# can write in: of the game files (/game/t6, as links), and of Plutonium (/plutonium, the server's copy, mounted
# read-only), copied again for each game, as the server downloads its copy, or brings it up to date, as it starts one.
# It draws at 800x600, the least it takes, with its graphics turned down, at 20 frames a second at most. With more to
# draw, its frames take so long that the room's game hears nothing from it for a minute, and drops it. Mesa
# compiles each of the game's shaders the first time the game draws it, and the game waits: the first time, its first
# minutes in a map pass in fits and starts. MESA_SHADER_CACHE_DIR keeps the compiled shaders for the next run.
#
# <out> gets each game's output, <name>-game-<n>.log. play prints what the player does. To take a screenshot, run
# docker exec <container> import -window root <file>.png.
set -u
home=$HOME/player

# The last view that the lobby sent the player, and the password of their room's game while the game is up.
view() { grep '^data: ' "$home/events" 2>/dev/null | tail -n 1 | cut -c7-; }
password() { view | jq -r '.room.t6 | select(.up == true) | .password // empty' 2>/dev/null; }

case ${1:-} in
say)
    [ $# -ge 2 ] || { echo "usage: t6-player.sh say <action> [json]" >&2; exit 2; }
    body=${3:-}
    [ -n "$body" ] || body='{}'
    exec curl -sS -X POST -H "Authorization: Bearer $(cat "$home/token")" "$(cat "$home/server")/lobby/$2" -d "$body" ;;
view)
    view
    exit ;;
play)
    [ $# = 4 ] || { echo "usage: t6-player.sh play <out> <server> <name>" >&2; exit 2; } ;;
*)
    echo "usage: t6-player.sh play <out> <server> <name> | say <action> [json] | view" >&2
    exit 2 ;;
esac
out=$2 server=$3 name=$4
mkdir -p "$home/game"
echo "http://$server:3075" >"$home/server"

Xvfb "$DISPLAY" -screen 0 800x600x24 -nolisten tcp &
for _ in $(seq 100); do
    [ -S "/tmp/.X11-unix/X${DISPLAY#:}" ] && break
    kill -0 $! 2>/dev/null || { echo "t6-player: Xvfb ended" >&2; exit 1; }
    sleep 0.1
done
cp -Rs /game/t6 "$home/game/t6" || { echo "t6-player: no game files in /game/t6" >&2; exit 1; }

hello=$(curl -sS -X POST "http://$server:3075/lobby/hello" -d "$(jq -cn --arg name "$name" '{name: $name}')") &&
    jq -er .token <<<"$hello" >"$home/token" ||
    { echo "t6-player: the lobby did not answer hello: $hello" >&2; exit 1; }
# the lobby's events, as the player's page keeps them coming: again whenever the stream ends
( while :; do
      curl -sSN -H "Authorization: Bearer $(cat "$home/token")" "http://$server:3075/lobby/events" >>"$home/events"
      sleep 1
  done ) &
for _ in $(seq 100); do [ -n "$(view)" ] && break; sleep 0.1; done
"$0" say hand '{"hand":true}' >/dev/null
echo "t6-player: $name is in the lobby"

dir=$(printf 'Z:%s' "$home/game/t6" | tr / '\\')
n=0
while :; do
    pw=$(password)
    if [ -z "$pw" ]; then
        sleep 1
        continue
    fi
    as=$(view | jq -r .room.t6.name)
    n=$((n + 1))
    # Plutonium as the room's game has it: the server downloads its copy, or brings it up to date, as it starts a game
    rm -rf "$home/plutonium"
    cp -R /plutonium "$home/plutonium" && [ -f "$home/plutonium/bin/plutonium-bootstrapper-win32.exe" ] ||
        { echo "t6-player: no Plutonium in /plutonium" >&2; exit 1; }
    echo "t6-player: $name's game $n joins their room's game as $as"
    (cd "$home/plutonium" && exec wine bin/plutonium-bootstrapper-win32.exe t6zm "$dir" -lan +name "$as" \
        +set r_fullscreen 0 +set r_mode 800x600 +set com_maxfps 20 +set cg_drawFPS 1 +set r_picmip 3 \
        +set r_picmip_manual 1 +set sm_enable 0 +set r_dof_enable 0 +set r_ssao 0 +set r_aaSamples 1 +set r_fxaa 0 \
        +set r_texFilterQuality 0 +set r_motionblur_enable 0 +set r_lodBiasRigid 2000 +set r_lodBiasSkinned 2000 \
        +set password "$pw" +connect "$server:3075" >"$out/$name-game-$n.log" 2>&1) &
    game=$!
    while [ "$(password)" = "$pw" ]; do sleep 1; done
    echo "t6-player: $name's room's game is no longer up: their game $n closes"
    wineserver -k
    wait "$game"
done

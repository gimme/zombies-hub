#!/usr/bin/env bash
# docker/server.sh - run a dev server in Docker.
#
#   docker/server.sh [map] [+commands]      e.g.  docker/server.sh zombie_pentagon +set g_debug 1
#
# It runs the server (server/). Players group into rooms on the server's page. The server runs each room's game under
# Wine, with no display, and relays the players' packets to it. A game is BO1Zombies.exe (32-bit) as a dedicated
# server, or Plutonium's server for a room on a Black Ops II map.
#
# map is the map a new room starts with. +commands are added to the command line of each Black Ops game. Players join
# on port 3075. UDP carries the games, for browsers (over WebTransport) and for Windows players. TCP serves the web
# client, its rooms and the game files it downloads: at http://localhost:3075/, or over HTTPS through a reverse proxy
# for other machines.
#
# It is a dev run (ZOMBIES_DEV=1): where the web client leaves something out of the picture, it shows the engine's
# blinking colours instead of black, and the server takes the launcher's dev builds (docker/launcher.sh) as well as
# its releases. Browsers download the game files of the maps they play as fast as they can
# (ZOMBIES_DOWNLOAD_RATE=unlimited). The server's log and the games' output go to stdout. Each game's output also goes
# to a log file in $ZOMBIES_SERVER_HOME/logs.
#
# Each line typed or piped in is a console command. It runs in every running game (status, ...), or in one room's game
# when the line starts with the room's code (KXQM status). rooms lists the rooms. When a game ends (game over, a fatal
# error, a crash or a hang), its players are back in their room. Ctrl+C or docker stop makes the games quit cleanly
# and then stops the server.
#
# ZOMBIES_GAME_DIR       The game files (default game/), with a folder for each title the server hosts: t5 is a copy of
#                        a Black Ops install folder, and t6 is a copy of a Black Ops II install folder. The server
#                        needs at least one of them. The folder is mounted read-only, and nothing writes there.
# ZOMBIES_ENGINE_URL     Where the server downloads its engine build from (default: the release of bo1-zombies that
#                        internal/bo1zombies pins).
# ZOMBIES_ENGINE_FILE    A local engine archive to run instead. The engine's make package makes one.
# ZOMBIES_SERVER_HOME    The folder the server writes in (default build/server): game/ (the games' fs_homepath),
#                        logs/, and engine/ (the engine build it unpacked).
# ZOMBIES_NET            Docker network options (default: publish the server's UDP and TCP port 3075).
# ZOMBIES_SERVER_NAME    The container's name (default zombies-hub-server), for docker stop and docker exec.
# ZOMBIES_HANG_TIMEOUT   Passed through when set. How long a game may run no frame before it ends (default 1m, 0 for
#                        never).
# ZOMBIES_CHEATS         Passed through when set. ZOMBIES_CHEATS=1 runs the rooms' games with cheats (devmap).
# ZOMBIES_MIN_PLAYERS    Passed through when set. The players a room needs to start a game (default 2). 1 lets a
#                        player start a game alone.
# ZOMBIES_DOWNLOAD_RATE  The most bits a second of game files that the server sends to browsers (40M), unlimited (the
#                        default), or off.
# WINEDEBUG              Passed through when set. For example, WINEDEBUG=+loaddll shows which DLLs load (native or
#                        builtin), and WINEDEBUG=+msgbox shows the text of a message box that a game is stuck on.
#                        There is no display to show a message box. A game opens none, but a DLL might.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
game=${ZOMBIES_GAME_DIR:-$root/game}
svhome=${ZOMBIES_SERVER_HOME:-$root/build/server}
image=${ZOMBIES_SERVER_IMAGE:-zombies-hub-server}
name=${ZOMBIES_SERVER_NAME:-zombies-hub-server}
net=${ZOMBIES_NET:--p 3075:3075/udp -p 3075:3075/tcp}
map=zombie_theater
if [ $# -gt 0 ] && [ "${1#+}" = "$1" ]; then map=$1; shift; fi

[ -d "$game/t5/zone" ] || [ -d "$game/t6/zone" ] ||
    { echo "no game files in $game: copy a Black Ops install to $game/t5 or a Black Ops II install to $game/t6" >&2
      exit 1; }
# The image is built the first time. Each later run brings it up to date with docker/Dockerfile and server/, without
# printing the build's progress. That takes seconds when nothing changed, and a minute or so when server/ changed,
# because the server is tested and built again. The echo below says so, because a run that prints nothing for a minute
# looks stuck.
progress=quiet; docker image inspect "$image" >/dev/null 2>&1 || progress=auto
echo "server: bringing the image $image up to date (a minute or so when server/ or docker/Dockerfile changed)" >&2
docker build --progress=$progress -f "$here/Dockerfile" --target server -t "$image" \
    --build-context repo="$root" \
    --build-arg UID="$uid" --build-arg GID="$gid" "$here" >/dev/null
# The server serves the built page, web/dist. A change to the page shows after docker/web.sh has run again.
"$here/web.sh"
mkdir -p "$svhome"
gamedir=$(for_wine "$game" zombies-hub-game "$image")
mapfile -t engine < <(engine_args "$name")
# In Git Bash the server's copy of Plutonium and the games' home folders go in Docker volumes too. /config is a
# Windows folder there, and Wine can't run a program from one (host.sh). With Plutonium in /config, a Black Ops II
# room's game crashed when it started. With its home folder in /config, the game never answered.
pluto=()
if [ -n "${MSYSTEM:-}" ]; then
    docker run --rm -u 0 --entrypoint chown -v "$name-plutonium:/plutonium" -v "$name-homes:/config/game" "$image" \
        "$uid:$gid" /plutonium /config/game
    pluto=(-v "$name-plutonium:/plutonium" -e ZOMBIES_PLUTONIUM_DIR=/plutonium -v "$name-homes:/config/game")
fi

tty=-i; [ -t 0 ] && [ -t 1 ] && tty=-it
# NET_ADMIN is for the firewall that lets a game reach only its players (entrypoint.sh).
# shellcheck disable=SC2086
docker run --rm $tty --name "$name" $net --cap-add NET_ADMIN \
    -e ZOMBIES_DEV=1 -e ZOMBIES_DOWNLOAD_RATE="${ZOMBIES_DOWNLOAD_RATE:-unlimited}" \
    ${WINEDEBUG:+-e WINEDEBUG="$WINEDEBUG"} \
    ${ZOMBIES_HANG_TIMEOUT:+-e ZOMBIES_HANG_TIMEOUT="$ZOMBIES_HANG_TIMEOUT"} \
    ${ZOMBIES_CHEATS:+-e ZOMBIES_CHEATS="$ZOMBIES_CHEATS"} \
    ${ZOMBIES_MIN_PLAYERS:+-e ZOMBIES_MIN_PLAYERS="$ZOMBIES_MIN_PLAYERS"} \
    -v "$gamedir:/game:ro" -v "$(abs "$svhome"):/config" -v "$root/web/dist:/web:ro" \
    "${engine[@]}" "${pluto[@]}" \
    "$image" -map "$map" -- "$@"

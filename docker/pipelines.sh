#!/usr/bin/env bash
# docker/pipelines.sh - make the engine's lists of pipelines, web/pipelines/<map>.bin.
#
#   docker/pipelines.sh <dir> [map...]
#
# A map's list has the pipelines that the web client draws with on the map (bo1-zombies/web/src/d3d9_webgpu.cpp:
# recipes). A browser that draws with WebGPU compiles a pipeline for each new combination of shaders and states it
# draws. Until a pipeline is ready, the browser leaves what the pipeline draws out of the picture. The client
# therefore compiles the map's list while the map loads. It compiles the spawn's pipelines, which the player waits for
# before going in, and the stand-ins of the rest. The pipelines of the rest compile when a draw first needs them.
#
# <dir> is where the lists are written: web/pipelines in a checkout of bo1-zombies, which packs them into its next
# build. With no maps named, the script makes the lists that <dir> already has, or zombie_theater's list if <dir> has
# none.
#
# The script runs docker/server.sh's server and a browser in Docker (docker/player/pipelines.mjs, Playwright driving
# headless Chromium). The browser plays each map in a room's game on the server, and then in the browser alone, where
# cheats work. There it looks all around the spawn, draws every cell of the world at once lit by each kind of light
# the map has, plays every effect, holds every weapon and lets zombies come. Then it looks all around the spawn once
# more as a new player whose shadow maps are off, as they are on mobile. The spawn draws with other pipelines then.
#
# A recipe is valid on any GPU, because it is the game's shaders and Direct3D states. The lists made here therefore
# serve every player. Each map's list is written once the map is done. A map that fails keeps the list it had, and
# the script goes on with the next map. Run the script again when the renderer changes what it draws with, with
# ZOMBIES_ENGINE_FILE set to an archive of that build. The script needs the game files.
#
# ZOMBIES_GAME_DIR, ZOMBIES_ENGINE_URL, ZOMBIES_ENGINE_FILE  as for docker/server.sh
# ZOMBIES_PIPELINES_NAME  The containers' name (default zombies-hub-pipelines). Two runs with different names can make
#                         different maps' lists at the same time.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
name=${ZOMBIES_PIPELINES_NAME:-zombies-hub-pipelines}
player_image=${ZOMBIES_PLAYER_IMAGE:-zombies-hub-player}
lists=${1:?usage: docker/pipelines.sh <dir> [map...]}
shift
if [ $# -eq 0 ]; then
    for f in "$lists"/*.bin; do [ -e "$f" ] && set -- "$@" "$(basename "$f" .bin)"; done
    [ $# -gt 0 ] || set -- zombie_theater
fi

progress=quiet; docker image inspect "$player_image" >/dev/null 2>&1 || progress=auto
docker build --progress=$progress -f "$here/Dockerfile" --target player -t "$player_image" "$here" >/dev/null

tmp=$(abs "$(mktemp -d)")
cleanup() {
    docker stop "$name" >/dev/null 2>&1 || true
    [ -z "${server:-}" ] || wait "$server" 2>/dev/null || true
    rm -rf "$tmp"
}
trap cleanup EXIT
docker rm -f "$name" >/dev/null 2>&1 || true

# Start the server with nothing on its console's stdin (the browser runs commands in the page), and with its home in
# $tmp, so that nothing in build/server changes.
mkdir -p "$tmp/home"
ZOMBIES_SERVER_NAME=$name ZOMBIES_NET="--network bridge" ZOMBIES_SERVER_HOME="$tmp/home" \
    ZOMBIES_MIN_PLAYERS=1 "$here/server.sh" </dev/null >"$tmp/server.log" 2>&1 &
server=$!
for _ in $(seq 180); do
    [ "$(docker container inspect -f '{{.State.Health.Status}}' "$name" 2>/dev/null)" = healthy ] && break
    kill -0 "$server" 2>/dev/null || { tail -n 20 "$tmp/server.log"; echo "pipelines: the server ended" >&2; exit 1; }
    sleep 1
done
echo "pipelines: the server is up"

mkdir -p "$tmp/lists" "$lists"
status=0
docker run --rm --name "$name-browser" --network "container:$name" --shm-size 1g -u "$uid:$gid" -e HOME=/tmp \
    -v "$tmp:/out" --entrypoint node "$player_image" /player/pipelines.mjs http://localhost:3075 /out/lists "$@" ||
    status=$?
for f in "$tmp"/lists/*.bin; do
    [ -e "$f" ] || continue
    cp "$f" "$lists/"
    echo "pipelines: $lists/$(basename "$f") written"
done
[ $status -eq 0 ] || tail -n 20 "$tmp/server.log"
exit $status

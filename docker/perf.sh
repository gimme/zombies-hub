#!/usr/bin/env bash
# docker/perf.sh - profile what the web client's frames cost the browser, at fixed views of Kino.
#
#   docker/perf.sh [--survey] [--gpu <flags>] [--cpu] [--clear <mode>] [--args <engine args>] [--command <command>]
#                  [--toggle <command>] [--capture] [--mobile] [--overlay] [--screen <width>x<height>]
#                  [<x>,<y>,<z>,<yaw>,<pitch>]...
#
# A profile has each pass's draws, the calls to WebGPU that set state for them, and the bytes each frame writes to the
# GPU, by where they went (web/src/main.ts: &gpu=16). These counts don't depend on the GPU, so the headless browser
# here, which draws in software, counts the same as a gaming PC's browser. Its frame times mean nothing. To measure
# frame times, run docker/player/perf.mjs --chrome on the PC while docker/server.sh runs. It uses the PC's Chrome.
#
# With no views given, the script profiles the spawn and docker/player/perf.mjs's VIEWS. --survey first looks around
# every cell and lists the views that cost the most. The script runs docker/server.sh's server and a browser in Docker
# (docker/player/perf.mjs), as docker/pipelines.sh does. The profiles go to build/perf/<time>.json. The log and a
# screenshot of each view go next to them.
#
# The game files (.env), ZOMBIES_ENGINE_URL, ZOMBIES_ENGINE_FILE  as for docker/server.sh
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
name=zombies-hub-perf
player_image=${ZOMBIES_PLAYER_IMAGE:-zombies-hub-player}

progress=quiet; docker image inspect "$player_image" >/dev/null 2>&1 || progress=auto
docker build --progress=$progress -f "$here/Dockerfile" --target player -t "$player_image" "$here" >/dev/null

out=$root/build/perf
mkdir -p "$out"
stamp=$(date +%Y%m%d-%H%M%S)
tmp=$(abs "$(mktemp -d)")
cleanup() {
    docker stop "$name" >/dev/null 2>&1 || true
    [ -z "${server:-}" ] || wait "$server" 2>/dev/null || true
    rm -rf "$tmp"
}
trap cleanup EXIT
docker rm -f "$name" >/dev/null 2>&1 || true

# Start the server with nothing on its console's stdin (the browser runs commands in the page), and with its home in
# $tmp.
mkdir -p "$tmp/home"
ZOMBIES_SERVER_NAME=$name ZOMBIES_NET="--network bridge" ZOMBIES_SERVER_HOME="$tmp/home" \
    ZOMBIES_MIN_PLAYERS=1 "$here/server.sh" </dev/null >"$tmp/server.log" 2>&1 &
server=$!
for _ in $(seq 180); do
    [ "$(docker container inspect -f '{{.State.Health.Status}}' "$name" 2>/dev/null)" = healthy ] && break
    kill -0 "$server" 2>/dev/null || { tail -n 20 "$tmp/server.log"; echo "perf: the server ended" >&2; exit 1; }
    sleep 1
done
echo "perf: the server is up"

docker run --rm --name "$name-browser" --network "container:$name" --shm-size 1g -u "$uid:$gid" -e HOME=/tmp \
    -v "$tmp:/out" --entrypoint node "$player_image" /player/perf.mjs http://localhost:3075 /out/perf.json "$@" |
    tee "$out/$stamp.log"
cp "$tmp/perf.json" "$out/$stamp.json"
for f in "$tmp"/perf-*.png "$tmp"/perf-*.cpuprofile "$tmp"/perf-*.capture "$tmp"/perf-shaders.json; do [ -e "$f" ] && cp "$f" "$out/$stamp-${f##*/perf-}"; done
echo "perf: build/perf/$stamp.json written"

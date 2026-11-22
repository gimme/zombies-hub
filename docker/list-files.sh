#!/usr/bin/env bash
# docker/list-files.sh - list what each map's game reads from the game files, into server/reads/<map>.json.
#
#   docker/list-files.sh [map...]
#
# The lists are built into the server (server/files.go). The script makes the list of each map named, or else of each
# map that the game files have. For each map, zombies-server list-files runs a dedicated server under Wine. The
# dedicated server loads the map with +set bo1_files 1 (bo1-zombies/src/server_mp/sv_files_mp.cpp), prints the zones
# it loads and every image and sound of those zones that is in the archives, and quits. That takes about 3 s a map.
# Run the script again when the engine changes what a map's game reads, or when a map is added that has no list.
# Commit what it writes.
#
# ZOMBIES_GAME_DIR, ZOMBIES_ENGINE_URL, ZOMBIES_ENGINE_FILE  the game files and the engine build, as for
#                                                          docker/server.sh
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
game=${ZOMBIES_GAME_DIR:-$root/game}
image=${ZOMBIES_SERVER_IMAGE:-zombies-server}
name=zombies-list-files

[ -d "$game/t5/zone" ] ||
    { echo "no game files in $game/t5 (no zone folder): copy a Black Ops install there" >&2; exit 1; }
progress=quiet; docker image inspect "$image" >/dev/null 2>&1 || progress=auto
docker build --progress=$progress -f "$here/Dockerfile" --target server -t "$image" \
    --build-context repo="$root" \
    --build-arg UID="$uid" --build-arg GID="$gid" "$here" >/dev/null
mapfile -t engine < <(engine_args "$name")
gamedir=$(for_wine "$game" zombies-game "$image")
home=$(abs "$(mktemp -d)")
trap 'rm -rf "$home"' EXIT

docker rm -f "$name" >/dev/null 2>&1 || true
# The container is on the default network, because the server downloads its engine build unless it has it.
docker run --rm --name "$name" -v "$gamedir:/game:ro" -v "$home:/config" "${engine[@]}" \
    -v "$root/server/reads:/out" "$image" list-files /out "$@"

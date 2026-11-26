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
# ZOMBIES_T5, ZOMBIES_ENGINE_URL, ZOMBIES_ENGINE_FILE  Black Ops' game files and the engine build, as for
#                                                    docker/server.sh
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
image=${ZOMBIES_SERVER_IMAGE:-zombies-hub-server}
name=zombies-hub-list-files

t5=$(game_folder t5)
[ -n "$t5" ] && [ -d "$t5/zone" ] ||
    { echo "list-files: no Black Ops game files: ZOMBIES_T5 in .env names the game's install folder" \
          "(.env.example)" >&2
      exit 1; }
progress=quiet; docker image inspect "$image" >/dev/null 2>&1 || progress=auto
docker build --progress=$progress -f "$here/Dockerfile" --target server -t "$image" \
    --build-context repo="$root" \
    --build-arg UID="$uid" --build-arg GID="$gid" "$here" >/dev/null
mapfile -t engine < <(engine_args "$name")
gamefiles=$(game_args zombies-hub-game "$image")
mapfile -t gamefiles <<<"$gamefiles"
home=$(abs "$(mktemp -d)")
trap 'rm -rf "$home"' EXIT

docker rm -f "$name" >/dev/null 2>&1 || true
# The container is on the default network, because the server downloads its engine build unless it has it.
docker run --rm --name "$name" "${gamefiles[@]}" -v "$home:/config" "${engine[@]}" \
    -v "$root/server/reads:/out" "$image" list-files /out "$@"

#!/usr/bin/env bash
# docker/image.sh - build the server image (docker/Dockerfile, target release).
#
#   docker/image.sh [name:tag]      default zombies:latest
#
# The image has the server, Wine, the launcher and the page. It runs anywhere Docker runs (Unraid, Compose, docker run)
# and needs only the game files, mounted at /game. It has no engine in it: the server downloads its engine build the
# first time it starts (internal/bo1zombies). The image's org.opencontainers.image.revision label is the commit it was
# built from (git describe).
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
tag=${1:-zombies:latest}

revision=$(git -C "$root" describe --always --dirty 2>/dev/null || echo unknown)
mapfile -t launcher < <(launcher_args "$root")
docker build -f "$here/Dockerfile" --target release -t "$tag" "${launcher[@]}" \
    --label org.opencontainers.image.revision="$revision" \
    --build-context repo="$root" --build-context webpage="$root/web" "$here"
echo "built $tag ($revision)"

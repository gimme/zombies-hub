#!/usr/bin/env bash
# docker/push.sh - push the server image (built by docker/image.sh) to a registry.
#
#   docker/push.sh <host>[:port][/path] [name:tag]      port 5000 unless given; image zombies-hub:latest by default
#
# The registry can be Docker's registry image running on the Unraid server (docs/hosting.md). The script tags the image
# with the registry's name and pushes it: docker/push.sh 192.168.0.123 pushes zombies-hub:latest as
# 192.168.0.123:5000/zombies-hub:latest. Docker pushes to a registry without HTTPS, such as that one, only when the
# registry is listed in Docker's insecure-registries. A registry on localhost needs no listing.
set -euo pipefail
registry=${1:?usage: docker/push.sh <host>[:port][/path] [name:tag]}
image=${2:-zombies-hub:latest}
registry=${registry%/}
host=${registry%%/*}
case $host in *:*) ;; *) registry=$host:5000${registry#"$host"} ;; esac
target=$registry/$image

label='{{index .Config.Labels "org.opencontainers.image.revision"}}'
revision=$(docker image inspect -f "$label" "$image" 2>/dev/null) ||
    { echo "no image $image (docker/image.sh builds it)" >&2; exit 1; }
docker tag "$image" "$target"
# docker push's error output is kept, to look for the error that says the registry has no HTTPS. Its progress output
# goes to the terminal.
status=0
{ err=$(docker push "$target" 2>&1 >&3); } 3>&1 || status=$?
[ -z "$err" ] || echo "$err" >&2
if [ "$status" != 0 ]; then
    case $err in *"HTTP response to HTTPS client"*)
        echo "${registry%%/*} has no HTTPS: add it to insecure-registries in /etc/docker/daemon.json (Docker Desktop:" \
            "Settings, Docker Engine), restart Docker, and push again" >&2 ;;
    esac
    exit "$status"
fi
echo "pushed $target ($revision)"

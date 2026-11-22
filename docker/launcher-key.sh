#!/usr/bin/env bash
# docker/launcher-key.sh - make the key that the launcher's builds are signed with (internal/release). Run it once.
#
#   docker/launcher-key.sh
#
# The private half is written to ~/.zombies/launcher.key (ZOMBIES_LAUNCHER_KEY), on this machine and outside the
# repository. The public half is written to launcher/release.pub, which is committed. Every launcher built after that
# has the public half inside. The images built on this machine then sign their launcher (host.sh: launcher_args).
# Launchers with the public half replace themselves with those builds, and with no others.
#
# Keep a copy of the private half somewhere safe. Without it, no build can be signed for the launchers that players
# have. Those launchers then stay at their build until their players install a new one by hand. Whoever has the
# private half can sign a program that those launchers replace themselves with.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
key=${ZOMBIES_LAUNCHER_KEY:-$HOME/.zombies/launcher.key}
[ ! -s "$key" ] || { echo "launcher-key: $key is there already" >&2; exit 1; }
[ ! -s "$root/launcher/release.pub" ] ||
    { echo "launcher-key: launcher/release.pub has a key already, whose private half is not at $key" >&2; exit 1; }
image=$(sed -n 's/^FROM \(golang:[^ ]*\) AS server-build$/\1/p' "$here/Dockerfile")
[ -n "$image" ] || { echo "launcher-key: docker/Dockerfile has no server-build stage FROM a golang image" >&2; exit 1; }
mkdir -p "$(dirname "$key")"
docker run --rm -v "$root:/src:ro" -v "$(abs "$(dirname "$key")"):/key" -v "$root/launcher:/pub" -w /src \
    -v zombies-go-mod:/go/pkg/mod -v zombies-go-build:/root/.cache/go-build "$image" \
    sh -c 'go run ./cmd/launcher-release key "/key/$0" /pub/release.pub && chown "$1" "/key/$0" /pub/release.pub' \
    "$(basename "$key")" "$uid:$gid"
echo "launcher-key: the private half is $key: keep a copy of it somewhere safe, and out of the repository"
echo "launcher-key: the public half is launcher/release.pub: commit it"

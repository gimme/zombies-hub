#!/usr/bin/env bash
# docker/launcher-key.sh - make the key that the launcher's releases are signed with (internal/release). Run it once.
#
#   docker/launcher-key.sh
#
# The public half is written to launcher/release.pub, which is committed. Every launcher built after that has the
# public half inside, and replaces itself with the releases that the private half signed, and with no others.
#
# The private half is written to ~/.zombies-hub/launcher.key (ZOMBIES_LAUNCHER_KEY), outside the repository. Its one
# line goes into the repository's secret LAUNCHER_KEY on GitHub, where the release workflow signs each release with
# it (.github/workflows/launcher.yml). Keep a copy somewhere safe, offline, and then delete the file: no other machine
# needs the key. Without it, no release can be signed for the launchers that players have. Those launchers then stay
# at their build until their players install a new one by hand. Whoever has the private half can sign a program that
# those launchers replace themselves with.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
key=${ZOMBIES_LAUNCHER_KEY:-$HOME/.zombies-hub/launcher.key}
[ ! -s "$key" ] || { echo "launcher-key: $key is there already" >&2; exit 1; }
[ ! -s "$root/launcher/release.pub" ] ||
    { echo "launcher-key: launcher/release.pub has a key already, whose private half is the secret LAUNCHER_KEY" >&2
      exit 1; }
image=$(sed -n 's/^FROM \(golang:[^ ]*\) AS server-build$/\1/p' "$here/Dockerfile")
[ -n "$image" ] || { echo "launcher-key: docker/Dockerfile has no server-build stage FROM a golang image" >&2; exit 1; }
mkdir -p "$(dirname "$key")"
docker run --rm -v "$root:/src:ro" -v "$(abs "$(dirname "$key")"):/key" -v "$root/launcher:/pub" -w /src \
    -v zombies-hub-go-mod:/go/pkg/mod -v zombies-hub-go-build:/root/.cache/go-build "$image" \
    sh -c 'go run ./cmd/launcher-release key "/key/$0" /pub/release.pub && chown "$1" "/key/$0" /pub/release.pub' \
    "$(basename "$key")" "$uid:$gid"
echo "launcher-key: the private half is $key. Put it into the repository's secret LAUNCHER_KEY"
echo "launcher-key:   (gh secret set LAUNCHER_KEY < $key), keep a copy somewhere safe and offline, and delete the file"
echo "launcher-key: the public half is launcher/release.pub: commit it"

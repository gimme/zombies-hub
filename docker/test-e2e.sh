#!/usr/bin/env bash
# docker/test-e2e.sh - run the page tests (e2e/): the built page in Chromium, against the server with stand-in games.
#
#   docker/test-e2e.sh [playwright test flags...]      e.g. tests/bo1.spec.ts, or -g leaderboards
#
# It builds the page, the server and the stand-in game, and runs the tests, all in Docker (docker/Dockerfile, target
# e2e). It needs neither the game files nor an engine build, and takes about three minutes. A failed test leaves its
# screenshot, trace, server log and players' consoles in build/e2e/results. With Node on the host,
# npx playwright show-trace <trace.zip> in e2e/ shows a trace.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
docker build --quiet -f "$here/Dockerfile" --target e2e -t zombies-e2e --build-context repo="$root" \
    --build-context webpage="$root/web" "$here" >/dev/null
mkdir -p "$root/build/e2e"
MSYS_NO_PATHCONV=1 docker run --rm --init --ipc=host -u "$uid:$gid" -e HOME=/tmp -v "$root/build/e2e:/out" \
    zombies-e2e --output /out/results "$@"

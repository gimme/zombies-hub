#!/usr/bin/env bash
# docker/web.sh - build the page (web/) into web/dist.
#
#   docker/web.sh
#
# Vite builds the page in Docker (docker/Dockerfile, target web), so the host needs no Node. The build checks the
# page's TypeScript first. It takes seconds, because the page's dependencies are installed again only when
# web/package-lock.json changes. docker/server.sh runs this script before it starts and serves web/dist. After a
# change to the page, run the script again and reload the page. With Node on the host, `npm run watch` in web/ builds
# the page on every change instead.
#
# The built files replace those in web/dist. A file that the build no longer makes stays there until it is deleted.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
docker build --quiet -f "$here/Dockerfile" --target web --build-context webpage="$root/web" \
    --output "type=local,dest=$root/web/dist" "$here" >/dev/null
echo "built the page in web/dist" >&2

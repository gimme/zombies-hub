#!/usr/bin/env bash
# docker/test-go.sh - vet and test the Go code (server/, launcher/, internal/, cmd/, e2e/standin).
#
#   docker/test-go.sh [go test flags...]      e.g. -run TestRooms -v
#
# It vets and tests the code the way docker/image.sh's build does (docker/Dockerfile, stage server-build), in that
# stage's Go image. It takes about a minute and needs neither an engine build nor the game files. Use it for a change
# to the server, before the game tests check the change with the game. The launcher is also vetted as the Windows
# program it is. Before that, it checks that each file is formatted as gofmt formats it, as GitHub does
# (.github/workflows/test.yml), and names the files that are not. Go's module and build caches stay in Docker volumes
# (zombies-hub-go-mod, zombies-hub-go-build), so a run after the first compiles only what changed.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
image=$(sed -n 's/^FROM \(golang:[^ ]*\) AS server-build$/\1/p' "$here/Dockerfile")
[ -n "$image" ] || { echo "test-go: docker/Dockerfile has no server-build stage FROM a golang image" >&2; exit 1; }
docker run --rm -v "$root:/src:ro" -w /src -v zombies-hub-go-mod:/go/pkg/mod \
    -v zombies-hub-go-build:/root/.cache/go-build "$image" \
    sh -c 'files=$(gofmt -l server launcher internal cmd e2e/standin) &&
        if [ -n "$files" ]; then
            echo "gofmt would format these files otherwise (run gofmt -w on them):" $files >&2; exit 1
        fi &&
        go=$(echo ./server/... ./launcher/... ./internal/... ./cmd/... ./e2e/...) &&
        go vet $go && GOOS=windows GOARCH=amd64 go vet ./launcher && go test $go "$@"' sh "$@"

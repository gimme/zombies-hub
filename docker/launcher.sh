#!/usr/bin/env bash
# docker/launcher.sh - build the launcher (launcher/) as a dev build, to try a change on a Windows PC.
#
#   docker/launcher.sh
#
# It builds build/zombies-launcher-setup.exe for Windows the way the release workflow does
# (.github/workflows/launcher.yml), in the Go image of docker/Dockerfile's stage server-build, with two differences:
# its version is 0, and it is not signed. go-winres gives the program its icon (launcher/icon.png, a copy of the page's
# icon), its name, and a manifest that gives its message boxes the current Windows look. -H=windowsgui builds it
# without a console window. The build works on a copy of the code in the container, so the files that go-winres makes
# stay out of the checkout.
#
# Run the program by hand on the PC: it installs itself over the launcher the PC has. Version 0 is a dev build. It
# never replaces itself with a release, and only a dev server (make dev) takes it. On any other server, the room's page
# then links to the latest release, which installs over it.
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
image=$(sed -n 's/^FROM \(golang:[^ ]*\) AS server-build$/\1/p' "$here/Dockerfile")
[ -n "$image" ] || { echo "launcher: docker/Dockerfile has no server-build stage FROM a golang image" >&2; exit 1; }
mkdir -p "$root/build"
docker run --rm -v "$root:/repo:ro" -v "$root/build:/out" -v zombies-go-mod:/go/pkg/mod \
    -v zombies-go-build:/root/.cache/go-build "$image" sh -ec '
        mkdir /src && cd /repo && cp -R go.mod go.sum launcher internal /src && cd /src
        go install github.com/tc-hib/go-winres@v0.3.3
        (cd launcher && go-winres simply --arch amd64 --manifest gui --icon icon.png --product-name "Zombies Launcher" \
            --file-description "Zombies Launcher" --original-filename zombies-launcher.exe \
            --product-version 0 --file-version 0)
        GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=0" \
            -o /out/zombies-launcher-setup.exe ./launcher
        chown "$0" /out/zombies-launcher-setup.exe' "$uid:$gid"
echo "launcher: built build/zombies-launcher-setup.exe, a dev build (version 0): run it on the PC to install it"

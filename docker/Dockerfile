# syntax=docker/dockerfile:1
#
# Linux images that run the server and the games, and the tests. One file, eight targets:
#
#   wine    Runs BO1Zombies.exe (32-bit) under Wine, with no display, as a user whose uid is the UID build arg. It is
#           the base of windows-player.
#   server  Wine and the server (server/). The server runs the games as a separate user and relays the players'
#           packets to them. Used by docker/server.sh. It needs the checkout as a second build context:
#           --build-context repo=.
#   web     The built page alone (web/, built with Vite), with no system under it. docker/web.sh writes it to web/dist
#           for docker/server.sh. It needs the page's folder as a build context: --build-context webpage=web
#   release The server target with the built page in it. Unraid or Compose runs this image. Built by docker/image.sh.
#           It needs both build contexts, repo and webpage.
#   player  A browser that scripts drive in the web client: Playwright drives Chromium (player/). Used by
#           docker/pipelines.sh and docker/perf.sh.
#   e2e     The page tests (e2e/): Playwright Test drives Chromium on the built page, against the server with stand-in
#           games. Used by docker/test-e2e.sh. It needs both build contexts, repo and webpage.
#   windows-player
#           A Windows player: the wine target and a virtual display, for the Windows client (windows-player.sh). Used
#           by the game tests (e2e/game). It is the base of t6-player.
#   t6-player
#           A Black Ops II player: windows-player with what Plutonium's game needs, for the player's page and their
#           game at once (t6-player.sh). Used by the game tests of Black Ops II (e2e/game/bo2).
#
# No image has the Black Ops engine in it. The server downloads its engine build the first time it starts
# (internal/bo1zombies).

ARG UBUNTU=ubuntu:24.04
# The version of the Playwright image. player/package.json pins the same version, because the Playwright library
# drives only the browser build of its own version.
ARG PLAYWRIGHT_VERSION=1.62.1
# The version of Node that builds the page (stage web-build).
ARG NODE_VERSION=26.13.1
# WINE_ARCH selects the Wine for the games. win64 installs 32-bit and 64-bit Wine: Black Ops and Black Ops II are
# 32-bit programs, and Black Ops III's dedicated server is a 64-bit one (server/t7.go). win32 installs 32-bit Wine
# only, for an image that is 0.8 GB smaller and hosts no Black Ops III.
ARG WINE_ARCH=win64

# ---- stage wine-games: Wine for the games, shared by the wine and server targets. It is half the size of the Wine in
# the engine's build image (bo1-zombies/docker/Dockerfile) ----
FROM ${UBUNTU} AS wine-games
ARG WINE_ARCH
RUN case "$WINE_ARCH" in \
        win32) wine64= ;; \
        win64) wine64=wine64 ;; \
        *) echo "WINE_ARCH is win32 or win64, not '$WINE_ARCH'" >&2; exit 1 ;; \
    esac && \
    dpkg --add-architecture i386 && apt-get update && \
    apt-get install -y --no-install-recommends wine wine32:i386 $wine64 ca-certificates && \
    rm -rf /var/lib/apt/lists/*
ENV WINEDEBUG=-all WINEARCH=${WINE_ARCH}

# ---- target wine: runs the game as a user whose uid is the UID build arg. It is the base of windows-player ----
FROM wine-games AS wine
# The engine comes without DLLs. Direct3D and the other DirectX DLLs are Wine's own, and the exe loads binkw32.dll
# from the game files, which are its working directory.
ARG UID=1000
ARG GID=1000
RUN (userdel -r ubuntu 2>/dev/null || true) && \
    groupadd -g "$GID" game && useradd -m -u "$UID" -g "$GID" game
COPY --chmod=755 wineprefix-init.sh /usr/local/bin/
USER game
ENV WINEPREFIX=/home/game/.wine
RUN wineprefix-init.sh
WORKDIR /home/game
# Wine turns SIGINT into Ctrl+C, which a dedicated server answers by quitting cleanly. Docker's default SIGTERM ends a
# Wine process at once, without telling its clients.
STOPSIGNAL SIGINT

# ---- stage server-build: tests and builds the server (server/, Go) from the build context "repo", the checkout ----
# It vets and tests all the Go code, the launcher's too (launcher/), and vets the launcher as the Windows program it
# is. The image has no launcher in it: the launcher's releases are on GitHub (.github/workflows/launcher.yml), and the
# room's page links to them.
FROM golang:1.27.4-bookworm AS server-build
WORKDIR /src
COPY --from=repo go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY --from=repo server server
COPY --from=repo launcher launcher
COPY --from=repo internal internal
COPY --from=repo cmd cmd
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && GOOS=windows GOARCH=amd64 go vet ./launcher && go test ./... && \
    CGO_ENABLED=0 go build -trimpath -o /out/zombies-server ./server

# ---- target server: runs the server, which runs the games ----
# There is no X server. A game opens no window, and its console is stdin and stdout. Without a display driver, an
# attempt to open a window fails at once in Wine (WINEDEBUG=err+all shows explorer.exe and wined3d's GL probe failing,
# harmlessly). A stray message box from a DLL therefore returns instead of waiting for a click that can never come.
# The games run as the user game, uid 3075, and the server runs as PUID:PGID (entrypoint.sh). A game that is taken
# over through its network code then can't read the server's files (zombies.db) or the server's environment. With
# NET_ADMIN, a firewall also lets the games reach only their players (nftables, entrypoint.sh). The Wine prefix of the
# user game is made here, owned by uid 3075. It is not taken from the wine stage, whose user has the UID build arg as
# its uid: a chown of the prefix when the container starts would copy its 140 MB up from the image, which takes
# seconds.
FROM wine-games AS server
RUN (userdel -r ubuntu 2>/dev/null || true) && \
    groupadd -g 3075 game && useradd -m -u 3075 -g 3075 game
COPY --chmod=755 wineprefix-init.sh /usr/local/bin/
USER game
ENV WINEPREFIX=/home/game/.wine
RUN wineprefix-init.sh
WORKDIR /home/game
# The container starts as root, and entrypoint.sh starts the server as the user PUID:PGID. Their defaults are the UID
# and GID build args, which docker/server.sh sets to the host's user. tini reaps the processes that Wine leaves behind
# (wineserver and Wine's services outlive a run of the game), so the container runs the same with or without
# docker run --init.
USER root
RUN apt-get update && apt-get install -y --no-install-recommends tini nftables && rm -rf /var/lib/apt/lists/*
ARG UID=1000
ARG GID=1000
ENV PUID=$UID PGID=$GID
COPY --chmod=755 entrypoint.sh /usr/local/bin/
# console opens the server's console from a shell in the container (server/console.go).
RUN printf '#!/bin/sh\nexec zombies-server console "$@"\n' >/usr/local/bin/console && chmod 755 /usr/local/bin/console
COPY --from=server-build /out/zombies-server /usr/local/bin/zombies-server
# The server stops the games on SIGTERM or SIGINT. Under Wine a game gets a Ctrl+C, which it answers by quitting
# cleanly.
STOPSIGNAL SIGTERM
ENTRYPOINT ["tini", "--", "entrypoint.sh"]
# The container is healthy while the server answers through the relay, the way it answers a web client
# (zombies-server probe, which the relay leaves out of its log). The rooms' games are not checked: they come and go
# with their rooms, and the server ends a game that hangs. Unraid's Docker tab shows the result.
HEALTHCHECK --interval=30s --timeout=15s --start-period=2m --start-interval=1s --retries=3 \
    CMD zombies-server probe -timeout 10s "localhost:${ZOMBIES_PORT:-3075}" || exit 1

# ---- stage web-build: checks, tests and builds the page (web/, with Vite) from the build context "webpage", the
# folder web of the checkout. The page's dependencies are installed from web/package-lock.json, in a layer that is
# built again only when that file changes. The built page is /web/dist (web/vite.config.ts) ----
FROM node:${NODE_VERSION}-bookworm-slim AS web-build
WORKDIR /web
COPY --from=webpage package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY --from=webpage index.html tsconfig.json vite.config.ts ./
COPY --from=webpage src src
COPY --from=webpage public public
RUN npm run check && npm test && npm run build

# ---- target web: the built page alone. docker/web.sh writes it to web/dist ----
FROM scratch AS web
COPY --from=web-build /web/dist /

# ---- target release: the server with the built page in it. It runs anywhere Docker runs. Built by docker/image.sh ----
FROM server AS release
COPY --from=web-build /web/dist /web/

# ---- target player: a browser that scripts drive in the web client (player/pipelines.mjs, player/perf.mjs), for
# docker/pipelines.sh and docker/perf.sh. Playwright's image has Playwright's Chromium (Chrome for Testing) and the
# system libraries that Chromium needs. The Playwright library is installed from player/package-lock.json ----
FROM mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble AS player
ARG PLAYWRIGHT_VERSION
WORKDIR /player
COPY player/package.json player/package-lock.json ./
RUN npm ci --no-audit --no-fund && \
    test "$(node -p "require('playwright/package.json').version")" = "$PLAYWRIGHT_VERSION"
COPY player/pipelines.mjs player/perf.mjs ./
ENTRYPOINT ["node"]

# ---- stage e2e-build: the server and the stand-in game (e2e/standin), for the page tests ----
FROM golang:1.27.4-bookworm AS e2e-build
WORKDIR /src
COPY --from=repo go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY --from=repo server server
COPY --from=repo internal internal
COPY --from=repo e2e/standin e2e/standin
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/zombies-server ./server && CGO_ENABLED=0 go build -o /out/standin ./e2e/standin

# ---- target e2e: the page tests (e2e/), for docker/test-e2e.sh. Playwright Test drives Playwright's Chromium on the
# built page, against the server with stand-in games. The files are where they are in the checkout: the tests in
# /src/e2e, the server and the stand-in in /src/build/e2e, the page in /src/web/dist. The Playwright library is
# installed from e2e/package-lock.json ----
FROM mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble AS e2e
ARG PLAYWRIGHT_VERSION
WORKDIR /src/e2e
COPY --from=repo e2e/package.json e2e/package-lock.json ./
RUN npm ci --no-audit --no-fund && \
    test "$(node -p "require('@playwright/test/package.json').version")" = "$PLAYWRIGHT_VERSION"
COPY --from=web-build /web/dist /src/web/dist
COPY --from=e2e-build /out /src/build/e2e
COPY --from=repo e2e/playwright.config.ts e2e/global-setup.ts e2e/fixtures.ts e2e/host.js e2e/tsconfig.json ./
COPY --from=repo e2e/tests tests
ENTRYPOINT ["node_modules/.bin/playwright", "test"]

# ---- target windows-player: a Windows player (windows-player.sh), for the game tests. It is the Windows client under
# Wine on a virtual display (Xvfb). Wine's d3d9 draws with OpenGL, which Mesa (llvmpipe) runs on the CPU. For that,
# this target installs what the server's image lacks: the 32-bit GL, and the X libraries that Wine's display driver
# loads. ImageMagick's import takes screenshots, and curl and jq are for a Black Ops II player's calls to the lobby
# (t6-player) ----
FROM wine AS windows-player
USER root
RUN apt-get update && apt-get install -y --no-install-recommends tini xvfb imagemagick curl jq \
        libgl1:i386 libglx-mesa0:i386 libgl1-mesa-dri:i386 libxrender1:i386 libxrandr2:i386 libxi6:i386 \
        libxcursor1:i386 libxinerama1:i386 libxcomposite1:i386 libxfixes3:i386 libxxf86vm1:i386 && \
    rm -rf /var/lib/apt/lists/*
COPY --chmod=755 windows-player.sh /usr/local/bin/
USER game
ENV DISPLAY=:99
ENTRYPOINT ["tini", "--", "windows-player.sh"]

# ---- target t6-player: a Black Ops II player (t6-player.sh), for the game tests. It is Plutonium's T6 Zombies under
# Wine on the windows-player's virtual display, where Wine's d3d11 draws with Mesa on the CPU. Wine tells the game
# that its graphics card is an Intel one with 1 GB: the game passes over a card with NVIDIA's id when it can't load
# nvapi.dll, and over one with Mesa's id, and either way it then stops with "DirectX encountered an unrecoverable
# error" ----
FROM windows-player AS t6-player
RUN for setting in 'VideoMemorySize REG_SZ 1024' 'VideoPciVendorID REG_DWORD 0x8086' \
            'VideoPciDeviceID REG_DWORD 0x5912'; do \
        set -- $setting && wine reg add 'HKCU\Software\Wine\Direct3D' /v "$1" /t "$2" /d "$3" /f || exit 1; \
    done && wineserver -w
COPY --chmod=755 t6-player.sh /usr/local/bin/
ENTRYPOINT ["tini", "--", "t6-player.sh"]

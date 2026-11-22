# Development

## How it works

The server is one Go program. It serves the page, keeps the rooms, and runs one game process per room under Wine. Players never reach a game directly: the server relays their packets to the game's UDP port, which only the server can reach.

Each game it hosts is a title, named after its engine:

- **`t5`, Black Ops.** The game process is `BO1Zombies.exe` from [bo1-zombies](https://github.com/gimme/bo1-zombies), running as a dedicated server. Players run the same engine in the browser, as WebAssembly, and connect over WebTransport.
- **`t6`, Black Ops II.** The game process is Plutonium's dedicated server. Players run Plutonium on their own PC. The launcher installs and starts it, and their game connects over UDP.

A room plays one title: its map's. The page keeps the titles apart. A room's host picks the title, then a map of it, and the leaderboards are by title. Where the server hosts one title, the page offers no choice.

## Layout

- `server/`: the server. `rooms.go` has the rooms, `game.go` runs a game process, `relay.go` relays players' packets, and `web.go` serves the page and the game files. Each title has a file: `t5.go`, `t6.go`. `title.go` has what the rest of the server knows of a title, and which map a room goes to when its host picks one. `store.go` is the SQLite database that keeps the players and the games' reports, and has its schema.
- `launcher/`: Zombies Launcher, the Windows program that starts a player's Black Ops II. Its releases are on GitHub ([below](#releasing-the-launcher)), and the room's page links to the latest one.
- `internal/`: Go packages.
  - `bo1zombies/`: downloads and unpacks the Black Ops engine build.
  - `plutonium/`: keeps a copy of Plutonium up to date, for the server and the launcher.
  - `launcherapi/`: what the server and the launcher say to each other, and where the launcher's releases are.
  - `release/`: the signature on a launcher release.
- `cmd/launcher-release/`: makes the launcher's signing key and signs a release.
- `web/`: the page, built with [Vite](https://vite.dev/).
  - `index.html`: the page's markup.
  - `src/`: the page's code, in TypeScript. It is in parts, one for each thing the page does, such as `room.ts`, `profile.ts`, `game.ts` and `boards.ts` (the leaderboards). `main.ts` is the entry, and its header lists the parts and the URL parameters. The parts are layered: a part imports only from the parts below it. `api.ts` has the types of what the server sends, `titles.ts` keeps the titles apart, and `settings.ts` is Black Ops' options menu. A part's tests are next to it, in `<part>.test.ts`. The `.css` files are the style sheets.
  - `public/`: files that are served as they are. `player.js` runs the Black Ops engine in a page, `store.js` keeps game files in the browser, and `sw.js` is the service worker.
  - `dist/` (not in the repo): the built page, which the server serves.
- `e2e/`: the page tests and the game tests, with [Playwright Test](https://playwright.dev/).
  - `tests/` has the page tests. `fixtures.ts` starts a server for each test and opens players' pages, `host.js` stands in for `player.js`, and `standin/` is a Go program that stands in for the games.
  - `game/` has the game tests, in three groups: `smoke/`, `engine/` and `long/`. `game/fixtures.ts` starts a server container for each test (`server.ts`) and opens players in Chrome (`player.ts`).
- `docker/`: the image, and the scripts that build, run and test it. They work on Linux and in Git Bash on Windows.
- `.github/workflows/`: what GitHub runs. `test.yml` checks every push and pull request, and `launcher.yml` releases the launcher.
- `game/` (not in the repo): the game files, `game/t5` and `game/t6`.
- `build/` (not in the repo): what the dev server and the tests keep.

Every setting starts with `ZOMBIES_`. Docker images, containers and volumes start with `zombies-`.

## Running

`make dev` runs a dev server on <http://localhost:3075/> (`docker/server.sh`). `make` lists the other targets. Each target runs one script in `docker/`, and the script's header lists its options.

`docker/server.sh` rebuilds its image when `server/` or `docker/Dockerfile` changed, and builds the page into `web/dist`.

After a change in `web/`, run `make web` and reload the page. The page is built in Docker, so it needs no Node on the host. With Node installed, `npm ci` and then `npm run watch` in `web/` build the page on every change instead. `npm run check` checks its TypeScript, and `npm test` runs its tests in under a second.

On Windows, Wine can't run programs or load game files from a Windows folder. The scripts therefore copy the game files into the Docker volume `zombies-game` (9 GB per game, once), and the server unpacks the engine into the volume `<container>-engine`.

To try a change to the launcher, `make launcher` builds it into `build/zombies-launcher-setup.exe` (`docker/launcher.sh`). Run that file on a Windows PC to install it over the launcher the PC has. It is a dev build, of version 0: it never updates itself, and only a dev server takes it. On any other server the room says that the launcher is too old, and links to the latest release.

## The engine

The server runs one pinned build of bo1-zombies. The pin is two constants in `internal/bo1zombies/bo1zombies.go`: the release (such as `v1`) and the SHA-256 of its archive. To move to a new build, change both.

To run a build that is not released, package it in a checkout of bo1-zombies (`make package`) and point the server at the archive:

```sh
ZOMBIES_ENGINE_FILE=../bo1-zombies/build/bo1-zombies-<id>.tar.gz make dev
```

Two things in this repo depend on the engine build:

- `server/reads/<map>.json` lists what each map reads from the game files, for browsers to download. `docker/list-files.sh` remakes the lists.
- The pipeline lists that the web client compiles while a map loads belong to bo1-zombies, but are made here: `docker/pipelines.sh <dir> [map...]`, with `<dir>` set to `web/pipelines` in the bo1-zombies checkout.

## Testing

- `make test-go` runs the Go tests in about a minute. It needs no game files. GitHub runs the same tests on every push to `main` and on every pull request, on Linux and on Windows (`.github/workflows/test.yml`). It also checks, tests and builds the page there.
- `make test-e2e` runs the page tests: the built page in Chromium, against the server, with stand-ins for the games and for the engine in the page. A stand-in game answers the server as the real one does, and the test tells it on the server's console when a round begins and when the game is over. The pages join it through the relay, as the engine does. The tests cover what the page and the server do around a game: rooms and their games for both titles, the choice of a room's title, mods, a player's tabs, playing alone, the leaderboards, missing maps and game folders, the gamepad in the lobby, the Windows client's join, and a launcher too old for the server. They need no game files, run in Docker in about three minutes, and also run on GitHub. `docker/test-e2e.sh tests/room.spec.ts` runs one file, and a failed test leaves its screenshot, trace and logs in `build/e2e/results`.
- `make test` runs the game tests (`docker/test-games.sh`): real games against the server image, played in this machine's Chrome on its GPU, and by the Windows client under Wine. Each test has a server container of its own, and they run four at a time. They need the Black Ops game files, Node, `npm ci` in `e2e`, and Chrome. They are in three groups:
  - `smoke`: what a deploy needs to work. The server image as Unraid runs it, a room's game of two to game over, playing alone offline and from the player's own install, downloads at a capped rate, and the Windows client.
  - `engine`: what the engine does in the page and as a room's game. Playing alone with the settings menu, the gamepad, tabs, the end scoreboard, mods, a hung game and a client of another build.
  - `long`: a revive, a door bought, and six players in a room, one at a time, in about six minutes.

  With nothing named, `smoke` and `engine` run, in about seven minutes. `docker/test-games.sh smoke` runs a group, and `docker/test-games.sh coop solo` the named tests. A test's results are in `build/test/results`: the server's log and data, each player's log, and screenshots.
- A new check of what the page does belongs in the page tests, and one of what the engine does in the game tests.

The tests use the pinned engine build. Set `ZOMBIES_ENGINE_FILE` to test another.

## Releasing the launcher

The launcher's releases are among the [releases of this repository](https://github.com/gimme/zombies-hub/releases). Each has a tag `launcher-v<N>` and two files: the launcher, `zombies-launcher-setup.exe`, and `zombies-launcher.sig`, its version and hash signed with the maker's key. N is the launcher's version, a whole number.

One more release, tagged `launcher`, always has the files of the latest version. The room's page links to the launcher there. An installed launcher reads the statement there each time it goes to play, before it calls the server, and replaces itself when the statement has a later version and the maker's signature (`launcher/update.go`). Neither goes by the release that GitHub marks as the latest, so the repository can have releases of other things too.

To release the launcher, tag the commit with the next version and push the tag:

```sh
git tag launcher-v<N>        # N: one more than the last release
git push origin launcher-v<N>
```

The workflow `.github/workflows/launcher.yml` then checks the tag, vets and tests the launcher, builds it with version N, signs it with the repository's secret `LAUNCHER_KEY`, publishes the release, and puts its files in the release tagged `launcher`. It stops before it builds anything when N is not larger than every release so far.

`LAUNCHER_KEY` is the one line of the private half of the maker's key, `zombies-release-private:...`. `make launcher-key` made the key once (`docker/launcher-key.sh`), and the public half is `launcher/release.pub`. To set the secret, `gh secret set LAUNCHER_KEY < ~/.zombies/launcher.key`. Keep a copy of the private half somewhere safe and offline, and no copy on any other machine. Whoever has it can sign a program that every installed launcher takes. If it is lost, a new key needs a new `release.pub`, and players then install the next release by hand.

The server takes launchers from a lowest version on: `MinVersion` in `internal/launcherapi/launcherapi.go`. A dev server also takes dev builds, of version 0. Raise `MinVersion` when the server needs something that older launchers don't do, and only once a release that does it is out. A launcher updates itself before it calls the server, so a server with the new code hears only of the launchers that found no later release they could take. It turns those away, and the player's room then says that the launcher is too old, with the link to the latest one.

## Commits

Commits follow [Conventional Commits](https://www.conventionalcommits.org/): a short imperative subject, such as `fix(launcher): retry the update check`, and a body only when it adds something.

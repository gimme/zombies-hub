# Development

## How it works

The server is one Go program. It serves the page, keeps the rooms, and runs one game process per room under Wine. Players never reach a game directly: the server relays their packets to the game's UDP port, which only the server can reach.

Each game it hosts is a title, named after its engine:

- **`t5`, Black Ops.** The game process is `BO1Zombies.exe` from [bo1-zombies](https://github.com/gimme/bo1-zombies), running as a dedicated server. Players run the same engine in the browser, as WebAssembly, and connect over WebTransport.
- **`t6`, Black Ops II.** The game process is Plutonium's dedicated server. Players run Plutonium on their own PC. The launcher installs and starts it, and their game connects over UDP.

## Layout

- `server/`: the server. `rooms.go` has the rooms, `game.go` runs a game process, `relay.go` relays players' packets, and `web.go` serves the page and the game files. Each title has a file: `t5.go`, `t6.go`. `store.go` is the SQLite database that keeps the players and the games' reports, and has its schema.
- `launcher/`: Zombies Launcher, the Windows program that starts a player's Black Ops II. The image's build compiles it and puts it inside the server, which hands it out.
- `internal/`: Go packages.
  - `bo1zombies/`: downloads and unpacks the Black Ops engine build.
  - `plutonium/`: keeps a copy of Plutonium up to date, for the server and the launcher.
  - `launcherapi/`: what the server and the launcher say to each other.
  - `release/`: the signature on a launcher update.
- `cmd/launcher-release/`: makes the launcher's signing key and signs a build.
- `web/`: the page, built with [Vite](https://vite.dev/).
  - `index.html`: the page's markup.
  - `src/`: the page's code, in TypeScript. It is in parts, one for each thing the page does, such as `room.ts`, `profile.ts`, `game.ts` and `boards.ts` (the leaderboards). `main.ts` is the entry, and its header lists the parts and the URL parameters. The parts are layered: a part imports only from the parts below it. `api.ts` has the types of what the server sends, and `settings.ts` is the game's options menu. A part's tests are next to it, in `<part>.test.ts`. The `.css` files are the style sheets.
  - `public/`: files that are served as they are. `player.js` runs the Black Ops engine in a page, `store.js` keeps game files in the browser, and `sw.js` is the service worker.
  - `dist/` (not in the repo): the built page, which the server serves.
- `e2e/`: the page tests and the game tests, with [Playwright Test](https://playwright.dev/).
  - `tests/` has the page tests. `fixtures.ts` starts a server for each test and opens players' pages, `host.js` stands in for `player.js`, and `standin/` is a Go program that stands in for the games.
  - `game/` has the game tests, in three groups: `smoke/`, `engine/` and `long/`. `game/fixtures.ts` starts a server container for each test (`server.ts`) and opens players in Chrome (`player.ts`).
- `docker/`: the image, and the scripts that build, run and test it. They work on Linux and in Git Bash on Windows.
- `game/` (not in the repo): the game files, `game/t5` and `game/t6`.
- `build/` (not in the repo): what the dev server and the tests keep.

Every setting starts with `ZOMBIES_`. Docker images, containers and volumes start with `zombies-`.

## Running

`make dev` runs a dev server on <http://localhost:3075/> (`docker/server.sh`). `make` lists the other targets. Each target runs one script in `docker/`, and the script's header lists its options.

`docker/server.sh` rebuilds its image when `server/` or `docker/Dockerfile` changed, and builds the page into `web/dist`.

After a change in `web/`, run `make web` and reload the page. The page is built in Docker, so it needs no Node on the host. With Node installed, `npm ci` and then `npm run watch` in `web/` build the page on every change instead. `npm run check` checks its TypeScript, and `npm test` runs its tests in under a second.

On Windows, Wine can't run programs or load game files from a Windows folder. The scripts therefore copy the game files into the Docker volume `zombies-game` (9 GB per game, once), and the server unpacks the engine into the volume `<container>-engine`.

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
- `make test-e2e` runs the page tests: the built page in Chromium, against the server, with stand-ins for the games and for the engine in the page. A stand-in game answers the server as the real one does, and the test tells it on the server's console when a round begins and when the game is over. The pages join it through the relay, as the engine does. The tests cover what the page and the server do around a game: rooms and their games for both titles, mods, a player's tabs, playing alone, the leaderboards, missing maps and game folders, the gamepad in the lobby, and the Windows client's join. They need no game files, run in Docker in about three minutes, and also run on GitHub. `docker/test-e2e.sh tests/room.spec.ts` runs one file, and a failed test leaves its screenshot, trace and logs in `build/e2e/results`.
- `make test` runs the game tests (`docker/test-games.sh`): real games against the server image, played in this machine's Chrome on its GPU, and by the Windows client under Wine. Each test has a server container of its own, and they run four at a time. They need the Black Ops game files, Node, `npm ci` in `e2e`, and Chrome. They are in three groups:
  - `smoke`: what a deploy needs to work. The server image as Unraid runs it, a room's game of two to game over, playing alone offline and from the player's own install, downloads at a capped rate, and the Windows client.
  - `engine`: what the engine does in the page and as a room's game. Playing alone with the settings menu, the gamepad, tabs, the end scoreboard, mods, a hung game and a client of another build.
  - `long`: a revive, a door bought, and six players in a room, one at a time, in about six minutes.

  With nothing named, `smoke` and `engine` run, in about seven minutes. `docker/test-games.sh smoke` runs a group, and `docker/test-games.sh coop solo` the named tests. A test's results are in `build/test/results`: the server's log and data, each player's log, and screenshots.
- A new check of what the page does belongs in the page tests, and one of what the engine does in the game tests.

The tests use the pinned engine build. Set `ZOMBIES_ENGINE_FILE` to test another.

## Commits

Commits follow [Conventional Commits](https://www.conventionalcommits.org/): a short imperative subject, such as `fix(launcher): retry the update check`, and a body only when it adds something.

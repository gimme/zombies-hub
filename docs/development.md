# Development

## How it works

The server is one Go program. It serves the page, keeps the rooms, and runs one game process per room under Wine. Players never reach a game directly: the server relays their packets to the game's UDP port, which only the server can reach.

Each game it hosts is a title, named after its engine:

- **`t5`, Black Ops.** The game process is `BO1Zombies.exe` from [bo1-zombies](https://github.com/gimme/bo1-zombies), running as a dedicated server. Players run the same engine in the browser, as WebAssembly, and connect over WebTransport.
- **`t6`, Black Ops II.** The game process is Plutonium's dedicated server. Players run Plutonium on their own PC. The launcher installs and starts it, and their game connects over UDP.
- **`t7`, Black Ops III.** The game process is Steam's dedicated server of the game, run by [BOIII](https://github.com/Ezz-lol/boiii-free), under 64-bit Wine. Players run BOIII on their own PC. The launcher installs and starts it too, and their game connects over UDP.

A room plays one title: its map's. The page keeps the titles apart. A room's host picks the title, then a map of it, and the leaderboards are by title. Where the server hosts one title, the page offers no choice.

## Layout

- `server/`: the server. `rooms.go` has the rooms, `game.go` runs a game process, `relay.go` relays players' packets, and `web.go` serves the page and the game files. Each title has a file: `t5.go`, `t6.go`, `t7.go`, and `pc.go` has what the rooms of the last two share. `title.go` has what the rest of the server knows of a title, and which map a room goes to when its host picks one. `store.go` is the SQLite database that keeps the players and the games' reports, and has its schema.
- `launcher/`: Zombies Launcher, the Windows program that starts a player's Black Ops II or Black Ops III and joins their room's game. `title.go` has what differs between the two games, and `boiii.go` how Black Ops III is started and joined. Its releases are on GitHub ([below](#releasing-the-launcher)), and the room's page links to the latest one. `mac.sh` installs the launcher on a Mac and runs it there, in Wine. A release has it as `zombies-launcher-mac.sh`, and the room's page shows a Mac the line that fetches it.
- `internal/`: Go packages.
  - `bo1zombies/`: downloads and unpacks the Black Ops engine build.
  - `plutonium/`: keeps a copy of Plutonium up to date, for the server and the launcher.
  - `boiii/`: keeps a copy of BOIII up to date, for the server, and gets BOIII's program for the launcher.
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
  - `game/` has the game tests, in four groups: `smoke/`, `engine/`, `long/` and `bo2/`. `game/fixtures.ts` starts a server container for each test (`server.ts`), opens players in Chrome (`player.ts`), and starts Black Ops II players in containers (`t6.ts`).
- `docker/`: the image, and the scripts that build, run and test it. They work on Linux and in Git Bash on Windows.
- `.github/workflows/`: what GitHub runs. `test.yml` checks every push and pull request, and `launcher.yml` releases the launcher.
- `.env` (not in the repo): this machine's settings for the scripts in `docker/`. It names where the games are installed ([below](#the-game-files)). `.env.example` is the one to copy.
- `build/` (not in the repo): what the dev server and the tests keep.

Every setting starts with `ZOMBIES_`. The server image is `zombies-hub`, and the other Docker images, containers and volumes start with `zombies-hub-`.

## Running

`make dev` runs a dev server on <http://localhost:3075/> (`docker/server.sh`). `make` lists the other targets. Each target runs one script in `docker/`, and the script's header lists its options.

`docker/server.sh` rebuilds its image when `server/` or `docker/Dockerfile` changed, and builds the page into `web/dist`.

After a change in `web/`, run `make web` and reload the page. The page is built in Docker, so it needs no Node on the host. With Node installed, `npm ci` and then `npm run watch` in `web/` build the page on every change instead. `npm run check` checks its TypeScript, and `npm test` runs its tests in under a second.

## The game files

The scripts take each game from where it is installed, so a game is on the disk once and can still be played there. `.env` at the top of the checkout names the folders (`docker/host.sh`), and `.env.example` is the one to copy:

```sh
ZOMBIES_T7=C:/Program Files (x86)/Steam/steamapps/common/Call of Duty Black Ops III
ZOMBIES_T7_SERVER=C:/Program Files (x86)/Steam/steamapps/common/Call of Duty Black Ops III 545990
```

`ZOMBIES_T5` and `ZOMBIES_T6` name Black Ops and Black Ops II the same way. A path needs no quotes, and one that is not absolute is from the top of the checkout. The scripts mount each folder read-only at its place in the container (`/game/t5`, `/game/t7-server`), which is how a host mounts them too ([hosting.md](hosting.md#game-files)).

On Linux that is all: the games run from the install folders. On Windows, Wine can't run programs or load game files from a Windows folder, which is what Docker Desktop mounts: Black Ops III's dedicated server hangs on the second fastfile it loads from one, as the other games fail there. The scripts therefore copy what the server reads into the Docker volume `zombies-hub-game`, a folder at a time, and again only when a folder's files change. That is all of Black Ops and Zombies' files of Black Ops II, 9 to 12 GB each, and of Black Ops III 4 GB of its 91: the fastfiles of Zombies, and the dedicated server without its multiplayer maps (`server/gamefiles.go` has the rules for each game, and the scripts ask the image's server for them: `zombies-server game-files`). A later run copies only the files that changed. So a Windows machine has each game once where Steam installed it, and once more, in the volume, only what its server reads. What the dev server sends of Black Ops III to a launcher is far more than that, so the install folder is mounted as well, as it is, to send from (`ZOMBIES_SEND_DIR`): the server only reads the files there, which a Windows folder does for. Its first start reads all 87 GB once, to list them, in about four minutes. The server also unpacks the engine into the volume `<container>-engine`, and keeps Plutonium and BOIII in volumes of their own.

The image has 32-bit and 64-bit Wine, because Black Ops III's dedicated server is a 64-bit program. `--build-arg WINE_ARCH=win32` builds an image without the 64-bit one, which is 0.8 GB smaller and hosts no Black Ops III.

To try a change to the launcher, `make launcher` builds it into `build/zombies-launcher-setup.exe` (`docker/launcher.sh`). Run that file on a Windows PC to install it over the launcher the PC has. It is a dev build, of version 0: it never updates itself, and only a dev server takes it. On any other server the room says that the launcher is too old, and links to the latest release.

On a Mac, copy the build and `launcher/mac.sh` there, and run `ZOMBIES_LAUNCHER_EXE=zombies-launcher-setup.exe bash mac.sh` in their folder. Without `ZOMBIES_LAUNCHER_EXE`, the script installs the latest release. `ZOMBIES_LAUNCHER_RELEASE=<address>` has it fetch the release's files from another place, such as a folder of them that this PC serves. `?mac=1` in the page's address shows a Mac's page on a PC. The script's header lists its commands: `log` shows what went wrong, and `check <host>:<port>` tells whether the launcher reaches a server on the Mac's own network.

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
- `make test-e2e` runs the page tests: the built page in Chromium, against the server, with stand-ins for the games and for the engine in the page. A stand-in game answers the server as the real one does, and the test tells it on the server's console when a round begins and when the game is over. The pages join it through the relay, as the engine does. The tests cover what the page and the server do around a game: rooms and their games for both titles, the choice of a room's title, mods, a player's tabs, playing alone, the leaderboards, missing maps and game folders, the gamepad in the lobby, the Windows client's join, a launcher too old for the server, and a Black Ops III room, its Launch game and its Play local, with a launcher's calls and no game. They need no game files, run in Docker in about three minutes, and also run on GitHub. `docker/test-e2e.sh tests/room.spec.ts` runs one file, and a failed test leaves its screenshot, trace and logs in `build/e2e/results`.
- `make test` runs the game tests (`docker/test-games.sh`): real games against the server image, played in this machine's Chrome on its GPU, and by the Windows client under Wine. Each test has a server container of its own, and they run four at a time. They need the Black Ops game files, Node, `npm ci` in `e2e`, and Chrome. They are in four groups:
  - `smoke`: what a deploy needs to work. The server image as Unraid runs it, a room's game of two to game over, playing alone offline and from the player's own install, downloads at a capped rate, and the Windows client.
  - `engine`: what the engine does in the page and as a room's game. Playing alone with the settings menu, the gamepad, tabs, the end scoreboard, mods, a hung game and a client of another build.
  - `long`: a revive, a door bought, and six players in a room, one at a time, in about six minutes.
  - `bo2`: Black Ops II's games, one at a time: a room's game of co-op to game over, a game with the room's mods, Grief in two teams past the server's most players, and Turned. Each player is a container of its own (`docker/t6-player.sh`), with their page's calls and Plutonium's game under Wine, which Mesa draws on the CPU, two to three cores a player. The players stand still: a script that the tests put in the server's games (`e2e/game/bo2/steer.gsc`) keeps the zombies away, downs players, gives a player a gun off the wall, and moves a player onto Turned's cure, and the game does the rest. The tests need the Black Ops II game files too (`ZOMBIES_T6`), and are skipped without them. The first run downloads Plutonium for the test servers and compiles the game's shaders, which takes minutes longer.

  With nothing named, `smoke` and `engine` run, in about seven minutes. `docker/test-games.sh smoke` runs a group, and `docker/test-games.sh coop solo` the named tests. A test's results are in `build/test/results`: the server's log and data, each player's log, and screenshots. One run goes at a time.

  To stop a run, press Ctrl+C in its terminal, or run `make test-stop` (`docker/test-games.sh --stop`) in another, which waits for the run to end. The run then stops by itself, in a few minutes at most: its tests end as at their own end, each player's Chrome closing and each server stopping, and it exits with code 130, the results so far in `build/test/results`. A second Ctrl+C does not hurry it. The run goes on apart from its terminal, so closing the terminal, or killing the script, stops it the same way. Don't end its processes (node, Chrome, or the PowerShell it runs in on Windows): a Chrome ended while it draws can hang the GPU's driver until the machine restarts (`e2e/game/chrome.ts`).
- A new check of what the page does belongs in the page tests, and one of what the engine does in the game tests.

The tests use the pinned engine build. Set `ZOMBIES_ENGINE_FILE` to test another.

## Releasing the launcher

The launcher's releases are among the [releases of this repository](https://github.com/gimme/zombies-hub/releases). Each has a tag `launcher-v<N>` and four files: the launcher, `zombies-launcher-setup.exe`; `zombies-launcher.sig`, its version and hash signed with the maker's key; `zombies-launcher-mac.sh`, the script that installs the launcher on a Mac (`launcher/mac.sh`); and `zombies-launcher.png`, the launcher's icon for the app that the script makes. N is the launcher's version, a whole number.

One more release, tagged `launcher`, always has the files of the latest version. The room's page links to the launcher there, and the line that it shows a Mac fetches the script from there. An installed launcher reads the statement there each time it goes to play, before it asks the server anything, and replaces itself when the statement has a later version and the maker's signature (`launcher/update.go`). Neither goes by the release that GitHub marks as the latest, so the repository can have releases of other things too.

To release the launcher, tag the commit with the next version and push the tag:

```sh
git tag launcher-v<N>        # N: one more than the last release
git push origin launcher-v<N>
```

The workflow `.github/workflows/launcher.yml` then checks the tag, vets and tests the launcher, builds it with version N, signs it with the repository's secret `LAUNCHER_KEY`, publishes the release, and puts its files in the release tagged `launcher`. It stops before it builds anything when N is not larger than every release so far.

`LAUNCHER_KEY` is the one line of the private half of the maker's key, `zombies-release-private:...`. `make launcher-key` made the key once (`docker/launcher-key.sh`), and the public half is `launcher/release.pub`. To set the secret, `gh secret set LAUNCHER_KEY < ~/.zombies-hub/launcher.key`. Keep a copy of the private half somewhere safe and offline, and no copy on any other machine. Whoever has it can sign a program that every installed launcher takes. If it is lost, a new key needs a new `release.pub`, and players then install the next release by hand.

The server takes launchers from a lowest version on: `MinVersion` in `internal/launcherapi/launcherapi.go`. A title can take a later one, the first version that starts it (`MinVersionOf`: 4 for Black Ops III), and the server then refuses an older launcher only while its player is in a room of that title. A dev server also takes dev builds, of version 0. Raise `MinVersion` when the server needs something that older launchers don't do, and only once a release that does it is out. A launcher updates itself before it asks the server anything, so a server with the new code turns away only the launchers that found no later release they could take. The one call a launcher makes first, which says that it started (`POST /launcher/started`), the server takes from any version. It turns the others away, and the player's room then says that the launcher is too old, with the link to the latest one.

On a Mac the launcher updates itself too, but the script that runs it there doesn't, nor plutonium-mac-aio, which it installs at the commit it names (`aio_commit`). Those change only when the player pastes the room's line into Terminal again. So the script has a version, `mac_version` in `launcher/mac.sh`, which it gives the launcher, and the launcher has the lowest version it plays with, `macNeeds` in `launcher/mac.go`. When a change to the script is one that the launcher or the game needs, such as another `aio_commit` for a later Plutonium, raise both in the same release. A launcher whose script is older then ends, and the player's room shows the line again, saying that the launcher needs an update.

## Commits

Commits follow [Conventional Commits](https://www.conventionalcommits.org/): a short imperative subject, such as `fix(launcher): retry the update check`, and a body only when it adds something.

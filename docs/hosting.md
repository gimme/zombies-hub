# Hosting

## Run the server

Build the image and start it with Docker Compose:

```sh
make image              # builds zombies-hub:latest
docker compose up -d    # uses compose.yaml
```

The container needs:

- **`/game`**, read-only: the game files ([below](#game-files)).
- **`/config`**: what the server keeps, such as its players, leaderboards, logs and downloads.
- **Port 3075, UDP and TCP.** TCP serves the page. UDP carries the games.
- **`--cap-add NET_ADMIN`**: lets the container firewall the games, so a game can only talk to its players.

The server runs as `PUID`:`PGID` (default 1000:1000) and owns what it writes in `/config`.

The players, the leaderboards and the report of every game are in `/config/zombies.db`, a SQLite database. To back it up, copy the file while the server is stopped.

On Unraid, copy `docker/unraid/my-zombies-hub.xml` to `/boot/config/plugins/dockerMan/templates-user/` and add the container from that template. `make push REGISTRY=<host>` pushes the image to a registry that Unraid can pull from.

## Game files

`/game` has one folder per game, each a copy of the game's install folder. The server hosts the games it finds, and needs at least one.

| Game | Folder |
| --- | --- |
| Black Ops | `/game/t5` |
| Black Ops II | `/game/t6` |
| Black Ops III | `/game/t7`, and `/game/t7-server` |

Black Ops III takes two folders, because its dedicated server is a program of its own:

- **`/game/t7-server`** is the install folder of the tool Call of Duty: Black Ops III - Unranked Dedicated Server, the one that has `UnrankedServer` in it. Steam gives the tool to who has the game: in Steam's library, show Tools. On a machine without Steam, `steamcmd +@sSteamCmdForcePlatformType windows +force_install_dir <folder> +login <your account> +app_update 545990` downloads it. It is 4 GB, of which the server reads 1 GB: everything but the multiplayer maps (`UnrankedServer/zone/*mp_*`).
- **`/game/t7`** is the game's install folder, which is 87 GB. The server reads 3 GB of it: the fastfiles of Zombies, which are the files in `zone` with `zm_` in their names that end in `.ff` or `.fd`. A dedicated server draws nothing and plays no sound, so the rest need not be there to host the game: the `.xpak` files, `zone/snd` and `video`. It does need to be there for the server to send the game to players ([below](#game-files)). The page's pictures of the game's maps are from more of the install. A map's picture is the one in the game's map select, from the menus' fastfiles: `core_frontend.ff`, `core_frontend.fd` and `core_frontend_patch.ff`, 430 MB together. Without `core_frontend.ff` the page shows the maps without pictures. The page's background in a room is the map's loading screen, from 11 GB of the `.xpak` files: `core_patch.xpak` lists them, and they are in `initial.xpak`, `base_patch.xpak`, and `base_patch_tu5`, `tu9`, `tu12`, `tu15`, `tu21` and `tu26.xpak`.

A folder of `/game` can also be a mount of its own, so that a game stays where it is installed: `-v "/games/Call of Duty Black Ops III:/game/t7:ro"`. A link in the folder that is mounted at `/game` does not work, because the container does not see where it leads.

An install folder may have more than Zombies, such as the game's multiplayer or campaign, which Steam installs into the same folder. The server takes only Zombies' files (`server/gamefiles.go`), so the rest changes nothing: it is not read, and not sent to players.

By default the server sends no game files to players. They use their own copy: Black Ops players import it in the browser, and the launcher finds a Black Ops II or Black Ops III install from Steam. Set `ZOMBIES_DOWNLOAD_RATE` to send the files to players who have none.

A player's copy of Black Ops II or III may lack a map pack. A room on a map of that pack shows the player as missing the map, and its game does not start with them. With `ZOMBIES_DOWNLOAD_RATE` set, such a player can have the launcher download the server's game files, which have every map the server has, and use those instead.

Black Ops II is sent whole, 9 GB. Black Ops III is sent a map at a time: 51 GB that every map needs, and 1 to 7 GB a map, which a player's launcher downloads as they come to the map. The server sends it only where `/game/t7` is the whole install: the log says so at the start when the folder has only what a server reads. `ZOMBIES_SEND_DIR` names another folder to send from, with the whole install as `t7` in it, for a server whose `/game/t7` is kept small. The maps' pictures are then from that folder too. The first start with downloads on reads all of the game once to list it, which takes some minutes.

On Docker Desktop for Windows, keep the game files in a Docker volume. Wine can't load them from a Windows folder.

## What the server downloads

- **The Black Ops engine**, the first time it starts with Black Ops: about 10 MB from the [releases of bo1-zombies](https://github.com/gimme/bo1-zombies/releases), into `/config/engine`. The download is checked against a SHA-256 that is fixed in the server. Later starts download nothing until an update of the server needs another build. `ZOMBIES_ENGINE_URL` sets another address for the same file.
- **Plutonium**, when the first Black Ops II game starts: about 56 MB from Plutonium's updater, into `/config/plutonium`. The server checks for updates before a Black Ops II game, because players can only join a server of the same Plutonium version. It does not check while another Black Ops II game is running: that game keeps the version it started with, and so do the games that start beside it.
- **BOIII**, the first time it starts with Black Ops III: about 15 MB from BOIII's updater, into `/config/boiii`. The server checks for updates before a Black Ops III game, as it does for Plutonium.
- **Black Ops III's fastfile of the day**, the first time the container starts with Black Ops III: a file of 189 kB that the game's dedicated server downloads from the game's publisher, and without which it hosts nothing. The container has the dedicated server fetch it once, before the games' firewall is up, and keeps it in `/config/boiii/lpc`. From then on a Black Ops III game asks no one for it. The file is for one version of the game: after an update of the game, the next start of the container fetches it again. That first start takes half a minute longer and needs the internet. If it fails, the log says so, rooms can't start Black Ops III, and the next start of the container tries again. To have the server ask no one, keep the file with the game files: copy it from `/config/boiii/lpc` into the dedicated server's folder, as `UnrankedServer/identities/dedicatedpc/LPC/core_sv_ffotd_tu32_593.ff`, which is where that server leaves the file when it runs from its own folder. A container whose game files have it fetches nothing, and its log says so.

## HTTPS

Players on other machines need the page over HTTPS. Put a reverse proxy with a certificate in front of TCP 3075, on a host name of its own such as `zombies.example.com`.

- The page must be at the root of the name, not under a path.
- UDP 3075 must reach the server directly at the same name, through a port forward and not through the proxy. On Cloudflare, set the name to DNS only. If the outside UDP port is not 3075, set `ZOMBIES_PUBLIC_PORT`.
- Turn off the proxy's caching of scripts. In Nginx Proxy Manager that is Cache Assets.
- Don't put a password on the proxy. Use Discord sign-in to limit who can play.

## Discord sign-in

With Discord sign-in, only members of your Discord server can play, under their names there.

1. Create an application at <https://discord.com/developers/applications>. Under OAuth2, add the redirect `https://zombies.example.com/auth/discord/callback`, with your page's address.
2. Set `ZOMBIES_DISCORD_CLIENT_ID` and `ZOMBIES_DISCORD_CLIENT_SECRET` to the application's client ID and secret.
3. Set `ZOMBIES_DISCORD_SERVER` to your Discord server's ID. `ZOMBIES_DISCORD_ROLES` limits it to members with one of the listed role IDs.

Without `ZOMBIES_DISCORD_SERVER`, anyone with a Discord account can play. Without any of these settings, anyone who reaches the page can play under a name they choose.

## Settings

Settings are environment variables. `docker run --rm zombies-hub -h` lists all of them.

| Variable | Default | Meaning |
| --- | --- | --- |
| `ZOMBIES_MAP` | `zombie_theater` | The map a new room starts with. |
| `ZOMBIES_MIN_PLAYERS` | `2` | Players a room needs to start a game. |
| `ZOMBIES_MAX_PLAYERS` | `4` | Most players a room can take, up to 8. The maps were made for 4. A Black Ops II room of Grief can take 8 whatever this is: four in each team. |
| `ZOMBIES_MAX_GAMES` | `4` | Games running at once. |
| `ZOMBIES_DOWNLOAD_RATE` | empty | Send game files to players, at most this many bits per second in total (`40M`, `1G`) or `unlimited`. Empty sends none. |
| `ZOMBIES_SEND_DIR` | empty | A folder to send Black Ops III to players from, with the whole install as `t7` in it, where `/game/t7` has only what the server reads. Empty sends from `/game`. |
| `ZOMBIES_PUBLIC_PORT` | `3075` | The UDP port players reach from outside. |
| `ZOMBIES_LOAD_TIMEOUT` | `5m` | How long a game waits for its players to load. |
| `ZOMBIES_HANG_TIMEOUT` | `1m` | How long a game may stop responding before the server ends it. |
| `ZOMBIES_GAME_ARGS` | empty | Extra command line for every Black Ops game, such as `+set g_deadChat 0`. |
| `ZOMBIES_ENGINE_URL` | empty | Another download address for the Black Ops engine. `{version}` is replaced with the release, such as `v1`. |
| `ZOMBIES_BOIII_DIR` | `/config/boiii` | Where the server keeps its copy of BOIII, for Black Ops III. |
| `ZOMBIES_DISCORD_...` | empty | See [Discord sign-in](#discord-sign-in). |
| `PUID`, `PGID` | `1000` | The user and group the server runs as. |

## Console and logs

`docker exec -it <container> console` opens the server's console. A command goes to every running game, or to one room's game when it starts with the room's code (`KXQM status`). `rooms` lists the rooms.

The container's log has the server's messages and every game's output. Each game also has a log file in `/config/logs`.

## Launcher updates

Players install the launcher for Black Ops II and Black Ops III from the [releases of zombies-hub](https://github.com/gimme/zombies-hub/releases) on GitHub. The room's page links to the latest launcher there. The image has no launcher in it.

Each time an installed launcher goes to play, it first looks for a later release of itself. It replaces itself with that release when it is a later version, signed with the key whose public half is `launcher/release.pub`. The player sees nothing of it. Only the project's maintainer and its release workflow have the private half, so neither your server nor GitHub can put another program in the launcher's place.

The server takes only launchers of the versions its code works with. Black Ops III takes a later one than Black Ops II does: the first that starts that game. A launcher updates itself before it asks the server anything, so the server turns away only a launcher that could not: one that can't reach GitHub. That player's room then says that the launcher is too old, with the link to the latest one.

How the launcher is released is in [Development](development.md#releasing-the-launcher).

## Limits

- Rooms and running games are lost when the server restarts. Players and leaderboards are kept.
- The launcher has no Windows code-signing certificate, so Windows and the browser warn about it the first time.
- Players get the launcher from GitHub. Their PCs need to reach github.com to install it and to update it.
- Dead Ops Arcade is not available.
- Black Ops III needs Steam installed on a player's PC, which BOIII asks for, and doesn't run on a Mac. Its rooms have no mods.

## Security

The Black Ops engine's network code is decompiled from a 2010 game and has not been audited. Plutonium's server and BOIII are programs that the server downloads and that this project has not reviewed. The container limits what a compromised game can do:

- Games run as a separate user (uid 3075) that can't read the server's data, such as player keys and the Discord secret.
- With `NET_ADMIN`, a firewall lets that user reach only the server's relay. Without it, a game can reach the network, and the container's log says so. The one exception is the run that fetches Black Ops III's fastfile of the day, before the firewall is up: no player reaches that run.
- The server passes a game only the kinds of packets a client sends, from the players seated in that game.
- All games run as the same user, so a compromised game could reach the other running games.

Players trust the server too. The launcher starts the game files that the server sends it, so it asks before using a server other than the one it was installed from. Of those files it takes a program only when it is one of the game's own, by a list that is built into the launcher. The launcher itself comes only from its signed releases on GitHub, and no server can change it.

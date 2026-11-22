# Hosting

## Run the server

Build the image and start it with Docker Compose:

```sh
make image              # builds zombies:latest
docker compose up -d    # uses compose.yaml
```

The container needs:

- **`/game`**, read-only: the game files ([below](#game-files)).
- **`/config`**: what the server keeps, such as its players, leaderboards, logs and downloads.
- **Port 3075, UDP and TCP.** TCP serves the page. UDP carries the games.
- **`--cap-add NET_ADMIN`**: lets the container firewall the games, so a game can only talk to its players.

The server runs as `PUID`:`PGID` (default 1000:1000) and owns what it writes in `/config`.

The players, the leaderboards and the report of every game are in `/config/zombies.db`, a SQLite database. To back it up, copy the file while the server is stopped.

On Unraid, copy `docker/unraid/my-zombies.xml` to `/boot/config/plugins/dockerMan/templates-user/` and add the container from that template. `make push REGISTRY=<host>` pushes the image to a registry that Unraid can pull from.

## Game files

`/game` has one folder per game, each a copy of the game's install folder. The server hosts the games it finds, and needs at least one.

| Game | Folder |
| --- | --- |
| Black Ops | `/game/t5` |
| Black Ops II | `/game/t6` |

By default the server sends no game files to players. They use their own copy: Black Ops players import it in the browser, and the launcher finds a Black Ops II install from Steam. Set `ZOMBIES_DOWNLOAD_RATE` to send the files to players who have none.

A Black Ops II player's copy may lack a map pack. A room on a map of that pack shows the player as missing the map, and its game does not start with them. With `ZOMBIES_DOWNLOAD_RATE` set, such a player can have the launcher download the server's game files, which have every map the server has, and use those instead.

On Docker Desktop for Windows, keep the game files in a Docker volume. Wine can't load them from a Windows folder.

## What the server downloads

- **The Black Ops engine**, the first time it starts with Black Ops: about 10 MB from the [releases of bo1-zombies](https://github.com/gimme/bo1-zombies/releases), into `/config/engine`. The download is checked against a SHA-256 that is fixed in the server. Later starts download nothing until an update of the server needs another build. `ZOMBIES_ENGINE_URL` sets another address for the same file.
- **Plutonium**, when the first Black Ops II game starts: about 56 MB from Plutonium's updater, into `/config/plutonium`. The server checks for updates before each Black Ops II game, because players can only join a server of the same Plutonium version.

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

Settings are environment variables. `docker run --rm zombies -h` lists all of them.

| Variable | Default | Meaning |
| --- | --- | --- |
| `ZOMBIES_MAP` | `zombie_theater` | The map a new room starts with. |
| `ZOMBIES_MIN_PLAYERS` | `2` | Players a room needs to start a game. |
| `ZOMBIES_MAX_PLAYERS` | `4` | Most players a room can take, up to 8. The maps were made for 4. |
| `ZOMBIES_MAX_GAMES` | `4` | Games running at once. |
| `ZOMBIES_DOWNLOAD_RATE` | empty | Send game files to players, at most this many bits per second in total (`40M`, `1G`) or `unlimited`. Empty sends none. |
| `ZOMBIES_PUBLIC_PORT` | `3075` | The UDP port players reach from outside. |
| `ZOMBIES_LOAD_TIMEOUT` | `5m` | How long a game waits for its players to load. |
| `ZOMBIES_HANG_TIMEOUT` | `1m` | How long a game may stop responding before the server ends it. |
| `ZOMBIES_GAME_ARGS` | empty | Extra command line for every Black Ops game, such as `+set g_deadChat 0`. |
| `ZOMBIES_ENGINE_URL` | empty | Another download address for the Black Ops engine. `{version}` is replaced with the release, such as `v1`. |
| `ZOMBIES_DISCORD_...` | empty | See [Discord sign-in](#discord-sign-in). |
| `PUID`, `PGID` | `1000` | The user and group the server runs as. |

## Console and logs

`docker exec -it <container> console` opens the server's console. A command goes to every running game, or to one room's game when it starts with the room's code (`KXQM status`). `rooms` lists the rooms.

The container's log has the server's messages and every game's output. Each game also has a log file in `/config/logs`.

## Launcher updates

Players install the launcher for Black Ops II from the [releases of zombies-hub](https://github.com/gimme/zombies-hub/releases) on GitHub. The room's page links to the latest launcher there. The image has no launcher in it.

Each time an installed launcher goes to play, it first looks for a later release of itself. It replaces itself with that release when it is a later version, signed with the key whose public half is `launcher/release.pub`. The player sees nothing of it. Only the project's maintainer and its release workflow have the private half, so neither your server nor GitHub can put another program in the launcher's place.

The server takes only launchers of the versions its code works with. A launcher updates itself before it calls the server, so the server turns away only a launcher that could not: one that can't reach GitHub. That player's room then says that the launcher is too old, with the link to the latest one.

How the launcher is released is in [Development](development.md#releasing-the-launcher).

## Limits

- One Black Ops II game runs at a time.
- Rooms and running games are lost when the server restarts. Players and leaderboards are kept.
- The launcher has no Windows code-signing certificate, so Windows and the browser warn about it the first time.
- Players get the launcher from GitHub. Their PCs need to reach github.com to install it and to update it.
- Dead Ops Arcade is not available.

## Security

The Black Ops engine's network code is decompiled from a 2010 game and has not been audited. Plutonium's server is a program that the server downloads and that this project has not reviewed. The container limits what a compromised game can do:

- Games run as a separate user (uid 3075) that can't read the server's data, such as player keys and the Discord secret.
- With `NET_ADMIN`, a firewall lets that user reach only the server's relay. Without it, a game can reach the network, and the container's log says so.
- The server passes a game only the kinds of packets a client sends, from the players seated in that game.
- All games run as the same user, so a compromised game could reach the other running games.

Players trust the server too. The launcher starts the game files that the server sends it, so it asks before using a server other than the one it was installed from. The launcher itself comes only from its signed releases on GitHub, and no server can change it.

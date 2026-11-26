# Zombies Hub

A community hub for playing Call of Duty Zombies together, on a dedicated server. Players open the page, group into rooms and start a co-op game. Results and achievements are kept in leaderboards.

- **Black Ops** plays in the browser (Chrome, WebGPU). Each player imports the game files from their own copy of Black Ops, and the browser keeps them. Imported maps can be played offline too.
- **Black Ops II** plays on [Plutonium](https://plutonium.pw/). The server runs Plutonium's dedicated server, and each player automatically joins from their own PC with the help of a small launcher, which sits in the tray. A Mac with Apple Silicon can play too.
- **Black Ops III** plays on [BOIII](https://github.com/Ezz-lol/boiii-free). The server runs Steam's dedicated server of the game, and each player joins from their own PC with the same launcher.
- The server runs in Docker.

## Quick start

You need Docker, `make`, and at least one of the games installed. Copy `.env.example` to `.env`, and name each game's install folder there. The server hosts the games it finds, and only reads their folders:

| Game | In `.env` | Usually installed in |
| --- | --- | --- |
| Black Ops | `ZOMBIES_T5` | `C:\Program Files (x86)\Steam\steamapps\common\Call of Duty Black Ops` |
| Black Ops II | `ZOMBIES_T6` | `C:\Program Files (x86)\Steam\steamapps\common\Call of Duty Black Ops II` |
| Black Ops III | `ZOMBIES_T7` | `C:\Program Files (x86)\Steam\steamapps\common\Call of Duty Black Ops III` |
| Black Ops III's dedicated server | `ZOMBIES_T7_SERVER` | `C:\Program Files (x86)\Steam\steamapps\common\Call of Duty Black Ops III 545990` |

Black Ops III needs both of its folders. The dedicated server is a tool that Steam gives to who has the game: in Steam's library, show Tools, and install Call of Duty: Black Ops III - Unranked Dedicated Server.

```sh
# runs the server on port 3075
make dev
```

Then open <http://localhost:3075/> in Chrome. On Windows, run it in Git Bash with Docker Desktop started.

The server downloads the rest by itself: the Black Ops engine ([bo1-zombies](https://github.com/gimme/bo1-zombies)) the first time it starts with Black Ops, Plutonium when the first Black Ops II game starts, and BOIII the first time it starts with Black Ops III.

## Documentation

- [Playing](docs/playing.md): what players need and how to join a game.
- [Hosting](docs/hosting.md): running the server for others, with HTTPS, Discord sign-in and its settings.
- [Development](docs/development.md): the repo's layout, running and testing.

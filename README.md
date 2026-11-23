# Zombies Hub

A community hub for playing Call of Duty Zombies together, on a dedicated server. Players open the page, group into rooms and start a co-op game. Results and achievements are kept in leaderboards.

- **Black Ops** plays in the browser (Chrome, WebGPU). Each player imports the game files from their own copy of Black Ops, and the browser keeps them. Imported maps can be played offline too.
- **Black Ops II** plays on [Plutonium](https://plutonium.pw/). The server runs Plutonium's dedicated server, and each player automatically joins from their own PC with the help of a small launcher, which sits in the tray. A Mac with Apple Silicon can play too.
- The server runs in Docker.

## Quick start

You need Docker, `make`, and a copy of at least one of the games. The server hosts the games whose files it finds:

| Game | Copy its install folder to | Usually installed in |
| --- | --- | --- |
| Black Ops | `game/t5/` | `C:\Program Files (x86)\Steam\steamapps\common\Call of Duty Black Ops` |
| Black Ops II | `game/t6/` | `C:\Program Files (x86)\Steam\steamapps\common\Call of Duty Black Ops II` |

```sh
# runs the server on port 3075
make dev
```

Then open <http://localhost:3075/> in Chrome. On Windows, run it in Git Bash with Docker Desktop started.

The server downloads the rest by itself: the Black Ops engine ([bo1-zombies](https://github.com/gimme/bo1-zombies)) the first time it starts with Black Ops, and Plutonium when the first Black Ops II game starts.

## Documentation

- [Playing](docs/playing.md): what players need and how to join a game.
- [Hosting](docs/hosting.md): running the server for others, with HTTPS, Discord sign-in and its settings.
- [Development](docs/development.md): the repo's layout, running and testing.

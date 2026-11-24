# Playing

Open the server's page in Chrome. Make a room, or join one from the list or with its code. The host picks the game, Black Ops or Black Ops II, then a map of it. The others can suggest a map with a click on it. When everyone has pressed Ready, the game starts.

The page offers only the games the server hosts, and a server of one game has nothing to pick. The list of rooms says each room's game, and each game has its own leaderboards.

## Black Ops

Black Ops runs in the browser tab.

- **Browser:** Chrome, or another browser with WebGPU.
- **Game files:** you need your own copy of Black Ops. Open the menu under your name, and under Black Ops game folder choose the game's install folder. Each map then has an Import button. The browser keeps the imported files, and nothing from the folder is sent to the server. If the host has turned on downloads, the button says Download and no copy is needed.
- **Settings:** Black Ops settings, under Ready in a Black Ops room and under Play when you play offline, opens the game's graphics, sound and controls. Your next game in this browser starts with them. In a game, the game's own menu has them.
- **Mouse and full screen:** a click captures the mouse and goes full screen. Esc opens the game's menu. Hold Esc or press F11 to leave full screen.
- **Gamepads:** Xbox and PlayStation controllers work. Press any button to turn the gamepad on. Start opens the settings where the page has them.
- **Play offline:** plays a map solo in the browser. It also works without the server, for maps you have imported.
- **Dropped from a game:** rejoin within 5 minutes to get your character back.

## Black Ops II

Black Ops II runs on your Windows PC, on [Plutonium](https://plutonium.pw/). The Zombies Launcher starts it for you and joins your room's game. A Mac with Apple Silicon can play too: see [On a Mac](#on-a-mac).

- **Game files:** Black Ops II installed from Steam, or a copy of it anywhere on the PC. If the launcher doesn't find it, it asks: choose Choose folder… and pick the game's folder, the one with `t6zm.exe` in it. If the host has turned on downloads, the launcher can download the files instead (about 9 GB), into its own folder or one you choose.
- **Modes:** the host picks how the room plays in the room's settings, under Mode: Co-op, Grief or Turned. The maps below are those of the mode, and each map's card says its mode after its name. In Co-op, everyone plays together against the zombies, on a map's story or on Survival. In Grief (Bus Depot, Town, Farm, Cell Block, Borough), two teams survive the same rounds, and a team wins once every player of the other is down and it lives to the end of that round. The room shows each player's team: the first half of the room is team 1. A Grief room can take up to 8 players, four in each team. In Turned (Diner, Borough), one player is human and everyone else a zombie: whoever kills the human takes the cure, and the player with the most points when time runs out wins. A room of Grief or Turned needs at least 2 players to make sense, and its games count for no leaderboard: the room's report says who won.
- **Map packs:** a map of a pack you don't have shows you as Missing map, and you can't be ready until the host picks another map. If the host has turned on downloads, the room's button says Download: the launcher then asks whether to download the server's game files (about 9 GB) and use them instead. Your own copy stays as it is, and Choose Black Ops II folder… in the launcher's menu goes back to it.
- **First time:** install the launcher with the link under the room's button. The link is there until the launcher has run from your browser, and again when Connect starts nothing. It downloads from GitHub. Windows does not know the program and asks whether to run it: choose More info, then Run anyway.
- **Each game:** if the room's button says Connect, press it. The launcher brings Plutonium up to date, checks the game files and stays in the tray. Then press Ready. When the room's game starts, the launcher starts your game and joins it. You don't need to install Plutonium or sign in to it.
- **The next game:** leave your game open at its menu when a game ends. When the room's next game starts, your game joins it without starting again. The launcher restarts your game instead when Plutonium has been updated, or when you chose another game folder in its menu.
- **Updates:** the launcher updates itself from GitHub. If the room says that your launcher is too old, install the new one with the link there, then press Connect.
- **Plutonium:** the launcher keeps Plutonium in Plutonium's own folder, `%LOCALAPPDATA%\Plutonium`, where Plutonium's own launcher (`plutonium.exe`, from its site) has it too. So Plutonium is not part of the Zombies Launcher: a PC that has Plutonium already keeps it, and Plutonium stays when the launcher is uninstalled.
- **Play offline:** the lobby's Play offline has Black Ops II too, behind a switch where the server hosts both games. The switch shows the game you last picked or were last in a room of, as a new room and the leaderboards do. Launch game starts the game on your PC through Plutonium, and you pick a map in the game. It needs no room, and no Plutonium account. The launcher plays with the Plutonium the PC has, whatever its version, and gets Plutonium only when the PC has none yet. So once the PC has played, it plays solo with no internet. Where the browser can't reach the server either, its copy of the page still has the button: the game then starts from the game files the PC has.
- **Join by hand:** check Join by hand under the room's button to join from your game's console instead, in a game that Plutonium started in LAN mode. The room shows the line to type there. The launcher then leaves your game alone, even when it still runs from an earlier game.
- **The launcher's icon in the tray:** its menu shows which game folder the launcher uses. Choose Black Ops II folder… points it at another copy, from the game's next start. Delete downloaded Black Ops II files… frees the space of files the launcher downloaded.
- **Your colour:** the game shows your points in the colour of your ring in the room, while everyone listed before you in the room is in the game. For that, the games join in the room's order: yours may stay at Connecting for up to 30 seconds while it waits for the game of a player before you.
- **Same PC:** keep the room's page open on the PC you play on. The server matches your game to your page by address.

If something goes wrong:

- **The launcher doesn't find your Black Ops II, or uses the wrong copy:** click its icon in the tray, choose Choose Black Ops II folder… and pick the game's folder. A folder above it works too, such as a Steam library.
- **"Black Ops II can't run from that folder":** the folder's path has a character other than A to Z, such as é or я. Move the game to a folder such as `D:\Games\Black Ops II` and choose that one.
- **A file is missing (`d3dx9_43.dll`, `XINPUT1_3.dll`, `MSVCR100.dll`):** run `redist\DirectX\DXSETUP.exe` and `redist\vcredist_x86.exe` from the game's folder, once.
- **"Your game is outdated":** press Join the game again.
- **Antivirus removes Plutonium's files:** add an exception for `%LOCALAPPDATA%\Plutonium`.
- **Anything else:** the launcher's log is `%LOCALAPPDATA%\Zombies Launcher\launcher.log`.

To remove the launcher, uninstall Zombies Launcher in Windows' Settings under Apps. It asks whether to delete the game files that it downloaded too. Plutonium stays: to remove it, delete its folder.

### On a Mac

Black Ops II is a Windows game. On a Mac with Apple Silicon (M1 or later), the launcher runs it in Wine, with [plutonium-mac-aio](https://github.com/LuidgiAlt/plutonium-mac-aio). An Intel Mac can't play it. What is said above of the launcher holds on a Mac too, but for this:

- **First time:** install the launcher with the line under the room's button: paste it into Terminal. The ? by the line says what it does. It installs the launcher, in `Library/Application Support/Zombies Launcher` of your home folder, and what the game runs on: plutonium-mac-aio, in the folder `plutonium-mac-aio` of your home folder, with its Wine and Plutonium. A Mac that has plutonium-mac-aio there already keeps its own. That takes a few minutes, most of it downloads, and ends with "Zombies Launcher successfully installed." From then on the room's Connect button starts the launcher. The line fetches a script from GitHub, and you need the script for nothing more. A click on the line selects it. The room has no button that copies it, because a content blocker would warn of one.
- **Game files:** the Mac needs the files of the Windows game. Where the host has turned on downloads, choose Download when the launcher asks. Otherwise copy the game's folder from a Windows PC to the Mac, and run the script's `folder <the folder>` command in Terminal (below).
- **What macOS asks:** Chrome asks whether to open Zombies Launcher. When the server is on your own network, macOS asks whether Zombies Launcher may find devices on local networks: allow it, or the launcher can't reach the server. As the game starts, macOS asks whether it may use the microphone, which is for the game's voice chat and not required.
- **The game's window:** it opens behind the browser, so switch to it with Command+Tab. The first time it is a window: the game's options have full screen, and the game remembers it.
- **The launcher's icon:** it is in the menu bar, where a PC has the tray.
- **Updates:** the launcher updates itself, but not what the line installed around it: the script that runs it, and plutonium-mac-aio. When a new launcher needs those newer, or the server needs a newer launcher, the room says that your launcher needs an update. Paste the room's line into Terminal again.
- **If something goes wrong:** the script has commands for Terminal, each after `bash ~/Library/Application\ Support/Zombies\ Launcher/zombies-launcher-mac.sh`. `log` shows the launcher's log and Wine's, and `stop` closes the launcher and the game.

To remove the launcher, open Zombies Launcher in the Applications folder of your home folder, and choose Uninstall. It asks first, and says how large the game files are that the launcher downloaded, which go with it. plutonium-mac-aio stays, with Plutonium, as Plutonium stays on a PC: to remove it too, delete the folder `plutonium-mac-aio` in your home folder. Nothing of the launcher's is in it. Without it the room's Connect starts nothing, until you paste the room's line into Terminal again, which downloads plutonium-mac-aio again: the launcher's settings and the game files that it downloaded are still there. Don't move the folder: Wine then starts no game.

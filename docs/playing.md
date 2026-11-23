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
- **Play offline:** plays a map alone in the browser. It also works without the server, for maps you have imported.
- **Dropped from a game:** rejoin within 5 minutes to get your character back.

## Black Ops II

Black Ops II runs on your Windows PC, on [Plutonium](https://plutonium.pw/). The Zombies Launcher starts it for you and joins your room's game.

- **Game files:** Black Ops II installed from Steam, or a copy of it anywhere on the PC. If the launcher doesn't find it, it asks: choose Choose folder… and pick the game's folder, the one with `t6zm.exe` in it. If the host has turned on downloads, the launcher can download the files instead (about 9 GB), into its own folder or one you choose.
- **Map packs:** a map of a pack you don't have shows you as Missing map, and you can't be ready until the host picks another map. If the host has turned on downloads, the room's button says Download: the launcher then asks whether to download the server's game files (about 9 GB) and use them instead. Your own copy stays as it is, and Choose Black Ops II folder… in the launcher's menu goes back to it.
- **First time:** install the launcher with the link under the room's button. The link is there until the launcher has run from your browser, and again when Connect starts nothing. It downloads from GitHub. Windows does not know the program and asks whether to run it: choose More info, then Run anyway.
- **Each game:** if the room's button says Connect, press it. The launcher brings Plutonium up to date, checks the game files and stays in the tray. Then press Ready. When the room's game starts, the launcher starts your game and joins it. You don't need to install Plutonium or sign in to it.
- **The next game:** leave your game open at its menu when a game ends. When the room's next game starts, your game joins it without starting again. The launcher restarts your game instead when Plutonium has been updated, or when you chose another game folder in its menu.
- **Updates:** the launcher updates itself from GitHub. If the room says that your launcher is too old, install the new one with the link there, then press Connect.
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

To remove the launcher, uninstall Zombies Launcher in Windows' Settings under Apps.

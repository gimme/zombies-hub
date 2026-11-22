# Playing

Open the server's page in Chrome. Make a room, or join one from the list or with its code. The host picks the map. When everyone has pressed Ready, the game starts.

A room shows only the games the server hosts.

## Black Ops

Black Ops runs in the browser tab.

- **Browser:** Chrome, or another browser with WebGPU.
- **Game files:** you need your own copy of Black Ops. Open the menu under your name, choose Game folder and select the game's install folder. Each map then has an Import button. The browser keeps the imported files, and nothing from the folder is sent to the server. If the host has turned on downloads, the button says Download and no copy is needed.
- **Mouse and full screen:** a click captures the mouse and goes full screen. Esc opens the game's menu. Hold Esc or press F11 to leave full screen.
- **Gamepads:** Xbox and PlayStation controllers work. Press any button to turn the gamepad on.
- **Play offline:** plays a map alone in the browser. It also works without the server, for maps you have imported.
- **Dropped from a game:** rejoin within 5 minutes to get your character back.

## Black Ops II

Black Ops II runs on your Windows PC, on [Plutonium](https://plutonium.pw/). The Zombies Launcher starts it for you and joins your room's game.

- **Game files:** Black Ops II installed from Steam. If the host has turned on downloads, the launcher can download the files instead (about 9 GB). It asks before the first download.
- **First time:** install the launcher with the link under the Ready button. Windows does not know the program and asks whether to run it: choose More info, then Run anyway.
- **Each game:** if the room's button says Connect, press it. The launcher brings Plutonium up to date, checks the game files and stays in the tray. Then press Ready. When the room's game starts, the launcher starts your game and joins it. You don't need to install Plutonium or sign in to it.
- **Same PC:** keep the room's page open on the PC you play on. The server matches your game to your page by address.

If something goes wrong:

- **A file is missing (`d3dx9_43.dll`, `XINPUT1_3.dll`, `MSVCR100.dll`):** run `redist\DirectX\DXSETUP.exe` and `redist\vcredist_x86.exe` from the game's folder, once.
- **"Your game is outdated":** press Join the game again.
- **Antivirus removes Plutonium's files:** add an exception for `%LOCALAPPDATA%\Plutonium`.
- **Anything else:** the launcher's log is `%LOCALAPPDATA%\Zombies Launcher\launcher.log`.

To remove the launcher, uninstall Zombies Launcher in Windows' Settings under Apps.

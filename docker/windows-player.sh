#!/bin/sh
# docker/windows-player.sh - run a Windows player, for the game tests (e2e/game/smoke/windows.spec.ts).
#
#   windows-player.sh <home> <host>[:port] <key>
#
# It runs BO1Zombies.exe, the Windows client, under Wine on a virtual display (Xvfb at $DISPLAY, which Mesa draws on
# the CPU). The client plays its player's room's game on a server, with the settings of bo1-zombies/Play.bat. <key> is
# the player's key (server/rooms.go). The client waits for the room's game to start.
#
# The script runs in the image (docker/Dockerfile, target windows-player), with the engine build in /engine and the
# game files in /game/t5, the same ones the server has. <home> is the client's fs_h: it has the client's settings, and
# its log in main/console_mp.log. The script ends when the client does, with the client's exit code. To take a
# screenshot, run docker exec <container> import -window root <file>.png. Mesa compiles each of the game's shaders the
# first time it is drawn, and the game waits for it. That takes minutes in all, the first time. MESA_SHADER_CACHE_DIR
# keeps the compiled shaders for the next run.
set -eu
[ $# = 3 ] || { echo "usage: windows-player.sh <home> <host>[:port] <key>" >&2; exit 2; }
home=$1 server=$2 key=$3
mkdir -p "$home"
Xvfb "$DISPLAY" -screen 0 1280x720x24 -nolisten tcp &
for _ in $(seq 100); do
    [ -S "/tmp/.X11-unix/X${DISPLAY#:}" ] && break
    kill -0 $! 2>/dev/null || { echo "windows-player: Xvfb ended" >&2; exit 1; }
    sleep 0.1
done
cd /game/t5
exec wine /engine/bin/BO1Zombies.exe +set bo1_zombies 1 +set fs_b 'Z:\game\t5' +set fs_h "Z:$(printf %s "$home" | tr / '\\')" \
    +set logfile 2 +set com_introPlayed 1 +set r_fullscreen 0 +set r_borderless 1 +set vid_xpos 0 +set vid_ypos 0 \
    +set r_vsync 0 +set com_maxfps 0 +set cl_maxpackets 250 +set r_mode 1280x720 +set bo1_key "$key" +connect "$server"

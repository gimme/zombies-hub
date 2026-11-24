#!/bin/bash
# launcher/mac.sh - Zombies Launcher on a Mac with Apple Silicon: the script installs the launcher there, and runs it.
# A release of the launcher has the script as zombies-launcher-mac.sh (.github/workflows/launcher.yml). A room's page
# shows a Mac the line that installs the launcher, for the player to paste into Terminal (web/src/windows.ts):
#
#   /bin/bash -c "$(curl -fsSL https://github.com/gimme/zombies-hub/releases/download/launcher/zombies-launcher-mac.sh)"
#
# The line fetches the script from the latest release and runs it, which installs the launcher (setup). From then on
# the room's button starts the launcher, as on Windows, and the player needs the script for nothing more. Its other
# commands are for when something goes wrong. It has run on one Mac, an M3 Pro, in November 2026. The commands, each
# after "bash ~/Library/Application\ Support/Zombies\ Launcher/zombies-launcher-mac.sh":
#
#   setup [<Black Ops II folder>]   install the launcher, or bring it up to date; with no command, the script does this
#   folder <Black Ops II folder>    point the launcher at a copy of the game
#   link <link>                     start the launcher with a link, as a room's Connect button does
#   game [<name>]                   start Black Ops II by hand, without the launcher
#   check <host>[:<port>]           see whether the launcher reaches a server, from Terminal and through the app
#   log                             show the end of the launcher's log and of Wine's
#   stop                            close the launcher, the game and Wine
#   uninstall                       remove the launcher
#
# The launcher is a Windows program, like the game (main.go). On a Mac both run in the Wine of plutonium-mac-aio
# (https://github.com/LuidgiAlt/plutonium-mac-aio), which is patched so that Plutonium's games run under Rosetta. The
# launcher has to run in the game's own Wine: it starts the game with Windows' CreateProcess and types into the
# game's console (windows.go), and it keeps Plutonium where that Wine has it, in %LOCALAPPDATA%\Plutonium.
#
# plutonium-mac-aio is not the launcher's, as Plutonium is not the launcher's on Windows. It is in a folder of its
# own, ~/plutonium-mac-aio, which is where the lines of its README put it from a new Terminal window. It works there
# without the launcher, and it stays when the launcher is removed (uninstall). setup uses the one that the Mac has.
# On a Mac that has none, setup downloads it, at the commit below. Its own setup.sh then downloads its Wine and makes
# the Wine prefix, prefix/. setup tells it to leave Plutonium out (AIO_SKIP_PLUTONIUM): its way to get Plutonium asks
# for a Plutonium account, and the launcher downloads Plutonium itself, as on Windows.
#
# Of the launcher, setup makes:
#   - the folder ~/Library/Application Support/Zombies Launcher, which is where a Mac's apps keep what is theirs. All
#     that is the launcher's is in it: its program, its config and log, the game files that it downloads, a copy of
#     this script, which the app below runs (keep_script), and Wine's log. The program is the launcher's latest
#     release from GitHub. Between two runs of setup it updates itself (update.go). ZOMBIES_LAUNCHER_EXE=<file> puts
#     another build there instead, such as a dev build (docker/launcher.sh).
#   - a link to that folder in the prefix, as %LOCALAPPDATA%\Zombies Launcher, which is where the launcher has its
#     folder on a Windows PC and looks for it (link_folder). So nothing of the launcher's is in plutonium-mac-aio's
#     folder but that link, and a player who removes plutonium-mac-aio keeps the game files.
#   - ~/Applications/Zombies Launcher.app. macOS gives a link of the launcher's scheme only to an app, so the app
#     takes the link from the browser and runs the script's link command with it. Opened by hand, the app says where
#     it starts from, and offers to remove the launcher (uninstall), so that a player needs no Terminal for that.
#
# The game needs Black Ops II's Windows files on the Mac: a copy of the game's folder from a Windows PC, which setup
# and folder take, or the server's files, which the launcher downloads where the server sends them. setup also looks
# where Steam for Mac would have the game.
#
# link starts the launcher in the prefix with the settings that the game needs (wine_env, profile). Those are
# plutonium-mac-aio's own, from its lib/env.sh and launch.sh at the commit below. Its launch.sh can't be used as it
# is: it starts Plutonium's own launcher, and first closes whatever runs in the prefix. When the commit below
# changes, wine_env and profile have to follow those two files. X87=0 turns off its faster x87 maths, as it does
# for its launch.sh.
#
# game is for telling a fault of the launcher from a fault of the game: it starts the game in LAN mode in this
# Terminal, with no launcher. Plutonium has to be there, which the launcher downloads on its first run. To join a
# room's game then, check Join by hand in the room and type its line into the game's console.
set -eu
set -o pipefail

# The script's version, which it gives the launcher as it starts it (wine_env). The launcher updates itself and the
# script does not, so a launcher that needs a later script ends, and the room shows the line that runs the latest one
# (launcher/mac.go). Raise it, and macNeeds there, when a change here is one that the launcher or the game needs, as
# another commit of plutonium-mac-aio is.
mac_version=1
# plutonium-mac-aio, pinned: setup runs its scripts and its Wine, and those check their own downloads by hash.
aio_repo=https://github.com/LuidgiAlt/plutonium-mac-aio
aio_commit=0b2a40463a2e4f22c43ca547329658bb4ea7cec4
# The launcher's latest release (internal/launcherapi: Releases, Current), which has the launcher (Setup), this script
# (MacSetup) and the launcher's icon, and the scheme of the launcher's links (Scheme). ZOMBIES_LAUNCHER_RELEASE is
# another place that has the release's files, for a test.
latest=${ZOMBIES_LAUNCHER_RELEASE:-https://github.com/gimme/zombies-hub/releases/download/launcher}
setup_exe=$latest/zombies-launcher-setup.exe
name=zombies-launcher-mac.sh
script_url=$latest/$name
icon_url=$latest/zombies-launcher.png
scheme=zombies-launcher
app_name="Zombies Launcher" # the launcher's name, and its folder's (update.go: appName)
# What macOS shows when it asks whether the app may use the microphone. An AppleScript app's own text says that the
# script needs it to run, which the game does not.
microphone="Black Ops II uses the microphone for voice chat. Not required."
exe_name=zombies-launcher.exe

home="$HOME/Library/Application Support/$app_name"
mark=$home/.zombies-launcher-mac # shows that setup made the folder, for uninstall
# plutonium-mac-aio's folder. It has to stay where it is once its Wine has run. Wine starts each program through a
# folder in the Mac's temporary folder, which it names after the loader's file (inode, size and time), not after
# where the file is, and which has a link to the loader's ntdll.so by its full path (CrossOver's hack 22144, in
# dlls/ntdll/unix/loader.c). After a move the link leads nowhere, and Wine starts no program from another: "wine:
# could not load ntdll.so". It does again once that folder is removed: rm -rf "$TMPDIR"winetemp-*
aio_name=plutonium-mac-aio
aio=$HOME/$aio_name
engine=$aio/runner/engine
prefix=$aio/prefix
app=$HOME/Applications/$app_name.app
wine_log=$home/wine.log
# script is the copy of this script in the launcher's folder, which the app runs and a message names. self is the
# file that runs now, or "" for a script that is no file: the text that "bash -c" was given, or a pipe's.
script=$home/$name
self=${BASH_SOURCE[0]:-}
if [ -n "$self" ] && [ -f "$self" ]; then
    self=$(cd "$(dirname "$self")" && pwd)/$(basename "$self")
else
    self=
fi
this=${self:-$script} # the copy to run a command of, as a message says it
lsregister=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
plistbuddy=/usr/libexec/PlistBuddy
# Wine's two libraries for .NET and for HTML, turned off (WINEDLLOVERRIDES). As Wine makes or updates a prefix, it
# asks in a dialog whether to download Mono and Gecko for them. The dialog opens behind the other windows, and Wine
# waits there unseen. With the libraries off it does not ask. Neither the launcher nor the game uses them.
no_mono="mscoree,mshtml="

say() { echo "$app_name: $*"; }
die() { echo "$app_name: $*" >&2; exit 1; }

# usage prints the commands, from the list at the top of the script's file.
usage() {
    local file=${self:-$script}
    echo "The commands, each after: bash $script" >&2
    if [ -f "$file" ]; then
        sed -n '/^#   setup /,/^#   uninstall /s/^#   /  /p' "$file" >&2
    else
        echo "  setup, folder, link, game, check, log, stop, uninstall" >&2
    fi
    exit 2
}

# local_appdata prints the prefix's %LOCALAPPDATA%: the folder of its Windows user, whom plutonium-mac-aio's Wine
# names crossover.
local_appdata() {
    local d
    for d in "$prefix/drive_c/users/crossover" "$prefix"/drive_c/users/*; do
        case "$d" in */Public) continue ;; esac
        if [ -d "$d/AppData/Local" ]; then
            echo "$d/AppData/Local"
            return 0
        fi
    done
    return 1
}

# folders sets the folders in the prefix: the launcher's own and Plutonium's. The prefix has to be there. The
# launcher's is a link to its folder on the Mac (link_folder). The script names the launcher's files by the link, a
# path on Wine's drive C: the launcher takes its program for the installed one by that path (update.go).
folders() {
    local data
    [ -f "$prefix/system.reg" ] || die "this Mac is not set up yet. Run: bash $this setup"
    data=$(local_appdata) || die "the Wine prefix has no Windows user. Run: bash $this setup"
    launcher_dir=$data/$app_name
    plutonium=$data/Plutonium
}

# link_folder makes the launcher's folder in the prefix a link to the launcher's folder on the Mac, where its files
# are. A folder with files in it is not replaced: the files would be the launcher's.
link_folder() {
    if [ -d "$launcher_dir" ] && [ ! -L "$launcher_dir" ]; then
        rmdir "$launcher_dir" 2> /dev/null ||
            die "$launcher_dir has files in it. Move them to $home, remove the folder, and run setup again."
    fi
    ln -sfn "$home" "$launcher_dir" || die "the launcher's folder could not be linked into the prefix"
}

# ready checks that setup ran to its end, and sets the folders.
ready() {
    folders
    [ -x "$engine/lib/wine/x86_64-unix/wine" ] && [ -f "$launcher_dir/$exe_name" ] ||
        die "this Mac is not set up yet. Run: bash $this setup"
}

# wine_env sets the environment that Wine and the game need: what plutonium-mac-aio's aio_env sets (lib/env.sh), and
# the switches that its launch.sh has on by default (apply_switches). A program that the launcher starts gets them
# from the launcher.
wine_env() {
    export WINEPREFIX="$prefix"
    export WINESERVER="$engine/bin/wineserver"
    export WINEDLLPATH="$engine/lib/wine/x86_64-windows:$engine/lib/wine/x86_64-unix"
    export PATH="$engine/bin:/usr/bin:/bin:/usr/sbin:/sbin"
    export WINEMSYNC=1
    export WINEDEBUG="${WINEDEBUG:--all}"
    # Wine updates the prefix by itself at its next start after another commit's Wine, and would ask then (no_mono).
    export WINEDLLOVERRIDES="$no_mono"
    # For the launcher: Wine passes the Mac's variables on to the programs it runs (dlls/ntdll/unix/env.c).
    export ZOMBIES_LAUNCHER_MAC=$mac_version
    # MoltenVK, which Wine loads for each program, prints only its errors. It otherwise lists the GPU's features each
    # time, a hundred lines in wine.log.
    export MVK_CONFIG_LOG_LEVEL=1
    # The launcher is a Go program. To switch a busy thread to another task, Go suspends the thread and changes where
    # it runs on (SuspendThread, SetThreadContext). In this Wine that crashed the launcher seven seconds into a
    # download, in Go's scheduler ("unexpected signal during runtime execution"), with the thread that hashed the
    # download interrupted so. With this, Go switches tasks only where the program lets it, and the download ran on.
    export GODEBUG=asyncpreemptoff=1
    # Plutonium needs debug registers and execution breakpoints, which Rosetta has only with these.
    export WINE_ROSETTA_EXEC_BREAKPOINTS=1 WINE_ROSETTA_WOW64_BREAKPOINTS=1 WINE_ROSETTA_CALL_WORKAROUND=1
    export WINE_ROSETTA_LAZY_EXEC=1
    # The game does its physics in x87 maths, which Rosetta translates slowly. Wine starts the program that the game
    # runs in under plutonium-mac-aio's accelerator.
    if [ "${X87:-1}" != 0 ]; then
        export WINE_X87_SIDECAR=plutonium-bootstrapper-win32.exe
    else
        unset WINE_X87_SIDECAR
    fi
    wine=$engine/lib/wine/x86_64-unix/wine
}

# profile keeps the game from starting with the graphics profile of another PC. Black Ops II reads its anti-aliasing
# from a binary profile. Without one in Plutonium's folder it takes the one in the game's folder, and a profile from a
# Windows PC may ask for 8x, which an Apple GPU can't do: the game then crashes as it starts. A profile of zeros in
# Plutonium's folder makes the game detect its own instead, and replace it (plutonium-mac-aio's lib/games.sh:
# bo2_prepare).
profile() {
    local mode file
    mkdir -p "$plutonium/storage/t6/players"
    for mode in mp zm; do
        file=$plutonium/storage/t6/players/hardware_$mode.chp
        [ -f "$file" ] || head -c 4096 /dev/zero > "$file"
    done
}

# win_path prints a Mac folder as a program in Wine sees it: Wine's drive Z is the Mac's whole disk.
win_path() { printf 'Z:%s\n' "$1" | tr / '\\'; }

# has_t6 is the launcher's own test of a copy of Black Ops II (files.go: hasT6).
has_t6() { [ -f "$1/zone/all/patch_zm.ff" ] && [ -f "$1/binkw32.dll" ]; }

# game_folder prints dir as a full path, when it is a copy of Black Ops II that the game can read. The game reads no
# file under a folder with a letter other than plain ASCII (files.go: plain).
game_folder() {
    local dir=$1
    [ -d "$dir" ] || die "there is no folder $dir"
    dir=$(cd "$dir" && pwd)
    has_t6 "$dir" || die "$dir is not Black Ops II's folder. It is the folder with t6zm.exe and zone in it."
    if printf %s "$dir" | LC_ALL=C grep -q '[^ -~]'; then
        die "the game can't read $dir: the path has a character other than A to Z. Move the game's folder."
    fi
    echo "$dir"
}

# set_folder makes dir, which game_folder printed, the player's own copy of the game: it puts the folder into the
# launcher's config as "t6" (config.go), which is launcher.json as Go writes it, a member to a line. It keeps the
# config's other members. It also notes the folder for game.
set_folder() {
    local dir=$1 value file=$launcher_dir/launcher.json
    value=$(win_path "$dir" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g') # as a JSON string
    mkdir -p "$launcher_dir"
    if [ -f "$file" ] && [ "$(sed -n '1p' "$file")" = "{" ] && [ "$(sed -n '$p' "$file")" = "}" ]; then
        T6=$value awk '
            { line[NR] = $0 }
            END {
                n = 0
                for (i = 2; i < NR; i++) if (line[i] !~ /^  "t6": /) keep[++n] = line[i]
                print "{"
                printf "  \"t6\": \"%s\"%s\n", ENVIRON["T6"], (n ? "," : "")
                for (i = 1; i <= n; i++) {
                    if (i == n) sub(/,$/, "", keep[i])
                    print keep[i]
                }
                print "}"
            }' "$file" > "$file.mac"
    else
        printf '{\n  "t6": "%s"\n}\n' "$value" > "$file.mac"
    fi
    mv -f "$file.mac" "$file"
    printf '%s\n' "$dir" > "$home/t6-folder"
    say "Black Ops II: $dir"
}

# t6_folder prints the game folder that game starts from: the one that set_folder noted, or else the game files that
# the launcher downloaded from a server (files.go: downloads).
t6_folder() {
    local dir
    dir=$(cat "$home/t6-folder" 2>/dev/null || true)
    if [ -n "$dir" ] && has_t6 "$dir"; then
        echo "$dir"
    elif has_t6 "$launcher_dir/t6"; then
        echo "$launcher_dir/t6"
    else
        return 1
    fi
}

# get_aio downloads plutonium-mac-aio at its commit into its folder, on a Mac that has none there. A plutonium-mac-aio
# that the player put there stays as it is: they keep it up to date, as its README says. One that this script
# downloaded has the commit in .commit. When the commit above changes, the script downloads the new one over its
# files. What its setup made there stays: its Wine, the prefix and its downloads. The mark is there before the files,
# so that a download that failed halfway is not taken for the player's own.
get_aio() {
    if [ -f "$aio/setup.sh" ] && [ ! -f "$aio/.commit" ]; then
        return 0
    fi
    if [ "$(cat "$aio/.commit" 2>/dev/null || true)" = "$aio_commit" ]; then
        return 0
    fi
    say "downloading plutonium-mac-aio into $aio"
    mkdir -p "$aio"
    echo "not yet" > "$aio/.commit"
    curl -fsSL --retry 2 --connect-timeout 20 "$aio_repo/archive/$aio_commit.tar.gz" |
        tar -xzf - -C "$aio" --strip-components 1 || die "plutonium-mac-aio could not be downloaded"
    [ -f "$aio/setup.sh" ] || die "the download of plutonium-mac-aio has no setup.sh"
    echo "$aio_commit" > "$aio/.commit"
}

# aio_setup runs plutonium-mac-aio's setup, without Plutonium, and without Wine's question as it makes the prefix
# (no_mono). It leaves out the lines that tell a player of plutonium-mac-aio what to do next, which is not what a
# player of this script does.
aio_setup() {
    say "running plutonium-mac-aio's setup: the first time it downloads its Wine, which takes a while"
    AIO_SKIP_PLUTONIUM=1 WINEDLLOVERRIDES="$no_mono" /bin/bash "$aio/setup.sh" 2>&1 | {
        grep --line-buffered -v -e '^In the Plutonium launcher' -e '^  bo[12]: ' -e '^setup done: start with' \
            -e '^warning: BO[12] not found' -e '^xaudio: BO1 not found' || true
    } || die "plutonium-mac-aio's setup failed. Its log is $aio/logs/setup.log"
    [ -x "$engine/lib/wine/x86_64-unix/wine" ] || die "plutonium-mac-aio's setup left no Wine in $engine"
}

# keep_script puts this script into the launcher's folder, where the app runs it. The script is a file, or it is the
# text that "bash -c" was given, which is how the line of a room's page runs it. A script that reached bash another
# way, as through a pipe, is fetched again from where that line has it.
keep_script() {
    if [ -n "$self" ]; then
        if [ "$self" != "$script" ]; then
            cp "$self" "$script.part"
        fi
    elif [ -n "${BASH_EXECUTION_STRING:-}" ]; then
        printf '%s\n' "$BASH_EXECUTION_STRING" > "$script.part"
    else
        curl -fsSL --retry 2 --connect-timeout 20 -o "$script.part" "$script_url" || {
            rm -f "$script.part"
            die "the launcher's script could not be downloaded from $script_url"
        }
    fi
    if [ -f "$script.part" ]; then
        mv -f "$script.part" "$script"
    fi
}

# get_icon fetches the launcher's icon, for the app (make_app). Without it the app has the icon of every such app.
get_icon() {
    curl -fsSL --retry 2 --connect-timeout 20 -o "$home/icon.part" "$icon_url" 2> /dev/null &&
        mv -f "$home/icon.part" "$home/icon.png" || rm -f "$home/icon.part"
}

# get_launcher puts the launcher into its folder: the build that ZOMBIES_LAUNCHER_EXE names, or else the
# latest release. Between two runs of setup the launcher updates itself, so the latest release is most often the one
# that is there. Before another build takes a launcher's place, whatever runs in the prefix is closed: the launcher
# that runs is the one before. When the latest release can't be fetched, a Mac that has a launcher keeps it.
get_launcher() {
    local to=$launcher_dir/$exe_name from=${ZOMBIES_LAUNCHER_EXE:-}
    mkdir -p "$launcher_dir"
    if [ -n "$from" ]; then
        [ -f "$from" ] || die "there is no file $from"
        cp "$from" "$to.part"
        say "the launcher is the build $from"
    else
        say "downloading the launcher's latest release"
        if ! curl -fsSL --retry 2 --connect-timeout 20 -o "$to.part" "$setup_exe"; then
            rm -f "$to.part"
            [ -f "$to" ] || die "the launcher could not be downloaded from $setup_exe"
            say "the latest release could not be downloaded: this Mac keeps the launcher it has"
            return 0
        fi
    fi
    if [ "$(head -c 2 "$to.part")" != MZ ]; then
        rm -f "$to.part"
        die "what was downloaded for the launcher is not a Windows program"
    fi
    if cmp -s "$to.part" "$to"; then
        rm -f "$to.part"
        return 0
    fi
    WINEPREFIX="$prefix" "$engine/bin/wineserver" -k 2> /dev/null || true
    mv -f "$to.part" "$to"
}

# make_app makes the app that takes the launcher's links. It is an AppleScript app. Its handler for a link that the
# browser opens runs the copy of this script with the link, and does not wait for it. The link is one quoted word of
# the shell's line, whatever is in it.
#
# The app stays open while Wine runs, and asks every ten seconds whether it still does (cmd_running). macOS lets a
# program onto the Mac's own network when the app that started it may go there (Local Network, in the Privacy
# settings). An app that ends as soon as it has started Wine is not enough: that Wine reached no server on the Mac's
# network (Windows' error 10065), though the app was allowed there and Wine reached the server from Terminal. With
# the app open, it did (cmd_check, on an M3 Pro in November 2026).
#
# The game's window opens behind the browser's: macOS keeps a program that started in the background from taking the
# front. An app that asked macOS to bring Wine's new windows to the front (NSRunningApplication's
# activateWithOptions) changed nothing about that, so this one does not try.
#
# Opened by hand, the app has no link to start the launcher with. It then says where it starts from, and offers to
# remove the launcher, which is how a Mac's player gets rid of it without Terminal. Before it removes anything it
# asks, with what the script says goes and what stays (cmd_uninstall, asking). The app notes that it was opened, and
# the script tells from that and from the last link's time whether a hand opened it (cmd_running). While Wine runs the
# app runs too, and a hand that opens it then opens it again, which macOS tells it apart (reopen): it asks at once.
# Uninstall closes Wine.
#
# The app has no icon in the Dock (LSUIElement), has the launcher's icon where the release's could be fetched
# (get_icon), and says what the microphone is for when macOS asks, which it does as the game starts. The changes to
# what is in it break the signature that osacompile gave it, so it is signed again, for this Mac alone. macOS knows
# such an app by that signature and asks about each new one again, so make_app leaves an app as it is when it made it
# from the same source (app.applescript), which has the microphone's text and the icon's checksum too. It builds the
# new app apart, and puts it in the place of the one before only once it is whole.
make_app() {
    local quoted stamp opened run icon plist built source=$home/app.applescript new=$home/app.new.applescript
    quoted=$(printf %s "$script" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g') # as an AppleScript string
    stamp=$(printf %s "$home/.last-link" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')
    opened=$(printf %s "$home/.opened" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')
    run="\"/bin/bash \" & quoted form of \"$quoted\""
    icon=none
    if [ -f "$home/icon.png" ]; then
        icon=$(cksum < "$home/icon.png" | cut -d ' ' -f 1)
    fi
    cat > "$new" << EOF
-- $app_name, the app that takes the launcher's links. launcher/mac.sh makes it (make_app).
-- Of the microphone, its Info.plist says: $microphone
-- Its icon's checksum: $icon

-- A link is noted before the script starts, so that the app stays open while Wine starts (cmd_running).
on open location theURL
	set noteLink to "/usr/bin/touch " & quoted form of "$stamp" & "; "
	do shell script noteLink & $run & " link " & quoted form of theURL & " >/dev/null 2>&1 &"
end open location

-- The app notes that it was opened, for the script to tell whether a hand did it or a link (cmd_running).
on run
	do shell script "/usr/bin/touch " & quoted form of "$opened"
end run

-- Opened again while it runs, as it does while Wine runs: only a hand does that, so the app asks at once. It stays
-- open, for Wine.
on reopen
	try
		my byHand()
	end try
end reopen

-- Every ten seconds, and first as the app has opened: the script says whether the app is to stay open. Of an app
-- that a hand opened it says so. The app then says where it starts from, offers to remove the launcher, and ends.
on idle
	try
		set said to do shell script $run & " running"
	on error
		quit
		return 10
	end try
	if said is "hand" then
		try
			my byHand()
		end try
		quit
	end if
	return 10
end idle

on byHand()
	activate
	set saying to "$app_name starts from your browser."
	set answer to display dialog saying buttons {"Uninstall", "OK"} default button "OK" with title "$app_name"
	if button returned of answer is "Uninstall" then
		set saying to do shell script $run & " uninstall ask"
		set answer to display dialog saying buttons {"Cancel", "Uninstall"} default button "Cancel" with title "$app_name"
		if button returned of answer is "Uninstall" then
			do shell script $run & " uninstall yes >/dev/null 2>&1 &"
		end if
	end if
end byHand
EOF
    if [ -d "$app" ] && cmp -s "$new" "$source"; then
        rm -f "$new"
        return 0
    fi
    built=$home/app.new/$app_name.app
    rm -rf "$home/app.new"
    mkdir -p "$home/app.new"
    osacompile -s -o "$built" "$new" || die "the app that takes the launcher's links could not be made"
    plist=$built/Contents/Info.plist
    # The launcher's icon in place of the app's own, applet.icns. A newer macOS takes an app's icon from Assets.car,
    # by the name in CFBundleIconName, before it looks at the file: without those two it looks at the file.
    if [ -f "$home/icon.png" ] &&
        sips -s format icns "$home/icon.png" --out "$built/Contents/Resources/applet.icns" > /dev/null 2>&1; then
        rm -f "$built/Contents/Resources/Assets.car"
        "$plistbuddy" -c 'Delete :CFBundleIconName' "$plist" > /dev/null 2>&1 || true
    fi
    "$plistbuddy" -c 'Delete :CFBundleIdentifier' "$plist" > /dev/null 2>&1 || true
    "$plistbuddy" -c 'Delete :NSMicrophoneUsageDescription' "$plist" > /dev/null 2>&1 || true
    "$plistbuddy" -c 'Add :CFBundleIdentifier string io.github.gimme.zombies-launcher' \
        -c 'Add :LSUIElement bool true' \
        -c "Add :NSMicrophoneUsageDescription string $microphone" \
        -c 'Add :CFBundleURLTypes array' \
        -c 'Add :CFBundleURLTypes:0 dict' \
        -c "Add :CFBundleURLTypes:0:CFBundleURLName string $app_name" \
        -c 'Add :CFBundleURLTypes:0:CFBundleURLSchemes array' \
        -c "Add :CFBundleURLTypes:0:CFBundleURLSchemes:0 string $scheme" "$plist" > /dev/null ||
        die "the app's Info.plist could not be changed: $plist"
    codesign --force --sign - "$built" > /dev/null 2>&1 || say "note: the app could not be signed again (codesign)"
    mkdir -p "$HOME/Applications"
    pkill -f "$app/Contents/MacOS/" 2> /dev/null || true # an app that runs is the one before
    rm -rf "$app"
    mv "$built" "$app"
    rmdir "$home/app.new" 2> /dev/null || true
    "$lsregister" -f "$app" || say "note: macOS did not register the app. Open it once in Finder: $app"
    mv -f "$new" "$source"
}

# cmd_running is what the app asks, every ten seconds (make_app): whether it is to stay open, by the exit status. It
# is while Wine runs for this user, and for a minute after a link, which the app notes (.last-link) and in which Wine
# starts. An app that noted that it was opened (.opened), with no Wine running and no link in that minute, was opened
# by hand: running then prints "hand", for the app to say what it is for.
cmd_running() {
    local last now opened=0
    if [ -f "$home/.opened" ]; then
        opened=1
        rm -f "$home/.opened"
    fi
    if pgrep -U "$(id -u)" -x wineserver > /dev/null 2>&1; then
        return 0
    fi
    last=$(stat -f %m "$home/.last-link" 2> /dev/null || echo 0)
    now=$(date +%s)
    if [ $((now - last)) -lt 60 ]; then
        return 0
    fi
    if [ "$opened" = 1 ]; then
        echo hand
        return 0
    fi
    return 1
}

cmd_setup() {
    local t6=${1:-} steam="$HOME/Library/Application Support/Steam/steamapps/common/Call of Duty Black Ops II"
    [ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] || die "this is for a Mac with Apple Silicon (M1 or later)"
    if [ -n "$t6" ]; then
        t6=$(game_folder "$t6")
    fi
    # plutonium-mac-aio's Wine is an Intel program, which a Mac with Apple Silicon runs with Rosetta. A Mac that has
    # never run one has no Rosetta yet, and installs it when asked.
    if ! arch -x86_64 /usr/bin/true 2> /dev/null; then
        say "installing Rosetta, which this Mac runs Intel programs with"
        softwareupdate --install-rosetta --agree-to-license ||
            die "Rosetta could not be installed. Run: softwareupdate --install-rosetta"
    fi
    mkdir -p "$home"
    : > "$mark"
    keep_script
    get_icon
    get_aio
    aio_setup
    folders
    link_folder
    get_launcher
    if [ -n "$t6" ]; then
        set_folder "$t6"
    elif ! t6_folder > /dev/null && has_t6 "$steam"; then
        t6=$(game_folder "$steam")
        set_folder "$t6"
    fi
    make_app
    # As on Windows (update.go: install), setup says only that it is done. The room's page leads the player from
    # there, and the launcher asks for the game's files when it finds none.
    echo
    echo "$app_name successfully installed."
}

cmd_folder() {
    local dir
    [ $# = 1 ] || usage
    folders
    dir=$(game_folder "$1")
    set_folder "$dir"
}

# cmd_link starts the launcher with a link. Any page can have a link of the launcher's scheme, and the launcher
# checks the link itself (link.go: parseLink). This turns away what can't be one before Wine starts. The launcher's
# standard output is neither a file nor a pipe: so it knows that it has no window, asks in dialogs, writes its log to
# its folder and reports to the server (windows.go: windowless). What Wine prints goes to wine.log, which starts
# again once it is large. link returns when the launcher ends: at once when the player's launcher runs already,
# which this one tells of the click.
cmd_link() {
    local url=${1:-}
    [ $# = 1 ] || usage
    case "$url" in
    "$scheme"://*) ;;
    *) die "that is not a link of a room's page: it starts with $scheme://" ;;
    esac
    if printf %s "$url" | LC_ALL=C grep -q '[^A-Za-z0-9.:/?&=_%-]'; then
        die "that is not a link of a room's page"
    fi
    ready
    wine_env
    profile
    if [ -f "$wine_log" ] && [ $(($(wc -c < "$wine_log"))) -gt 20000000 ]; then
        : > "$wine_log"
    fi
    echo "== $(date '+%Y-%m-%d %H:%M:%S') link" >> "$wine_log"
    exec "$wine" "$launcher_dir/$exe_name" "$url" < /dev/null > /dev/null 2>> "$wine_log"
}

# cmd_game starts the game as the launcher does (launch.go: commandLine), but joins no game: Plutonium's bootstrapper
# starts Black Ops II's Zombies on the game folder, in LAN mode, under a name. The bootstrapper is a console program,
# and this Terminal is its console.
cmd_game() {
    local name=${1:-Player} dir
    [ $# -le 1 ] || usage
    case "$name" in
    *[!A-Za-z0-9_.-]*) die "the name can have letters, digits, and _ . - only" ;;
    esac
    ready
    [ -f "$plutonium/bin/plutonium-bootstrapper-win32.exe" ] ||
        die "Plutonium is not on this Mac yet. The launcher downloads it: press Connect in a Black Ops II room once."
    dir=$(t6_folder) || die "Black Ops II's files are not set. Run: bash $this folder <the game's folder>"
    wine_env
    profile
    cd "$plutonium"
    say "starting Black Ops II from $dir, as $name"
    exec "$wine" bin/plutonium-bootstrapper-win32.exe t6zm "$(win_path "$dir")" -lan +name "$name"
}

# cmd_check tells whether the launcher reaches a server on the Mac's own network, started in each of two ways: from
# this Terminal, and through the app, as a room's button starts it. macOS lets a program onto that network when the
# app that started it may go there (make_app). A server that the program may not reach looks unreachable to it:
# Windows' error 10065, no route to the host.
#
# Each try closes what runs in the prefix, so that all of Wine starts in the one way. It runs the launcher with a link
# to the server, on plain HTTP, with a key that no server knows: a server that is reached turns the key away, and the
# launcher ends. The launcher's log then has the answer (verdict). A launcher writes a failed run's error there only
# from the build on that fixed play's log (main.go). macOS may ask about the app and local networks during a try, which
# fails that try, so the app has three.
cmd_check() {
    local server=${1:-} log link by_terminal by_app try
    [ $# = 1 ] || usage
    case "$server" in
    "" | *[!A-Za-z0-9.:-]*) die "that is not a server's address, such as 192.168.1.20:3075" ;;
    esac
    ready
    wine_env
    log=$launcher_dir/launcher.log
    link="$scheme://$server/play?key=00000000&tls=0"
    say "starting the launcher from this Terminal, for $server"
    stop_wine
    rm -f "$log"
    "$wine" "$launcher_dir/$exe_name" "$link" < /dev/null > /dev/null 2>> "$wine_log" &
    await "$log"
    by_terminal=$(verdict "$log")
    say "from Terminal: ${by_terminal:-no answer}"
    say "starting it through $app_name, as a room's button does. If macOS asks about local networks, allow it."
    for try in 1 2 3; do
        rm -f "$log"
        open "$link"
        await "$log"
        by_app=$(verdict "$log")
        if [ "$by_app" = REACHED ]; then
            break
        fi
        sleep 10
    done
    say "through the app: ${by_app:-no answer}"
    echo
    echo "== the launcher's log, of the last try"
    end_of "$log"
    echo "from Terminal:   ${by_terminal:-no answer}"
    echo "through the app: ${by_app:-no answer}"
}

# verdict prints what the launcher's log says of the check's server: REACHED, NOT REACHED, or nothing yet.
verdict() {
    if grep -q "no longer recognises this link" "$1" 2> /dev/null; then
        echo "REACHED"
    elif grep -q "can't be reached" "$1" 2> /dev/null; then
        echo "NOT REACHED"
    fi
}

# await waits until the launcher's log has an answer, for up to 30 seconds, and then closes what runs in the prefix.
await() {
    local n=0
    while [ "$n" -lt 30 ] && [ -z "$(verdict "$1")" ]; do
        sleep 1
        n=$((n + 1))
    done
    stop_wine
}

# stop_wine closes what runs in the prefix, and waits until it is gone.
stop_wine() {
    "$WINESERVER" -k 2> /dev/null || true
    "$WINESERVER" -w 2> /dev/null || true
}

# quiet prints a file without what MoltenVK lists of the GPU: a line that starts with [mvk-, and the lines after it
# that start with a tab.
quiet() { awk '/^\[mvk-/ { skip = 1; next } skip && /^\t/ { next } { skip = 0; print }' "$1"; }

# last_run prints what Wine printed since the last link: all of it when it is short, or else its start and its end. A
# program that crashes says why first, and what follows is long.
last_run() {
    if [ -f "$1" ]; then
        quiet "$1" | awk '
            /^== .* link$/ { n = 0 }
            { line[++n] = $0 }
            END {
                if (n <= 130) {
                    for (i = 1; i <= n; i++) print line[i]
                } else {
                    for (i = 1; i <= 90; i++) print line[i]
                    print "... (" n - 130 " lines left out) ..."
                    for (i = n - 39; i <= n; i++) print line[i]
                }
            }'
    else
        echo "(none yet)"
    fi
    echo
}

# end_of prints the last lines of a file.
end_of() {
    if [ -f "$1" ]; then
        quiet "$1" | tail -n 40
    else
        echo "(none yet)"
    fi
    echo
}

# cmd_log shows what there is to send when something went wrong: what the launcher said of its runs, the config it
# keeps (the servers it plays on, and the game's folder), and what Wine printed.
cmd_log() {
    folders
    echo "== the launcher's log"
    end_of "$launcher_dir/launcher.log"
    echo "== the launcher's config"
    end_of "$launcher_dir/launcher.json"
    echo "== Wine's log, since the last link"
    last_run "$wine_log"
}

cmd_stop() {
    ready
    wine_env
    "$WINESERVER" -k 2> /dev/null || true
    say "stopped"
}

# asking prints what uninstall asks. It names what goes that a player may not think of, the game files that the
# launcher downloaded into its folder (files.go: downloads), with their size, as the launcher's own uninstall does on
# Windows (update.go). It also names what stays.
asking() {
    local files=$home/t6 size
    echo "Uninstall $app_name?"
    echo
    if [ -d "$files" ]; then
        size=$(du -sk "$files" | awk '{
            bytes = $1 * 1024
            if (bytes >= 1e9) printf "%.1f GB", bytes / 1e9
            else printf "%.0f MB", bytes / 1e6
        }')
        echo "This also deletes the game files that it downloaded ($size)."
    fi
    echo "Plutonium stays, in $aio_name in your home folder."
}

# cmd_uninstall removes what setup made of the launcher: the app, the launcher's folder, which has its program, its
# config and log, and the game files that it downloaded, and the link to that folder in the prefix. plutonium-mac-aio
# stays, with its Wine and with Plutonium, as Plutonium stays on Windows (update.go: uninstall). A copy of the game
# elsewhere on the Mac stays too, and Rosetta, which is macOS's own. uninstall asks first, in Terminal. For the app,
# which asks in a dialog (make_app), "uninstall ask" prints the question and "uninstall yes" removes without it.
cmd_uninstall() {
    local answer=${1:-} data
    [ $# -le 1 ] || usage
    [ -f "$mark" ] || die "$home is not a folder that setup made"
    case "$answer" in
    yes) ;;
    ask)
        asking
        return 0
        ;;
    *)
        asking
        printf '[y/N] '
        read -r answer || answer=
        case "$answer" in
        y | Y | yes) ;;
        *) die "nothing was removed" ;;
        esac
        ;;
    esac
    if [ -x "$engine/bin/wineserver" ]; then
        WINEPREFIX="$prefix" "$engine/bin/wineserver" -k 2> /dev/null || true
    fi
    pkill -f "$app/Contents/MacOS/" 2> /dev/null || true
    "$lsregister" -u "$app" > /dev/null 2>&1 || true
    rm -rf "$app" "$home"
    if data=$(local_appdata) && [ -L "$data/$app_name" ]; then
        rm -f "$data/$app_name"
    fi
    say "removed"
}

# With no command, the script installs the launcher, as the launcher's own program does on Windows when it is run by
# hand (main.go).
[ $# -ge 1 ] || set -- setup
command=$1
shift
case "$command" in
setup) [ $# -le 1 ] || usage; cmd_setup "$@" ;;
folder) cmd_folder "$@" ;;
link) cmd_link "$@" ;;
game) cmd_game "$@" ;;
check) cmd_check "$@" ;;
running) cmd_running ;; # for the app
log) cmd_log ;;
stop) cmd_stop ;;
uninstall) cmd_uninstall "$@" ;;
*) usage ;;
esac

#!/bin/bash
# launcher/mac-t7-trial.sh - a trial: does Black Ops III run on a Mac with Apple Silicon, in the Wine that the
# launcher plays Black Ops II in? The launcher starts no Black Ops III on a Mac (boiii.go: haveBoiii), and nobody is
# known to have run BOIII on one. This script finds out how far the game gets there: it starts the game by hand, as
# the launcher starts it on Windows (boiii.go: startBoiii), and says what it heard of it. It has run on no Mac yet.
# It is no part of the launcher or of its releases: copy it to the Mac, and run it in Terminal.
#
#   bash mac-t7-trial.sh files [<map>...]
#                                   download Black Ops III from the server, into the launcher's own folder
#   bash mac-t7-trial.sh game [<Black Ops III folder>] [<argument for BOIII>...]
#                                   set up what the trial lacks, start the game, and say how far it gets
#   bash mac-t7-trial.sh log        show the end of what Wine said
#   bash mac-t7-trial.sh stop       close the game and the trial's Wine
#   bash mac-t7-trial.sh remove     remove the trial: its Wine prefix, with Steam and BOIII in it
#
# The Mac needs the launcher installed (mac.sh: setup), for plutonium-mac-aio's Wine, and Black Ops III's Windows
# files, 51 GB and more: files downloads them, or game takes a copy of the game's folder from a Windows PC. The trial
# changes nothing of plutonium-mac-aio's, and of the launcher's only the folder of its Black Ops III, which files
# fills. All else that is its own is in one folder, which remove removes.
#
# files downloads the game as a launcher does (internal/launcherapi): the server's list of the game's files, and then
# each file, which it checks against the size and the hash in the list. It takes what every map needs, which has
# Shadows of Evil, and the maps that it is given, by their names on the server (zm_castle). A download that stopped
# goes on from where it was at the next run. The files go where the launcher keeps the Black Ops III that it
# downloads, ~/Library/Application Support/Zombies Launcher/t7 (files.go: downloads), and files writes the launcher's
# own record of that folder there (sync.go: syncState). So a launcher that takes Black Ops III on a Mac one day finds
# its download done, and downloads none of it again. Where a file's time in the record is not the one that such a
# launcher reads, the launcher reads the file once, to check it. The launcher's uninstall removes the folder with the
# rest of the launcher's (mac.sh: cmd_uninstall). game starts the game from that folder when it is given none.
#
# The server knows the player of a launcher's call by their key (server/launcher.go), which is in the link that a
# room's button starts the launcher with. A launcher that runs has the link on its command line, so files reads the
# server and the key there (find_server). Where none runs, it asks for the line of the player's profile on the
# server's page, Play.bat <server> <key>, which has the same two. Its calls say that they are a launcher's of version
# 4, the first that takes Black Ops III: a server sends that game to no launcher before it (MinVersionOf). Of the
# server's files it takes a program only when it is one of the game's own, as the launcher does (programs.go).
#
# The Wine is plutonium-mac-aio's, at the commit that mac.sh names: CrossOver 26.3.0's Wine, with DXMT 0.80 as its
# Direct3D 11, in 32 and in 64 bits (its manifest.txt, and lib/runner.sh: runner_thirdparty). Black Ops III is a
# 64-bit game on Direct3D 11, so DXMT draws it, on Metal. The one report of the Windows game running on a Mac had it
# on DXMT too, in CrossOver 25.1 (https://macgamingdb.app/games/311210).
#
# The trial has a Wine prefix of its own, for Steam. BOIII starts no game where Steam is not installed: it reads
# Steam's folder from the registry (HKLM\Software\WOW6432Node\Valve\Steam, InstallPath) and looks for steam.exe there
# (BOIII's src/client/main.cpp: restart_app_if_necessary_stub). Steam need not run, and BOIII goes on without the
# libraries that a Steam which has run once has there (steam_proxy.cpp: load_client). So the trial installs Steam
# with Valve's installer, and never starts it. That can't be in plutonium-mac-aio's prefix: Plutonium's programs quit
# when they find Steam there, and plutonium-mac-aio's launch.sh removes it before each start (steam_clear).
#
# BOIII's program is in the prefix too, C:\boiii\boiii.exe: the build that BOIII's updater lists, checked against the
# list (internal/boiii). BOIII then brings itself up to date as it starts, and downloads its data into the prefix
# (%LOCALAPPDATA%\boiii). It also writes into the game's folder, as on Windows: its settings (boiii_players), and
# another BlackOps3.exe where it asks to and the player agrees.
#
# game starts BOIII in the game's folder, past BOIII's own launcher window, with a UDP port of its own, as the
# launcher does. A game on BOIII answers getInfo on that port from when its menu runs, and says the map it is on
# (boiii.go: probeBoiii). game asks every two seconds, and says when the game first answers, when it is at its menu,
# and when it is on a map. When Wine has ended it says how far the game got, and shows the end of Wine's log.
#
# To join a room's game from the menu, the room's page has to be a PC's: a Mac's page of a Black Ops III room has no
# button to press, since the game can't be played there (web/src/room.ts: showRoomButton). mac=0 in the page's
# address shows a PC's page on a Mac (web/src/windows.ts: onMac). There, check Join by hand, and press Ready. The
# room shows its line from the start, but the line joins nothing before the room's game runs, which is when the
# room's button says Join by hand. Type it then into BOIII's console, a window of its own beside the game's, without
# the slash at its start: the slash is for the console inside the game (web/src/windows.ts: t7Console), and the
# launcher types the line into BOIII's console without it (launch.go). The trial needs no launcher for that, and no
# release of it: it runs none.
#
# What changes a run, each before "bash":
#   LINK=<link>       files takes the server and the key from this link of the launcher's
#   SERVER=<address>  files downloads from this server's page, with KEY=<key> as this player; without KEY it asks the
#                     server for a player of its own, which a server gives where anyone plays under a name
#   SWITCHES=0        without the switches that plutonium-mac-aio's Wine has for Plutonium under Rosetta (wine_env)
#   HUD=1             with Apple's Metal HUD, which shows the frame rate
#   WINEDEBUG=err+all with Wine's errors in the log, which has none of them otherwise
#
# What a game that runs here does not answer: the launcher runs in plutonium-mac-aio's prefix and starts its games
# there (mac.sh), where Steam can't be. A launcher that starts Black Ops III on a Mac needs a way around that.
set -eu
set -o pipefail

aio=$HOME/plutonium-mac-aio # where mac.sh has plutonium-mac-aio
engine=$aio/runner/engine
trial="$HOME/Library/Application Support/Zombies Launcher T7 Trial"
mark=$trial/.zombies-t7-trial # shows that this script made the folder, for remove
prefix=$trial/prefix
wine_log=$trial/wine.log
# Steam's installer, as Steam's own page has it, and where it installs Steam.
steam_url=https://cdn.akamai.steamstatic.com/client/installer/SteamSetup.exe
steam_dir="$prefix/drive_c/Program Files (x86)/Steam"
steam_win='C:\Program Files (x86)\Steam'
steam_key='HKLM\Software\Wow6432Node\Valve\Steam'
# BOIII's updater, and the User-Agent that the server asks it with (internal/boiii: Manifest, agent).
manifest=https://r2.ezz.lol/boiii.json
agent=zombies-hub
boiii_exe=$prefix/drive_c/boiii/boiii.exe
boiii_win='C:\boiii\boiii.exe'
# The game's own UDP port. The launcher takes a free one (boiii.go: freeUDPPort). The trial takes this one, which is
# not the game's default, 27017.
port=27117
menu=core_frontend # the map that a game on BOIII says it is on at its menu (boiii.go: boiiiMenu)
no_mono="mscoree,mshtml=" # as in mac.sh: Wine asks nothing as it makes the prefix
# The launcher's folder on a Mac (mac.sh: home), the folder in it where the launcher keeps the Black Ops III that it
# downloads (files.go: downloads), and the launcher's record of what that folder has (sync.go: syncFile).
launcher_home="$HOME/Library/Application Support/Zombies Launcher"
games=$launcher_home/t7
record=$games/zombies-launcher-files.json
# The trial's own note of the files that it checked against the server's list: a file to a line, with its hash, its
# size, its time and its path, and tabs between. recorded shows that the launcher's record there is the trial's.
checked=$trial/files.tsv
recorded=$trial/.wrote-record
# The version that the trial's calls say their launcher is of (internal/launcherapi: MinVersionOf).
launcher_version=4
# Black Ops III's own programs, each by its hash and its path: the lines of launcher/t7-programs.txt. files takes no
# other program from a server.
programs='51ca63bbc660e0826943c60da67606f6 blackops3.exe
b1a06f7aa52439a948a152bfd3301d9b d3dcompiler_46.dll
71af8666392acad2080a816b9f1196fe steam_api64.dll'
this=${BASH_SOURCE[0]:-$0}

say() { echo "T7 trial: $*"; }
die() { echo "T7 trial: $*" >&2; exit 1; }

usage() {
    cat >&2 << EOF
The commands:
  bash $this files [<map>...]
  bash $this game [<Black Ops III folder>] [<argument for BOIII>...]
  bash $this log
  bash $this stop
  bash $this remove
EOF
    exit 2
}

need_mac() {
    [ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] || die "this is for a Mac with Apple Silicon (M1 or later)"
}

# need_wine checks that the Mac has plutonium-mac-aio's Wine, which the launcher's setup puts there.
need_wine() {
    [ -x "$engine/lib/wine/x86_64-unix/wine" ] && [ -x "$engine/bin/wineserver" ] ||
        die "this Mac has no plutonium-mac-aio in $aio. Install the launcher first: a Black Ops II room's page has" \
            "the line for Terminal."
    if [ ! -f "$engine/lib/wine/x86_64-windows/winemetal.dll" ]; then
        say "note: this Wine has no DXMT, which draws the game (winemetal.dll). The launcher's setup installs it."
    fi
}

# wine_env sets the environment for Wine: what mac.sh's wine_env sets for the launcher's games, in the trial's prefix.
# Of that, it leaves out what is for the launcher itself, and the x87 accelerator, which is for the 32-bit program
# that Black Ops II runs in. The Rosetta switches are for Plutonium, which needs debug registers and execution
# breakpoints. Whether BOIII needs them, or runs worse with them, is not known: SWITCHES=0 leaves them out.
wine_env() {
    export WINEPREFIX="$prefix"
    export WINESERVER="$engine/bin/wineserver"
    export WINEDLLPATH="$engine/lib/wine/x86_64-windows:$engine/lib/wine/x86_64-unix"
    export PATH="$engine/bin:/usr/bin:/bin:/usr/sbin:/sbin"
    export WINEMSYNC=1
    export WINEDEBUG="${WINEDEBUG:--all}"
    export WINEDLLOVERRIDES="$no_mono"
    export MVK_CONFIG_LOG_LEVEL=1
    if [ "${SWITCHES:-1}" != 0 ]; then
        export WINE_ROSETTA_EXEC_BREAKPOINTS=1 WINE_ROSETTA_WOW64_BREAKPOINTS=1 WINE_ROSETTA_CALL_WORKAROUND=1
        export WINE_ROSETTA_LAZY_EXEC=1
    else
        unset WINE_ROSETTA_EXEC_BREAKPOINTS WINE_ROSETTA_WOW64_BREAKPOINTS WINE_ROSETTA_CALL_WORKAROUND
        unset WINE_ROSETTA_LAZY_EXEC
    fi
    if [ "${HUD:-0}" = 1 ]; then
        export MTL_HUD_ENABLED=1
    fi
    wine=$engine/lib/wine/x86_64-unix/wine
}

# prefix_running tells whether Wine runs in the trial's prefix: a wineserver has the file "lock" open in the folder
# that it names after the prefix's device and inode (plutonium-mac-aio's launch.sh: prefix_running). The program that
# game starts does not tell: BOIII starts itself again once it has brought itself up to date, and the first one ends.
prefix_running() {
    local id lock
    id=$(stat -f '%d %i' "$prefix" 2> /dev/null) || return 1
    lock=/tmp/.wine-$(id -u)/server-$(printf '%x-%x' "${id% *}" "${id#* }")/lock
    [ -f "$lock" ] && lsof -t -- "$lock" > /dev/null 2>&1
}

# stop_wine closes what runs in the trial's prefix, and waits until it is gone.
stop_wine() {
    "$WINESERVER" -k 2> /dev/null || true
    "$WINESERVER" -w 2> /dev/null || true
}

# make_prefix makes the trial's folder and its Wine prefix, on a Mac that has none.
make_prefix() {
    mkdir -p "$trial"
    : > "$mark"
    if [ -f "$prefix/system.reg" ]; then
        return 0
    fi
    say "making the trial's Wine prefix, which takes a minute: $prefix"
    "$wine" wineboot -i < /dev/null >> "$wine_log" 2>&1 || die "Wine made no prefix. What it said: bash $this log"
    "$WINESERVER" -w 2> /dev/null || true
    [ -f "$prefix/system.reg" ] || die "Wine made no prefix. What it said: bash $this log"
}

# get_steam installs Steam in the prefix, with Valve's installer, without its windows (/S). It starts no Steam. The
# installer writes Steam's folder into the registry, where BOIII reads it. Where this Wine's registry has none after
# the installer ran, the script writes it, for the Steam that the installer put there.
get_steam() {
    local setup=$trial/SteamSetup.exe
    if [ ! -f "$steam_dir/steam.exe" ]; then
        say "installing Steam in the trial's prefix, with Valve's installer: BOIII starts no game without it"
        if ! curl -fsSL --retry 2 --connect-timeout 20 -o "$setup.part" "$steam_url" ||
            [ "$(head -c 2 "$setup.part")" != MZ ]; then
            rm -f "$setup.part"
            die "Steam's installer could not be downloaded from $steam_url"
        fi
        mv -f "$setup.part" "$setup"
        "$wine" "$setup" /S < /dev/null >> "$wine_log" 2>&1 || true
        [ -f "$steam_dir/steam.exe" ] ||
            die "Steam's installer left no steam.exe in $steam_dir. What Wine said: bash $this log"
    fi
    if ! "$wine" reg query "$steam_key" /v InstallPath < /dev/null >> "$wine_log" 2>&1; then
        say "the installer wrote no folder of Steam's into the registry: writing it"
        "$wine" reg add "$steam_key" /v InstallPath /d "$steam_win" /f < /dev/null >> "$wine_log" 2>&1 ||
            die "Steam's folder could not be written into the prefix's registry. What Wine said: bash $this log"
    fi
}

# get_boiii downloads BOIII's program, on a Mac whose trial has none: the file that the updater's list names, which
# has each file as [name, size, SHA-1], checked against that size and hash, as the launcher checks it (internal/boiii).
# A program that is there stays: BOIII brings itself up to date as it starts.
get_boiii() {
    local entry size hash got from=${manifest%.json}/boiii.exe
    if [ -f "$boiii_exe" ]; then
        return 0
    fi
    say "downloading BOIII's program from its updater"
    entry=$(curl -fsSL --retry 2 --connect-timeout 20 -A "$agent" "$manifest" | tr -d ' \t\r\n' |
        grep -o '\["boiii\.exe",[0-9]*,"[0-9A-Fa-f]*"]' | head -n 1) ||
        die "BOIII's updater could not be reached, or its list has no boiii.exe: $manifest"
    size=$(printf %s "$entry" | cut -d , -f 2)
    hash=$(printf %s "$entry" | cut -d '"' -f 4 | tr A-F a-f)
    mkdir -p "$(dirname "$boiii_exe")"
    if ! curl -fsSL --retry 2 --connect-timeout 20 -A "$agent" -o "$boiii_exe.part" "$from"; then
        rm -f "$boiii_exe.part"
        die "BOIII could not be downloaded from $from"
    fi
    got=$(shasum -a 1 "$boiii_exe.part" | cut -d ' ' -f 1)
    if [ "$(($(wc -c < "$boiii_exe.part")))" != "$size" ] || [ "$got" != "$hash" ]; then
        rm -f "$boiii_exe.part"
        die "what was downloaded for BOIII is not the file that its updater lists: $from"
    fi
    mv -f "$boiii_exe.part" "$boiii_exe"
}

# game_folder prints dir as a full path, when it is a copy of Black Ops III with its Zombies, by the launcher's own
# test (title.go: titleT7).
game_folder() {
    local dir=$1
    [ -d "$dir" ] || die "there is no folder $dir"
    dir=$(cd "$dir" && pwd)
    [ -f "$dir/BlackOps3.exe" ] && [ -f "$dir/zone/zm_common.ff" ] ||
        die "$dir is not Black Ops III's folder. It is the folder with BlackOps3.exe and zone in it."
    echo "$dir"
}

# ---- files: the download of the game from the server ----

# size_of prints a file's size in bytes, hash_of the name of its contents as the server's list has it, the first 32
# hex digits of their SHA-256 (internal/launcherapi: HashOf), and gb a number of bytes for a reader.
size_of() { stat -f %z "$1"; }
hash_of() { shasum -a 256 "$1" | cut -c 1-32; }
gb() { awk -v n="$1" 'BEGIN { if (n >= 1e9) printf "%.1f GB", n / 1e9; else printf "%.0f MB", n / 1e6 }'; }

# hello asks the server for a player of the trial's own, and sets key to that player's (server/rooms.go: hello). A
# server where players sign in with Discord gives none. The server forgets such a player within a minute or so, and
# no longer knows the key then: the trial asks for another (list_files, fetch).
hello() {
    key=$(curl -fsS --connect-timeout 20 -H 'Content-Type: application/json' -d '{"name":"Mac trial"}' \
        "$base/lobby/hello" | sed -n 's/.*"key":"\([0-9a-f]*\)".*/\1/p' || true)
    [ -n "$key" ]
}

# find_server sets base, the address of the server's page, and key, the player's key there. Both are in the link that
# a room's button starts the launcher with, zombies-launcher://<host>/play?key=<key>, with tls=0 for a page on plain
# HTTP (internal/launcherapi). A launcher that runs has its link on its command line (mac.sh: cmd_link), so the trial
# reads the link there, or takes the one that LINK gives. Where no launcher runs, it asks for the line of the
# player's profile on the server's page, Play.bat <host>[:<port>] <key> (web/src/windows.ts: playLine), whose port is
# the games' and not the page's: the page is then taken to be at https://<host>. SERVER names the page's address
# instead, with KEY, or for a player of the trial's own (hello). own_key is 1 for such a player.
find_server() {
    local link=${LINK:-} host="" line
    base="" key="" own_key=0
    if [ -n "${SERVER:-}" ]; then
        base=$(printf '%s\n' "$SERVER" | sed -n 's|^\(https\{0,1\}://[A-Za-z0-9.:-]*\).*|\1|p')
        [ -n "$base" ] || die "SERVER is the address of the server's page, such as https://zombies.example.com"
        key=${KEY:-}
        if [ -z "$key" ]; then
            own_key=1
            hello || die "$base gives the trial no player of its own: its players sign in. Give your key: KEY=<key>"
        fi
    else
        if [ -z "$link" ]; then
            link=$(ps -axww -o command= 2> /dev/null | grep -o 'zombies-launcher://[A-Za-z0-9.:/?&=_%-]*' |
                grep 'key=' | head -n 1 || true)
            [ -z "$link" ] || say "taking the server and your key from the launcher that runs on this Mac"
        fi
        if [ -n "$link" ]; then
            host=$(printf '%s\n' "$link" | sed -n 's|^zombies-launcher://\([A-Za-z0-9.:-]*\)/.*|\1|p')
            key=$(printf '%s\n' "$link" | sed -n 's/.*[?&]key=\([0-9A-Za-z]*\).*/\1/p')
            case "$link" in
            *[?\&]tls=0*) base=http://$host ;;
            *) base=https://$host ;;
            esac
        elif [ -t 0 ]; then
            cat << EOF
T7 trial: no launcher runs on this Mac, whose link would have the server and your key.
  On the server's page, open your profile, and copy its line for the Windows client: Play.bat <server> <key>
EOF
            printf '  Paste the line here, and press Enter: '
            read -r line || line=""
            host=$(printf '%s\n' "$line" | sed -n 's/^ *Play\.bat  *\([A-Za-z0-9.-]*\)[:0-9]*  *.*/\1/p')
            key=$(printf '%s\n' "$line" | sed -n 's/^ *Play\.bat  *[^ ]*  *\([0-9A-Za-z]*\) *$/\1/p')
            base=https://$host
        fi
        [ -n "$host" ] || die "found no server. Press a Black Ops II room's button on this Mac, which starts the" \
            "launcher, and run this again; or give the two: SERVER=<the page's address> KEY=<your key> bash $this files"
    fi
    case "$key" in
    "" | *[!0-9A-Za-z]*) die "found no key for $base. Give it: SERVER=$base KEY=<your key> bash $this files" ;;
    esac
    say "the server: $base"
}

# get asks the server for a path as a launcher asks, with the player's key and a launcher's version, into a file. It
# prints the status of the answer, or 000 where there was none.
get() {
    curl -sS --connect-timeout 20 -H "Authorization: Key $key" -H "Zombies-Launcher-Version: $launcher_version" \
        -o "$2" -w '%{http_code}' "$base$1" || true
}

# list_files asks the server for its list of Black Ops III's files (internal/launcherapi: Files), and writes it as
# $trial/list.tsv: a file to a line, with its size, its hash, its pack and its path, and tabs between. The pack of a
# file that every map needs has no name, and is "-" there, so that no line has an empty part. The list is
# JSON as Go writes it, an object to a file, with the members in the order of launcherapi.File. list_files takes the
# list only when each path is one that the launcher takes (LocalPath), and each program one of the game's own
# (programs.go: isProgram).
list_files() {
    local body=$trial/list.json status
    say "asking the server for its list of Black Ops III's files"
    echo "  A server that lists them for the first time reads all of the game first, which takes some minutes."
    status=$(get /launcher/t7/files "$body")
    if [ "$status" = 401 ] && [ "$own_key" = 1 ] && hello; then
        status=$(get /launcher/t7/files "$body")
    fi
    case "$status" in
    200) ;;
    000) die "$base can't be reached" ;;
    401) die "$base doesn't know the key. Open its page on this Mac, sign in there, and run this again." ;;
    403) die "$base sends no Black Ops III. A server sends the game where its download rate is set" \
        "(ZOMBIES_DOWNLOAD_RATE) and it has the whole install, as /game/t7 or in ZOMBIES_SEND_DIR." ;;
    *) die "$base answered $status: $(head -c 200 "$body" | tr -d '\r\n')" ;;
    esac
    tr -d '\r\n' < "$body" | awk -v programs="$(printf '%s' "$programs" | tr '\n' ';')" '
        function str(name) {
            if (!match($0, "\"" name "\":\"[^\"]*\"")) return ""
            return substr($0, RSTART + length(name) + 4, RLENGTH - length(name) - 5)
        }
        function bad(why, path) {
            print "T7 trial: the server lists " why ", which the trial takes no list with: " path > "/dev/stderr"
            exit 1
        }
        BEGIN {
            RS = "}"
            n = split(programs, line, ";")
            for (i = 1; i <= n; i++) if (split(line[i], w, " ") == 2) known[w[2]] = w[1]
            m = split(".exe .dll .cab .inf .bat .cmd .com .scr .msi .ps1 .vbs .js .jar .lnk .sys .ocx .cpl .hta " \
                ".wsf .reg", ends, " ")
        }
        {
            path = str("path")
            if (path == "") next
            hash = str("hash")
            pack = str("pack")
            if (!match($0, /"size":[0-9]+/)) bad("a file of no size", path)
            size = substr($0, RSTART + 7, RLENGTH - 7)
            k = split(path, part, "/")
            for (i = 1; i <= k; i++)
                if (part[i] !~ /^[A-Za-z0-9_$()-][A-Za-z0-9_$() .-]*$/ || part[i] ~ /[. ]$/)
                    bad("a file by a name that is not plain", path)
            lower = tolower(path)
            if (length(path) > 200 || length(hash) != 32 || hash !~ /^[0-9a-f]+$/ || pack !~ /^[A-Za-z0-9_]*$/ ||
                lower == "zombies-launcher-files.json" || lower ~ /\.part$/ || seen[lower]++)
                bad("a file that is not as a list has it", path)
            for (i = 1; i <= m; i++) {
                e = ends[i]
                if (length(lower) > length(e) && substr(lower, length(lower) - length(e) + 1) == e &&
                    known[lower] != hash)
                    bad("a program that is none of the game", path)
            }
            print size "\t" hash "\t" (pack == "" ? "-" : pack) "\t" path
        }' > "$trial/list.tsv" || exit 1
    [ -s "$trial/list.tsv" ] || die "the server's list has no file"
}

# wanted tells whether files downloads a pack of the list: the one that every map needs ("-"), and those of the maps
# that it was given (want).
wanted() {
    case "$1" in -) return 0 ;; esac
    case "$want" in *" $1 "*) return 0 ;; esac
    return 1
}

# noted tells whether the trial has checked a file of the folder against the list's hash, and the file is still as it
# was then, by its size and its time. note records a file so, in place of what it had of the file.
noted() {
    [ -f "$checked" ] && grep -qxF "$(printf '%s\t%s\t%s\t%s' "$1" "$2" "$(stat -f %Fm "$games/$3")" "$3")" "$checked"
}

note() {
    if [ -f "$checked" ]; then
        awk -F '\t' -v path="$3" '$4 != path' "$checked" > "$checked.new"
    else
        : > "$checked.new"
    fi
    printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$(stat -f %Fm "$games/$3")" "$3" >> "$checked.new"
    mv -f "$checked.new" "$checked"
}

# write_record writes the launcher's record of the folder (sync.go: syncState), from the trial's note: each file by
# its path, with its size, its time and its hash. The time is in nanoseconds, as the launcher reads a file's time in
# Wine: Wine keeps a file's time in units of 100 nanoseconds, so the last two digits are 0. A folder that has a record
# which the trial did not write is the launcher's: its record stays, and the launcher checks the other files itself.
write_record() {
    if [ ! -s "$checked" ]; then
        return 0
    fi
    if [ -f "$record" ] && [ ! -f "$recorded" ]; then
        say "the folder has a record of the launcher's own, which stays as it is: $record"
        return 0
    fi
    awk -F '\t' '
        BEGIN { printf "{\"files\":{" }
        {
            split($3, t, ".")
            printf "%s\"%s\":{\"size\":%s,\"time\":%s%s00,\"hash\":\"%s\"}", (NR > 1 ? "," : ""), $4, $2, t[1],
                substr(t[2] "0000000", 1, 7), $1
        }
        END { printf "}}\n" }' "$checked" > "$record.tmp"
    mv -f "$record.tmp" "$record"
    : > "$recorded"
}

# fetch downloads one file of the list into its place in the folder, by way of a .part file next to it, which a
# download that stopped leaves there: the next try goes on from its end (the server sends a file from the offset of a
# Range header). The .part file takes the file's place once its contents have the list's hash. A large file shows
# curl's bar.
fetch() {
    local size=$1 hash=$2 path=$3 dest=$games/$3 part=$games/$3.part has status tries=0 bar=-s url
    url=$base/launcher/t7/files/$(printf '%s' "$path" |
        sed -e 's/%/%25/g' -e 's/ /%20/g' -e 's/\$/%24/g' -e 's/(/%28/g' -e 's/)/%29/g')
    if [ "$size" -gt 100000000 ]; then
        bar='-#'
    fi
    mkdir -p "$(dirname "$dest")"
    while :; do
        has=0
        if [ -f "$part" ]; then
            has=$(size_of "$part")
        fi
        if [ "$has" -gt "$size" ]; then
            rm -f "$part"
            has=0
        fi
        if [ "$size" = 0 ]; then
            : > "$part"
        fi
        if [ "$has" = "$size" ]; then
            break
        fi
        tries=$((tries + 1))
        [ "$tries" -le 30 ] || die "the download of $path stopped 30 times. Run files again to go on from where it is."
        status=$(curl "$bar" -S -f --connect-timeout 20 -C - -H "Authorization: Key $key" \
            -H "Zombies-Launcher-Version: $launcher_version" -o "$part" -w '%{http_code}' "$url" < /dev/null || true)
        case "$status" in
        200 | 206) ;;
        401)
            if [ "$own_key" != 1 ] || ! hello; then
                die "$base no longer knows the key. Open its page on this Mac, sign in there, and run files again."
            fi
            ;;
        409) die "the server's game files changed since it listed them. Run files again." ;;
        403 | 404 | 426) die "the server refused $path, with the status $status" ;;
        *)
            echo "  the download stopped (status $status): going on from where it is"
            sleep 3
            ;;
        esac
    done
    if [ "$(hash_of "$part")" != "$hash" ]; then
        rm -f "$part"
        die "what was downloaded for $path is not the file of the server's list. Run files again."
    fi
    mv -f "$part" "$dest"
    note "$hash" "$size" "$path"
}

# cmd_files downloads Black Ops III from the server into the launcher's folder for it: what every map needs, and the
# maps that it is given. A file that the folder has is kept when it is the list's, which the trial's note tells, or
# else its hash, once. Ctrl+C stops the download, and the next run goes on from there.
cmd_files() {
    local map want=" " count bytes todo=0 free size hash pack path dest n=0 got=0 have others
    need_mac
    [ -d "$launcher_home" ] ||
        die "the launcher is not installed on this Mac. Its folder is where the game goes: $launcher_home"
    mkdir -p "$trial" "$games"
    : > "$mark"
    find_server
    list_files
    for map in "$@"; do
        case "$map" in "" | *[!A-Za-z0-9_]*) die "$map is no map's name on a server, as zm_castle is" ;; esac
        awk -F '\t' -v map="$map" '$3 == map { found = 1 } END { exit !found }' "$trial/list.tsv" ||
            die "the server's list has no map $map. Its maps:" \
                "$(awk -F '\t' '$3 != "-" { print $3 }' "$trial/list.tsv" | sort -u | tr '\n' ' ')"
        want="$want$map "
    done
    count=$(awk -F '\t' -v want="$want" '
        $3 == "-" || index(want, " " $3 " ") { n++; b += $1 }
        END { printf "%d %.0f\n", n, b }' "$trial/list.tsv")
    bytes=${count#* }
    count=${count% *}
    others=$(awk -F '\t' -v want="$want" '
        $3 != "-" && !index(want, " " $3 " ") { b[$3] += $1 }
        END { for (p in b) printf "%s (%.1f GB)\n", p, b[p] / 1e9 }' "$trial/list.tsv" | sort | tr '\n' ' ')
    say "$count files, $(gb "$bytes"), into $games"
    if [ -n "$others" ]; then
        echo "  The server's other maps, each of which files takes by its name: $others"
    fi
    while IFS=$'\t' read -r size hash pack path; do
        if wanted "$pack" && { [ ! -f "$games/$path" ] || [ "$(size_of "$games/$path")" != "$size" ]; }; then
            todo=$((todo + size))
        fi
    done < "$trial/list.tsv"
    free=$(df -k "$games" | awk 'NR == 2 { print $4 }')
    if [ "$todo" -gt $((free * 1024)) ]; then
        die "the files to download are $(gb "$todo"), and the disk has $(gb $((free * 1024))) free"
    fi
    trap 'echo; say "stopped. Run files again to go on from here."; write_record; exit 130' INT TERM
    while IFS=$'\t' read -r size hash pack path <&3; do
        wanted "$pack" || continue
        n=$((n + 1))
        dest=$games/$path
        have=0
        if [ -f "$dest" ] && [ "$(size_of "$dest")" = "$size" ]; then
            if noted "$hash" "$size" "$path"; then
                have=1
            else
                echo "  [$n/$count] checking $path, which the folder has"
                if [ "$(hash_of "$dest")" = "$hash" ]; then
                    note "$hash" "$size" "$path"
                    have=1
                fi
            fi
        fi
        if [ "$have" = 0 ]; then
            echo "  [$n/$count] $(gb "$got") of $(gb "$bytes"): $path, $(gb "$size")"
            fetch "$size" "$hash" "$path"
        fi
        got=$((got + size))
    done 3< "$trial/list.tsv"
    write_record
    trap - INT TERM
    if [ -f "$games/BlackOps3.exe" ] && [ -f "$games/zone/zm_common.ff" ]; then
        say "Black Ops III is in $games. Start it with: bash $this game"
    else
        say "the server's files are in $games, and they are no whole game: BlackOps3.exe or zone/zm_common.ff is" \
            "not among them"
    fi
}

# probe asks the game for its info on its port, as the launcher does (boiii.go: probeBoiii), and prints the letters
# of the answer: "infoResponse", and on the next line the info, \key\value\key\value. It prints nothing for a game
# that doesn't answer. nc waits a second for the answer.
probe() {
    printf '\377\377\377\377getInfo zombies' | nc -u -w 1 127.0.0.1 "$port" 2> /dev/null |
        LC_ALL=C tr -cd '\11\12\15\40-\176' || true
}

# watch says what the game does while Wine runs in the prefix, and then how far the game got. dir is the game's
# folder, for its advice.
watch() {
    local dir=$1 n=0 start now said map last="" answered=0 played="" hinted=0
    while ! prefix_running; do
        n=$((n + 1))
        if [ "$n" -gt 30 ]; then
            die "Wine did not start. What it said: bash $this log"
        fi
        sleep 1
    done
    start=$(date +%s)
    now=$start
    while prefix_running; do
        sleep 2
        now=$(date +%s)
        said=$(probe)
        case "$said" in
        *infoResponse*)
            map=$(printf '%s\n' "$said" | sed -n 's/.*\\mapname\\\([^\\]*\).*/\1/p' | head -n 1)
            if [ "$answered" = 0 ]; then
                answered=1
                say "the game answers on its port, $((now - start)) seconds after Wine started"
            fi
            if [ "$map" != "$last" ]; then
                last=$map
                if [ "$map" = "$menu" ]; then
                    say "the game is at its menu"
                elif [ -n "$map" ]; then
                    played=$map
                    say "the game is on the map $map"
                fi
            fi
            ;;
        *)
            if [ "$answered" = 0 ] && [ "$hinted" = 0 ] && [ $((now - start)) -ge 180 ]; then
                hinted=1
                say "nothing heard from the game yet, three minutes in. It goes on for as long as Wine runs."
            fi
            ;;
        esac
    done
    trap - INT TERM
    echo
    if [ -n "$played" ]; then
        say "Black Ops III ran, and got onto the map $played. Wine ran for $((now - start)) seconds."
    elif [ "$answered" = 1 ]; then
        say "Black Ops III ran as far as its menu, and got onto no map. Wine ran for $((now - start)) seconds."
    else
        say "Wine ran for $((now - start)) seconds, and the script heard nothing from the game on its port $port."
        cat << EOF
  What you saw tells more: an error in a window, BOIII's console, the game's window, its menu.
  - For Wine's errors in the log: WINEDEBUG=err+all bash $this game ...
  - Without the Rosetta switches: SWITCHES=0 bash $this game ...
  - A game window that stayed black: a report of the game in CrossOver got past that by renaming the game's
    video folder. mv "$dir/video" "$dir/video.off"
EOF
    fi
    echo
    cmd_log
}

# quiet prints a file without what MoltenVK lists of the GPU, as in mac.sh.
quiet() { awk '/^\[mvk-/ { skip = 1; next } skip && /^\t/ { next } { skip = 0; print }' "$1"; }

# cmd_log shows what Wine said in the last run of game, which game marks in the log: its last lines.
cmd_log() {
    echo "== what Wine said in the last run, at its end"
    if [ -f "$wine_log" ]; then
        quiet "$wine_log" |
            awk '/^== .* game/ { n = 0 } { line[++n] = $0 } END { for (i = 1; i <= n; i++) print line[i] }' |
            tail -n 40
    else
        echo "(none yet)"
    fi
    echo
}

# cmd_game sets up what the trial lacks, starts the game as the launcher starts it on Windows (boiii.go: startBoiii),
# and watches it. The folder is a copy of the game's, the one with BlackOps3.exe and zone in it, or without one the
# launcher's folder, which files fills. What follows goes to BOIII as it is: its own arguments, such as -nosteam, and
# the game's, such as +set r_fullscreen 0. Ctrl+C closes the game and Wine, also while the trial is still being set
# up.
cmd_game() {
    local dir
    need_mac
    need_wine
    case "${1:-}" in
    "" | -* | +*) # no folder: the game that files downloaded, in the launcher's folder
        [ -f "$games/BlackOps3.exe" ] ||
            die "the launcher's folder has no Black Ops III yet. Download it: bash $this files. Or name a copy's" \
                "folder."
        dir=$(game_folder "$games")
        ;;
    *)
        dir=$(game_folder "$1")
        shift
        ;;
    esac
    wine_env
    trap 'echo; say "closing the game and Wine"; stop_wine; exit 130' INT TERM
    make_prefix
    get_steam
    get_boiii
    stop_wine # a game of the run before, and what the installer left running
    if [ -f "$wine_log" ] && [ $(($(wc -c < "$wine_log"))) -gt 20000000 ]; then
        : > "$wine_log"
    fi
    echo "== $(date '+%Y-%m-%d %H:%M:%S') game${*:+ $*}" >> "$wine_log"
    say "starting Black Ops III from $dir"
    cat << EOF
  Close the game, or press Ctrl+C here, to end the trial.
  - BOIII first downloads its data, and may bring itself up to date and start again.
  - If BOIII asks whether to download another BlackOps3.exe, the copy is not of the build that BOIII runs. OK lets
    BOIII replace the program in the game's folder.
  - If macOS asks whether Terminal may find devices on local networks, allow it: the game reaches a server on your
    network through it.
  - At the game's menu, to join a room's game: open a Black Ops III room on this Mac with ?mac=0 at the end of the
    page's address (&mac=0 where the address has a ? already), which shows a PC's page. Check Join by hand there,
    and press Ready. Once the room's game runs, the room's button says Join by hand: type the room's line then,
    without the slash at its start, into BOIII's console, a window of its own beside the game's.
EOF
    (cd "$dir" && exec "$wine" "$boiii_win" -launch +set net_port "$port" "$@" < /dev/null >> "$wine_log" 2>&1) &
    watch "$dir"
}

cmd_stop() {
    need_wine
    wine_env
    stop_wine
    say "stopped"
}

# cmd_remove removes the trial's folder: its prefix, with Steam, BOIII and BOIII's data, Steam's installer, and
# Wine's log. The launcher, plutonium-mac-aio and the game's folder stay as they are. So does what BOIII wrote into
# the game's folder.
cmd_remove() {
    [ -f "$mark" ] || die "$trial is not a folder that this script made"
    if [ -x "$engine/bin/wineserver" ]; then
        wine_env
        stop_wine
    fi
    rm -rf "$trial"
    say "removed $trial. What BOIII wrote into the game's folder stays there: boiii_players."
    if [ -d "$games" ]; then
        say "The game that files downloaded stays too, as the launcher's: $games"
    fi
}

[ $# -ge 1 ] || usage
command=$1
shift
case "$command" in
files) cmd_files "$@" ;;
game) cmd_game "$@" ;;
log) [ $# = 0 ] || usage; cmd_log ;;
stop) [ $# = 0 ] || usage; cmd_stop ;;
remove) [ $# = 0 ] || usage; cmd_remove ;;
*) usage ;;
esac

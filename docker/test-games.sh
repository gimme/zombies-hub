#!/usr/bin/env bash
# docker/test-games.sh - run the game tests (e2e/game/): real games against the server image, with the game files,
# played in this PC's Chrome on its GPU.
#
#   docker/test-games.sh [--map <map>] [--jobs <n>] [smoke|engine|long|<test>...] [-- <playwright test flags>]
#   docker/test-games.sh --stop
#
# smoke, engine and long are the groups (e2e/game.config.ts), and a test is named by its file: coop for
# e2e/game/smoke/coop.spec.ts. With none named, smoke and engine run. --map is the map of the rooms' games, and the one
# a player plays alone (default zombie_theater). --jobs is how many tests run at once (default 4), each with a server
# of its own: on 8 cores, four keep them busy, and six at a time overload them. A run of long, or of bigsquad, runs
# one test at a time: bigsquad's six browsers need the machine to themselves (ZOMBIES_TEST_PLAYERS: 5 to 8 of them).
#
# It builds the server image as zombies:test and the Windows player's image (docker/Dockerfile: windows-player), and
# runs the tests with Playwright on this machine, which needs Node, npm ci in e2e, and Chrome (ZOMBIES_CHROME: its
# executable, where it is not where Chrome installs). Each test's results are in build/test/results: the server's log
# and data, each player's log, and screenshots of a test that failed. The run's output is in build/test/run.log, which
# this shows as it comes. One run goes at a time.
#
# A run stops safely at any time. Ctrl+C asks it to stop, and so do SIGTERM and SIGHUP, and --stop (make test-stop)
# from another terminal, which then waits for it to end. The run then ends by itself (e2e/game/stop.ts): Playwright
# stops its tests as at its own Ctrl+C, each player's Chrome closes as at a test's end, each server stops, and this
# says the run was stopped and exits with 130. That takes a few minutes at most. A Chrome ended from outside while it
# draws can leave the GPU's driver hung (e2e/game/chrome.ts), so nothing here ends the run: it runs apart from the
# terminal, which only asks. A second Ctrl+C cuts nothing short. Nor does the terminal closing, or this script killed:
# the run stops by itself then, as if asked, as its runner watches for this script.
#
# On Windows the tests run in a window station of their own (docker/window-station.ps1): a headless Chrome there
# still captures the machine's mouse whenever its page captures the game's, which a window station of its own has no
# mouse for. On Linux they run in a session of their own (setsid), which the terminal's signals don't reach.
#
# ZOMBIES_GAME_DIR, ZOMBIES_ENGINE_URL, ZOMBIES_ENGINE_FILE  the game files and the engine build, as for
#                            docker/server.sh
# ZOMBIES_TEST_FPS           the frames a second a player's game draws at most (default 30; 0: the display's rate)
# ZOMBIES_TEST_DRIVER_CACHE  on Windows: the MB written to a file of the NVIDIA driver's cache of Chrome's shaders past
#                            which the run deletes it (default 64; 0 leaves the files alone: e2e/game/chrome.ts)
set -euo pipefail
. "$(dirname "$0")/host.sh"
here=$(abs "$(dirname "$0")")
root=$(abs "$here/..")
test=$root/build/test
log=$test/run.log
stop=$test/stop       # the request to stop the run (e2e/game/stop.ts)
pidfile=$test/run.pid # the run's process: on Windows the window station's PowerShell, on Linux node; the script's
                      # own while it builds the images, for --stop
codefile=$test/run.code
mkdir -p "$test"

# Whether the process is there: on Windows a process of Windows, by its number and its name, as Windows soon gives a
# number again (Git Bash's ps, which a Ctrl+C that the caller ignores does not end, as it would tasklist); elsewhere a
# process of this machine.
alive() {
    if [ -n "${MSYSTEM:-}" ]; then
        ps -W | awk -v pid="$1" '$4 == pid' | grep -Eiq '(powershell\.exe|bash(\.exe)?)$'
    else
        kill -0 "$1" 2>/dev/null
    fi
}

# The process of the run that is going, if one is: one that has an exit code has ended, whatever has its number now.
running() {
    local pid
    pid=$(cat "$pidfile" 2>/dev/null) && [ -n "$pid" ] && [ ! -s "$codefile" ] && alive "$pid" && echo "$pid"
}

# Shows the run's log from line $2 on as it grows, until the run (process $1) has ended, with all of it shown. Neither
# the watch nor tail takes a signal: Ctrl+C asks the run to stop (trap below), and they go on showing it stop.
follow() {
    : >>"$log"
    ( trap '' INT TERM HUP; while [ ! -s "$codefile" ] && alive "$1"; do sleep 1; done ) &
    local watch=$!
    ( trap '' INT TERM HUP; exec tail -n "$2" -f --pid=$watch "$log" ) &
    local tailer=$!
    # a signal this traps ends a wait early: wait again until the watch has ended
    while kill -0 $watch 2>/dev/null; do wait $watch || true; done
    wait $tailer || true
}

if [ "${1:-}" = --stop ]; then
    pid=$(running) || { echo "test-games: no run of the game tests is going"; exit 0; }
    : >"$stop"
    echo "test-games: asked the run to stop; waiting for it to end (each player's Chrome closes, each server stops)"
    follow "$pid" 0
    rm -f "$pidfile"
    code=$(cat "$codefile" 2>/dev/null) || code=
    echo "test-games: the run has ended${code:+ (exit code $code)}"
    exit 0
fi

map=zombie_theater jobs=4 named=()
while [ $# -gt 0 ]; do
    case $1 in
        --map) map=${2:?--map takes a map}; shift 2 ;;
        --jobs) jobs=${2:?--jobs takes how many tests run at once}; shift 2 ;;
        --) shift; break ;;
        smoke|engine|long) named+=(--project "$1"); [ "$1" != long ] || jobs=1; shift ;;
        *) spec=$(cd "$root/e2e" && ls game/*/"$1".spec.ts 2>/dev/null) || {
               all=$(cd "$root/e2e/game" && ls */*.spec.ts | sed 's|.*/||; s|\.spec\.ts$||' | tr '\n' ' ')
               echo "test-games: no test $1 (they are $all)" >&2
               exit 2
           }
           named+=("$spec"); [ "$1" != bigsquad ] || jobs=1; shift ;;
    esac
done
[[ "$jobs" =~ ^[1-9][0-9]*$ ]] || { echo "test-games: --jobs is how many tests run at once, not $jobs" >&2; exit 2; }
[ ${#named[@]} -gt 0 ] || named=(--project smoke --project engine)
command -v node >/dev/null || { echo "test-games: the tests run in Node, which this machine lacks" >&2; exit 2; }
[ -f "$root/e2e/node_modules/@playwright/test/cli.js" ] ||
    { echo "test-games: the tests need their library: npm ci in e2e" >&2; exit 2; }
[ -n "${MSYSTEM:-}" ] || command -v setsid >/dev/null ||
    { echo "test-games: the tests run in a session of their own, which needs setsid (util-linux)" >&2; exit 2; }
game=${ZOMBIES_GAME_DIR:-$root/game}
[ -d "$game/t5/zone" ] ||
    { echo "test-games: no game files in $game/t5 (no zone folder): copy a Black Ops install there" >&2; exit 1; }
game=$(abs "$game")
if pid=$(running); then
    echo "test-games: a run of the game tests is going (process $pid, build/test/run.pid): wait for it, or stop it" \
        "(--stop)" >&2
    exit 2
fi

# A stop asked of an earlier run is not this one's. While the images build, the run is this script, which a stop
# request ends before any test starts; Ctrl+C ends it at once, as no Chrome is open yet.
rm -f "$stop" "$codefile"
if [ -n "${MSYSTEM:-}" ]; then self=$(cat /proc/$$/winpid); else self=$$; fi
echo "$self" >"$pidfile"
trap 'rm -f "$pidfile"' EXIT

# the images, brought up to date (seconds when nothing changed), and the game files where Wine can run them
progress=quiet; docker image inspect zombies:test >/dev/null 2>&1 || progress=auto
BUILDKIT_PROGRESS=$progress "$here/image.sh" zombies:test >/dev/null
progress=quiet; docker image inspect zombies-windows-player >/dev/null 2>&1 || progress=auto
docker build --progress=$progress -f "$here/Dockerfile" --target windows-player -t zombies-windows-player \
    --build-arg UID="$uid" --build-arg GID="$gid" "$here" >/dev/null
ZOMBIES_TEST_GAME=$(for_wine "$game" zombies-game zombies:test)
export ZOMBIES_TEST_GAME ZOMBIES_TEST_INSTALL=$game/t5 ZOMBIES_TEST_MAP=$map
if [ -n "${ZOMBIES_ENGINE_FILE:-}" ]; then
    ZOMBIES_ENGINE_FILE=$(abs "$(dirname "$ZOMBIES_ENGINE_FILE")")/$(basename "$ZOMBIES_ENGINE_FILE")
    export ZOMBIES_ENGINE_FILE
fi
if [ -e "$stop" ]; then
    rm -f "$stop"
    echo "test-games: the run was stopped before its tests began" >&2
    exit 130
fi

# The run, apart from the terminal. From here a signal asks it to stop, and this waits for it to end.
asked=
ask() {
    if [ -z "$asked" ]; then
        asked=1
        : >"$stop"
        echo "test-games: stopping the run: its tests end, each player's Chrome closes and each server stops. That" \
            "can take a few minutes, and Ctrl+C again does not hurry it." >&2
    else
        echo "test-games: the run is stopping; it ends by itself" >&2
    fi
}
trap ask INT TERM HUP
# the runner stops the run once this script is gone
export ZOMBIES_TEST_SCRIPT=$self
cd "$root/e2e"
run=(node_modules/@playwright/test/cli.js test -c game.config.ts --workers "$jobs" "${named[@]}" "$@")
: >"$log"
if [ -n "${MSYSTEM:-}" ]; then
    printf '%s\n' "$(cygpath -w "$(command -v node)")" "${run[@]}" >"$test/run.args"
    pid=$(powershell -NoProfile -ExecutionPolicy Bypass -File "$(cygpath -w "$here/window-station.ps1")" \
        "$(cygpath -w "$test/run.args")" "$(cygpath -w "$log")" "$(cygpath -w "$codefile")" | tr -d '\r' | tail -n 1)
    [[ "$pid" =~ ^[0-9]+$ ]] || { echo "test-games: the window station did not start the run" >&2; exit 1; }
else
    # node is not a process group's leader here, so setsid makes it a session's in place, as this script's child
    setsid node "${run[@]}" </dev/null >>"$log" 2>&1 &
    pid=$!
fi
echo "$pid" >"$pidfile"
trap - EXIT
if [ -n "${MSYSTEM:-}" ]; then
    follow "$pid" +1
    code=$(cat "$codefile" 2>/dev/null) || code=
else
    ( trap '' INT TERM HUP; exec tail -n +1 -f --pid="$pid" "$log" ) &
    tailer=$!
    code=
    while [ -z "$code" ]; do
        wait "$pid" && code=0 || { status=$?; kill -0 "$pid" 2>/dev/null || code=$status; }
    done
    wait $tailer || true
    echo "$code" >"$codefile"
fi
rm -f "$pidfile"

# the containers the tests left, which a test removes as it ends (a Windows client's, of a test given up on)
left=$(docker ps -a --format '{{.Names}}' --filter name='^zombies-test-' 2>/dev/null | tr '\n' ' ') || left=
if [ -n "${left// }" ]; then
    echo "test-games: removing the containers the tests left: $left" >&2
    docker rm -f $left >/dev/null || true
fi
[ ! -e "$test/gpu-stuck" ] || echo "test-games: the GPU's driver holds a player's Chrome (build/test/gpu-stuck):" \
    "restart the machine before the next run" >&2
if [ -n "$asked" ] || [ -e "$stop" ]; then
    rm -f "$stop"
    echo "test-games: the run was stopped (exit code ${code:-unknown}); the results so far are in" \
        "build/test/results" >&2
    exit 130
fi
[ -n "$code" ] || { echo "test-games: the run ended without an exit code (build/test/run.log says how)" >&2; exit 1; }
exit "$code"

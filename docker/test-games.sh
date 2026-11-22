#!/usr/bin/env bash
# docker/test-games.sh - run the game tests (e2e/game/): real games against the server image, with the game files,
# played in this PC's Chrome on its GPU.
#
#   docker/test-games.sh [--map <map>] [--jobs <n>] [smoke|engine|long|<test>...] [-- <playwright test flags>]
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
# and data, each player's log, and screenshots of a test that failed.
#
# On Windows the tests run in a window station of their own (docker/window-station.ps1): a headless Chrome there
# still captures the machine's mouse whenever its page captures the game's, which a window station of its own has no
# mouse for. Their output goes to build/test/run.log, which this shows as it comes.
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
game=${ZOMBIES_GAME_DIR:-$root/game}
[ -d "$game/t5/zone" ] ||
    { echo "test-games: no game files in $game/t5 (no zone folder): copy a Black Ops install there" >&2; exit 1; }
game=$(abs "$game")

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

mkdir -p "$root/build/test"
cd "$root/e2e"
run=(node_modules/@playwright/test/cli.js test -c game.config.ts --workers "$jobs" "${named[@]}" "$@")
if [ -z "${MSYSTEM:-}" ]; then
    exec node "${run[@]}"
fi
log=$root/build/test/run.log
: >"$log"
printf '%s\n' "$(cygpath -w "$(command -v node)")" "${run[@]}" >"$root/build/test/run.args"
powershell -NoProfile -ExecutionPolicy Bypass -File "$(cygpath -w "$here/window-station.ps1")" \
    "$(cygpath -w "$root/build/test/run.args")" "$(cygpath -w "$log")" &
station=$!
# ended (Ctrl+C), the window station's process takes the tests and their Chrome with it
trap 'kill $station 2>/dev/null || true' EXIT
tail -n +1 -f --pid=$station "$log"
code=0
wait $station || code=$?
trap - EXIT
exit $code

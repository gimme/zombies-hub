# docker/host.sh - shared code of the docker/*.sh scripts that run on the host.
#
# The scripts source this file so that they run the same on Linux and in Git Bash on Windows (Docker Desktop).
#
# abs <dir>   Prints the directory's absolute path in the form docker takes for a host path (for -v, and for a build's
#             context). In Git Bash that is C:/..., because docker.exe is a Windows program and does not understand
#             Git Bash's own paths (/c/..., /tmp).
# uid, gid    The user and group the containers run as, and the images' user. On Linux they are the user's own, so
#             that the user owns what the containers write. In Git Bash they are 1000:1000, the images' default,
#             because Windows owns every file there anyway.
# for_wine <dir> <volume> <image>
#             Prints what to mount for Wine to run the game from <dir>, the game files. On Linux that is <dir> itself.
#             In Git Bash, Wine can't use a Windows folder (Docker Desktop's mount of one): an exe there crashes on its
#             first instruction, and the game loads the fastfiles there wrong. So a container of <image> copies <dir>
#             into the Docker volume <volume> when the files of <dir> have changed since the last copy. The game
#             files, 9 GB, take a minute or so the first time. A note of what was copied last is kept in a volume of
#             its own, <volume>-copied, and not in the copy. The server lists every file of the game files it serves.
#             A note among them would be in every map's list of files, and no player's own install has it, so no
#             player could import a map. The note is deleted before a copy begins, so that a copy that was cut short
#             is made again. A volume with nothing in it (it was removed, and the note's volume was left) is not a
#             copy, whatever the note says.
# engine_args <name>
#             Prints docker run's arguments for the engine build of the server container <name>, one per line. The
#             server downloads the pinned build by itself (internal/bo1zombies). ZOMBIES_ENGINE_URL is passed through
#             when set. ZOMBIES_ENGINE_FILE is the path of a local engine archive: the archive is mounted, and the
#             server is told to use it. In Git Bash the folder that the server unpacks the engine into is the Docker
#             volume <name>-engine, because Wine can't run a program from a Windows folder (for_wine).
#
# Git Bash would also rewrite docker's arguments that look like paths as Windows paths: a path in the container
# (/src), and a volume's host:container pair as a list of two paths (C:\...;C:\Program Files\Git\...).
# MSYS_NO_PATHCONV=1 stops that.
[ -z "${ZOMBIES_ENGINE_FILE:-}" ] || [ -f "$ZOMBIES_ENGINE_FILE" ] ||
    { echo "ZOMBIES_ENGINE_FILE: no file $ZOMBIES_ENGINE_FILE" >&2; exit 1; }
engine_args() {
    local file=${ZOMBIES_ENGINE_FILE:-}
    if [ -n "${ZOMBIES_ENGINE_URL:-}" ]; then
        printf '%s\n' -e "ZOMBIES_ENGINE_URL=$ZOMBIES_ENGINE_URL"
    fi
    if [ -n "$file" ]; then
        printf '%s\n' -v "$(abs "$(dirname "$file")")/$(basename "$file"):/engine.tar.gz:ro" \
            -e ZOMBIES_ENGINE_FILE=/engine.tar.gz
    fi
    if [ -n "${MSYSTEM:-}" ]; then
        printf '%s\n' -v "$1-engine:/config/engine"
    fi
}
if [ -n "${MSYSTEM:-}" ]; then
    export MSYS_NO_PATHCONV=1
    abs() { (cd "${1:?}" && pwd -W); }
    uid=1000 gid=1000
    for_wine() {
        docker run --rm -u 0 --entrypoint sh -v "$(abs "$1"):/from:ro" -v "$2:/to" -v "$2-copied:/note" "$3" -c '
            copied=$(cd /from && find . -type f -printf "%p %s %T@\n" | sort | md5sum)
            [ "$copied" = "$(cat /note/copied 2>/dev/null)" ] && [ -n "$(ls -A /to)" ] && exit
            echo "copying $0 into volume $1 for Wine"
            rm -f /note/copied && find /to -mindepth 1 -delete && cp -R /from/. /to/ &&
                echo "$copied" >/note/copied' "$1" "$2" >&2 &&
            echo "$2"
    }
else
    abs() { (cd "${1:?}" && pwd); }
    uid=$(id -u) gid=$(id -g)
    for_wine() { abs "$1"; }
fi

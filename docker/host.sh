# docker/host.sh - shared code of the docker/*.sh scripts that run on the host.
#
# The scripts source this file so that they run the same on Linux and in Git Bash on Windows (Docker Desktop).
#
# .env        The file .env at the top of the checkout sets the variables that the scripts take, a line each:
#             NAME=value. The value is the rest of the line as it is, so a path with spaces needs no quotes, and may
#             have them. A line that starts with # is a comment. A variable that the environment sets keeps its
#             value. The file is not committed: .env.example is the one to copy, and has the variables that say where
#             the games are.
# abs <dir>   Prints the directory's absolute path in the form docker takes for a host path (for -v, and for a build's
#             context). In Git Bash that is C:/..., because docker.exe is a Windows program and does not understand
#             Git Bash's own paths (/c/..., /tmp). A link to a directory gives the directory it links to.
# uid, gid    The user and group the containers run as, and the images' user. On Linux they are the user's own, so
#             that the user owns what the containers write. In Git Bash they are 1000:1000, the images' default,
#             because Windows owns every file there anyway.
# folders     The folders of game files that the server takes, each at /game/<folder> in its container: one for each
#             title (server/title.go), and t7-server, Steam's dedicated server of Black Ops III, which is a program of
#             its own that Steam installs in a folder of its own (server/t7.go). Each is a game's install folder,
#             where it is on this machine, so that the game is on the disk once. ZOMBIES_T5, ZOMBIES_T6, ZOMBIES_T7
#             and ZOMBIES_T7_SERVER name them (.env). A path that is not absolute is from the top of the checkout.
#             The scripts and the containers only read the folders.
# game_folder <folder>
#             Prints where that folder is on this machine, as abs does, or nothing when no variable names it. It
#             fails, and says so, when the variable names a folder that is not there.
# has_game    Tells whether the game files have a title.
# game_mounts <volume> <image>
#             Prints what to mount for Wine to run the games: a line for each mount, <source>:<path in the
#             container>, for docker run's -v with :ro after it (game_args does that).
#
#             On Linux the mounts are the folders. In Git Bash, Wine can't use a Windows folder (Docker Desktop's
#             mount of one): an exe there crashes on its first instruction, and the game loads the fastfiles there
#             wrong. So a container of <image> copies the game files into the Docker volume <volume>, which is the one
#             mount. Of each folder it copies what the server takes of it, which the image's server lists
#             (zombies-server game-files, server/gamefiles.go): all of Black Ops, Zombies' files of Black Ops II, and
#             of Black Ops III, 87 GB, the 3 GB that a dedicated server reads, with Steam's server of that game
#             without its multiplayer maps, 1 GB. What else a folder has, such as a game's other modes, is not copied.
#             A folder is copied when what the server takes of it has changed since its last copy, the other folders'
#             staying as they are. Black Ops and Black Ops II, 9 to 12 GB each, take some minutes the first time.
#
#             A note of what was copied last of each folder is kept in a volume of its own, <volume>-copied, and not
#             in the copy. The server lists every file of the game files it serves. A note among them would be in
#             every map's list of files, and no player's own install has it, so no player could import a map. A
#             folder's note is deleted before its copy begins, so that a copy that was cut short is made again. A
#             folder with no copy in the volume (the volume was removed, and the note's volume was left) is copied
#             whatever its note says.
# game_args <volume> <image>
#             Prints docker run's arguments for those mounts, one per line.
# send_args   Prints docker run's arguments, one per line, for the game files that the server sends to players'
#             launchers from another folder than its games run on (ZOMBIES_SEND_DIR, server/main.go). On Linux there
#             are none: the games run on the install folders, which have all of a game. In Git Bash the volume has
#             of Black Ops III only what its server reads (game_mounts), which is not what a player's game needs. So
#             that game's install folder is mounted as well, as it is, at /send/t7: the server only reads the files
#             there to send them, which a Windows folder does for.
# engine_args <name>
#             Prints docker run's arguments for the engine build of the server container <name>, one per line. The
#             server downloads the pinned build by itself (internal/bo1zombies). ZOMBIES_ENGINE_URL is passed through
#             when set. ZOMBIES_ENGINE_FILE is the path of a local engine archive: the archive is mounted, and the
#             server is told to use it. In Git Bash the folder that the server unpacks the engine into is the Docker
#             volume <name>-engine, because Wine can't run a program from a Windows folder (game_mounts).
#
# Git Bash would also rewrite docker's arguments that look like paths as Windows paths: a path in the container
# (/src), and a volume's host:container pair as a list of two paths (C:\...;C:\Program Files\Git\...).
# MSYS_NO_PATHCONV=1 stops that.
top=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if [ -f "$top/.env" ]; then
    quoted='^"(.*)"$|^'\''(.*)'\''$'
    while IFS= read -r line || [ -n "$line" ]; do
        line=${line%$'\r'}
        [[ $line =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
        name=${BASH_REMATCH[1]} value=${BASH_REMATCH[2]}
        value=${value%"${value##*[![:space:]]}"} # without the spaces at its end
        [[ ! $value =~ $quoted ]] || value=${BASH_REMATCH[1]}${BASH_REMATCH[2]}
        [ -n "${!name+set}" ] || export "$name=$value"
    done <"$top/.env"
    unset quoted line name value
fi
[ -z "${ZOMBIES_ENGINE_FILE:-}" ] || [ -f "$ZOMBIES_ENGINE_FILE" ] ||
    { echo "ZOMBIES_ENGINE_FILE: no file $ZOMBIES_ENGINE_FILE" >&2; exit 1; }
folders="t5 t6 t7 t7-server"
game_folder() {
    local name path
    name=ZOMBIES_${1^^}
    name=${name//-/_}
    path=${!name:-}
    [ -n "$path" ] || return 0
    case $path in
        /* | [A-Za-z]:*) ;;
        *) path=$top/$path ;;
    esac
    [ -d "$path" ] || { echo "$name: no folder $path (.env)" >&2; return 1; }
    abs "$path"
}
has_game() {
    local f path
    for f in t5 t6 t7; do
        path=$(game_folder "$f") || return
        [ -z "$path" ] || [ ! -d "$path/zone" ] || return 0
    done
    return 1
}
game_args() {
    local mounts mount
    mounts=$(game_mounts "$@") || return
    while IFS= read -r mount; do
        printf '%s\n' -v "$mount:ro"
    done <<<"$mounts"
}
send_args() {
    local path
    [ -n "${MSYSTEM:-}" ] || return 0
    path=$(game_folder t7) || return
    [ -z "$path" ] || printf '%s\n' -v "$path:/send/t7:ro" -e ZOMBIES_SEND_DIR=/send
}
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
    game_mounts() {
        local f path from=()
        for f in $folders; do
            path=$(game_folder "$f") || return
            [ -z "$path" ] || from+=(-v "$path:/from/$f:ro")
        done
        docker run --rm -u 0 --entrypoint bash -e FOLDERS="$folders" "${from[@]}" -v "$1:/to" -v "$1-copied:/note" \
            "$2" -c '
            set -o pipefail
            rm -f /note/copied # the note of a copy of every folder at once, as this script made them before
            for f in $FOLDERS; do
                if [ ! -d "/from/$f" ]; then
                    rm -rf "/to/$f" "/note/$f"
                    continue
                fi
                cd "/from/$f" || exit
                # what the server takes of the folder, and each such file with its size and time, to tell a change by
                zombies-server game-files "$f" . >/tmp/taken || exit
                copied=$(xargs -0 -r -a /tmp/taken sh -c "find \"\$@\" -maxdepth 0 -printf \"%p %s %T@\\n\"" sh |
                    sort | md5sum) || exit
                [ "$copied" = "$(cat "/note/$f" 2>/dev/null)" ] && [ -d "/to/$f" ] && continue
                echo "copying what the server reads of $f into volume $0 for Wine"
                rm -f "/note/$f" && rm -rf "/to/$f" && mkdir "/to/$f" &&
                    tar -c --null -T /tmp/taken | tar -x -C "/to/$f" && echo "$copied" >"/note/$f" || exit
            done' "$1" >&2 || return
        echo "$1:/game"
    }
else
    abs() { (cd "${1:?}" && pwd -P); }
    uid=$(id -u) gid=$(id -g)
    game_mounts() {
        local f path
        for f in $folders; do
            path=$(game_folder "$f") || return
            [ -z "$path" ] || echo "$path:/game/$f"
        done
    }
fi

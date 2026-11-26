#!/bin/sh
# docker/entrypoint.sh - start the server in its image (docker/Dockerfile, target server).
#
# The server runs as the user PUID:PGID, which owns what the server writes in /config. The server runs the games as the
# user game (uid 3075, the owner of the image's Wine prefix) with group PGID. A game that is taken over through its
# network code then can't read the server's files (zombies.db has mode 0600 and belongs to the server's user) or the
# server's environment. Unraid's convention is PUID=99 and PGID=100 (nobody:users). When they are not set, they are
# the image's UID and GID build args.
#
# The container starts as root so that it can switch to those users. The script gives the server every file in /config
# that another user owns. It also gives the server three capabilities: setuid and setgid to run the games as the user
# game, and kill to stop them, because they belong to another user.
#
# With NET_ADMIN (docker run --cap-add NET_ADMIN, as in compose.yaml and Unraid's template), the script also sets up
# the container's firewall. The firewall lets the packets of the user game reach only two kinds of address. The first
# is the relay's seats, which are each game's players as the game sees them (127.1.x.y, port 28960: server/relay.go,
# seatPort in server/rooms.go). The second is the address that the server watches each game from, to check that the
# game still answers (127.2.x.y, port 28960: server/game.go, watchAddr). A game that is taken over then reaches
# nothing else: not the internet, not Docker's DNS, and not the server's web port. The packets of the user game jump
# to a chain of their own. The other way would be to accept every other user's packets ahead of the drop. That would
# drop the packets that belong to no socket, such as the kernel's TCP handshakes and resets, because they match no
# uid. Without NET_ADMIN, the script prints a warning, and the games run all the same.
#
# Before the firewall is up, the script has the server fetch what Black Ops III's dedicated server downloads from the
# game's publisher as it starts, where the game files have that game and the server has not fetched it yet
# (zombies-server -t7-fetch, server/t7.go: t7Fetch). That is one run of the game as the games' user, which no player
# reaches, and the last time that user reaches the internet.
#
# With docker run --user, the user is already chosen. The server starts as that user and runs the games as that user
# too. Wine allows that only for the owner of its prefix, game (--user 3075).
set -eu
if [ "$(id -u)" != 0 ]; then
    exec zombies-server "$@"
fi
uid=${PUID:-1000} gid=${PGID:-1000}
if [ "$uid" = "$(id -u game)" ]; then
    echo "entrypoint: PUID can't be $uid, the games' user" >&2
    exit 1
fi
mkdir -p /config
find /config \( ! -user "$uid" -o ! -group "$gid" \) ! -path '/config/game/*' -exec chown -h "$uid:$gid" {} + ||
    echo "entrypoint: can't give all of /config to $uid:$gid" >&2
export HOME=/home/game USER=game LOGNAME=game
# the server's user, with the three capabilities
as_server="setpriv --reuid $uid --regid $gid --clear-groups --inh-caps=-all,+setuid,+setgid,+kill"
as_server="$as_server --ambient-caps=-all,+setuid,+setgid,+kill"
$as_server zombies-server -t7-fetch "$@" ||
    echo "entrypoint: the server plays no Black Ops III until its dedicated server has fetched its files" >&2
if ! err=$(nft -f - 2>&1 <<EOF
table inet zombies_games
flush table inet zombies_games
table inet zombies_games {
    chain output {
        type filter hook output priority filter; policy accept;
        meta skuid $(id -u game) jump game
    }
    chain game {
        ip daddr 127.1.0.0/16 udp dport 28960 accept
        ip daddr 127.2.0.0/16 udp dport 28960 accept
        drop
    }
}
EOF
); then
    echo "entrypoint: the games can reach the network, as only NET_ADMIN lets a firewall stop them" \
        "(docker run --cap-add NET_ADMIN): $(echo "$err" | head -n 1)" >&2
fi
exec $as_server zombies-server "$@"

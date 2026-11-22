#!/bin/sh
# docker/wineprefix-init.sh - create $WINEPREFIX. docker/Dockerfile runs it at image build time, as the image's user.
#
# wineboot copies Wine's own files into the prefix: hundreds of MB that are byte for byte what
# /usr/lib/*/wine/*-windows already holds. The script replaces those copies with symlinks to the originals, which Wine
# loads the same way.
set -eu
wine wineboot --init
# With ShowCrashDialog off, a crash prints Wine's backtrace and ends the process. Otherwise Wine would open a crash
# dialog that nobody can click (there is no display, or only Xvfb), and the process would hang there.
wine reg add 'HKCU\Software\Wine\WineDbg' /v ShowCrashDialog /t REG_DWORD /d 0 /f >/dev/null
while pgrep -u "$(id -u)" wineserver >/dev/null; do sleep 1; done
# wineserver's directory (/tmp/wine-XXXXXX, which $WINEPREFIX/wineserver names) belongs to the user who ran
# wineserver. If it were left in the image, it would break a container that runs as another user
# (docker/entrypoint.sh). The first run of wine makes the directory again.
server=$(cat "$WINEPREFIX/wineserver" 2>/dev/null || true)
[ -z "$server" ] || rm -rf "/tmp/$server"
rm -f "$WINEPREFIX/wineserver"
# Each file is compared with both of Wine's sets of files, because a 64-bit prefix has 32-bit and 64-bit files of the
# same name.
find "$WINEPREFIX/drive_c/windows" -type f | while IFS= read -r f; do
    name=$(basename "$f")
    for lib in /usr/lib/i386-linux-gnu/wine/i386-windows /usr/lib/x86_64-linux-gnu/wine/x86_64-windows; do
        if [ -f "$lib/$name" ] && cmp -s "$f" "$lib/$name"; then
            ln -sf "$lib/$name" "$f"
            break
        fi
    done
done
du -sh "$WINEPREFIX"

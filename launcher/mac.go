package main

import (
	"errors"
	"strconv"
)

// On a Mac the launcher runs in Wine, which the script mac.sh starts it in. The launcher updates itself there as it
// does on Windows (update.go), but the script does not: it changes only when the player pastes the room's line into
// Terminal again, which fetches the latest release's script and runs its setup. That setup also brings
// plutonium-mac-aio, which has the Wine, to the commit that the script names.
//
// So the script says which version it is, in the variable macEnv, as it starts the launcher. A build of the launcher
// that needs a later script (macNeeds) does not play. It ends, and the player's room shows the line for Terminal
// (launcherapi.Report: Setup). A launcher that no script started, as on Windows, has no such variable.

// macEnv is the variable in which mac.sh gives the launcher the script's version (mac_version there).
const macEnv = "ZOMBIES_LAUNCHER_MAC"

// macNeeds is the lowest version of mac.sh that this build of the launcher plays with. Raise it to the script's
// mac_version when a change to the script is one that the launcher or the game needs, such as another commit of
// plutonium-mac-aio for a later Plutonium. TestMacScript checks that the script is of that version at least.
const macNeeds = 1

// errSetup is the error of a launcher that runs on a Mac through a script older than macNeeds.
var errSetup = errors.New("This Mac's Zombies Launcher needs an update: paste the line from the room's page into Terminal again.")

// macOld reports whether the launcher runs on a Mac through a script older than this build needs. mac is the
// script's version, as macEnv has it, or "" where no script started the launcher.
func macOld(mac string) bool {
	if mac == "" {
		return false
	}
	v, err := strconv.Atoi(mac)
	return err != nil || v < macNeeds
}

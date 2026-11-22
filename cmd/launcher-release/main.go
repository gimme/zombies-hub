// launcher-release makes the maker's key for the launcher's updates, and signs a build of the launcher with it
// (internal/release).
//
//	launcher-release key <private key file> <public key file>
//	        makes a key. The private half is for the repository's secret LAUNCHER_KEY and a safe copy, outside the
//	        repository. The public half is for launcher/release.pub, and every launcher built from then on has it built
//	        in. The command overwrites neither file.
//	launcher-release sign <private key file> <version> <launcher> <statement file>
//	        signs the build. It writes the statement that the launcher's release has next to the build
//	        (.github/workflows/launcher.yml). Launchers with the key's public half accept the statement as their
//	        maker's. The command signs only a build that has that public half built in, so that the launchers that
//	        update to the build go on accepting their maker's builds.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"zombies/internal/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "launcher-release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 3 && args[0] == "key":
		for _, file := range args[1:] {
			if data, err := os.ReadFile(file); err == nil && len(data) > 0 {
				return fmt.Errorf("%s is there already: a new key would leave the launchers with the old one without updates", file)
			}
		}
		public, private, err := release.NewKey()
		if err != nil {
			return err
		}
		if err := os.WriteFile(args[1], []byte(private+"\n"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(args[2], []byte(public+"\n"), 0o644)
	case len(args) == 5 && args[0] == "sign":
		text, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		key, err := release.ParsePrivate(string(text))
		if err != nil {
			return fmt.Errorf("%s: %v", args[1], err)
		}
		version, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil || version <= 0 {
			return fmt.Errorf("the version is a number above 0, not %q", args[2])
		}
		program, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		// A launcher replaces itself with this build, and then accepts the build after it by the key that this
		// build has built in. A build that was signed without the key built in, or with another key built in, would
		// be the last build those launchers ever accepted.
		if !bytes.Contains(program, []byte(release.PublicOf(key))) {
			return fmt.Errorf("%s was not built with this key's public half inside (launcher/release.pub): the "+
				"launchers that took it in their place would take no build after it", args[3])
		}
		signed, err := release.Sign(key, release.Statement{Version: version, Hash: release.HashOf(program)})
		if err != nil {
			return err
		}
		out, _ := json.Marshal(signed)
		return os.WriteFile(args[4], out, 0o644)
	}
	return fmt.Errorf("usage: launcher-release key <private key file> <public key file> | " +
		"sign <private key file> <version> <launcher> <statement file>")
}

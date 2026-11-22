package main

import (
	_ "embed"
	"strings"
)

// A server sends game files, and some of a game's files are programs: the game's own, and the installers of what the
// game needs. A launcher that accepted any such file from a server would run whatever the server's owner, or someone
// who broke into the server, put there. So the launcher accepts a program from a server only when it is one of the
// game's own. The launcher has a list of those built in, with each program's path and the hash of its contents as
// Steam installs it. The files that a game only reads (maps, sounds, pictures) are accepted as the server has them.

//go:embed t6-programs.txt
var t6ProgramList string

// programEnds are the endings of the names of the files that Windows, or an installer, runs.
var programEnds = []string{".exe", ".dll", ".cab", ".inf", ".bat", ".cmd", ".com", ".scr", ".msi", ".ps1", ".vbs",
	".js", ".jar", ".lnk", ".sys", ".ocx", ".cpl", ".hta", ".wsf", ".reg"}

// isProgram reports whether a game's file is one that a PC runs, judging by its name.
func isProgram(path string) bool {
	path = strings.ToLower(path)
	for _, end := range programEnds {
		if strings.HasSuffix(path, end) {
			return true
		}
	}
	return false
}

// knownPrograms reads a list of a game's own programs (t6-programs.txt). It returns the hash of each program, by the
// program's path in lowercase.
func knownPrograms(list string) map[string]string {
	known := map[string]string{}
	for _, line := range strings.Split(list, "\n") {
		hash, path, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && !strings.HasPrefix(hash, "#") {
			known[path] = hash
		}
	}
	return known
}

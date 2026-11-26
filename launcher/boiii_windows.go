package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// BOIII's console is a window of its own beside the game's (BOIII's src/client/component/console.cpp): a window of
// the class "BOIII WinConsole", with the line that a player types commands into as a child of it, an edit control
// with the id 0x65. The launcher types a line there as a player does: it sets the control's text, and sends it the
// Enter key. Unlike a console of Windows, which the game of Black Ops II has (process.join), such a window takes
// messages from any program of the same user.

var (
	enumWindows              = user32.NewProc("EnumWindows")
	getClassNameW            = user32.NewProc("GetClassNameW")
	getWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	getDlgItem               = user32.NewProc("GetDlgItem")
	sendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
)

// consoles finds BOIII's console window among the windows of some processes (consoleOf). Windows calls each back for
// every window, and Go makes such a function only so many times, so it is made once, and one search runs at a time.
var consoles struct {
	sync.Mutex
	each  uintptr
	pids  []uint32
	found windows.Handle
}

// consoleOf returns the console window of the BOIII that is one of the processes pids, or 0 when none has one.
func consoleOf(pids []uint32) windows.Handle {
	consoles.Lock()
	defer consoles.Unlock()
	if consoles.each == 0 {
		consoles.each = windows.NewCallback(func(wnd windows.Handle, _ uintptr) uintptr {
			var pid uint32
			getWindowThreadProcessID.Call(uintptr(wnd), uintptr(unsafe.Pointer(&pid)))
			of := false
			for _, id := range consoles.pids {
				of = of || id == pid
			}
			if !of {
				return 1
			}
			var class [64]uint16
			n, _, _ := getClassNameW.Call(uintptr(wnd), uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
			if windows.UTF16ToString(class[:n]) != boiiiConsoleClass {
				return 1
			}
			consoles.found = wnd
			return 0 // no more windows
		})
	}
	consoles.pids, consoles.found = pids, 0
	enumWindows.Call(consoles.each, 0)
	return consoles.found
}

const (
	boiiiConsoleClass = "BOIII WinConsole"
	boiiiConsoleInput = 0x65
	wmSetText         = 0x000C
	wmChar            = 0x0102
	smtoAbortIfHung   = 0x0002
)

// pids returns the ids of the processes of the job that has the program and the programs it started, or the
// program's own id alone when Windows gave it no job. BOIII starts itself again once it has replaced its program
// with a later build, so the game may be another process than the one the launcher started.
func (p *process) pids() []uint32 {
	if p.job != 0 {
		// JOBOBJECT_BASIC_PROCESS_ID_LIST: two counts, and then the ids, each the size of a pointer
		var list struct {
			assigned, listed uint32
			ids              [64]uintptr
		}
		const basicProcessIDList = 3
		if windows.QueryInformationJobObject(p.job, basicProcessIDList, uintptr(unsafe.Pointer(&list)),
			uint32(unsafe.Sizeof(list)), nil) == nil {
			ids := make([]uint32, 0, list.listed)
			for _, id := range list.ids[:min(int(list.listed), len(list.ids))] {
				ids = append(ids, uint32(id))
			}
			return ids
		}
	}
	return []uint32{p.pid}
}

// typeWindow types line into the console window of the BOIII that the program is, then Enter (the comment at the
// top).
func (p *process) typeWindow(line string) error {
	console := consoleOf(p.pids())
	if console == 0 {
		return errors.New("the game has no console window")
	}
	input, _, _ := getDlgItem.Call(uintptr(console), boiiiConsoleInput)
	if input == 0 {
		return errors.New("the game's console window has no line to type into")
	}
	text, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return err
	}
	// A game that is busy takes the text when it gets to it, or not at all within the two seconds.
	var result uintptr
	if r, _, err := sendMessageTimeoutW.Call(input, wmSetText, 0, uintptr(unsafe.Pointer(text)), smtoAbortIfHung, 2000,
		uintptr(unsafe.Pointer(&result))); r == 0 {
		return fmt.Errorf("typing into the game's console: %w", err)
	}
	if r, _, err := postMessage.Call(input, wmChar, '\r', 0); r == 0 {
		return fmt.Errorf("typing into the game's console: %w", err)
	}
	return nil
}

// steamInstalled reports whether the PC has Steam, as BOIII looks for it before it starts a game: the folder that the
// registry names as Steam's has Steam's program.
func steamInstalled() bool {
	for _, dir := range regStrings(registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath") {
		if isFile(filepath.Join(dir, "steam.exe")) {
			return true
		}
	}
	return false
}

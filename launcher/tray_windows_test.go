//go:build windows

package main

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The tray icon has a hidden window behind it, which is gone again once the icon is taken away.
func TestTrayWindow(t *testing.T) {
	findAfter := user32.NewProc("FindWindowExW")
	owner := user32.NewProc("GetWindowThreadProcessId")
	class, _ := windows.UTF16PtrFromString("ZombiesLauncherTray")
	// this program's window of the class: a launcher that runs on the PC has one too
	mine := func() bool {
		for w := uintptr(0); ; {
			if w, _, _ = findAfter.Call(0, w, uintptr(unsafe.Pointer(class)), 0); w == 0 {
				return false
			}
			var pid uint32
			owner.Call(w, uintptr(unsafe.Pointer(&pid)))
			if int(pid) == os.Getpid() {
				return true
			}
		}
	}
	stop := showTray("zombies.example.com", func() {}, func() {})
	if !mine() {
		t.Error("no window behind the tray icon")
	}
	stop()
	if mine() {
		t.Error("the tray icon's window is still there once the icon is taken away")
	}
}

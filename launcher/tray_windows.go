//go:build windows

package main

import (
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The launcher's icon in the tray is all that the launcher shows while it stays (serve). A click opens the icon's
// menu (menu.go), which opens the server's page, shows and changes the game files, or quits the launcher. Windows
// needs a window to send an icon's clicks to. The launcher makes a hidden window for them, on a thread of its own.

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	shellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	shellExecute        = shell32.NewProc("ShellExecuteW")
	getModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	registerClassEx     = user32.NewProc("RegisterClassExW")
	createWindowEx      = user32.NewProc("CreateWindowExW")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	getMessage          = user32.NewProc("GetMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	postMessage         = user32.NewProc("PostMessageW")
	postQuitMessage     = user32.NewProc("PostQuitMessage")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	getCursorPos        = user32.NewProc("GetCursorPos")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	loadImage           = user32.NewProc("LoadImageW")
	loadIcon            = user32.NewProc("LoadIconW")
	registerMessage     = user32.NewProc("RegisterWindowMessageW")
)

const (
	wmDestroy   = 0x0002
	wmClose     = 0x0010
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmTray      = 0x8000 + 1 // WM_APP + 1: the icon's clicks

	nimAdd, nimDelete           = 0, 2
	nifMessage, nifIcon, nifTip = 1, 2, 4
	mfString, mfGrayed, mfSep   = 0x0, 0x1, 0x800
	tpmRightButton, tpmBottom   = 0x2, 0x20
	tpmNoNotify, tpmReturnCmd   = 0x80, 0x100
	imageIcon, lrDefaultSize    = 1, 0x40
	lrShared, idiApplication    = 0x8000, 32512
	swShowNormal                = 1
)

type wndClassEx struct {
	size, style                        uint32
	wndProc                            uintptr
	clsExtra, wndExtra                 int32
	instance, icon, cursor, background windows.Handle
	menuName, className                *uint16
	iconSm                             windows.Handle
}

type notifyIconData struct {
	size            uint32
	wnd             windows.Handle
	id, flags       uint32
	callbackMessage uint32
	icon            windows.Handle
	tip             [128]uint16
	state, mask     uint32
	info            [256]uint16
	version         uint32
	infoTitle       [64]uint16
	infoFlags       uint32
	guid            windows.GUID
	balloonIcon     windows.Handle
}

type point struct{ x, y int32 }

type message struct {
	wnd            windows.Handle
	message        uint32
	wParam, lParam uintptr
	time           uint32
	pt             point
}

// tray is the program's one tray icon. It is a global for the window's procedure, which Windows calls with no
// reference to the icon.
var tray struct {
	menu        func() []menuItem
	icon        notifyIconData
	taskbarMade uint32 // the message Explorer sends every window when its taskbar restarts: add the icon again
}

// showTray puts the launcher's icon in the tray, with the server's host in its tip. A click on the icon shows the
// items that menu returns then. The item the player chooses is done on a goroutine of its own. stop removes the icon.
func showTray(host string, menu func() []menuItem) (stop func()) {
	tray.menu = menu
	ready, done := make(chan windows.Handle), make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread() // a window's messages go to the thread that made it
		defer runtime.UnlockOSThread()
		instance, _, _ := getModuleHandle.Call(0)
		class, _ := windows.UTF16PtrFromString("ZombiesLauncherTray")
		wc := wndClassEx{wndProc: windows.NewCallback(trayProc), instance: windows.Handle(instance), className: class}
		wc.size = uint32(unsafe.Sizeof(wc))
		registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		wnd, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0,
			0, 0, instance, 0)
		if wnd == 0 {
			ready <- 0
			return
		}
		name, _ := windows.UTF16PtrFromString("TaskbarCreated")
		made, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(name)))
		tray.taskbarMade = uint32(made)
		// Use the program's own icon, which the image's build links in (go-winres: APP), or else Windows' icon for
		// any program.
		app, _ := windows.UTF16PtrFromString("APP")
		icon, _, _ := loadImage.Call(instance, uintptr(unsafe.Pointer(app)), imageIcon, 0, 0, lrDefaultSize|lrShared)
		if icon == 0 {
			icon, _, _ = loadIcon.Call(0, idiApplication)
		}
		tray.icon = notifyIconData{wnd: windows.Handle(wnd), id: 1, flags: nifMessage | nifIcon | nifTip,
			callbackMessage: wmTray, icon: windows.Handle(icon)}
		tray.icon.size = uint32(unsafe.Sizeof(tray.icon))
		tip, _ := windows.UTF16FromString(appName + ": " + host)
		copy(tray.icon.tip[:len(tray.icon.tip)-1], tip)
		shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&tray.icon)))
		ready <- windows.Handle(wnd)
		var m message
		for {
			got, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(got) <= 0 {
				return
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&m)))
			dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	wnd := <-ready
	return func() {
		if wnd != 0 {
			postMessage.Call(uintptr(wnd), wmClose, 0, 0)
			<-done
		}
	}
}

// trayProc is the hidden window's procedure. It handles the icon's clicks and the window's end.
func trayProc(wnd windows.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch {
	case msg == wmTray && (lParam == wmLButtonUp || lParam == wmRButtonUp):
		trayMenu(wnd)
	case msg == wmClose:
		destroyWindow.Call(uintptr(wnd))
	case msg == wmDestroy:
		shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&tray.icon)))
		postQuitMessage.Call(0)
	case tray.taskbarMade != 0 && msg == tray.taskbarMade:
		shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&tray.icon)))
	default:
		r, _, _ := defWindowProc.Call(uintptr(wnd), uintptr(msg), wParam, lParam)
		return r
	}
	return 0
}

// trayMenu shows the icon's menu at the pointer, with the items that tray.menu returns now, and does the item that
// the player chooses. An item's number in the menu is its place in the list, from 1. A menu takes & for the mark of a
// shortcut key, and && for &: a folder's name may have one.
func trayMenu(wnd windows.Handle) {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	items := tray.menu()
	for i, it := range items {
		flags := uintptr(mfString)
		switch {
		case it.text == "":
			flags = mfSep
		case it.do == nil:
			flags |= mfGrayed
		}
		t, _ := windows.UTF16PtrFromString(strings.ReplaceAll(it.text, "&", "&&"))
		appendMenu.Call(menu, flags, uintptr(i+1), uintptr(unsafe.Pointer(t)))
	}
	var at point
	getCursorPos.Call(uintptr(unsafe.Pointer(&at)))
	// The menu closes on a click elsewhere only when its window is in front (Windows' own note on tray menus).
	setForegroundWindow.Call(uintptr(wnd))
	chosen, _, _ := trackPopupMenu.Call(menu, tpmRightButton|tpmBottom|tpmReturnCmd|tpmNoNotify, uintptr(at.x),
		uintptr(at.y), 0, uintptr(wnd), 0)
	postMessage.Call(uintptr(wnd), 0, 0, 0)
	if i := int(chosen) - 1; i >= 0 && i < len(items) && items[i].do != nil {
		go items[i].do()
	}
}

// openPage opens a page in the player's browser.
func openPage(url string) {
	verb, _ := windows.UTF16PtrFromString("open")
	page, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return
	}
	shellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(page)), 0, 0, swShowNormal)
}

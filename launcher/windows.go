//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"zombies/internal/boiii"
	"zombies/internal/launcherapi"
)

// This file has the launcher's calls to Windows.

// classKey is the registry key of the launcher's URL scheme. It is among the user's own URL schemes, which need no
// administrator.
const classKey = `Software\Classes\` + launcherapi.Scheme

// nameKey is the registry key where Windows reads the name of the program that opens a link. A browser shows the name
// when it asks the player ("Open Zombies Launcher?"). Without the key, the name is the file's name.
const nameKey = `Software\Classes\Applications\` + exeName

// appKey is the launcher's entry in Windows' list of this user's installed apps (Settings, Apps), where the user
// uninstalls it.
const appKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\zombies-launcher`

// register makes Windows start exe with a link of the launcher's scheme, for this user (a browser asks them first).
// It also adds exe to the user's installed apps, where they uninstall it.
func register(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, classKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("", "URL:"+appName); err != nil {
		return err
	}
	if err := k.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}
	c, _, err := registry.CreateKey(registry.CURRENT_USER, classKey+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetStringValue("", `"`+exe+`" "%1"`); err != nil {
		return err
	}
	n, _, err := registry.CreateKey(registry.CURRENT_USER, nameKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer n.Close()
	if err := n.SetStringValue("FriendlyAppName", appName); err != nil {
		return err
	}
	if err := n.SetStringValue("NoOpenWith", ""); err != nil { // the launcher opens no kind of file
		return err
	}
	a, _, err := registry.CreateKey(registry.CURRENT_USER, appKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer a.Close()
	for _, v := range [][2]string{{"DisplayName", appName}, {"UninstallString", `"` + exe + `" uninstall`},
		{"InstallLocation", filepath.Dir(exe)}, {"DisplayIcon", exe}} {
		if err := a.SetStringValue(v[0], v[1]); err != nil {
			return err
		}
	}
	for _, name := range []string{"NoModify", "NoRepair"} { // the list offers only Uninstall
		if err := a.SetDWordValue(name, 1); err != nil {
			return err
		}
	}
	return nil
}

func unregister() error {
	for _, key := range []string{appKey, nameKey, classKey + `\shell\open\command`, classKey + `\shell\open`, classKey + `\shell`,
		classKey} {
		if err := registry.DeleteKey(registry.CURRENT_USER, key); err != nil && err != registry.ErrNotExist {
			return err
		}
	}
	return nil
}

// regStrings returns a registry value as 64-bit programs see it and as 32-bit programs see it.
func regStrings(root registry.Key, path, name string) []string {
	var out []string
	for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
		k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|view)
		if err != nil {
			continue
		}
		if v, _, err := k.GetStringValue(name); err == nil && v != "" {
			out = append(out, v)
		}
		k.Close()
	}
	return out
}

// steamCopies returns the folders where Steam may have installed the title t. A game is one Steam app or more (Black
// Ops II is three: Zombies, the campaign, multiplayer), and the registry has an install folder for each. The other
// folders are the one in the key of the game's own installer, which Black Ops II has, and the game's folder in each
// library of the installed Steam.
func steamCopies(t *title) []string {
	var dirs []string
	for _, app := range t.apps {
		dirs = append(dirs, regStrings(registry.LOCAL_MACHINE,
			`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Steam App `+app, "InstallLocation")...)
	}
	if t == titleT6 {
		dirs = append(dirs, regStrings(registry.LOCAL_MACHINE, `SOFTWARE\Activision\Black Ops II`, "InstallPath")...)
	}
	steam := regStrings(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath")
	steam = append(steam, regStrings(registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath")...)
	steam = append(steam, `C:\Program Files (x86)\Steam`)
	for _, s := range steam {
		dirs = append(dirs, steamLibraries(filepath.FromSlash(s), t.folder)...)
	}
	return dirs
}

// startProgram runs exe with the command line exactly as given, because the game parses its own command line. The
// program runs in dir, in a console window of its own, and is left running, as "start" does from a Command Prompt.
// Its standard input and output are that console, and it inherits no handle of the launcher's. os/exec would give it
// the launcher's standard handles, or NUL in their place: the game would then read no line typed into its console
// (process.join), and Windows would let no other program attach to the console to type one. The returned game
// follows the program and any program it starts in turn, through a Windows job that has them all.
func startProgram(exe, commandLine, dir string) (game, error) {
	exeName, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	line, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, err
	}
	in, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	si := &windows.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(exeName, line, nil, nil, false,
		windows.CREATE_NEW_CONSOLE|windows.CREATE_NEW_PROCESS_GROUP, nil, in, si, &pi); err != nil {
		return nil, &os.PathError{Op: "start", Path: exe, Err: err}
	}
	windows.CloseHandle(pi.Thread)
	p := &process{pid: pi.ProcessId, h: pi.Process, boiii: strings.EqualFold(filepath.Base(exe), boiii.Exe)}
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		if windows.AssignProcessToJobObject(job, p.h) == nil {
			p.job = job
		} else {
			windows.CloseHandle(job)
		}
	}
	return p, nil
}

// process is a program that the launcher started. pid is its process's ID, and h its process. job is the job that has
// the process and the programs it starts, when Windows gave it one. boiii marks BOIII, whose console is a window of
// its own (boiii_windows.go).
type process struct {
	pid    uint32
	h, job windows.Handle
	boiii  bool
}

// jobAccounting is Windows' JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

func (p *process) running() bool {
	if p.job != 0 {
		var info jobAccounting
		const basicAccounting = 1 // JobObjectBasicAccountingInformation
		if windows.QueryInformationJobObject(p.job, basicAccounting, uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)), nil) == nil {
			return info.ActiveProcesses > 0
		}
	}
	if p.h == 0 {
		return false
	}
	e, _ := windows.WaitForSingleObject(p.h, 0)
	return e == uint32(windows.WAIT_TIMEOUT)
}

// stop closes the program and the programs it started, as Task Manager's End task does, and waits for them to be
// gone.
func (p *process) stop() {
	if p.job != 0 {
		windows.TerminateJobObject(p.job, 1)
	} else if p.h != 0 {
		windows.TerminateProcess(p.h, 1)
	}
	for i := 0; i < 100 && p.running(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

var (
	attachConsole      = kernel32.NewProc("AttachConsole")
	freeConsole        = kernel32.NewProc("FreeConsole")
	writeConsoleInputW = kernel32.NewProc("WriteConsoleInputW")
)

// keyEvent is Windows' INPUT_RECORD for a KEY_EVENT_RECORD.
type keyEvent struct {
	eventType   uint16
	_           uint16
	keyDown     int32
	repeatCount uint16
	virtualKey  uint16
	virtualScan uint16
	char        uint16
	controlKeys uint32
}

// join types line into the program's console window, then Enter, as if the player typed it there. Plutonium's
// bootstrapper is a console program, and the game runs in its process and reads that console's input, so the line runs
// as a command of the game's console, whether its window is in front or not. A program has one console at most: the
// launcher attaches to the game's for the moment it types. A launcher that a link started has no console of its own
// (-H=windowsgui). One started from a terminal has that terminal's, and can't attach to another.
func (p *process) join(line string) error {
	if p.boiii {
		return p.typeWindow(line)
	}
	if r, _, err := attachConsole.Call(uintptr(p.pid)); r == 0 {
		return fmt.Errorf("attaching to the game's console: %w", err)
	}
	defer freeConsole.Call()
	name, err := windows.UTF16PtrFromString("CONIN$")
	if err != nil {
		return err
	}
	in, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("opening the game's console: %w", err)
	}
	defer windows.CloseHandle(in)
	var keys []keyEvent
	for _, c := range utf16.Encode([]rune(line + "\r")) {
		key := keyEvent{eventType: 1, keyDown: 1, repeatCount: 1, char: c} // KEY_EVENT
		if c == '\r' {
			key.virtualKey = 0x0D // VK_RETURN
		}
		up := key
		up.keyDown = 0
		keys = append(keys, key, up)
	}
	var n uint32
	if r, _, err := writeConsoleInputW.Call(uintptr(in), uintptr(unsafe.Pointer(&keys[0])), uintptr(len(keys)),
		uintptr(unsafe.Pointer(&n))); r == 0 {
		return fmt.Errorf("typing into the game's console: %w", err)
	}
	if int(n) != len(keys) {
		return fmt.Errorf("the game's console took %d of %d keys", n, len(keys))
	}
	return nil
}

// presses returns two channels, each of which gets a value each time the player asks something of their launcher (id)
// while it runs: join, to join again (the room's button), and solo, to play alone (the lobby's Play local). Each
// waits for a Windows event. The launcher that another link of theirs starts sets the event (press) before it ends.
func presses(id string) (join, solo <-chan struct{}) {
	return pressesOf(id, "join"), pressesOf(id, "solo")
}

func press(id string, solo bool) {
	if solo {
		pressOf(id, "solo")
	} else {
		pressOf(id, "join")
	}
}

func pressesOf(id, what string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	name, err := windows.UTF16PtrFromString(`Local\zombies-launcher-` + id + `-` + what)
	if err != nil {
		return ch
	}
	h, _ := windows.CreateEvent(nil, 0, 0, name)
	if h == 0 {
		return ch
	}
	go func() {
		for {
			if e, _ := windows.WaitForSingleObject(h, windows.INFINITE); e != windows.WAIT_OBJECT_0 {
				return
			}
			select {
			case ch <- struct{}{}:
			default: // a press is waiting already
			}
		}
	}()
	return ch
}

func pressOf(id, what string) {
	name, err := windows.UTF16PtrFromString(`Local\zombies-launcher-` + id + `-` + what)
	if err != nil {
		return
	}
	if h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name); err == nil {
		windows.SetEvent(h)
		windows.CloseHandle(h)
	}
}

// removeLater deletes the launcher's program once this run has ended, and then its folder dir if it is empty.
// Windows lets no program delete itself while it runs, so a Command Prompt without a window does it, two seconds
// later. The two names go into its command line between quotes. Quotes keep every character that a file's name can
// have, except a percent sign.
func removeLater(exe, dir string) error {
	if strings.ContainsAny(exe+dir, `%"`) {
		return errors.New("a name the Command Prompt would read otherwise")
	}
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	line := `"` + shell + `" /d /s /c "ping -n 3 127.0.0.1 >nul & del /f /q "` + exe + `" >nul 2>&1 & rmdir "` + dir +
		`" >nul 2>&1"`
	cmd := &exec.Cmd{Path: shell, Dir: os.TempDir(), SysProcAttr: &syscall.SysProcAttr{CmdLine: line,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// rerun starts the launcher again with the same link, in this launcher's window, and leaves it running there.
func rerun(exe, link string) error {
	cmd := exec.Command(exe, link)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), updatedEnv+"=1")
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// single takes the turn of a player's launcher (id: link.id). One launcher has the turn at a time, and the launcher
// that another link of the player starts finds the turn taken. release gives up the turn. ok is false while another
// launcher has it.
func single(id string) (release func(), ok bool) {
	name, err := windows.UTF16PtrFromString(`Local\zombies-launcher-` + id)
	if err != nil {
		return func() {}, true
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(h)
		return nil, false
	}
	if err != nil {
		return func() {}, true // no way to tell: go on
	}
	return func() { windows.CloseHandle(h) }, true
}

// markName names the Windows mutex that shows that a launcher runs a link. mark creates it and keeps it until the
// program ends. launchers reports whether any launcher has it. Uninstalling does not go on while one does.
const markName = `Local\zombies-launcher`

func mark() {
	if name, err := windows.UTF16PtrFromString(markName); err == nil {
		windows.CreateMutex(nil, false, name) // the handle stays open until the program ends
	}
}

func launchers() bool {
	name, err := windows.UTF16PtrFromString(markName)
	if err != nil {
		return false
	}
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// filesTurn takes the turn to change what the launchers on this PC share: the launcher's own program, the copy of
// Plutonium and the game files. When another launcher has the turn, filesTurn calls waits and then waits for that
// launcher. It returns once this launcher has the turn. release gives up the turn. A launcher that ends has given it
// up. A Windows mutex belongs to its thread, so one goroutine, locked to its thread, takes the mutex and releases
// it.
func filesTurn(waits func()) (release func()) {
	release, _ = takeFilesTurn(waits)
	return release
}

// tryFilesTurn takes the turn only when no other launcher has it now: it does not wait. ok is false when another
// launcher has it. The tray's menu takes it so to delete game files (menu.go).
func tryFilesTurn() (release func(), ok bool) { return takeFilesTurn(nil) }

// takeFilesTurn is filesTurn, which waits for the turn, and tryFilesTurn, which does not when waits is nil. When the
// turn can't be had from Windows at all, it goes on without it, as before there was a turn.
func takeFilesTurn(waits func()) (release func(), ok bool) {
	name, err := windows.UTF16PtrFromString(`Local\zombies-launcher-files`)
	if err != nil {
		return func() {}, true
	}
	const none, got, taken = 0, 1, 2
	result, done := make(chan int), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		h, _ := windows.CreateMutex(nil, false, name)
		if h == 0 {
			result <- none
			return
		}
		defer windows.CloseHandle(h)
		if e, _ := windows.WaitForSingleObject(h, 0); e == uint32(windows.WAIT_TIMEOUT) {
			if waits == nil {
				result <- taken
				return
			}
			waits()
			windows.WaitForSingleObject(h, windows.INFINITE)
		}
		result <- got
		<-done
		windows.ReleaseMutex(h)
	}()
	switch <-result {
	case none:
		return func() {}, true
	case taken:
		return nil, false
	}
	return func() { close(done) }, true
}

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	getConsoleWindow = kernel32.NewProc("GetConsoleWindow")
)

// windowless reports whether the program runs with no console and nothing in a console's place. That is so when a
// double click, a link or Windows' list of apps starts it, because it is built without a console of its own. It is
// not so when its output goes to a pipe or a file, as in its tests.
func windowless() bool {
	if w, _, _ := getConsoleWindow.Call(); w != 0 {
		return false
	}
	kind, err := windows.GetFileType(windows.Handle(os.Stdout.Fd()))
	return err != nil || kind != windows.FILE_TYPE_PIPE && kind != windows.FILE_TYPE_DISK
}

// box shows text in a Windows message box with an OK button, in front of the other windows. bad marks a message
// about something that went wrong. boxAsk asks a question in such a box, with Yes and No.
func box(text string, bad bool) {
	icon := uint32(windows.MB_ICONINFORMATION)
	if bad {
		icon = windows.MB_ICONWARNING
	}
	messageBox(text, windows.MB_OK|icon)
}

func boxAsk(question string) bool {
	return messageBox(question, windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2) == 6 // IDYES
}

func messageBox(text string, kind uint32) int32 {
	t, err1 := windows.UTF16PtrFromString(text)
	title, err2 := windows.UTF16PtrFromString(appName)
	if err1 != nil || err2 != nil {
		return 0
	}
	ret, _ := windows.MessageBox(0, t, title, kind|windows.MB_SETFOREGROUND|windows.MB_TOPMOST)
	return ret
}

// interactive reports whether someone is at the launcher's window to press Enter: its input is a console.
func interactive() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
}

// freeBytes returns the number of bytes that the disk of dir has free for this user.
func freeBytes(dir string) (uint64, bool) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, false
	}
	return free, true
}

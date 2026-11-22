//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The launcher's two dialogs of Windows' own, for a run with no window: the folder picker, in which the player shows
// the launcher a folder (pickFolder), and the task dialog, whose buttons say what each answer does (taskDialog). They
// are what Windows' own programs show, in the player's language. The launcher has no window to own them, so they
// show on their own.

var (
	ole32              = windows.NewLazySystemDLL("ole32.dll")
	coCreateInstance   = ole32.NewProc("CoCreateInstance")
	comctl32           = windows.NewLazySystemDLL("comctl32.dll")
	taskDialogIndirect = comctl32.NewProc("TaskDialogIndirect")
	setWindowPos       = user32.NewProc("SetWindowPos")
)

// The folder picker is a COM object: the File Open dialog, set to pick folders. Its methods are called by their
// place in its table of methods (IFileOpenDialog, and IShellItem for what it returns), as Windows' headers
// (ShObjIdl_core.h) list them.
var (
	clsidFileOpenDialog = windows.GUID{Data1: 0xdc1c5a9c, Data2: 0xe88a, Data3: 0x4dde,
		Data4: [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	iidFileOpenDialog = windows.GUID{Data1: 0xd57c7288, Data2: 0xd4ad, Data3: 0x4768,
		Data4: [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
)

const (
	comRelease = 2 // IUnknown: QueryInterface, AddRef, Release
	// IFileOpenDialog's methods: IModalWindow's Show, then IFileDialog's
	dialogShow, dialogSetOptions, dialogGetOptions, dialogSetTitle, dialogGetResult = 3, 9, 10, 17, 20
	itemGetDisplayName                                                              = 5 // IShellItem's

	clsctxInprocServer = 1
	// FOS_NOCHANGEDIR, FOS_PICKFOLDERS, FOS_FORCEFILESYSTEM and FOS_PATHMUSTEXIST: a folder that is on a disk
	fosFolders       = 0x8 | 0x20 | 0x40 | 0x800
	sigdnFileSysPath = 0x80058000
	errorCancelled   = 0x800704c7 // HRESULT_FROM_WIN32(ERROR_CANCELLED): the player closed the picker
)

// comObject is a COM object as Windows hands it out: a pointer to the table of its methods. A method is called with
// the object's own pointer first.
type comObject struct{ methods *[32]uintptr }

func (o *comObject) method(i int) uintptr { return o.methods[i] }

// hresult returns a COM call's result as an error.
func hresult(r uintptr) error { return fmt.Errorf("error 0x%08x", uint32(r)) }

// pickFolder shows Windows' folder picker, with title, and returns the folder that the player chose, or "" when they
// cancelled. It shows the picker on a thread of its own, which COM is set up on for a window (single-threaded). The
// goroutine keeps that thread to its end, so Go ends the thread with it: nothing of COM or of the picker is left on a
// thread that Go hands to other code.
func pickFolder(title string) (string, error) {
	type picked struct {
		dir string
		err error
	}
	done := make(chan picked, 1)
	go func() {
		runtime.LockOSThread() // not unlocked: the thread ends with the goroutine
		dir, err := showPicker(title)
		done <- picked{dir, err}
	}()
	p := <-done
	return p.dir, p.err
}

// showPicker is pickFolder on its thread. Each call that passes the address of a variable is a call of SyscallN
// itself, which keeps the variable where it is until the call returns.
func showPicker(title string) (string, error) {
	const sFalse = syscall.Errno(1) // COM was set up on the thread already
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && err != sFalse {
		return "", err
	}
	defer windows.CoUninitialize()
	var dialog *comObject
	if r, _, _ := syscall.SyscallN(coCreateInstance.Addr(), uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0,
		clsctxInprocServer, uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&dialog))); r != 0 {
		return "", hresult(r)
	}
	defer syscall.SyscallN(dialog.method(comRelease), uintptr(unsafe.Pointer(dialog)))
	var options uint32
	if r, _, _ := syscall.SyscallN(dialog.method(dialogGetOptions), uintptr(unsafe.Pointer(dialog)),
		uintptr(unsafe.Pointer(&options))); r != 0 {
		return "", hresult(r)
	}
	if r, _, _ := syscall.SyscallN(dialog.method(dialogSetOptions), uintptr(unsafe.Pointer(dialog)),
		uintptr(options|fosFolders)); r != 0 {
		return "", hresult(r)
	}
	if t, err := windows.UTF16PtrFromString(title); err == nil {
		syscall.SyscallN(dialog.method(dialogSetTitle), uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(t)))
	}
	switch r, _, _ := syscall.SyscallN(dialog.method(dialogShow), uintptr(unsafe.Pointer(dialog)), 0); {
	case r == errorCancelled:
		return "", nil
	case r != 0:
		return "", hresult(r)
	}
	var item *comObject
	if r, _, _ := syscall.SyscallN(dialog.method(dialogGetResult), uintptr(unsafe.Pointer(dialog)),
		uintptr(unsafe.Pointer(&item))); r != 0 {
		return "", hresult(r)
	}
	defer syscall.SyscallN(item.method(comRelease), uintptr(unsafe.Pointer(item)))
	var path *uint16
	if r, _, _ := syscall.SyscallN(item.method(itemGetDisplayName), uintptr(unsafe.Pointer(item)), sigdnFileSysPath,
		uintptr(unsafe.Pointer(&path))); r != 0 {
		return "", hresult(r)
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(path))
	return windows.UTF16PtrToString(path), nil
}

// taskDialogs returns the launcher's choose for a run with no window, which asks in a task dialog (taskDialog). It
// returns nil when Windows has no task dialog for the program. Only version 6 of comctl32.dll has one, and Windows
// gives a program that version only when the program's manifest asks for it. The release's build has that manifest
// (docker/Dockerfile: go-winres); a developer's build has none.
func taskDialogs() func(question string, answers []string) int {
	if taskDialogIndirect.Find() != nil {
		return nil
	}
	return taskDialog
}

const (
	tdfAllowCancel, tdfCommandLinks = 0x8, 0x10 // TDF_ALLOW_DIALOG_CANCELLATION, TDF_USE_COMMAND_LINKS
	tdcbfCancel                     = 0x8       // TDCBF_CANCEL_BUTTON
	tdnCreated                      = 0
	firstAnswer                     = 100 // the number of the first answer's button; Cancel's is 2 (IDCANCEL)
	hwndTopmost                     = ^uintptr(0)
	swpNoSize, swpNoMove            = 0x1, 0x2
)

// inFront is the task dialog's callback. As the dialog is made, it puts the dialog in front of the other windows, as
// MB_TOPMOST and MB_SETFOREGROUND do for the launcher's message boxes (box). Windows has a limited number of
// callbacks for a program, so the launcher makes this one once.
var inFront = sync.OnceValue(func() uintptr {
	return windows.NewCallback(func(wnd, msg, wParam, lParam, data uintptr) uintptr {
		if msg == tdnCreated {
			setWindowPos.Call(wnd, hwndTopmost, 0, 0, 0, 0, swpNoSize|swpNoMove)
			setForegroundWindow.Call(wnd)
		}
		return 0
	})
})

// taskDialog asks question in a task dialog, with a button for each of answers, and Cancel. The question's first
// paragraph is the dialog's heading, and the rest is its text. An answer with a second line shows as a big button
// with that line under it (a command link). taskDialog returns the number of the answer that the player chose, or -1
// when they cancelled or closed the dialog.
//
// The dialog's settings are a TASKDIALOGCONFIG, which Windows' header packs (CommCtrl.h: pshpack1.h): a pointer in
// it may be at any offset. So it is written into bytes at its fields' offsets, for 64-bit Windows. What it points to
// is Go's memory, pinned for as long as the dialog is open: the dialog's callback runs Go code on the same goroutine,
// whose stack may then move.
func taskDialog(question string, answers []string) int {
	heading, text, _ := strings.Cut(question, "\n\n")
	var pin runtime.Pinner
	defer pin.Unpin()
	str := func(s string) uint64 {
		u, err := windows.UTF16FromString(strings.ReplaceAll(s, "\x00", ""))
		if err != nil || s == "" {
			return 0
		}
		pin.Pin(&u[0])
		return uint64(uintptr(unsafe.Pointer(&u[0])))
	}
	buttons := make([]byte, 12*len(answers)) // TASKDIALOG_BUTTON, packed: its number, and its text
	for i, a := range answers {
		binary.LittleEndian.PutUint32(buttons[12*i:], uint32(firstAnswer+i))
		binary.LittleEndian.PutUint64(buttons[12*i+4:], str(a))
	}
	flags := uint32(tdfAllowCancel)
	if slices.ContainsFunc(answers, func(a string) bool { return strings.Contains(a, "\n") }) {
		flags |= tdfCommandLinks
	}
	config := new([160]byte)
	le := binary.LittleEndian
	le.PutUint32(config[0:], uint32(len(config))) // cbSize
	le.PutUint32(config[20:], flags)              // dwFlags
	le.PutUint32(config[24:], tdcbfCancel)        // dwCommonButtons
	le.PutUint64(config[28:], str(appName))       // pszWindowTitle
	le.PutUint64(config[44:], str(heading))       // pszMainInstruction
	le.PutUint64(config[52:], str(text))          // pszContent
	le.PutUint32(config[60:], uint32(len(answers)))
	if len(answers) > 0 {
		pin.Pin(&buttons[0])
		le.PutUint64(config[64:], uint64(uintptr(unsafe.Pointer(&buttons[0])))) // pButtons
	}
	le.PutUint32(config[72:], firstAnswer)        // nDefaultButton
	le.PutUint64(config[140:], uint64(inFront())) // pfCallback
	pin.Pin(config)
	button := new(int32)
	pin.Pin(button)
	r, _, _ := syscall.SyscallN(taskDialogIndirect.Addr(), uintptr(unsafe.Pointer(config)), uintptr(unsafe.Pointer(button)), 0, 0)
	if r != 0 {
		// No task dialog after all: ask about each answer in turn, in a message box.
		for i, a := range answers {
			label, _, _ := strings.Cut(a, "\n")
			if boxAsk(question + "\n\n" + strings.TrimSuffix(label, "…") + "?") {
				return i
			}
		}
		return -1
	}
	if i := int(*button) - firstAnswer; i >= 0 && i < len(answers) {
		return i
	}
	return -1
}
